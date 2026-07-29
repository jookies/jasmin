package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/billing"
)

func openQuotaStore(t *testing.T) *PostgresQuotaStore {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	store, err := OpenPostgresQuotaStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Applying the whole embedded file again must be a no-op: that is the only
	// migration contract this repository has (CREATE TABLE IF NOT EXISTS, run
	// whole on every boot).
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `TRUNCATE billing_quotas`); err != nil {
		t.Fatal(err)
	}
	return store
}

func float64Ptr(value float64) *float64 { return &value }
func intValuePtr(value int) *int        { return &value }

func TestPostgresQuotaStoreRoundTrip(t *testing.T) {
	store := openQuotaStore(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		record billing.QuotaRecord
	}{
		{
			name: "finite user quota",
			record: billing.QuotaRecord{
				Scope: billing.QuotaScopeUser, Key: "alice",
				Live:        billing.Quota{Balance: float64Ptr(100.25), SubmitSmCount: intValuePtr(40)},
				Provisioned: billing.Quota{Balance: float64Ptr(1000.5), SubmitSmCount: intValuePtr(100)},
			},
		},
		{
			name: "unlimited members survive as NULL, not zero",
			record: billing.QuotaRecord{
				Scope: billing.QuotaScopeUser, Key: "bob",
				Live:        billing.Quota{SubmitSmCount: intValuePtr(3)},
				Provisioned: billing.Quota{SubmitSmCount: intValuePtr(9)},
			},
		},
		{
			name: "group scope shares the table",
			record: billing.QuotaRecord{
				Scope: billing.QuotaScopeGroup, Key: "premium",
				Live:        billing.Quota{Balance: float64Ptr(4600)},
				Provisioned: billing.Quota{Balance: float64Ptr(5000)},
			},
		},
		{
			name: "a user and a group may share a key",
			record: billing.QuotaRecord{
				Scope: billing.QuotaScopeUser, Key: "premium",
				Live:        billing.Quota{Balance: float64Ptr(1)},
				Provisioned: billing.Quota{Balance: float64Ptr(2)},
			},
		},
	}
	records := make([]billing.QuotaRecord, 0, len(cases))
	for _, testCase := range cases {
		records = append(records, testCase.record)
	}
	if err := store.SaveQuotas(ctx, records); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(records) {
		t.Fatalf("loaded %d rows, want %d", len(loaded), len(records))
	}
	index := billing.NewQuotaIndex(loaded)
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := index.Take(testCase.record.Scope, testCase.record.Key, testCase.record.Provisioned)
			// Round-tripped through double precision, the durable value must
			// come back bit-identical or the top-up baseline comparison would
			// drift and silently re-grant.
			if !equalFloat(got.Balance, testCase.record.Live.Balance) {
				t.Fatalf("balance=%v want=%v", got.Balance, testCase.record.Live.Balance)
			}
			if !equalInt(got.SubmitSmCount, testCase.record.Live.SubmitSmCount) {
				t.Fatalf("submit_sm_count=%v want=%v", got.SubmitSmCount, testCase.record.Live.SubmitSmCount)
			}
		})
	}

	// Re-saving the same key updates in place rather than conflicting.
	updated := records[0]
	updated.Live.Balance = float64Ptr(7.5)
	if err := store.SaveQuotas(ctx, []billing.QuotaRecord{updated}); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.LoadQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(records) {
		t.Fatalf("upsert produced %d rows, want %d", len(loaded), len(records))
	}
	after := billing.NewQuotaIndex(loaded).Take(billing.QuotaScopeUser, "alice", updated.Provisioned)
	if after.Balance == nil || *after.Balance != 7.5 {
		t.Fatalf("upserted balance=%v want=7.5", after.Balance)
	}
}

func TestPostgresQuotaStoreRejectsEmptyKeyAndRollsBack(t *testing.T) {
	store := openQuotaStore(t)
	ctx := context.Background()
	err := store.SaveQuotas(ctx, []billing.QuotaRecord{
		{Scope: billing.QuotaScopeUser, Key: "alice", Live: billing.Quota{Balance: float64Ptr(1)}},
		{Scope: billing.QuotaScopeUser, Key: ""},
	})
	if err == nil {
		t.Fatal("empty principal key accepted")
	}
	loaded, err := store.LoadQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("rejected batch left %d rows behind; the write must be atomic", len(loaded))
	}
}

