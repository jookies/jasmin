package outbound

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/billing"
	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/core/interceptor"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
	"github.com/pumpitspace/synevyr/internal/core/segmentation"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/httpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
	"github.com/pumpitspace/synevyr/internal/transport/restcompat"
)

// Runtime owns the production outbound composition and all long-lived
// resources used by the HTTP → routing → billing → RabbitMQ path.
type Runtime struct {
	Handler     http.Handler
	RESTHandler http.Handler

	submitter           core.Submitter
	directory           *runtimeDirectory
	publisher           *amqpcompat.Publisher
	bridge              picklecompat.Codec
	connection          *amqp.Connection
	billing             *lateBillingConsumer
	outboxCancel        context.CancelFunc
	outboxWG            sync.WaitGroup
	ownedBridge         bool
	ownedStore          *storage.PostgresSubmitTransactionRepository
	ownedRESTBatchStore *storage.PostgresRESTBatchStore
	restClose           func()

	// Durable billing quotas: the persister flushes mutated balances and
	// submit_sm_count quotas so a restart cannot refund what a customer spent.
	// ownedQuotaStore is set only when this runtime opened the store and must
	// therefore close it; an injected store belongs to the caller.
	quotaPersister    *billing.QuotaPersister
	quotaStore        billing.QuotaStore
	ownedQuotaStore   *storage.PostgresQuotaStore
	quotaCancel       context.CancelFunc
	quotaWG           sync.WaitGroup
	quotaFlushOnStop  time.Duration
	cdrService        *cdr.Service
	cdrCancel         context.CancelFunc
	cdrWG             sync.WaitGroup
	maintenanceTicker atomic.Pointer[time.Ticker]

	// Live routing: the submit path selects through routes (atomic); admin
	// route provisioning rebuilds config + admin routes and swaps it. mu
	// serialises rebuilds so concurrent admin calls can't interleave.
	routes          *routingtable.AtomicTable
	configRoutes    []RouteConfig
	configUsernames []string
	resolveUID      uidResolver
	configGroupIDs  []string
	httpStats       *stats.HTTPStats
	routesMu        sync.Mutex
	// Live MT interception: the submit path runs mtInterceptors (atomic);
	// admin interceptor provisioning rebuilds config + admin entries and swaps
	// it, serialised by interceptorsMu.
	mtInterceptors       *interceptor.AtomicTable
	configMTInterceptors []InterceptorConfig
	interceptorsMu       sync.Mutex
}

type RuntimeDependencies struct {
	Bridge             picklecompat.Codec
	Transactions       *submittransaction.Service
	Repository         submittransaction.Repository
	ConnectorAvailable func(string) bool

	// Observability (optional). When supplied by the gateway, /metrics renders
	// the smppc/smpps counters and per-connector labels alongside httpapi.
	SMPPcStats   *stats.SMPPcRegistry
	SMPPsStats   *stats.SMPPsStats
	ConnectorIDs func() []string

	// DLRLookupPID names the DLRLookup queue (DLRLookup-<pid>) the response path
	// publishes dlr.submit_sm_resp to. It is declared and bound here regardless
	// of whether the in-process DLRLookup worker runs, so the mandatory publish
	// is always routable; empty falls back to the legacy default "main".
	DLRLookupPID string

	// ConnectorPDUDefaults resolves a routed connector's default submit_sm PDU
	// params (TON/NPI, service_type, ...) for the front-door submit (GAP 4).
	ConnectorPDUDefaults func(connectorID string) (smppc.PDUDefaults, bool)

	// DLRRequestStore, when supplied, persists dlr:<msgid> for httpapi submits
	// so terminal (level-2/3) receipts correlate back. ConnectorDLRExpiry gives
	// the record TTL per routed connector (dlr_expiry).
	DLRRequestStore    core.DLRRequestStore
	ConnectorDLRExpiry func(connectorID string) int64

	// InterceptorRunner runs MT interception scripts. Required when the config
	// declares mt_interceptors; nil otherwise (interception is a no-op).
	InterceptorRunner interceptor.Runner

	// QuotaStore is the durable home of prepaid balances and submit_sm_count
	// quotas. When nil the runtime opens and migrates its own small PostgreSQL
	// pool from config.PostgresDSN — the same database the submit outbox already
	// requires, so this adds no deployment surface.
	QuotaStore billing.QuotaStore

	// QuotaPersistErrors, when set, receives durable-quota flush failures. The
	// worker keeps running and retries on the next tick either way; without a
	// sink the failures are silent, matching how the outbox dispatcher behaves.
	QuotaPersistErrors func(error)

	// RESTBatchStore is the durable /secure/sendbatch task and callback queue.
	// Gateway production wiring supplies PostgreSQL. Tests that omit it use an
	// in-memory store; standalone NewRuntime opens PostgreSQL itself.
	RESTBatchStore restcompat.BatchStore
	RESTConfig     restcompat.Config
	// MessagePull mounts the scoped, audited message pull endpoint on the REST
	// listener. It is a resolver rather than a handler because the message spool
	// that backs it is built after this runtime — it needs this runtime's
	// publisher — so anything captured here would always be nil. Nil leaves the
	// path unregistered.
	MessagePull func() http.Handler

	// Named component loggers are built once by the gateway so file rotation is
	// not split across multiple writers for the same legacy log_file.
	RouterLogger     *slog.Logger
	HTTPLogger       *slog.Logger
	HTTPAccessLogger *slog.Logger
	// CDRAlert observes every periodic reconciliation report. Nonzero issue
	// counts are production alerts and are never auto-corrected.
	CDRAlert func(cdr.ReconciliationReport)
}

