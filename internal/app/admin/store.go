// Package admin is the gateway's runtime provisioning plane: an authenticated
// HTTP API backed by an embedded SQLite store that persists admin-created
// connectors and applies them live through the smppc manager, surviving
// restart. It is deliberately additive to the --config connectors (which stay
// config-driven); admin owns its own set, keyed by cid, and cannot collide
// with a config connector.
package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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

// Store persists admin provisioning state in SQLite.
type Store struct {
	db *sql.DB
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
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// StoredConnector is one persisted admin connector.
type StoredConnector struct {
	Config         smppc.Config
	DesiredStarted bool
}

// ListConnectors returns every persisted admin connector, ordered by cid.
func (s *Store) ListConnectors(ctx context.Context) ([]StoredConnector, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT config_json, desired_started FROM admin_connectors ORDER BY cid`)
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
	err := s.db.QueryRowContext(ctx, `SELECT config_json, desired_started FROM admin_connectors WHERE cid=?`, cid).Scan(&configJSON, &started)
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
	_, err = s.db.ExecContext(ctx,
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
	result, err := s.db.ExecContext(ctx, `UPDATE admin_connectors SET desired_started=?, updated_at=? WHERE cid=?`, flag, now, cid)
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
	result, err := s.db.ExecContext(ctx, `DELETE FROM admin_connectors WHERE cid=?`, cid)
	if err != nil {
		return fmt.Errorf("admin: delete connector %q: %w", cid, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrConnectorNotFound
	}
	return nil
}
