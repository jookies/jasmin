package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// Several admin entities share one shape: an integer order is the identity, the
// payload is an opaque JSON spec that only the provisioner interprets, and a
// mutation means "apply the whole desired set live, then persist". MT routes,
// MO routes and interceptors are all this. The store helpers and service below
// implement that shape once.

// StoredSpec is one persisted order-keyed spec.
type StoredSpec struct {
	Order    int
	SpecJSON string
}

// orderedSpecTable names a SQLite table with an (order, spec_json, updated_at)
// shape. Values are package constants and never come from user input, so
// interpolating the names into SQL is safe.
type orderedSpecTable struct {
	name        string
	orderColumn string
	notFound    error
}

func (s *Store) listOrderedSpecs(ctx context.Context, table orderedSpecTable) ([]StoredSpec, error) {
	query := fmt.Sprintf(`SELECT %s, spec_json FROM %s ORDER BY %s`, table.orderColumn, table.name, table.orderColumn)
	rows, err := s.queryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("admin: list %s: %w", table.name, err)
	}
	defer rows.Close()
	var result []StoredSpec
	for rows.Next() {
		var spec StoredSpec
		if err := rows.Scan(&spec.Order, &spec.SpecJSON); err != nil {
			return nil, fmt.Errorf("admin: scan %s: %w", table.name, err)
		}
		result = append(result, spec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: iterate %s: %w", table.name, err)
	}
	return result, nil
}

func (s *Store) getOrderedSpec(ctx context.Context, table orderedSpecTable, order int) (StoredSpec, error) {
	query := fmt.Sprintf(`SELECT %s, spec_json FROM %s WHERE %s = ?`, table.orderColumn, table.name, table.orderColumn)
	var spec StoredSpec
	err := s.queryRowContext(ctx, query, order).Scan(&spec.Order, &spec.SpecJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredSpec{}, fmt.Errorf("%w: %d", table.notFound, order)
	}
	if err != nil {
		return StoredSpec{}, fmt.Errorf("admin: get %s %d: %w", table.name, order, err)
	}
	return spec, nil
}

func (s *Store) upsertOrderedSpec(ctx context.Context, table orderedSpecTable, spec StoredSpec, now string) error {
	query := fmt.Sprintf(
		`INSERT INTO %s (%s, spec_json, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(%s) DO UPDATE SET spec_json = excluded.spec_json, updated_at = excluded.updated_at`,
		table.name, table.orderColumn, table.orderColumn)
	if _, err := s.execContext(ctx, query, spec.Order, spec.SpecJSON, now); err != nil {
		return fmt.Errorf("admin: upsert %s %d: %w", table.name, spec.Order, err)
	}
	return nil
}

func (s *Store) deleteOrderedSpec(ctx context.Context, table orderedSpecTable, order int) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE %s = ?`, table.name, table.orderColumn)
	if _, err := s.execContext(ctx, query, order); err != nil {
		return fmt.Errorf("admin: delete %s %d: %w", table.name, order, err)
	}
	return nil
}

// SpecProvisioner rebuilds and swaps live state from the full desired set of
// specs. An error means the set is invalid or collides with config-owned
// entries; the active state must be left unchanged.
type SpecProvisioner interface {
	Apply(ctx context.Context, specsJSON []string) error
}

// orderedSpecService is the apply-first-then-persist CRUD engine shared by the
// order-keyed admin entities. It never interprets a spec: the provisioner
// validates by applying, so the store can only ever hold specs that were live.
type orderedSpecService struct {
	store       *Store
	table       orderedSpecTable
	provisioner SpecProvisioner
	now         func() string
	mu          sync.Mutex
}

func newOrderedSpecService(store *Store, table orderedSpecTable, provisioner SpecProvisioner, now func() string) (*orderedSpecService, error) {
	if store == nil || provisioner == nil {
		return nil, fmt.Errorf("admin: %s store and provisioner are required", table.name)
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	return &orderedSpecService{store: store, table: table, provisioner: provisioner, now: now}, nil
}

// loadAndApply re-applies every persisted spec at boot.
func (s *orderedSpecService) loadAndApply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	specs, err := s.currentSpecs(ctx)
	if err != nil {
		return err
	}
	return s.provisioner.Apply(ctx, specs)
}

func (s *orderedSpecService) currentSpecs(ctx context.Context) ([]string, error) {
	stored, err := s.store.listOrderedSpecs(ctx, s.table)
	if err != nil {
		return nil, err
	}
	specs := make([]string, 0, len(stored))
	for _, entry := range stored {
		specs = append(specs, entry.SpecJSON)
	}
	return specs, nil
}

// specsWith returns the stored specs with order's spec replaced/appended, or
// removed — the desired set for an apply-first dry run.
func (s *orderedSpecService) specsWith(ctx context.Context, order int, specJSON string, remove bool) ([]string, error) {
	stored, err := s.store.listOrderedSpecs(ctx, s.table)
	if err != nil {
		return nil, err
	}
	specs := make([]string, 0, len(stored)+1)
	replaced := false
	for _, entry := range stored {
		if entry.Order == order {
			replaced = true
			if remove {
				continue
			}
			specs = append(specs, specJSON)
			continue
		}
		specs = append(specs, entry.SpecJSON)
	}
	if !replaced && !remove {
		specs = append(specs, specJSON)
	}
	return specs, nil
}

// put creates or replaces the spec at order: apply the full desired set first,
// persist only if the live apply succeeded.
func (s *orderedSpecService) put(ctx context.Context, order int, specJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	desired, err := s.specsWith(ctx, order, specJSON, false)
	if err != nil {
		return err
	}
	if err := s.provisioner.Apply(ctx, desired); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.upsertOrderedSpec(ctx, s.table, StoredSpec{Order: order, SpecJSON: specJSON}, s.now())
}

func (s *orderedSpecService) delete(ctx context.Context, order int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.store.getOrderedSpec(ctx, s.table, order); err != nil {
		return err
	}
	desired, err := s.specsWith(ctx, order, "", true)
	if err != nil {
		return err
	}
	if err := s.provisioner.Apply(ctx, desired); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.deleteOrderedSpec(ctx, s.table, order)
}

func (s *orderedSpecService) list(ctx context.Context) ([]StoredSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.listOrderedSpecs(ctx, s.table)
}

func (s *orderedSpecService) get(ctx context.Context, order int) (StoredSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.getOrderedSpec(ctx, s.table, order)
}
