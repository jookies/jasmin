package routingtable_test

import (
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

func TestSelectedRouteCarriesStableTableSlotIdentity(t *testing.T) {
	builder, err := routingtable.NewBuilder(routingfilter.MT)
	if err != nil {
		t.Fatal(err)
	}
	route, err := routingtable.NewDefaultRoute(
		routingtable.Connector{IDValue: "smsc-a", TypeValue: routingtable.SMPPC},
		0.5,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(0, route); err != nil {
		t.Fatal(err)
	}
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction: routingfilter.MT,
		DestinationAddr: routingfilter.BytesField{
			Present: true,
			Value:   []byte("15551234567"),
		},
		Timestamp: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, found, err := builder.Build().Select(routable)
	if err != nil || !found {
		t.Fatalf("select found=%v err=%v", found, err)
	}
	if got, want := selected.ID(), "mt:0"; got != want {
		t.Fatalf("route ID=%q want %q", got, want)
	}
}
