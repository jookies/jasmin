package modispatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

// moRoutable builds the routable selectRoute evaluates, for a given source cid.
func moRoutable(t *testing.T, sourceCID string) routingfilter.Routable {
	t.Helper()
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MO,
		ConnectorID:     sourceCID,
		SourceAddr:      routingfilter.BytesField{Present: true, Value: []byte("1111")},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte("2222")},
		ShortMessage:    routingfilter.BytesField{Present: true, Value: []byte("hello")},
		Timestamp:       time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("build routable: %v", err)
	}
	return routable
}

func TestApplyRoutesSwapsLiveTable(t *testing.T) {
	ctx := context.Background()
	bridge := &fakeBridge{repickled: []byte("pdu-pickle")}
	service, err := NewService(ctx, baseConfig(), bridge)
	if err != nil {
		t.Fatal(err)
	}

	// Before the swap, an unknown source falls back to the default route.
	route, err := service.selectRoute(moRoutable(t, "smsc-new"))
	if err != nil {
		t.Fatal(err)
	}
	if route == nil || route.config.Connector.CID != "mo-any" {
		t.Fatalf("expected the default route before apply, got %+v", route)
	}

	// Admin adds a static route for that source.
	adminRoute := RouteConfig{
		Order:             20,
		FilterConnectorID: "smsc-new",
		Connector:         ConnectorConfig{Type: "http", CID: "mo-admin", URL: "http://localhost:2/mo", Method: "POST"},
	}
	if err := service.ApplyRoutes(ctx, []RouteConfig{adminRoute}); err != nil {
		t.Fatalf("ApplyRoutes: %v", err)
	}

	route, err = service.selectRoute(moRoutable(t, "smsc-new"))
	if err != nil {
		t.Fatal(err)
	}
	if route == nil || route.config.Connector.CID != "mo-admin" {
		t.Fatalf("expected the admin route after apply, got %+v", route)
	}
	if route.routingKey != "deliver_sm_thrower.http" {
		t.Fatalf("routing key %q", route.routingKey)
	}

	// Config routes survive the swap: the config static route still applies and
	// the default is still the fallback.
	route, err = service.selectRoute(moRoutable(t, "smsc-special"))
	if err != nil {
		t.Fatal(err)
	}
	if route == nil || route.config.Connector.SystemID != "alice" {
		t.Fatalf("config static route lost after apply: %+v", route)
	}
	route, err = service.selectRoute(moRoutable(t, "smsc-unknown"))
	if err != nil {
		t.Fatal(err)
	}
	if route == nil || route.config.Connector.CID != "mo-any" {
		t.Fatalf("default route lost after apply: %+v", route)
	}

	// Applying an empty set removes admin routes, back to config-only.
	if err := service.ApplyRoutes(ctx, nil); err != nil {
		t.Fatalf("ApplyRoutes(nil): %v", err)
	}
	route, err = service.selectRoute(moRoutable(t, "smsc-new"))
	if err != nil {
		t.Fatal(err)
	}
	if route == nil || route.config.Connector.CID != "mo-any" {
		t.Fatalf("admin route not removed: %+v", route)
	}
}

func TestApplyRoutesRejectsReservedOrder(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(ctx, baseConfig(), &fakeBridge{repickled: []byte("pdu-pickle")})
	if err != nil {
		t.Fatal(err)
	}
	// Order 10 belongs to a config route.
	err = service.ApplyRoutes(ctx, []RouteConfig{{
		Order:             10,
		FilterConnectorID: "smsc-other",
		Connector:         ConnectorConfig{Type: "smpps", SystemID: "bob"},
	}})
	if !errors.Is(err, ErrMORouteOrderReserved) {
		t.Fatalf("expected ErrMORouteOrderReserved, got %v", err)
	}
	// The config route is untouched.
	route, selErr := service.selectRoute(moRoutable(t, "smsc-special"))
	if selErr != nil {
		t.Fatal(selErr)
	}
	if route == nil || route.config.Connector.SystemID != "alice" {
		t.Fatalf("config route changed after a rejected apply: %+v", route)
	}
}

// A rebuild that fails must leave the previous table live — no partial apply.
func TestApplyRoutesFailureKeepsPreviousTable(t *testing.T) {
	ctx := context.Background()
	bridge := &fakeBridge{repickled: []byte("pdu-pickle")}
	service, err := NewService(ctx, baseConfig(), bridge)
	if err != nil {
		t.Fatal(err)
	}
	good := RouteConfig{
		Order:             20,
		FilterConnectorID: "smsc-new",
		Connector:         ConnectorConfig{Type: "http", CID: "mo-admin", URL: "http://localhost:2/mo", Method: "GET"},
	}
	if err := service.ApplyRoutes(ctx, []RouteConfig{good}); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]RouteConfig{
		"invalid filter regex": {{
			Order: 30, FilterConnectorID: "smsc-x",
			Filters:   []FilterConfig{{Type: "destination_addr", Pattern: "([unclosed"}},
			Connector: ConnectorConfig{Type: "http", CID: "c", URL: "http://localhost:3/mo", Method: "GET"},
		}},
		"bad connector type": {{
			Order: 30, FilterConnectorID: "smsc-x",
			Connector: ConnectorConfig{Type: "carrier-pigeon"},
		}},
		"duplicate admin order": {good, good},
	}
	for name, routes := range cases {
		t.Run(name, func(t *testing.T) {
			if err := service.ApplyRoutes(ctx, routes); err == nil {
				t.Fatal("expected the apply to fail")
			}
			// The previously applied admin route is still live.
			route, selErr := service.selectRoute(moRoutable(t, "smsc-new"))
			if selErr != nil {
				t.Fatal(selErr)
			}
			if route == nil || route.config.Connector.CID != "mo-admin" {
				t.Fatalf("previous table lost after a failed apply: %+v", route)
			}
		})
	}

	// A bridge failure mid-rebuild is the same contract.
	bridge.listErr = errors.New("bridge down")
	if err := service.ApplyRoutes(ctx, []RouteConfig{good}); err == nil {
		t.Fatal("expected the apply to fail when the bridge errors")
	}
	bridge.listErr = nil
	route, selErr := service.selectRoute(moRoutable(t, "smsc-new"))
	if selErr != nil {
		t.Fatal(selErr)
	}
	if route == nil || route.config.Connector.CID != "mo-admin" {
		t.Fatalf("previous table lost after a bridge failure: %+v", route)
	}
}

// The inbound path reads the table without locking while admin swaps it.
func TestApplyRoutesConcurrentWithSelect(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(ctx, baseConfig(), &fakeBridge{repickled: []byte("pdu-pickle")})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := service.selectRoute(moRoutable(t, "smsc-special")); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()

	for i := range 50 {
		routes := []RouteConfig{{
			Order:             20 + i,
			FilterConnectorID: "smsc-new",
			Connector:         ConnectorConfig{Type: "http", CID: "mo-admin", URL: "http://localhost:2/mo", Method: "GET"},
		}}
		if err := service.ApplyRoutes(ctx, routes); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
}
