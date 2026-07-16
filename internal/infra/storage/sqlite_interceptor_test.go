package storage

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

func TestSQLiteInterceptorPersistence(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	moRepo := NewSQLiteInterceptorRepository(db, routingfilter.MO)

	if err := moRepo.Init(ctx); err != nil {
		t.Fatal(err)
	}

	f1, _ := routingfilter.NewConnectorFilter("conn1")
	script := interceptor.Script{IDValue: "scr1", PyCode: "print('hello')"}
	intcp, err := interceptor.NewInterceptor(script, f1)
	if err != nil {
		t.Fatal(err)
	}

	if err := moRepo.Save(ctx, 10, intcp); err != nil {
		t.Fatal(err)
	}

	interceptors, err := moRepo.LoadAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	loaded, ok := interceptors[10]
	if !ok {
		t.Fatal("interceptor not found")
	}

	state1 := intcp.GetState()
	state2 := loaded.GetState()

	if state1.ScriptID != state2.ScriptID || state1.PyCode != state2.PyCode {
		t.Errorf("script mismatch: %s vs %s", state1.ScriptID, state2.ScriptID)
	}

	if len(state1.Filters) != len(state2.Filters) {
		t.Errorf("filter count mismatch")
	}
}
