package routingtable_test

import (
	"errors"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
)

func TestRouteConnectorPoolRejectsDirectionIncompatibleBackup(t *testing.T) {
	primary := routingtable.Connector{IDValue: "primary", TypeValue: routingtable.SMPPC}
	route, err := routingtable.NewStaticRoute(routingfilter.MT, primary, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = route.WithConnectors([]routingtable.Connector{
		primary,
		{IDValue: "wrong-backup", TypeValue: routingtable.HTTP},
	})
	if !errors.Is(err, routingtable.ErrInvalidTableParameter) {
		t.Fatalf("mixed MT connector pool error=%v", err)
	}

	defaultRoute, err := routingtable.NewDefaultRoute(primary, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = defaultRoute.WithConnectors([]routingtable.Connector{
		primary,
		{IDValue: "wrong-backup", TypeValue: routingtable.HTTP},
	})
	if !errors.Is(err, routingtable.ErrInvalidTableParameter) {
		t.Fatalf("mixed default MT connector pool error=%v", err)
	}

	_, err = routingtable.FromRouteState(routingtable.RouteState{
		ConnectorID: "primary", ConnectorType: routingtable.SMPPC, DefaultRoute: true,
		Connectors: []routingtable.ConnectorState{
			{ID: "primary", Type: routingtable.SMPPC},
			{ID: "wrong-backup", Type: routingtable.HTTP},
		},
	})
	if !errors.Is(err, routingtable.ErrInvalidTableParameter) {
		t.Fatalf("persisted mixed default MT connector pool error=%v", err)
	}
}
