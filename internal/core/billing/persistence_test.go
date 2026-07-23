package billing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

type persistenceFixture struct {
	Cases []struct {
		ID    string `json:"id"`
		Input struct {
			DirtyBefore   []bool `json:"dirty_before"`
			PersistResult bool   `json:"persist_result"`
		} `json:"input"`
		Expected struct {
			PersistScopes []string `json:"persist_scopes"`
			DirtyAfter    []bool   `json:"dirty_after"`
			RearmCount    int      `json:"rearm_count"`
		} `json:"expected"`
	} `json:"cases"`
}

type recordingQuotaStore struct {
	mu         sync.Mutex
	operations []string
	result     bool
	err        error
	started    chan struct{}
	release    chan struct{}
	userWrites chan struct{}
	blockOnce  sync.Once
}

func (store *recordingQuotaStore) block(ctx context.Context) error {
	var result error
	store.blockOnce.Do(func() {
		if store.started != nil {
			select {
			case store.started <- struct{}{}:
			case <-ctx.Done():
				result = ctx.Err()
				return
			}
		}
		if store.release != nil {
			select {
			case <-store.release:
			case <-ctx.Done():
				result = ctx.Err()
			}
		}
	})
	return result
}

func (store *recordingQuotaStore) PersistGroup(ctx context.Context, _ GroupState) (bool, error) {
	if err := store.block(ctx); err != nil {
		return false, err
	}
	store.mu.Lock()
	store.operations = append(store.operations, "groups")
	store.mu.Unlock()
	return store.result, store.err
}

func (store *recordingQuotaStore) PersistUser(ctx context.Context, _ UserState) (bool, error) {
	if err := store.block(ctx); err != nil {
		return false, err
	}
	store.mu.Lock()
	store.operations = append(store.operations, "users")
	store.mu.Unlock()
	if store.userWrites != nil {
		select {
		case store.userWrites <- struct{}{}:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return store.result, store.err
}

func TestQuotaPersistenceGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "compat", "fixtures", "billing-persistence", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture persistenceFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			if testCase.Expected.RearmCount != 1 {
				t.Fatalf("rearm_count=%d want=1", testCase.Expected.RearmCount)
			}
			users := make([]*User, len(testCase.Input.DirtyBefore))
			for index, dirty := range testCase.Input.DirtyBefore {
				users[index] = NewUser(int64(index + 1))
				users[index].SetGroup(NewGroup(1))
				if dirty {
					if err := users[index].SetBalance(2); err != nil {
						t.Fatal(err)
					}
				}
			}
			store := &recordingQuotaStore{result: testCase.Input.PersistResult}
			service, err := NewQuotaPersistenceService(users, store, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := service.PersistOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantPersisted := len(testCase.Expected.PersistScopes) != 0
			if persisted != wantPersisted {
				t.Fatalf("persisted=%v want=%v", persisted, wantPersisted)
			}
			if !slices.Equal(store.operations, testCase.Expected.PersistScopes) {
				t.Fatalf("operations=%v want=%v", store.operations, testCase.Expected.PersistScopes)
			}
			for index, user := range users {
				if got := user.QuotasDirty(); got != testCase.Expected.DirtyAfter[index] {
					t.Fatalf("user %d dirty=%v want=%v", index, got, testCase.Expected.DirtyAfter[index])
				}
			}
		})
	}
}

