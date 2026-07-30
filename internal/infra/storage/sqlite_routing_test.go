package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
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
	route, err = route.WithConnectors([]routingtable.Connector{
		conn,
		{IDValue: "dest2", TypeValue: routingtable.HTTP},
	})
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
	loadedConnectors := loaded.Connectors()
	if len(loadedConnectors) != 2 || loadedConnectors[0].ID() != "dest1" || loadedConnectors[1].ID() != "dest2" {
		t.Fatalf("connector pool mismatch: %+v", loadedConnectors)
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

func TestSQLiteRouteInitMigratesLegacySchema(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TABLE mt_routes (
		routing_order INTEGER PRIMARY KEY, direction TEXT, connector_id TEXT,
		connector_type TEXT, rate REAL, is_default INTEGER, filters_json TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO mt_routes VALUES (0, ?, ?, ?, 1.0, 1, '[]')`, routingfilter.MT, "legacy-primary", routingtable.SMPPC)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSQLiteRouteRepository(db, routingfilter.MT)
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}
	routes, err := repo.LoadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	connectors := routes[0].Connectors()
	if len(connectors) != 1 || connectors[0].ID() != "legacy-primary" {
		t.Fatalf("legacy route connectors=%+v", connectors)
	}
}

func TestSQLiteRouteInitMigratesLegacySchemaConcurrently(t *testing.T) {
	for iteration := 0; iteration < 10; iteration++ {
		path := filepath.Join(t.TempDir(), "routes.db")
		setup, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = setup.Exec(`CREATE TABLE mt_routes (
			routing_order INTEGER PRIMARY KEY, direction TEXT, connector_id TEXT,
			connector_type TEXT, rate REAL, is_default INTEGER, filters_json TEXT
		)`)
		setup.Close()
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for worker := 0; worker < 2; worker++ {
			db, err := sql.Open("sqlite3", path)
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			wg.Add(1)
			go func(db *sql.DB) {
				defer wg.Done()
				defer db.Close()
				<-start
				results <- NewSQLiteRouteRepository(db, routingfilter.MT).Init(context.Background())
			}(db)
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatalf("iteration %d concurrent Init: %v", iteration, err)
			}
		}
		verify, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatal(err)
		}
		found, err := sqliteColumnExists(context.Background(), verify, "mt_routes", "connector_pool_json")
		verify.Close()
		if err != nil || !found {
			t.Fatalf("iteration %d column found=%v err=%v", iteration, found, err)
		}
	}
}
