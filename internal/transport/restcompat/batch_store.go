package restcompat

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	// ErrBatchQueueFull is returned before a batch is acknowledged when the
	// configured durable backlog ceiling would be exceeded.
	ErrBatchQueueFull = errors.New("REST batch queue is full")
	// ErrNoBatchWork is the normal empty-queue result from a non-blocking claim.
	ErrNoBatchWork = errors.New("no REST batch work available")
)

const (
	taskPending = "PENDING"
	taskRunning = "RUNNING"
	taskDone    = "DONE"

	callbackNone    = "NONE"
	callbackPending = "PENDING"
	callbackRunning = "RUNNING"
	callbackDone    = "DONE"
)

// BatchStore is the durable boundary for accepted sendbatch work. Implementors
// must atomically persist every task before CreateBatch returns and must claim
// work with lease/fencing semantics so several gateway processes cannot execute
// the same live task concurrently.
type BatchStore interface {
	CreateBatch(context.Context, StoredBatch, int) error
	RecoverExpired(context.Context, time.Time) (int64, error)
	ClaimTask(context.Context, string, time.Time, time.Duration) (StoredTask, error)
	RetryTask(context.Context, string, string, time.Time, string) error
	FinishTask(context.Context, string, string, TaskResult, time.Time) error
	ClaimCallback(context.Context, string, time.Time, time.Duration) (StoredCallback, error)
	RetryCallback(context.Context, string, string, time.Time, string) error
	FinishCallback(context.Context, string, string, time.Time) error
}

// StoredBatch is the complete admission record written before the HTTP request
// is acknowledged. Passwords are never present: tasks retain only a SHA-256
// credential proof, which the internal HTTP handler compares with the user's
// current digest when the delayed task actually runs.
type StoredBatch struct {
	ID          string
	AcceptedAt  time.Time
	CallbackURL string
	ErrbackURL  string
	Tasks       []StoredTask
}

// StoredTask is one expanded destination in a batch.
type StoredTask struct {
	ID               string
	BatchID          string
	Sequence         int
	Destination      string
	Username         string
	CredentialDigest []byte
	Body             []byte
	AvailableAt      time.Time
	Attempt          int
	CallbackURL      string
	ErrbackURL       string
}

// TaskResult is committed atomically with terminal task state. If CallbackURL
// is empty, callback delivery is already complete.
type TaskResult struct {
	Successful  bool
	HTTPStatus  int
	StatusText  string
	CallbackURL string
}

// StoredCallback is terminal task progress waiting to be delivered.
type StoredCallback struct {
	TaskID      string
	BatchID     string
	Destination string
	URL         string
	Status      int
	StatusText  string
	Attempt     int
}

type memoryTask struct {
	task          StoredTask
	state         string
	lockOwner     string
	lockedUntil   time.Time
	lastError     string
	callback      StoredCallback
	callbackState string
	callbackOwner string
	callbackUntil time.Time
}

// MemoryBatchStore is a deterministic non-production implementation used by
// package consumers that do not opt into sendbatch and by unit tests. Gateway
// production wiring always supplies the PostgreSQL implementation.
type MemoryBatchStore struct {
	mu    sync.Mutex
	tasks map[string]*memoryTask
}

func NewMemoryBatchStore() *MemoryBatchStore {
	return &MemoryBatchStore{tasks: make(map[string]*memoryTask)}
}

func (store *MemoryBatchStore) CreateBatch(_ context.Context, batch StoredBatch, maxPending int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	pending := 0
	for _, task := range store.tasks {
		if task.state != taskDone ||
			task.callbackState == callbackPending || task.callbackState == callbackRunning {
			pending++
		}
	}
	if maxPending > 0 && pending+len(batch.Tasks) > maxPending {
		return ErrBatchQueueFull
	}
	for _, candidate := range batch.Tasks {
		if _, exists := store.tasks[candidate.ID]; exists {
			continue
		}
		task := cloneStoredTask(candidate)
		task.BatchID = batch.ID
		task.CallbackURL = batch.CallbackURL
		task.ErrbackURL = batch.ErrbackURL
		store.tasks[task.ID] = &memoryTask{
			task: task, state: taskPending, callbackState: callbackNone,
		}
	}
	return nil
}

func (store *MemoryBatchStore) RecoverExpired(_ context.Context, now time.Time) (int64, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var recovered int64
	for _, task := range store.tasks {
		if task.state == taskRunning && !task.lockedUntil.After(now) {
			task.state, task.lockOwner = taskPending, ""
			task.lockedUntil = time.Time{}
			recovered++
		}
		if task.callbackState == callbackRunning && !task.callbackUntil.After(now) {
			task.callbackState, task.callbackOwner = callbackPending, ""
			task.callbackUntil = time.Time{}
			recovered++
		}
	}
	return recovered, nil
}

