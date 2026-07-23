package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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
			filters_json TEXT,
			connector_pool_json TEXT
		);
		CREATE TABLE IF NOT EXISTS connectors (
			id TEXT PRIMARY KEY,
			type TEXT
		);
	`, tableName)

	if _, err := r.db.ExecContext(ctx, query); err != nil {
		return err
	}
	return ensureSQLiteColumn(ctx, r.db, tableName, "connector_pool_json", "TEXT")
}

func ensureSQLiteColumn(ctx context.Context, db *sql.DB, tableName, columnName, columnType string) error {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		found, err := sqliteColumnExists(ctx, db, tableName, columnName)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		_, lastErr = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, columnName, columnType))
		if lastErr == nil {
			return nil
		}
		found, inspectErr := sqliteColumnExists(ctx, db, tableName, columnName)
		if inspectErr == nil && found {
			return nil
		}
		if inspectErr != nil {
			lastErr = fmt.Errorf("alter column: %v; inspect schema: %w", lastErr, inspectErr)
		}
		time.Sleep(time.Duration(1<<attempt) * 10 * time.Millisecond)
	}
	return lastErr
}

func sqliteColumnExists(ctx context.Context, db *sql.DB, tableName, columnName string) (bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
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
	connectorPoolJSON, err := json.Marshal(state.Connectors)
	if err != nil {
		return err
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (routing_order, direction, connector_id, connector_type, rate, is_default, filters_json, connector_pool_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(routing_order) DO UPDATE SET
			direction = excluded.direction,
			connector_id = excluded.connector_id,
			connector_type = excluded.connector_type,
			rate = excluded.rate,
			is_default = excluded.is_default,
			filters_json = excluded.filters_json,
			connector_pool_json = excluded.connector_pool_json
	`, tableName)

	_, err = r.db.ExecContext(ctx, query, order, state.Direction, state.ConnectorID, state.ConnectorType, state.Rate, state.DefaultRoute, string(filtersJSON), string(connectorPoolJSON))
	return err
}

func (r *SQLiteRouteRepository) LoadAll(ctx context.Context) (map[int]routingtable.Route, error) {
	tableName := "mo_routes"
	if r.direction == routingfilter.MT {
		tableName = "mt_routes"
	}

	query := fmt.Sprintf(`SELECT routing_order, direction, connector_id, connector_type, rate, is_default, filters_json, connector_pool_json FROM %s`, tableName)
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
		var connectorPoolJSON sql.NullString
		if err := rows.Scan(&order, &state.Direction, &state.ConnectorID, &state.ConnectorType, &state.Rate, &state.DefaultRoute, &filtersJSON, &connectorPoolJSON); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(filtersJSON), &state.Filters); err != nil {
			return nil, err
		}
		if connectorPoolJSON.Valid && connectorPoolJSON.String != "" {
			if err := json.Unmarshal([]byte(connectorPoolJSON.String), &state.Connectors); err != nil {
				return nil, err
			}
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
