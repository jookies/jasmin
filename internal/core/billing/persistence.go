package billing

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrInvalidQuotaPersistenceConfig = errors.New("invalid quota persistence configuration")

// QuotaSnapshot is an immutable state projection bound to its mutation generation.
type QuotaSnapshot struct {
	User       UserState
	Group      *GroupState
	Generation uint64
}

// QuotaPersistenceStore preserves RouterPB's groups-then-users persistence
// contour. The bool is the legacy perspective_persist result; legacy ignores a
// false result, while an error represents a target-side durable write failure.
type QuotaPersistenceStore interface {
	PersistGroup(context.Context, GroupState) (bool, error)
	PersistUser(context.Context, UserState) (bool, error)
}

type QuotaPersistenceService struct {
	users    []*User
	store    QuotaPersistenceStore
	interval time.Duration
	persist  sync.Mutex
}

func NewQuotaPersistenceService(users []*User, store QuotaPersistenceStore, interval time.Duration) (*QuotaPersistenceService, error) {
	if store == nil || interval <= 0 {
		return nil, ErrInvalidQuotaPersistenceConfig
	}
	for _, user := range users {
		if user == nil {
			return nil, ErrInvalidQuotaPersistenceConfig
		}
	}
	return &QuotaPersistenceService{users: append([]*User(nil), users...), store: store, interval: interval}, nil
}

func (u *User) QuotasDirty() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.quotaVersion != u.persistedQuotaVersion
}

func (u *User) quotaSnapshot() (QuotaSnapshot, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.quotaVersion == u.persistedQuotaVersion {
		return QuotaSnapshot{}, false
	}
	state := UserState{
		UID: u.uid, Balance: cloneFloat64(u.balance),
		EarlyDecrementBalancePercent: cloneInt(u.earlyDecrementBalancePercent),
		SubmitSmCountQuota:           cloneInt(u.submitSmCountQuota),
	}
	var groupState *GroupState
	if u.group != nil {
		u.group.mu.Lock()
		gid := u.group.gid
		state.GID = &gid
		projected := GroupState{GID: gid, Balance: cloneFloat64(u.group.balance), SubmitSmCountQuota: cloneInt(u.group.submitSmCountQuota)}
		u.group.mu.Unlock()
		groupState = &projected
	}
	return QuotaSnapshot{User: state, Group: groupState, Generation: u.quotaVersion}, true
}

func (u *User) markQuotaPersisted(generation uint64) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.quotaVersion != generation {
		return false
	}
	u.persistedQuotaVersion = generation
	return true
}

// PersistOnce serializes callers, scans in legacy user order, persists groups
// before users, and stops after the first dirty user. A false legacy result is
// intentionally ignored; a durable error or concurrent mutation remains dirty.
func (service *QuotaPersistenceService) PersistOnce(ctx context.Context) (bool, error) {
	service.persist.Lock()
	defer service.persist.Unlock()
	for _, user := range service.users {
		snapshot, dirty := user.quotaSnapshot()
		if !dirty {
			continue
		}
		if snapshot.Group != nil {
			if _, err := service.store.PersistGroup(ctx, *snapshot.Group); err != nil {
				return false, err
			}
		}
		if _, err := service.store.PersistUser(ctx, snapshot.User); err != nil {
			return false, err
		}
		user.markQuotaPersisted(snapshot.Generation)
		return true, nil
	}
	return false, nil
}

func (service *QuotaPersistenceService) Run(ctx context.Context) error {
	ticker := time.NewTicker(service.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := service.PersistOnce(ctx); err != nil {
				return err
			}
		}
	}
}
