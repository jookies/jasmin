package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	redis "github.com/redis/go-redis/v9"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, for the local-run spool

	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/core/termination"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
)

// terminationPlane is everything a gateway process needs to run MT termination
// connectors: the connector manager, the shared message spool, and the two
// runners that drain it.
//
// The runners are per process rather than per connector because the spool is
// shared: a delivery or a receipt is owed by a committed row, not by the
// connector that happened to write it, so a message survives its connector
// being stopped, updated or removed.
type terminationPlane struct {
	manager *termination.Manager
	service *msgspool.Service
	// consumers is the scoped pull API: credential CRUD for the admin plane and
	// the authenticated read for the downstream application. It exists whenever
	// the spool does, because a pull-only connector has no other way to deliver
	// and a push connector's application may still want to reconcile.
	consumers *msgspool.ConsumerService
	pull      *msgspool.PullHandler
	delivery  *termination.DeliveryRunner
	receipt   *termination.ReceiptRunner
	// owner identifies this process in a receipt claim. Two processes must never
	// emit the same receipt.
	owner string

	// stitchers are the assemblers whose plain-split window has to be swept.
	// UDH and SAR groups complete when their last segment lands; a stitched group
	// is released only by a timer, so without this loop those messages are held
	// until the process exits.
	stitchers []stitchFlusher

	logger *slog.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup

	deliveryInterval time.Duration
	receiptInterval  time.Duration
	pruneInterval    time.Duration
	censusInterval   time.Duration
	// censusSeen remembers every connector the census has ever reported, so a
	// connector whose rows all prune away is zeroed instead of freezing at its
	// last non-zero depth. A stuck dead-letter gauge is indistinguishable from a
	// real backlog, and the alarm on it would never clear.
	censusSeen map[string]struct{}

	closers   []func() error
	closeOnce sync.Once
	closeErr  error
}

// terminationDeps are the collaborators the plane borrows from the rest of the
// gateway. All are required.
type terminationDeps struct {
	// Config is the whole gateway configuration: the plane needs the outbound
	// DSN and broker as well as its own section.
	Config Config
	// Publisher emits the synthesized submit_sm_resp and receipt legs. It is the
	// outbound runtime's confirmed publisher, so a termination receipt travels
	// the identical path a carrier receipt does.
	Publisher termination.EnvelopePublisher
	// Submits decodes the queued submit body — the same codec the SMPP client
	// connector uses, because both consume the identical queue payload.
	Submits picklecompat.Codec
	Logger  *slog.Logger
}

