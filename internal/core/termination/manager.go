package termination

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

var (
	ErrNotFound      = errors.New("termination connector not found")
	ErrAlreadyExists = errors.New("termination connector already exists")
	// ErrReserved reports an attempt to mutate a connector the configuration
	// file owns. Config and admin manage disjoint sets: an operator who removed
	// a config-declared connector through the API would see it return at the
	// next restart, which is worse than being told no.
	ErrReserved = errors.New("termination connector is config-owned")
)

// Status is what a termination connector is observably doing.
//
// It is deliberately not smppc.Status: there is no bind here, so BOUND would be
// a lie and DISCONNECTED would suggest a socket that does not exist. What
// matters for routing is whether this process is consuming the connector's
// submit queue.
type Status string

const (
	StatusStopped Status = "STOPPED"
	// StatusConnecting is set while the AMQP consumer is being established, and
	// again after it drops while the connector waits to retry.
	StatusConnecting Status = "CONNECTING"
	// StatusConsuming is the only status that makes a connector routable.
	StatusConsuming Status = "CONSUMING"
)

// AMQPProvider is the submit-queue consumption seam, reused from smppc rather
// than restated: both connector types consume the same queue with the same
// topology, so they share one interface and one production implementation
// (smppc.NewDefaultAMQPProvider). A fake written for either satisfies both.
type AMQPProvider = smppc.AMQPProvider

// AMQPDeliveryStream is smppc's delivery stream. Done closes when the provider
// begins tearing the generation down, before AMQP cleanup.
type AMQPDeliveryStream = smppc.AMQPDeliveryStream

// Consumer timings. They are connector-independent because they describe this
// process's relationship with the broker, not the partner's traffic.
const (
	// DefaultConsumerRetryDelay is the base wait before re-establishing a
	// dropped consumer. Jittered per attempt so a broker restart does not bring
	// every connector back in the same millisecond.
	DefaultConsumerRetryDelay = 5 * time.Second
	// DefaultProcessFailurePause bounds the redelivery loop after a failed
	// Process. A requeued message comes straight back, so without this pause a
	// permanently undecodable submit would spin the connector at full CPU.
	DefaultProcessFailurePause = time.Second
	// DefaultPrefetch is how many unsettled deliveries the broker may hand this
	// connector. One message is processed at a time; prefetch only pipelines the
	// fetch.
	DefaultPrefetch = 1
)

// ConnectorOptions carries the collaborators a connector needs beyond its
// configuration and its worker. Every field has a working default.
type ConnectorOptions struct {
	// AMQP defaults to smppc.NewDefaultAMQPProvider(Prefetch, Durable).
	AMQP AMQPProvider
	// Prefetch and Durable are used only when AMQP is nil.
	Prefetch int
	Durable  bool
	// Logger records consumer lifecycle. Nil disables it.
	Logger *slog.Logger
	// OnError is called for every failure that does not stop the connector: a
	// consumer that could not be established, a message that could not be
	// processed, a settlement the broker refused. Nil discards them, which is
	// how a connector silently stops carrying traffic, so production wiring
	// should always set it.
	OnError func(error)
	// RetryDelay and FailurePause default to the constants above.
	RetryDelay   time.Duration
	FailurePause time.Duration
	// jitter returns a value in [0,1). Injected by tests for a deterministic
	// retry delay.
	jitter func() float64
}

func (o ConnectorOptions) withDefaults() ConnectorOptions {
	if o.Prefetch < 1 {
		o.Prefetch = DefaultPrefetch
	}
	if o.AMQP == nil {
		o.AMQP = smppc.NewDefaultAMQPProvider(o.Prefetch, o.Durable)
	}
	if o.RetryDelay <= 0 {
		o.RetryDelay = DefaultConsumerRetryDelay
	}
	if o.FailurePause <= 0 {
		o.FailurePause = DefaultProcessFailurePause
	}
	if o.jitter == nil {
		o.jitter = rand.Float64
	}
	return o
}

