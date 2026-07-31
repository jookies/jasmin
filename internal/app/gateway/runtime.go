package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/adminweb"
	"github.com/pumpitspace/synevyr/internal/app/dlrlookup"
	"github.com/pumpitspace/synevyr/internal/app/dlrthrower"
	"github.com/pumpitspace/synevyr/internal/app/jcli"
	"github.com/pumpitspace/synevyr/internal/app/modispatch"
	"github.com/pumpitspace/synevyr/internal/app/mothrower"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/app/smppsdelivery"
	"github.com/pumpitspace/synevyr/internal/app/smppsserver"
	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/dlr"
	"github.com/pumpitspace/synevyr/internal/core/interceptor"
	"github.com/pumpitspace/synevyr/internal/core/logging"
	"github.com/pumpitspace/synevyr/internal/core/mo"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/core/termination"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
	"github.com/pumpitspace/synevyr/internal/transport/pyintercept"
)

type ManagerFactory func(string) *smppc.Manager

type Runtime struct {
	Handler          http.Handler
	WebHandler       http.Handler // admin web UI, served on WebListenAddress (nil when disabled)
	WebListenAddress string
	// AdminAPIHandler serves /admin/ on its own listener when
	// admin.api_listen_address is set. Nil means it stays on the public mux.
	AdminAPIHandler       http.Handler
	AdminAPIListenAddress string
	RESTHandler           http.Handler // standalone legacy REST daemon view
	RESTListenAddress     string
	manager               *smppc.Manager
	outbound              *outbound.Runtime
	bridge                picklecompat.Codec
	store                 *storage.PostgresSubmitTransactionRepository
	leadership            *storage.PostgresLeaderLease
	dlrLookup             *dlrlookup.Service
	dlrThrower            *dlrthrower.Service
	moThrower             *mothrower.Service
	smppsServer           *smppsserver.Service
	requiredConnectors    []string
	dlrRedisClose         func()
	adminStore            *admin.Store
	jcli                  *jcli.Server
	interceptorRunner     *pyintercept.Runner
	// termination runs the MT termination connectors and the two runners that
	// drain their shared spool. Nil when the config declares no such section.
	termination *terminationPlane
	// messagePull is the late-bound message pull endpoint. The REST listener is
	// built by the outbound runtime, and the spool that backs this endpoint is
	// built afterwards by the termination plane — it needs that runtime's
	// publisher — so the listener is handed a resolver and this slot is filled
	// in once the plane exists. Until then, and forever on a gateway with no
	// termination section, it resolves to nil and the path answers 404.
	messagePull atomic.Value
	// Live MO interception (nil when MO interception is neither configured nor
	// admin-editable); config orders are reserved against admin entries.
	moInterceptors       *interceptor.AtomicTable
	configMOInterceptors []outbound.InterceptorConfig
	moInterceptorsMu     sync.Mutex
	workerCancel         context.CancelFunc
	closeOnce            sync.Once
	closeErr             error
}

