package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// Termination connector persistence, mirroring the admin_connectors CRUD above
// it. The two families are stored apart rather than in one polymorphic table:
// an SMPP client connector is defined by a bind and a termination connector by
// a verdict source and an HTTP endpoint, and a shared table would make every
// read guess which half of its columns is meaningful.

// ErrTerminationConnectorNotFound is returned when a termination connector cid
// is absent. It is distinct from ErrConnectorNotFound so a caller that holds
// both kinds can tell which table answered "no".
var ErrTerminationConnectorNotFound = errors.New("admin: termination connector not found")

// StoredTerminationConnector is one persisted termination connector.
//
// The stored config carries the delivery secret in clear, exactly as
// admin_connectors carries a bind password: this row is where the credential
// lives. Nothing outside this file and TerminationService may read it — every
// display path goes through the service, which redacts.
type StoredTerminationConnector struct {
	Config         termination.ConnectorConfig
	DesiredStarted bool
}

// ListTerminationConnectors returns every persisted termination connector,
// ordered by cid.
func (s *Store) ListTerminationConnectors(ctx context.Context) ([]StoredTerminationConnector, error) {
	rows, err := s.queryContext(ctx,
		`SELECT config_json, desired_started FROM admin_termination_connectors ORDER BY cid`)
	if err != nil {
		return nil, fmt.Errorf("admin: list termination connectors: %w", err)
	}
	defer rows.Close()
	var result []StoredTerminationConnector
	for rows.Next() {
		var configJSON string
		var started int
		if err := rows.Scan(&configJSON, &started); err != nil {
			return nil, fmt.Errorf("admin: scan termination connector: %w", err)
		}
		var config termination.ConnectorConfig
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
			return nil, fmt.Errorf("admin: decode stored termination connector %q: %w", config.CID, err)
		}
		result = append(result, StoredTerminationConnector{Config: config, DesiredStarted: started != 0})
	}
	return result, rows.Err()
}

// GetTerminationConnector returns one persisted termination connector.
func (s *Store) GetTerminationConnector(ctx context.Context, cid string) (StoredTerminationConnector, error) {
	var configJSON string
	var started int
	err := s.queryRowContext(ctx,
		`SELECT config_json, desired_started FROM admin_termination_connectors WHERE cid=?`, cid).
		Scan(&configJSON, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredTerminationConnector{}, ErrTerminationConnectorNotFound
	}
	if err != nil {
		return StoredTerminationConnector{}, fmt.Errorf("admin: get termination connector %q: %w", cid, err)
	}
	var config termination.ConnectorConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return StoredTerminationConnector{}, fmt.Errorf("admin: decode termination connector %q: %w", cid, err)
	}
	return StoredTerminationConnector{Config: config, DesiredStarted: started != 0}, nil
}

// UpsertTerminationConnector persists (insert or replace) a termination
// connector.
func (s *Store) UpsertTerminationConnector(ctx context.Context, connector StoredTerminationConnector, now string) error {
	configJSON, err := json.Marshal(connector.Config)
	if err != nil {
		return fmt.Errorf("admin: encode termination connector %q: %w", connector.Config.CID, err)
	}
	started := 0
	if connector.DesiredStarted {
		started = 1
	}
	_, err = s.execContext(ctx,
		`INSERT INTO admin_termination_connectors (cid, config_json, desired_started, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(cid) DO UPDATE SET config_json=excluded.config_json, desired_started=excluded.desired_started, updated_at=excluded.updated_at`,
		connector.Config.CID, string(configJSON), started, now)
	if err != nil {
		return fmt.Errorf("admin: upsert termination connector %q: %w", connector.Config.CID, err)
	}
	return nil
}

// SetTerminationDesiredStarted updates only the desired-started flag.
func (s *Store) SetTerminationDesiredStarted(ctx context.Context, cid string, started bool, now string) error {
	flag := 0
	if started {
		flag = 1
	}
	result, err := s.execContext(ctx,
		`UPDATE admin_termination_connectors SET desired_started=?, updated_at=? WHERE cid=?`, flag, now, cid)
	if err != nil {
		return fmt.Errorf("admin: set termination desired_started %q: %w", cid, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrTerminationConnectorNotFound
	}
	return nil
}

// DeleteTerminationConnector removes a termination connector.
func (s *Store) DeleteTerminationConnector(ctx context.Context, cid string) error {
	result, err := s.execContext(ctx, `DELETE FROM admin_termination_connectors WHERE cid=?`, cid)
	if err != nil {
		return fmt.Errorf("admin: delete termination connector %q: %w", cid, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrTerminationConnectorNotFound
	}
	return nil
}
