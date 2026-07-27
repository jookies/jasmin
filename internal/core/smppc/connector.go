package smppc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

var (
	ErrBindResponse = errors.New("invalid SMPP bind_transceiver_resp")
)

type Status string

const (
	StatusDisconnected Status = "DISCONNECTED"
	StatusConnecting   Status = "CONNECTING"
	StatusBound        Status = "BOUND"
	StatusUnbinding    Status = "UNBINDING"
)

type AMQPProvider interface {
	Consume(ctx context.Context, amqpURL, cid string) (AMQPDeliveryStream, error)
}

// AMQPDeliveryStream separates delivery availability from consumer liveness.
// Done closes when the provider begins terminating this generation, before
// AMQP resource cleanup. It is a conservative local ownership fence; closure
// does not itself claim that the broker has completed requeue/redelivery.
type AMQPDeliveryStream struct {
	Deliveries <-chan *amqpcompat.Delivery
	Done       <-chan struct{}
}

type defaultAMQPProvider struct {
	prefetch int
	durable  bool
}

func dialAMQPTransport(
	ctx context.Context,
	timeout time.Duration,
	dialContext func(context.Context, string, string) (net.Conn, error),
	network, address string,
) (net.Conn, func() bool, error) {
	connection, err := dialContext(ctx, network, address)
	if err != nil {
		return nil, nil, err
	}
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = connection.Close()
		return nil, nil, err
	}
	stopContextClose := context.AfterFunc(ctx, func() { _ = connection.Close() })
	return connection, stopContextClose, nil
}

func (p *defaultAMQPProvider) Consume(ctx context.Context, amqpURL, cid string) (AMQPDeliveryStream, error) {
	const connectionTimeout = 30 * time.Second
	var rawConnection net.Conn
	var stopContextClose func() bool
	config := amqp091.Config{Dial: func(network, address string) (net.Conn, error) {
		dialer := net.Dialer{Timeout: connectionTimeout}
		connection, stop, err := dialAMQPTransport(ctx, connectionTimeout, dialer.DialContext, network, address)
		if err != nil {
			return nil, err
		}
		rawConnection = connection
		stopContextClose = stop
		return connection, nil
	}}
	conn, err := amqp091.DialConfig(amqpURL, config)
	if err != nil {
		if rawConnection != nil {
			_ = rawConnection.Close()
		}
		if conn != nil {
			_ = conn.Close()
		}
		if stopContextClose != nil {
			stopContextClose()
		}
		return AMQPDeliveryStream{}, err
	}
	closeSetup := func() {
		if rawConnection != nil {
			_ = rawConnection.Close()
		}
		_ = conn.Close()
		if stopContextClose != nil {
			stopContextClose()
		}
	}
	topology := amqpcompat.NewTopology(conn, p.durable)
	if err = topology.DeclareQueue(ctx, amqpcompat.ConnectorSubmitQueue(cid), "messaging", amqpcompat.ConnectorSubmitRoutingKey(cid)); err != nil {
		closeSetup()
		return AMQPDeliveryStream{}, err
	}
	prefetch := p.prefetch
	if prefetch < 1 {
		prefetch = 1
	}
	consumer, err := amqpcompat.NewConsumerWithPrefetch(conn, prefetch)
	if err != nil {
		closeSetup()
		return AMQPDeliveryStream{}, err
	}
	deliveries, err := consumer.Consume(ctx, amqpcompat.ConnectorSubmitQueue(cid))
	if err != nil {
		if rawConnection != nil {
			_ = rawConnection.Close()
		}
		_ = consumer.Close()
		_ = conn.Close()
		if stopContextClose != nil {
			stopContextClose()
		}
		return AMQPDeliveryStream{}, err
	}
	out := make(chan *amqpcompat.Delivery)
	done := make(chan struct{})
	go func() {
		defer func() {
			close(done)
			if rawConnection != nil {
				_ = rawConnection.Close()
			}
			_ = consumer.Close()
			_ = conn.Close()
			if stopContextClose != nil {
				stopContextClose()
			}
			close(out)
		}()
		for d := range deliveries {
			select {
			case out <- d:
			case <-ctx.Done():
				return
			}
		}
	}()
	return AMQPDeliveryStream{Deliveries: out, Done: done}, nil
}

