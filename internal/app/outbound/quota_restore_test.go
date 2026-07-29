package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/billing"
)

// memoryQuotaStore stands in for the PostgreSQL quota store so the restart
// behaviour can be exercised without a database.
type memoryQuotaStore struct {
	mu   sync.Mutex
	rows map[string]billing.QuotaRecord
}

func newMemoryQuotaStore() *memoryQuotaStore {
	return &memoryQuotaStore{rows: make(map[string]billing.QuotaRecord)}
}

func (store *memoryQuotaStore) SaveQuotas(_ context.Context, records []billing.QuotaRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, record := range records {
		store.rows[string(record.Scope)+"/"+record.Key] = record
	}
	return nil
}

func (store *memoryQuotaStore) LoadQuotas(context.Context) ([]billing.QuotaRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	records := make([]billing.QuotaRecord, 0, len(store.rows))
	for _, record := range store.rows {
		records = append(records, record)
	}
	return records, nil
}

func testUser(username string, balance *float64, count *int, groupID string) UserConfig {
	digest := sha256.Sum256([]byte("secret"))
	return UserConfig{
		Username:       username,
		ExternalID:     username + "-id",
		PasswordSHA256: hex.EncodeToString(digest[:]),
		Balance:        balance,
		SubmitSMCount:  count,
		GroupID:        groupID,
	}
}

func floatPtr(value float64) *float64 { return &value }
func intPtr(value int) *int           { return &value }