// NewRuntime is the standalone production composition. It never falls back to
// SQLite: PostgreSQL is opened, migrated and recovered before AMQP workers.
func NewRuntime(ctx context.Context, config Config) (*Runtime, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if err := validateStandaloneConfig(config); err != nil {
		return nil, err
	}
	repository, err := storage.OpenPostgresSubmitTransactionRepository(ctx, config.PostgresDSN)
	if err != nil {
		return nil, err
	}
	if err = repository.Migrate(ctx); err != nil {
		_ = repository.Close()
		return nil, fmt.Errorf("migrate submit transaction store: %w", err)
	}
	transactions, err := submittransaction.NewProductionService(repository, nil)
	if err != nil {
		_ = repository.Close()
		return nil, err
	}
	if _, err = transactions.Recover(ctx); err != nil {
		_ = repository.Close()
		return nil, fmt.Errorf("recover submit attempts: %w", err)
	}
	restBatchStore, err := storage.OpenPostgresRESTBatchStore(ctx, config.PostgresDSN)
	if err != nil {
		_ = repository.Close()
		return nil, fmt.Errorf("open REST batch store: %w", err)
	}
	if err = restBatchStore.Migrate(ctx); err != nil {
		_ = restBatchStore.Close()
		_ = repository.Close()
		return nil, err
	}
	// The native Go codec is the only pickle path; the Python bridge subprocess
	// it replaced has been removed along with the rest of the Python surface.
	bridge := picklecompat.NewNativeCodec()
	runtime, err := NewRuntimeWithDependencies(ctx, config, RuntimeDependencies{
		Bridge: bridge, Transactions: transactions, Repository: repository,
		RESTBatchStore: restBatchStore,
	})
	if err != nil {
		_ = bridge.Close()
		_ = restBatchStore.Close()
		_ = repository.Close()
		return nil, err
	}
	runtime.ownedBridge = true
	runtime.ownedStore = repository
	runtime.ownedRESTBatchStore = restBatchStore
	return runtime, nil
}