// Connector consumes one termination connector's submit queue and hands each
// message to its Worker.
//
// The settlement contract is the whole point of this type: a delivery is
// acknowledged only after Worker.Process has returned nil, which means the spool
// row is committed. Anything else requeues. Acknowledging first would produce a
// message that was charged, was promised a receipt, and no longer exists
// anywhere — the exact failure the legacy queue tap avoided by ACKing only after
// a successful save.
type Connector struct {
	cfg     ConnectorConfig
	amqpURL string
	amqp    AMQPProvider
	worker  *Worker
	opts    ConnectorOptions

	mu      sync.RWMutex
	status  Status
	cancel  context.CancelFunc
	running bool
	wg      sync.WaitGroup
	// lifecycleMu serializes Start against Stop so a caller cannot observe a
	// half-started connector, mirroring smppc.Connector.
	lifecycleMu sync.Mutex
}

// NewConnector wires a connector. The worker is required: a connector without
// one would consume its queue and discard every message.
func NewConnector(cfg ConnectorConfig, amqpURL string, worker *Worker, opts ConnectorOptions) (*Connector, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if worker == nil {
		return nil, errors.New("termination: connector requires a worker")
	}
	if amqpURL == "" {
		return nil, errors.New("termination: connector requires an AMQP URL")
	}
	opts = opts.withDefaults()
	return &Connector{
		cfg:     cfg.WithDefaults(),
		amqpURL: amqpURL,
		amqp:    opts.AMQP,
		worker:  worker,
		opts:    opts,
		status:  StatusStopped,
	}, nil
}

// Config returns the connector's configuration. It is not redacted: callers that
// expose it to an operator must call ConnectorConfig.Redacted first, exactly as
// they must for an SMPP client connector's password.
func (c *Connector) Config() ConnectorConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *Connector) CID() string { return c.cfg.CID }

func (c *Connector) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

func (c *Connector) setStatus(status Status) {
	c.mu.Lock()
	c.status = status
	c.mu.Unlock()
}

// Start begins consuming. It returns once the consumer goroutine is launched,
// not once the broker is reached: a connector whose broker is down is
// CONNECTING, is not Available, and keeps retrying.
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
	c.log(slog.LevelInfo, "Started termination connector [%s]", c.cfg.CID)
	return nil
}

// Stop cancels the consumer and waits for it to settle whatever it was holding.
// It is idempotent.
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
	// Never hold c.mu across the wait: the consumer goroutine takes it.
	c.wg.Wait()
	c.mu.Lock()
	c.status = StatusStopped
	c.mu.Unlock()
	c.log(slog.LevelInfo, "Stopped termination connector [%s]", c.cfg.CID)
	return nil
}

func (c *Connector) loop(ctx context.Context) {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.cancel = nil
		c.status = StatusStopped
		c.mu.Unlock()
		c.wg.Done()
	}()
	for {
		c.setStatus(StatusConnecting)
		stream, err := c.amqp.Consume(ctx, c.amqpURL, c.cfg.CID)
		if err != nil {
			c.report(fmt.Errorf("termination: consume %s: %w", amqpcompat.ConnectorSubmitQueue(c.cfg.CID), err))
			if !waitFor(ctx, c.retryDelay()) {
				return
			}
			continue
		}
		if stream.Deliveries == nil {
			c.report(fmt.Errorf("termination: consume %s: no delivery stream", amqpcompat.ConnectorSubmitQueue(c.cfg.CID)))
			if !waitFor(ctx, c.retryDelay()) {
				return
			}
			continue
		}
		c.setStatus(StatusConsuming)
		c.log(slog.LevelInfo, "Consuming %s", amqpcompat.ConnectorSubmitQueue(c.cfg.CID))
		c.consume(ctx, stream)
		if ctx.Err() != nil {
			return
		}
		// The stream ended without this connector being stopped: the broker
		// connection dropped. Anything unsettled is the broker's to redeliver.
		c.log(slog.LevelInfo, "Consumer for %s ended; reconnecting", amqpcompat.ConnectorSubmitQueue(c.cfg.CID))
		if !waitFor(ctx, c.retryDelay()) {
			return
		}
	}
}

func (c *Connector) consume(ctx context.Context, stream AMQPDeliveryStream) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-stream.Done:
			return
		case delivery, ok := <-stream.Deliveries:
			if !ok {
				return
			}
			if !c.handle(ctx, delivery, stream.Done) {
				return
			}
		}
	}
}

