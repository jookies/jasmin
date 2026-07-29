package billing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

// memoryQuotaStore is an in-memory QuotaStore that records every batch it was
// handed, so tests can assert both the contents and the groups-before-users
// order the legacy persistence contour uses.
type memoryQuotaStore struct {
	mu      sync.Mutex
	rows    map[string]QuotaRecord
	batches [][]QuotaRecord
	saveErr error
	loadErr error
	saved   chan struct{}
}

func newMemoryQuotaStore() *memoryQuotaStore {
	return &memoryQuotaStore{rows: make(map[string]QuotaRecord), saved: make(chan struct{}, 16)}
}

func (store *memoryQuotaStore) SaveQuotas(_ context.Context, records []QuotaRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saveErr != nil {
		return store.saveErr
	}
	if store.rows == nil {
		store.rows = make(map[string]QuotaRecord)
	}
	batch := make([]QuotaRecord, 0, len(records))
	for _, record := range records {
		store.rows[string(record.Scope)+"/"+record.Key] = record
		batch = append(batch, record)
	}
	store.batches = append(store.batches, batch)
	if store.saved != nil {
		select {
		case store.saved <- struct{}{}:
		default:
		}
	}
	return nil
}

func (store *memoryQuotaStore) LoadQuotas(context.Context) ([]QuotaRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loadErr != nil {
		return nil, store.loadErr
	}
	records := make([]QuotaRecord, 0, len(store.rows))
	for _, record := range store.rows {
		records = append(records, record)
	}
	return records, nil
}

func (store *memoryQuotaStore) record(scope QuotaScope, key string) (QuotaRecord, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.rows[string(scope)+"/"+key]
	return record, ok
}

func (store *memoryQuotaStore) batchOrder() [][]string {
	store.mu.Lock()
	defer store.mu.Unlock()
	order := make([][]string, 0, len(store.batches))
	for _, batch := range store.batches {
		keys := make([]string, 0, len(batch))
		for _, record := range batch {
			keys = append(keys, string(record.Scope)+"/"+record.Key)
		}
		order = append(order, keys)
	}
	return order
}

func TestNewQuotaPersisterRejectsInvalidConfiguration(t *testing.T) {
	principals := func() []QuotaPrincipal { return nil }
	cases := []struct {
		name       string
		store      QuotaStore
		interval   time.Duration
		principals func() []QuotaPrincipal
	}{
		{name: "nil store", store: nil, interval: time.Second, principals: principals},
		{name: "nil principals", store: newMemoryQuotaStore(), interval: time.Second},
		{name: "zero interval", store: newMemoryQuotaStore(), principals: principals},
		{name: "negative interval", store: newMemoryQuotaStore(), interval: -time.Second, principals: principals},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewQuotaPersister(testCase.store, testCase.interval, testCase.principals, nil); !errors.Is(err, ErrInvalidQuotaPersisterConfig) {
				t.Fatalf("err=%v want=%v", err, ErrInvalidQuotaPersisterConfig)
			}
		})
	}
}

