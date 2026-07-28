package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrGroupNotFound is returned when an admin gid is absent.
var ErrGroupNotFound = errors.New("admin: group not found")

// StoredGroup is one persisted admin group: its legacy gid, the stable internal
// numeric gid the store assigned (routing filters compare that number, so it
// must survive restarts), and the opaque JSON spec (an outbound.GroupConfig).
type StoredGroup struct {
	GID      string
	Number   int64
	SpecJSON string
}

// ListGroups returns every persisted admin group, ordered by its numeric gid.
func (s *Store) ListGroups(ctx context.Context) ([]StoredGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT gid, gid_number, spec_json FROM admin_groups ORDER BY gid_number`)
	if err != nil {
		return nil, fmt.Errorf("admin: list groups: %w", err)
	}
	defer rows.Close()
	var result []StoredGroup
	for rows.Next() {
		var group StoredGroup
		if err := rows.Scan(&group.GID, &group.Number, &group.SpecJSON); err != nil {
			return nil, fmt.Errorf("admin: scan group: %w", err)
		}
		result = append(result, group)
	}
	return result, rows.Err()
}

// GetGroup returns one persisted admin group by gid.
func (s *Store) GetGroup(ctx context.Context, gid string) (StoredGroup, error) {
	var group StoredGroup
	err := s.db.QueryRowContext(ctx, `SELECT gid, gid_number, spec_json FROM admin_groups WHERE gid=?`, gid).
		Scan(&group.GID, &group.Number, &group.SpecJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredGroup{}, ErrGroupNotFound
	}
	if err != nil {
		return StoredGroup{}, fmt.Errorf("admin: get group %q: %w", gid, err)
	}
	return group, nil
}

// MaxGroupNumber returns the highest numeric gid stored (0 when empty).
func (s *Store) MaxGroupNumber(ctx context.Context) (int64, error) {
	var maxNumber sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(gid_number) FROM admin_groups`).Scan(&maxNumber); err != nil {
		return 0, fmt.Errorf("admin: max group number: %w", err)
	}
	if !maxNumber.Valid {
		return 0, nil
	}
	return maxNumber.Int64, nil
}

// UpsertGroup persists (insert or replace) an admin group with its number.
func (s *Store) UpsertGroup(ctx context.Context, group StoredGroup, now string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_groups (gid, gid_number, spec_json, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(gid) DO UPDATE SET spec_json=excluded.spec_json, updated_at=excluded.updated_at`,
		group.GID, group.Number, group.SpecJSON, now)
	if err != nil {
		return fmt.Errorf("admin: upsert group %q: %w", group.GID, err)
	}
	return nil
}

// DeleteGroup removes an admin group by gid.
func (s *Store) DeleteGroup(ctx context.Context, gid string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM admin_groups WHERE gid=?`, gid)
	if err != nil {
		return fmt.Errorf("admin: delete group %q: %w", gid, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrGroupNotFound
	}
	return nil
}