// newTerminationPlane builds the plane. It returns nil (and no error) when the
// configuration declares no termination section at all, so every call site can
// treat "not configured" the same way it treats a disabled worker.
func newTerminationPlane(ctx context.Context, deps terminationDeps) (_ *terminationPlane, resultErr error) {
	section := deps.Config.TerminationConnectors
	if section == nil {
		return nil, nil
	}
	if deps.Publisher == nil || deps.Submits == nil {
		return nil, errors.New("termination: publisher and submit decoder are required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	plane := &terminationPlane{
		logger:           logger,
		deliveryInterval: section.deliveryInterval(),
		receiptInterval:  section.receiptInterval(),
		pruneInterval:    section.pruneInterval(),
		censusInterval:   defaultSpoolCensusInterval,
		censusSeen:       map[string]struct{}{},
	}
	defer func() {
		if resultErr != nil {
			plane.closeResources()
		}
	}()

	repository, err := openMessageSpool(ctx, deps.Config)
	if err != nil {
		return nil, err
	}
	plane.closers = append(plane.closers, repository.close)

	service, err := msgspool.NewService(repository.store, section.retention(), time.Now)
	if err != nil {
		return nil, fmt.Errorf("build message spool service: %w", err)
	}
	plane.service = service

	consumers, err := msgspool.NewConsumerService(service, repository.consumers, time.Now)
	if err != nil {
		return nil, fmt.Errorf("build message spool consumer service: %w", err)
	}
	plane.consumers = consumers
	pull, err := msgspool.NewPullHandler(consumers)
	if err != nil {
		return nil, fmt.Errorf("build message pull handler: %w", err)
	}
	plane.pull = pull

	// The activation gate reads a keyspace this platform does not own, so the
	// client is opened here rather than reusing the DLR store's typed wrapper.
	// It is opened even when no connector uses redis-window today: an admin
	// plane can add one at runtime, and a connector that cannot reach its gate
	// fails open on every message, which is exactly the outcome that must not
	// depend on a restart.
	var gate termination.RedisKeyProbe
	// parts holds the segments of a concatenated submit until the last one
	// arrives. It is the same keyspace and the same 300 s TTL the inbound MO path
	// uses, so partial messages in both directions live in one place. Without
	// Redis there is nowhere to hold them and the connector refuses concatenated
	// traffic instead of silently spooling fragments.
	var parts termination.PartStore
	if url := deps.Config.ResolvedRedisURL(); url != "" {
		options, parseErr := redis.ParseURL(url)
		if parseErr != nil {
			return nil, fmt.Errorf("termination redis_url: %w", parseErr)
		}
		client := redis.NewClient(options)
		plane.closers = append(plane.closers, client.Close)
		gate = client
		parts = multipartStore{rediscompat.NewClient(client)}
	}

	manager, err := termination.NewManager(deps.Config.Outbound.AMQPURL,
		terminationConnectorFactory(deps, repository.store, gate, parts, logger,
			func(flusher stitchFlusher) { plane.stitchers = append(plane.stitchers, flusher) }))
	if err != nil {
		return nil, err
	}
	plane.manager = manager

	// No connector pushes anywhere: this is the pull-only deployment, and its
	// rows stay pending for their whole retention. Running the delivery loop
	// against it would count attempts nobody made and dead-letter every row
	// once the retry budget ran out, which is exactly what makes a DLQ depth
	// metric useless.
	if sink := terminationSink(section); sink != nil {
		deliveryRunner, err := termination.NewDeliveryRunner(repository.store, sink, termination.DeliveryRunnerConfig{
			Batch: section.deliveryBatch(),
		}, time.Now)
		if err != nil {
			return nil, err
		}
		plane.delivery = deliveryRunner
	}

	// A random per-process owner: two gateways on one host, or a pid reused
	// after a crash, must never look like the same claimant.
	owner, err := outbound.NewProcessOwner("gateway-receipt-")
	if err != nil {
		return nil, fmt.Errorf("create termination receipt owner: %w", err)
	}
	plane.owner = owner
	// The receipt runner builds one leg per connector id from the row it claims,
	// so no single "which cid owns the receipts" choice is made here.
	if err != nil {
		return nil, err
	}
	receiptRunner, err := termination.NewReceiptRunner(repository.store, deps.Publisher, termination.ReceiptRunnerConfig{
		Owner: owner,
		Lease: section.receiptLease(),
		Batch: section.receiptBatch(),
	}, time.Now)
	if err != nil {
		return nil, err
	}
	plane.receipt = receiptRunner

	for _, connector := range section.Connectors {
		if err := manager.Add(connector); err != nil {
			return nil, fmt.Errorf("add termination connector %q: %w", connector.CID, err)
		}
	}
	// Config owns these cids: the admin plane may start and stop them, not edit
	// or delete them.
	manager.Reserve(deps.Config.TerminationCIDs()...)
	return plane, nil
}

// terminationConnectorFactory builds one connector's worker chain. The manager
// calls it for config connectors at boot and for admin-provisioned connectors
// later, so everything a connector needs is captured here rather than wired
// once at startup.
func terminationConnectorFactory(
	deps terminationDeps,
	store termination.SpoolStore,
	gate termination.RedisKeyProbe,
	parts termination.PartStore,
	logger *slog.Logger,
	registerStitch func(stitchFlusher),
) termination.ConnectorFactory {
	section := deps.Config.TerminationConnectors
	content := termination.NewDefaultMsgContentDecoder()
	return func(cfg termination.ConnectorConfig, amqpURL string) (*termination.Connector, error) {
		verdicts, err := termination.NewVerdictSource(cfg.Verdict, termination.Dependencies{Redis: gate})
		if err != nil {
			return nil, fmt.Errorf("termination connector %q verdict source: %w", cfg.CID, err)
		}
		drain := &deferredStitchDrain{}
		assembler, err := terminationAssembler(cfg, parts, content, drain)
		if err != nil {
			return nil, err
		}
		// The spool is built per connector, so "does this connector push?" is
		// answered by its own endpoint: a pull-only connector's rows are never
		// scheduled for a delivery that no sink would make.
		pushes := cfg.Delivery.Endpoint != ""
		spool, err := termination.NewSpool(store, time.Now, func(string) bool { return pushes })
		if err != nil {
			return nil, err
		}
		leg, err := termination.NewSMSCLeg(deps.Publisher, cfg.CID, time.Now)
		if err != nil {
			return nil, err
		}
		worker, err := termination.NewWorker(
			termination.WorkerConfig{
				CID:           cfg.CID,
				ReceiptDelay:  cfg.ReceiptDelay,
				ReceiptJitter: cfg.ReceiptJitter,
			},
			deps.Submits,
			content,
			assembler,
			verdicts,
			spool,
			leg,
			time.Now,
			nil,
		)
		if err != nil {
			return nil, err
		}
		// Closes the loop opened above: from here a message the stitch releases on
		// a timer runs the same decide-announce-spool tail as one that completed
		// on an arriving segment.
		drain.worker.Store(worker)
		if runner, ok := assembler.(*termination.MultipartAssembler); ok && cfg.Stitch.Enabled() && registerStitch != nil {
			registerStitch(stitchFlusher{cid: cfg.CID, assembler: runner})
		}
		return termination.NewConnector(cfg, amqpURL, worker, termination.ConnectorOptions{
			Prefetch: section.prefetch(),
			Durable:  deps.Config.Outbound.AMQPDurableTopology,
			Logger:   logger,
			OnError: func(err error) {
				// A connector that stops carrying traffic without saying so is
				// the failure this callback exists to prevent.
				logger.Error("termination connector failed", "connector", cfg.CID, "error", err.Error())
			},
		})
	}
}

// terminationAssembler chooses how a connector joins a concatenated submit.
//
// stitchFlusher is one connector's stitch buffer plus the id it reports under.
type stitchFlusher struct {
	cid       string
	assembler *termination.MultipartAssembler
}

// deferredStitchDrain resolves the circular dependency between the assembler and
// the worker: the assembler needs somewhere to hand a stitched message, and that
// somewhere is the worker, which needs the assembler to be built first.
//
// The pointer is set immediately after the worker exists, so the only window in
// which Complete can find nothing is before the connector starts consuming. It
// returns an error rather than dropping the message, because a stitched group is
// already settled on the queue — nothing redelivers it.
type deferredStitchDrain struct {
	worker atomic.Pointer[termination.Worker]
}

func (d *deferredStitchDrain) Complete(ctx context.Context, msg termination.Message) error {
	worker := d.worker.Load()
	if worker == nil {
		return errors.New("termination: stitch drain used before the worker was wired")
	}
	return worker.Complete(ctx, msg)
}

// With a part store it reassembles UDH-declared segments into one message. With
// no Redis configured there is nowhere to hold a partial message, so the
// connector keeps refusing concatenated traffic — visibly, in the dead-letter
// queue — rather than spooling, announcing and delivering each fragment as if it
// were a whole message.
func terminationAssembler(
	cfg termination.ConnectorConfig,
	parts termination.PartStore,
	content termination.ContentDecoder,
	drain termination.StitchDrain,
) (termination.Assembler, error) {
	if parts == nil {
		if cfg.Stitch.Enabled() {
			return nil, fmt.Errorf(
				"termination connector %q enables the plain-split stitch but no redis_url is configured to hold partial messages", cfg.CID)
		}
		return termination.PassThroughAssembler{}, nil
	}
	return termination.NewMultipartAssembler(
		termination.AssemblerConfig{CID: cfg.CID, Stitch: cfg.Stitch},
		parts,
		content,
		drain,
		time.Now,
	)
}

// terminationSink builds the downstream delivery sink shared by every
// connector, or nil when no connector has an endpoint.
//
// A connector without an endpoint is pull-only: its rows are spooled and
// receipted, and nothing pushes them.
func terminationSink(section *TerminationConfig) termination.DeliverySink {
	endpoints := make(map[string]*termination.HTTPSink, len(section.Connectors))
	for _, connector := range section.Connectors {
		if connector.Delivery.Endpoint == "" {
			continue
		}
		sink, err := termination.NewHTTPSink(termination.HTTPSinkConfig{
			Endpoint: connector.Delivery.Endpoint,
			Format:   termination.DeliveryFormat(connector.Delivery.Format),
			Secret:   []byte(connector.Delivery.Secret),
			Timeout:  connector.Delivery.Timeout,
		})
		if err != nil {
			// Unreachable: ValidateConfig has already accepted the endpoint.
			continue
		}
		endpoints[connector.CID] = sink
	}
	if len(endpoints) == 0 {
		return nil
	}
	return &terminationRoutingSink{endpoints: endpoints}
}

// terminationRoutingSink dispatches a spooled row to its own connector's
// endpoint. The runner works off the shared spool and therefore sees rows from
// every connector, so the sink — not the runner — is where per-connector
// delivery configuration is resolved.
type terminationRoutingSink struct {
	endpoints map[string]*termination.HTTPSink
}

func (s *terminationRoutingSink) Name() string { return "http-push" }

func (s *terminationRoutingSink) Deliver(ctx context.Context, msg termination.Message, attempt int) (termination.DeliveryResult, error) {
	sink, ok := s.endpoints[msg.Connector]
	if !ok {
		// This connector is pull-only while others on this gateway push.
		// Reporting success would claim a push that never happened, so the row
		// is failed as retryable: it is rescheduled with backoff and eventually
		// dead-lettered with its content intact, where the console can replay it
		// once the connector gains an endpoint.
		//
		// It is the one place the shared runner and per-connector delivery
		// configuration do not meet: Spool.Record schedules a first attempt for
		// every row regardless of whether the connector has a sink, so a
		// sink-less connector cannot leave its rows untouched unless no
		// connector on this gateway pushes at all.
		return termination.DeliveryResult{Attempt: attempt}, errTerminationPullOnly
	}
	return sink.Deliver(ctx, msg, attempt)
}

// errTerminationPullOnly is retryable-by-default (it is not a *DeliveryError),
// so the runner reschedules the row instead of dead-lettering it on the first
// attempt: a connector can gain an endpoint through the admin plane, and its
// spooled rows should then be pushed.
var errTerminationPullOnly = errors.New("termination: connector has no delivery endpoint")

// spoolRepository is the opened spool plus the closer for whatever it opened.
type spoolRepository struct {
	store msgspool.Repository
	// consumers is the scoped pull-credential store, on the same handle as the
	// spool: authenticating a poll and answering it are one request, and putting
	// them on separate pools would let a consumer authenticate against a
	// database its read cannot reach.
	consumers msgspool.ConsumerRepository
	close     func() error
}

// openMessageSpool selects the spool backend the same way the rest of the
// gateway's durable state is selected: PostgreSQL when the outbound DSN is
// configured (which the gateway requires), SQLite otherwise for a local run.
func openMessageSpool(ctx context.Context, config Config) (spoolRepository, error) {
	if dsn := config.Outbound.PostgresDSN; dsn != "" {
		store, err := storage.OpenPostgresMessageSpool(ctx, dsn)
		if err != nil {
			return spoolRepository{}, fmt.Errorf("open message spool: %w", err)
		}
		if err := store.Migrate(ctx); err != nil {
			_ = store.Close()
			return spoolRepository{}, fmt.Errorf("migrate message spool: %w", err)
		}
		return spoolRepository{store: store, consumers: store.Consumers(), close: store.Close}, nil
	}
	path := config.TerminationConnectors.SpoolDBPath
	if path == "" {
		return spoolRepository{}, errors.New(
			"termination_connectors requires outbound.postgres_dsn or termination_connectors.spool_db_path")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return spoolRepository{}, fmt.Errorf("open message spool %q: %w", path, err)
	}
	// One writer: SQLite serialises them anyway, and the runners plus every
	// connector write concurrently.
	db.SetMaxOpenConns(1)
	store, err := storage.NewSQLiteMessageSpool(db)
	if err != nil {
		_ = db.Close()
		return spoolRepository{}, err
	}
	if err := store.Init(ctx); err != nil {
		_ = db.Close()
		return spoolRepository{}, fmt.Errorf("init message spool: %w", err)
	}
	return spoolRepository{store: store, consumers: store.Consumers(), close: db.Close}, nil
}

// Start launches the connectors and the three loops. The context bounds the
// loops; Close stops them and waits.
func (p *terminationPlane) Start() error {
	if p == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	if p.delivery != nil {
		p.wg.Add(1)
		go p.loop(ctx, "termination delivery", p.deliveryInterval, func(ctx context.Context) error {
			_, err := p.delivery.RunOnce(ctx)
			return err
		})
	}
	p.wg.Add(3)
	go p.loop(ctx, "termination receipt", p.receiptInterval, func(ctx context.Context) error {
		_, err := p.receipt.RunOnce(ctx)
		return err
	})
	go p.loop(ctx, "message spool retention", p.pruneInterval, p.prune)
	go p.loop(ctx, "message spool census", p.censusInterval, p.observeSpool)
	// One sweep per connector that enables the stitch. Without it a message with
	// no sibling waits in the buffer forever: it was settled on the queue when it
	// arrived, so nothing redelivers it, and the partner would receive no receipt.
	for _, flusher := range p.stitchers {
		p.wg.Add(1)
		go func(flusher stitchFlusher) {
			defer p.wg.Done()
			flusher.assembler.Run(ctx, func(err error) {
				p.logger.Error("termination stitch flush failed", "connector", flusher.cid, "error", err.Error())
			})
		}(flusher)
	}
	return p.manager.StartAll()
}

// defaultSpoolCensusInterval is how often the spool gauges are re-counted.
//
// Ten seconds against a 5-7 s receipt delay: an overdue receipt must be visible
// within a scrape or two of becoming overdue, or "partners are waiting" is
// discovered by the partner rather than by the gauge. The query is three counts
// over one grouped index scan, so the cost is a rounding error next to the
// delivery loop already running beside it.
const defaultSpoolCensusInterval = 10 * time.Second

// observeSpool re-counts the spool and publishes the three operator gauges.
//
// These are populations, so they are polled rather than counted at a change:
// dead-letter depth falls when a row is replayed OR when retention prunes it,
// and receipts-owed falls when a receipt is emitted OR when the row ages out.
// Deriving them from events would mean every one of those paths remembering to
// decrement, and the one that forgot would leave a gauge that only ever rises.
func (p *terminationPlane) observeSpool(ctx context.Context) error {
	census, err := p.service.Census(ctx)
	if err != nil {
		return err
	}
	present := make(map[string]struct{}, len(census))
	for _, entry := range census {
		present[entry.ConnectorID] = struct{}{}
		p.censusSeen[entry.ConnectorID] = struct{}{}
		stats.DefaultPrometheus().SetTerminationSpool(entry.ConnectorID, stats.TerminationSpoolCensus{
			Rows:            entry.Rows,
			DeadLettered:    entry.DeadLettered,
			ReceiptsOverdue: entry.ReceiptsOverdue,
		})
	}
	for connector := range p.censusSeen {
		if _, ok := present[connector]; !ok {
			stats.DefaultPrometheus().SetTerminationSpool(connector, stats.TerminationSpoolCensus{})
		}
	}
	return nil
}

// loop runs one pass per tick. A pass that fails is logged and retried on the
// next tick: neither runner holds state between passes, because everything they
// act on is a committed spool row.
func (p *terminationPlane) loop(ctx context.Context, name string, interval time.Duration, once func(context.Context) error) {
	defer p.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := once(ctx); err != nil && ctx.Err() == nil {
				p.logger.Error(name+" failed", "error", err.Error())
			}
		}
	}
}

