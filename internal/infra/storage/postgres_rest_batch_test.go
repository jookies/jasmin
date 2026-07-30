package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/restcompat"
)

func TestPostgresRESTBatchStoreLifecycleAndRecovery(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	store, err := OpenPostgresRESTBatchStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}

	batchID := "rest-store-test-" + time.Now().UTC().Format("20060102150405.000000000")
	taskID := batchID + "-task"
	defer func() {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM rest_batch_tasks WHERE task_id=$1`, taskID)
		_, _ = store.db.ExecContext(ctx, `DELETE FROM rest_batches WHERE batch_id=$1`, batchID)
	}()
	digest := sha256.Sum256([]byte("secret-password-must-not-be-stored"))
	now := time.Now().UTC()
	batch := restcompat.StoredBatch{
		ID: batchID, AcceptedAt: now, CallbackURL: "https://callback.invalid/progress",
		Tasks: []restcompat.StoredTask{{
			ID: taskID, BatchID: batchID, Sequence: 1, Destination: "15551234567",
			Username: "alice", CredentialDigest: digest[:],
			Body:        []byte(`{"username":"alice","to":"15551234567","content":"hi"}`),
			AvailableAt: now,
		}},
	}
	if err = store.CreateBatch(ctx, batch, 0); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBatch(ctx, batch, 0); err != nil {
		t.Fatalf("idempotent admission: %v", err)
	}
	var body []byte
	if err = store.db.QueryRowContext(ctx,
		`SELECT request_body FROM rest_batch_tasks WHERE task_id=$1`, taskID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("secret-password")) {
		t.Fatalf("durable request leaked password: %s", body)
	}

	task, err := store.ClaimTask(ctx, "worker-a", now.Add(time.Second), time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != taskID || task.Attempt != 1 || !bytes.Equal(task.CredentialDigest, digest[:]) {
		t.Fatalf("claimed task = %+v", task)
	}
	if _, err = store.RecoverExpired(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	task, err = store.ClaimTask(ctx, "worker-b", now.Add(2*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if task.Attempt != 2 {
		t.Fatalf("recovered attempt = %d", task.Attempt)
	}
	if err = store.FinishTask(ctx, task.ID, "worker-b", restcompat.TaskResult{
		Successful: true, HTTPStatus: 200, StatusText: `Success "id"`,
		CallbackURL: batch.CallbackURL,
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimTask(ctx, "worker-c", now.Add(4*time.Second), time.Minute); !errors.Is(err, restcompat.ErrNoBatchWork) {
		t.Fatalf("terminal task reclaimed: %v", err)
	}
	callback, err := store.ClaimCallback(ctx, "callback-a", now.Add(4*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if callback.TaskID != taskID || callback.BatchID != batchID ||
		callback.Status != 1 || callback.StatusText != `Success "id"` {
		t.Fatalf("callback = %+v", callback)
	}
	if err = store.FinishCallback(ctx, taskID, "callback-a", now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimCallback(ctx, "callback-b", now.Add(6*time.Second), time.Minute); !errors.Is(err, restcompat.ErrNoBatchWork) {
		t.Fatalf("terminal callback reclaimed: %v", err)
	}
}