// handle processes one delivery and settles it. It reports whether the consumer
// should continue.
func (c *Connector) handle(ctx context.Context, delivery *amqpcompat.Delivery, done <-chan struct{}) bool {
	if delivery == nil {
		return true
	}
	envelope := delivery.Envelope()
	meta := SubmitMetadataFromEnvelope(envelope.Properties())
	messageID := meta.QueueMessageID

	err := c.worker.Process(ctx, meta, envelope.Body())

	// Settling on a generation the provider has already torn down is a channel
	// error, and the broker is redelivering the message regardless. Abandon the
	// local handle instead of pretending either outcome reached the broker.
	select {
	case <-done:
		_ = delivery.Abandon()
		return false
	default:
	}

	if err == nil {
		// The spool row is committed. Only now may the queue forget the message.
		if ackErr := delivery.Ack(); ackErr != nil {
			c.report(fmt.Errorf("termination: ack %s on [%s]: %w", messageID, c.cfg.CID, ackErr))
		}
		return true
	}

	c.report(fmt.Errorf("termination: process %s on [%s]: %w", messageID, c.cfg.CID, err))

	if IsTerminal(err) {
		// The same message will fail the same way on every redelivery, so
		// requeueing it would starve the connector with one poison message
		// while the partner's other traffic waits behind it. Rejected without
		// requeue: the broker routes it to the queue's dead-letter exchange if
		// one is configured, and drops it otherwise. Either way it is off the
		// hot path and the failure was reported.
		if rejectErr := delivery.Reject(false); rejectErr != nil {
			c.report(fmt.Errorf("termination: dead-letter %s on [%s]: %w", messageID, c.cfg.CID, rejectErr))
		}
		return true
	}

	if rejectErr := delivery.Reject(true); rejectErr != nil {
		c.report(fmt.Errorf("termination: requeue %s on [%s]: %w", messageID, c.cfg.CID, rejectErr))
	}
	// A requeued message returns immediately. Pausing bounds the retry rate
	// while the transient cause — a spool outage, an unreachable broker for the
	// accept leg — clears. It is head-of-line blocking by design, because the
	// alternative for a message that was already charged is dropping it.
	return waitFor(ctx, c.opts.FailurePause)
}

// retryDelay jitters the base delay across [delay, 2*delay).
func (c *Connector) retryDelay() time.Duration {
	base := c.opts.RetryDelay
	return base + time.Duration(float64(base)*c.opts.jitter())
}

func (c *Connector) report(err error) {
	if err == nil {
		return
	}
	if c.opts.OnError != nil {
		c.opts.OnError(err)
	}
	c.log(slog.LevelError, "%v", err)
}

func (c *Connector) log(level slog.Level, format string, args ...any) {
	if c.opts.Logger == nil {
		return
	}
	c.opts.Logger.Log(context.Background(), level, fmt.Sprintf(format, args...))
}

