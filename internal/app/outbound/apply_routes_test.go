package outbound

import (
	"errors"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

// newRoutesRuntime builds a minimal Runtime carrying just the live-routing
// state, seeded from config routes — enough to exercise ApplyAdminRoutes
// without standing up AMQP/Postgres.
func newRoutesRuntime(t *testing.T, configRoutes []RouteConfig) *Runtime {
	t.Helper()
	table, _, _, err := buildRoutes(configRoutes, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Runtime{
		routes:       routingtable.NewAtomicTable(table),
		configRoutes: append([]RouteConfig(nil), configRoutes...),
		resolveUID:   nil,
	}
}

func mtRoutableTo(t *testing.T, destination string) routingfilter.Routable {
	t.Helper()
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MT,
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte(destination)},
		ShortMessage:    routingfilter.BytesField{Present: true, Value: []byte("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return routable
}

func TestApplyAdminRoutesSwapsLiveTable(t *testing.T) {
	runtime := newRoutesRuntime(t, []RouteConfig{
		{ConnectorID: "default-c", Order: 0, Rate: 1, Default: true},
	})

	// Before: everything routes to the default connector.
	route, found, err := runtime.routes.Select(mtRoutableTo(t, "33612345678"))
	if err != nil || !found || route.Connector().ID() != "default-c" {
		t.Fatalf("pre-apply routed to %v (found=%v err=%v)", route.Connector().ID(), found, err)
	}

	// Apply an admin route: French destinations to a premium connector.
	err = runtime.ApplyAdminRoutes([]RouteConfig{
		{ConnectorID: "premium", Order: 10, Rate: 1, Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^33"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// After: French destinations now hit premium, others still default — the
	// swap took effect on the same holder the submit path reads.
	route, _, _ = runtime.routes.Select(mtRoutableTo(t, "33612345678"))
	if route.Connector().ID() != "premium" {
		t.Fatalf("post-apply french routed to %q want premium", route.Connector().ID())
	}
	route, _, _ = runtime.routes.Select(mtRoutableTo(t, "15551234567"))
	if route.Connector().ID() != "default-c" {
		t.Fatalf("post-apply us routed to %q want default-c", route.Connector().ID())
	}
}

func TestApplyAdminRoutesRejectsOrderCollision(t *testing.T) {
	runtime := newRoutesRuntime(t, []RouteConfig{
		{ConnectorID: "default-c", Order: 0, Rate: 1, Default: true},
		{ConnectorID: "config-static", Order: 5, Rate: 1},
	})
	err := runtime.ApplyAdminRoutes([]RouteConfig{
		{ConnectorID: "admin-c", Order: 5, Rate: 1}, // collides with config order 5
	})
	if !errors.Is(err, ErrRouteOrderReserved) {
		t.Fatalf("err=%v want ErrRouteOrderReserved", err)
	}
}

func TestApplyAdminRoutesInvalidSpecLeavesTableUntouched(t *testing.T) {
	runtime := newRoutesRuntime(t, []RouteConfig{
		{ConnectorID: "default-c", Order: 0, Rate: 1, Default: true},
	})
	// A static route with a non-positive order is invalid → buildRoutes fails,
	// the live table must be unchanged (apply-first, no partial swap).
	err := runtime.ApplyAdminRoutes([]RouteConfig{{ConnectorID: "bad", Order: -1, Rate: 1}})
	if err == nil {
		t.Fatal("invalid admin route accepted")
	}
	route, found, _ := runtime.routes.Select(mtRoutableTo(t, "123"))
	if !found || route.Connector().ID() != "default-c" {
		t.Fatalf("table changed after a failed apply: %v", route.Connector().ID())
	}
}
