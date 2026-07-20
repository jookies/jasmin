package outbound

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/httpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// Runtime owns the production outbound composition and all long-lived
// resources used by the HTTP → routing → billing → RabbitMQ path.
type Runtime struct {
	Handler http.Handler

	directory  *runtimeDirectory
	publisher  *amqpcompat.Publisher
	bridge     *picklecompat.Bridge
	connection *amqp.Connection
	billing    *lateBillingConsumer
}

func NewRuntime(ctx context.Context, config Config) (_ *Runtime, resultErr error) {
	if err := validateConfig(config); err != nil {
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
	bridge, err := picklecompat.NewBridge(ctx, config.PythonPath)
	if err != nil {
		return nil, fmt.Errorf("start trusted pickle bridge: %w", err)
	}
	defer func() {
		if resultErr != nil {
			_ = bridge.Close()
		}
	}()
	envelopeBuilder, err := NewSubmitEnvelopeBuilder(bridge)
	if err != nil {
		return nil, err
	}
	submitService, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable: interceptor.NewTableBuilder().Build(),
		RoutingTable:     &routes,
		BillingUsers:     directory.users,
		EnvelopeBuilder:  envelopeBuilder,
		Publisher:        publisher,
	})
	if err != nil {
		return nil, fmt.Errorf("create submit service: %w", err)
	}
	lateBilling, err := core.NewLateBillingService(directory.users)
	if err != nil {
		return nil, fmt.Errorf("create late billing service: %w", err)
	}
	billingConsumer, err := newLateBillingConsumer(ctx, connection, lateBilling)
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
	return &Runtime{
		Handler:    handler,
		directory:  directory,
		publisher:  publisher,
		bridge:     bridge,
		connection: connection,
		billing:    billingConsumer,
	}, nil
}

func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	var errs []error
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
	if runtime.bridge != nil {
		if err := runtime.bridge.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if runtime.connection != nil {
		if err := runtime.connection.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validateConfig(config Config) error {
	if config.ListenAddress == "" || config.AMQPURL == "" {
		return fmt.Errorf("%w: listen_address and amqp_url are required", ErrInvalidRuntimeConfig)
	}
	if len(config.Users) == 0 || len(config.Routes) == 0 {
		return fmt.Errorf("%w: at least one user and route are required", ErrInvalidRuntimeConfig)
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
		connector := routingtable.Connector{IDValue: entry.ConnectorID, TypeValue: routingtable.SMPPC}
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
		if err := builder.Add(entry.Order, route); err != nil {
			return routingtable.Table{}, nil, 0, fmt.Errorf("%w: route %d: %v", ErrInvalidRuntimeConfig, index, err)
		}
		connectors[entry.ConnectorID] = struct{}{}
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