// waitFor sleeps unless the context ends first. It reports whether the wait
// completed, so a caller can treat cancellation as "stop".
func waitFor(ctx context.Context, delay time.Duration) bool {
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

// ConnectorFactory builds a connector for one configuration. The gateway
// supplies it, because a termination worker needs collaborators the manager has
// no business knowing about: a submit decoder, a content decoder, an assembler,
// a verdict source, the spool and the SMSC leg.
type ConnectorFactory func(ConnectorConfig, string) (*Connector, error)

type managedConnector struct {
	connector *Connector
	desired   bool
}

// ManagedStatus is one connector's desired and observed state.
type ManagedStatus struct {
	CID      string
	Desired  bool
	Observed Status
	Config   ConnectorConfig
	// Reserved reports that the configuration file owns this connector, so the
	// admin plane may read but not mutate it.
	Reserved bool
}

// ManagerStats summarises the managed set for the operator surfaces.
type ManagerStats struct {
	Total      int
	Desired    int
	Stopped    int
	Connecting int
	Consuming  int
}

// Manager owns the termination connectors this process runs, keyed by cid.
//
// It mirrors smppc.Manager deliberately: the admin plane, the console and the
// routing availability check all treat the two connector types the same way, and
// a second lifecycle model would be a second set of bugs.
type Manager struct {
	amqpURL    string
	factory    ConnectorFactory
	connectors map[string]*managedConnector
	reserved   map[string]struct{}
	mu         sync.RWMutex
	opMu       sync.Mutex
	operations map[string]*sync.Mutex
}

// NewManager builds the manager. The factory is required: unlike an SMPP client
// connector there is no configuration-only default, because a termination
// connector cannot exist without the collaborators its worker needs.
func NewManager(amqpURL string, factory ConnectorFactory) (*Manager, error) {
	if amqpURL == "" {
		return nil, errors.New("termination: manager requires an AMQP URL")
	}
	if factory == nil {
		return nil, errors.New("termination: manager requires a connector factory")
	}
	return &Manager{
		amqpURL:    amqpURL,
		factory:    factory,
		connectors: make(map[string]*managedConnector),
		reserved:   make(map[string]struct{}),
		operations: make(map[string]*sync.Mutex),
	}, nil
}

// operationLock serializes every lifecycle transition for one cid without
// holding the manager map lock across a broker round-trip.
func (m *Manager) operationLock(cid string) func() {
	m.opMu.Lock()
	lock := m.operations[cid]
	if lock == nil {
		lock = &sync.Mutex{}
		m.operations[cid] = lock
	}
	m.opMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// Reserve marks cids as owned by the configuration file. Reserved connectors can
// be started, stopped and read, but not updated or removed — the same boundary
// the admin plane enforces for config-declared SMPP client connectors, enforced
// here as well so it holds no matter which surface calls in.
func (m *Manager) Reserve(cids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cid := range cids {
		m.reserved[cid] = struct{}{}
	}
}

// Reserved reports whether the configuration file owns this cid.
func (m *Manager) Reserved(cid string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, reserved := m.reserved[cid]
	return reserved
}

// ReservedCIDs lists the config-owned cids in stable order, for a management
// service that builds its own reserved set at construction.
func (m *Manager) ReservedCIDs() []string {
	m.mu.RLock()
	ids := make([]string, 0, len(m.reserved))
	for cid := range m.reserved {
		ids = append(ids, cid)
	}
	m.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

func (m *Manager) Add(cfg ConnectorConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	// Built before the map is locked so a factory failure cannot leave a
	// half-registered cid behind.
	connector, err := m.factory(cfg.WithDefaults(), m.amqpURL)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.connectors[cfg.CID]; exists {
		return ErrAlreadyExists
	}
	m.connectors[cfg.CID] = &managedConnector{connector: connector}
	return nil
}

// Remove drops a stopped connector. A running one is refused rather than
// stopped implicitly: removal while a message is in flight would settle nothing
// and the operator would not know which.
func (m *Manager) Remove(cid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, reserved := m.reserved[cid]; reserved {
		return fmt.Errorf("%w: %s", ErrReserved, cid)
	}
	entry, ok := m.connectors[cid]
	if !ok {
		return ErrNotFound
	}
	if entry.desired || entry.connector.Status() != StatusStopped {
		return fmt.Errorf("termination connector %q must be stopped before removal", cid)
	}
	delete(m.connectors, cid)
	return nil
}

func (m *Manager) Get(cid string) (*Connector, error) {
	m.mu.RLock()
	entry, ok := m.connectors[cid]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return entry.connector, nil
}

// List returns every managed configuration, sorted by cid. The delivery secret
// is present: callers exposing this must redact.
func (m *Manager) List() []ConnectorConfig {
	m.mu.RLock()
	list := make([]ConnectorConfig, 0, len(m.connectors))
	for _, entry := range m.connectors {
		list = append(list, entry.connector.Config())
	}
	m.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].CID < list[j].CID })
	return list
}

func (m *Manager) Start(cid string) error {
	unlock := m.operationLock(cid)
	defer unlock()
	return m.start(cid)
}

func (m *Manager) start(cid string) error {
	m.mu.Lock()
	entry, ok := m.connectors[cid]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	entry.desired = true
	connector := entry.connector
	m.mu.Unlock()
	if err := connector.Start(); err != nil {
		m.mu.Lock()
		if current := m.connectors[cid]; current == entry {
			current.desired = false
		}
		m.mu.Unlock()
		return err
	}
	return nil
}

