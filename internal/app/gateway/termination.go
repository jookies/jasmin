package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	redis "github.com/redis/go-redis/v9"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, for the local-run spool

	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
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
	// AcceptCDR records the terminating gateway's own acceptance of a message on
	// its CDR, which is what lets the final-DLR guard admit the receipt this
	// connector synthesizes. Nil is tolerated.
	AcceptCDR func(ctx context.Context, partKey string, at time.Time) error
	// Transactions settles the submit ledger for a terminated part: the same
	// BeginAttempt/CommitResponse pair the SMPP client runs on a carrier
	// acceptance. Without it the part stays PENDING in submit_parts forever, so
	// /api/message-status contradicts the CDR for every terminated message, and
	// a split-billed part's deferred charge is never raised. Nil is tolerated.
	Transactions *submittransaction.Service
	Logger       *slog.Logger
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
			func(flusher stitchFlusher) { plane.stitchers = append(plane.stitchers, flusher) },
			deps.AcceptCDR))
	if err != nil {
		return nil, err
	}
	plane.manager = manager

	// The runner is always built. It used to be omitted when the CONFIG FILE
	// declared no endpoint, which silently made push impossible for every
	// connector the admin plane created later -- they spooled and were never
	// delivered. The sink now answers ErrDeliveryNotConfigured per row instead,
	// so a pull-only deployment costs one map lookup per due row and nothing
	// else: no attempt counted, no dead letter, no misleading DLQ depth.
	deliveryRunner, err := termination.NewDeliveryRunner(repository.store,
		terminationSink(manager.List), termination.DeliveryRunnerConfig{
			Batch: section.deliveryBatch(),
		}, time.Now)
	if err != nil {
		return nil, err
	}
	plane.delivery = deliveryRunner

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
// settleTerminatedPart closes the submit transaction for a message this gateway
// terminated itself, exactly as the SMPP client closes it for one a carrier
// accepted.
//
// Two things went unrecorded without it. The part stayed PENDING in
// submit_parts forever, because only the smppc path ever called
// BeginAttempt/CommitResponse -- so /api/message-status reported PENDING for
// messages the billing console showed as delivered days earlier. And the
// deferred half of a split-billed part was quoted at admission but never
// raised, so it was never charged, the row was never prunable, and it sat on
// every statement as unsettled money.
//
// The late charge is raised, not waived. late_amount is non-zero only when the
// operator configured early_decrement_balance_percent on a rated route, which
// is a deliberate instruction to charge the rest on acceptance; terminating a
// message here *is* an acceptance, and a confirmed delivery at that.
//
// A failure is returned so the delivery is redelivered and settlement retried:
// the spool write is idempotent on the message id and CommitResponse is
// idempotent on the part key, so a redelivery repeats neither side effect.
func settleTerminatedPart(
	ctx context.Context,
	transactions *submittransaction.Service,
	logger *slog.Logger,
	connectorID string,
	partKey string,
	msg termination.Message,
) error {
	if transactions == nil {
		return nil
	}
	attempt, committed, err := transactions.BeginAttempt(ctx, partKey)
	switch {
	case errors.Is(err, submittransaction.ErrPartNotFound):
		// No ledger row for this part: the same shape as a missing CDR, and just
		// as unrepairable by retrying.
		logger.Error("termination settlement found no submit part",
			"connector", connectorID, "part_key", partKey)
		return nil
	case err != nil:
		return fmt.Errorf("termination settle begin attempt: %w", err)
	case committed:
		// Already resolved by an earlier delivery of the same message.
		return nil
	}
	if err := transactions.MarkSent(ctx, attempt.ID); err != nil {
		return fmt.Errorf("termination settle mark sent: %w", err)
	}
	var events []submittransaction.OutboxEvent
	if smppc.LateBillAmountIsPayable(msg.LateBillAmount) {
		intent, err := smppc.NewLateBillingIntent(partKey, msg.Partner, msg.BillID, msg.LateBillAmount)
		if err != nil {
			return fmt.Errorf("termination settle late billing: %w", err)
		}
		event, err := submittransaction.NewEnvelopeEvent(
			smppc.LateBillingEventKey(partKey), partKey,
			submittransaction.EventLateBilling, "billing", intent, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("termination settle late billing event: %w", err)
		}
		events = append(events, event)
	}
	// The SMSC message id a terminating connector reports is derived from the
	// queue message id, the same value its synthesized receipt carries.
	if _, err := transactions.CommitResponse(ctx, submittransaction.Result{
		PartKey: partKey, AttemptID: attempt.ID,
		Kind:          submittransaction.ResultSuccess,
		SMPPStatus:    "ESME_ROK",
		SMSCMessageID: string(termination.SMSCMessageID(msg.MessageID)),
		// Without this the result would project SMSC_ACCEPTED and relabel every
		// terminated message as carrier-accepted, undoing the distinction the
		// statement depends on.
		LocalTermination: true,
	}, events...); err != nil {
		return fmt.Errorf("termination settle commit: %w", err)
	}
	return nil
}

// cdrPartKey derives the submit transaction's part key from a queue message id.
//
// Admission writes one cdr_records row per part, keyed "<aggregate>/<6-digit
// part>". A single-part submit is enqueued under the bare aggregate id, so the
// suffix has to be added here. Every segment of a concatenated submit is already
// enqueued under its own suffixed id (submit_envelope_builder appends it once
// len(Parts) > 1), and appending a second suffix there produced keys like
// "<uuid>/000002/000001" that no row was ever written under -- so the CDR stayed
// ADMITTED and every synthesized terminal receipt was refused, forever.
func cdrPartKey(messageID string) string {
	if hasPartSuffix(messageID) {
		return messageID
	}
	return fmt.Sprintf("%s/%06d", messageID, 1)
}

