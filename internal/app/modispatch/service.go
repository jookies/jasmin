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
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

var (
	ErrInvalidConfig = errors.New("modispatch: invalid configuration")
	// ErrMORouteOrderReserved is an admin route colliding with a config-owned
	// route order. Config wins; admin manages an additive set.
	ErrMORouteOrderReserved = errors.New("modispatch: route order is config-owned")
)

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
// (positive order) guarded by the legacy ConnectorFilter on the source cid, plus
// optional content Filters (source/destination/short_message/tag/date/time) that
// must all match for the route to apply.
type RouteConfig struct {
	Order             int             `json:"order"`
	Default           bool            `json:"default"`
	FilterConnectorID string          `json:"filter_connector_id,omitempty"`
	Filters           []FilterConfig  `json:"filters,omitempty"`
	Connector         ConnectorConfig `json:"connector"`
}

// FilterConfig is one MO route content filter. Kept local (mirroring the MT
// filter spec) so modispatch stays decoupled from the submit package. The user
// filter is MT-only and unsupported here; the connector filter is expressed by
// FilterConnectorID, not as a content filter.
type FilterConfig struct {
	Type    string `json:"type"`
	Pattern string `json:"pattern,omitempty"`
	Value   string `json:"value,omitempty"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
}

// buildMORouteFilters translates a route's content-filter specs into engine
// filters, rejecting shapes an MO route cannot carry.
func buildMORouteFilters(specs []FilterConfig) ([]routingfilter.Filter, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	filters := make([]routingfilter.Filter, 0, len(specs))
	for index, spec := range specs {
		filter, err := buildMORouteFilter(spec)
		if err != nil {
			return nil, fmt.Errorf("filter %d: %w", index, err)
		}
		filters = append(filters, filter)
	}
	return filters, nil
}

func buildMORouteFilter(spec FilterConfig) (routingfilter.Filter, error) {
	switch spec.Type {
	case "source_addr":
		return routingfilter.NewSourceAddrFilter(spec.Pattern)
	case "destination_addr":
		return routingfilter.NewDestinationAddrFilter(spec.Pattern)
	case "short_message":
		return routingfilter.NewShortMessageFilter(spec.Pattern)
	case "tag":
		return routingfilter.NewTagFilter(spec.Value)
	case "date_interval":
		return routingfilter.NewDateIntervalFilter(spec.Start, spec.End)
	case "time_interval":
		return routingfilter.NewTimeIntervalFilter(spec.Start, spec.End)
	case "eval_py":
		return routingfilter.NewEvalPyFilter(spec.Value)
	case "user":
		return nil, fmt.Errorf("user filter is MT-only, not valid on an MO route")
	case "connector":
		return nil, fmt.Errorf("connector filter is expressed by filter_connector_id, not filters")
	case "":
		return nil, fmt.Errorf("filter type is required")
	default:
		return nil, fmt.Errorf("unknown MO route filter type %q", spec.Type)
	}
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
	return validateRoutes(config.Routes)
}

// validateRoutes checks the route-set invariants shared by boot-time config
// validation and admin applies: default-route shape, positive static orders,
// unique orders, compilable filters and a well-formed destination connector.
func validateRoutes(routes []RouteConfig) error {
	seenOrder := make(map[int]struct{}, len(routes))
	for index, route := range routes {
		if route.Default {
			if route.Order != 0 {
				return fmt.Errorf("%w: default route %d must use order 0", ErrInvalidConfig, index)
			}
			if route.FilterConnectorID != "" {
				return fmt.Errorf("%w: default route %d cannot carry a filter", ErrInvalidConfig, index)
			}
			if len(route.Filters) > 0 {
				return fmt.Errorf("%w: default route %d cannot carry content filters", ErrInvalidConfig, index)
			}
		} else {
			if route.Order <= 0 {
				return fmt.Errorf("%w: static route %d must use positive order", ErrInvalidConfig, index)
			}
			// No filter_connector_id is legal: the route then matches any
			// inbound connector, like a legacy StaticMORoute whose filter list
			// carries no ConnectorFilter.
		}
		if _, err := buildMORouteFilters(route.Filters); err != nil {
			return fmt.Errorf("%w: route %d %v", ErrInvalidConfig, index, err)
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
	RepickleRoutablePDU(ctx context.Context, routable []byte) ([]byte, picklecompat.RoutableFields, error)
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
// (static config, encoded once at boot through the bridge) and its content
// filters compiled.
type preparedRoute struct {
	config     RouteConfig
	routingKey string
	pickled    []byte
	filters    []routingfilter.Filter
}

// routeTable is an immutable dispatch snapshot. It is swapped atomically by
// ApplyRoutes and read without locking by selectRoute, so admin mutations never
// contend with the inbound MO path.
type routeTable struct {
	routes   []preparedRoute // static routes, highest order first
	fallback *preparedRoute  // default route (order 0), nil when absent
}

// Service consumes the RouterPB deliver queue and dispatches.
type Service struct {
	cfg    Config
	bridge Bridge
	// table is the live dispatch snapshot; never nil after NewService.
	table atomic.Pointer[routeTable]
	// configRoutes are the config-owned routes admin may not displace; their
	// orders are reserved. applyMu serialises rebuilds so two concurrent
	// applies cannot interleave their reads of configRoutes.
	configRoutes []RouteConfig
	applyMu      sync.Mutex
	onError      func(error)
	now          func() time.Time
}

// Option customises the service.
type Option func(*Service)

// WithOnError observes handling errors (tests and logging).
func WithOnError(observer func(error)) Option {
	return func(s *Service) { s.onError = observer }
}

// NewService prepares the dispatch table: connector lists are pickled through
// the bridge once, so per-message work is one repickle + one publish.
// An empty route set is valid here even though ValidateConfig rejects one: the
// config-file contract is "if you declare mo_routes, declare at least one",
// while the runtime may legitimately start with none and receive them from the
// admin plane. An MO arriving with no matching route is simply unroutable.
func NewService(ctx context.Context, config Config, bridge Bridge, options ...Option) (*Service, error) {
	if config.AMQPURL == "" {
		return nil, fmt.Errorf("%w: empty amqp_url", ErrInvalidConfig)
	}
	if err := validateRoutes(config.Routes); err != nil {
		return nil, err
	}
	if bridge == nil {
		return nil, fmt.Errorf("%w: nil bridge", ErrInvalidConfig)
	}
	service := &Service{
		cfg:          config,
		bridge:       bridge,
		configRoutes: append([]RouteConfig(nil), config.Routes...),
		onError:      func(error) {},
		now:          time.Now,
	}
	table, err := prepareTable(ctx, bridge, config.Routes)
	if err != nil {
		return nil, err
	}
	service.table.Store(table)
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// prepareTable pickles each route's connector list and compiles its filters,
// producing an immutable snapshot. It either returns a complete table or an
// error — a caller swapping the result can never install a partial one.
func prepareTable(ctx context.Context, bridge Bridge, routes []RouteConfig) (*routeTable, error) {
	table := &routeTable{}
	for _, route := range routes {
		spec := picklecompat.MOConnectorSpec{
			Type: route.Connector.Type, CID: route.Connector.CID, URL: route.Connector.URL,
			Method: route.Connector.Method, SystemID: route.Connector.SystemID,
		}
		pickled, err := bridge.EncodeConnectorList(ctx, []picklecompat.MOConnectorSpec{spec})
		if err != nil {
			return nil, fmt.Errorf("modispatch: pickle route %d connectors: %w", route.Order, err)
		}
		filters, err := buildMORouteFilters(route.Filters)
		if err != nil {
			return nil, fmt.Errorf("modispatch: route %d filters: %w", route.Order, err)
		}
		routingKey := "deliver_sm_thrower.http"
		if route.Connector.Type == "smpps" {
			routingKey = "deliver_sm_thrower.smpps"
		}
		prepared := preparedRoute{config: route, routingKey: routingKey, pickled: pickled, filters: filters}
		if route.Default {
			fallback := prepared
			table.fallback = &fallback
			continue
		}
		table.routes = append(table.routes, prepared)
	}
	sort.SliceStable(table.routes, func(i, j int) bool {
		return table.routes[i].config.Order > table.routes[j].config.Order
	})
	return table, nil
}

// ApplyRoutes rebuilds the dispatch table from the config-owned routes plus the
// supplied admin routes and swaps it in atomically. Config orders are reserved
// (config owns its set; admin manages an additive set), and a rebuild that
// fails for any reason leaves the previous table live.
func (s *Service) ApplyRoutes(ctx context.Context, adminRoutes []RouteConfig) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	reserved := make(map[int]struct{}, len(s.configRoutes))
	for _, route := range s.configRoutes {
		reserved[route.Order] = struct{}{}
	}
	for _, route := range adminRoutes {
		if _, clash := reserved[route.Order]; clash {
			return fmt.Errorf("%w: order %d", ErrMORouteOrderReserved, route.Order)
		}
	}
	combined := make([]RouteConfig, 0, len(s.configRoutes)+len(adminRoutes))
	combined = append(combined, s.configRoutes...)
	combined = append(combined, adminRoutes...)
	if err := validateRoutes(combined); err != nil {
		return err
	}
	table, err := prepareTable(ctx, s.bridge, combined)
	if err != nil {
		return err
	}
	s.table.Store(table)
	return nil
}

// selectRoute walks static routes highest-order-first (the legacy route table
// scan): a route applies when its connector filter matches the source cid AND
// all its content filters match. Falls back to the default route.
func (s *Service) selectRoute(routable routingfilter.Routable) (*preparedRoute, error) {
	table := s.table.Load()
	sourceCID := routable.ConnectorID()
	for index := range table.routes {
		route := &table.routes[index]
		// An empty FilterConnectorID means "any inbound connector". Legacy's
		// StaticMORoute matches on its filter list alone and has no notion of a
		// mandatory connector filter, so requiring one here made a legal legacy
		// route unexpressible.
		if route.config.FilterConnectorID != "" && route.config.FilterConnectorID != sourceCID {
			continue
		}
		matched, err := matchAllFilters(route.filters, routable)
		if err != nil {
			return nil, err
		}
		if matched {
			return route, nil
		}
	}
	return table.fallback, nil
}

// matchAllFilters reports whether every filter matches (empty matches all).
func matchAllFilters(filters []routingfilter.Filter, routable routingfilter.Routable) (bool, error) {
	for _, filter := range filters {
		ok, err := filter.Match(routable)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// buildRoutable assembles the MO routable the route filters evaluate, from the
// source connector id and the bridge-decoded routing fields.
func (s *Service) buildRoutable(sourceCID string, fields picklecompat.RoutableFields) (routingfilter.Routable, error) {
	return routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MO,
		ConnectorID:     sourceCID,
		SourceAddr:      routingfilter.BytesField{Present: len(fields.SourceAddr) > 0, Value: fields.SourceAddr},
		DestinationAddr: routingfilter.BytesField{Present: len(fields.DestinationAddr) > 0, Value: fields.DestinationAddr},
		ShortMessage:    routingfilter.BytesField{Present: len(fields.ShortMessage) > 0, Value: fields.ShortMessage},
		Timestamp:       s.now(),
		Tags:            fields.Tags,
	})
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
	concatenated, err := boolHeaderValue(headers, "concatenated")
	if err != nil {
		_ = delivery.Reject(false)
		s.onError(err)
		return err
	}
	willBeConcatenated, err := boolHeaderValue(headers, "will_be_concatenated")
	if err != nil {
		_ = delivery.Reject(false)
		s.onError(err)
		return err
	}
	// Repickle first: the same bridge round-trip returns the decoded routing
	// fields the content filters need, so route selection can see the message.
	pduPickle, fields, err := s.bridge.RepickleRoutablePDU(ctx, envelope.Body())
	if err != nil {
		_ = delivery.Reject(false)
		err = fmt.Errorf("modispatch: repickle routable (msgid %s): %w", envelope.Properties().MessageID(), err)
		s.onError(err)
		return err
	}
	routable, err := s.buildRoutable(sourceCID, fields)
	if err != nil {
		_ = delivery.Reject(false)
		err = fmt.Errorf("modispatch: build routable (msgid %s): %w", envelope.Properties().MessageID(), err)
		s.onError(err)
		return err
	}
	route, err := s.selectRoute(routable)
	if err != nil {
		_ = delivery.Reject(false)
		err = fmt.Errorf("modispatch: select route (msgid %s): %w", envelope.Properties().MessageID(), err)
		s.onError(err)
		return err
	}
	if route == nil {
		// The legacy router logs and drops an unroutable MO.
		_ = delivery.Ack()
		err := fmt.Errorf("modispatch: no route matched MO from %q (msgid %s), dropped", sourceCID, envelope.Properties().MessageID())
		s.onError(err)
		return err
	}
	if concatenated && route.config.Connector.Type != "http" {
		return delivery.Reject(false)
	}
	if willBeConcatenated && route.config.Connector.Type == "http" {
		return delivery.Reject(false)
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

func boolHeaderValue(headers map[string]amqpcompat.Field, name string) (bool, error) {
	field, ok := headers[name]
	if !ok {
		return false, fmt.Errorf("modispatch: missing header %q", name)
	}
	value, isBool := field.Bool()
	if !isBool {
		return false, fmt.Errorf("modispatch: header %q has wrong kind", name)
	}
	return value, nil
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