func (m *Manager) Stop(cid string) error {
	unlock := m.operationLock(cid)
	defer unlock()
	return m.stop(cid)
}

func (m *Manager) stop(cid string) error {
	m.mu.Lock()
	entry, ok := m.connectors[cid]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	entry.desired = false
	connector := entry.connector
	m.mu.Unlock()
	// Stop drains an in-flight message. Never hold manager.mu across it.
	return connector.Stop()
}

// Update replaces a connector's configuration, preserving whether it was
// desired-started. The replacement is built first, so an invalid configuration
// cannot disturb the connector currently carrying traffic.
func (m *Manager) Update(cfg ConnectorConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if m.Reserved(cfg.CID) {
		return fmt.Errorf("%w: %s", ErrReserved, cfg.CID)
	}
	unlock := m.operationLock(cfg.CID)
	defer unlock()
	m.mu.RLock()
	old, ok := m.connectors[cfg.CID]
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	replacement, err := m.factory(cfg.WithDefaults(), m.amqpURL)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.connectors[cfg.CID] != old {
		m.mu.Unlock()
		return errors.New("termination connector changed concurrently")
	}
	desired := old.desired
	m.mu.Unlock()
	if err := old.connector.Stop(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.connectors[cfg.CID] != old {
		m.mu.Unlock()
		return errors.New("termination connector changed concurrently")
	}
	m.connectors[cfg.CID] = &managedConnector{connector: replacement, desired: desired}
	m.mu.Unlock()
	if desired {
		return replacement.Start()
	}
	return nil
}

func (m *Manager) Status(cid string) (ManagedStatus, error) {
	m.mu.RLock()
	entry, ok := m.connectors[cid]
	if !ok {
		m.mu.RUnlock()
		return ManagedStatus{}, ErrNotFound
	}
	desired, connector := entry.desired, entry.connector
	_, reserved := m.reserved[cid]
	m.mu.RUnlock()
	return ManagedStatus{
		CID:      cid,
		Desired:  desired,
		Observed: connector.Status(),
		Config:   connector.Config(),
		Reserved: reserved,
	}, nil
}

// Available reports whether MT routing may select this connector.
//
// It is the same contract smppc.Manager.Available implements: desired-started
// AND observably able to carry a message. A stopped termination connector drops
// out of route selection exactly as a stopped SMPP client connector does, and a
// connector whose broker consumer is down is not "nearly available" — routing
// must fail over instead of queueing behind a consumer that is not reading.
func (m *Manager) Available(cid string) bool {
	status, err := m.Status(cid)
	return err == nil && status.Desired && status.Observed == StatusConsuming
}

func (m *Manager) Stats() ManagerStats {
	m.mu.RLock()
	entries := make([]*managedConnector, 0, len(m.connectors))
	for _, entry := range m.connectors {
		entries = append(entries, entry)
	}
	m.mu.RUnlock()
	stats := ManagerStats{Total: len(entries)}
	for _, entry := range entries {
		if entry.desired {
			stats.Desired++
		}
		switch entry.connector.Status() {
		case StatusStopped:
			stats.Stopped++
		case StatusConnecting:
			stats.Connecting++
		case StatusConsuming:
			stats.Consuming++
		}
	}
	return stats
}

// StartAll starts every managed connector, in cid order, and joins the failures.
// One connector failing to start never prevents the others: a gateway that
// refuses to serve any partner because one downstream is misconfigured is a
// worse outage than the one it is avoiding.
func (m *Manager) StartAll() error {
	var errs []error
	for _, cid := range m.cids() {
		if err := m.Start(cid); err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", cid, err))
		}
	}
	return errors.Join(errs...)
}

// StopAll stops every managed connector in reverse cid order.
func (m *Manager) StopAll() error {
	ids := m.cids()
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var errs []error
	for _, cid := range ids {
		if err := m.Stop(cid); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", cid, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) cids() []string {
	m.mu.RLock()
	ids := make([]string, 0, len(m.connectors))
	for cid := range m.connectors {
		ids = append(ids, cid)
	}
	m.mu.RUnlock()
	sort.Strings(ids)
	return ids
}