// prune enforces the retention window, draining batches the way the CDR
// maintenance loop does. The actor is the gateway itself and the deletion is
// audited by the spool service.
func (p *terminationPlane) prune(ctx context.Context) error {
	principal := msgspool.Principal{
		Subject: "gateway-maintenance",
		Roles:   []msgspool.Role{msgspool.RoleOperator},
	}
	batch := int64(p.service.Retention().BatchSize)
	for {
		result, err := p.service.Prune(ctx, principal)
		if err != nil {
			if errors.Is(err, msgspool.ErrDisabled) {
				return nil
			}
			return err
		}
		if result.Records < batch {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// Manager exposes the connector manager for routing availability and for the
// management surfaces.
func (p *terminationPlane) Manager() *termination.Manager {
	if p == nil {
		return nil
	}
	return p.manager
}

// Spool exposes the audited read/prune boundary. Management surfaces must use
// this, never the repository: it is where "every read of message content writes
// an audit row" is enforced.
func (p *terminationPlane) Spool() *msgspool.Service {
	if p == nil {
		return nil
	}
	return p.service
}

// Consumers exposes the scoped pull credentials for the admin plane. Nil when
// the gateway runs no termination connectors at all, which is what makes the
// management surfaces answer 404 rather than an empty list.
func (p *terminationPlane) Consumers() *msgspool.ConsumerService {
	if p == nil {
		return nil
	}
	return p.consumers
}

// PullHandler exposes the message pull endpoint for mounting on the REST
// listener. It returns a nil http.Handler — not a typed nil inside a non-nil
// interface — when there is no plane, so restcompat's "is this deployment
// serving pulled messages" check answers correctly.
func (p *terminationPlane) PullHandler() http.Handler {
	if p == nil || p.pull == nil {
		return nil
	}
	return p.pull
}

// Close stops the connectors, then the runners, then the stores.
//
// The order matters: connectors first so nothing new is spooled, runners next so
// a delivery or receipt in flight finishes, stores last. It must run before the
// outbound runtime closes, because the receipt runner publishes through the
// outbound publisher.
func (p *terminationPlane) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		var errs []error
		if p.manager != nil {
			if err := p.manager.StopAll(); err != nil {
				errs = append(errs, err)
			}
		}
		if p.cancel != nil {
			p.cancel()
		}
		p.wg.Wait()
		errs = append(errs, p.closeResources())
		p.closeErr = errors.Join(errs...)
	})
	return p.closeErr
}

