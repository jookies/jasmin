package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pumpitspace/jasmin/internal/transport/restcompat"
)

//go:embed migrations/0005_rest_batches.sql
var restBatchMigrations embed.FS

// PostgresRESTBatchStore is the production sendbatch task/callback queue.
// Claims use SKIP LOCKED leases, permitting active-passive recovery and
// horizontal worker scaling without executing the same live claim twice.
type PostgresRESTBatchStore struct {
	db *sql.DB
}

func OpenPostgresRESTBatchStore(ctx context.Context, dsn string) (*PostgresRESTBatchStore, error) {
	if dsn == "" {
		return nil, errors.New("empty PostgreSQL DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect PostgreSQL REST batch store: %w", err)
	}
	return &PostgresRESTBatchStore{db: db}, nil
}

func NewPostgresRESTBatchStore(db *sql.DB) (*PostgresRESTBatchStore, error) {
	if db == nil {
		return nil, errors.New("nil PostgreSQL database")
	}
	return &PostgresRESTBatchStore{db: db}, nil
}

func (store *PostgresRESTBatchStore) Migrate(ctx context.Context) error {
	migration, err := restBatchMigrations.ReadFile("migrations/0005_rest_batches.sql")
	if err != nil {
		return err
	}
	if _, err = store.db.ExecContext(ctx, string(migration)); err != nil {
		return fmt.Errorf("migrate PostgreSQL REST batch store: %w", err)
	}
	return nil
}

func (store *PostgresRESTBatchStore) Ping(ctx context.Context) error {
	return store.db.PingContext(ctx)
}

func (store *PostgresRESTBatchStore) Close() error {
	return store.db.Close()
}

func (store *PostgresRESTBatchStore) CreateBatch(
	ctx context.Context,
	batch restcompat.StoredBatch,
	maxPending int,
) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize only the backlog count+insert admission boundary. Workers keep
	// claiming concurrently and no process-scoped in-memory count can drift.
	if _, err = tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('jasmin-rest-batch-admission', 0))`); err != nil {
		return err
	}
	if maxPending > 0 {
		var pending int
		if err = tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM rest_batch_tasks
			 WHERE state <> 'DONE' OR callback_state IN ('PENDING','RUNNING')`).Scan(&pending); err != nil {
			return err
		}
		if pending+len(batch.Tasks) > maxPending {
			return restcompat.ErrBatchQueueFull
		}
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO rest_batches(batch_id,accepted_at,callback_url,errback_url,task_count)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(batch_id) DO NOTHING`,
		batch.ID, batch.AcceptedAt.UTC(), batch.CallbackURL, batch.ErrbackURL, len(batch.Tasks))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		// Replaying the same admission is idempotent. The first transaction
		// wrote all tasks atomically, so there is nothing to repair here.
		return tx.Commit()
	}
	for _, task := range batch.Tasks {
		if len(task.CredentialDigest) != 32 {
			return fmt.Errorf("REST batch task %q credential digest must be 32 bytes", task.ID)
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO rest_batch_tasks(
				task_id,batch_id,task_sequence,destination,username,
				credential_digest,request_body,available_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			task.ID, batch.ID, task.Sequence, task.Destination, task.Username,
			task.CredentialDigest, task.Body, task.AvailableAt.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *PostgresRESTBatchStore) RecoverExpired(ctx context.Context, now time.Time) (int64, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	taskResult, err := tx.ExecContext(ctx, `
		UPDATE rest_batch_tasks
		SET state='PENDING', lock_owner=NULL, locked_until=NULL,
		    available_at=LEAST(available_at,$1)
		WHERE state='RUNNING' AND (locked_until IS NULL OR locked_until <= $1)`, now.UTC())
	if err != nil {
		return 0, err
	}
	callbackResult, err := tx.ExecContext(ctx, `
		UPDATE rest_batch_tasks
		SET callback_state='PENDING', callback_lock_owner=NULL,
		    callback_locked_until=NULL,
		    callback_available_at=LEAST(callback_available_at,$1)
		WHERE callback_state='RUNNING'
		  AND (callback_locked_until IS NULL OR callback_locked_until <= $1)`, now.UTC())
	if err != nil {
		return 0, err
	}
	taskCount, err := taskResult.RowsAffected()
	if err != nil {
		return 0, err
	}
	callbackCount, err := callbackResult.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return taskCount + callbackCount, nil
}

func (store *PostgresRESTBatchStore) ClaimTask(
	ctx context.Context,
	owner string,
	now time.Time,
	lease time.Duration,
) (restcompat.StoredTask, error) {
	var task restcompat.StoredTask
	err := store.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT task_id
			FROM rest_batch_tasks
			WHERE state='PENDING' AND available_at <= $2
			ORDER BY available_at, batch_id, task_sequence
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE rest_batch_tasks AS task
		SET state='RUNNING', lock_owner=$1, locked_until=$3,
		    attempts=task.attempts+1
		FROM candidate, rest_batches AS batch
		WHERE task.task_id=candidate.task_id AND batch.batch_id=task.batch_id
		RETURNING task.task_id,task.batch_id,task.task_sequence,task.destination,
		          task.username,task.credential_digest,task.request_body,
		          task.available_at,task.attempts,batch.callback_url,batch.errback_url`,
		owner, now.UTC(), now.Add(lease).UTC()).Scan(
		&task.ID, &task.BatchID, &task.Sequence, &task.Destination,
		&task.Username, &task.CredentialDigest, &task.Body,
		&task.AvailableAt, &task.Attempt, &task.CallbackURL, &task.ErrbackURL,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return restcompat.StoredTask{}, restcompat.ErrNoBatchWork
	}
	return task, err
}