type Connector struct {
	cfg          Config
	status       Status
	amqpURL      string
	amqp         AMQPProvider
	readiness    *ReadinessPolicy
	pacer        *Pacer
	decoder      SubmitDecoder
	transactions *submittransaction.Service
	mu           sync.RWMutex
	lifecycleMu  sync.Mutex

	session *Session
	cancel  context.CancelFunc
	running bool
	wg      sync.WaitGroup

	// auditLogger/auditPrivacy are passed to each session for the SMS-MT audit
	// line; nil (the default) leaves audit logging off.
	auditLogger  *slog.Logger
	auditPrivacy bool

	// deliverPublisher/deliverEncoder are passed to each session for
	// deliver_sm ingestion (MO + receipt publications); nil leaves inbound
	// PDUs acked-and-dropped like the legacy RouterPB-not-set branch.
	deliverPublisher DeliverPublisher
	deliverEncoder   DeliverEncoder
}

// SetSubmitAuditLogger sets the SMS-MT audit logger applied to every session this
// connector creates. A nil logger (the default) disables audit logging. Call
// before Start; a session already running is unaffected until it reconnects.
func (c *Connector) SetSubmitAuditLogger(logger *slog.Logger, privacy bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.auditLogger = logger
	c.auditPrivacy = privacy
}

// SetDeliverUpstream sets the deliver_sm ingress wiring (publisher + routable
// encoder) applied to every session this connector creates. Call before Start;
// a session already running is unaffected until it reconnects.
func (c *Connector) SetDeliverUpstream(publisher DeliverPublisher, encoder DeliverEncoder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deliverPublisher = publisher
	c.deliverEncoder = encoder
}

// logExpiredDiscard emits the legacy SM listener's expired-message discard line at
// INFO. The expiration renders as str(datetime) of the parsed header (space
// separator, microseconds only when present), matching the legacy log.
func (c *Connector) logExpiredDiscard(msgID string, expiration time.Time) {
	c.mu.RLock()
	logger := c.auditLogger
	c.mu.RUnlock()
	if logger == nil {
		return
	}
	logger.Info(fmt.Sprintf("Discarding expired message[%s]: expiration is %s",
		msgID, legacyDateTimeString(expiration)))
}

// legacyDateTimeString renders a time as Python's str(datetime): "YYYY-MM-DD
// HH:MM:SS", with 6-digit microseconds only when non-zero.
func legacyDateTimeString(value time.Time) string {
	value = value.Round(0)
	if value.Nanosecond() == 0 {
		return value.Format("2006-01-02 15:04:05")
	}
	return value.Format("2006-01-02 15:04:05.000000")
}

// NewConnector preserves the pre-decoder constructor for compatibility tests.
// Production composition must use NewConnectorWithDecoder.
func NewConnector(cfg Config, amqpURL string) (*Connector, error) {
	return newConnector(cfg, amqpURL, rawSubmitDecoder{})
}

func NewConnectorWithDecoder(cfg Config, amqpURL string, decoder SubmitDecoder) (*Connector, error) {
	if decoder == nil {
		return nil, errors.New("SubmitSM decoder is required")
	}
	return newConnector(cfg, amqpURL, decoder)
}

// ConfigureDurability is called by the production factory before Start.
func (c *Connector) ConfigureDurability(transactions *submittransaction.Service) error {
	if transactions == nil {
		return errors.New("submit transaction service is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return errors.New("cannot configure durability after connector start")
	}
	c.transactions = transactions
	return nil
}

func newConnector(cfg Config, amqpURL string, decoder SubmitDecoder) (*Connector, error) {
	clonedConfig := cfg.Clone()
	readiness, err := NewReadinessPolicy(DefaultReadinessConfig())
	if err != nil {
		return nil, err
	}
	pacer, err := NewPacer(clonedConfig.EffectiveSubmitSMThroughput())
	if err != nil {
		return nil, err
	}
	return &Connector{
		cfg:       clonedConfig,
		status:    StatusDisconnected,
		amqpURL:   amqpURL,
		amqp:      &defaultAMQPProvider{prefetch: clonedConfig.PrefetchCount, durable: clonedConfig.AMQPDurableTopology},
		readiness: readiness,
		pacer:     pacer,
		decoder:   decoder,
	}, nil
}

func (c *Connector) Config() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Clone()
}

