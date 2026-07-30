package routingtable

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

const (
	MaxRoutes          = 10_000
	MaxFiltersPerRoute = 64
	MaxOrder           = math.MaxInt32
)

var (
	ErrInvalidTableParameter = errors.New("invalid routing table parameter")
	ErrTooManyRoutes         = errors.New("too many routes")
	ErrTooManyFilters        = errors.New("too many route filters")
)

type ConnectorType string

const (
	HTTP  ConnectorType = "http"
	SMPPS ConnectorType = "smpps"
	SMPPC ConnectorType = "smppc"
)

type Connector struct {
	IDValue   string
	TypeValue ConnectorType
}

func (c Connector) ID() string          { return c.IDValue }
func (c Connector) Type() ConnectorType { return c.TypeValue }

type Route struct {
	direction    routingfilter.Direction
	order        int
	connector    Connector
	connectors   []Connector
	rate         float64
	filters      []routingfilter.Filter
	defaultRoute bool
}

func (r Route) Connector() Connector { return r.connector }

// ID is the stable routing-table slot used by durable commercial records.
// Replacing a route at an order keeps the same identity by design; historical
// CDRs also retain the selected connector and rate, so a later replacement
// cannot rewrite what was actually chosen.
func (r Route) ID() string { return fmt.Sprintf("%s:%d", r.direction, r.order) }
func (r Route) Connectors() []Connector {
	if len(r.connectors) == 0 {
		return []Connector{r.connector}
	}
	return append([]Connector(nil), r.connectors...)
}
func (r Route) Rate() float64   { return r.rate }
func (r Route) IsDefault() bool { return r.defaultRoute }

func (r Route) WithConnectors(connectors []Connector) (Route, error) {
	if len(connectors) == 0 || connectors[0] != r.connector {
		return Route{}, fmt.Errorf("%w: connector pool primary", ErrInvalidTableParameter)
	}
	seen := make(map[string]struct{}, len(connectors))
	direction := r.direction
	if direction == "" {
		if r.connector.Type() == SMPPC {
			direction = routingfilter.MT
		} else {
			direction = routingfilter.MO
		}
	}
	for _, connector := range connectors {
		if err := validateConnector(connector); err != nil {
			return Route{}, err
		}
		if !connectorAllowed(direction, connector.Type()) {
			return Route{}, fmt.Errorf("%w: connector type", ErrInvalidTableParameter)
		}
		if _, duplicate := seen[connector.ID()]; duplicate {
			return Route{}, fmt.Errorf("%w: duplicate connector %q", ErrInvalidTableParameter, connector.ID())
		}
		seen[connector.ID()] = struct{}{}
	}
	r.connectors = append([]Connector(nil), connectors...)
	return r, nil
}

func NewStaticRoute(direction routingfilter.Direction, connector Connector, rate float64, filters ...routingfilter.Filter) (Route, error) {
	if direction != routingfilter.MT && direction != routingfilter.MO {
		return Route{}, fmt.Errorf("%w: direction", ErrInvalidTableParameter)
	}
	if err := validateConnector(connector); err != nil {
		return Route{}, err
	}
	if len(filters) > MaxFiltersPerRoute {
		return Route{}, ErrTooManyFilters
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return Route{}, fmt.Errorf("%w: rate", ErrInvalidTableParameter)
	}
	copied := append([]routingfilter.Filter(nil), filters...)
	for _, f := range copied {
		if f == nil || !allows(f.Directions(), direction) {
			return Route{}, fmt.Errorf("%w: incompatible filter", ErrInvalidTableParameter)
		}
	}
	if direction == routingfilter.MO {
		rate = 0
	}
	return Route{direction: direction, connector: connector, rate: rate, filters: copied}, nil
}
func NewDefaultRoute(connector Connector, rate float64) (Route, error) {
	if err := validateConnector(connector); err != nil {
		return Route{}, err
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return Route{}, fmt.Errorf("%w: rate", ErrInvalidTableParameter)
	}
	return Route{connector: connector, rate: rate, defaultRoute: true}, nil
}

