package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// ErrSMPPsUserNotFound is returned when an admin SMPPs system_id is absent.
var ErrSMPPsUserNotFound = errors.New("admin: SMPPs user not found")

// StoredSMPPsUser is one persisted SMPPs bind account: its system_id (identity)
// and the opaque JSON spec (an smppsserver.UserConfig, validated by the
// provisioner on apply — admin does not interpret it).
type StoredSMPPsUser struct {
	SystemID string
	SpecJSON string
}

// SMPPsUserProvisioner rebuilds and swaps the live SMPPs directory from the full
// set of admin user specs. The gateway implements it over Directory.ApplyUsers.
type SMPPsUserProvisioner interface {
	ApplySMPPsUsers(ctx context.Context, specsJSON []string) error
}

// SMPPsUserService applies admin SMPPs bind-user CRUD: apply-first to the live
// directory, then persist.
//
// Auth is resolved at bind time, so removing a user prevents new binds but does
// not tear down a session that is already bound.
type SMPPsUserService struct {
	store       *Store
	provisioner SMPPsUserProvisioner
	now         func() string
	mu          sync.Mutex
}

// NewSMPPsUserService builds the SMPPs user admin service.
func NewSMPPsUserService(store *Store, provisioner SMPPsUserProvisioner, now func() string) (*SMPPsUserService, error) {
	if store == nil || provisioner == nil {
		return nil, errors.New("admin: SMPPs user store and provisioner are required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	return &SMPPsUserService{store: store, provisioner: provisioner, now: now}, nil
}

// LoadAndApply re-applies every persisted SMPPs user at boot.
func (s *SMPPsUserService) LoadAndApply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	specs, err := s.currentSpecs(ctx)
	if err != nil {
		return err
	}
	return s.provisioner.ApplySMPPsUsers(ctx, specs)
}

func (s *SMPPsUserService) currentSpecs(ctx context.Context) ([]string, error) {
	stored, err := s.store.ListSMPPsUsers(ctx)
	if err != nil {
		return nil, err
	}
	specs := make([]string, 0, len(stored))
	for _, user := range stored {
		specs = append(specs, user.SpecJSON)
	}
	return specs, nil
}

// specsWith returns the stored specs with systemID's spec replaced/appended, or
// removed — the desired set for an apply-first dry run.
func (s *SMPPsUserService) specsWith(ctx context.Context, systemID, specJSON string, remove bool) ([]string, error) {
	stored, err := s.store.ListSMPPsUsers(ctx)
	if err != nil {
		return nil, err
	}
	specs := make([]string, 0, len(stored)+1)
	replaced := false
	for _, user := range stored {
		if user.SystemID == systemID {
			replaced = true
			if remove {
				continue
			}
			specs = append(specs, specJSON)
			continue
		}
		specs = append(specs, user.SpecJSON)
	}
	if !replaced && !remove {
		specs = append(specs, specJSON)
	}
	return specs, nil
}

// PutUser creates or replaces the admin SMPPs user for systemID.
func (s *SMPPsUserService) PutUser(ctx context.Context, systemID, specJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	desired, err := s.specsWith(ctx, systemID, specJSON, false)
	if err != nil {
		return err
	}
	if err := s.provisioner.ApplySMPPsUsers(ctx, desired); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.UpsertSMPPsUser(ctx, StoredSMPPsUser{SystemID: systemID, SpecJSON: specJSON}, s.now())
}

// DeleteUser removes an admin SMPPs user and re-applies the remaining set.
func (s *SMPPsUserService) DeleteUser(ctx context.Context, systemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.store.GetSMPPsUser(ctx, systemID); err != nil {
		return err
	}
	desired, err := s.specsWith(ctx, systemID, "", true)
	if err != nil {
		return err
	}
	if err := s.provisioner.ApplySMPPsUsers(ctx, desired); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.DeleteSMPPsUser(ctx, systemID)
}

// ListUsers returns the persisted admin SMPPs users.
func (s *SMPPsUserService) ListUsers(ctx context.Context) ([]StoredSMPPsUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.ListSMPPsUsers(ctx)
}

// GetUser returns one persisted admin SMPPs user.
func (s *SMPPsUserService) GetUser(ctx context.Context, systemID string) (StoredSMPPsUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.GetSMPPsUser(ctx, systemID)
}

// ListSMPPsUsers returns every persisted admin SMPPs user, ordered by system_id.
func (s *Store) ListSMPPsUsers(ctx context.Context) ([]StoredSMPPsUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT system_id, spec_json FROM admin_smpps_users ORDER BY system_id`)
	if err != nil {
		return nil, fmt.Errorf("admin: list SMPPs users: %w", err)
	}
	defer rows.Close()
	var result []StoredSMPPsUser
	for rows.Next() {
		var user StoredSMPPsUser
		if err := rows.Scan(&user.SystemID, &user.SpecJSON); err != nil {
			return nil, fmt.Errorf("admin: scan SMPPs user: %w", err)
		}
		result = append(result, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: iterate SMPPs users: %w", err)
	}
	return result, nil
}

// GetSMPPsUser returns one persisted SMPPs user.
func (s *Store) GetSMPPsUser(ctx context.Context, systemID string) (StoredSMPPsUser, error) {
	var user StoredSMPPsUser
	err := s.db.QueryRowContext(ctx,
		`SELECT system_id, spec_json FROM admin_smpps_users WHERE system_id = ?`, systemID).
		Scan(&user.SystemID, &user.SpecJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredSMPPsUser{}, fmt.Errorf("%w: %s", ErrSMPPsUserNotFound, systemID)
	}
	if err != nil {
		return StoredSMPPsUser{}, fmt.Errorf("admin: get SMPPs user %q: %w", systemID, err)
	}
	return user, nil
}

// UpsertSMPPsUser inserts or replaces a persisted SMPPs user.
func (s *Store) UpsertSMPPsUser(ctx context.Context, user StoredSMPPsUser, now string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_smpps_users (system_id, spec_json, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(system_id) DO UPDATE SET spec_json = excluded.spec_json, updated_at = excluded.updated_at`,
		user.SystemID, user.SpecJSON, now)
	if err != nil {
		return fmt.Errorf("admin: upsert SMPPs user %q: %w", user.SystemID, err)
	}
	return nil
}

// DeleteSMPPsUser forgets a persisted SMPPs user.
func (s *Store) DeleteSMPPsUser(ctx context.Context, systemID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM admin_smpps_users WHERE system_id = ?`, systemID); err != nil {
		return fmt.Errorf("admin: delete SMPPs user %q: %w", systemID, err)
	}
	return nil
}
