package smppc

import (
	"context"
	"errors"
	"fmt"
	"net"
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

type defaultAMQPProvider struct{}

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
	topology := amqpcompat.NewTopology(conn)
	if err = topology.DeclareQueue(ctx, amqpcompat.ConnectorSubmitQueue(cid), "messaging", amqpcompat.ConnectorSubmitRoutingKey(cid)); err != nil {
		closeSetup()
		return AMQPDeliveryStream{}, err
	}
	consumer, err := amqpcompat.NewConsumer(conn)
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
		amqp:      &defaultAMQPProvider{},
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
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
	c.mu.Lock()
	c.session = nil
	c.status = StatusDisconnected
	c.mu.Unlock()
	return nil
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
			if !waitContext(ctx, seconds(c.Config().ConFailDelay)) {
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
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			sessionDone <- session.Run(sessionCtx)
		}()
		go func() {
			defer workers.Done()
			c.runConsumer(sessionCtx, session)
			close(consumerDone)
		}()

		select {
		case <-ctx.Done():
		case <-sessionDone:
		case <-consumerDone:
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
		if ctx.Err() != nil || !waitContext(ctx, seconds(c.Config().ConLossDelay)) {
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
			decision, decideErr := readiness.Decide(ReadinessInput{
				Now:        time.Now().UTC(),
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
				settleReject(delivery, false)
			}
		}
	}
}

func (c *Connector) connectAndBind(ctx context.Context) (*Session, error) {
	cfg := c.Config()
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
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
	request := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandBindTransceiver, SequenceNumber: bindSequence},
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
	if response.Header.CommandID != smppwire.CommandBindTransceiverResp ||
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
	c.mu.RUnlock()
	session := NewSessionWithDurability(conn, cfg, retry, c.readiness, c.decoder, transactions, nil)
	owned = false
	return session, nil
}