func (store *MemoryBatchStore) ClaimTask(
	_ context.Context,
	owner string,
	now time.Time,
	lease time.Duration,
) (StoredTask, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	ordered := make([]*memoryTask, 0, len(store.tasks))
	for _, task := range store.tasks {
		if task.state == taskPending && !task.task.AvailableAt.After(now) {
			ordered = append(ordered, task)
		}
	}
	if len(ordered) == 0 {
		return StoredTask{}, ErrNoBatchWork
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].task.AvailableAt.Equal(ordered[j].task.AvailableAt) {
			if ordered[i].task.BatchID == ordered[j].task.BatchID {
				return ordered[i].task.Sequence < ordered[j].task.Sequence
			}
			return ordered[i].task.BatchID < ordered[j].task.BatchID
		}
		return ordered[i].task.AvailableAt.Before(ordered[j].task.AvailableAt)
	})
	selected := ordered[0]
	selected.state = taskRunning
	selected.lockOwner = owner
	selected.lockedUntil = now.Add(lease)
	selected.task.Attempt++
	return cloneStoredTask(selected.task), nil
}

func (store *MemoryBatchStore) RetryTask(
	_ context.Context,
	taskID string,
	owner string,
	availableAt time.Time,
	lastError string,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, exists := store.tasks[taskID]
	if !exists || task.state != taskRunning || task.lockOwner != owner {
		return errors.New("REST batch task claim lost")
	}
	task.state, task.lockOwner, task.lastError = taskPending, "", lastError
	task.lockedUntil = time.Time{}
	task.task.AvailableAt = availableAt
	return nil
}

func (store *MemoryBatchStore) FinishTask(
	_ context.Context,
	taskID string,
	owner string,
	result TaskResult,
	_ time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, exists := store.tasks[taskID]
	if !exists || task.state != taskRunning || task.lockOwner != owner {
		return errors.New("REST batch task claim lost")
	}
	task.state, task.lockOwner = taskDone, ""
	task.lockedUntil = time.Time{}
	if result.CallbackURL == "" {
		task.callbackState = callbackDone
		return nil
	}
	status := 0
	if result.Successful {
		status = 1
	}
	task.callback = StoredCallback{
		TaskID: taskID, BatchID: task.task.BatchID, Destination: task.task.Destination,
		URL: result.CallbackURL, Status: status, StatusText: result.StatusText,
	}
	task.callbackState = callbackPending
	return nil
}

func (store *MemoryBatchStore) ClaimCallback(
	_ context.Context,
	owner string,
	now time.Time,
	lease time.Duration,
) (StoredCallback, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	ordered := make([]*memoryTask, 0, len(store.tasks))
	for _, task := range store.tasks {
		if task.callbackState == callbackPending && !task.task.AvailableAt.After(now) {
			ordered = append(ordered, task)
		}
	}
	if len(ordered) == 0 {
		return StoredCallback{}, ErrNoBatchWork
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].task.BatchID == ordered[j].task.BatchID {
			return ordered[i].task.Sequence < ordered[j].task.Sequence
		}
		return ordered[i].task.BatchID < ordered[j].task.BatchID
	})
	selected := ordered[0]
	selected.callbackState = callbackRunning
	selected.callbackOwner = owner
	selected.callbackUntil = now.Add(lease)
	selected.callback.Attempt++
	return selected.callback, nil
}

func (store *MemoryBatchStore) RetryCallback(
	_ context.Context,
	taskID string,
	owner string,
	availableAt time.Time,
	lastError string,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, exists := store.tasks[taskID]
	if !exists || task.callbackState != callbackRunning || task.callbackOwner != owner {
		return errors.New("REST callback claim lost")
	}
	task.callbackState, task.callbackOwner, task.lastError = callbackPending, "", lastError
	task.callbackUntil = time.Time{}
	task.task.AvailableAt = availableAt
	return nil
}

func (store *MemoryBatchStore) FinishCallback(
	_ context.Context,
	taskID string,
	owner string,
	_ time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, exists := store.tasks[taskID]
	if !exists || task.callbackState != callbackRunning || task.callbackOwner != owner {
		return errors.New("REST callback claim lost")
	}
	task.callbackState, task.callbackOwner = callbackDone, ""
	task.callbackUntil = time.Time{}
	return nil
}

func cloneStoredTask(task StoredTask) StoredTask {
	task.CredentialDigest = append([]byte(nil), task.CredentialDigest...)
	task.Body = append([]byte(nil), task.Body...)
	return task
}
