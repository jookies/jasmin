package billing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var ErrInvalidQuotaPersisterConfig = errors.New("invalid quota persister configuration")

// DefaultQuotaPersistInterval is the flush cadence used when a deployment does
// not pick one. It bounds how much spending a hard crash can refund, so it is
// deliberately short: the cost is one small UPSERT batch per interval, and only
// when something actually charged.
const DefaultQuotaPersistInterval = 10 * time.Second

// QuotaPrincipal binds one live billing principal to its durable identity.
//
// The key is the legacy identity (username, or gid for a group), never the
// internal numeric uid: config-provisioned uids are the principal's position in
// the config array, so keying durable money on them would hand one customer's
// spent balance to another the first time the array is reordered.
//
// Group principals carry the live group pointer. A group balance only moves
// when one of its users is charged; the persister collects every dirty user
// first, then snapshots each affected group once from that canonical object.
type QuotaPrincipal struct {
	Scope QuotaScope
	Key   string
	// User is the live user; set only for QuotaScopeUser.
	User *User
	// Group is the live group; set only for QuotaScopeGroup. The persister
	// snapshots groups after it has scanned every dirty user. That ordering is
	// important: if a later user in the same group is charged while the scan is
	// in progress, the group row must include that charge rather than retaining
	// the first user's earlier view and then marking the later user clean.
	Group *Group
	// GroupKey is the durable key of the user's group, empty when ungrouped.
	GroupKey string
	// Provisioned is the balance/submit_sm_count the principal was last
	// provisioned with. It is written beside the live value so the next boot can
	// tell a restart from a top-up (QuotaRecord.Restore).
	Provisioned Quota
}

// QuotaPersister flushes mutated billing quotas to a durable store on a fixed
// cadence. It reuses the mutation-generation machinery the legacy persistence
// timer introduced (User.quotaSnapshot / markQuotaPersisted) so a write can
// never clear a mutation that landed while it was in flight, but unlike that
// timer — which reproduces RouterPB's one-dirty-user-per-tick scan for byte
// parity — it flushes every dirty user per tick. Legacy's contour is a fidelity
// artifact; at N dirty users it would take N ticks to make a charge durable.
type QuotaPersister struct {
	store QuotaStore
	// interval is read on every rearm rather than captured once, so an operator
	// changing the cadence at runtime takes effect from the next tick instead of
	// the next restart. Guarded by intervalMu because Run and SetInterval are on
	// different goroutines.
	interval   time.Duration
	intervalMu sync.RWMutex
	principals func() []QuotaPrincipal
	onError    func(error)
	// flush serialises FlushOnce so a manual (shutdown) flush cannot interleave
	// with the periodic one and mark a generation the other captured.
	flush sync.Mutex
}

// NewQuotaPersister builds the periodic flusher. principals is called on every
// tick so principals provisioned after boot (admin-created users) are picked up
// without restarting the worker. onError may be nil.
func NewQuotaPersister(store QuotaStore, interval time.Duration, principals func() []QuotaPrincipal, onError func(error)) (*QuotaPersister, error) {
	if store == nil || principals == nil || interval <= 0 {
		return nil, ErrInvalidQuotaPersisterConfig
	}
	return &QuotaPersister{store: store, interval: interval, principals: principals, onError: onError}, nil
}