type Builder struct {
	direction routingfilter.Direction
	routes    map[int]Route
}

func NewBuilder(direction routingfilter.Direction) (*Builder, error) {
	if direction != routingfilter.MT && direction != routingfilter.MO {
		return nil, fmt.Errorf("%w: direction", ErrInvalidTableParameter)
	}
	return &Builder{direction: direction, routes: map[int]Route{}}, nil
}
func (b *Builder) Add(order int, route Route) error {
	if order < 0 || order > MaxOrder {
		return fmt.Errorf("%w: order", ErrInvalidTableParameter)
	}
	for _, connector := range route.Connectors() {
		if !connectorAllowed(b.direction, connector.TypeValue) {
			return fmt.Errorf("%w: connector type", ErrInvalidTableParameter)
		}
	}
	if order == 0 && !route.defaultRoute {
		return fmt.Errorf("%w: order zero requires default", ErrInvalidTableParameter)
	}
	if order != 0 && (route.defaultRoute || route.direction != b.direction) {
		return fmt.Errorf("%w: route type", ErrInvalidTableParameter)
	}
	if _, exists := b.routes[order]; !exists && len(b.routes) >= MaxRoutes {
		return ErrTooManyRoutes
	}
	route.order = order
	route.direction = b.direction
	b.routes[order] = route
	return nil
}
func (b *Builder) Remove(order int) bool {
	if _, ok := b.routes[order]; !ok {
		return false
	}
	delete(b.routes, order)
	return true
}
func (b *Builder) Flush() { b.routes = map[int]Route{} }
func (b *Builder) Orders() []int {
	orders := make([]int, 0, len(b.routes))
	for order := range b.routes {
		orders = append(orders, order)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(orders)))
	return orders
}
func (b *Builder) Build() Table {
	orders := b.Orders()
	entries := make([]entry, 0, len(orders))
	for _, order := range orders {
		entries = append(entries, entry{order: order, route: b.routes[order]})
	}
	return Table{direction: b.direction, entries: entries}
}

type entry struct {
	order int
	route Route
}
type Table struct {
	direction routingfilter.Direction
	entries   []entry
}

// HasDefaultRoute reports whether the table has a default route at order 0.
//
// A table without one is legal, and the reference permits it, but the
// consequences differ by direction and the MO case is the dangerous one: an
// unmatched MT submit fails visibly at the front door, whereas an unmatched MO is
// logged and dropped, so inbound traffic disappears with nobody told. Callers use
// this to warn an operator once at startup rather than per message.
func (t Table) HasDefaultRoute() bool {
	for _, item := range t.entries {
		if item.route.defaultRoute {
			return true
		}
	}
	return false
}

func (t Table) Select(routable routingfilter.Routable) (Route, bool, error) {
	for _, item := range t.entries {
		if item.route.defaultRoute {
			return item.route, true, nil
		}
		matched := true
		for _, filter := range item.route.filters {
			ok, err := filter.Match(routable)
			if err != nil {
				return Route{}, false, err
			}
			if !ok {
				matched = false
				break
			}
		}
		if matched {
			return item.route, true, nil
		}
	}
	return Route{}, false, nil
}
func validateConnector(c Connector) error {
	if len(c.IDValue) > routingfilter.MaxIDBytes {
		return fmt.Errorf("%w: connector id too long", ErrInvalidTableParameter)
	}
	if !utf8.ValidString(c.IDValue) {
		return fmt.Errorf("%w: connector id UTF-8", ErrInvalidTableParameter)
	}
	switch c.TypeValue {
	case HTTP, SMPPS, SMPPC:
		return nil
	}
	return fmt.Errorf("%w: connector type", ErrInvalidTableParameter)
}
func connectorAllowed(d routingfilter.Direction, t ConnectorType) bool {
	if d == routingfilter.MT {
		return t == SMPPC
	}
	return t == HTTP || t == SMPPS
}
func allows(ds []routingfilter.Direction, want routingfilter.Direction) bool {
	for _, d := range ds {
		if d == want {
			return true
		}
	}
	return false
}
