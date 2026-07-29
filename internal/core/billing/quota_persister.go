package billing

import (
	"context"
	"errors"
	"fmt"
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
// Group principals carry no live pointer. A group balance only moves when one
// of its users is charged, and that charge happens under both locks, so the
// dirty user's snapshot is the coherent source for the group's live values;
// this entry exists to supply the group's key and provisioned baseline.
type QuotaPrincipal struct {
	Scope QuotaScope
	Key   string
	// User is the live user; set only for QuotaScopeUser.
	User *User
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
	store      QuotaStore
	interval   time.Duration
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
		groupRecords []QuotaRecord
		userRecords  []QuotaRecord
		marks        []mark
	)
	written := make(map[string]struct{}, len(groups))
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

		if principal.GroupKey == "" || snapshot.Group == nil {
			continue
		}
		if _, done := written[principal.GroupKey]; done {
			continue
		}
		group, known := groups[principal.GroupKey]
		if !known {
			continue
		}
		groupRecords = append(groupRecords, QuotaRecord{
			Scope: QuotaScopeGroup,
			Key:   group.Key,
			Live: Quota{
				Balance:       snapshot.Group.Balance,
				SubmitSmCount: snapshot.Group.SubmitSmCountQuota,
			},
			Provisioned: group.Provisioned,
		})
		written[principal.GroupKey] = struct{}{}
	}
	if len(userRecords) == 0 {
		return 0, nil
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
func (persister *QuotaPersister) Run(ctx context.Context) error {
	timer := time.NewTimer(persister.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if _, err := persister.FlushOnce(ctx); err != nil {
				persister.report(err)
			}
			timer.Reset(persister.interval)
		}
	}
}

func (persister *QuotaPersister) report(err error) {
	if persister.onError == nil || errors.Is(err, context.Canceled) {
		return
	}
	persister.onError(err)
}