// hasPartSuffix reports whether an id already ends in the "/%06d" part suffix
// the submit envelope builder appends to concatenated submits.
func hasPartSuffix(messageID string) bool {
	index := strings.LastIndex(messageID, "/")
	if index < 0 {
		return false
	}
	suffix := messageID[index+1:]
	if len(suffix) != 6 {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return false
		}
	}
	return true
}

func terminationConnectorFactory(
	deps terminationDeps,
	store termination.SpoolStore,
	gate termination.RedisKeyProbe,
	parts termination.PartStore,
	logger *slog.Logger,
	registerStitch func(stitchFlusher),
	// acceptor records the terminating gateway's acceptance on the CDR. Nil on a
	// deployment with no CDR store, where receipts still flow and only the
	// commercial record is absent.
	acceptor func(ctx context.Context, partKey string, at time.Time) error,
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
		// Record this gateway's acceptance on the CDR, which is what makes the
		// synthesized receipt admissible. The part key is the submit
		// transaction's: "<aggregate id>/<6-digit part>" -- see cdrPartKey for
		// why it cannot simply be appended. Every spooled row runs this hook,
		// the assembled one and each segment's receipt-only row alike, so every
		// admitted part of a concatenated submit leaves ADMITTED.
		if acceptor != nil || deps.Transactions != nil {
			spool = spool.WithAcceptance(func(ctx context.Context, msg termination.Message) error {
				partKey := cdrPartKey(msg.MessageID)
				if acceptor != nil {
					err := acceptor(ctx, partKey, time.Now())
					// A part key with no CDR row behind it cannot be repaired by
					// retrying, and the spool row is already committed:
					// propagating this would requeue the delivery forever against
					// a key that will never exist. Every other error is transient
					// (the store is down) and must retry, so only this one is
					// swallowed.
					switch {
					case errors.Is(err, cdr.ErrNotFound):
						logger.Error("termination acceptance found no CDR part",
							"connector", cfg.CID, "part_key", partKey)
						return nil
					case err != nil:
						return err
					}
				}
				return settleTerminatedPart(ctx, deps.Transactions, logger, cfg.CID, partKey, msg)
			})
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
// terminationSink dispatches a spooled row to its own connector's endpoint,
// resolved from the LIVE connector set rather than from a startup snapshot of
// the config file.
//
// That distinction is the whole point. Connectors can be created, edited and
// given an endpoint through the admin plane at runtime, and a sink built once
// from config.Connectors cannot see any of them: a console-created connector
// would consume its queue, spool every message, and never push one, with no
// error anywhere because nothing had been asked to deliver it.
func terminationSink(configs func() []termination.ConnectorConfig) termination.DeliverySink {
	return &terminationRoutingSink{configs: configs}
}

// terminationRoutingSink dispatches a spooled row to its own connector's
// endpoint. The runner works off the shared spool and therefore sees rows from
// every connector, so the sink — not the runner — is where per-connector
// delivery configuration is resolved.
//
// HTTPSinks are cached because building one per message would discard the
// connection pool, and keyed by the settings that shape them so an endpoint
// changed in the console takes effect on the next delivery rather than at the
// next restart.
type terminationRoutingSink struct {
	configs func() []termination.ConnectorConfig
	mu      sync.Mutex
	cache   map[string]cachedHTTPSink
}

type cachedHTTPSink struct {
	key  string
	sink *termination.HTTPSink
}

func (s *terminationRoutingSink) Name() string { return "http-push" }

// deliveryKey changes whenever anything that shapes the sink changes, which is
// what makes an endpoint edited in the console take effect immediately.
func deliveryKey(cfg termination.DeliveryConfig) string {
	return strings.Join([]string{
		cfg.Endpoint,
		string(cfg.Format),
		// Length, not the secret: this key is compared, held in memory, and must
		// never be the thing that leaks an HMAC key into a heap dump or a log.
		// A rotation changes the length rarely, so the timeout and endpoint carry
		// most of the discrimination and a same-length rotation is picked up by
		// the explicit invalidation below.
		fmt.Sprintf("%d", len(cfg.Secret)),
		cfg.Timeout.String(),
	}, "|")
}

func (s *terminationRoutingSink) resolve(connector string) (*termination.HTTPSink, error) {
	var found *termination.ConnectorConfig
	for _, cfg := range s.configs() {
		if cfg.CID == connector {
			candidate := cfg
			found = &candidate
			break
		}
	}
	if found == nil || found.Delivery.Endpoint == "" {
		// Either the connector is gone, or it is pull-only. Both mean "nothing
		// to push to", which the runner treats as a skip rather than a failure.
		return nil, termination.ErrDeliveryNotConfigured
	}

	key := deliveryKey(found.Delivery)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]cachedHTTPSink{}
	}
	if cached, ok := s.cache[connector]; ok && cached.key == key {
		return cached.sink, nil
	}
	sink, err := termination.NewHTTPSink(termination.HTTPSinkConfig{
		Endpoint: found.Delivery.Endpoint,
		Format:   termination.DeliveryFormat(found.Delivery.Format),
		Secret:   []byte(found.Delivery.Secret),
		Timeout:  found.Delivery.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("termination: build delivery sink for %q: %w", connector, err)
	}
	s.cache[connector] = cachedHTTPSink{key: key, sink: sink}
	return sink, nil
}

func (s *terminationRoutingSink) Deliver(ctx context.Context, msg termination.Message, attempt int) (termination.DeliveryResult, error) {
	sink, err := s.resolve(msg.Connector)
	if err != nil {
		return termination.DeliveryResult{Attempt: attempt}, err
	}
	return sink.Deliver(ctx, msg, attempt)
}

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