func NewRuntimeWithDependencies(ctx context.Context, config Config, dependencies RuntimeDependencies) (_ *Runtime, resultErr error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if err := dependencies.RESTConfig.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRuntimeConfig, err)
	}
	if dependencies.Bridge == nil || dependencies.Transactions == nil || dependencies.Repository == nil {
		return nil, fmt.Errorf("%w: bridge, transactions and PostgreSQL repository are required", ErrInvalidRuntimeConfig)
	}
	if _, err := submittransaction.RequireProductionRepository(dependencies.Repository); err != nil {
		return nil, err
	}
	restBatchStore := dependencies.RESTBatchStore
	var ownedRESTBatchStore *storage.PostgresRESTBatchStore
	if restBatchStore == nil {
		opened, err := storage.OpenPostgresRESTBatchStore(ctx, config.PostgresDSN)
		if err != nil {
			return nil, fmt.Errorf("open REST batch store: %w", err)
		}
		if err = opened.Migrate(ctx); err != nil {
			_ = opened.Close()
			return nil, err
		}
		ownedRESTBatchStore = opened
		restBatchStore = opened
	}
	defer func() {
		if resultErr != nil && ownedRESTBatchStore != nil {
			_ = ownedRESTBatchStore.Close()
		}
	}()
	// Durable quotas are resolved before anything else is built: the directory
	// provisions each user's balance exactly once, and it has to provision the
	// restored value rather than the spec value.
	quotaStore := dependencies.QuotaStore
	var ownedQuotaStore *storage.PostgresQuotaStore
	if quotaStore == nil {
		opened, err := storage.OpenPostgresQuotaStore(ctx, config.PostgresDSN)
		if err != nil {
			return nil, fmt.Errorf("open billing quota store: %w", err)
		}
		if err = opened.Migrate(ctx); err != nil {
			_ = opened.Close()
			return nil, err
		}
		ownedQuotaStore = opened
		quotaStore = opened
	}
	defer func() {
		if resultErr != nil && ownedQuotaStore != nil {
			_ = ownedQuotaStore.Close()
		}
	}()
	restoredQuotas, err := billing.LoadQuotaIndex(ctx, quotaStore)
	if err != nil {
		return nil, err
	}
	directory, err := newRuntimeDirectoryWithQuotas(config, restoredQuotas)
	if err != nil {
		return nil, err
	}
	routes, connectorIDs, defaultRate, err := buildRoutes(config.Routes, directory.resolveUID, directory.lookupGroupID)
	if err != nil {
		return nil, err
	}
	directory.defaultRate = defaultRate
	atomicRoutes := routingtable.NewAtomicTable(routes)
	// Rate quotes read the live table, not the boot snapshot, so a route added
	// through the admin plane prices immediately.
	directory.routes = atomicRoutes
	// A table with no default route is legal, but every submit that matches no
	// filter is then refused at the front door. Say so once at startup rather than
	// letting an operator discover it from a customer's failed send.
	if !routes.HasDefaultRoute() && dependencies.RouterLogger != nil {
		dependencies.RouterLogger.Warn("no default MT route configured (order 0); " +
			"a submit matching no filter will be refused with \"no route matched\"")
	}
	cdrRepository, ok := dependencies.Repository.(cdr.OperationsRepository)
	if !ok {
		return nil, fmt.Errorf("PostgreSQL repository does not implement CDR operations")
	}
	cdrService, err := cdr.NewService(cdrRepository, cdr.RetentionPolicy{
		Days: config.CDRRetentionDays, BatchSize: resolvedCDRRetentionBatch(config),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("create CDR operations service: %w", err)
	}

	connection, err := amqp.Dial(config.AMQPURL)
	if err != nil {
		return nil, fmt.Errorf("connect RabbitMQ: %w", err)
	}
	defer func() {
		if resultErr != nil {
			_ = connection.Close()
		}
	}()
	topology := amqpcompat.NewTopology(connection, config.AMQPDurableTopology)
	if err := topology.Declare(ctx); err != nil {
		return nil, fmt.Errorf("declare RabbitMQ exchanges: %w", err)
	}
	for _, connectorID := range connectorIDs {
		queue := amqpcompat.ConnectorSubmitQueue(connectorID)
		if err := topology.DeclareQueue(ctx, queue, "messaging", amqpcompat.ConnectorSubmitRoutingKey(connectorID)); err != nil {
			return nil, fmt.Errorf("declare connector %q queue: %w", connectorID, err)
		}
	}
	// Always declare the DLRLookup queue so the response path's mandatory
	// dlr.submit_sm_resp publish is routable even when the in-process DLRLookup
	// worker is not enabled (the legacy broker topology is fixed; DLRLookup is a
	// separate consumer). Idempotent with OpenDLRLookupSubscription.
	dlrLookupPID := dependencies.DLRLookupPID
	if dlrLookupPID == "" {
		dlrLookupPID = "main"
	}
	if err := topology.DeclareQueue(ctx, amqpcompat.DLRLookupQueue(dlrLookupPID), "messaging", amqpcompat.DLRLookupRoutingKey); err != nil {
		return nil, fmt.Errorf("declare DLRLookup queue: %w", err)
	}
	// Same rationale for the RouterPB deliver queue: the connectors' MO ingress
	// (deliver.sm.<cid>) publishes mandatory, so the queue+binding must exist
	// even before the in-process MO router consumer attaches — the legacy
	// RouterPB declares it on its own boot. Idempotent with the router's
	// OpenRouterSubscriptions.
	if err := topology.DeclareQueue(ctx, amqpcompat.RouterDeliverSMQueue, "messaging", amqpcompat.RouterDeliverSMRoutingKey); err != nil {
		return nil, fmt.Errorf("declare RouterPB deliver queue: %w", err)
	}
	publisher, err := amqpcompat.NewPublisher(connection)
	if err != nil {
		return nil, fmt.Errorf("create RabbitMQ publisher: %w", err)
	}
	defer func() {
		if resultErr != nil {
			_ = publisher.Close()
		}
	}()
	envelopeBuilder, err := NewSubmitEnvelopeBuilder(dependencies.Bridge)
	if err != nil {
		return nil, err
	}
	interceptorTable, err := buildInterceptorTable(config.MTInterceptors, directory.resolveUID, directory.lookupGroupID)
	if err != nil {
		return nil, err
	}
	if len(config.MTInterceptors) > 0 && dependencies.InterceptorRunner == nil {
		return nil, fmt.Errorf("%w: mt_interceptors configured without an interceptor runner", ErrInvalidRuntimeConfig)
	}
	atomicInterceptors := interceptor.NewAtomicTable(interceptorTable)
	submitService, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:     atomicInterceptors,
		InterceptorRunner:    dependencies.InterceptorRunner,
		RoutingTable:         atomicRoutes,
		BillingUsers:         directory.users,
		EnvelopeBuilder:      envelopeBuilder,
		Transaction:          dependencies.Transactions,
		SelectConnector:      connectorSelector(dependencies.ConnectorAvailable),
		ConnectorPDUDefaults: dependencies.ConnectorPDUDefaults,
		LongContentSplit:     segmentation.SplitMethod(config.LongContentSplit),
		LongContentMaxParts:  config.LongContentMaxParts,

		LongContentRejectOverMax: config.LongContentRejectOverMax,
		GroupIdentity:            directory.groupIdentity,
		CDRCurrency:              resolvedCDRCurrency(config),
		DLRRequestStore:          dependencies.DLRRequestStore,
		ConnectorDLRExpiry:       dependencies.ConnectorDLRExpiry,
		Throughput:               newThroughputGate(directory),
		Logger:                   dependencies.RouterLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("create submit service: %w", err)
	}
	lateBilling, err := core.NewLateBillingService(directory.users)
	if err != nil {
		return nil, fmt.Errorf("create late billing service: %w", err)
	}
	billingRepository, ok := dependencies.Repository.(billingApplicationRepository)
	if !ok {
		return nil, fmt.Errorf("PostgreSQL repository does not implement durable billing application ledger")
	}
	durableLateBilling, err := newDurableLateBillingProcessor(lateBilling, billingRepository)
	if err != nil {
		return nil, err
	}
	billingConsumer, err := newLateBillingConsumer(ctx, connection, durableLateBilling, config.AMQPDurableTopology)
	if err != nil {
		return nil, fmt.Errorf("start late billing consumer: %w", err)
	}
	defer func() {
		if resultErr != nil {
			_ = billingConsumer.Close()
		}
	}()

	// One HTTP stats registry, shared by /metrics and the jCli `stats` command:
	// two surfaces reporting the same counters must not be able to drift.
	httpStats := &stats.HTTPStats{}
	legacyHTTPHandler := httpcompat.NewHandler(httpcompat.Dependencies{
		Authenticator: directory,
		Credentials:   directory,
		BalanceReader: directory,
		RateReader:    directory,
		Submitter:     submitService,
		HTTPStats:     httpStats,
		SMPPcStats:    dependencies.SMPPcStats,
		SMPPsStats:    dependencies.SMPPsStats,
		ConnectorIDs:  dependencies.ConnectorIDs,
		Logger:        dependencies.HTTPLogger,
		AccessLogger:  dependencies.HTTPAccessLogger,
	})
	restOptions := []restcompat.Option{restcompat.WithBatchContext(ctx)}
	if dependencies.MessagePull != nil {
		restOptions = append(restOptions, restcompat.WithMessagePull(dependencies.MessagePull))
	}
	restOptions = append(restOptions, dependencies.RESTConfig.Options(restBatchStore)...)
	restHandlers, err := restcompat.NewHandlers(legacyHTTPHandler, restOptions...)
	if err != nil {
		return nil, fmt.Errorf("start REST batch dispatcher: %w", err)
	}
	defer func() {
		if resultErr != nil {
			restHandlers.Close()
		}
	}()
	outboxOwner, err := newOutboxOwner()
	if err != nil {
		return nil, fmt.Errorf("create submit outbox owner: %w", err)
	}
	dispatcher, err := submittransaction.NewDispatcher(dependencies.Repository, publisher, outboxOwner, 32, 30*time.Second, nil)
	if err != nil {
		return nil, fmt.Errorf("create submit outbox dispatcher: %w", err)
	}
	quotaPersister, err := billing.NewQuotaPersister(quotaStore, config.quotaPersistInterval(), directory.quotaPrincipals, dependencies.QuotaPersistErrors)
	if err != nil {
		return nil, fmt.Errorf("create billing quota persister: %w", err)
	}
	// Persist a late charge's balance as soon as it is applied instead of
	// waiting for the next tick, which is how long a crash could lose it. It
	// goes through FlushOnce so it serialises with the periodic flush and keeps
	// its generation guard, rather than becoming a second writer that could
	// put a stale balance over a newer one.
	durableLateBilling.SetQuotaFlusher(func(flushCtx context.Context) error {
		_, flushErr := quotaPersister.FlushOnce(flushCtx)
		return flushErr
	})
	outboxCtx, outboxCancel := context.WithCancel(ctx)
	quotaCtx, quotaCancel := context.WithCancel(ctx)
	cdrCtx, cdrCancel := context.WithCancel(ctx)
	runtime := &Runtime{
		Handler:             restHandlers.Combined,
		RESTHandler:         restHandlers.Daemon,
		restClose:           restHandlers.Close,
		ownedRESTBatchStore: ownedRESTBatchStore,
		submitter:           submitService,
		directory:           directory,
		publisher:           publisher,
		bridge:              dependencies.Bridge,
		connection:          connection,
		billing:             billingConsumer,
		outboxCancel:        outboxCancel,
		routes:              atomicRoutes,
		configRoutes:        append([]RouteConfig(nil), config.Routes...),
		configUsernames:     configUsernames(config.Users),
		configGroupIDs:      configGroupIDs(config.Groups),
		httpStats:           httpStats,
		resolveUID:          directory.resolveUID,

		mtInterceptors:       atomicInterceptors,
		configMTInterceptors: append([]InterceptorConfig(nil), config.MTInterceptors...),

		quotaPersister:   quotaPersister,
		quotaStore:       quotaStore,
		ownedQuotaStore:  ownedQuotaStore,
		quotaCancel:      quotaCancel,
		quotaFlushOnStop: shutdownQuotaFlushTimeout,
		cdrService:       cdrService,
		cdrCancel:        cdrCancel,
	}
	runtime.outboxWG.Add(1)
	go runtime.runOutbox(outboxCtx, dispatcher)
	runtime.quotaWG.Add(1)
	go func() {
		defer runtime.quotaWG.Done()
		_ = quotaPersister.Run(quotaCtx)
	}()
	runtime.cdrWG.Add(1)
	go func() {
		defer runtime.cdrWG.Done()
		runtime.runCDRMaintenance(cdrCtx, config, dependencies.CDRAlert, dependencies.RouterLogger)
	}()
	return runtime, nil
}

