package outbound

import (
	"errors"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

func fixedUIDResolver(byName map[string]int64) uidResolver {
	return func(username string) (int64, bool) {
		uid, ok := byName[username]
		return uid, ok
	}
}

func TestBuildRouteFiltersTranslatesEachType(t *testing.T) {
	resolve := fixedUIDResolver(map[string]int64{"alice": 7})
	cases := []struct {
		name string
		spec FilterConfig
		kind routingfilter.Kind
	}{
		{"destination", FilterConfig{Type: "destination_addr", Pattern: "^33.*"}, routingfilter.KindDestinationAddr},
		{"source", FilterConfig{Type: "source_addr", Pattern: "^1234$"}, routingfilter.KindSourceAddr},
		{"content", FilterConfig{Type: "short_message", Pattern: "STOP"}, routingfilter.KindShortMessage},
		{"tag", FilterConfig{Type: "tag", Value: "premium"}, routingfilter.KindTag},
		{"date", FilterConfig{Type: "date_interval", Start: "2026-01-01", End: "2026-12-31"}, routingfilter.KindDateInterval},
		{"time", FilterConfig{Type: "time_interval", Start: "08:00:00", End: "18:00:00"}, routingfilter.KindTimeInterval},
		{"user", FilterConfig{Type: "user", Username: "alice"}, routingfilter.KindUser},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			filters, err := buildRouteFilters([]FilterConfig{testCase.spec}, resolve)
			if err != nil {
				t.Fatal(err)
			}
			if len(filters) != 1 || filters[0].Kind() != testCase.kind {
				t.Fatalf("filters=%v want single %v", filters, testCase.kind)
			}
		})
	}
}

func TestBuildRouteFiltersRejects(t *testing.T) {
	resolve := fixedUIDResolver(map[string]int64{"alice": 7})
	cases := map[string]FilterConfig{
		"unknown type":    {Type: "carrier_pigeon"},
		"empty type":      {Type: ""},
		"connector on MT": {Type: "connector", Value: "smsc"},
		"unknown user":    {Type: "user", Username: "bob"},
		"bad regex":       {Type: "destination_addr", Pattern: "("},
		"bad date":        {Type: "date_interval", Start: "nope", End: "2026-12-31"},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := buildRouteFilters([]FilterConfig{spec}, resolve); err == nil {
				t.Fatalf("spec %+v accepted, want error", spec)
			}
		})
	}
}

func TestBuildRouteFiltersUserWithoutResolver(t *testing.T) {
	if _, err := buildRouteFilters([]FilterConfig{{Type: "user", Username: "alice"}}, nil); err == nil {
		t.Fatal("user filter without resolver must error")
	}
}

// TestBuildRoutesFilteredSelection proves filtered static routes select the
// right connector: a destination-filtered high-order route wins for matching
// destinations, and traffic that misses it falls through to the default route.
func TestBuildRoutesFilteredSelection(t *testing.T) {
	routes := []RouteConfig{
		{ConnectorID: "premium", Order: 10, Rate: 1, Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^33"}}},
		{ConnectorID: "default-c", Order: 0, Rate: 1, Default: true},
	}
	table, connectorIDs, _, err := buildRoutes(routes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(connectorIDs) != 2 {
		t.Fatalf("connectors=%v", connectorIDs)
	}

	frMatch := mtRoutable(t, 7, "33612345678", "hi")
	route, found, err := table.Select(frMatch)
	if err != nil || !found {
		t.Fatalf("french destination not routed: found=%v err=%v", found, err)
	}
	if route.Connector().ID() != "premium" {
		t.Fatalf("french destination routed to %q want premium", route.Connector().ID())
	}

	otherMatch := mtRoutable(t, 7, "15551234567", "hi")
	route, found, err = table.Select(otherMatch)
	if err != nil || !found {
		t.Fatalf("us destination not routed: found=%v err=%v", found, err)
	}
	if route.Connector().ID() != "default-c" {
		t.Fatalf("us destination routed to %q want default-c", route.Connector().ID())
	}
}

func TestBuildRoutesDefaultRouteRejectsFilters(t *testing.T) {
	routes := []RouteConfig{
		{ConnectorID: "c", Order: 0, Rate: 1, Default: true, Filters: []FilterConfig{{Type: "tag", Value: "x"}}},
	}
	if _, _, _, err := buildRoutes(routes, nil); !errors.Is(err, ErrInvalidRuntimeConfig) {
		t.Fatalf("err=%v want ErrInvalidRuntimeConfig", err)
	}
}

func mtRoutable(t *testing.T, uid int64, destination, content string) routingfilter.Routable {
	t.Helper()
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MT,
		UserID:          uid,
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte(destination)},
		ShortMessage:    routingfilter.BytesField{Present: true, Value: []byte(content)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return routable
}
