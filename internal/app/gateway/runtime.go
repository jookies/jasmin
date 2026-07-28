package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"log/slog"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/adminweb"
	"github.com/pumpitspace/jasmin/internal/app/dlrlookup"
	"github.com/pumpitspace/jasmin/internal/app/dlrthrower"
	"github.com/pumpitspace/jasmin/internal/app/jcli"
	"github.com/pumpitspace/jasmin/internal/app/modispatch"
	"github.com/pumpitspace/jasmin/internal/app/mothrower"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/app/smppsdelivery"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/logging"
	"github.com/pumpitspace/jasmin/internal/core/mo"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/stats"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/infra/storage"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/pyintercept"
)

type ManagerFactory func(string) *smppc.Manager

type Runtime struct {
	Handler            http.Handler
	WebHandler         http.Handler // admin web UI, served on WebListenAddress (nil when disabled)
	WebListenAddress   string
	manager            *smppc.Manager
	outbound           *outbound.Runtime
	bridge             picklecompat.Codec
	store              *storage.PostgresSubmitTransactionRepository
	dlrLookup          *dlrlookup.Service
	dlrThrower         *dlrthrower.Service
	moThrower          *mothrower.Service
	smppsServer        *smppsserver.Service
	requiredConnectors []string
	dlrRedisClose      func()
	adminStore         *admin.Store
	jcli               *jcli.Server
	interceptorRunner  *pyintercept.Runner
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
	repository, err := storage.OpenPostgresSubmitTransactionRepository(ctx, config.Outbound.PostgresDSN)
	if err != nil {
		workerCancel()
		return nil, err
	}
	runtime := &Runtime{store: repository, workerCancel: workerCancel}
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
	// Select the pickle codec: the native Go codec (no subprocess, the default) or
	// the legacy Python bridge (opt-in via pickle_codec: "bridge" for a fallback).
	var bridge picklecompat.Codec
	if config.PickleCodec == "bridge" {
		bridgeImpl, bridgeErr := picklecompat.NewBridge(workerCtx, config.Outbound.PythonPath)
		if bridgeErr != nil {
			return nil, fmt.Errorf("start trusted pickle bridge: %w", bridgeErr)
		}
		bridge = bridgeImpl
	} else {
		bridge = picklecompat.NewNativeCodec()
	}
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
	// Deferred deliver-ingest wiring: the publisher and multipart store are
	// built with the outbound runtime (below), after this factory. Capturing
	// them by reference lets connectors created LATER — the admin-provisioned
	// ones — receive MO/DLR too, not just the config connectors wired
	// explicitly once the runtime exists.
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
	smppcStats := stats.NewSMPPcRegistry()
	smppsStats := &stats.SMPPsStats{}
	connectorIDs := configuredConnectorIDs(config.Connectors)
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
	outboundRuntime, err := outbound.NewRuntimeWithDependencies(workerCtx, config.Outbound, outbound.RuntimeDependencies{
		Bridge: bridge, Transactions: transactions, Repository: repository, ConnectorAvailable: manager.Available,
		SMPPcStats: smppcStats, SMPPsStats: smppsStats, ConnectorIDs: connectorIDs,
		DLRLookupPID: dlrLookupPID, ConnectorPDUDefaults: pduDefaultsProvider,
		DLRRequestStore: dlrRequestStore, ConnectorDLRExpiry: dlrExpiryProvider,
		InterceptorRunner: interceptorRunner,
	})
	if err != nil {
		return nil, fmt.Errorf("start outbound runtime: %w", err)
	}
	runtime.outbound = outboundRuntime
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
	// /health (real readiness) rides beside the legacy-parity endpoints; the
	// outbound handler keeps everything else, including the unconditional /ping.
	// MO dispatch is constructed before the admin plane because the admin MO
	// route service provisions through it. It also starts when admin is enabled
	// but config declares no MO routes, so routes can be added at runtime — an
	// MO with no matching route is unroutable, which is the same outcome as
	// having no dispatcher at all.
	var dispatchService *modispatch.Service
	if len(config.MORoutes) > 0 || config.Admin != nil {
		dispatchConfig := modispatch.Config{
			AMQPURL:             config.Outbound.AMQPURL,
			AMQPDurableTopology: config.Outbound.AMQPDurableTopology,
			Routes:              config.MORoutes,
		}
		service, dispatchErr := modispatch.NewService(ctx, dispatchConfig, bridge,
			modispatch.WithOnError(func(err error) { slog.Default().Error("modispatch: " + err.Error()) }))
		if dispatchErr != nil {
			return nil, fmt.Errorf("start MO dispatch: %w", dispatchErr)
		}
		dispatchService = service
	}

	// Assigned inside the admin block; its persisted users are applied after
	// the SMPPs server is constructed.
	var smppsUserService *admin.SMPPsUserService

	mux := http.NewServeMux()
	mux.Handle("/health", runtime.healthHandler())
	// The runtime provisioning plane (SQLite-backed connector CRUD) mounts at
	// /admin, applying changes live through the same manager and re-applying
	// persisted connectors at boot. Config connectors are reserved (config
	// owns them); admin manages an additive set.
	if config.Admin != nil {
		store, storeErr := admin.OpenStore(ctx, config.Admin.DBPath)
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
		// Route provisioning: admin persists opaque route JSON and drives the
		// outbound runtime (the RouteProvisioner) to rebuild+swap the live
		// table. The adapter parses each JSON into an outbound.RouteConfig here,
		// where both packages are in scope.
		routeService, routeErr := admin.NewRouteService(store, outboundRouteProvisioner{outboundRuntime},
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if routeErr != nil {
			return nil, fmt.Errorf("build admin route service: %w", routeErr)
		}
		if applyErr := routeService.LoadAndApply(ctx); applyErr != nil {
			slog.Default().Error("admin: load persisted routes: " + applyErr.Error())
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
		// SMPPs bind users: the server is built further down, so the
		// provisioner resolves it at apply time and LoadAndApply runs after it
		// exists (below the SMPPS block).
		service, smppsUserErr := admin.NewSMPPsUserService(store, smppsUserProvisioner{runtime: runtime},
			func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
		if smppsUserErr != nil {
			return nil, fmt.Errorf("build admin SMPPs user service: %w", smppsUserErr)
		}
		smppsUserService = service
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
		adminHandler, handlerErr := admin.NewHandler(adminService, routeService, userService, config.Admin.Token)
		if handlerErr != nil {
			return nil, fmt.Errorf("build admin handler: %w", handlerErr)
		}
		mux.Handle("/admin/", adminHandler.Routes())

		// The browser management UI, when configured, is served on its own
		// listener (WebListenAddress) so it never shares the public sendsms port.
		// It renders against the same in-process admin services.
		if config.Admin.WebListenAddress != "" {
			webHandler, webErr := adminweb.New(adminweb.Deps{
				Connectors:   adminService,
				Routes:       routeService,
				MORoutes:     moRouteService,
				Users:        userService,
				SMPPsUsers:   smppsUserService,
				Interceptors: interceptorService, // nil unless explicitly enabled
				Health:       runtime.healthProbe(),
				Username:     config.Admin.WebUsername,
				Password:     config.Admin.WebPassword,
				Secure:       config.HTTPS != nil,
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
				Connectors:  adminService,
				Routes:      routeService,
				MORoutes:    moRouteService,
				Users:       userService,
				Groups:      groupService,
				SMPPsUsers:  smppsUserService,
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
		lookupService, lookupErr := dlrlookup.NewService(lookupConfig)
		if lookupErr != nil {
			return nil, fmt.Errorf("start DLR lookup worker: %w", lookupErr)
		}
		// Surface per-delivery failures: a dropped DLR correlation (map not
		// found, malformed record) is otherwise silent — the same blind spot
		// that hid earlier drops.
		lookupService.OnError = func(err error) { slog.Default().Error("dlrlookup: " + err.Error()) }
		runtime.dlrLookup = lookupService
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
