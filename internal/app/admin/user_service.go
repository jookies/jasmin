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

// LiveReplacement is an applied in-memory edit whose locks remain held until
// its durable admin-store write succeeds or fails.
type LiveReplacement interface {
	Commit()
	Rollback()
}

// UserReplacer is the optional quota-safe transactional replacement boundary
// implemented by the production outbound runtime. Keeping it separate
// preserves compatibility with simple provisioners while allowing a live
// update to distinguish unchanged provisioning fields from an intentional
// balance/count top-up and to restore the exact spent state on store failure.
type UserReplacer interface {
	BeginReplaceUser(username, specJSON string, uid int64) (LiveReplacement, error)
}

// DeletedQuotaPruner is the optional production hook that removes durable
// billing rows after the corresponding admin-store deletion commits. Boot also
// prunes as a recovery pass, but doing it here closes same-process name reuse.
type DeletedQuotaPruner interface {
	PruneDeletedQuotas(context.Context) (int64, error)
}

// UserService applies admin user CRUD: apply-first to the live directory, then
// persist. It assigns each new user a stable uid (max stored uid, or the
// config floor, + 1) so route user-filters resolve the same uid after restart.
type UserService struct {
	store       *Store
	provisioner UserProvisioner
	now         func() string
	mu          sync.Mutex
	applied     map[string]struct{}
}

// NewUserService builds the user admin service.
func NewUserService(store *Store, provisioner UserProvisioner, now func() string) (*UserService, error) {
	if store == nil || provisioner == nil {
		return nil, errors.New("admin: user store and provisioner are required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	return &UserService{
		store:       store,
		provisioner: provisioner,
		now:         now,
		applied:     make(map[string]struct{}),
	}, nil
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
	for username := range s.applied {
		if err := s.provisioner.RemoveUser(username); err != nil {
			errs = append(errs, fmt.Errorf("admin: remove previously applied user %q: %w", username, err))
		}
		// Drop the bookkeeping even when the removal failed. The common cause is
		// that the user is already absent — CreateUser removes before it adds, so
		// a rejected replacement spec leaves the store holding a user the
		// directory no longer has. Keeping the entry would make the re-add below
		// skip it as "could not be refreshed" and strand that user on 401 until
		// the process restarts, which is exactly what this reconcile exists to
		// repair.
		delete(s.applied, username)
	}
	for _, user := range stored {
		if _, stillApplied := s.applied[user.Username]; stillApplied {
			errs = append(errs, fmt.Errorf("admin: user %q could not be refreshed", user.Username))
			continue
		}
		if err := s.provisioner.AddUser(user.Username, user.SpecJSON, user.UID); err != nil {
			errs = append(errs, fmt.Errorf("admin: re-add user %q: %w", user.Username, err))
			continue
		}
		s.applied[user.Username] = struct{}{}
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
	var replacement LiveReplacement
	if replacing {
		if replacer, ok := s.provisioner.(UserReplacer); ok {
			// The production replacer carries forward live spent-down quotas
			// when their provisioned baselines did not change. A remove/add
			// pair cannot make that distinction and re-grants the stored grant.
			replacement, err = replacer.BeginReplaceUser(username, specJSON, uid)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
			}
		} else {
			// Compatibility path for provisioners without live quota state.
			if err := s.provisioner.RemoveUser(username); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
			}
			if err := s.provisioner.AddUser(username, specJSON, uid); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
			}
		}
	} else if err := s.provisioner.AddUser(username, specJSON, uid); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.UpsertUser(ctx, StoredUser{Username: username, UID: uid, SpecJSON: specJSON}, s.now()); err != nil {
		if replacement != nil {
			replacement.Rollback()
		} else {
			_ = s.provisioner.RemoveUser(username) // roll back a newly added/fallback live user
			delete(s.applied, username)
		}
		return err
	}
	if replacement != nil {
		replacement.Commit()
	}
	s.applied[username] = struct{}{}
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
	if err := s.store.DeleteUser(ctx, username); err != nil {
		return err
	}
	delete(s.applied, username)
	if pruner, ok := s.provisioner.(DeletedQuotaPruner); ok {
		if _, err := pruner.PruneDeletedQuotas(ctx); err != nil {
			return fmt.Errorf("admin: prune deleted user quota: %w", err)
		}
	}
	return nil
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
