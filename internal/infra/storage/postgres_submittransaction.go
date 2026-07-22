package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
)

//go:embed migrations/0001_submit_transaction.sql
var submitTransactionMigrations embed.FS

type PostgresSubmitTransactionRepository struct{ db *sql.DB }

func NewPostgresSubmitTransactionRepository(db *sql.DB) (*PostgresSubmitTransactionRepository, error) {
	if db == nil {
		return nil, errors.New("nil PostgreSQL database")
	}
	return &PostgresSubmitTransactionRepository{db: db}, nil
}
func OpenPostgresSubmitTransactionRepository(ctx context.Context, dsn string) (*PostgresSubmitTransactionRepository, error) {
	if dsn == "" {
		return nil, errors.New("empty PostgreSQL DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect PostgreSQL submit store: %w", err)
	}
	return &PostgresSubmitTransactionRepository{db: db}, nil
}
func (r *PostgresSubmitTransactionRepository) Close() error                { return r.db.Close() }
func (r *PostgresSubmitTransactionRepository) ProductionSubmitRepository() {}

func (r *PostgresSubmitTransactionRepository) BillingApplied(ctx context.Context, eventKey string) (bool, error) {
	var applied bool
	err := r.db.QueryRowContext(ctx, `SELECT applied_at IS NOT NULL FROM submit_billing_intents WHERE event_key=$1`, eventKey).Scan(&applied)
	return applied, err
}

func (r *PostgresSubmitTransactionRepository) MarkBillingApplied(ctx context.Context, eventKey string, appliedAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE submit_billing_intents SET applied_at=COALESCE(applied_at,$2) WHERE event_key=$1`, eventKey, appliedAt.UTC())
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("billing intent %q not found", eventKey)
	}
	return nil
}

func (r *PostgresSubmitTransactionRepository) Migrate(ctx context.Context) error {
	migration, err := submitTransactionMigrations.ReadFile("migrations/0001_submit_transaction.sql")
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, string(migration))
	return err
}

func (r *PostgresSubmitTransactionRepository) Admit(ctx context.Context, parts []submittransaction.LogicalPart, events []submittransaction.OutboxEvent) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range parts {
		_, err = tx.ExecContext(ctx, `INSERT INTO submit_parts(part_key,message_id,part_number,connector_id,user_id,bill_id,state,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(part_key) DO NOTHING`, p.Key, p.MessageID, p.PartNumber, p.ConnectorID, p.UserID, p.BillID, p.State, p.CreatedAt)
		if err != nil {
			return err
		}
	}
	for _, event := range events {
		if err = insertPostgresEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func insertPostgresEvent(ctx context.Context, tx *sql.Tx, event submittransaction.OutboxEvent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO submit_outbox(event_key,part_key,kind,exchange_name,routing_key,payload,created_at,available_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(event_key) DO NOTHING`, event.Key, event.PartKey, event.Kind, event.Exchange, event.RoutingKey, event.Payload, event.CreatedAt, event.AvailableAt)
	if err == nil && event.Kind == submittransaction.EventLateBilling {
		_, err = tx.ExecContext(ctx, `INSERT INTO submit_billing_intents(event_key,part_key) VALUES($1,$2) ON CONFLICT(event_key) DO NOTHING`, event.Key, event.PartKey)
	}
	return err
}

