package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
)

type SQLiteRouteRepository struct {
	db        *sql.DB
	direction routingfilter.Direction
}

func NewSQLiteRouteRepository(db *sql.DB, direction routingfilter.Direction) *SQLiteRouteRepository {
	return &SQLiteRouteRepository{db: db, direction: direction}
}

func (r *SQLiteRouteRepository) Init(ctx context.Context) error {
	tableName := "mo_routes"
	if r.direction == routingfilter.MT {
		tableName = "mt_routes"
	}

	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			routing_order INTEGER PRIMARY KEY,
			direction TEXT,
			connector_id TEXT,
			connector_type TEXT,
			rate REAL,
			is_default INTEGER,
			filters_json TEXT
		);
		CREATE TABLE IF NOT EXISTS connectors (
			id TEXT PRIMARY KEY,
			type TEXT
		);
	`, tableName)

	_, err := r.db.ExecContext(ctx, query)
	return err
}

func (r *SQLiteRouteRepository) Save(ctx context.Context, order int, route routingtable.Route) error {
	tableName := "mo_routes"
	if r.direction == routingfilter.MT {
		tableName = "mt_routes"
	}

	state := route.GetState()
	filtersJSON, err := json.Marshal(state.Filters)
	if err != nil {
		return err
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (routing_order, direction, connector_id, connector_type, rate, is_default, filters_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(routing_order) DO UPDATE SET
			direction = excluded.direction,
			connector_id = excluded.connector_id,
			connector_type = excluded.connector_type,
			rate = excluded.rate,
			is_default = excluded.is_default,
			filters_json = excluded.filters_json
	`, tableName)

	_, err = r.db.ExecContext(ctx, query, order, state.Direction, state.ConnectorID, state.ConnectorType, state.Rate, state.DefaultRoute, string(filtersJSON))
	return err
}

func (r *SQLiteRouteRepository) LoadAll(ctx context.Context) (map[int]routingtable.Route, error) {
	tableName := "mo_routes"
	if r.direction == routingfilter.MT {
		tableName = "mt_routes"
	}

	query := fmt.Sprintf(`SELECT routing_order, direction, connector_id, connector_type, rate, is_default, filters_json FROM %s`, tableName)
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := make(map[int]routingtable.Route)
	for rows.Next() {
		var order int
		var state routingtable.RouteState
		var filtersJSON string
		if err := rows.Scan(&order, &state.Direction, &state.ConnectorID, &state.ConnectorType, &state.Rate, &state.DefaultRoute, &filtersJSON); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(filtersJSON), &state.Filters); err != nil {
			return nil, err
		}

		route, err := routingtable.FromRouteState(state)
		if err != nil {
			return nil, err
		}
		routes[order] = route
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return routes, nil
}

func (r *SQLiteRouteRepository) Delete(ctx context.Context, order int) error {
	tableName := "mo_routes"
	if r.direction == routingfilter.MT {
		tableName = "mt_routes"
	}

	query := fmt.Sprintf(`DELETE FROM %s WHERE routing_order = ?`, tableName)
	_, err := r.db.ExecContext(ctx, query, order)
	return err
}

type SQLiteConnectorRepository struct {
	db *sql.DB
}

func NewSQLiteConnectorRepository(db *sql.DB) *SQLiteConnectorRepository {
	return &SQLiteConnectorRepository{db: db}
}

func (r *SQLiteConnectorRepository) Save(ctx context.Context, c routingtable.Connector) error {
	state := c.GetState()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO connectors (id, type)
		VALUES (?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type
	`, state.ID, state.Type)
	return err
}

func (r *SQLiteConnectorRepository) Load(ctx context.Context, id string) (routingtable.Connector, error) {
	var state routingtable.ConnectorState
	err := r.db.QueryRowContext(ctx, `SELECT id, type FROM connectors WHERE id = ?`, id).Scan(&state.ID, &state.Type)
	if err != nil {
		return routingtable.Connector{}, err
	}
	return routingtable.FromConnectorState(state), nil
}

func (r *SQLiteConnectorRepository) List(ctx context.Context) ([]routingtable.Connector, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, type FROM connectors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var connectors []routingtable.Connector
	for rows.Next() {
		var state routingtable.ConnectorState
		if err := rows.Scan(&state.ID, &state.Type); err != nil {
			return nil, err
		}
		connectors = append(connectors, routingtable.FromConnectorState(state))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return connectors, nil
}

func (r *SQLiteConnectorRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM connectors WHERE id = ?`, id)
	return err
}