// TestPostgresQuotaStoreSurvivesRestart drives the whole durable loop against a
// real database: charge a grouped user, flush through the persister, then
// reload the way a boot does and confirm the spent balance — not the
// provisioned grant — is what comes back.
func TestPostgresQuotaStoreSurvivesRestart(t *testing.T) {
	store := openQuotaStore(t)
	ctx := context.Background()

	group := billing.NewGroup(1)
	if err := group.SetBalance(5000); err != nil {
		t.Fatal(err)
	}
	user := billing.NewUser(1)
	user.SetGroup(group)
	if err := user.SetBalance(1000); err != nil {
		t.Fatal(err)
	}
	user.SetSubmitSmCountQuota(100)
	if err := user.AuthorizeAndApplySubmit(billing.Bill{SubmitSmAmount: 900, AuthorizationAmount: 900, DecrementSubmitSmCount: 60}); err != nil {
		t.Fatal(err)
	}

	provisionedUser := billing.Quota{Balance: float64Ptr(1000), SubmitSmCount: intValuePtr(100)}
	provisionedGroup := billing.Quota{Balance: float64Ptr(5000)}
	persister, err := billing.NewQuotaPersister(store, time.Hour, func() []billing.QuotaPrincipal {
		return []billing.QuotaPrincipal{
			{Scope: billing.QuotaScopeGroup, Key: "premium", Group: group, Provisioned: provisionedGroup},
			{Scope: billing.QuotaScopeUser, Key: "alice", User: user, GroupKey: "premium", Provisioned: provisionedUser},
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cleared, err := persister.FlushOnce(ctx); err != nil || cleared != 1 {
		t.Fatalf("flush=(%d,%v) want=(1,nil)", cleared, err)
	}

	index, err := billing.LoadQuotaIndex(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	restoredUser := index.Take(billing.QuotaScopeUser, "alice", provisionedUser)
	if restoredUser.Balance == nil || *restoredUser.Balance != 100 {
		t.Fatalf("restored user balance=%v want=100", restoredUser.Balance)
	}
	if restoredUser.SubmitSmCount == nil || *restoredUser.SubmitSmCount != 40 {
		t.Fatalf("restored submit_sm_count=%v want=40", restoredUser.SubmitSmCount)
	}
	restoredGroup := index.Take(billing.QuotaScopeGroup, "premium", provisionedGroup)
	if restoredGroup.Balance == nil || *restoredGroup.Balance != 4100 {
		t.Fatalf("restored group balance=%v want=4100", restoredGroup.Balance)
	}
	// A re-grant still wins, so topping the account up remains possible.
	toppedUp := billing.Quota{Balance: float64Ptr(2500), SubmitSmCount: intValuePtr(100)}
	freshIndex, err := billing.LoadQuotaIndex(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	afterTopUp := freshIndex.Take(billing.QuotaScopeUser, "alice", toppedUp)
	if afterTopUp.Balance == nil || *afterTopUp.Balance != 2500 {
		t.Fatalf("topped-up balance=%v want=2500", afterTopUp.Balance)
	}
	if afterTopUp.SubmitSmCount == nil || *afterTopUp.SubmitSmCount != 40 {
		t.Fatalf("submit_sm_count after a balance-only top-up=%v want=40", afterTopUp.SubmitSmCount)
	}
}

func TestPostgresQuotaStoreEmptyBatchIsANoOp(t *testing.T) {
	store := openQuotaStore(t)
	if err := store.SaveQuotas(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresQuotaStorePrunesOnlyOrphanedPrincipals(t *testing.T) {
	store := openQuotaStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `TRUNCATE billing_quotas`); err != nil {
		t.Fatal(err)
	}
	records := []billing.QuotaRecord{
		{Scope: billing.QuotaScopeUser, Key: "alice"},
		{Scope: billing.QuotaScopeUser, Key: "deleted"},
		{Scope: billing.QuotaScopeGroup, Key: "premium"},
		{Scope: billing.QuotaScopeGroup, Key: "orphan"},
	}
	if err := store.SaveQuotas(ctx, records); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.PruneQuotas(ctx, []billing.QuotaKey{
		{Scope: billing.QuotaScopeUser, Key: "alice"},
		{Scope: billing.QuotaScopeGroup, Key: "premium"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted=%d want=2", deleted)
	}
	got, err := store.LoadQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "premium" || got[1].Key != "alice" {
		t.Fatalf("remaining quotas=%+v", got)
	}
}

func equalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func equalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