// CDRService exposes the authorization-and-audit enforcing read/export
// boundary to management transports.
func (runtime *Runtime) CDRService() *cdr.Service {
	if runtime == nil {
		return nil
	}
	return runtime.cdrService
}

func (runtime *Runtime) runCDRMaintenance(
	ctx context.Context,
	config Config,
	alert func(cdr.ReconciliationReport),
	logger *slog.Logger,
) {
	interval := 24 * time.Hour
	if config.CDRMaintenanceIntervalSeconds > 0 {
		interval = time.Duration(config.CDRMaintenanceIntervalSeconds) * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// The cadence is operator-changeable at runtime, so the ticker is handed to
	// the runtime rather than owned privately by this loop.
	runtime.maintenanceTicker.Store(ticker)
	principal := cdr.Principal{Subject: "gateway-maintenance", Roles: []cdr.Role{cdr.RoleOperator}}
	run := func() {
		report, err := runtime.cdrService.Reconcile(ctx, principal)
		if err != nil {
			if logger != nil && ctx.Err() == nil {
				logger.Error("CDR reconciliation failed", "error", err)
			}
		} else {
			if alert != nil {
				alert(report)
			}
			if !report.Healthy() && logger != nil {
				logger.Error("CDR reconciliation mismatch", "issues", report.Issues)
			}
		}
		if runtime.cdrService.Retention().Days <= 0 {
			return
		}
		for {
			result, pruneErr := runtime.cdrService.Prune(ctx, principal)
			if pruneErr != nil {
				if logger != nil && ctx.Err() == nil {
					logger.Error("CDR retention prune failed", "error", pruneErr)
				}
				return
			}
			if result.Records < int64(runtime.cdrService.Retention().BatchSize) {
				return
			}
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// PruneDurableQuotas removes spent-quota rows for principals that no longer
// exist. The gateway invokes it after config and persisted admin groups/users
// have all replayed, before it exposes any management listener that could
// recreate a deleted username.
func (runtime *Runtime) PruneDurableQuotas(ctx context.Context) (int64, error) {
	if runtime == nil || runtime.directory == nil {
		return 0, errors.New("outbound: nil runtime quota directory")
	}
	pruner, ok := runtime.quotaStore.(billing.QuotaPruner)
	if !ok {
		return 0, errors.New("outbound: durable quota store does not support principal pruning")
	}
	principals := runtime.directory.quotaPrincipals()
	active := make([]billing.QuotaKey, 0, len(principals))
	for _, principal := range principals {
		active = append(active, billing.QuotaKey{Scope: principal.Scope, Key: principal.Key})
	}
	deleted, err := pruner.PruneQuotas(ctx, active)
	if err != nil {
		return 0, fmt.Errorf("prune deleted billing principals: %w", err)
	}
	// Every principal that exists after config + admin replay has already
	// consumed its boot restore. Anything left in the index is therefore an
	// orphan too. Drop it in memory as well as in PostgreSQL so recreating a
	// deleted username during this same process cannot resurrect the old row.
	runtime.directory.mu.Lock()
	runtime.directory.restore = billing.NewQuotaIndex(nil)
	runtime.directory.mu.Unlock()
	return deleted, nil
}

// shutdownQuotaFlushTimeout bounds the final quota flush performed on Close.
// An orderly shutdown should not lose the charges made since the last tick, but
// it also must not hang on an unreachable database.
const shutdownQuotaFlushTimeout = 5 * time.Second

// ErrRouteOrderReserved reports an admin route whose order collides with a
// config route (config owns those orders).
var ErrRouteOrderReserved = errors.New("outbound: route order is config-reserved")

// ApplyAdminRoutes rebuilds the routing table from the config routes plus the
// supplied admin routes and swaps it live. It is the RouteProvisioner seam the
// admin plane drives: a bad spec or an order that collides with a config route
// returns an error and leaves the active table untouched (apply-first, so the
// caller persists only on success). Passing nil restores the config-only table.
func (runtime *Runtime) ApplyAdminRoutes(adminRoutes []RouteConfig) error {
	runtime.routesMu.Lock()
	defer runtime.routesMu.Unlock()
	reserved := make(map[int]struct{}, len(runtime.configRoutes))
	for _, route := range runtime.configRoutes {
		reserved[route.Order] = struct{}{}
	}
	for _, route := range adminRoutes {
		if _, clash := reserved[route.Order]; clash {
			return fmt.Errorf("%w: order %d", ErrRouteOrderReserved, route.Order)
		}
	}
	combined := make([]RouteConfig, 0, len(runtime.configRoutes)+len(adminRoutes))
	combined = append(combined, runtime.configRoutes...)
	combined = append(combined, adminRoutes...)
	table, _, _, err := buildRoutes(combined, runtime.resolveUID, runtime.directory.lookupGroupID)
	if err != nil {
		return err
	}
	runtime.routes.Store(table)
	return nil
}

// ErrInterceptorOrderReserved reports an admin interceptor whose order collides
// with a config interceptor (config owns those orders).
var ErrInterceptorOrderReserved = errors.New("outbound: interceptor order is config-reserved")

// ApplyAdminMTInterceptors rebuilds the MT interception table from the config
// interceptors plus the supplied admin ones and swaps it live. Same contract as
// ApplyAdminRoutes: a bad spec or a reserved order leaves the active table
// untouched, and passing nil restores the config-only table.
func (runtime *Runtime) ApplyAdminMTInterceptors(adminInterceptors []InterceptorConfig) error {
	runtime.interceptorsMu.Lock()
	defer runtime.interceptorsMu.Unlock()
	reserved := make(map[int]struct{}, len(runtime.configMTInterceptors))
	for _, entry := range runtime.configMTInterceptors {
		reserved[entry.Order] = struct{}{}
	}
	for _, entry := range adminInterceptors {
		if _, clash := reserved[entry.Order]; clash {
			return fmt.Errorf("%w: order %d", ErrInterceptorOrderReserved, entry.Order)
		}
	}
	combined := make([]InterceptorConfig, 0, len(runtime.configMTInterceptors)+len(adminInterceptors))
	combined = append(combined, runtime.configMTInterceptors...)
	combined = append(combined, adminInterceptors...)
	table, err := buildInterceptorTable(combined, runtime.resolveUID, runtime.directory.lookupGroupID)
	if err != nil {
		return err
	}
	runtime.mtInterceptors.Store(table)
	return nil
}

// configUsernames extracts the config-owned usernames.
func configUsernames(users []UserConfig) []string {
	names := make([]string, 0, len(users))
	for _, user := range users {
		names = append(names, user.Username)
	}
	return names
}

// ErrUserReserved reports an admin user whose username collides with a config
// user (config owns those).
var ErrUserReserved = errors.New("outbound: username is config-reserved")

// AdminReplacement is a live user/group edit whose locks remain held until the
// admin store either commits or rejects the corresponding durable write.
type AdminReplacement interface {
	Commit()
	Rollback()
}

// AddAdminUser installs an admin-provisioned user with a caller-supplied stable
// uid (the admin store assigns and persists it, so route user-filters resolve
// the same uid across restarts). It refuses config-owned usernames.
func (runtime *Runtime) AddAdminUser(entry UserConfig, uid int64) error {
	for _, reserved := range runtime.configUsernames {
		if reserved == entry.Username {
			return fmt.Errorf("%w: %q", ErrUserReserved, entry.Username)
		}
	}
	return runtime.directory.applyUser(entry, uid)
}

// BeginReplaceAdminUser starts an admin-provisioned user update without
// re-granting money when only credentials or other provisioning fields
// changed. The caller must Commit or Rollback the returned replacement.
func (runtime *Runtime) BeginReplaceAdminUser(entry UserConfig, uid int64) (AdminReplacement, error) {
	for _, reserved := range runtime.configUsernames {
		if reserved == entry.Username {
			return nil, fmt.Errorf("%w: %q", ErrUserReserved, entry.Username)
		}
	}
	return runtime.directory.beginReplaceUser(entry, uid)
}

// ReplaceAdminUser performs an immediately committed replacement. The admin
// service uses BeginReplaceAdminUser so a failed SQLite write can roll back.
func (runtime *Runtime) ReplaceAdminUser(entry UserConfig, uid int64) error {
	replacement, err := runtime.BeginReplaceAdminUser(entry, uid)
	if err != nil {
		return err
	}
	replacement.Commit()
	return nil
}

// RemoveAdminUser removes an admin-provisioned user. Config users are protected.
func (runtime *Runtime) RemoveAdminUser(username string) error {
	for _, reserved := range runtime.configUsernames {
		if reserved == username {
			return fmt.Errorf("%w: %q", ErrUserReserved, username)
		}
	}
	return runtime.directory.removeUser(username)
}

// ConfigUsernames lists the config-owned usernames the admin plane must not
// touch, and the count seeds admin uid assignment above the config range.
func (runtime *Runtime) ConfigUsernames() []string {
	return append([]string(nil), runtime.configUsernames...)
}

// ErrGroupReserved reports an admin group whose gid collides with a config
// group (config owns those).
var ErrGroupReserved = errors.New("outbound: gid is config-reserved")

// AddAdminGroup installs an admin-provisioned billing group with a stable
// numeric gid. It refuses config-owned gids.
func (runtime *Runtime) AddAdminGroup(entry GroupConfig, number int64) error {
	for _, reserved := range runtime.configGroupIDs {
		if reserved == entry.GID {
			return fmt.Errorf("%w: %q", ErrGroupReserved, entry.GID)
		}
	}
	return runtime.directory.applyGroup(entry, number)
}

// BeginReplaceAdminGroup starts an in-place live group edit. The caller must
// Commit or Rollback the returned replacement.
func (runtime *Runtime) BeginReplaceAdminGroup(entry GroupConfig, number int64) (AdminReplacement, error) {
	for _, reserved := range runtime.configGroupIDs {
		if reserved == entry.GID {
			return nil, fmt.Errorf("%w: %q", ErrGroupReserved, entry.GID)
		}
	}
	return runtime.directory.beginReplaceGroup(entry, number)
}

// ReplaceAdminGroup performs an immediately committed replacement. The admin
// service uses BeginReplaceAdminGroup so a failed SQLite write can roll back.
func (runtime *Runtime) ReplaceAdminGroup(entry GroupConfig, number int64) error {
	replacement, err := runtime.BeginReplaceAdminGroup(entry, number)
	if err != nil {
		return err
	}
	replacement.Commit()
	return nil
}

// RemoveAdminGroup removes an admin-provisioned group. Config groups are
// protected. Users still pointing at the group keep the billing.Group they were
// given: dropping a ceiling out from under a live user mid-submit would let
// charges through that the operator meant to cap, so the group survives until
// those users are themselves re-provisioned.
func (runtime *Runtime) RemoveAdminGroup(gid string) error {
	for _, reserved := range runtime.configGroupIDs {
		if reserved == gid {
			return fmt.Errorf("%w: %q", ErrGroupReserved, gid)
		}
	}
	return runtime.directory.removeGroup(gid)
}

// HTTPStats exposes the front door's counter registry so other management
// surfaces (the jCli `stats` command) report the same numbers /metrics does.
func (runtime *Runtime) HTTPStats() *stats.HTTPStats { return runtime.httpStats }

// ConfigRoutes lists the config-owned MT routes. Management surfaces show them
// alongside admin-managed ones -- an operator looking at the console must see
// the routes the gateway is actually running, not only the ones the admin plane
// happens to own.
func (runtime *Runtime) ConfigRoutes() []RouteConfig {
	return append([]RouteConfig(nil), runtime.configRoutes...)
}

// ConfigGroupIDs lists the config-owned gids the admin plane must not touch.
func (runtime *Runtime) ConfigGroupIDs() []string {
	return append([]string(nil), runtime.configGroupIDs...)
}

// SetCDRRetention changes the retention policy in force, without a restart.
func (runtime *Runtime) SetCDRRetention(days, batch int) error {
	if runtime == nil || runtime.cdrService == nil {
		return fmt.Errorf("no CDR service is configured")
	}
	return runtime.cdrService.SetRetention(cdr.RetentionPolicy{Days: days, BatchSize: batch})
}

// CDRRetentionDays and CDRRetentionBatch report the policy in force, so a
// caller changing one half does not have to guess the other.
func (runtime *Runtime) CDRRetentionDays() int {
	if runtime == nil || runtime.cdrService == nil {
		return 0
	}
	return runtime.cdrService.Retention().Days
}

func (runtime *Runtime) CDRRetentionBatch() int {
	if runtime == nil || runtime.cdrService == nil {
		return 0
	}
	return runtime.cdrService.Retention().BatchSize
}

// SetCDRMaintenanceInterval re-arms the reconciliation and prune cadence.
func (runtime *Runtime) SetCDRMaintenanceInterval(seconds int) error {
	if seconds <= 0 {
		return fmt.Errorf("the maintenance interval must be a positive number of seconds")
	}
	ticker := runtime.maintenanceTicker.Load()
	if ticker == nil {
		return fmt.Errorf("CDR maintenance is not running")
	}
	ticker.Reset(time.Duration(seconds) * time.Second)
	return nil
}

// SetQuotaPersistInterval changes how often spent balances are made durable.
func (runtime *Runtime) SetQuotaPersistInterval(seconds int) error {
	if runtime == nil || runtime.quotaPersister == nil {
		return fmt.Errorf("no quota persister is running")
	}
	return runtime.quotaPersister.SetInterval(time.Duration(seconds) * time.Second)
}

// GroupQuota reports a billing group's live shared balance and submit_sm_count
// -- what the ceiling has left now, not what it was provisioned with. Management
// surfaces need both to tell a spent group from a small one. A nil value inside
// the quota is the legacy unlimited marker.
func (runtime *Runtime) GroupQuota(gid string) (billing.Quota, bool) {
	if runtime == nil || runtime.directory == nil {
		return billing.Quota{}, false
	}
	group, ok := runtime.directory.lookupGroup(gid)
	if !ok {
		return billing.Quota{}, false
	}
	state := group.GetState()
	return billing.Quota{Balance: state.Balance, SubmitSmCount: state.SubmitSmCountQuota}, true
}

// Submitter exposes the composed MT submit pipeline so other ingress paths
// (the SMPPS server) can share the same routing/billing/publication engine.
// AMQPHealthy reports whether the runtime's broker connection is open. A nil
// connection (dependency-injected runtimes without an owned broker) counts as
// healthy so the check reflects only state this runtime owns.
func (runtime *Runtime) AMQPHealthy() bool {
	return runtime.connection == nil || !runtime.connection.IsClosed()
}

// Publisher exposes the runtime's confirmed AMQP publisher for ingress
// publications that share the broker (deliver_sm MO/DLR from the connectors).
func (runtime *Runtime) Publisher() *amqpcompat.Publisher {
	return runtime.publisher
}

// QueueDepths observes the broker depth of the named queues on this runtime's
// connection, for the gateway's queue-depth gauge.
//
// It reuses the runtime's own connection rather than dialling a second one, so a
// broker outage shows up as one failure in one place. The observation is passive
// and read-only; see amqpcompat.QueueDepths for what the number does and does
// not include.
func (runtime *Runtime) QueueDepths(ctx context.Context, queues []string) (map[string]int, error) {
	if runtime == nil {
		return nil, amqpcompat.ErrNoBrokerConnection
	}
	return amqpcompat.QueueDepths(ctx, runtime.connection, queues)
}

func (runtime *Runtime) Submitter() core.Submitter {
	if runtime == nil {
		return nil
	}
	return runtime.submitter
}

// Authenticator exposes the live credential directory through its narrow core
// contract for trusted management compatibility surfaces.
func (runtime *Runtime) Authenticator() core.Authenticator {
	if runtime == nil {
		return nil
	}
	return runtime.directory
}

// BalanceReader and RateReader expose the live user directory to trusted
// management surfaces. They deliberately return the narrow core interfaces,
// not the directory itself, so callers cannot mutate billing state.
func (runtime *Runtime) BalanceReader() core.BalanceReader {
	if runtime == nil {
		return nil
	}
	return runtime.directory
}

func (runtime *Runtime) RateReader() core.RateReader {
	if runtime == nil {
		return nil
	}
	return runtime.directory
}

// NewProcessOwner mints an owner token that is unique to this process for the
// lifetime of a claim.
//
// It is random rather than derived from hostname or pid on purpose: two
// processes on one host, or a pid reused after a crash, would otherwise be
// indistinguishable in a lease, and every exclusive claim in this codebase —
// the submit outbox, the termination connector's pending receipts — depends on
// exactly that distinction. A duplicated owner means two processes both believe
// they hold the same row.
func NewProcessOwner(prefix string) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(token[:]), nil
}

func newOutboxOwner() (string, error) {
	return NewProcessOwner("gateway-outbox-")
}

func (runtime *Runtime) runOutbox(ctx context.Context, dispatcher *submittransaction.Dispatcher) {
	defer runtime.outboxWG.Done()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	// Dispatch failures were discarded entirely. An event that can never be
	// published -- an unroutable key after a topology change, a payload that
	// fails to restore -- is claimed, fails, and is released again forever, and
	// every later event for the same part is held behind it by the in-order
	// gate. That is a customer's receipts and billing intents stalled
	// indefinitely with nothing said. Log it, rate-limited so a broker outage
	// does not become its own log flood.
	var lastReport time.Time
	var suppressed int
	for {
		if _, err := dispatcher.DispatchOnce(ctx); err != nil && ctx.Err() == nil {
			if now := time.Now(); now.Sub(lastReport) >= outboxErrorReportInterval {
				slog.Error("submit outbox dispatch failed",
					"error", err.Error(), "suppressed_since_last_report", suppressed)
				lastReport, suppressed = now, 0
			} else {
				suppressed++
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// outboxErrorReportInterval rate-limits the dispatch-failure line. The loop runs
// twenty times a second, so an unrated log would bury everything else.
const outboxErrorReportInterval = 30 * time.Second

func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	var errs []error
	if runtime.restClose != nil {
		runtime.restClose()
	}
	if runtime.cdrCancel != nil {
		runtime.cdrCancel()
		runtime.cdrWG.Wait()
	}
	if runtime.outboxCancel != nil {
		runtime.outboxCancel()
		runtime.outboxWG.Wait()
	}
	// Stop the periodic flusher, then flush once more on a fresh deadline: the
	// runtime context is usually already cancelled by the time Close runs, and
	// an orderly restart must not refund the charges made since the last tick.
	if runtime.quotaCancel != nil {
		runtime.quotaCancel()
		runtime.quotaWG.Wait()
	}
	if runtime.quotaPersister != nil {
		flushCtx, cancelFlush := context.WithTimeout(context.Background(), runtime.quotaFlushOnStop)
		if _, err := runtime.quotaPersister.FlushOnce(flushCtx); err != nil {
			errs = append(errs, err)
		}
		cancelFlush()
	}
	if runtime.ownedQuotaStore != nil {
		if err := runtime.ownedQuotaStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if runtime.billing != nil {
		if err := runtime.billing.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if runtime.publisher != nil {
		if err := runtime.publisher.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if runtime.ownedBridge && runtime.bridge != nil {
		if err := runtime.bridge.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if runtime.connection != nil {
		if err := runtime.connection.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if runtime.ownedRESTBatchStore != nil {
		if err := runtime.ownedRESTBatchStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if runtime.ownedStore != nil {
		if err := runtime.ownedStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validateStandaloneConfig(config Config) error {
	for index, route := range config.Routes {
		if len(route.ConnectorCandidates()) > 1 {
			return fmt.Errorf("%w: standalone route %d cannot use a connector pool without observed availability", ErrInvalidRuntimeConfig, index)
		}
	}
	return nil
}

func validateConfig(config Config) error {
	// python_path is no longer required: it existed for the pickle bridge, which
	// the native Go codec replaced. It is still honoured for the optional
	// interceptor script runner, which defaults to "python3" when unset, so a
	// deployment without interceptors need not configure a Python at all.
	if config.ListenAddress == "" || config.AMQPURL == "" || config.PostgresDSN == "" {
		return fmt.Errorf("%w: listen_address, amqp_url and postgres_dsn are required", ErrInvalidRuntimeConfig)
	}
	if len(config.Users) == 0 || len(config.Routes) == 0 {
		return fmt.Errorf("%w: at least one user and route are required", ErrInvalidRuntimeConfig)
	}
	if config.QuotaPersistIntervalSeconds < 0 {
		return fmt.Errorf("%w: quota_persist_interval_seconds cannot be negative", ErrInvalidRuntimeConfig)
	}
	if config.CDRCurrency != "" {
		if err := cdr.ValidateCurrency(config.CDRCurrency); err != nil {
			return fmt.Errorf("%w: cdr_currency must be a three-letter uppercase ISO-4217 code", ErrInvalidRuntimeConfig)
		}
	}
	if config.CDRRetentionDays < 0 || config.CDRRetentionBatchSize < 0 ||
		config.CDRRetentionBatchSize > 10000 || config.CDRMaintenanceIntervalSeconds < 0 {
		return fmt.Errorf("%w: invalid CDR retention/maintenance settings", ErrInvalidRuntimeConfig)
	}
	for index, route := range config.Routes {
		if len(route.ConnectorCandidates()) == 0 {
			return fmt.Errorf("%w: route %d has no connectors", ErrInvalidRuntimeConfig, index)
		}
	}
	return nil
}

func resolvedCDRCurrency(config Config) string {
	if config.CDRCurrency == "" {
		return cdr.DefaultCurrency
	}
	return config.CDRCurrency
}

func resolvedCDRRetentionBatch(config Config) int {
	if config.CDRRetentionDays <= 0 {
		return 0
	}
	if config.CDRRetentionBatchSize == 0 {
		return 1000
	}
	return config.CDRRetentionBatchSize
}

// routeConnectorType resolves a route's declared connector type. An empty value
// is "smppc" rather than an error: every route persisted before the termination
// connector existed carries no type and must keep loading as the outbound SMPP
// route it has always been. Silently defaulting a *declared* type would be the
// dangerous direction — a "term" route loaded as "smppc" would be handed to a
// carrier connector that does not exist.
func routeConnectorType(declared string) (routingtable.ConnectorType, error) {
	switch declared {
	case "", string(routingtable.SMPPC):
		return routingtable.SMPPC, nil
	case string(routingtable.TERM):
		return routingtable.TERM, nil
	default:
		return "", fmt.Errorf("unknown connector type %q", declared)
	}
}

func buildRoutes(configs []RouteConfig, resolveUID uidResolver, groupResolvers ...gidResolver) (routingtable.Table, []string, float64, error) {
	var resolveGID gidResolver
	if len(groupResolvers) > 0 {
		resolveGID = groupResolvers[0]
	}
	builder, err := routingtable.NewBuilder(routingfilter.MT)
	if err != nil {
		return routingtable.Table{}, nil, 0, err
	}
	connectors := make(map[string]struct{})
	defaultRate := 0.0
	bestOrder := -1
	for index, entry := range configs {
		candidates := entry.ConnectorCandidates()
		if len(candidates) == 0 {
			return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d has no connectors", ErrInvalidRuntimeConfig, index)
		}
		connectorType, err := routeConnectorType(entry.ConnectorType)
		if err != nil {
			return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d: %w", ErrInvalidRuntimeConfig, index, err)
		}
		seenCandidates := make(map[string]struct{}, len(candidates))
		routeConnectors := make([]routingtable.Connector, 0, len(candidates))
		for _, connectorID := range candidates {
			if connectorID == "" {
				return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d has empty connector", ErrInvalidRuntimeConfig, index)
			}
			if _, duplicate := seenCandidates[connectorID]; duplicate {
				return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d repeats connector %q", ErrInvalidRuntimeConfig, index, connectorID)
			}
			seenCandidates[connectorID] = struct{}{}
			connectors[connectorID] = struct{}{}
			routeConnectors = append(routeConnectors, routingtable.Connector{IDValue: connectorID, TypeValue: connectorType})
		}
		connector := routeConnectors[0]
		var route routingtable.Route
		if entry.Default {
			if entry.Order != 0 {
				return routingtable.Table{}, nil, 0, fmt.Errorf("%w: default route %d must use order 0", ErrInvalidRuntimeConfig, index)
			}
			if len(entry.Filters) > 0 {
				return routingtable.Table{}, nil, 0, fmt.Errorf("%w: default route %d cannot carry filters", ErrInvalidRuntimeConfig, index)
			}
			route, err = routingtable.NewDefaultRoute(connector, entry.Rate)
		} else {
			if entry.Order <= 0 {
				return routingtable.Table{}, nil, 0, fmt.Errorf("%w: static route %d must use positive order", ErrInvalidRuntimeConfig, index)
			}
			filters, filterErr := buildRouteFilters(entry.Filters, resolveUID, resolveGID)
			if filterErr != nil {
				return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d: %v", ErrInvalidRuntimeConfig, index, filterErr)
			}
			route, err = routingtable.NewStaticRoute(routingfilter.MT, connector, entry.Rate, filters...)
		}
		if err != nil {
			return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d: %v", ErrInvalidRuntimeConfig, index, err)
		}
		route, err = route.WithConnectors(routeConnectors)
		if err != nil {
			return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d connector pool: %v", ErrInvalidRuntimeConfig, index, err)
		}
		if err := builder.Add(entry.Order, route); err != nil {
			return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d: %v", ErrInvalidRuntimeConfig, index, err)
		}
		if entry.Order > bestOrder {
			bestOrder = entry.Order
			defaultRate = entry.Rate
		}
	}
	connectorIDs := make([]string, 0, len(connectors))
	for connectorID := range connectors {
		connectorIDs = append(connectorIDs, connectorID)
	}
	sort.Strings(connectorIDs)
	return builder.Build(), connectorIDs, defaultRate, nil
}

func connectorSelector(available func(string) bool) func(routingtable.Route) (string, bool) {
	if available == nil {
		return nil
	}
	return func(route routingtable.Route) (string, bool) {
		for _, connector := range route.Connectors() {
			if available(connector.ID()) {
				return connector.ID(), true
			}
		}
		return "", false
	}
}
