// Package modispatch is the gateway's MO router dispatch: it consumes the
// RouterPB deliver queue (deliver.sm.* — the connectors' MO ingress) and
// republishes each message as the legacy RoutedDeliverSmContent to
// deliver_sm_thrower.http/.smpps, the RouterPB.deliver_sm_callback port for
// default and connector-filtered static routes. Content filters arrive with
// the filter-config step (plan 008 Step 6).
package modispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

var ErrInvalidConfig = errors.New("modispatch: invalid configuration")

// ConnectorConfig names one MO destination.
type ConnectorConfig struct {
	// Type is "http" (CID+URL+Method, validated by the legacy HttpConnector —
	// dotted-host/localhost/IP URLs only) or "smpps" (SystemID).
	Type     string `json:"type"`
	CID      string `json:"cid,omitempty"`
	URL      string `json:"url,omitempty"`
	Method   string `json:"method,omitempty"`
	SystemID string `json:"system_id,omitempty"`
}

// RouteConfig is one MO route: the default route (order 0) or a static route
// (positive order) guarded by the legacy ConnectorFilter on the source cid.
type RouteConfig struct {
	Order             int             `json:"order"`
	Default           bool            `json:"default"`
	FilterConnectorID string          `json:"filter_connector_id,omitempty"`
	Connector         ConnectorConfig `json:"connector"`
}

type Config struct {
	AMQPURL             string        `json:"amqp_url"`
	AMQPDurableTopology bool          `json:"amqp_durable_topology,omitempty"`
	Routes              []RouteConfig `json:"routes"`
}

func ValidateConfig(config Config) error {
	if config.AMQPURL == "" {
		return fmt.Errorf("%w: empty amqp_url", ErrInvalidConfig)
	}
	if len(config.Routes) == 0 {
		return fmt.Errorf("%w: at least one MO route is required", ErrInvalidConfig)
	}
	seenOrder := make(map[int]struct{}, len(config.Routes))
	for index, route := range config.Routes {
		if route.Default {
			if route.Order != 0 {
				return fmt.Errorf("%w: default route %d must use order 0", ErrInvalidConfig, index)
			}
			if route.FilterConnectorID != "" {
				return fmt.Errorf("%w: default route %d cannot carry a filter", ErrInvalidConfig, index)
			}
		} else {
			if route.Order <= 0 {
				return fmt.Errorf("%w: static route %d must use positive order", ErrInvalidConfig, index)
			}
			if route.FilterConnectorID == "" {
				return fmt.Errorf("%w: static route %d requires filter_connector_id", ErrInvalidConfig, index)
			}
		}
		if _, duplicate := seenOrder[route.Order]; duplicate {
			return fmt.Errorf("%w: duplicate route order %d", ErrInvalidConfig, route.Order)
		}
		seenOrder[route.Order] = struct{}{}
		connector := route.Connector
		switch connector.Type {
		case "http":
			if connector.CID == "" || connector.URL == "" {
				return fmt.Errorf("%w: route %d http connector requires cid and url", ErrInvalidConfig, index)
			}
		case "smpps":
			if connector.SystemID == "" {
				return fmt.Errorf("%w: route %d smpps connector requires system_id", ErrInvalidConfig, index)
			}
		default:
			return fmt.Errorf("%w: route %d connector type %q", ErrInvalidConfig, index, connector.Type)
		}
	}
	return nil
}

// RoutablePDURepickler and ConnectorListEncoder are the bridge seams.
type RoutablePDURepickler interface {
	RepickleRoutablePDU(ctx context.Context, routable []byte) ([]byte, error)
}

type ConnectorListEncoder interface {
	EncodeConnectorList(ctx context.Context, connectors []picklecompat.MOConnectorSpec) ([]byte, error)
}

type Bridge interface {
	RoutablePDURepickler
	ConnectorListEncoder
}

type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, envelope amqpcompat.Envelope) error
}

// preparedRoute is a config route with its dst-connectors header pre-pickled
// (static config, encoded once at boot through the bridge).
type preparedRoute struct {
	config     RouteConfig
	routingKey string
	pickled    []byte
}

// Service consumes the RouterPB deliver queue and dispatches.
type Service struct {
	cfg      Config
	bridge   RoutablePDURepickler
	routes   []preparedRoute // static routes, highest order first
	fallback *preparedRoute  // default route (order 0), nil when absent
	onError  func(error)
}

// Option customises the service.
type Option func(*Service)

// WithOnError observes handling errors (tests and logging).
func WithOnError(observer func(error)) Option {
	return func(s *Service) { s.onError = observer }
}