func (r *PostgresSubmitTransactionRepository) BeginAttempt(ctx context.Context, partKey string, now time.Time) (submittransaction.SendAttempt, bool, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM submit_parts WHERE part_key=$1 FOR UPDATE`, partKey).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return submittransaction.SendAttempt{}, false, submittransaction.ErrPartNotFound
	} else if err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	if state == string(submittransaction.PartResultCommitted) {
		var a submittransaction.SendAttempt
		err = tx.QueryRowContext(ctx, `SELECT id,attempt_number,state,created_at,sent_at,resolved_at FROM submit_attempts WHERE part_key=$1 ORDER BY attempt_number DESC LIMIT 1`, partKey).Scan(&a.ID, &a.Number, &a.State, &a.CreatedAt, &a.SentAt, &a.ResolvedAt)
		a.PartKey = partKey
		if err != nil {
			return a, false, err
		}
		return a, true, tx.Commit()
	}
	if state == string(submittransaction.PartAttempting) {
		var a submittransaction.SendAttempt
		err = tx.QueryRowContext(ctx, `SELECT id,attempt_number,state,created_at,sent_at,resolved_at FROM submit_attempts WHERE part_key=$1 AND state IN ($2,$3) ORDER BY attempt_number DESC LIMIT 1`, partKey, submittransaction.AttemptIntent, submittransaction.AttemptSent).Scan(&a.ID, &a.Number, &a.State, &a.CreatedAt, &a.SentAt, &a.ResolvedAt)
		a.PartKey = partKey
		if err != nil {
			return a, false, err
		}
		return a, false, tx.Commit()
	}
	var number int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_number),0)+1 FROM submit_attempts WHERE part_key=$1`, partKey).Scan(&number); err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	var id int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO submit_attempts(part_key,attempt_number,state,created_at) VALUES($1,$2,$3,$4) RETURNING id`, partKey, number, submittransaction.AttemptIntent, now).Scan(&id); err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE submit_parts SET state=$1 WHERE part_key=$2`, submittransaction.PartAttempting, partKey); err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	return submittransaction.SendAttempt{ID: id, PartKey: partKey, Number: number, State: submittransaction.AttemptIntent, CreatedAt: now}, false, tx.Commit()
}
func (r *PostgresSubmitTransactionRepository) MarkAttemptSent(ctx context.Context, id int64, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE submit_attempts SET state=$1,sent_at=$2 WHERE id=$3 AND state=$4`, submittransaction.AttemptSent, now, id, submittransaction.AttemptIntent)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return submittransaction.ErrAttemptNotFound
	}
	return nil
}
func (r *PostgresSubmitTransactionRepository) RecoverUnresolved(ctx context.Context, now time.Time) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE submit_attempts SET state=$1,resolved_at=$2 WHERE state IN ($3,$4)`, submittransaction.AttemptUnknownAfterSend, now, submittransaction.AttemptIntent, submittransaction.AttemptSent)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE submit_parts p SET state=$1 WHERE p.state=$2 AND EXISTS(SELECT 1 FROM submit_attempts a WHERE a.part_key=p.part_key AND a.state=$3)`, submittransaction.PartUnknownAfterSend, submittransaction.PartAttempting, submittransaction.AttemptUnknownAfterSend); err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return count, tx.Commit()
}
func (r *PostgresSubmitTransactionRepository) CommitResult(ctx context.Context, commit submittransaction.ResultCommit) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO submit_results(part_key,attempt_id,kind,smpp_status,smsc_message_id,committed_at) VALUES($1,$2,$3,$4,NULLIF($5,''),$6) ON CONFLICT(attempt_id) DO NOTHING`, commit.Result.PartKey, commit.Result.AttemptID, commit.Result.Kind, commit.Result.SMPPStatus, commit.Result.SMSCMessageID, commit.Result.CommittedAt)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE submit_attempts SET state=$1,resolved_at=$2 WHERE id=$3 AND part_key=$4`, submittransaction.AttemptResultCommitted, commit.Result.CommittedAt, commit.Result.AttemptID, commit.Result.PartKey); err != nil {
		return false, err
	}
	partState := submittransaction.PartResultCommitted
	if commit.Result.Kind == submittransaction.ResultRetry {
		partState = submittransaction.PartPending
	}
	if _, err = tx.ExecContext(ctx, `UPDATE submit_parts SET state=$1 WHERE part_key=$2`, partState, commit.Result.PartKey); err != nil {
		return false, err
	}
	for _, event := range commit.Events {
		if err = insertPostgresEvent(ctx, tx, event); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (r *PostgresSubmitTransactionRepository) ClaimOutbox(ctx context.Context, owner string, limit int, now time.Time, lease time.Duration) ([]submittransaction.OutboxEvent, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT event_key,part_key,kind,exchange_name,routing_key,payload,created_at,available_at,attempts FROM submit_outbox WHERE dispatched_at IS NULL AND available_at<=$1 AND (locked_until IS NULL OR locked_until<=$1) ORDER BY created_at,event_key FOR UPDATE SKIP LOCKED LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	var events []submittransaction.OutboxEvent
	for rows.Next() {
		var event submittransaction.OutboxEvent
		if err = rows.Scan(&event.Key, &event.PartKey, &event.Kind, &event.Exchange, &event.RoutingKey, &event.Payload, &event.CreatedAt, &event.AvailableAt, &event.Attempts); err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, event := range events {
		if _, err = tx.ExecContext(ctx, `UPDATE submit_outbox SET lock_owner=$1,locked_until=$2,attempts=attempts+1 WHERE event_key=$3`, owner, now.Add(lease), event.Key); err != nil {
			return nil, err
		}
	}
	return events, tx.Commit()
}
func (r *PostgresSubmitTransactionRepository) MarkOutboxDispatched(ctx context.Context, key, owner string, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE submit_outbox SET dispatched_at=$1,lock_owner=NULL,locked_until=NULL,last_error=NULL WHERE event_key=$2 AND lock_owner=$3`, now, key, owner)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("outbox event %q is not owned", key)
	}
	return nil
}
func (r *PostgresSubmitTransactionRepository) ReleaseOutbox(ctx context.Context, key, owner string, now time.Time, cause error) error {
	_, err := r.db.ExecContext(ctx, `UPDATE submit_outbox SET lock_owner=NULL,locked_until=NULL,available_at=$1,last_error=$2 WHERE event_key=$3 AND lock_owner=$4`, now, cause.Error(), key, owner)
	return err
}

var _ submittransaction.ProductionRepository = (*PostgresSubmitTransactionRepository)(nil)
