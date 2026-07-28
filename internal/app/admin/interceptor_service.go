package admin

import (
	"context"
	"errors"
	"fmt"
)

// ErrInterceptorNotFound is returned when an admin interceptor order is absent
// for the requested direction.
var ErrInterceptorNotFound = errors.New("admin: interceptor not found")

// InterceptorDirection selects which interception table an entry belongs to.
type InterceptorDirection string

const (
	// InterceptMT runs pre-routing on the submit path.
	InterceptMT InterceptorDirection = "mt"
	// InterceptMO runs on the inbound deliver path.
	InterceptMO InterceptorDirection = "mo"
)

var interceptorTables = map[InterceptorDirection]orderedSpecTable{
	InterceptMT: {name: "admin_mt_interceptors", orderColumn: "interceptor_order", notFound: ErrInterceptorNotFound},
	InterceptMO: {name: "admin_mo_interceptors", orderColumn: "interceptor_order", notFound: ErrInterceptorNotFound},
}

// InterceptorProvisioner rebuilds and swaps a live interception table from the
// full set of admin specs (opaque JSON, each an outbound.InterceptorConfig).
// The gateway implements it over the MT and MO apply hooks.
type InterceptorProvisioner interface {
	ApplyInterceptors(ctx context.Context, direction InterceptorDirection, specsJSON []string) error
}

// directionProvisioner binds a provisioner to one direction so it satisfies the
// direction-agnostic SpecProvisioner.
type directionProvisioner struct {
	provisioner InterceptorProvisioner
	direction   InterceptorDirection
}

func (d directionProvisioner) Apply(ctx context.Context, specsJSON []string) error {
	return d.provisioner.ApplyInterceptors(ctx, d.direction, specsJSON)
}

// InterceptorService applies admin interceptor CRUD for both directions.
//
// Interceptor scripts are arbitrary Python executed on the gateway host. This
// service is only constructed when the operator has explicitly opted in
// (admin.allow_interceptor_editing); callers must not expose it otherwise.
type InterceptorService struct {
	byDirection map[InterceptorDirection]*orderedSpecService
}

// NewInterceptorService builds the interceptor admin service.
func NewInterceptorService(store *Store, provisioner InterceptorProvisioner, now func() string) (*InterceptorService, error) {
	if provisioner == nil {
		return nil, errors.New("admin: interceptor provisioner is required")
	}
	service := &InterceptorService{byDirection: make(map[InterceptorDirection]*orderedSpecService, len(interceptorTables))}
	for direction, table := range interceptorTables {
		inner, err := newOrderedSpecService(store, table, directionProvisioner{provisioner, direction}, now)
		if err != nil {
			return nil, err
		}
		service.byDirection[direction] = inner
	}
	return service, nil
}

func (s *InterceptorService) forDirection(direction InterceptorDirection) (*orderedSpecService, error) {
	inner, ok := s.byDirection[direction]
	if !ok {
		return nil, fmt.Errorf("%w: unknown interceptor direction %q", ErrInvalidRequest, direction)
	}
	return inner, nil
}

// LoadAndApply re-applies every persisted interceptor, both directions, at boot.
// Errors are joined so one broken direction does not hide the other.
func (s *InterceptorService) LoadAndApply(ctx context.Context) error {
	var errs []error
	for direction, inner := range s.byDirection {
		if err := inner.loadAndApply(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s interceptors: %w", direction, err))
		}
	}
	return errors.Join(errs...)
}

// PutInterceptor creates or replaces the interceptor at order in direction.
func (s *InterceptorService) PutInterceptor(ctx context.Context, direction InterceptorDirection, order int, specJSON string) error {
	inner, err := s.forDirection(direction)
	if err != nil {
		return err
	}
	return inner.put(ctx, order, specJSON)
}

// DeleteInterceptor removes one interceptor and re-applies the remaining set.
func (s *InterceptorService) DeleteInterceptor(ctx context.Context, direction InterceptorDirection, order int) error {
	inner, err := s.forDirection(direction)
	if err != nil {
		return err
	}
	return inner.delete(ctx, order)
}

// ListInterceptors returns the persisted interceptors for one direction.
func (s *InterceptorService) ListInterceptors(ctx context.Context, direction InterceptorDirection) ([]StoredSpec, error) {
	inner, err := s.forDirection(direction)
	if err != nil {
		return nil, err
	}
	return inner.list(ctx)
}

// GetInterceptor returns one persisted interceptor.
func (s *InterceptorService) GetInterceptor(ctx context.Context, direction InterceptorDirection, order int) (StoredSpec, error) {
	inner, err := s.forDirection(direction)
	if err != nil {
		return StoredSpec{}, err
	}
	return inner.get(ctx, order)
}