// NewService prepares the dispatch table: connector lists are pickled through
// the bridge once, so per-message work is one repickle + one publish.
func NewService(ctx context.Context, config Config, bridge Bridge, options ...Option) (*Service, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	if bridge == nil {
		return nil, fmt.Errorf("%w: nil bridge", ErrInvalidConfig)
	}
	service := &Service{cfg: config, bridge: bridge, onError: func(error) {}}
	for _, route := range config.Routes {
		spec := picklecompat.MOConnectorSpec{
			Type: route.Connector.Type, CID: route.Connector.CID, URL: route.Connector.URL,
			Method: route.Connector.Method, SystemID: route.Connector.SystemID,
		}
		pickled, err := bridge.EncodeConnectorList(ctx, []picklecompat.MOConnectorSpec{spec})
		if err != nil {
			return nil, fmt.Errorf("modispatch: pickle route %d connectors: %w", route.Order, err)
		}
		routingKey := "deliver_sm_thrower.http"
		if route.Connector.Type == "smpps" {
			routingKey = "deliver_sm_thrower.smpps"
		}
		prepared := preparedRoute{config: route, routingKey: routingKey, pickled: pickled}
		if route.Default {
			fallback := prepared
			service.fallback = &fallback
			continue
		}
		service.routes = append(service.routes, prepared)
	}
	sort.SliceStable(service.routes, func(i, j int) bool {
		return service.routes[i].config.Order > service.routes[j].config.Order
	})
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// selectRoute walks static routes highest-order-first (the legacy route table
// scan) and falls back to the default route.
func (s *Service) selectRoute(sourceCID string) *preparedRoute {
	for index := range s.routes {
		if s.routes[index].config.FilterConnectorID == sourceCID {
			return &s.routes[index]
		}
	}
	return s.fallback
}

// Handle dispatches one DeliverSmContent delivery. It owns settlement:
// poison (missing header, unroutable, bad pickle) rejects without requeue,
// transient publish failures reject with requeue.
func (s *Service) Handle(ctx context.Context, delivery *amqpcompat.Delivery, publisher Publisher) error {
	if delivery == nil {
		return errors.New("modispatch: nil delivery")
	}
	envelope := delivery.Envelope()
	headers := envelope.Properties().Headers()
	sourceField, ok := headers["connector-id"]
	sourceCID := ""
	if ok {
		sourceCID, _ = sourceField.String()
	}
	if sourceCID == "" {
		_ = delivery.Reject(false)
		err := fmt.Errorf("modispatch: missing connector-id header (msgid %s)", envelope.Properties().MessageID())
		s.onError(err)
		return err
	}
	route := s.selectRoute(sourceCID)
	if route == nil {
		// The legacy router logs and drops an unroutable MO.
		_ = delivery.Ack()
		err := fmt.Errorf("modispatch: no route matched MO from %q (msgid %s), dropped", sourceCID, envelope.Properties().MessageID())
		s.onError(err)
		return err
	}
	pduPickle, err := s.bridge.RepickleRoutablePDU(ctx, envelope.Body())
	if err != nil {
		_ = delivery.Reject(false)
		err = fmt.Errorf("modispatch: repickle routable (msgid %s): %w", envelope.Properties().MessageID(), err)
		s.onError(err)
		return err
	}
	publication, err := newRoutedDeliverPublication(envelope.Properties().MessageID(), sourceCID, route, pduPickle)
	if err != nil {
		_ = delivery.Reject(false)
		s.onError(err)
		return err
	}
	if err := publisher.Publish(ctx, "messaging", route.routingKey, publication); err != nil {
		_ = delivery.Reject(true)
		err = fmt.Errorf("modispatch: publish %s: %w", route.routingKey, err)
		s.onError(err)
		return err
	}
	return delivery.Ack()
}

// newRoutedDeliverPublication is the legacy RoutedDeliverSmContent envelope:
// pickled bare PDU body, route-type/src-connector-id/dst-connectors/try-count.
func newRoutedDeliverPublication(msgID, sourceCID string, route *preparedRoute, pduPickle []byte) (amqpcompat.Envelope, error) {
	properties, err := amqpcompat.NewProperties(msgID, map[string]amqpcompat.Field{
		"route-type":       amqpcompat.StringField("simple"),
		"src-connector-id": amqpcompat.StringField(sourceCID),
		"dst-connectors":   amqpcompat.BytesField(route.pickled),
		"try-count":        amqpcompat.IntegerField(0),
	})
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope(route.routingKey, properties, pduPickle)
}

// Run consumes RouterPB_deliver_sm_all until ctx cancels, redialling on broker
// loss like the other workers.
func (s *Service) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := s.runOnce(ctx); err != nil && ctx.Err() == nil {
			s.onError(fmt.Errorf("modispatch session: %w", err))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(10 * time.Second):
		}
	}
}

func (s *Service) runOnce(ctx context.Context) error {
	connection, err := amqp.Dial(s.cfg.AMQPURL)
	if err != nil {
		return fmt.Errorf("connect RabbitMQ: %w", err)
	}
	defer connection.Close()
	topology := amqpcompat.NewTopology(connection, s.cfg.AMQPDurableTopology)
	// Pre-declare the thrower queue so the mandatory publish routes even when
	// the in-process MO thrower is disabled (legacy topology is fixed).
	if err := topology.DeclareQueue(ctx, "deliver_sm_thrower", "messaging", "deliver_sm_thrower.*"); err != nil {
		return err
	}
	subscription, err := topology.OpenDeliverSMSubscription(ctx)
	if err != nil {
		return err
	}
	defer subscription.Close()
	publisher, err := amqpcompat.NewPublisher(connection)
	if err != nil {
		return err
	}
	defer publisher.Close()

	for {
		select {
		case <-ctx.Done():
			return nil
		case raw, ok := <-subscription.Deliveries:
			if !ok {
				return errors.New("deliver stream closed")
			}
			delivery, err := amqpcompat.NewDelivery(raw)
			if err != nil {
				_ = raw.Reject(false)
				s.onError(fmt.Errorf("modispatch: malformed delivery: %w", err))
				continue
			}
			_ = s.Handle(ctx, delivery, publisher)
		}
	}
}
