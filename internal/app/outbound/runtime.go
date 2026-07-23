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

	directory    *runtimeDirectory
	publisher    *amqpcompat.Publisher
	bridge       *picklecompat.Bridge
	connection   *amqp.Connection
	billing      *lateBillingConsumer
	outboxCancel context.CancelFunc
	outboxWG     sync.WaitGroup
	ownedBridge  bool
	ownedStore   *storage.PostgresSubmitTransactionRepository
}

type RuntimeDependencies struct {
	Bridge             *picklecompat.Bridge
	Transactions       *submittransaction.Service
	Repository         submittransaction.Repository
	ConnectorAvailable func(string) bool
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
	routes, connectorIDs, defaultRate, err := buildRoutes(config.Routes)
	if err != nil {
		return nil, err
	}
	directory.defaultRate = defaultRate

	connection, err := amqp.Dial(config.AMQPURL)
	if err != nil {
		return nil, fmt.Errorf("connect RabbitMQ: %w", err)
	}
	defer func() {
		if resultErr != nil {
			_ = connection.Close()
		}
	}()
	topology := amqpcompat.NewTopology(connection)
	if err := topology.Declare(ctx); err != nil {
		return nil, fmt.Errorf("declare RabbitMQ exchanges: %w", err)
	}
	for _, connectorID := range connectorIDs {
		queue := amqpcompat.ConnectorSubmitQueue(connectorID)
		if err := topology.DeclareQueue(ctx, queue, "messaging", amqpcompat.ConnectorSubmitRoutingKey(connectorID)); err != nil {
			return nil, fmt.Errorf("declare connector %q queue: %w", connectorID, err)
		}
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
	submitService, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable: interceptor.NewTableBuilder().Build(),
		RoutingTable:     &routes,
		BillingUsers:     directory.users,
		EnvelopeBuilder:  envelopeBuilder,
		Transaction:      dependencies.Transactions,
		SelectConnector:  connectorSelector(dependencies.ConnectorAvailable),
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
	billingConsumer, err := newLateBillingConsumer(ctx, connection, durableLateBilling)
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
		Handler:      handler,
		directory:    directory,
		publisher:    publisher,
		bridge:       dependencies.Bridge,
		connection:   connection,
		billing:      billingConsumer,
		outboxCancel: outboxCancel,
	}
	runtime.outboxWG.Add(1)
	go runtime.runOutbox(outboxCtx, dispatcher)
	return runtime, nil
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

func buildRoutes(configs []RouteConfig) (routingtable.Table, []string, float64, error) {
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
			route, err = routingtable.NewDefaultRoute(connector, entry.Rate)
			if entry.Order != 0 {
				return routingtable.Table{}, nil, 0, fmt.Errorf("%w: default route %d must use order 0", ErrInvalidRuntimeConfig, index)
			}
		} else {
			route, err = routingtable.NewStaticRoute(routingfilter.MT, connector, entry.Rate)
			if entry.Order <= 0 {
				return routingtable.Table{}, nil, 0, fmt.Errorf("%w: static route %d must use positive order", ErrInvalidRuntimeConfig, index)
			}
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
