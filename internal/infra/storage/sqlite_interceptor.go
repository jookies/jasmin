package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
)

type SQLiteInterceptorRepository struct {
	db        *sql.DB
	direction routingfilter.Direction
}

func NewSQLiteInterceptorRepository(db *sql.DB, direction routingfilter.Direction) *SQLiteInterceptorRepository {
	return &SQLiteInterceptorRepository{db: db, direction: direction}
}

func (r *SQLiteInterceptorRepository) Init(ctx context.Context) error {
	tableName := "mo_interceptors"
	if r.direction == routingfilter.MT {
		tableName = "mt_interceptors"
	}

	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			routing_order INTEGER PRIMARY KEY,
			script_id TEXT,
			py_code TEXT,
			filters_json TEXT
		);
	`, tableName)

	_, err := r.db.ExecContext(ctx, query)
	return err
}

func (r *SQLiteInterceptorRepository) Save(ctx context.Context, order int, intcp interceptor.Interceptor) error {
	tableName := "mo_interceptors"
	if r.direction == routingfilter.MT {
		tableName = "mt_interceptors"
	}

	state := intcp.GetState()
	filtersJSON, err := json.Marshal(state.Filters)
	if err != nil {
		return err
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (routing_order, script_id, py_code, filters_json)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(routing_order) DO UPDATE SET
			script_id = excluded.script_id,
			py_code = excluded.py_code,
			filters_json = excluded.filters_json
	`, tableName)

	_, err = r.db.ExecContext(ctx, query, order, state.ScriptID, state.PyCode, string(filtersJSON))
	return err
}

func (r *SQLiteInterceptorRepository) LoadAll(ctx context.Context) (map[int]interceptor.Interceptor, error) {
	tableName := "mo_interceptors"
	if r.direction == routingfilter.MT {
		tableName = "mt_interceptors"
	}

	query := fmt.Sprintf(`SELECT routing_order, script_id, py_code, filters_json FROM %s`, tableName)
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	interceptors := make(map[int]interceptor.Interceptor)
	for rows.Next() {
		var order int
		var state interceptor.InterceptorState
		var filtersJSON string
		if err := rows.Scan(&order, &state.ScriptID, &state.PyCode, &filtersJSON); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(filtersJSON), &state.Filters); err != nil {
			return nil, err
		}

		intcp, err := interceptor.FromInterceptorState(state)
		if err != nil {
			return nil, err
		}
		interceptors[order] = intcp
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return interceptors, nil
}

func (r *SQLiteInterceptorRepository) Delete(ctx context.Context, order int) error {
	tableName := "mo_interceptors"
	if r.direction == routingfilter.MT {
		tableName = "mt_interceptors"
	}

	query := fmt.Sprintf(`DELETE FROM %s WHERE routing_order = ?`, tableName)
	_, err := r.db.ExecContext(ctx, query, order)
	return err
}