func (c *Connector) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *Connector) Session() *Session {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.session
}

func (c *Connector) SetStatus(status Status) { c.setStatus(status) }

func (c *Connector) setStatus(status Status) {
	c.mu.Lock()
	c.status = status
	c.mu.Unlock()
}

func (c *Connector) Start() error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.running = true
	c.status = StatusConnecting
	c.wg.Add(1)
	go c.loop(ctx)
	return nil
}

func (c *Connector) Stop() error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.mu.Lock()
	cancel := c.cancel
	session := c.session
	bound := c.status == StatusBound && session != nil
	if bound {
		c.status = StatusUnbinding
	}
	c.cancel = nil
	cfg := c.cfg.Clone()
	c.mu.Unlock()
	var unbindErr error
	if bound {
		timeout := seconds(cfg.TrxTimeout)
		if timeout <= 0 {
			timeout = 250 * time.Millisecond
		}
		unbindCtx, stopUnbind := context.WithTimeout(context.Background(), timeout)
		unbindErr = session.Unbind(unbindCtx)
		stopUnbind()
	}
	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
	c.mu.Lock()
	c.session = nil
	c.status = StatusDisconnected
	c.mu.Unlock()
	if errors.Is(unbindErr, ErrSessionClosed) || errors.Is(unbindErr, context.Canceled) || errors.Is(unbindErr, context.DeadlineExceeded) {
		return nil
	}
	return unbindErr
}