func (p *terminationPlane) closeResources() error {
	var errs []error
	for index := len(p.closers) - 1; index >= 0; index-- {
		if err := p.closers[index](); err != nil {
			errs = append(errs, err)
		}
	}
	p.closers = nil
	return errors.Join(errs...)
}

// availabilityGate answers "may routing select this connector" across both MT
// connector types.
//
// The termination manager is installed after the outbound runtime is built —
// it needs that runtime's publisher — so the pointer is atomic rather than a
// plain field: the submit path may already be reading it.
type availabilityGate struct {
	smppc       func(string) bool
	termination atomic.Pointer[termination.Manager]
}

func (g *availabilityGate) install(manager *termination.Manager) {
	if manager != nil {
		g.termination.Store(manager)
	}
}

// Available reports whether a connector can carry an MT message now. A stopped
// connector of either kind drops out of route selection identically.
func (g *availabilityGate) Available(cid string) bool {
	if g.smppc != nil && g.smppc(cid) {
		return true
	}
	if manager := g.termination.Load(); manager != nil {
		return manager.Available(cid)
	}
	return false
}

// terminationStatusFunc reports a config-owned termination connector's live
// status for the console.
//
// It returns nil when the gateway has no termination plane, so the console can
// tell "not enabled here" from "enabled and idle" — the same distinction the
// nil service makes on the CRUD side.
func terminationStatusFunc(plane *terminationPlane) func(string) (termination.ManagedStatus, error) {
	if plane == nil {
		return nil
	}
	manager := plane.Manager()
	if manager == nil {
		return nil
	}
	return manager.Status
}