// FlushOnce writes every dirty user, and the group backing each dirty user, as
// one atomic batch. It returns the number of users whose dirty state it
// cleared, which is lower than the number written when a concurrent charge
// landed mid-write — those stay dirty and are retried on the next tick.
func (persister *QuotaPersister) FlushOnce(ctx context.Context) (int, error) {
	persister.flush.Lock()
	defer persister.flush.Unlock()

	principals := persister.principals()
	groups := make(map[string]QuotaPrincipal, len(principals))
	for _, principal := range principals {
		if principal.Scope == QuotaScopeGroup {
			groups[principal.Key] = principal
		}
	}

	type mark struct {
		user       *User
		generation uint64
	}
	var (
		userRecords    []QuotaRecord
		marks          []mark
		dirtyGroupKeys = make(map[string]struct{}, len(groups))
	)
	for _, principal := range principals {
		if principal.Scope != QuotaScopeUser || principal.User == nil {
			continue
		}
		snapshot, dirty := principal.User.quotaSnapshot()
		if !dirty {
			continue
		}
		userRecords = append(userRecords, QuotaRecord{
			Scope: QuotaScopeUser,
			Key:   principal.Key,
			Live: Quota{
				Balance:       snapshot.User.Balance,
				SubmitSmCount: snapshot.User.SubmitSmCountQuota,
			},
			Provisioned: principal.Provisioned,
		})
		marks = append(marks, mark{user: principal.User, generation: snapshot.Generation})

		if principal.GroupKey != "" && snapshot.Group != nil {
			dirtyGroupKeys[principal.GroupKey] = struct{}{}
		}
	}
	if len(userRecords) == 0 {
		return 0, nil
	}

	// Snapshot each affected group only after every dirty user has been
	// observed. A charge holds the user and group locks and bumps that user's
	// generation. Therefore:
	//   * a charge during the user scan is included in this final group view;
	//   * a charge after this view prevents its user mark below from clearing,
	//     so the next flush rewrites both rows.
	// This closes the shared-group refund window caused by keeping the first
	// dirty user's group snapshot while a later dirty user was marked clean.
	groupRecords := make([]QuotaRecord, 0, len(dirtyGroupKeys))
	groupKeys := make([]string, 0, len(dirtyGroupKeys))
	for key := range dirtyGroupKeys {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		group, known := groups[key]
		if !known || group.Group == nil {
			// A missing live group is a broken principal projection. Refuse the
			// whole atomic batch instead of persisting user rows without their
			// shared ceiling.
			return 0, fmt.Errorf("persist billing quotas: live group %q is unavailable", key)
		}
		state := group.Group.GetState()
		groupRecords = append(groupRecords, QuotaRecord{
			Scope: QuotaScopeGroup,
			Key:   group.Key,
			Live: Quota{
				Balance:       state.Balance,
				SubmitSmCount: state.SubmitSmCountQuota,
			},
			Provisioned: group.Provisioned,
		})
	}

	// Groups before users preserves the legacy persistence order
	// (RouterPB.persistenceTimerExpired writes groups, then users).
	records := make([]QuotaRecord, 0, len(groupRecords)+len(userRecords))
	records = append(records, groupRecords...)
	records = append(records, userRecords...)
	if err := persister.store.SaveQuotas(ctx, records); err != nil {
		return 0, fmt.Errorf("persist billing quotas: %w", err)
	}

	cleared := 0
	for _, entry := range marks {
		if entry.user.markQuotaPersisted(entry.generation) {
			cleared++
		}
	}
	return cleared, nil
}

// Run flushes until ctx is cancelled, rearming only after a flush returns so a
// slow store cannot queue overlapping flushes.
//
// A store error is reported through onError and swallowed rather than returned.
// The quotas stay dirty and the next tick retries them; returning would stop
// the only thing standing between a customer's spent balance and a restart that
// refunds it, and a transient database blip must not be able to do that.
// SetInterval changes the flush cadence for subsequent ticks. A zero or
// negative value is refused rather than silently ignored: it would either spin
// or stop the only thing making a spent balance durable.
func (persister *QuotaPersister) SetInterval(interval time.Duration) error {
	if interval <= 0 {
		return ErrInvalidQuotaPersisterConfig
	}
	persister.intervalMu.Lock()
	persister.interval = interval
	persister.intervalMu.Unlock()
	return nil
}

func (persister *QuotaPersister) currentInterval() time.Duration {
	persister.intervalMu.RLock()
	defer persister.intervalMu.RUnlock()
	return persister.interval
}

func (persister *QuotaPersister) Run(ctx context.Context) error {
	timer := time.NewTimer(persister.currentInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if _, err := persister.FlushOnce(ctx); err != nil {
				persister.report(err)
			}
			timer.Reset(persister.currentInterval())
		}
	}
}

func (persister *QuotaPersister) report(err error) {
	if persister.onError == nil || errors.Is(err, context.Canceled) {
		return
	}
	persister.onError(err)
}
