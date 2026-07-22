package routepolicy_test

import (
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/routepolicy"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
)

func TestFailoverAttemptSkipsUnavailableInOrderAndExhausts(t *testing.T) {
	connectors := []routingtable.Connector{
		{IDValue: "first", TypeValue: routingtable.SMPPC},
		{IDValue: "second", TypeValue: routingtable.SMPPC},
		{IDValue: "third", TypeValue: routingtable.SMPPC},
	}
	route, err := routepolicy.New(routepolicy.Failover, routingfilter.MT, connectors, 0)
	if err != nil {
		t.Fatal(err)
	}
	attempt := route.NewAttempt()
	available := func(connector routingtable.Connector) bool { return connector.ID() != "first" }
	got, ok := attempt.NextAvailable(available)
	if !ok || got.ID() != "second" {
		t.Fatalf("first available=(%q,%v)", got.ID(), ok)
	}
	got, ok = attempt.NextAvailable(available)
	if !ok || got.ID() != "third" {
		t.Fatalf("second available=(%q,%v)", got.ID(), ok)
	}
	if got, ok = attempt.NextAvailable(available); ok || got.ID() != "" {
		t.Fatalf("exhaustion=(%+v,%v)", got, ok)
	}
}