// restart rebuilds a directory from the durable store, the way a process
// restart does: load the quota index, then provision from the config spec.
func restart(t *testing.T, config Config, store billing.QuotaStore) *runtimeDirectory {
	t.Helper()
	index, err := billing.LoadQuotaIndex(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := newRuntimeDirectoryWithQuotas(config, index)
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func flush(t *testing.T, directory *runtimeDirectory, store billing.QuotaStore) {
	t.Helper()
	persister, err := billing.NewQuotaPersister(store, time.Hour, directory.quotaPrincipals, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persister.FlushOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func userBalance(t *testing.T, directory *runtimeDirectory, username string) float64 {
	t.Helper()
	user, err := directory.users.GetUser(username)
	if err != nil {
		t.Fatal(err)
	}
	return user.Balance()
}

// TestDirectoryRestoresSpentBalanceAcrossRestart is the regression for the
// money bug: a balance mutated in memory is flushed, and a fresh directory
// built from the same config restores what is left instead of re-granting the
// provisioned amount.
func TestDirectoryRestoresSpentBalanceAcrossRestart(t *testing.T) {
	config := Config{Users: []UserConfig{testUser("alice", floatPtr(1000), intPtr(100), "")}}
	store := newMemoryQuotaStore()

	first := restart(t, config, store)
	user, err := first.users.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := user.AuthorizeAndApplySubmit(billing.Bill{SubmitSmAmount: 900, AuthorizationAmount: 900, DecrementSubmitSmCount: 60}); err != nil {
		t.Fatal(err)
	}
	flush(t, first, store)

	second := restart(t, config, store)
	if got := userBalance(t, second, "alice"); got != 100 {
		t.Fatalf("restored balance=%v want=100 (the provisioned 1000 would be a refund)", got)
	}
	snapshot, err := second.Balance(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SMSCount == nil || *snapshot.SMSCount != "40" {
		t.Fatalf("restored submit_sm_count=%v want=40", snapshot.SMSCount)
	}
}

// TestDirectoryQuotaRestorePrecedence walks the whole precedence rule through
// the real provisioning path.
func TestDirectoryQuotaRestorePrecedence(t *testing.T) {
	cases := []struct {
		name          string
		provisioned   *float64
		spend         float64
		reprovisioned *float64
		want          float64
	}{
		{
			name:        "unchanged spec keeps the spent balance",
			provisioned: floatPtr(1000), spend: 900,
			reprovisioned: floatPtr(1000), want: 100,
		},
		{
			name:        "raised grant is a top-up and wins",
			provisioned: floatPtr(1000), spend: 900,
			reprovisioned: floatPtr(2500), want: 2500,
		},
		{
			name:        "lowered grant wins too",
			provisioned: floatPtr(1000), spend: 900,
			reprovisioned: floatPtr(50), want: 50,
		},
		{
			name:        "fully spent account stays spent",
			provisioned: floatPtr(1000), spend: 1000,
			reprovisioned: floatPtr(1000), want: 0,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := newMemoryQuotaStore()
			before := restart(t, Config{Users: []UserConfig{testUser("alice", testCase.provisioned, nil, "")}}, store)
			user, err := before.users.GetUser("alice")
			if err != nil {
				t.Fatal(err)
			}
			if err := user.AuthorizeAndApplySubmit(billing.Bill{SubmitSmAmount: testCase.spend, AuthorizationAmount: testCase.spend}); err != nil {
				t.Fatal(err)
			}
			flush(t, before, store)

			after := restart(t, Config{Users: []UserConfig{testUser("alice", testCase.reprovisioned, nil, "")}}, store)
			if got := userBalance(t, after, "alice"); got != testCase.want {
				t.Fatalf("balance=%v want=%v", got, testCase.want)
			}
		})
	}
}

// TestDirectoryWithoutPersistedRowUsesProvisionedValue pins the new-customer
// branch: nothing durable yet means the spec is the grant.
func TestDirectoryWithoutPersistedRowUsesProvisionedValue(t *testing.T) {
	store := newMemoryQuotaStore()
	config := Config{Users: []UserConfig{
		testUser("alice", floatPtr(1000), nil, ""),
		testUser("bob", floatPtr(7), nil, ""),
	}}
	first := restart(t, config, store)
	user, err := first.users.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := user.AuthorizeAndApplySubmit(billing.Bill{SubmitSmAmount: 900, AuthorizationAmount: 900}); err != nil {
		t.Fatal(err)
	}
	flush(t, first, store)

	second := restart(t, config, store)
	if got := userBalance(t, second, "alice"); got != 100 {
		t.Fatalf("alice balance=%v want=100", got)
	}
	// bob never charged, so nothing was ever written for him; he must still get
	// the provisioned grant rather than a zero balance.
	if got := userBalance(t, second, "bob"); got != 7 {
		t.Fatalf("bob balance=%v want the provisioned 7", got)
	}
}

// TestDirectoryRestoresGroupCeiling proves the group ceiling survives too: a
// restored user balance with a re-granted group ceiling would let a customer
// spend the group's money twice.
func TestDirectoryRestoresGroupCeiling(t *testing.T) {
	config := Config{
		Groups: []GroupConfig{{GID: "premium", Balance: floatPtr(5000)}},
		Users:  []UserConfig{testUser("alice", floatPtr(1000), nil, "premium")},
	}
	store := newMemoryQuotaStore()

	first := restart(t, config, store)
	user, err := first.users.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := user.AuthorizeAndApplySubmit(billing.Bill{SubmitSmAmount: 400, AuthorizationAmount: 400}); err != nil {
		t.Fatal(err)
	}
	flush(t, first, store)

	second := restart(t, config, store)
	group, known := second.lookupGroup("premium")
	if !known {
		t.Fatal("group premium missing after restart")
	}
	if got := group.Balance(); got != 4600 {
		t.Fatalf("group balance=%v want=4600", got)
	}
	if got := userBalance(t, second, "alice"); got != 600 {
		t.Fatalf("user balance=%v want=600", got)
	}
}

// TestDirectoryRestoreIsConsumedOnce pins that restoration belongs to boot
// only: an operator editing an account while the gateway runs installs exactly
// the balance they supplied.
func TestDirectoryRestoreIsConsumedOnce(t *testing.T) {
	config := Config{Users: []UserConfig{testUser("alice", floatPtr(1000), nil, "")}}
	store := newMemoryQuotaStore()

	first := restart(t, config, store)
	user, err := first.users.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := user.AuthorizeAndApplySubmit(billing.Bill{SubmitSmAmount: 900, AuthorizationAmount: 900}); err != nil {
		t.Fatal(err)
	}
	flush(t, first, store)

	second := restart(t, config, store)
	if got := userBalance(t, second, "alice"); got != 100 {
		t.Fatalf("balance after restart=%v want=100", got)
	}
	// The admin plane edits the account online: remove, then re-add with the
	// operator's number. The stale durable row must not win.
	if err := second.removeUser("alice"); err != nil {
		t.Fatal(err)
	}
	if err := second.applyUser(testUser("alice", floatPtr(1000), nil, ""), 42); err != nil {
		t.Fatal(err)
	}
	if got := userBalance(t, second, "alice"); got != 1000 {
		t.Fatalf("balance after an online re-provision=%v want the operator's 1000", got)
	}
}

// TestDirectoryRestoreClampsCorruptNegativeBalance documents the defensive
// branch: a hand-edited or corrupt negative row must not take the gateway down,
// and must not be restored as a debt the customer can spend around.
func TestDirectoryRestoreClampsCorruptNegativeBalance(t *testing.T) {
	index := billing.NewQuotaIndex([]billing.QuotaRecord{{
		Scope: billing.QuotaScopeUser, Key: "alice",
		Live:        billing.Quota{Balance: floatPtr(-5), SubmitSmCount: intPtr(-2)},
		Provisioned: billing.Quota{Balance: floatPtr(1000), SubmitSmCount: intPtr(10)},
	}})
	directory, err := newRuntimeDirectoryWithQuotas(Config{Users: []UserConfig{testUser("alice", floatPtr(1000), intPtr(10), "")}}, index)
	if err != nil {
		t.Fatal(err)
	}
	if got := userBalance(t, directory, "alice"); got != 0 {
		t.Fatalf("balance=%v want=0", got)
	}
	snapshot, err := directory.Balance(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SMSCount == nil || *snapshot.SMSCount != "0" {
		t.Fatalf("submit_sm_count=%v want=0", snapshot.SMSCount)
	}
}

// TestRuntimeCloseFlushesAndJoinsTheQuotaWorker exercises the shutdown half of
// the lifecycle on a runtime carrying only the quota fields (every other
// resource is nil-guarded in Close). The flush interval is an hour, so nothing
// but the shutdown flush can have written the row — and if Close failed to join
// the worker, the WaitGroup would never return.
func TestRuntimeCloseFlushesAndJoinsTheQuotaWorker(t *testing.T) {
	store := newMemoryQuotaStore()
	directory := restart(t, Config{Users: []UserConfig{testUser("alice", floatPtr(1000), nil, "")}}, store)
	persister, err := billing.NewQuotaPersister(store, time.Hour, directory.quotaPrincipals, nil)
	if err != nil {
		t.Fatal(err)
	}
	quotaCtx, quotaCancel := context.WithCancel(context.Background())
	runtime := &Runtime{quotaPersister: persister, quotaCancel: quotaCancel, quotaFlushOnStop: shutdownQuotaFlushTimeout}
	runtime.quotaWG.Add(1)
	go func() {
		defer runtime.quotaWG.Done()
		_ = persister.Run(quotaCtx)
	}()

	user, err := directory.users.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := user.AuthorizeAndApplySubmit(billing.Bill{SubmitSmAmount: 900, AuthorizationAmount: 900}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	after := restart(t, Config{Users: []UserConfig{testUser("alice", floatPtr(1000), nil, "")}}, store)
	if got := userBalance(t, after, "alice"); got != 100 {
		t.Fatalf("balance after a clean shutdown=%v want=100", got)
	}
}

func TestQuotaPersistIntervalConfiguration(t *testing.T) {
	cases := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{name: "unset falls back to the default", seconds: 0, want: billing.DefaultQuotaPersistInterval},
		{name: "explicit seconds", seconds: 3, want: 3 * time.Second},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := (Config{QuotaPersistIntervalSeconds: testCase.seconds}).quotaPersistInterval(); got != testCase.want {
				t.Fatalf("interval=%v want=%v", got, testCase.want)
			}
		})
	}
	config := Config{
		ListenAddress:               "127.0.0.1:1",
		AMQPURL:                     "amqp://localhost/",
		PythonPath:                  "/usr/bin/python3",
		PostgresDSN:                 "postgres://localhost/jasmin",
		Users:                       []UserConfig{testUser("alice", nil, nil, "")},
		Routes:                      []RouteConfig{{ConnectorID: "smppc", Default: true}},
		QuotaPersistIntervalSeconds: -1,
	}
	if err := validateConfig(config); err == nil {
		t.Fatal("negative quota_persist_interval_seconds accepted")
	}
	config.QuotaPersistIntervalSeconds = 30
	if err := validateConfig(config); err != nil {
		t.Fatal(err)
	}
}
