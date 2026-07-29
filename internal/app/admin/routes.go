package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrRouteNotFound is returned when an admin route order is absent.
var ErrRouteNotFound = errors.New("admin: route not found")

// StoredRoute is one persisted admin MT route: its order (identity) and the
// opaque JSON spec (an outbound.RouteConfig, validated by the provisioner on
// apply — admin does not interpret it).
type StoredRoute struct {
	Order    int
	SpecJSON string
}

// ListRoutes returns every persisted admin route, ordered by route_order.
func (s *Store) ListRoutes(ctx context.Context) ([]StoredRoute, error) {
	rows, err := s.queryContext(ctx, `SELECT route_order, spec_json FROM admin_routes ORDER BY route_order`)
	if err != nil {
		return nil, fmt.Errorf("admin: list routes: %w", err)
	}
	defer rows.Close()
	var result []StoredRoute
	for rows.Next() {
		var route StoredRoute
		if err := rows.Scan(&route.Order, &route.SpecJSON); err != nil {
			return nil, fmt.Errorf("admin: scan route: %w", err)
		}
		result = append(result, route)
	}
	return result, rows.Err()
}

// GetRoute returns one persisted admin route by order.
func (s *Store) GetRoute(ctx context.Context, order int) (StoredRoute, error) {
	var route StoredRoute
	err := s.queryRowContext(ctx, `SELECT route_order, spec_json FROM admin_routes WHERE route_order=?`, order).
		Scan(&route.Order, &route.SpecJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredRoute{}, ErrRouteNotFound
	}
	if err != nil {
		return StoredRoute{}, fmt.Errorf("admin: get route %d: %w", order, err)
	}
	return route, nil
}

// UpsertRoute persists (insert or replace) an admin route by order.
func (s *Store) UpsertRoute(ctx context.Context, route StoredRoute, now string) error {
	_, err := s.execContext(ctx,
		`INSERT INTO admin_routes (route_order, spec_json, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(route_order) DO UPDATE SET spec_json=excluded.spec_json, updated_at=excluded.updated_at`,
		route.Order, route.SpecJSON, now)
	if err != nil {
		return fmt.Errorf("admin: upsert route %d: %w", route.Order, err)
	}
	return nil
}

// DeleteRoute removes an admin route by order.
func (s *Store) DeleteRoute(ctx context.Context, order int) error {
	result, err := s.execContext(ctx, `DELETE FROM admin_routes WHERE route_order=?`, order)
	if err != nil {
		return fmt.Errorf("admin: delete route %d: %w", order, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrRouteNotFound
	}
	return nil
}