func (c *Connector) loop(ctx context.Context) {
	defer func() {
		c.mu.Lock()
		c.session = nil
		c.cancel = nil
		c.running = false
		c.status = StatusDisconnected
		c.mu.Unlock()
		c.wg.Done()
	}()
	for {
		c.setStatus(StatusConnecting)
		session, err := c.connectAndBind(ctx)
		if err != nil {
			cfg := c.Config()
			if !cfg.ConnectionFailureRetryEnabled() || !waitContext(ctx, seconds(cfg.ConFailDelay)) {
				return
			}
			continue
		}

		c.mu.Lock()
		c.session = session
		c.status = StatusBound
		c.mu.Unlock()

		sessionCtx, cancel := context.WithCancel(ctx)
		sessionDone := make(chan error, 1)
		consumerDone := make(chan struct{})
		// The session reader (deliver_sm + responses) always runs. The submit-
		// queue consumer runs only for bind roles that may submit (transmitter,
		// transceiver); a receiver never consumes submits — the SMSC rejects a
		// submit on an RX bind, and MT routing already excludes it. For a
		// receiver, consumerWait stays nil so the select never triggers teardown
		// on a (non-existent) consumer exit; the session lives until ctx or the
		// socket closes.
		canSubmit := c.cfg.CanSubmit()
		var consumerWait <-chan struct{}
		var workers sync.WaitGroup
		workers.Add(1)
		go func() {
			defer workers.Done()
			sessionDone <- session.Run(sessionCtx)
		}()
		if canSubmit {
			consumerWait = consumerDone
			workers.Add(1)
			go func() {
				defer workers.Done()
				c.runConsumer(sessionCtx, session)
				close(consumerDone)
			}()
		}

		select {
		case <-ctx.Done():
		case <-sessionDone:
		case <-consumerWait:
		}
		cancel()
		_ = session.conn.Close()
		workers.Wait()

		c.mu.Lock()
		if c.session == session {
			c.session = nil
		}
		c.status = StatusDisconnected
		c.mu.Unlock()
		cfg := c.Config()
		if ctx.Err() != nil || !cfg.ConnectionLossRetryEnabled() || !waitContext(ctx, seconds(cfg.ConLossDelay)) {
			return
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *Connector) RunConsumer(ctx context.Context, session *Session) {
	c.runConsumer(ctx, session)
}

func (c *Connector) runConsumer(ctx context.Context, session *Session) {
	c.mu.RLock()
	amqpURL := c.amqpURL
	cid := c.cfg.CID
	readiness := c.readiness
	pacer := c.pacer
	provider := c.amqp
	c.mu.RUnlock()

	stream, err := provider.Consume(ctx, amqpURL, cid)
	if err != nil {
		return
	}
	if stream.Deliveries == nil {
		return
	}
	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	var generationMu sync.Mutex
	generationLost := false
	fenceGenerationLocked := func() {
		if generationLost {
			return
		}
		generationLost = true
		if session != nil {
			session.AbortConsumerGeneration()
		}
	}
	consumerLost := func() bool {
		generationMu.Lock()
		defer generationMu.Unlock()
		if !generationLost {
			select {
			case <-stream.Done:
				fenceGenerationLocked()
			default:
			}
		}
		return generationLost
	}
	settleReject := func(delivery *amqpcompat.Delivery, requeue bool) {
		generationMu.Lock()
		defer generationMu.Unlock()
		if !generationLost {
			select {
			case <-stream.Done:
				fenceGenerationLocked()
			default:
			}
		}
		if generationLost {
			_ = delivery.Abandon()
			return
		}
		if session != nil {
			session.settleDeliveryReject(delivery, requeue)
			return
		}
		_ = delivery.Reject(requeue)
	}
	livenessDone := make(chan struct{})
	go func() {
		defer close(livenessDone)
		select {
		case <-consumerCtx.Done():
		case <-stream.Done:
			generationMu.Lock()
			fenceGenerationLocked()
			generationMu.Unlock()
			cancelConsumer()
		}
	}()
	defer func() {
		cancelConsumer()
		<-livenessDone
	}()
	for {
		select {
		case <-consumerCtx.Done():
			// If broker loss and parent cancellation become ready together,
			// observe Done here rather than allowing the watcher select to
			// choose cancellation and skip the irreversible generation fence.
			_ = consumerLost()
			return
		case delivery, ok := <-stream.Deliveries:
			if !ok {
				// A closed delivery stream is itself terminal for this consumer
				// generation, even if the liveness watcher loses a select race
				// against the deferred consumer-context cancellation.
				generationMu.Lock()
				fenceGenerationLocked()
				generationMu.Unlock()
				return
			}
			if consumerLost() {
				return
			}
			// After legacy message-id extraction and deserialization, the listener
			// paces before type, expiry, connection, and bind-readiness checks.
			// The Go transport has already accepted an envelope at this boundary;
			// malformed pre-pacing pickle/property parity is intentionally outside
			// this slice. A later readiness discard still consumes the pacing slot.
			// Until pacing succeeds, the connector owns AMQP settlement; after
			// readiness succeeds, Session.Submit takes ownership on entry.
			if err := pacer.Wait(consumerCtx); err != nil {
				settleReject(delivery, true)
				return
			}
			if consumerLost() {
				return
			}
			createdAt, _ := delivery.Envelope().Properties().CreatedAt()
			expiration, _ := delivery.Envelope().Properties().Expiration()
			var expirationPtr *time.Time
			if !expiration.IsZero() {
				expirationPtr = &expiration
			}
			now := time.Now().UTC()
			decision, decideErr := readiness.Decide(ReadinessInput{
				Now:        now,
				CreatedAt:  createdAt,
				Expiration: expirationPtr,
				Connected:  true,
				Bound:      c.Status() == StatusBound,
			})
			if decideErr != nil {
				settleReject(delivery, true)
				continue
			}
			switch decision.Action {
			case ReadinessProceed:
				if session == nil {
					settleReject(delivery, true)
					continue
				}
				// Submit owns settlement even when it returns an encode/write error.
				_ = session.Submit(consumerCtx, delivery)
			case ReadinessRequeue:
				settleReject(delivery, true)
			case ReadinessDiscard:
				// The legacy logs an expired-message discard (the not-bound over-aged
				// discard, with its #N retry count, is deferred pending the Go retry
				// model). Reuse Decide's exact expiry check.
				if expirationPtr != nil && expirationPtr.Before(now) {
					c.logExpiredDiscard(delivery.Envelope().Properties().MessageID(), *expirationPtr)
				}
				settleReject(delivery, false)
			}
		}
	}
}

func dialSMPP(ctx context.Context, cfg Config) (net.Conn, error) {
	address := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if !cfg.TLSEnabled {
		return raw, nil
	}
	serverName := cfg.TLSServerName
	if serverName == "" {
		serverName = cfg.Host
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName, InsecureSkipVerify: cfg.TLSInsecureSkipVerify} // #nosec G402 -- explicit config opt-out
	if cfg.TLSCAFile != "" {
		pem, readErr := os.ReadFile(cfg.TLSCAFile)
		if readErr != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("read SMPP TLS CA: %w", readErr)
		}
		roots, rootErr := x509.SystemCertPool()
		if rootErr != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			_ = raw.Close()
			return nil, errors.New("SMPP TLS CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	secured := tls.Client(raw, tlsConfig)
	if err := secured.HandshakeContext(ctx); err != nil {
		_ = secured.Close()
		return nil, fmt.Errorf("SMPP TLS handshake: %w", err)
	}
	return secured, nil
}

// bindCommands maps a bind role to its SMPP bind request/response command ids.
func bindCommands(bind BindType) (request, response uint32) {
	switch bind {
	case BindTransmitter:
		return smppwire.CommandBindTransmitter, smppwire.CommandBindTransmitterResp
	case BindReceiver:
		return smppwire.CommandBindReceiver, smppwire.CommandBindReceiverResp
	default:
		return smppwire.CommandBindTransceiver, smppwire.CommandBindTransceiverResp
	}
}

func (c *Connector) connectAndBind(ctx context.Context) (*Session, error) {
	cfg := c.Config()
	conn, err := dialSMPP(ctx, cfg)
	if err != nil {
		return nil, err
	}
	owned := true
	stopContextClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer func() {
		stopContextClose()
		if owned {
			_ = conn.Close()
		}
	}()

	bindSequence := uint32(1)
	bindCommand, bindRespCommand := bindCommands(cfg.Bind)
	request := smppwire.PDU{
		Header: smppwire.Header{CommandID: bindCommand, SequenceNumber: bindSequence},
		Bind: &smppwire.BindBody{
			SystemID:         []byte(cfg.SystemID),
			Password:         []byte(cfg.Password),
			SystemType:       []byte(cfg.SystemType),
			InterfaceVersion: 0x34,
			AddressTON:       byte(cfg.AddrTON),
			AddressNPI:       byte(cfg.AddrNPI),
			AddressRange:     []byte(cfg.AddressRange),
		},
	}
	wire, err := smppwire.Encode(request)
	if err != nil {
		return nil, err
	}
	if cfg.TrxTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(seconds(cfg.TrxTimeout)))
	}
	if err = writeFrame(conn, wire); err != nil {
		return nil, err
	}
	response, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	if response.Header.CommandID != bindRespCommand ||
		response.Header.SequenceNumber != bindSequence || response.Header.CommandStatus != 0 {
		return nil, fmt.Errorf("%w: command=%#x status=%#x sequence=%d", ErrBindResponse,
			response.Header.CommandID, response.Header.CommandStatus, response.Header.SequenceNumber)
	}
	retry, err := NewErrorRetryPolicy(DefaultErrorRetryRules())
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	transactions := c.transactions
	auditLogger := c.auditLogger
	auditPrivacy := c.auditPrivacy
	deliverPublisher := c.deliverPublisher
	deliverEncoder := c.deliverEncoder
	c.mu.RUnlock()
	session := NewSessionWithDurability(conn, cfg, retry, c.readiness, c.decoder, transactions, nil)
	session.SetSubmitAuditLogger(auditLogger, auditPrivacy)
	session.SetDeliverUpstream(deliverPublisher, deliverEncoder)
	owned = false
	return session, nil
}