func (store *PostgresRESTBatchStore) RetryTask(
	ctx context.Context,
	taskID string,
	owner string,
	availableAt time.Time,
	lastError string,
) error {
	result, err := store.db.ExecContext(ctx, `
		UPDATE rest_batch_tasks
		SET state='PENDING',available_at=$3,last_error=$4,
		    lock_owner=NULL,locked_until=NULL
		WHERE task_id=$1 AND state='RUNNING' AND lock_owner=$2`,
		taskID, owner, availableAt.UTC(), lastError)
	return claimedUpdate(result, err, "REST batch task")
}

func (store *PostgresRESTBatchStore) FinishTask(
	ctx context.Context,
	taskID string,
	owner string,
	result restcompat.TaskResult,
	completedAt time.Time,
) error {
	callbackState := "DONE"
	var callbackStatus any
	var callbackAvailable any
	if result.CallbackURL != "" {
		callbackState = "PENDING"
		if result.Successful {
			callbackStatus = 1
		} else {
			callbackStatus = 0
		}
		callbackAvailable = completedAt.UTC()
	}
	update, err := store.db.ExecContext(ctx, `
		UPDATE rest_batch_tasks
		SET state='DONE',successful=$3,response_status=$4,status_text=$5,
		    completed_at=$6,lock_owner=NULL,locked_until=NULL,
		    callback_state=$7,callback_url=$8,callback_status=$9,
		    callback_available_at=$10
		WHERE task_id=$1 AND state='RUNNING' AND lock_owner=$2`,
		taskID, owner, result.Successful, result.HTTPStatus, result.StatusText,
		completedAt.UTC(), callbackState, result.CallbackURL, callbackStatus, callbackAvailable)
	return claimedUpdate(update, err, "REST batch task")
}

func (store *PostgresRESTBatchStore) ClaimCallback(
	ctx context.Context,
	owner string,
	now time.Time,
	lease time.Duration,
) (restcompat.StoredCallback, error) {
	var callback restcompat.StoredCallback
	err := store.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT task_id
			FROM rest_batch_tasks
			WHERE callback_state='PENDING' AND callback_available_at <= $2
			ORDER BY callback_available_at, batch_id, task_sequence
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE rest_batch_tasks AS task
		SET callback_state='RUNNING',callback_lock_owner=$1,
		    callback_locked_until=$3,
		    callback_attempts=task.callback_attempts+1
		FROM candidate
		WHERE task.task_id=candidate.task_id
		RETURNING task.task_id,task.batch_id,task.destination,task.callback_url,
		          task.callback_status,task.status_text,task.callback_attempts`,
		owner, now.UTC(), now.Add(lease).UTC()).Scan(
		&callback.TaskID, &callback.BatchID, &callback.Destination, &callback.URL,
		&callback.Status, &callback.StatusText, &callback.Attempt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return restcompat.StoredCallback{}, restcompat.ErrNoBatchWork
	}
	return callback, err
}

func (store *PostgresRESTBatchStore) RetryCallback(
	ctx context.Context,
	taskID string,
	owner string,
	availableAt time.Time,
	lastError string,
) error {
	result, err := store.db.ExecContext(ctx, `
		UPDATE rest_batch_tasks
		SET callback_state='PENDING',callback_available_at=$3,last_error=$4,
		    callback_lock_owner=NULL,callback_locked_until=NULL
		WHERE task_id=$1 AND callback_state='RUNNING' AND callback_lock_owner=$2`,
		taskID, owner, availableAt.UTC(), lastError)
	return claimedUpdate(result, err, "REST callback")
}

func (store *PostgresRESTBatchStore) FinishCallback(
	ctx context.Context,
	taskID string,
	owner string,
	completedAt time.Time,
) error {
	result, err := store.db.ExecContext(ctx, `
		UPDATE rest_batch_tasks
		SET callback_state='DONE',callback_completed_at=$3,
		    callback_lock_owner=NULL,callback_locked_until=NULL
		WHERE task_id=$1 AND callback_state='RUNNING' AND callback_lock_owner=$2`,
		taskID, owner, completedAt.UTC())
	return claimedUpdate(result, err, "REST callback")
}

func claimedUpdate(result sql.Result, err error, kind string) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%s claim lost", kind)
	}
	return nil
}

var _ restcompat.BatchStore = (*PostgresRESTBatchStore)(nil)
