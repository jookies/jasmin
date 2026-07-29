package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// GroupProvisioner applies admin group CRUD to the live billing directory with
// a caller-supplied stable numeric gid. The outbound runtime implements it.
type GroupProvisioner interface {
	AddGroup(gid, specJSON string, number int64) error
	RemoveGroup(gid string) error
	// RemoveUser drops a live user during a group cascade delete. Legacy's
	// perspective_group_remove (routing/router.py) removes the group's users
	// along with it, so deleting a group must not leave members behind
	// referencing a gid that no longer resolves.
	RemoveUser(username string) error
	// ConfigGroupFloor is the number of config-owned groups; admin numbers
	// start above it so they never collide with a config group's index.
	ConfigGroupFloor() int64
}

// GroupService applies admin group CRUD: apply-first to the live directory,
// then persist, rolling back the live change if persistence fails — the same
// shape as UserService, because a half-applied group is a spending ceiling that
// exists in one place and not the other.
type GroupService struct {
	store       *Store
	provisioner GroupProvisioner
	now         func() string
	mu          sync.Mutex
	applied     map[string]struct{}
}

// NewGroupService builds the group admin service.
func NewGroupService(store *Store, provisioner GroupProvisioner, now func() string) (*GroupService, error) {
	if store == nil || provisioner == nil {
		return nil, errors.New("admin: group store and provisioner are required")
	}
	if now == nil {
		return nil, errors.New("admin: now func is required")
	}
	return &GroupService{
		store:       store,
		provisioner: provisioner,
		now:         now,
		applied:     make(map[string]struct{}),
	}, nil
}

// LoadAndApply re-installs every persisted admin group at boot. It runs before
// user LoadAndApply: a user resolves its group by gid, so the groups must be
// live first.
func (s *GroupService) LoadAndApply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.store.ListGroups(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for gid := range s.applied {
		if err := s.provisioner.RemoveGroup(gid); err != nil {
			errs = append(errs, fmt.Errorf("admin: remove previously applied group %q: %w", gid, err))
		}
		// Same reasoning as UserService.LoadAndApply: retaining the entry after a
		// failed removal permanently blocks the re-add, and a group that never
		// re-installs takes every one of its users down with it at boot.
		delete(s.applied, gid)
	}
	for _, group := range stored {
		if _, stillApplied := s.applied[group.GID]; stillApplied {
			errs = append(errs, fmt.Errorf("admin: group %q could not be refreshed", group.GID))
			continue
		}
		if err := s.provisioner.AddGroup(group.GID, group.SpecJSON, group.Number); err != nil {
			errs = append(errs, fmt.Errorf("admin: re-add group %q: %w", group.GID, err))
			continue
		}
		s.applied[group.GID] = struct{}{}
	}
	return errors.Join(errs...)
}

// CreateGroup assigns a stable numeric gid, applies to the directory, then
// persists. Re-creating an existing admin group replaces it in place, keeping
// its number so route group-filters keep resolving.
func (s *GroupService) CreateGroup(ctx context.Context, gid, specJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	number, replacing, err := s.resolveNumber(ctx, gid)
	if err != nil {
		return err
	}
	if replacing {
		if err := s.provisioner.RemoveGroup(gid); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	if err := s.provisioner.AddGroup(gid, specJSON, number); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.UpsertGroup(ctx, StoredGroup{GID: gid, Number: number, SpecJSON: specJSON}, s.now()); err != nil {
		_ = s.provisioner.RemoveGroup(gid) // roll back the live change
		delete(s.applied, gid)
		return err
	}
	s.applied[gid] = struct{}{}
	return nil
}

func (s *GroupService) resolveNumber(ctx context.Context, gid string) (int64, bool, error) {
	if existing, err := s.store.GetGroup(ctx, gid); err == nil {
		return existing.Number, true, nil
	} else if !errors.Is(err, ErrGroupNotFound) {
		return 0, false, err
	}
	maxNumber, err := s.store.MaxGroupNumber(ctx)
	if err != nil {
		return 0, false, err
	}
	floor := s.provisioner.ConfigGroupFloor()
	next := maxNumber + 1
	if floor+1 > next {
		next = floor + 1
	}
	return next, false, nil
}

// DeleteGroup removes an admin group from the directory and the store, and
// cascades to the group's users.
//
// The cascade is the oracle's behaviour: perspective_group_remove
// (jasmin/routing/router.py) removes every user of the group before removing
// the group itself. Skipping it would leave stored users whose group_id no
// longer resolves — they would fail to install on the next boot, and a
// *disabled* group's members would authenticate again the moment the group
// went away.
func (s *GroupService) DeleteGroup(ctx context.Context, gid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.store.GetGroup(ctx, gid); err != nil {
		return err
	}
	members, err := s.membersOf(ctx, gid)
	if err != nil {
		return err
	}
	// Users first: a member removed after its group is gone would be resolving
	// against a directory that no longer knows the gid.
	for _, username := range members {
		if err := s.provisioner.RemoveUser(username); err != nil {
			return fmt.Errorf("%w: cascade remove user %q: %v", ErrInvalidRequest, username, err)
		}
		if err := s.store.DeleteUser(ctx, username); err != nil {
			return err
		}
	}
	if err := s.provisioner.RemoveGroup(gid); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := s.store.DeleteGroup(ctx, gid); err != nil {
		return err
	}
	delete(s.applied, gid)
	return nil
}

// membersOf returns the usernames of every stored admin user whose spec names
// this gid. A spec that will not decode is skipped rather than failing the
// delete: it could not have been installed either.
func (s *GroupService) membersOf(ctx context.Context, gid string) ([]string, error) {
	stored, err := s.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	var members []string
	for _, user := range stored {
		var spec struct {
			GroupID string `json:"group_id"`
		}
		if err := json.Unmarshal([]byte(user.SpecJSON), &spec); err != nil {
			continue
		}
		if spec.GroupID == gid {
			members = append(members, user.Username)
		}
	}
	return members, nil
}

// ListGroups returns the persisted admin groups.
func (s *GroupService) ListGroups(ctx context.Context) ([]StoredGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.ListGroups(ctx)
}

// GetGroup returns one persisted admin group.
func (s *GroupService) GetGroup(ctx context.Context, gid string) (StoredGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.GetGroup(ctx, gid)
}
