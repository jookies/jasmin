// Package admin is the gateway's runtime provisioning plane: an authenticated
// HTTP API backed by SQLite in local/single-node mode and a namespace-isolated
// PostgreSQL schema in HA mode. Persisted entities apply live and survive
// restart or standby promotion. Admin remains additive to --config connectors
// and cannot collide with config-owned identities.
package admin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (works with CGO_ENABLED=0)

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// ErrConnectorNotFound is returned when an admin connector cid is absent.
var ErrConnectorNotFound = errors.New("admin: connector not found")

const connectorSchema = `
CREATE TABLE IF NOT EXISTS admin_connectors (
    cid             TEXT PRIMARY KEY,
    config_json     TEXT NOT NULL,
    desired_started INTEGER NOT NULL DEFAULT 1,
    updated_at      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_routes (
    route_order INTEGER PRIMARY KEY,
    spec_json   TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_users (
    username   TEXT PRIMARY KEY,
    uid        INTEGER NOT NULL UNIQUE,
    spec_json  TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_mo_routes (
    route_order INTEGER PRIMARY KEY,
    spec_json   TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_mt_interceptors (
    interceptor_order INTEGER PRIMARY KEY,
    spec_json         TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_mo_interceptors (
    interceptor_order INTEGER PRIMARY KEY,
    spec_json         TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_smpps_users (
    system_id  TEXT PRIMARY KEY,
    spec_json  TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_filters (
    fid        TEXT PRIMARY KEY,
    spec_json  TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_httpccs (
    cid        TEXT PRIMARY KEY,
    spec_json  TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_profiles (
    profile    TEXT NOT NULL,
    table_name TEXT NOT NULL,
    payload    TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (profile, table_name)
);
CREATE TABLE IF NOT EXISTS admin_groups (
    gid        TEXT PRIMARY KEY,
    gid_number INTEGER NOT NULL UNIQUE,
    spec_json  TEXT NOT NULL,
    updated_at TEXT NOT NULL
);`

type storeDialect uint8

const (
	storeDialectSQLite storeDialect = iota
	storeDialectPostgres
)

// Store persists admin provisioning state. SQLite remains the local/test and
// single-node adapter; PostgreSQL is the shared production HA control plane.
type Store struct {
	db      *sql.DB
	dialect storeDialect
}

// OpenStore opens (creating if needed) the SQLite admin database at path and
// applies the schema. A ":memory:" path is valid for tests.
func OpenStore(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("admin: open sqlite %q: %w", path, err)
	}
	// SQLite tolerates only one writer; the admin plane is low-traffic, so a
	// single connection avoids "database is locked" under concurrent writes.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, connectorSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("admin: init schema: %w", err)
	}
	return &Store{db: db, dialect: storeDialectSQLite}, nil
}

// OpenPostgresStore opens the shared production HA control plane and applies
// the same idempotent schema used by SQLite. It is selected automatically by
// the gateway when HA is configured, so a promoted standby replays exactly the
// active node's persisted users/routes/connectors instead of a node-local file.
func OpenPostgresStore(ctx context.Context, dsn, namespace string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("admin: empty PostgreSQL DSN")
	}
	if strings.TrimSpace(namespace) == "" {
		return nil, errors.New("admin: empty PostgreSQL namespace")
	}
	schema := postgresSchema(namespace)
	pgxConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("admin: parse PostgreSQL DSN: %w", err)
	}
	// RuntimeParams are applied to every pooled connection, not just the one
	// that creates the schema. This keeps independently fenced deployments
	// sharing one database from reading or mutating each other's control state.
	pgxConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*pgxConfig)
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("admin: connect PostgreSQL: %w", err)
	}
	if _, err = db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("admin: create PostgreSQL schema: %w", err)
	}
	if _, err = db.ExecContext(ctx, connectorSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("admin: init PostgreSQL schema: %w", err)
	}
	return &Store{db: db, dialect: storeDialectPostgres}, nil
}

// postgresSchema maps an arbitrary operator namespace to a short safe
// PostgreSQL identifier. The hash avoids quoting/injection concerns and keeps
// the name below PostgreSQL's 63-byte identifier limit.
func postgresSchema(namespace string) string {
	sum := sha256.Sum256([]byte("jasmin-go/admin-control-plane/v1\x00" + namespace))
	return fmt.Sprintf("jasmin_admin_%x", sum[:12])
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) rebind(query string) string {
	if s == nil || s.dialect != storeDialectPostgres || !strings.Contains(query, "?") {
		return query
	}
	var output strings.Builder
	output.Grow(len(query) + 8)
	parameter := 1
	for _, character := range query {
		if character == '?' {
			fmt.Fprintf(&output, "$%d", parameter)
			parameter++
			continue
		}
		output.WriteRune(character)
	}
	return output.String()
}

