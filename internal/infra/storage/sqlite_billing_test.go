package storage

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pumpitspace/jasmin/internal/core/billing"
)

func TestSQLitePersistence(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewSQLiteBillingStore(db)
	ctx := context.Background()

	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	userRepo := NewSQLiteUserRepository(store)
	groupRepo := NewSQLiteGroupRepository(store)

	// Test Group Persistence
	g := billing.NewGroup(100)
	g.SetBalance(150.5)
	g.SetSubmitSmCountQuota(1000)

	if err := groupRepo.Save(ctx, g); err != nil {
		t.Fatal(err)
	}

	loadedG, err := groupRepo.Load(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if loadedG.GID() != 100 || loadedG.Balance() != 150.5 {
		t.Errorf("Group mismatch: %+v", loadedG)
	}

	// Test User Persistence
	u := billing.NewUser(200)
	u.SetGroup(g)
	u.SetBalance(50.0)
	u.SetEarlyDecrementPercent(50)
	u.SetSubmitSmCountQuota(500)

	if err := userRepo.Save(ctx, u); err != nil {
		t.Fatal(err)
	}

	groups := map[int64]*billing.Group{100: g}
	loadedU, err := userRepo.Load(ctx, 200, groups)
	if err != nil {
		t.Fatal(err)
	}
	if loadedU.UID() != 200 || loadedU.Balance() != 50.0 {
		t.Errorf("User mismatch: %+v", loadedU)
	}
}
