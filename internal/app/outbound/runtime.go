package outbound

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/stats"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/infra/storage"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/httpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// Runtime owns the production outbound composition and all long-lived
// resources used by the HTTP → routing → billing → RabbitMQ path.
type Runtime struct {
	Handler http.Handler

	submitter    core.Submitter
	directory    *runtimeDirectory
	publisher    *amqpcompat.Publisher
	bridge       *picklecompat.Bridge
	connection   *amqp.Connection
	billing      *lateBillingConsumer
	outboxCancel context.CancelFunc
	outboxWG     sync.WaitGroup
	ownedBridge  bool
	ownedStore   *storage.PostgresSubmitTransactionRepository

	// Live routing: the submit path selects through routes (atomic); admin
	// route provisioning rebuilds config + admin routes and swaps it. mu
	// serialises rebuilds so concurrent admin calls can't interleave.
	routes          *routingtable.AtomicTable
	configRoutes    []RouteConfig
	configUsernames []string
	resolveUID      uidResolver
	routesMu        sync.Mutex
}

type RuntimeDependencies struct {
	Bridge             *picklecompat.Bridge
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
	bridge, err := picklecompat.NewBridge(ctx, config.PythonPath)
	if err != nil {
		_ = repository.Close()
		return nil, fmt.Errorf("start trusted pickle bridge: %w", err)
	}
	runtime, err := NewRuntimeWithDependencies(ctx, config, RuntimeDependencies{Bridge: bridge, Transactions: transactions, Repository: repository})
	if err != nil {
		_ = bridge.Close()
		_ = repository.Close()
		return nil, err
	}
	runtime.ownedBridge = true
	runtime.ownedStore = repository
	return runtime, nil
}

func NewRuntimeWithDependencies(ctx context.Context, config Config, dependencies RuntimeDependencies) (_ *Runtime, resultErr error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if dependencies.Bridge == nil || dependencies.Transactions == nil || dependencies.Repository == nil {
		return nil, fmt.Errorf("%w: bridge, transactions and PostgreSQL repository are required", ErrInvalidRuntimeConfig)
	}
	if _, err := submittransaction.RequireProductionRepository(dependencies.Repository); err != nil {
		return nil, err
	}
	directory, err := newRuntimeDirectory(config)
	if err != nil {
		return nil, err
	}
	routes, connectorIDs, defaultRate, err := buildRoutes(config.Routes, directory.resolveUID)
	if err != nil {
		return nil, err
	}
	directory.defaultRate = defaultRate
	atomicRoutes := routingtable.NewAtomicTable(routes)

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
	interceptorTable, err := buildInterceptorTable(config.MTInterceptors, directory.resolveUID)
	if err != nil {
		return nil, err
	}
	if len(config.MTInterceptors) > 0 && dependencies.InterceptorRunner == nil {
		return nil, fmt.Errorf("%w: mt_interceptors configured without an interceptor runner", ErrInvalidRuntimeConfig)
	}
	submitService, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:     interceptorTable,
		InterceptorRunner:    dependencies.InterceptorRunner,
		RoutingTable:         atomicRoutes,
		BillingUsers:         directory.users,
		EnvelopeBuilder:      envelopeBuilder,
		Transaction:          dependencies.Transactions,
		SelectConnector:      connectorSelector(dependencies.ConnectorAvailable),
		ConnectorPDUDefaults: dependencies.ConnectorPDUDefaults,
		DLRRequestStore:      dependencies.DLRRequestStore,
		ConnectorDLRExpiry:   dependencies.ConnectorDLRExpiry,
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

	handler := httpcompat.NewHandler(httpcompat.Dependencies{
		Authenticator: directory,
		BalanceReader: directory,
		RateReader:    directory,
		Submitter:     submitService,
		HTTPStats:     &stats.HTTPStats{},
		SMPPcStats:    dependencies.SMPPcStats,
		SMPPsStats:    dependencies.SMPPsStats,
		ConnectorIDs:  dependencies.ConnectorIDs,
	})
	outboxOwner, err := newOutboxOwner()
	if err != nil {
		return nil, fmt.Errorf("create submit outbox owner: %w", err)
	}
	dispatcher, err := submittransaction.NewDispatcher(dependencies.Repository, publisher, outboxOwner, 32, 30*time.Second, nil)
	if err != nil {
		return nil, fmt.Errorf("create submit outbox dispatcher: %w", err)
	}
	outboxCtx, outboxCancel := context.WithCancel(ctx)
	runtime := &Runtime{
		Handler:         handler,
		submitter:       submitService,
		directory:       directory,
		publisher:       publisher,
		bridge:          dependencies.Bridge,
		connection:      connection,
		billing:         billingConsumer,
		outboxCancel:    outboxCancel,
		routes:          atomicRoutes,
		configRoutes:    append([]RouteConfig(nil), config.Routes...),
		configUsernames: configUsernames(config.Users),
		resolveUID:      directory.resolveUID,
	}
	runtime.outboxWG.Add(1)
	go runtime.runOutbox(outboxCtx, dispatcher)
	return runtime, nil
}

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
	table, _, _, err := buildRoutes(combined, runtime.resolveUID)
	if err != nil {
		return err
	}
	runtime.routes.Store(table)
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

func (runtime *Runtime) Submitter() core.Submitter {
	if runtime == nil {
		return nil
	}
	return runtime.submitter
}

func newOutboxOwner() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return "gateway-outbox-" + hex.EncodeToString(token[:]), nil
}

func (runtime *Runtime) runOutbox(ctx context.Context, dispatcher *submittransaction.Dispatcher) {
	defer runtime.outboxWG.Done()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, _ = dispatcher.DispatchOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	var errs []error
	if runtime.outboxCancel != nil {
		runtime.outboxCancel()
		runtime.outboxWG.Wait()
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
	if config.ListenAddress == "" || config.AMQPURL == "" || config.PythonPath == "" || config.PostgresDSN == "" {
		return fmt.Errorf("%w: listen_address, amqp_url, python_path and postgres_dsn are required", ErrInvalidRuntimeConfig)
	}
	if len(config.Users) == 0 || len(config.Routes) == 0 {
		return fmt.Errorf("%w: at least one user and route are required", ErrInvalidRuntimeConfig)
	}
	for index, route := range config.Routes {
		if len(route.ConnectorCandidates()) == 0 {
			return fmt.Errorf("%w: route %d has no connectors", ErrInvalidRuntimeConfig, index)
		}
	}
	return nil
}

func buildRoutes(configs []RouteConfig, resolveUID uidResolver) (routingtable.Table, []string, float64, error) {
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
			routeConnectors = append(routeConnectors, routingtable.Connector{IDValue: connectorID, TypeValue: routingtable.SMPPC})
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
			filters, filterErr := buildRouteFilters(entry.Filters, resolveUID)
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