func (s *Store) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.rebind(query), args...)
}

func (s *Store) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.rebind(query), args...)
}

func (s *Store) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.rebind(query), args...)
}

type storeTx struct {
	tx    *sql.Tx
	store *Store
}

func (s *Store) beginTx(ctx context.Context) (*storeTx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &storeTx{tx: tx, store: s}, nil
}

func (tx *storeTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.tx.ExecContext(ctx, tx.store.rebind(query), args...)
}

func (tx *storeTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.tx.QueryContext(ctx, tx.store.rebind(query), args...)
}

func (tx *storeTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.tx.QueryRowContext(ctx, tx.store.rebind(query), args...)
}

func (tx *storeTx) Commit() error   { return tx.tx.Commit() }
func (tx *storeTx) Rollback() error { return tx.tx.Rollback() }

// StoredConnector is one persisted admin connector.
type StoredConnector struct {
	Config         smppc.Config
	DesiredStarted bool
}

// ListConnectors returns every persisted admin connector, ordered by cid.
func (s *Store) ListConnectors(ctx context.Context) ([]StoredConnector, error) {
	rows, err := s.queryContext(ctx, `SELECT config_json, desired_started FROM admin_connectors ORDER BY cid`)
	if err != nil {
		return nil, fmt.Errorf("admin: list connectors: %w", err)
	}
	defer rows.Close()
	var result []StoredConnector
	for rows.Next() {
		var configJSON string
		var started int
		if err := rows.Scan(&configJSON, &started); err != nil {
			return nil, fmt.Errorf("admin: scan connector: %w", err)
		}
		var config smppc.Config
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			return nil, fmt.Errorf("admin: decode stored connector %q: %w", config.CID, err)
		}
		result = append(result, StoredConnector{Config: config, DesiredStarted: started != 0})
	}
	return result, rows.Err()
}

// GetConnector returns one persisted admin connector.
func (s *Store) GetConnector(ctx context.Context, cid string) (StoredConnector, error) {
	var configJSON string
	var started int
	err := s.queryRowContext(ctx, `SELECT config_json, desired_started FROM admin_connectors WHERE cid=?`, cid).Scan(&configJSON, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredConnector{}, ErrConnectorNotFound
	}
	if err != nil {
		return StoredConnector{}, fmt.Errorf("admin: get connector %q: %w", cid, err)
	}
	var config smppc.Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return StoredConnector{}, fmt.Errorf("admin: decode connector %q: %w", cid, err)
	}
	return StoredConnector{Config: config, DesiredStarted: started != 0}, nil
}

// UpsertConnector persists (insert or replace) an admin connector.
func (s *Store) UpsertConnector(ctx context.Context, connector StoredConnector, now string) error {
	configJSON, err := json.Marshal(connector.Config)
	if err != nil {
		return fmt.Errorf("admin: encode connector %q: %w", connector.Config.CID, err)
	}
	started := 0
	if connector.DesiredStarted {
		started = 1
	}
	_, err = s.execContext(ctx,
		`INSERT INTO admin_connectors (cid, config_json, desired_started, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(cid) DO UPDATE SET config_json=excluded.config_json, desired_started=excluded.desired_started, updated_at=excluded.updated_at`,
		connector.Config.CID, string(configJSON), started, now)
	if err != nil {
		return fmt.Errorf("admin: upsert connector %q: %w", connector.Config.CID, err)
	}
	return nil
}

// SetDesiredStarted updates only the desired-started flag.
func (s *Store) SetDesiredStarted(ctx context.Context, cid string, started bool, now string) error {
	flag := 0
	if started {
		flag = 1
	}
	result, err := s.execContext(ctx, `UPDATE admin_connectors SET desired_started=?, updated_at=? WHERE cid=?`, flag, now, cid)
	if err != nil {
		return fmt.Errorf("admin: set desired_started %q: %w", cid, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrConnectorNotFound
	}
	return nil
}

// DeleteConnector removes an admin connector.
func (s *Store) DeleteConnector(ctx context.Context, cid string) error {
	result, err := s.execContext(ctx, `DELETE FROM admin_connectors WHERE cid=?`, cid)
	if err != nil {
		return fmt.Errorf("admin: delete connector %q: %w", cid, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrConnectorNotFound
	}
	return nil
}
