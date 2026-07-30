package admin

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
)

func TestPostgresRebind(t *testing.T) {
	store := &Store{dialect: storeDialectPostgres}
	got := store.rebind("SELECT ? FROM x WHERE a=? AND b=?")
	if got != "SELECT $1 FROM x WHERE a=$2 AND b=$3" {
		t.Fatalf("rebind=%q", got)
	}
	sqlite := &Store{dialect: storeDialectSQLite}
	if got := sqlite.rebind("SELECT ?"); got != "SELECT ?" {
		t.Fatalf("sqlite rebind=%q", got)
	}
}

func TestPostgresSchemaIsStableAndNamespaced(t *testing.T) {
	first := postgresSchema("production")
	if first != postgresSchema("production") {
		t.Fatal("same namespace produced different PostgreSQL schemas")
	}
	if first == postgresSchema("staging") {
		t.Fatal("different namespaces share a PostgreSQL schema")
	}
}

func TestPostgresStoreIsSharedAndProfilesRestoreAtomically(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	first, err := OpenPostgresStore(ctx, dsn, "admin-store-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if _, err = first.db.ExecContext(ctx, `TRUNCATE
 admin_profiles,admin_httpccs,admin_filters,admin_smpps_users,
 admin_mo_interceptors,admin_mt_interceptors,admin_mo_routes,admin_routes,
 admin_connectors,admin_users,admin_groups`); err != nil {
		t.Fatal(err)
	}

	if err = first.UpsertGroup(ctx, StoredGroup{GID: "shared", Number: 1, SpecJSON: `{"gid":"shared"}`}, "t1"); err != nil {
		t.Fatal(err)
	}
	if err = first.UpsertUser(ctx, StoredUser{Username: "alice", UID: 1, SpecJSON: `{"username":"alice"}`}, "t1"); err != nil {
		t.Fatal(err)
	}
	if err = first.UpsertConnector(ctx, StoredConnector{
		Config:         smppc.Config{CID: "shared-cid", Host: "127.0.0.1", Port: 2775, SystemID: "system"},
		DesiredStarted: true,
	}, "t1"); err != nil {
		t.Fatal(err)
	}

	second, err := OpenPostgresStore(ctx, dsn, "admin-store-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if _, err = second.GetGroup(ctx, "shared"); err != nil {
		t.Fatalf("second node cannot read shared group: %v", err)
	}
	if _, err = second.GetUser(ctx, "alice"); err != nil {
		t.Fatalf("second node cannot read shared user: %v", err)
	}
	if connector, err := second.GetConnector(ctx, "shared-cid"); err != nil || !connector.DesiredStarted {
		t.Fatalf("second node connector=(%+v,%v)", connector, err)
	}

	profiles, err := NewProfileService(first, func() string { return "snapshot-time" })
	if err != nil {
		t.Fatal(err)
	}
	if err = profiles.Save(ctx, "known-good"); err != nil {
		t.Fatal(err)
	}
	if err = first.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = profiles.Load(ctx, "known-good"); err != nil {
		t.Fatal(err)
	}
	if _, err = second.GetUser(ctx, "alice"); err != nil {
		t.Fatalf("atomic profile restore is not visible to second node: %v", err)
	}

	isolated, err := OpenPostgresStore(ctx, dsn, "admin-store-test-isolated")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = isolated.Close() })
	if _, err = isolated.GetUser(ctx, "alice"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("another HA namespace can see alice: %v", err)
	}
}