func TestQuotaPersistenceFailedWriteRemainsDirty(t *testing.T) {
	user := NewUser(1)
	user.SetGroup(NewGroup(1))
	if err := user.SetBalance(2); err != nil {
		t.Fatal(err)
	}
	service, err := NewQuotaPersistenceService([]*User{user}, &recordingQuotaStore{err: errors.New("disk")}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PersistOnce(context.Background()); err == nil {
		t.Fatal("expected persistence error")
	}
	if !user.QuotasDirty() {
		t.Fatal("failed persistence cleared dirty state")
	}
}

func TestQuotaPersistenceDoesNotClearConcurrentMutation(t *testing.T) {
	user := NewUser(1)
	user.SetGroup(NewGroup(1))
	if err := user.SetBalance(2); err != nil {
		t.Fatal(err)
	}
	store := &recordingQuotaStore{result: true, started: make(chan struct{}), release: make(chan struct{})}
	service, err := NewQuotaPersistenceService([]*User{user}, store, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.PersistOnce(context.Background()); done <- err }()
	<-store.started
	user.SetSubmitSmCountQuota(9)
	close(store.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !user.QuotasDirty() {
		t.Fatal("concurrent mutation was incorrectly cleared")
	}
}

func TestQuotaPersistenceSerializesConcurrentCalls(t *testing.T) {
	user := NewUser(1)
	if err := user.SetBalance(2); err != nil {
		t.Fatal(err)
	}
	store := &recordingQuotaStore{result: true, started: make(chan struct{}), release: make(chan struct{})}
	service, err := NewQuotaPersistenceService([]*User{user}, store, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan bool, 2)
	for range 2 {
		go func() { persisted, _ := service.PersistOnce(context.Background()); results <- persisted }()
	}
	<-store.started
	close(store.release)
	first, second := <-results, <-results
	if first == second {
		t.Fatalf("persist results=%v,%v want exactly one write", first, second)
	}
	if !reflect.DeepEqual(store.operations, []string{"users"}) {
		t.Fatalf("operations=%v want one user write", store.operations)
	}
}

func TestQuotaPersistenceRunContinuesAcrossTicks(t *testing.T) {
	users := []*User{NewUser(1), NewUser(2)}
	for _, user := range users {
		if err := user.SetBalance(2); err != nil {
			t.Fatal(err)
		}
	}
	store := &recordingQuotaStore{result: true, userWrites: make(chan struct{}, 2)}
	service, err := NewQuotaPersistenceService(users, store, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	for range 2 {
		select {
		case <-store.userWrites:
		case <-time.After(time.Second):
			t.Fatal("periodic service did not continue to next tick")
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestQuotaPersistenceRunRearmsAfterCompletedWrite(t *testing.T) {
	users := []*User{NewUser(1), NewUser(2)}
	for _, user := range users {
		if err := user.SetBalance(2); err != nil {
			t.Fatal(err)
		}
	}
	interval := 40 * time.Millisecond
	store := &recordingQuotaStore{
		result:     true,
		started:    make(chan struct{}, 1),
		release:    make(chan struct{}),
		userWrites: make(chan struct{}, 2),
	}
	service, err := NewQuotaPersistenceService(users, store, interval)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	<-store.started
	time.Sleep(2 * interval)
	releasedAt := time.Now()
	close(store.release)
	select {
	case <-store.userWrites:
	case <-time.After(time.Second):
		t.Fatal("first write did not finish")
	}
	select {
	case <-store.userWrites:
		t.Fatalf("second write started %s after first completion; want at least %s", time.Since(releasedAt), interval)
	case <-time.After(interval / 2):
	}
	select {
	case <-store.userWrites:
	case <-time.After(3 * interval):
		t.Fatal("second write did not begin after completion-relative rearm")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestQuotaPersistenceRunCancelsActiveWrite(t *testing.T) {
	user := NewUser(1)
	user.SetGroup(NewGroup(1))
	if err := user.SetBalance(2); err != nil {
		t.Fatal(err)
	}
	store := &recordingQuotaStore{result: true, started: make(chan struct{}), release: make(chan struct{})}
	service, err := NewQuotaPersistenceService([]*User{user}, store, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	<-store.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active persistence did not honor cancellation")
	}
}

func TestQuotaPersistenceRunReturnsStoreError(t *testing.T) {
	user := NewUser(1)
	if err := user.SetBalance(2); err != nil {
		t.Fatal(err)
	}
	want := errors.New("store unavailable")
	service, err := NewQuotaPersistenceService([]*User{user}, &recordingQuotaStore{err: want}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Run(ctx); !errors.Is(err, want) {
		t.Fatalf("error=%v want=%v", err, want)
	}
	if !user.QuotasDirty() {
		t.Fatal("failed periodic persistence cleared dirty state")
	}
}
