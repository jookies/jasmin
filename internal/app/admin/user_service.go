package admin

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// UserProvisioner applies admin user CRUD to the live billing directory with a
// caller-supplied stable uid. The outbound runtime implements it.
type UserProvisioner interface {
	AddUser(username, specJSON string, uid int64) error
	RemoveUser(username string) error
	// ConfigUserFloor is the number of config-owned users; admin uids start
	// above it so they never collide with a config user's index-based uid.
	ConfigUserFloor() int64
}

// UserService applies admin user CRUD: apply-first to the live directory, then
// persist. It assigns each new user a stable uid (max stored uid, or the
// config floor, + 1) so route user-filters resolve the same uid after restart.
type UserService struct {
	store       *Store
	provisioner UserProvisioner
	now         func() string
	mu          sync.Mutex
}

// NewUserService builds the user admin service.
func NewUserService(store *Store, provisioner UserProvisioner, now func() string) (*UserService, error) {
	if store == nil || provisioner == nil {
		return nil, errors.New("admin: user store and provisioner are required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	return &UserService{store: store, provisioner: provisioner, now: now}, nil
}

// LoadAndApply re-installs every persisted admin user (with its stored uid)
// into the directory at boot.
func (s *UserService) LoadAndApply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.ListUsers(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, user := range stored {
		if err := s.provisioner.AddUser(user.Username, user.SpecJSON, user.UID); err != nil {
			errs = append(errs, fmt.Errorf("admin: re-add user %q: %w", user.Username, err))
		}
	}
	return errors.Join(errs...)
}

// CreateUser assigns a stable uid, applies to the directory, then persists.
// Update of an existing user replaces it in place, keeping its uid.
func (s *UserService) CreateUser(ctx context.Context, username, specJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	uid, replacing, err := s.resolveUID(ctx, username)
	if err != nil {
		return err
	}
	// Re-provision: remove the old live user first so applyUser's duplicate
	// guard does not reject the replacement.
	if replacing {
		if err := s.provisioner.RemoveUser(username); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	if err := s.provisioner.AddUser(username, specJSON, uid); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.UpsertUser(ctx, StoredUser{Username: username, UID: uid, SpecJSON: specJSON}, s.now()); err != nil {
		_ = s.provisioner.RemoveUser(username) // roll back the live change on persist failure
		return err
	}
	return nil
}

// resolveUID returns the uid to use for username: its existing uid if it is
// already an admin user (replace), else a fresh uid above the config floor and
// every stored admin uid.
func (s *UserService) resolveUID(ctx context.Context, username string) (int64, bool, error) {
	if existing, err := s.store.GetUser(ctx, username); err == nil {
		return existing.UID, true, nil
	} else if !errors.Is(err, ErrUserNotFound) {
		return 0, false, err
	}
	maxUID, err := s.store.MaxUID(ctx)
	if err != nil {
		return 0, false, err
	}
	floor := s.provisioner.ConfigUserFloor()
	next := maxUID + 1
	if floor+1 > next {
		next = floor + 1
	}
	return next, false, nil
}

// DeleteUser removes an admin user from the directory and the store.
func (s *UserService) DeleteUser(ctx context.Context, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.store.GetUser(ctx, username); err != nil {
		return err
	}
	if err := s.provisioner.RemoveUser(username); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return s.store.DeleteUser(ctx, username)
}

// ListUsers returns the persisted admin users (username + uid + raw spec).
func (s *UserService) ListUsers(ctx context.Context) ([]StoredUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.ListUsers(ctx)
}

// GetUser returns one persisted admin user.
func (s *UserService) GetUser(ctx context.Context, username string) (StoredUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.GetUser(ctx, username)
}
