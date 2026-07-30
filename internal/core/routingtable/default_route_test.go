package routingtable_test

import (
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

// A table with no default route is legal, but every submit matching no filter is
// then refused at the front door -- and on the MO side an unmatched message is
// acked and dropped, so inbound traffic disappears with nobody told. The gateway
// warns about that at startup, so the accessor it warns from has to be right: a
// false "has one" hides the exact misconfiguration the warning exists to surface.
func TestHasDefaultRoute(t *testing.T) {
	connector := routingtable.Connector{IDValue: "primary", TypeValue: routingtable.SMPPC}

	filtered, err := routingtable.NewStaticRoute(routingfilter.MT, connector, 1)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := routingtable.NewDefaultRoute(connector, 1)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		routes map[int]routingtable.Route
		want   bool
	}{
		{name: "empty table", routes: nil, want: false},
		{name: "only filtered routes", routes: map[int]routingtable.Route{10: filtered, 20: filtered}, want: false},
		{name: "default present", routes: map[int]routingtable.Route{10: filtered, 0: fallback}, want: true},
		{name: "default only", routes: map[int]routingtable.Route{0: fallback}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder, err := routingtable.NewBuilder(routingfilter.MT)
			if err != nil {
				t.Fatal(err)
			}
			for order, route := range tc.routes {
				if err := builder.Add(order, route); err != nil {
					t.Fatalf("add order %d: %v", order, err)
				}
			}
			if got := builder.Build().HasDefaultRoute(); got != tc.want {
				t.Errorf("HasDefaultRoute() = %v, want %v", got, tc.want)
			}
		})
	}
}
