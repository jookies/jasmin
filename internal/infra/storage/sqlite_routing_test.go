package storage

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
)

func TestSQLiteRoutePersistence(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	moRepo := NewSQLiteRouteRepository(db, routingfilter.MO)
	mtRepo := NewSQLiteRouteRepository(db, routingfilter.MT)

	if err := moRepo.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := mtRepo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Create a complex route with filters
	f1, _ := routingfilter.NewConnectorFilter("conn1")
	f2, _ := routingfilter.NewSourceAddrFilter("^123")
	
	conn := routingtable.Connector{IDValue: "dest1", TypeValue: routingtable.HTTP}
	route, err := routingtable.NewStaticRoute(routingfilter.MO, conn, 0.0, f1, f2)
	if err != nil {
		t.Fatal(err)
	}

	if err := moRepo.Save(ctx, 100, route); err != nil {
		t.Fatal(err)
	}

	routes, err := moRepo.LoadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	loaded, ok := routes[100]
	if !ok {
		t.Fatal("route not found")
	}

	if loaded.Connector().ID() != "dest1" {
		t.Errorf("connector mismatch: %s", loaded.Connector().ID())
	}

	// Verify filters are restored
	// Note: We'd need accessors in Route to check filters if they were private, 
	// but routingtable.Route has filters as private. 
	// I should add a check or use GetState to compare.
	
	state1 := route.GetState()
	state2 := loaded.GetState()
	
	if len(state1.Filters) != len(state2.Filters) {
		t.Errorf("filter count mismatch: %d vs %d", len(state1.Filters), len(state2.Filters))
	}
	
	for i, f := range state1.Filters {
		if f.Kind != state2.Filters[i].Kind {
			t.Errorf("filter %d kind mismatch: %s vs %s", i, f.Kind, state2.Filters[i].Kind)
		}
	}

	// Test Connector Persistence
	connRepo := NewSQLiteConnectorRepository(db)
	c1 := routingtable.Connector{IDValue: "c1", TypeValue: routingtable.SMPPC}
	if err := connRepo.Save(ctx, c1); err != nil {
		t.Fatal(err)
	}

	loadedC, err := connRepo.Load(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if loadedC.ID() != "c1" || loadedC.Type() != routingtable.SMPPC {
		t.Errorf("connector mismatch: %+v", loadedC)
	}
}
