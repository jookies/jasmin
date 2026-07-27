package admin

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// RouteProvisioner rebuilds and swaps the live routing table from the full set
// of admin route specs (opaque JSON, each an outbound.RouteConfig). The
// outbound runtime implements it. An error means the desired set is invalid or
// collides with a config route; the active table is left unchanged.
type RouteProvisioner interface {
	ApplyRoutes(ctx context.Context, routeSpecsJSON []string) error
}

// RouteService applies admin MT route CRUD: it rebuilds+swaps the live table
// (apply-first) and only then persists, so the store and the live table never
// diverge. A mutex serialises mutations.
type RouteService struct {
	store       *Store
	provisioner RouteProvisioner
	now         func() string
	mu          sync.Mutex
}

// NewRouteService builds the route admin service.
func NewRouteService(store *Store, provisioner RouteProvisioner, now func() string) (*RouteService, error) {
	if store == nil || provisioner == nil {
		return nil, errors.New("admin: route store and provisioner are required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	return &RouteService{store: store, provisioner: provisioner, now: now}, nil
}

// LoadAndApply re-applies every persisted admin route into the live table at
// boot (config routes + admin routes). A failure is returned; the gateway logs
// it and continues with config routes only.
func (s *RouteService) LoadAndApply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	specs, err := s.currentSpecs(ctx)
	if err != nil {
		return err
	}
	return s.provisioner.ApplyRoutes(ctx, specs)
}

// currentSpecs returns every stored route spec JSON in order.
func (s *RouteService) currentSpecs(ctx context.Context) ([]string, error) {
	stored, err := s.store.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	specs := make([]string, 0, len(stored))
	for _, route := range stored {
		specs = append(specs, route.SpecJSON)
	}
	return specs, nil
}

// specsWith returns the stored specs with the given order's spec replaced or
// appended (for a create/update apply-first dry run) or removed (delete).
func (s *RouteService) specsWith(ctx context.Context, order int, specJSON string, remove bool) ([]string, error) {
	stored, err := s.store.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	specs := make([]string, 0, len(stored)+1)
	replaced := false
	for _, route := range stored {
		if route.Order == order {
			replaced = true
			if remove {
				continue
			}
			specs = append(specs, specJSON)
			continue
		}
		specs = append(specs, route.SpecJSON)
	}
	if !replaced && !remove {
		specs = append(specs, specJSON)
	}
	return specs, nil
}

// PutRoute creates or replaces an admin route at order with specJSON. It
// applies the full desired set first (validating the spec and order via the
// provisioner) and persists only on success.
func (s *RouteService) PutRoute(ctx context.Context, order int, specJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	desired, err := s.specsWith(ctx, order, specJSON, false)
	if err != nil {
		return err
	}
	if err := s.provisioner.ApplyRoutes(ctx, desired); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.UpsertRoute(ctx, StoredRoute{Order: order, SpecJSON: specJSON}, s.now())
}

// DeleteRoute removes an admin route and re-applies the remaining set.
func (s *RouteService) DeleteRoute(ctx context.Context, order int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.store.GetRoute(ctx, order); err != nil {
		return err
	}
	desired, err := s.specsWith(ctx, order, "", true)
	if err != nil {
		return err
	}
	if err := s.provisioner.ApplyRoutes(ctx, desired); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.DeleteRoute(ctx, order)
}

// ListRoutes returns the persisted admin routes (order + raw spec JSON).
func (s *RouteService) ListRoutes(ctx context.Context) ([]StoredRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.ListRoutes(ctx)
}

// GetRoute returns one persisted admin route.
func (s *RouteService) GetRoute(ctx context.Context, order int) (StoredRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.GetRoute(ctx, order)
}
