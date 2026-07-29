package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// Named specs are admin entities keyed by an operator-chosen string id whose
// body is opaque JSON, with no live runtime table behind them: named filters
// and HTTP client connectors. Both exist because jCli names things the routing
// engine only ever sees inlined -- a route embeds a copy of the filter at save
// time, exactly as the frozen console pickles the filter object into the route
// -- so removing a named filter later cannot orphan a route.
//
// Because there is nothing to apply, these services are store-only: no
// provisioner, no rollback, no reserved config identities.

// ErrFilterNotFound is returned when an admin filter fid is absent.
var ErrFilterNotFound = errors.New("admin: filter not found")

// ErrHTTPConnectorNotFound is returned when an admin httpcc cid is absent.
var ErrHTTPConnectorNotFound = errors.New("admin: http connector not found")

// StoredNamedSpec is one named entity: its id and opaque JSON body.
type StoredNamedSpec struct {
	ID       string
	SpecJSON string
}

func (s *Store) listNamedSpecs(ctx context.Context, table, idColumn string) ([]StoredNamedSpec, error) {
	query := fmt.Sprintf(`SELECT %s, spec_json FROM %s ORDER BY %s`, idColumn, table, idColumn)
	rows, err := s.queryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("admin: list %s: %w", table, err)
	}
	defer rows.Close()
	var result []StoredNamedSpec
	for rows.Next() {
		var spec StoredNamedSpec
		if err := rows.Scan(&spec.ID, &spec.SpecJSON); err != nil {
			return nil, fmt.Errorf("admin: scan %s: %w", table, err)
		}
		result = append(result, spec)
	}
	return result, rows.Err()
}

func (s *Store) getNamedSpec(ctx context.Context, table, idColumn, id string, missing error) (StoredNamedSpec, error) {
	query := fmt.Sprintf(`SELECT %s, spec_json FROM %s WHERE %s=?`, idColumn, table, idColumn)
	var spec StoredNamedSpec
	err := s.queryRowContext(ctx, query, id).Scan(&spec.ID, &spec.SpecJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredNamedSpec{}, missing
	}
	if err != nil {
		return StoredNamedSpec{}, fmt.Errorf("admin: get %s %q: %w", table, id, err)
	}
	return spec, nil
}

func (s *Store) upsertNamedSpec(ctx context.Context, table, idColumn string, spec StoredNamedSpec, now string) error {
	query := fmt.Sprintf(
		`INSERT INTO %s (%s, spec_json, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(%s) DO UPDATE SET spec_json=excluded.spec_json, updated_at=excluded.updated_at`,
		table, idColumn, idColumn)
	if _, err := s.execContext(ctx, query, spec.ID, spec.SpecJSON, now); err != nil {
		return fmt.Errorf("admin: upsert %s %q: %w", table, spec.ID, err)
	}
	return nil
}

func (s *Store) deleteNamedSpec(ctx context.Context, table, idColumn, id string, missing error) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE %s=?`, table, idColumn)
	result, err := s.execContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("admin: delete %s %q: %w", table, id, err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return missing
	}
	return nil
}

// NamedSpecService is the shared CRUD for a named, store-only entity.
type NamedSpecService struct {
	store    *Store
	table    string
	idColumn string
	missing  error
	now      func() string
	mu       sync.Mutex
}

// NewFilterService builds the named-filter registry.
func NewFilterService(store *Store, now func() string) (*NamedSpecService, error) {
	return newNamedSpecService(store, "admin_filters", "fid", ErrFilterNotFound, now)
}

// NewHTTPConnectorService builds the HTTP client connector registry.
func NewHTTPConnectorService(store *Store, now func() string) (*NamedSpecService, error) {
	return newNamedSpecService(store, "admin_httpccs", "cid", ErrHTTPConnectorNotFound, now)
}

func newNamedSpecService(store *Store, table, idColumn string, missing error, now func() string) (*NamedSpecService, error) {
	if store == nil {
		return nil, errors.New("admin: store is required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	return &NamedSpecService{store: store, table: table, idColumn: idColumn, missing: missing, now: now}, nil
}

// List returns every stored entity, ordered by id.
func (s *NamedSpecService) List(ctx context.Context) ([]StoredNamedSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.listNamedSpecs(ctx, s.table, s.idColumn)
}

// Get returns one stored entity.
func (s *NamedSpecService) Get(ctx context.Context, id string) (StoredNamedSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.getNamedSpec(ctx, s.table, s.idColumn, id, s.missing)
}

// Put inserts or replaces one entity.
func (s *NamedSpecService) Put(ctx context.Context, id, specJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.upsertNamedSpec(ctx, s.table, s.idColumn, StoredNamedSpec{ID: id, SpecJSON: specJSON}, s.now())
}

// Delete removes one entity.
func (s *NamedSpecService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.deleteNamedSpec(ctx, s.table, s.idColumn, id, s.missing)
}
