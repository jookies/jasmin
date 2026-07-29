package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrUserNotFound is returned when an admin username is absent.
var ErrUserNotFound = errors.New("admin: user not found")

// StoredUser is one persisted admin user: its username, the stable internal
// uid the store assigned (so route user-filters resolve the same uid across
// restarts), and the opaque JSON spec (an outbound.UserConfig).
type StoredUser struct {
	Username string
	UID      int64
	SpecJSON string
}

// ListUsers returns every persisted admin user, ordered by uid.
func (s *Store) ListUsers(ctx context.Context) ([]StoredUser, error) {
	rows, err := s.queryContext(ctx, `SELECT username, uid, spec_json FROM admin_users ORDER BY uid`)
	if err != nil {
		return nil, fmt.Errorf("admin: list users: %w", err)
	}
	defer rows.Close()
	var result []StoredUser
	for rows.Next() {
		var user StoredUser
		if err := rows.Scan(&user.Username, &user.UID, &user.SpecJSON); err != nil {
			return nil, fmt.Errorf("admin: scan user: %w", err)
		}
		result = append(result, user)
	}
	return result, rows.Err()
}

// GetUser returns one persisted admin user by username.
func (s *Store) GetUser(ctx context.Context, username string) (StoredUser, error) {
	var user StoredUser
	err := s.queryRowContext(ctx, `SELECT username, uid, spec_json FROM admin_users WHERE username=?`, username).
		Scan(&user.Username, &user.UID, &user.SpecJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredUser{}, ErrUserNotFound
	}
	if err != nil {
		return StoredUser{}, fmt.Errorf("admin: get user %q: %w", username, err)
	}
	return user, nil
}

// MaxUID returns the highest uid currently stored (0 when empty), for
// assigning the next admin uid above both config and existing admin users.
func (s *Store) MaxUID(ctx context.Context) (int64, error) {
	var maxUID sql.NullInt64
	if err := s.queryRowContext(ctx, `SELECT MAX(uid) FROM admin_users`).Scan(&maxUID); err != nil {
		return 0, fmt.Errorf("admin: max uid: %w", err)
	}
	if !maxUID.Valid {
		return 0, nil
	}
	return maxUID.Int64, nil
}

// UpsertUser persists (insert or replace) an admin user with its assigned uid.
func (s *Store) UpsertUser(ctx context.Context, user StoredUser, now string) error {
	_, err := s.execContext(ctx,
		`INSERT INTO admin_users (username, uid, spec_json, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(username) DO UPDATE SET spec_json=excluded.spec_json, updated_at=excluded.updated_at`,
		user.Username, user.UID, user.SpecJSON, now)
	if err != nil {
		return fmt.Errorf("admin: upsert user %q: %w", user.Username, err)
	}
	return nil
}

// DeleteUser removes an admin user by username.
func (s *Store) DeleteUser(ctx context.Context, username string) error {
	result, err := s.execContext(ctx, `DELETE FROM admin_users WHERE username=?`, username)
	if err != nil {
		return fmt.Errorf("admin: delete user %q: %w", username, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrUserNotFound
	}
	return nil
}
