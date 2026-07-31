package outbound

import (
	"errors"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

// A route persisted before the termination connector existed carries no
// connector_type. It must keep loading as an outbound SMPP route: silently
// retyping it would point it at a connector kind it was never configured for.
func TestBuildRoutesDefaultsConnectorTypeToSMPPC(t *testing.T) {
	table, _, _, err := buildRoutes([]RouteConfig{
		{ConnectorID: "carrier-a", Rate: 0, Default: true},
	}, nil)
	if err != nil {
		t.Fatalf("build routes: %v", err)
	}
	route, ok, err := table.Select(mtRoutableTo(t, "380671234567"))
	if err != nil || !ok {
		t.Fatalf("select default route: ok=%v err=%v", ok, err)
	}
	if got := route.Connector().Type(); got != routingtable.SMPPC {
		t.Errorf("connector type = %q, want %q", got, routingtable.SMPPC)
	}
}

// The regression this test exists for: buildRoutes hardcoded SMPPC, so a
// termination route survived a restart as an SMPP client route and would have
// been dispatched to a carrier connector that does not exist.
func TestBuildRoutesPreservesTerminationConnectorType(t *testing.T) {
	table, _, _, err := buildRoutes([]RouteConfig{
		{ConnectorID: "partner-a-term", ConnectorType: "term", Rate: 0, Default: true},
	}, nil)
	if err != nil {
		t.Fatalf("build routes: %v", err)
	}
	route, ok, err := table.Select(mtRoutableTo(t, "380671234567"))
	if err != nil || !ok {
		t.Fatalf("select default route: ok=%v err=%v", ok, err)
	}
	if got := route.Connector().Type(); got != routingtable.TERM {
		t.Errorf("connector type = %q, want %q", got, routingtable.TERM)
	}
}

func TestBuildRoutesRejectsUnknownConnectorType(t *testing.T) {
	_, _, _, err := buildRoutes([]RouteConfig{
		{ConnectorID: "partner-a", ConnectorType: "carrier-pigeon", Rate: 0, Default: true},
	}, nil)
	if !errors.Is(err, ErrInvalidRuntimeConfig) {
		t.Fatalf("error = %v, want ErrInvalidRuntimeConfig", err)
	}
}