// TestQuotaPersisterFlushOnce covers what a tick writes for each shape of
// dirty state, including the group a charged user is capped by.
func TestQuotaPersisterFlushOnce(t *testing.T) {
	cases := []struct {
		name       string
		build      func() []QuotaPrincipal
		wantWrites []string
		wantUser   *float64
		wantGroup  *float64
	}{
		{
			name: "clean user writes nothing",
			build: func() []QuotaPrincipal {
				user := NewUser(1)
				return []QuotaPrincipal{{Scope: QuotaScopeUser, Key: "alice", User: user}}
			},
		},
		{
			name: "charged user is written with its provisioned baseline",
			build: func() []QuotaPrincipal {
				user := NewUser(1)
				mustSetBalance(t, user, 1000)
				user.markQuotaPersisted(1)
				if err := user.AuthorizeAndApplySubmit(Bill{SubmitSmAmount: 100, AuthorizationAmount: 100}); err != nil {
					t.Fatal(err)
				}
				return []QuotaPrincipal{{
					Scope: QuotaScopeUser, Key: "alice", User: user,
					Provisioned: Quota{Balance: floatPtr(1000)},
				}}
			},
			wantWrites: []string{"user/alice"},
			wantUser:   floatPtr(900),
		},
		{
			name: "grouped user writes the group first",
			build: func() []QuotaPrincipal {
				group := NewGroup(1)
				if err := group.SetBalance(5000); err != nil {
					t.Fatal(err)
				}
				user := NewUser(1)
				user.SetGroup(group)
				mustSetBalance(t, user, 1000)
				if err := user.AuthorizeAndApplySubmit(Bill{SubmitSmAmount: 100, AuthorizationAmount: 100}); err != nil {
					t.Fatal(err)
				}
				return []QuotaPrincipal{
					{Scope: QuotaScopeGroup, Key: "premium", Group: group, Provisioned: Quota{Balance: floatPtr(5000)}},
					{Scope: QuotaScopeUser, Key: "alice", User: user, GroupKey: "premium", Provisioned: Quota{Balance: floatPtr(1000)}},
				}
			},
			wantWrites: []string{"group/premium", "user/alice"},
			wantUser:   floatPtr(900),
			wantGroup:  floatPtr(4900),
		},
		{
			name: "a group is written once for two dirty users",
			build: func() []QuotaPrincipal {
				group := NewGroup(1)
				if err := group.SetBalance(5000); err != nil {
					t.Fatal(err)
				}
				principals := []QuotaPrincipal{{Scope: QuotaScopeGroup, Key: "premium", Group: group, Provisioned: Quota{Balance: floatPtr(5000)}}}
				for index, name := range []string{"alice", "bob"} {
					user := NewUser(int64(index + 1))
					user.SetGroup(group)
					mustSetBalance(t, user, 1000)
					if err := user.AuthorizeAndApplySubmit(Bill{SubmitSmAmount: 100, AuthorizationAmount: 100}); err != nil {
						t.Fatal(err)
					}
					principals = append(principals, QuotaPrincipal{
						Scope: QuotaScopeUser, Key: name, User: user, GroupKey: "premium",
						Provisioned: Quota{Balance: floatPtr(1000)},
					})
				}
				return principals
			},
			wantWrites: []string{"group/premium", "user/alice", "user/bob"},
			wantUser:   floatPtr(900),
			wantGroup:  floatPtr(4800),
		},
		{
			name: "submit_sm_count only",
			build: func() []QuotaPrincipal {
				user := NewUser(1)
				user.SetSubmitSmCountQuota(10)
				return []QuotaPrincipal{{
					Scope: QuotaScopeUser, Key: "alice", User: user,
					Provisioned: Quota{SubmitSmCount: intPtr(10)},
				}}
			},
			wantWrites: []string{"user/alice"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			principals := testCase.build()
			store := newMemoryQuotaStore()
			persister, err := NewQuotaPersister(store, time.Hour, func() []QuotaPrincipal { return principals }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := persister.FlushOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			order := store.batchOrder()
			if len(testCase.wantWrites) == 0 {
				if len(order) != 0 {
					t.Fatalf("batches=%v want none", order)
				}
				return
			}
			if len(order) != 1 {
				t.Fatalf("batches=%v want exactly one", order)
			}
			if fmt.Sprint(order[0]) != fmt.Sprint(testCase.wantWrites) {
				t.Fatalf("batch=%v want=%v", order[0], testCase.wantWrites)
			}
			if testCase.wantUser != nil {
				record, ok := store.record(QuotaScopeUser, "alice")
				if !ok {
					t.Fatal("no persisted row for alice")
				}
				if !sameFloat(record.Live.Balance, testCase.wantUser) {
					t.Fatalf("alice balance=%s want=%s", formatFloat(record.Live.Balance), formatFloat(testCase.wantUser))
				}
			}
			if testCase.wantGroup != nil {
				record, ok := store.record(QuotaScopeGroup, "premium")
				if !ok {
					t.Fatal("no persisted row for premium")
				}
				if !sameFloat(record.Live.Balance, testCase.wantGroup) {
					t.Fatalf("premium balance=%s want=%s", formatFloat(record.Live.Balance), formatFloat(testCase.wantGroup))
				}
			}
			// A flushed user is clean, so an idle tick writes nothing more.
			if _, err := persister.FlushOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := len(store.batchOrder()); got != 1 {
				t.Fatalf("batches after idle flush=%d want=1", got)
			}
		})
	}
}

// TestQuotaPersisterFailedWriteStaysDirty proves a store outage never loses a
// charge: the user remains dirty and the next tick retries it.
func TestQuotaPersisterFailedWriteStaysDirty(t *testing.T) {
	user := NewUser(1)
	mustSetBalance(t, user, 1000)
	store := newMemoryQuotaStore()
	store.saveErr = errors.New("connection refused")
	persister, err := NewQuotaPersister(store, time.Hour, func() []QuotaPrincipal {
		return []QuotaPrincipal{{Scope: QuotaScopeUser, Key: "alice", User: user}}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persister.FlushOnce(context.Background()); !errors.Is(err, store.saveErr) {
		t.Fatalf("err=%v want=%v", err, store.saveErr)
	}
	if !user.QuotasDirty() {
		t.Fatal("failed write cleared dirty state")
	}

	store.mu.Lock()
	store.saveErr = nil
	store.mu.Unlock()
	if cleared, err := persister.FlushOnce(context.Background()); err != nil || cleared != 1 {
		t.Fatalf("retry=(%d,%v) want=(1,nil)", cleared, err)
	}
	if user.QuotasDirty() {
		t.Fatal("successful retry left the user dirty")
	}
}

func TestQuotaPersisterSortsSharedGroupRows(t *testing.T) {
	principals := make([]QuotaPrincipal, 0, 4)
	for index, key := range []string{"zeta", "alpha"} {
		group := NewGroup(int64(index + 1))
		if err := group.SetBalance(1000); err != nil {
			t.Fatal(err)
		}
		user := NewUser(int64(index + 1))
		user.SetGroup(group)
		mustSetBalance(t, user, 100)
		if err := user.AuthorizeAndApplySubmit(Bill{SubmitSmAmount: 1, AuthorizationAmount: 1}); err != nil {
			t.Fatal(err)
		}
		principals = append(principals,
			QuotaPrincipal{Scope: QuotaScopeGroup, Key: key, Group: group},
			QuotaPrincipal{Scope: QuotaScopeUser, Key: key + "-user", User: user, GroupKey: key},
		)
	}
	store := newMemoryQuotaStore()
	persister, err := NewQuotaPersister(store, time.Hour, func() []QuotaPrincipal { return principals }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persister.FlushOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	order := store.batchOrder()
	want := []string{"group/alpha", "group/zeta", "user/zeta-user", "user/alpha-user"}
	if len(order) != 1 || fmt.Sprint(order[0]) != fmt.Sprint(want) {
		t.Fatalf("batch=%v want=%v", order, want)
	}
}

// TestQuotaPersisterConcurrentChargeStaysDirty pins that a charge landing while
// a flush is in flight is not marked persisted by that flush.
func TestQuotaPersisterConcurrentChargeStaysDirty(t *testing.T) {
	user := NewUser(1)
	mustSetBalance(t, user, 1000)
	store := &blockingQuotaStore{inner: newMemoryQuotaStore(), entered: make(chan struct{}), release: make(chan struct{})}
	persister, err := NewQuotaPersister(store, time.Hour, func() []QuotaPrincipal {
		return []QuotaPrincipal{{Scope: QuotaScopeUser, Key: "alice", User: user}}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := persister.FlushOnce(context.Background()); done <- err }()
	<-store.entered
	if err := user.AuthorizeAndApplySubmit(Bill{SubmitSmAmount: 1, AuthorizationAmount: 1}); err != nil {
		t.Fatal(err)
	}
	close(store.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !user.QuotasDirty() {
		t.Fatal("a charge that landed mid-write was marked persisted")
	}
}

// TestQuotaPersisterRunSurvivesStoreErrors proves the worker keeps flushing
// after a failure instead of exiting: a transient database blip must not leave
// balances memory-only until the next restart.
func TestQuotaPersisterRunSurvivesStoreErrors(t *testing.T) {
	user := NewUser(1)
	mustSetBalance(t, user, 1000)
	store := newMemoryQuotaStore()
	store.saveErr = errors.New("connection refused")
	failures := make(chan error, 4)
	persister, err := NewQuotaPersister(store, time.Millisecond, func() []QuotaPrincipal {
		return []QuotaPrincipal{{Scope: QuotaScopeUser, Key: "alice", User: user, Provisioned: Quota{Balance: floatPtr(1000)}}}
	}, func(err error) {
		select {
		case failures <- err:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- persister.Run(ctx) }()

	select {
	case <-failures:
	case <-time.After(2 * time.Second):
		t.Fatal("flush failure was not reported")
	}
	store.mu.Lock()
	store.saveErr = nil
	store.mu.Unlock()
	select {
	case <-store.saved:
	case <-time.After(2 * time.Second):
		t.Fatal("worker stopped flushing after an error")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v want=%v", err, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not honour cancellation")
	}
	if record, ok := store.record(QuotaScopeUser, "alice"); !ok || !sameFloat(record.Live.Balance, floatPtr(1000)) {
		t.Fatalf("persisted record=%+v ok=%v", record, ok)
	}
}

type blockingQuotaStore struct {
	inner   *memoryQuotaStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (store *blockingQuotaStore) SaveQuotas(ctx context.Context, records []QuotaRecord) error {
	store.once.Do(func() {
		close(store.entered)
		<-store.release
	})
	return store.inner.SaveQuotas(ctx, records)
}

func (store *blockingQuotaStore) LoadQuotas(ctx context.Context) ([]QuotaRecord, error) {
	return store.inner.LoadQuotas(ctx)
}

func mustSetBalance(t *testing.T, user *User, balance float64) {
	t.Helper()
	if err := user.SetBalance(balance); err != nil {
		t.Fatal(err)
	}
}

func formatFloat(value *float64) string {
	if value == nil {
		return "unlimited"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func formatInt(value *int) string {
	if value == nil {
		return "unlimited"
	}
	return strconv.Itoa(*value)
}
