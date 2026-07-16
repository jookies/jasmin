package routingtable

import (
	"context"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

// RouteState represents the serializable state of a route.
type RouteState struct {
	Direction    routingfilter.Direction      `json:"direction"`
	ConnectorID  string                       `json:"connector_id"`
	ConnectorType ConnectorType               `json:"connector_type"`
	Rate         float64                      `json:"rate"`
	DefaultRoute bool                         `json:"default_route"`
	Filters      []routingfilter.FilterState  `json:"filters"`
}

// GetState returns the serializable state of the route.
func (r Route) GetState() RouteState {
	filters := make([]routingfilter.FilterState, 0, len(r.filters))
	for _, f := range r.filters {
		filters = append(filters, routingfilter.GetFilterState(f))
	}

	return RouteState{
		Direction:    r.direction,
		ConnectorID:  r.connector.IDValue,
		ConnectorType: r.connector.TypeValue,
		Rate:         r.rate,
		DefaultRoute: r.defaultRoute,
		Filters:      filters,
	}
}

// FromRouteState reconstructs a route from its state.
func FromRouteState(s RouteState) (Route, error) {
	connector := Connector{
		IDValue:   s.ConnectorID,
		TypeValue: s.ConnectorType,
	}

	if s.DefaultRoute {
		return NewDefaultRoute(connector, s.Rate)
	}

	filters := make([]routingfilter.Filter, 0, len(s.Filters))
	for _, fs := range s.Filters {
		f, err := routingfilter.FromFilterState(fs)
		if err != nil {
			return Route{}, err
		}
		filters = append(filters, f)
	}

	return NewStaticRoute(s.Direction, connector, s.Rate, filters...)
}

// ConnectorState represents the serializable state of a connector.
type ConnectorState struct {
	ID   string        `json:"id"`
	Type ConnectorType `json:"type"`
}

// GetState returns the serializable state of the connector.
func (c Connector) GetState() ConnectorState {
	return ConnectorState{
		ID:   c.IDValue,
		Type: c.TypeValue,
	}
}

// FromConnectorState reconstructs a connector from its state.
func FromConnectorState(s ConnectorState) Connector {
	return Connector{
		IDValue:   s.ID,
		TypeValue: s.Type,
	}
}

// RouteRepository defines the persistence contract for routes.
type RouteRepository interface {
	Save(ctx context.Context, order int, r Route) error
	LoadAll(ctx context.Context) (map[int]Route, error)
	Delete(ctx context.Context, order int) error
}

// ConnectorRepository defines the persistence contract for connectors.
type ConnectorRepository interface {
	Save(ctx context.Context, c Connector) error
	Load(ctx context.Context, id string) (Connector, error)
	List(ctx context.Context) ([]Connector, error)
	Delete(ctx context.Context, id string) error
}
