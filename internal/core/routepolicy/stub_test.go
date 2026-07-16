package routepolicy_test

import (
	"errors"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/routepolicy"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
)

func TestBestQualityMTRouteStub(t *testing.T) {
	connector := routingtable.Connector{IDValue: "c1", TypeValue: routingtable.SMPPC}
	_, err := routepolicy.New(routepolicy.BestQuality, routingfilter.MT, []routingtable.Connector{connector}, 0.0)
	if !errors.Is(err, routepolicy.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}