func NewRuntime(ctx context.Context, config Config) (_ *Runtime, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	// One process-level durability choice: the top-level flag propagates into
	// every component that declares topology (outbound, connectors, workers).
	// A section-level true is honoured for a worker on its own broker.
	config.Outbound.AMQPDurableTopology = config.Outbound.AMQPDurableTopology || config.AMQPDurableTopology
	workerCtx, workerCancel := context.WithCancel(context.Background())
	var leadership *storage.PostgresLeaderLease
	if config.HA != nil {
		var leadershipErr error
		leadership, leadershipErr = storage.WaitPostgresLeaderLease(
			ctx,
			config.Outbound.PostgresDSN,
			config.HA.Namespace,
			config.HA.StandbyRetryInterval(),
		)
		if leadershipErr != nil {
			workerCancel()
			return nil, fmt.Errorf("acquire active-passive gateway fence: %w", leadershipErr)
		}
	}
	repository, err := storage.OpenPostgresSubmitTransactionRepository(ctx, config.Outbound.PostgresDSN)
	if err != nil {
		if leadership != nil {
			_ = leadership.Close()
		}
		workerCancel()
		return nil, err
	}
	runtime := &Runtime{store: repository, leadership: leadership, workerCancel: workerCancel}
	defer func() {
		if resultErr != nil {
			_ = runtime.Close()
		}
	}()
	if err = repository.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate submit transaction store: %w", err)
	}
	transactions, err := submittransaction.NewProductionService(repository, nil)
	if err != nil {
		return nil, err
	}
	if _, err = transactions.Recover(ctx); err != nil {
		return nil, fmt.Errorf("recover unresolved submit attempts: %w", err)
	}
	// The native Go codec is the only pickle path. The opt-in Python bridge
	// subprocess was removed with the rest of the Python surface; pickle_codec is
	// accepted and ignored so an existing config does not fail to load.
	var bridge picklecompat.Codec = picklecompat.NewNativeCodec()
	runtime.bridge = bridge
	// One shared jasmin-sm-listener logger renders the SMS-MT audit line for every
	// connector (the legacy uses a single listener logger); the connector id in the
	// line distinguishes them. Routes to the sm-listener log_file (messages.log)
	// with TimedRotatingFileHandler rotation when set, else stderr.
	submitAuditLogger := logging.Logger("jasmin-sm-listener", logging.Config{
		Level:  config.SubmitAuditLog.Level,
		File:   config.SubmitAuditLog.File,
		Rotate: config.SubmitAuditLog.Rotate,
	})
	submitAuditPrivacy := config.SubmitAuditLog.Privacy
	routerLogger := componentLogger("jasmin-router", config.RouterLog)
	httpAPILogger := componentLogger("jasmin-http-api", config.HTTPAPILog)
	httpAccessLogger := componentLogger("jasmin-http-access", config.HTTPAccessLog)
	dlrLogger := componentLogger("jasmin-dlr-lookup", config.DLRLog)
	amqpLogger := componentLogger("jasmin-amqp-factory", config.AMQPLog)
	dlrThrowerLogger := componentLogger("dlr-thrower", config.DLRThrowerLog)
	deliverSMThrowerLogger := componentLogger("deliversm-thrower", config.DeliverSMThrowerLog)
	// Deferred deliver-ingest wiring: the publisher and multipart store are
	// built with the outbound runtime (below), after this factory. Capturing
	// them by reference lets connectors created LATER — the admin-provisioned
	// ones — receive MO/DLR too, not just the config connectors wired
	// explicitly once the runtime exists.
	// The per-connector counter registry is created before the connector factory
	// so the closure can hand it to every connector, including the ones the admin
	// plane provisions later. The admin surfaces read from this same registry.
	smppcStats := stats.NewSMPPcRegistry()
	var deliverPublisher smppc.DeliverPublisher
	var deliverMultipart smppc.MultipartStore
	// Deferred MO interceptor: built below (needs the script runner), captured
	// by reference so admin-provisioned connectors get MO interception too.
	var moInterceptor smppc.MOInterceptor
	manager := smppc.NewManagerWithFactory(config.Outbound.AMQPURL, func(connectorConfig smppc.Config, amqpURL string) (*smppc.Connector, error) {
		connectorConfig.AMQPDurableTopology = connectorConfig.AMQPDurableTopology || config.AMQPDurableTopology
		connector, connectorErr := smppc.NewConnectorWithDecoder(connectorConfig, amqpURL, bridge)
		if connectorErr != nil {
			return nil, connectorErr
		}
		if connectorErr = connector.ConfigureDurability(transactions); connectorErr != nil {
			return nil, connectorErr
		}
		connector.SetSubmitAuditLogger(submitAuditLogger, submitAuditPrivacy)
		// The per-connector counter registry the admin surfaces read. Without
		// this the web console's "Connector counters" panel reported zeros for a
		// connector actively carrying traffic, while claiming to be live.
		connector.SetStats(smppcStats)
		connector.SetComponentLogger(logging.Logger("smpp.client."+connectorConfig.CID, logging.Config{
			Level: connectorConfig.LogLevel, File: connectorConfig.LogFile, Rotate: connectorConfig.LogRotate,
		}))
		if deliverPublisher != nil {
			connector.SetDeliverUpstream(deliverPublisher, bridge)
			connector.SetMultipartStore(deliverMultipart)
		}
		if moInterceptor != nil {
			connector.SetMOInterceptor(moInterceptor)
		}
		return connector, nil
	})
	runtime.manager = manager
	for _, connector := range config.Connectors {
		if err := manager.Add(connector); err != nil {
			return nil, fmt.Errorf("add connector %q: %w", connector.CID, err)
		}
	}
	// Observability registries are created here and shared by pointer: the HTTP
	// /metrics handler (built inside the outbound runtime) reads them, and the
	// SMPPS server increments smppsStats. connectorIDs lists the configured
	// connector ids in stable order for the smppc metric labels.
	// startedAt backs the console's created_at rows; one clock read at boot
	// keeps every report consistent.
	startedAt := time.Now().UTC()
	smppsStats := &stats.SMPPsStats{}
	connectorIDs := managedConnectorIDs(manager)
	// The DLRLookup queue is declared with this pid so the response path's DLR
	// publish is routable; it must match the pid the DLRLookup consumer binds
	// (both default to "main").
	dlrLookupPID := "main"
	if config.DLRLookup != nil && config.DLRLookup.PID != "" {
		dlrLookupPID = config.DLRLookup.PID
	}
	// Resolve each connector's default submit_sm PDU params once, so the
	// front-door submit applies the routed connector's TON/NPI etc. (GAP 4). A
	// validated copy resolves the legacy TON/NPI defaults.
	connectorPDUDefaults := make(map[string]smppc.PDUDefaults, len(config.Connectors))
	for _, connector := range config.Connectors {
		resolved := connector
		_ = resolved.Validate()
		connectorPDUDefaults[connector.CID] = resolved.PDUDefaults()
	}
	pduDefaultsProvider := func(connectorID string) (smppc.PDUDefaults, bool) {
		defaults, ok := connectorPDUDefaults[connectorID]
		return defaults, ok
	}
	// Per-connector dlr_expiry (record TTL) for the submit-side DLR store.
	connectorDLRExpiry := make(map[string]int64, len(config.Connectors))
	for _, connector := range config.Connectors {
		if connector.DLRExpiry > 0 {
			connectorDLRExpiry[connector.CID] = int64(connector.DLRExpiry)
		}
	}
	dlrExpiryProvider := func(connectorID string) int64 { return connectorDLRExpiry[connectorID] }
	// The submit-side DLR store shares the DLRLookup Redis: writing dlr:<msgid>
	// here is what lets terminal (level-2/3) receipts correlate. Enabled only
	// when a Redis URL is configured (dlr_lookup present); without it, level-1
	// callbacks via the response path still work, terminal ones cannot.
	var dlrRequestStore core.DLRRequestStore
	var multipartStore smppc.MultipartStore
	if config.DLRLookup != nil && config.DLRLookup.RedisURL != "" {
		store, parts, redisCleanup, storeErr := newDLRRequestStore(config.DLRLookup.RedisURL)
		if storeErr != nil {
			return nil, fmt.Errorf("open DLR request store: %w", storeErr)
		}
		runtime.dlrRedisClose = redisCleanup
		dlrRequestStore = store
		multipartStore = parts
	}
	// Interception: start the Python script runner subprocess when the config
	// declares MT or MO interceptors, and share it across both directions —
	// the MT submit pipeline and the MO deliver hook.
	// The runner also starts when the admin plane may add interceptors at
	// runtime, since a table swapped in later has nothing to execute without
	// it. Gating on the explicit flag keeps the Python subprocess out of
	// deployments that never intercept.
	var interceptorRunner interceptor.Runner
	interceptorEditing := config.Admin != nil && config.Admin.AllowInterceptorEditing
	if len(config.Outbound.MTInterceptors) > 0 || len(config.Outbound.MOInterceptors) > 0 || interceptorEditing {
		runnerImpl, runnerErr := pyintercept.NewRunner(workerCtx, config.Outbound.PythonPath)
		if runnerErr != nil {
			return nil, fmt.Errorf("start interceptor runner: %w", runnerErr)
		}
		runtime.interceptorRunner = runnerImpl
		interceptorRunner = runnerImpl
	}
	// MO-direction interception: build the MO table and bridge it to the smppc
	// deliver path via the shared runner. Assigning the deferred var here wires
	// both the config connectors (explicit loop below) and any admin-created
	// connectors (the factory closure).
	if len(config.Outbound.MOInterceptors) > 0 || interceptorEditing {
		moTable, moErr := outbound.BuildMOInterceptorTable(config.Outbound.MOInterceptors)
		if moErr != nil {
			return nil, fmt.Errorf("build MO interceptor table: %w", moErr)
		}
		runtime.moInterceptors = interceptor.NewAtomicTable(moTable)
		runtime.configMOInterceptors = append([]outbound.InterceptorConfig(nil), config.Outbound.MOInterceptors...)
		moInterceptor = newMOInterceptorAdapter(runtime.moInterceptors, interceptorRunner)
	}
	// MT routing consults both connector types through one gate: an SMPP client
	// connector hands the message upstream, a termination connector terminates
	// it here, and a stopped connector of either kind drops out of selection.
	// The termination manager is installed further down, once the outbound
	// publisher it needs exists.
	availability := &availabilityGate{smppc: manager.Available}
	outboundRuntime, err := outbound.NewRuntimeWithDependencies(workerCtx, config.Outbound, outbound.RuntimeDependencies{
		Bridge: bridge, Transactions: transactions, Repository: repository, ConnectorAvailable: availability.Available,
		SMPPcStats: smppcStats, SMPPsStats: smppsStats, ConnectorIDs: connectorIDs,
		DLRLookupPID: dlrLookupPID, ConnectorPDUDefaults: pduDefaultsProvider,
		DLRRequestStore: dlrRequestStore, ConnectorDLRExpiry: dlrExpiryProvider,
		InterceptorRunner: interceptorRunner, RouterLogger: routerLogger,
		HTTPLogger: httpAPILogger, HTTPAccessLogger: httpAccessLogger,
		RESTConfig:  config.REST,
		MessagePull: runtime.resolveMessagePull,
		QuotaPersistErrors: func(err error) {
			routerLogger.Error("Billing quota persistence failed: " + err.Error())
		},
	})
	if err != nil {
		amqpLogger.Error("Outbound runtime failed: " + err.Error())
		return nil, fmt.Errorf("start outbound runtime: %w", err)
	}
	amqpLogger.Info("AMQP topology and outbound workers are ready.")
	routerLogger.Info("Router configured and ready.")
	httpAPILogger.Info("HTTP API configured and ready.")
	runtime.outbound = outboundRuntime
	runtime.RESTHandler = outboundRuntime.RESTHandler
	runtime.RESTListenAddress = config.REST.ListenAddress
	runtime.requiredConnectors = config.RequiredConnectors()
	// Wire deliver_sm ingestion (MO + receipt publications) into every
	// connector: the config connectors were built before the runtime existed,
	// so wire them explicitly now; the deferred vars ensure admin-provisioned
	// connectors created later get the same wiring from the factory.
	if publisher := outboundRuntime.Publisher(); publisher != nil {
		deliverPublisher = publisher
		deliverMultipart = multipartStore
		for _, connectorConfig := range config.Connectors {
			connector, getErr := manager.Get(connectorConfig.CID)
			if getErr != nil {
				return nil, fmt.Errorf("wire deliver ingestion for %q: %w", connectorConfig.CID, getErr)
			}
			connector.SetDeliverUpstream(publisher, bridge)
			if multipartStore != nil {
				connector.SetMultipartStore(multipartStore)
			}
			if moInterceptor != nil {
				connector.SetMOInterceptor(moInterceptor)
			}
		}
	}
	// MT termination connectors: they consume the same per-connector submit
	// queue an SMPP client connector consumes, so everything upstream — routing,
	// filters, interception, billing, CDR admission, the MT audit line — has
	// already run. Built here because the connector's synthesized submit_sm_resp
	// and receipt legs publish through the outbound runtime's confirmed
	// publisher.
	termPlane, err := newTerminationPlane(ctx, terminationDeps{
		Config:    config,
		Publisher: outboundRuntime.Publisher(),
		Submits:   bridge,
		// The CDR store is the same repository the submit path uses, so a
		// terminated message's acceptance lands on the very record its receipt
		// is later matched against.
		AcceptCDR: repository.MarkCDRTerminated,
		Logger:    routerLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("start termination connectors: %w", err)
	}
	if termPlane != nil {
		runtime.termination = termPlane
		// Installed before Start: a connector that reaches CONSUMING before the
		// gate knows about it would be skipped by routing for that window.
		availability.install(termPlane.Manager())
		// Fills the slot the REST listener was handed a resolver for. Before this
		// point GET /messages answers 404, which is the correct answer while
		// there is no spool to read.
		runtime.installMessagePull(termPlane.PullHandler())
		if err := termPlane.Start(); err != nil {
			return nil, fmt.Errorf("start termination connectors: %w", err)
		}
		routerLogger.Info(fmt.Sprintf("Termination connectors configured and ready (%d).",
			len(config.terminationConnectors())))
	}
	// Broker depth is a population only the broker knows, so it is polled rather
	// than counted at a change. Started after the termination plane so the first
	// pass already sees both MT connector types.
	runtime.startQueueDepthObserver(workerCtx, dlrLookupPID, amqpLogger)
	// /health (real readiness) rides beside the legacy-parity endpoints; the
	// outbound handler keeps everything else, including the unconditional /ping.
	// MO dispatch is constructed before the admin plane because the admin MO
	// route service provisions through it. It also starts when admin is enabled
	// but config declares no MO routes, so routes can be added at runtime — an
	// MO with no matching route is unroutable, which is the same outcome as
	// having no dispatcher at all.
	var dispatchService *modispatch.Service
	// Set once the admin block has warned after loading persisted routes, so the
	// no-admin fallback below does not warn twice.
	moDefaultRouteWarned := false
	if len(config.MORoutes) > 0 || config.Admin != nil {
		dispatchConfig := modispatch.Config{
			AMQPURL:             config.Outbound.AMQPURL,
			AMQPDurableTopology: config.Outbound.AMQPDurableTopology,
			Routes:              config.MORoutes,
		}
		service, dispatchErr := modispatch.NewService(ctx, dispatchConfig, bridge,
			modispatch.WithLogger(routerLogger),
			modispatch.WithOnError(func(err error) {
				routerLogger.Error("MO dispatch failed: " + err.Error())
				amqpLogger.Error("MO dispatch worker failed: " + err.Error())
			}))
		if dispatchErr != nil {
			return nil, fmt.Errorf("start MO dispatch: %w", dispatchErr)
		}
		dispatchService = service
	}

	// Assigned inside the admin block; its persisted users are applied after
	// the SMPPs server is constructed.
	var smppsUserService *admin.SMPPsUserService
	// The PB facade is composed before the SMPPs server to keep all admin
	// services in one block. This late-bound slot receives the server before
	// any PB listener starts accepting requests.

	mux := http.NewServeMux()
	mux.Handle("/health", runtime.healthHandler())
	mux.Handle("/ready", runtime.healthHandler())
	mux.Handle("/live", livenessHandler())
	// The runtime provisioning plane mounts at /admin, applying changes live
	// through the same manager and replaying them at boot/promotion. The local
	// adapter is SQLite; HA uses the namespace-isolated PostgreSQL adapter.
	// Config connectors are reserved; admin manages an additive set.
	if config.Admin != nil {
		var (
			store    *admin.Store
			storeErr error
		)
		if config.HA != nil {
			store, storeErr = admin.OpenPostgresStore(ctx, config.Outbound.PostgresDSN, config.HA.Namespace)
		} else {
			store, storeErr = admin.OpenStore(ctx, config.Admin.DBPath)
		}
		if storeErr != nil {
			return nil, fmt.Errorf("open admin store: %w", storeErr)
		}
		runtime.adminStore = store
		adminService, adminErr := admin.NewService(store, manager, connectorIDs(),
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if adminErr != nil {
			return nil, fmt.Errorf("build admin service: %w", adminErr)
		}
		if applyErr := adminService.LoadAndApply(ctx); applyErr != nil {
			// Persisted connectors that fail to re-apply are logged, not fatal:
			// the gateway still serves config connectors and the admin API.
			slog.Default().Error("admin: load persisted connectors: " + applyErr.Error())
		}
		// Termination connectors get their own admin service: they are persisted
		// in their own table and driven through their own manager, because an
		// SMPP connector is defined by a bind and this one by a verdict source
		// and an endpoint. It stays nil when the gateway declares no termination
		// section, and the admin surfaces answer 404 rather than an empty list —
		// "this gateway does not terminate traffic" is a different statement
		// from "it terminates none right now".
		var terminationService *admin.TerminationService
		if runtime.termination != nil {
			terminationManager := runtime.termination.Manager()
			terminationService, adminErr = admin.NewTerminationService(store, terminationManager,
				terminationManager.ReservedCIDs(),
				func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
			if adminErr != nil {
				return nil, fmt.Errorf("build admin termination service: %w", adminErr)
			}
			if applyErr := terminationService.LoadAndApply(ctx); applyErr != nil {
				// Same policy as the SMPP connectors above: a persisted connector
				// that cannot re-apply is logged, not fatal, so config-declared
				// connectors and the admin API keep serving.
				slog.Default().Error("admin: load persisted termination connectors: " + applyErr.Error())
			}
		}

		// The pull credentials live beside the spool, not in the admin store:
		// their scope names connectors whose rows are in the spool's database,
		// and a credential that could outlive or diverge from the spool it
		// authorizes reads against is one nobody can reason about. So this
		// service exists exactly when the spool does, and both management
		// surfaces answer 404 otherwise.
		var messageConsumerService *admin.MessageConsumerService
		if consumers := runtime.termination.Consumers(); consumers != nil {
			messageConsumerService, adminErr = admin.NewMessageConsumerService(consumers)
			if adminErr != nil {
				return nil, fmt.Errorf("build admin message consumer service: %w", adminErr)
			}
		}

		// Route provisioning: admin persists opaque route JSON and drives the
		// outbound runtime (the RouteProvisioner) to rebuild+swap the live
		// table. The adapter parses each JSON into an outbound.RouteConfig here,
		// where both packages are in scope.
		routeService, routeErr := admin.NewRouteService(store, outboundRouteProvisioner{outboundRuntime},
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if routeErr != nil {
			return nil, fmt.Errorf("build admin route service: %w", routeErr)
		}
		// The named registries and profile snapshots are store-only, so they
		// need no provisioner and cannot fail to apply.
		filterService, filterErr := admin.NewFilterService(store,
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if filterErr != nil {
			return nil, fmt.Errorf("build admin filter service: %w", filterErr)
		}
		httpConnectorService, httpccErr := admin.NewHTTPConnectorService(store,
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if httpccErr != nil {
			return nil, fmt.Errorf("build admin http connector service: %w", httpccErr)
		}
		profileService, profileErr := admin.NewProfileService(store,
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if profileErr != nil {
			return nil, fmt.Errorf("build admin profile service: %w", profileErr)
		}
		// Group provisioning runs before users: a user resolves its group by
		// gid at apply time, so a persisted group must be live first or every
		// grouped user fails to re-add at boot.
		groupService, groupErr := admin.NewGroupService(store,
			outboundGroupProvisioner{runtime: outboundRuntime, configGroups: int64(len(outboundRuntime.ConfigGroupIDs()))},
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if groupErr != nil {
			return nil, fmt.Errorf("build admin group service: %w", groupErr)
		}
		if applyErr := groupService.LoadAndApply(ctx); applyErr != nil {
			slog.Default().Error("admin: load persisted groups: " + applyErr.Error())
		}
		// User provisioning: admin persists opaque user JSON + a stable uid and
		// installs it in the live billing directory. The floor is the config
		// user count so admin uids never collide with config index-based uids.
		userService, userErr := admin.NewUserService(store,
			outboundUserProvisioner{runtime: outboundRuntime, configUsers: int64(len(outboundRuntime.ConfigUsernames()))},
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if userErr != nil {
			return nil, fmt.Errorf("build admin user service: %w", userErr)
		}
		if applyErr := userService.LoadAndApply(ctx); applyErr != nil {
			slog.Default().Error("admin: load persisted users: " + applyErr.Error())
		}
		// Routes are replayed only after groups and users. UserFilter and
		// GroupFilter resolve their legacy names to live numeric ids while the
		// table is built; replaying routes earlier silently stranded otherwise
		// valid persisted routes on every restart.
		if applyErr := routeService.LoadAndApply(ctx); applyErr != nil {
			slog.Default().Error("admin: load persisted routes: " + applyErr.Error())
		}
		// MO route provisioning: same apply-first-then-persist chain as MT
		// routes, driving the MO dispatch table built above.
		moRouteService, moRouteErr := admin.NewMORouteService(store, moRouteProvisioner{service: dispatchService},
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if moRouteErr != nil {
			return nil, fmt.Errorf("build admin MO route service: %w", moRouteErr)
		}
		if applyErr := moRouteService.LoadAndApply(ctx); applyErr != nil {
			slog.Default().Error("admin: load persisted MO routes: " + applyErr.Error())
		}
		// Deliberately here rather than in NewService: persisted routes have only
		// just been applied, so this is the first point the effective table is
		// known. Warning earlier would fire for a deployment whose default route
		// is stored rather than configured.
		dispatchService.WarnIfNoDefaultRoute()
		moDefaultRouteWarned = true
		// SMPPs bind users: the server is built further down, so the
		// provisioner resolves it at apply time and LoadAndApply runs after it
		// exists (below the SMPPS block).
		service, smppsUserErr := admin.NewSMPPsUserService(store, smppsUserProvisioner{runtime: runtime},
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if smppsUserErr != nil {
			return nil, fmt.Errorf("build admin SMPPs user service: %w", smppsUserErr)
		}
		smppsUserService = service
		// Deleting a customer must close their bind door too, not just their
		// sending credential. Without this cascade the bind account survived:
		// credentials still authenticated, the session held a max_bindings slot
		// and showed as live, and submits were answered ESME_RSYSERR, which reads
		// as a server fault rather than a closed account.
		userService.SetSMPPsAccounts(smppsBindCascade{runtime: runtime, accounts: smppsUserService})
		// Interceptor provisioning is opt-in: the scripts are arbitrary Python
		// run on this host, so the service only exists when the operator set
		// allow_interceptor_editing.
		var interceptorService *admin.InterceptorService
		if config.Admin.AllowInterceptorEditing {
			service, interceptorErr := admin.NewInterceptorService(store,
				interceptorProvisioner{outbound: outboundRuntime, gateway: runtime},
				func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
			if interceptorErr != nil {
				return nil, fmt.Errorf("build admin interceptor service: %w", interceptorErr)
			}
			if applyErr := service.LoadAndApply(ctx); applyErr != nil {
				slog.Default().Error("admin: load persisted interceptors: " + applyErr.Error())
			}
			interceptorService = service
		}
		// The commercial surface rides the admin API too: it is the automation
		// side of the same data the console renders (scheduled usage pulls,
		// exports into an accounting system).
		configAccounts := func() []admin.ProvisionedAccount {
			accounts := make([]admin.ProvisionedAccount, 0, len(config.Outbound.Users))
			for _, user := range config.Outbound.Users {
				accounts = append(accounts, admin.ProvisionedAccount{
					Username:                     user.Username,
					ManagedBy:                    "config",
					GroupID:                      user.GroupID,
					Disabled:                     user.Disabled,
					Balance:                      user.Balance,
					SubmitSMCount:                user.SubmitSMCount,
					EarlyDecrementBalancePercent: user.EarlyDecrementBalancePercent,
				})
			}
			return accounts
		}
		adminHandler, handlerErr := admin.NewHandler(adminService, routeService, userService, config.Admin.Token,
			admin.WithBilling(outboundRuntime.CDRService(), outboundRuntime.BalanceReader(), configAccounts),
			admin.WithTerminationConnectors(terminationService),
			admin.WithMessageConsumers(messageConsumerService))
		if handlerErr != nil {
			return nil, fmt.Errorf("build admin handler: %w", handlerErr)
		}
		// /admin/ creates users, changes balances and starts connectors. When it
		// has its own listener it must NOT also stay on the public sendsms mux,
		// or isolating it achieves nothing.
		if config.Admin.APIListenAddress != "" {
			adminAPIMux := http.NewServeMux()
			adminAPIMux.Handle("/admin/", adminHandler.Routes())
			// The modern metrics surface rides the admin listener, not the public
			// send port: it carries per-user and per-connector labels that are
			// operational detail, and the legacy /metrics text format is frozen
			// and cannot carry new series.
			adminAPIMux.Handle("/metrics/prometheus", stats.DefaultPrometheus().Handler())
			runtime.AdminAPIHandler = adminAPIMux
			runtime.AdminAPIListenAddress = config.Admin.APIListenAddress
		} else {
			mux.Handle("/admin/", adminHandler.Routes())
			slog.Default().Warn("admin API is served on the public sendsms listener; " +
				"set admin.api_listen_address (e.g. 127.0.0.1:8405) to give it the same " +
				"boundary as the web UI and jCli")
		}

		// Operator overrides for the few settings whose consumers can re-read
		// them at runtime. Applied at boot before the UI is served, so a value an
		// operator set survives a restart rather than quietly reverting to the
		// file — which would be the same "it did not stick" failure the read-only
		// card was protecting against.
		settingsService, settingsErr := admin.NewSettingsService(store,
			settingsApplier{outbound: outboundRuntime},
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if settingsErr != nil {
			return nil, fmt.Errorf("build admin settings service: %w", settingsErr)
		}
		if overrides, overridesErr := settingsService.Overrides(ctx); overridesErr != nil {
			slog.Default().Error("admin: load setting overrides: " + overridesErr.Error())
		} else {
			applier := settingsApplier{outbound: outboundRuntime}
			for name, value := range overrides {
				if err := applier.ApplySetting(name, value); err != nil {
					slog.Default().Error("admin: apply stored setting override",
						"setting", name, "value", value, "error", err)
				}
			}
		}

		// The browser management UI, when configured, is served on its own
		// listener (WebListenAddress) so it never shares the public sendsms port.
		// It renders against the same in-process admin services.
		if config.Admin.WebListenAddress != "" {
			webHandler, webErr := adminweb.New(adminweb.Deps{
				Connectors:            adminService,
				Routes:                routeService,
				MORoutes:              moRouteService,
				Users:                 userService,
				Groups:                groupService,
				SMPPsUsers:            smppsUserService,
				Filters:               filterService,
				HTTPConnectors:        httpConnectorService,
				Profiles:              profileService,
				Transactions:          transactions,
				BalanceReader:         outboundRuntime.BalanceReader(),
				RateReader:            outboundRuntime.RateReader(),
				Submitter:             outboundRuntime.Submitter(),
				HTTPStats:             outboundRuntime.HTTPStats(),
				SMPPcStats:            smppcStats,
				SMPPsStats:            smppsStats,
				StartedAt:             func() time.Time { return startedAt },
				ConnectorIDs:          managedConnectorIDs(manager),
				ConfigConnectors:      func() []smppc.Config { return config.Connectors },
				TerminationConnectors: terminationService,
				ConfigTerminationConnectors: func() []termination.ConnectorConfig {
					return config.terminationConnectors()
				},
				TerminationStatus: terminationStatusFunc(runtime.termination),
				MessageConsumers:  messageConsumerService,
				// Nil when this deployment spools nothing, which is what makes
				// /api/messages answer 404 rather than an empty list.
				Messages:       runtime.termination.Spool(),
				ConfigRoutes:   outboundRuntime.ConfigRoutes,
				ConfigMORoutes: func() []modispatch.RouteConfig { return config.MORoutes },
				ConfigUsers:    func() []outbound.UserConfig { return config.Outbound.Users },
				ConfigGroups:   func() []outbound.GroupConfig { return config.Outbound.Groups },
				ConfigSMPPsUsers: func() []smppsserver.UserConfig {
					if config.SMPPS == nil {
						return nil
					}
					return config.SMPPS.Users
				},
				ConnectorStatus: manager.Status,
				UnbindSMPPsUser: func(systemID string) int {
					if runtime.smppsServer == nil {
						return 0
					}
					return runtime.smppsServer.Server().UnbindUser(systemID)
				},
				Interceptors: interceptorService, // nil unless explicitly enabled
				CDR:          outboundRuntime.CDRService(),
				Settings:     settingsService,
				GroupQuota: func(gid string) (adminweb.LiveQuota, bool) {
					quota, ok := outboundRuntime.GroupQuota(gid)
					if !ok {
						return adminweb.LiveQuota{}, false
					}
					return adminweb.LiveQuota{Balance: quota.Balance, SubmitSMCount: quota.SubmitSmCount}, true
				},
				BillingSettings: func() adminweb.BillingSettings {
					return adminweb.BillingSettings{
						Currency:                    config.Outbound.CDRCurrency,
						RetentionDays:               config.Outbound.CDRRetentionDays,
						RetentionBatchSize:          config.Outbound.CDRRetentionBatchSize,
						MaintenanceIntervalSeconds:  config.Outbound.CDRMaintenanceIntervalSeconds,
						QuotaPersistIntervalSeconds: config.Outbound.QuotaPersistIntervalSeconds,
					}
				},
				Ingress:  func() adminweb.IngressSnapshot { return ingressSnapshot(config) },
				Health:   runtime.healthProbe(),
				Username: config.Admin.WebUsername,
				Password: config.Admin.WebPassword,
				Secure:   config.HTTPS != nil,
			})
			if webErr != nil {
				return nil, fmt.Errorf("build admin web UI: %w", webErr)
			}
			runtime.WebHandler = webHandler
			runtime.WebListenAddress = config.Admin.WebListenAddress
		}

		// The jCli console is the third face over the same services. Like the
		// web UI it is inert unless an address is configured.
		if config.Admin.JCliListenAddress != "" {
			console, consoleErr := jcli.NewServer(config.Admin.JCliListenAddress, jcli.Deps{
				Connectors:     adminService,
				Routes:         routeService,
				MORoutes:       moRouteService,
				Users:          userService,
				Groups:         groupService,
				SMPPsUsers:     smppsUserService,
				Filters:        filterService,
				HTTPConnectors: httpConnectorService,
				Interceptors:   interceptorService,
				Profiles:       profileService,
				// nil on a gateway with no termination connector, which makes
				// `msgconsumer` say so instead of failing obscurely.
				MessageConsumers: messageConsumerService,
				HTTPStats:        outboundRuntime.HTTPStats(),
				SMPPcStats:       smppcStats,
				SMPPsStats:       smppsStats,
				StartedAt:        func() time.Time { return startedAt },
				// Config-owned entities: without these the console reports an
				// empty gateway on a config-file deployment, which is the
				// normal one.
				ConfigConnectors: func() []smppc.Config { return config.Connectors },
				ConfigRoutes:     outboundRuntime.ConfigRoutes,
				ConfigUsers:      func() []outbound.UserConfig { return config.Outbound.Users },
				ConnectorStatus:  manager.Status,
				UnbindSMPPsUser: func(systemID string) int {
					// Resolved at call time: the SMPPs server is built after
					// the console and may be absent entirely.
					if runtime.smppsServer == nil {
						return 0
					}
					return runtime.smppsServer.Server().UnbindUser(systemID)
				},
				Username:    config.Admin.JCliUsername,
				Password:    config.Admin.JCliPassword,
				IdleTimeout: time.Duration(config.Admin.JCliIdleTimeoutSeconds * float64(time.Second)),
			}, slog.Default())
			if consoleErr != nil {
				return nil, fmt.Errorf("start jCli console: %w", consoleErr)
			}
			runtime.jcli = console
			go func() { _ = console.Serve(workerCtx) }()
		}
	}
	// Only now is the complete principal set known: config users/groups were
	// installed by the outbound runtime and persisted admin users/groups were
	// replayed above. Pruning earlier would delete valid admin quota rows;
	// skipping it lets a deleted username inherit its former spent balance when
	// recreated after a restart. Fail startup closed on a pruning error because
	// account deletion is a money boundary, not best-effort housekeeping.
	if _, pruneErr := outboundRuntime.PruneDurableQuotas(ctx); pruneErr != nil {
		return nil, pruneErr
	}
	mux.Handle("/", outboundRuntime.Handler)
	runtime.Handler = mux
	if dispatchService != nil {
		go func() { _ = dispatchService.Run(workerCtx) }()
	}
	if config.DLRLookup != nil {
		lookupConfig := *config.DLRLookup
		if lookupConfig.AMQPURL == "" {
			lookupConfig.AMQPURL = config.Outbound.AMQPURL
		}
		lookupConfig.AMQPDurableTopology = lookupConfig.AMQPDurableTopology || config.AMQPDurableTopology
		lookupService, lookupErr := dlrlookup.NewService(
			lookupConfig,
			dlrlookup.WithFinalDLRRecorder(repository, nil),
		)
		if lookupErr != nil {
			return nil, fmt.Errorf("start DLR lookup worker: %w", lookupErr)
		}
		// Surface per-delivery failures: a dropped DLR correlation (map not
		// found, malformed record) is otherwise silent — the same blind spot
		// that hid earlier drops.
		lookupService.OnError = func(err error) {
			dlrLogger.Error("DLR lookup failed: " + err.Error())
			amqpLogger.Error("DLR lookup worker failed: " + err.Error())
		}
		runtime.dlrLookup = lookupService
		dlrLogger.Info("DLRLookup configured and ready.")
		go func() { _ = lookupService.Run(workerCtx) }()
	}
	// The SMPPS server is built before the DLR thrower so a receipt sink over
	// its Deliver path can be injected into the thrower — dlr_thrower.smpps
	// forwards then push a deliver_sm receipt down the sender's bound session.
	var dlrReceiptSink dlr.SMPPSReceiptSink
	var moDeliverySink mo.MODeliverySink
	if config.SMPPS != nil {
		smppServerLogger := logging.Logger("smpp.server", logging.Config{
			Level:  config.SMPPServerLog.Level,
			File:   config.SMPPServerLog.File,
			Rotate: config.SMPPServerLog.Rotate,
		})
		smppsService, smppsErr := smppsserver.NewService(*config.SMPPS, outboundRuntime.Submitter(),
			smppsserver.WithStats(smppsStats), smppsserver.WithLogger(smppServerLogger))
		if smppsErr != nil {
			return nil, fmt.Errorf("start SMPPS server: %w", smppsErr)
		}
		runtime.smppsServer = smppsService
		sink, sinkErr := smppsdelivery.NewReceiptSink(smppsService.Server())
		if sinkErr != nil {
			return nil, fmt.Errorf("wire SMPPS receipt delivery: %w", sinkErr)
		}
		dlrReceiptSink = sink
		moSink, moSinkErr := smppsdelivery.NewMOSink(smppsService.Server())
		if moSinkErr != nil {
			return nil, fmt.Errorf("wire SMPPS MO delivery: %w", moSinkErr)
		}
		moDeliverySink = moSink
		go func() { _ = smppsService.Run(workerCtx) }()
	}
	// Now that the SMPPs directory exists, install the persisted admin bind
	// users. Deferred to here because the provisioner needs the live server.
	if smppsUserService != nil && runtime.smppsServer != nil {
		if applyErr := smppsUserService.LoadAndApply(ctx); applyErr != nil {
			slog.Default().Error("admin: load persisted SMPPs users: " + applyErr.Error())
		}
	}
	// A config-only deployment has no admin block to warn from, and it is the one
	// that most needs the warning: without admin there is no way to add the missing
	// route later without an edit and a restart.
	if dispatchService != nil && !moDefaultRouteWarned {
		dispatchService.WarnIfNoDefaultRoute()
	}
	if config.DLRThrower != nil {
		throwerConfig := *config.DLRThrower
		if throwerConfig.AMQPURL == "" {
			throwerConfig.AMQPURL = config.Outbound.AMQPURL
		}
		throwerConfig.AMQPDurableTopology = throwerConfig.AMQPDurableTopology || config.AMQPDurableTopology
		var throwerOpts []dlrthrower.Option
		if dlrReceiptSink != nil {
			throwerOpts = append(throwerOpts, dlrthrower.WithSMPPSReceiptSink(dlrReceiptSink))
		}
		throwerService, throwerErr := dlrthrower.NewService(throwerConfig, throwerOpts...)
		if throwerErr != nil {
			return nil, fmt.Errorf("start DLR thrower worker: %w", throwerErr)
		}
		runtime.dlrThrower = throwerService
		throwerService.OnError = func(err error) {
			dlrThrowerLogger.Error("DLR throw failed: " + err.Error())
			amqpLogger.Error("DLR thrower worker failed: " + err.Error())
		}
		dlrThrowerLogger.Info("DLRThrower configured and ready.")
		go func() { _ = throwerService.Run(workerCtx) }()
	}
	if config.MOThrower != nil {
		moConfig := *config.MOThrower
		if moConfig.AMQPURL == "" {
			moConfig.AMQPURL = config.Outbound.AMQPURL
		}
		moConfig.AMQPDurableTopology = moConfig.AMQPDurableTopology || config.AMQPDurableTopology
		var moOpts []mothrower.Option
		if moDeliverySink != nil {
			moOpts = append(moOpts, mothrower.WithSMPPSDeliverySink(moDeliverySink))
		}
		moService, moErr := mothrower.NewService(moConfig, bridge, moOpts...)
		if moErr != nil {
			return nil, fmt.Errorf("start MO thrower worker: %w", moErr)
		}
		runtime.moThrower = moService
		moService.OnError = func(err error) {
			deliverSMThrowerLogger.Error("deliver_sm throw failed: " + err.Error())
			amqpLogger.Error("deliver_sm thrower worker failed: " + err.Error())
		}
		deliverSMThrowerLogger.Info("deliverSmThrower configured and ready.")
		go func() { _ = moService.Run(workerCtx) }()
	}
	if err := manager.StartAll(); err != nil {
		return nil, fmt.Errorf("start connectors: %w", err)
	}
	if err := waitRequiredBound(ctx, manager, config.RequiredConnectors(), config.BindTimeout()); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (runtime *Runtime) Manager() *smppc.Manager {
	if runtime == nil {
		return nil
	}
	return runtime.manager
}

// TerminationManager exposes the MT termination connector manager, for the
// management surfaces. Nil when no termination section is configured, which
// those surfaces must treat as "this gateway does not terminate" rather than as
// an empty connector list.
//
// The reserved set a management service needs is Manager.ReservedCIDs().
func (runtime *Runtime) TerminationManager() *termination.Manager {
	if runtime == nil {
		return nil
	}
	return runtime.termination.Manager()
}

// MessageSpool exposes the audited read/prune boundary over spooled message
// content. Management surfaces must go through it and never through the
// repository: it is where "every read of message text writes an audit row
// naming the actor" is enforced.
// installMessagePull fills the late-bound slot the REST listener resolves. A
// nil handler leaves it empty, so GET /messages keeps answering 404 rather than
// panicking on a typed nil.
func (runtime *Runtime) installMessagePull(handler http.Handler) {
	if runtime == nil || handler == nil {
		return
	}
	runtime.messagePull.Store(handler)
}

// resolveMessagePull is what restcompat calls per request.
func (runtime *Runtime) resolveMessagePull() http.Handler {
	if runtime == nil {
		return nil
	}
	handler, _ := runtime.messagePull.Load().(http.Handler)
	return handler
}

func (runtime *Runtime) MessageSpool() *msgspool.Service {
	if runtime == nil {
		return nil
	}
	return runtime.termination.Spool()
}

// LeadershipLost is nil when HA is disabled; otherwise it closes when the
// PostgreSQL session fence is lost or the runtime closes.
func (runtime *Runtime) LeadershipLost() <-chan struct{} {
	if runtime == nil || runtime.leadership == nil {
		return nil
	}
	return runtime.leadership.Lost()
}

// LeadershipError reports why LeadershipLost closed.
func (runtime *Runtime) LeadershipError() error {
	if runtime == nil || runtime.leadership == nil {
		return nil
	}
	return runtime.leadership.Err()
}

func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.closeOnce.Do(func() {
		var errs []error
		// Admission is stopped by the executable before Runtime.Close. Stop the
		// outbox/consumers and confirming publisher before fencing connectors.
		// The SMPPS server is MT ingress feeding the outbound submitter, so it
		// stops first — no new submit reaches the pipeline being torn down.
		// The management console is closed first: it is an operator surface, not
		// part of the message path, so it must not accept new commands while
		// the rest of the runtime tears down.
		if runtime.jcli != nil {
			if err := runtime.jcli.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.smppsServer != nil {
			if err := runtime.smppsServer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		// Before the outbound runtime: the receipt runner publishes its legs
		// through that runtime's publisher, so closing it first would strand
		// receipts that are already due.
		if runtime.termination != nil {
			if err := runtime.termination.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.outbound != nil {
			if err := runtime.outbound.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.manager != nil {
			if err := runtime.manager.StopAll(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.workerCancel != nil {
			runtime.workerCancel()
		}
		if runtime.dlrRedisClose != nil {
			runtime.dlrRedisClose()
		}
		if runtime.adminStore != nil {
			if err := runtime.adminStore.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.interceptorRunner != nil {
			if err := runtime.interceptorRunner.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.dlrLookup != nil {
			if err := runtime.dlrLookup.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.bridge != nil {
			if err := runtime.bridge.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if runtime.store != nil {
			if err := runtime.store.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		// Release leadership last. A standby must not become active while this
		// process still has message workers or mutable billing objects running.
		if runtime.leadership != nil {
			if err := runtime.leadership.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		runtime.closeErr = errors.Join(errs...)
	})
	return runtime.closeErr
}

func waitRequiredBound(parent context.Context, manager *smppc.Manager, required []string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		pending := make([]string, 0, len(required))
		for _, cid := range required {
			status, err := manager.Status(cid)
			if err != nil {
				return fmt.Errorf("read connector %q status: %w", cid, err)
			}
			if status.Observed != smppc.StatusBound {
				pending = append(pending, fmt.Sprintf("%s=%s", cid, status.Observed))
			}
		}
		if len(pending) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			sort.Strings(pending)
			return fmt.Errorf("required SMPPc connectors not bound (%v): %w", pending, ctx.Err())
		case <-ticker.C:
		}
	}
}

// configuredConnectorIDs returns a stable-order connector-id lister for the
// smppc /metrics labels.
func configuredConnectorIDs(connectors []smppc.Config) func() []string {
	ids := make([]string, 0, len(connectors))
	for _, connector := range connectors {
		ids = append(ids, connector.CID)
	}
	return func() []string { return append([]string(nil), ids...) }
}

// smppsBindCascade removes a deleted customer's SMPPs bind account AND drops any
// session already bound with it.
//
// Both halves are needed. Removing the account alone only prevents NEW binds --
// authentication is resolved at bind time, so an established session survives
// (internal/app/smppsserver/directory.go:109). A receiver bind left standing for a
// deleted customer can still be selected as an MO or receipt destination, which is
// the one way this could leak traffic rather than merely confuse. The web console's
// Ban action already pairs the two the same way.
type smppsBindCascade struct {
	runtime  *Runtime
	accounts *admin.SMPPsUserService
}

func (c smppsBindCascade) DeleteUser(ctx context.Context, systemID string) error {
	if err := c.accounts.DeleteUser(ctx, systemID); err != nil {
		return err
	}
	if c.runtime != nil && c.runtime.smppsServer != nil {
		if server := c.runtime.smppsServer.Server(); server != nil {
			server.UnbindUser(systemID)
		}
	}
	return nil
}

func componentLogger(name string, config ComponentLogConfig) *slog.Logger {
	return logging.Logger(name, logging.Config{
		Level: config.Level, File: config.File, Rotate: config.Rotate,
	})
}

// managedConnectorIDs lists the connector table the gateway is actually
// running, including admin-provisioned connectors added after boot.
func managedConnectorIDs(manager *smppc.Manager) func() []string {
	return func() []string {
		configs := manager.List()
		ids := make([]string, 0, len(configs))
		for _, connector := range configs {
			ids = append(ids, connector.CID)
		}
		return ids
	}
}

// ingressSnapshot reports the customer-facing listeners this deployment
// publishes, for the console's generated partner integration instructions.
//
// It reads the configuration rather than the bound sockets on purpose: a socket
// bound to 0.0.0.0 tells nobody how to reach it, and the honest answer that the
// console needs is "which ingress exists, on which port, and did the operator
// declare a public hostname". The callback policy is reported as configured,
// with only the HTTP client's own 30-second fallback applied — an unset
// max_retries in JSON really is zero retries, unlike the legacy .cfg defaults.
func ingressSnapshot(config Config) adminweb.IngressSnapshot {
	snapshot := adminweb.IngressSnapshot{
		PublicHostname:    strings.TrimSpace(config.PublicHostname),
		HTTPBindAddress:   config.Outbound.ListenAddress,
		HTTPTLS:           config.HTTPS != nil,
		RESTBindAddress:   config.REST.ListenAddress,
		DLRThrowerRunning: config.DLRThrower != nil,
		MOThrowerRunning:  config.MOThrower != nil,
	}
	if config.SMPPS != nil {
		snapshot.SMPPSBindAddress = config.SMPPS.BindAddr
		snapshot.SMPPSTLS = config.SMPPS.TLSCertFile != "" && config.SMPPS.TLSKeyFile != ""
		snapshot.SMPPSEnquireLinkTimeout = config.SMPPS.EnquireLinkTimeoutSeconds
		snapshot.SMPPSInactivityTimeout = config.SMPPS.InactivityTimeoutSeconds
	}
	if thrower := config.DLRThrower; thrower != nil {
		snapshot.CallbackTimeoutSeconds = thrower.HTTPTimeoutSeconds
		if snapshot.CallbackTimeoutSeconds == 0 {
			snapshot.CallbackTimeoutSeconds = 30
		}
		snapshot.CallbackRetryDelaySeconds = thrower.RetryDelaySeconds
		snapshot.CallbackMaxRetries = thrower.MaxRetries
	}
	return snapshot
}

// settingsApplier applies an operator's setting override to the live runtime.
// Every case must genuinely take effect now: a setting that only the next
// restart would honour belongs in the configuration file, not here.
type settingsApplier struct {
	outbound *outbound.Runtime
}

func (a settingsApplier) ApplySetting(name string, value int) error {
	switch name {
	case admin.SettingCDRRetentionDays:
		return a.outbound.SetCDRRetention(value, a.outbound.CDRRetentionBatch())
	case admin.SettingCDRRetentionBatchSize:
		return a.outbound.SetCDRRetention(a.outbound.CDRRetentionDays(), value)
	case admin.SettingCDRMaintenanceIntervalSeconds:
		return a.outbound.SetCDRMaintenanceInterval(value)
	case admin.SettingQuotaPersistIntervalSeconds:
		return a.outbound.SetQuotaPersistInterval(value)
	default:
		return admin.ErrSettingUnknown
	}
}
