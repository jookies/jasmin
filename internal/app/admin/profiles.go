package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Configuration profiles: named snapshots of the whole admin store.
//
// jCli's `persist -p NAME` / `load -p NAME` are how operators keep a known-good
// configuration to roll back to. The Go admin plane persists every mutation as
// it applies it, so "persist" has nothing to flush -- but a *named snapshot* is
// a real capability with no equivalent, and the honest way to support the
// command is to implement it rather than to accept the name and load something
// else.
//
// A snapshot is the row set of every admin table, stored as JSON under the
// profile name. Restoring replaces those tables and the caller re-applies each
// service, exactly as the gateway does at boot.

// ErrProfileNotFound is returned when a named profile has never been persisted.
var ErrProfileNotFound = errors.New("admin: profile not found")

// snapshotTables are the tables a profile covers, in a fixed order so a restore
// is deterministic.
var snapshotTables = []string{
	"admin_groups",
	"admin_users",
	"admin_connectors",
	"admin_routes",
	"admin_mo_routes",
	"admin_mt_interceptors",
	"admin_mo_interceptors",
	"admin_smpps_users",
	"admin_filters",
	"admin_httpccs",
	// Termination connectors have no frozen RouterPB family, so they are part
	// of the whole-store snapshot only. Leaving them out would make a profile
	// restore roll the SMPP connectors back while a termination connector kept
	// running its old config, which is the silent divergence a rollback exists
	// to avoid.
	"admin_termination_connectors",
}

// ProfileService saves and restores named configuration snapshots.
type ProfileService struct {
	store *Store
	now   func() string
	mu    sync.Mutex
}

// NewProfileService builds the profile service.
func NewProfileService(store *Store, now func() string) (*ProfileService, error) {
	if store == nil {
		return nil, errors.New("admin: store is required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	return &ProfileService{store: store, now: now}, nil
}

// Save snapshots every admin table under the profile name, replacing any
// previous snapshot of that name.
func (s *ProfileService) Save(ctx context.Context, profile string) error {
	return s.SaveScope(ctx, profile, "all")
}

// SaveScope snapshots only one frozen RouterPB entity family.
func (s *ProfileService) SaveScope(ctx context.Context, profile, scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tables, err := profileTables(scope)
	if err != nil {
		return err
	}

	transaction, err := s.store.beginTx(ctx)
	if err != nil {
		return fmt.Errorf("admin: begin profile save: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	for _, table := range tables {
		rows, err := readTableRows(ctx, transaction, table)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(rows)
		if err != nil {
			return fmt.Errorf("admin: encode %s snapshot: %w", table, err)
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO admin_profiles (profile, table_name, payload, updated_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT(profile, table_name) DO UPDATE SET payload=excluded.payload, updated_at=excluded.updated_at`,
			profile, table, string(payload), s.now()); err != nil {
			return fmt.Errorf("admin: save %s snapshot: %w", table, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("admin: commit profile save: %w", err)
	}
	return nil
}

// Load restores a snapshot into the admin tables. The caller must re-apply the
// services afterwards; this only moves rows.
func (s *ProfileService) Load(ctx context.Context, profile string) error {
	return s.LoadScope(ctx, profile, "all")
}

// LoadScope restores only one frozen RouterPB entity family. The replacement
// of that family's rows remains one store transaction.
func (s *ProfileService) LoadScope(ctx context.Context, profile, scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tables, err := profileTables(scope)
	if err != nil {
		return err
	}

	var count int
	for _, table := range tables {
		var present int
		if err := s.store.queryRowContext(ctx,
			`SELECT COUNT(*) FROM admin_profiles WHERE profile=? AND table_name=?`, profile, table).Scan(&present); err != nil {
			return fmt.Errorf("admin: look up profile %q scope %q: %w", profile, scope, err)
		}
		count += present
	}
	if count == 0 {
		return ErrProfileNotFound
	}

	transaction, err := s.store.beginTx(ctx)
	if err != nil {
		return fmt.Errorf("admin: begin profile load: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	// Restore in reverse order so a table is emptied before anything that might
	// reference it, and the whole restore is one transaction: a half-restored
	// configuration is worse than a failed one.
	for index := len(tables) - 1; index >= 0; index-- {
		if _, err := transaction.ExecContext(ctx, "DELETE FROM "+tables[index]); err != nil {
			return fmt.Errorf("admin: clear %s: %w", tables[index], err)
		}
	}
	for _, table := range tables {
		var payload string
		err := transaction.QueryRowContext(ctx,
			`SELECT payload FROM admin_profiles WHERE profile=? AND table_name=?`, profile, table).Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			continue // a table that did not exist when the snapshot was taken
		}
		if err != nil {
			return fmt.Errorf("admin: read %s snapshot: %w", table, err)
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(payload), &rows); err != nil {
			return fmt.Errorf("admin: decode %s snapshot: %w", table, err)
		}
		if err := writeTableRows(ctx, transaction, table, rows); err != nil {
			return err
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("admin: commit profile load: %w", err)
	}
	return nil
}

func profileTables(scope string) ([]string, error) {
	switch scope {
	case "", "all":
		return append([]string(nil), snapshotTables...), nil
	case "groups":
		return []string{"admin_groups"}, nil
	case "users":
		return []string{"admin_users"}, nil
	case "mtroutes":
		return []string{"admin_routes"}, nil
	case "moroutes":
		return []string{"admin_mo_routes"}, nil
	case "mtinterceptors":
		return []string{"admin_mt_interceptors"}, nil
	case "mointerceptors":
		return []string{"admin_mo_interceptors"}, nil
	case "connectors":
		return []string{"admin_connectors"}, nil
	default:
		return nil, fmt.Errorf("%w: invalid profile scope %q", ErrInvalidRequest, scope)
	}
}

// readTableRows reads a whole table as column-keyed maps.
func readTableRows(ctx context.Context, transaction *storeTx, table string) ([]map[string]any, error) {
	rows, err := transaction.QueryContext(ctx, "SELECT * FROM "+table)
	if err != nil {
		return nil, fmt.Errorf("admin: read %s: %w", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("admin: columns of %s: %w", table, err)
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, fmt.Errorf("admin: scan %s: %w", table, err)
		}
		row := make(map[string]any, len(columns))
		for index, column := range columns {
			// SQLite hands back []byte for TEXT; JSON must carry it as a string
			// or a restore would re-insert base64.
			if raw, isBytes := values[index].([]byte); isBytes {
				row[column] = string(raw)
				continue
			}
			row[column] = values[index]
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// writeTableRows inserts snapshot rows back into a table.
func writeTableRows(ctx context.Context, transaction *storeTx, table string, rows []map[string]any) error {
	for _, row := range rows {
		columns := make([]string, 0, len(row))
		placeholders := make([]string, 0, len(row))
		values := make([]any, 0, len(row))
		for column, value := range row {
			columns = append(columns, column)
			placeholders = append(placeholders, "?")
			values = append(values, value)
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			table, joinComma(columns), joinComma(placeholders))
		if _, err := transaction.ExecContext(ctx, query, values...); err != nil {
			return fmt.Errorf("admin: restore row into %s: %w", table, err)
		}
	}
	return nil
}

func joinComma(items []string) string {
	result := ""
	for index, item := range items {
		if index > 0 {
			result += ", "
		}
		result += item
	}
	return result
}
