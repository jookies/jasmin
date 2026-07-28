package admin

import (
	"context"
	"errors"
)

// ErrMORouteNotFound is returned when an admin MO route order is absent.
var ErrMORouteNotFound = errors.New("admin: MO route not found")

// moRouteTable is the SQLite table backing admin MO routes.
var moRouteTable = orderedSpecTable{
	name:        "admin_mo_routes",
	orderColumn: "route_order",
	notFound:    ErrMORouteNotFound,
}

// MORouteProvisioner rebuilds and swaps the live MO dispatch table from the
// full set of admin MO route specs (opaque JSON, each a modispatch.RouteConfig).
// The gateway implements it over modispatch.Service.ApplyRoutes.
type MORouteProvisioner interface {
	ApplyMORoutes(ctx context.Context, routeSpecsJSON []string) error
}

// moRouteProvisionerAdapter lets a MORouteProvisioner satisfy SpecProvisioner,
// keeping the public interface named for its domain.
type moRouteProvisionerAdapter struct{ provisioner MORouteProvisioner }

func (a moRouteProvisionerAdapter) Apply(ctx context.Context, specsJSON []string) error {
	return a.provisioner.ApplyMORoutes(ctx, specsJSON)
}

// MORouteService applies admin MO route CRUD: apply-first to the live dispatch
// table, then persist, so the store and the live table never diverge.
type MORouteService struct {
	inner *orderedSpecService
}

// NewMORouteService builds the MO route admin service.
func NewMORouteService(store *Store, provisioner MORouteProvisioner, now func() string) (*MORouteService, error) {
	if provisioner == nil {
		return nil, errors.New("admin: MO route provisioner is required")
	}
	inner, err := newOrderedSpecService(store, moRouteTable, moRouteProvisionerAdapter{provisioner}, now)
	if err != nil {
		return nil, err
	}
	return &MORouteService{inner: inner}, nil
}

// LoadAndApply re-applies every persisted admin MO route at boot.
func (s *MORouteService) LoadAndApply(ctx context.Context) error {
	return s.inner.loadAndApply(ctx)
}

// PutRoute creates or replaces the admin MO route at order.
func (s *MORouteService) PutRoute(ctx context.Context, order int, specJSON string) error {
	return s.inner.put(ctx, order, specJSON)
}

// DeleteRoute removes an admin MO route and re-applies the remaining set.
func (s *MORouteService) DeleteRoute(ctx context.Context, order int) error {
	return s.inner.delete(ctx, order)
}

// ListRoutes returns the persisted admin MO routes (order + raw spec JSON).
func (s *MORouteService) ListRoutes(ctx context.Context) ([]StoredSpec, error) {
	return s.inner.list(ctx)
}

// GetRoute returns one persisted admin MO route.
func (s *MORouteService) GetRoute(ctx context.Context, order int) (StoredSpec, error) {
	return s.inner.get(ctx, order)
}
