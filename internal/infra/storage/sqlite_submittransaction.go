package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
)

// SQLiteSubmitTransactionRepository is a unit/local projection only. It does
// not implement submittransaction.ProductionRepository.
type SQLiteSubmitTransactionRepository struct{ db *sql.DB }

func NewSQLiteSubmitTransactionRepository(db *sql.DB) (*SQLiteSubmitTransactionRepository, error) {
	if db == nil {
		return nil, errors.New("nil SQLite database")
	}
	return &SQLiteSubmitTransactionRepository{db: db}, nil
}

func (r *SQLiteSubmitTransactionRepository) Init(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, sqliteSubmitTransactionSchema+sqliteCDRSchema)
	return err
}

func (r *SQLiteSubmitTransactionRepository) BillingApplied(ctx context.Context, eventKey string) (bool, error) {
	var applied bool
	err := r.db.QueryRowContext(ctx, `SELECT applied_at IS NOT NULL FROM submit_billing_intents WHERE event_key=?`, eventKey).Scan(&applied)
	return applied, err
}

func (r *SQLiteSubmitTransactionRepository) MarkBillingApplied(ctx context.Context, eventKey string, appliedAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE submit_billing_intents SET applied_at=COALESCE(applied_at,?) WHERE event_key=?`, nanos(appliedAt.UTC()), eventKey)
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
	if err = recordSQLiteLateBillingOutcome(ctx, tx, eventKey, cdr.BillingApplied, appliedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLiteSubmitTransactionRepository) MarkBillingRejected(ctx context.Context, eventKey string, rejectedAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM submit_billing_intents WHERE event_key=?`, eventKey).Scan(&exists); err != nil {
		return err
	}
	if err = recordSQLiteLateBillingOutcome(ctx, tx, eventKey, cdr.BillingRejected, rejectedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

const sqliteSubmitTransactionSchema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS submit_parts (
 part_key TEXT PRIMARY KEY, message_id TEXT NOT NULL, part_number INTEGER NOT NULL,
 connector_id TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT '', bill_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL, created_at INTEGER NOT NULL,
 UNIQUE(message_id, part_number)
);
CREATE TABLE IF NOT EXISTS submit_attempts (
 id INTEGER PRIMARY KEY AUTOINCREMENT, part_key TEXT NOT NULL REFERENCES submit_parts(part_key),
 attempt_number INTEGER NOT NULL, state TEXT NOT NULL, created_at INTEGER NOT NULL,
 sent_at INTEGER, resolved_at INTEGER, UNIQUE(part_key, attempt_number)
);
CREATE TABLE IF NOT EXISTS submit_results (
 attempt_id INTEGER PRIMARY KEY REFERENCES submit_attempts(id), part_key TEXT NOT NULL REFERENCES submit_parts(part_key),
 kind TEXT NOT NULL, smpp_status TEXT NOT NULL DEFAULT '', smsc_message_id TEXT,
 committed_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS submit_results_final_part ON submit_results(part_key) WHERE kind <> 'RETRY';
DROP INDEX IF EXISTS submit_results_smsc_id;
CREATE INDEX IF NOT EXISTS submit_results_smsc_id_lookup ON submit_results(smsc_message_id) WHERE smsc_message_id IS NOT NULL AND smsc_message_id <> '';
CREATE TABLE IF NOT EXISTS submit_outbox (
 event_key TEXT PRIMARY KEY, part_key TEXT NOT NULL REFERENCES submit_parts(part_key), kind TEXT NOT NULL,
 exchange_name TEXT NOT NULL, routing_key TEXT NOT NULL, payload BLOB NOT NULL,
 created_at INTEGER NOT NULL, available_at INTEGER NOT NULL, attempts INTEGER NOT NULL DEFAULT 0,
 dispatched_at INTEGER, lock_owner TEXT, locked_until INTEGER, last_error TEXT
);
CREATE INDEX IF NOT EXISTS submit_outbox_pending ON submit_outbox(dispatched_at, available_at, created_at, event_key);
CREATE INDEX IF NOT EXISTS submit_outbox_part_order_pending ON submit_outbox(part_key, created_at, event_key) WHERE dispatched_at IS NULL;
CREATE TABLE IF NOT EXISTS submit_billing_intents (
 event_key TEXT PRIMARY KEY REFERENCES submit_outbox(event_key), part_key TEXT NOT NULL REFERENCES submit_parts(part_key),
 applied_at INTEGER
);`

func nanos(value time.Time) int64 { return value.UTC().UnixNano() }
func optionalNanos(value *time.Time) any {
	if value == nil {
		return nil
	}
	return nanos(*value)
}

func (r *SQLiteSubmitTransactionRepository) Admit(ctx context.Context, parts []submittransaction.LogicalPart, events []submittransaction.OutboxEvent) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, part := range parts {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO submit_parts(part_key,message_id,part_number,connector_id,user_id,bill_id,state,created_at) VALUES(?,?,?,?,?,?,?,?)`, part.Key, part.MessageID, part.PartNumber, part.ConnectorID, part.UserID, part.BillID, part.State, nanos(part.CreatedAt))
		if err != nil {
			return err
		}
		if err = insertSQLiteCDRAdmission(ctx, tx, part.CDRAdmission()); err != nil {
			return err
		}
	}
	for _, event := range events {
		if err = insertSQLiteEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *SQLiteSubmitTransactionRepository) AggregateStatus(ctx context.Context, messageID string) (submittransaction.AggregateStatus, error) {
	status := submittransaction.AggregateStatus{MessageID: messageID}
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*),
 SUM(CASE WHEN state=? THEN 1 ELSE 0 END),
 SUM(CASE WHEN state=? THEN 1 ELSE 0 END),
 SUM(CASE WHEN state=? THEN 1 ELSE 0 END),
 SUM(CASE WHEN state=? THEN 1 ELSE 0 END)
 FROM submit_parts WHERE message_id=?`,
		submittransaction.PartPending, submittransaction.PartAttempting,
		submittransaction.PartUnknownAfterSend, submittransaction.PartResultCommitted, messageID,
	).Scan(&status.TotalParts, &status.Pending, &status.Attempting, &status.UnknownAfterSend, &status.ResultCommitted)
	if err != nil {
		return submittransaction.AggregateStatus{}, err
	}
	if status.TotalParts == 0 {
		return submittransaction.AggregateStatus{}, submittransaction.ErrPartNotFound
	}
	status.State = status.DerivedState()
	return status, nil
}

func insertSQLiteEvent(ctx context.Context, tx *sql.Tx, event submittransaction.OutboxEvent) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO submit_outbox(event_key,part_key,kind,exchange_name,routing_key,payload,created_at,available_at) VALUES(?,?,?,?,?,?,?,?)`, event.Key, event.PartKey, event.Kind, event.Exchange, event.RoutingKey, event.Payload, nanos(event.CreatedAt), nanos(event.AvailableAt))
	if err == nil && event.Kind == submittransaction.EventLateBilling {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO submit_billing_intents(event_key,part_key) VALUES(?,?)`, event.Key, event.PartKey)
	}
	return err
}

func (r *SQLiteSubmitTransactionRepository) BeginAttempt(ctx context.Context, partKey string, now time.Time) (submittransaction.SendAttempt, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM submit_parts WHERE part_key=?`, partKey).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return submittransaction.SendAttempt{}, false, submittransaction.ErrPartNotFound
	} else if err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	if state == string(submittransaction.PartResultCommitted) {
		var attempt submittransaction.SendAttempt
		var created int64
		var sent, resolved sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT id,attempt_number,state,created_at,sent_at,resolved_at FROM submit_attempts WHERE part_key=? ORDER BY attempt_number DESC LIMIT 1`, partKey).Scan(&attempt.ID, &attempt.Number, &attempt.State, &created, &sent, &resolved)
		if err != nil {
			return submittransaction.SendAttempt{}, false, err
		}
		attempt.PartKey = partKey
		attempt.CreatedAt = time.Unix(0, created).UTC()
		attempt.SentAt = timePtr(sent)
		attempt.ResolvedAt = timePtr(resolved)
		return attempt, true, tx.Commit()
	}
	if state == string(submittransaction.PartAttempting) {
		var attempt submittransaction.SendAttempt
		var created int64
		var sent, resolved sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT id,attempt_number,state,created_at,sent_at,resolved_at FROM submit_attempts WHERE part_key=? AND state IN (?,?) ORDER BY attempt_number DESC LIMIT 1`, partKey, submittransaction.AttemptIntent, submittransaction.AttemptSent).Scan(&attempt.ID, &attempt.Number, &attempt.State, &created, &sent, &resolved)
		if err != nil {
			return submittransaction.SendAttempt{}, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE submit_attempts SET state=?,resolved_at=? WHERE id=? AND state IN (?,?)`, submittransaction.AttemptUnknownAfterSend, nanos(now), attempt.ID, submittransaction.AttemptIntent, submittransaction.AttemptSent); err != nil {
			return submittransaction.SendAttempt{}, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE submit_parts SET state=? WHERE part_key=? AND state=?`, submittransaction.PartUnknownAfterSend, partKey, submittransaction.PartAttempting); err != nil {
			return submittransaction.SendAttempt{}, false, err
		}
		if err = recordSQLiteCDRTransition(ctx, tx, cdr.Event{
			Key: cdr.UnknownEventKey(partKey, attempt.ID), CDRID: partKey,
			Kind: cdr.EventUnknownAfterSend, State: cdr.StateUnknownAfterSend,
			AttemptID: attempt.ID, OccurredAt: now,
		}); err != nil {
			return submittransaction.SendAttempt{}, false, err
		}
		if err = tx.Commit(); err != nil {
			return submittransaction.SendAttempt{}, false, err
		}
		return submittransaction.SendAttempt{}, false, submittransaction.ErrAttemptFenced
	}
	var number int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_number),0)+1 FROM submit_attempts WHERE part_key=?`, partKey).Scan(&number); err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO submit_attempts(part_key,attempt_number,state,created_at) VALUES(?,?,?,?)`, partKey, number, submittransaction.AttemptIntent, nanos(now))
	if err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE submit_parts SET state=? WHERE part_key=?`, submittransaction.PartAttempting, partKey); err != nil {
		return submittransaction.SendAttempt{}, false, err
	}
	return submittransaction.SendAttempt{ID: id, PartKey: partKey, Number: number, State: submittransaction.AttemptIntent, CreatedAt: now.UTC()}, false, tx.Commit()
}

func timePtr(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(0, value.Int64).UTC()
	return &result
}

func (r *SQLiteSubmitTransactionRepository) MarkAttemptSent(ctx context.Context, attemptID int64, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE submit_attempts SET state=?,sent_at=? WHERE id=? AND state=?`, submittransaction.AttemptSent, nanos(now), attemptID, submittransaction.AttemptIntent)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return submittransaction.ErrAttemptNotFound
	}
	return nil
}

func (r *SQLiteSubmitTransactionRepository) MarkAttemptUnknownAfterSend(ctx context.Context, attemptID int64, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE submit_attempts SET state=?,resolved_at=? WHERE id=? AND state IN (?,?)`, submittransaction.AttemptUnknownAfterSend, nanos(now), attemptID, submittransaction.AttemptIntent, submittransaction.AttemptSent)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return submittransaction.ErrAttemptNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE submit_parts SET state=? WHERE state=? AND part_key=(SELECT part_key FROM submit_attempts WHERE id=?)`, submittransaction.PartUnknownAfterSend, submittransaction.PartAttempting, attemptID); err != nil {
		return err
	}
	var partKey string
	if err = tx.QueryRowContext(ctx, `SELECT part_key FROM submit_attempts WHERE id=?`, attemptID).Scan(&partKey); err != nil {
		return err
	}
	if err = recordSQLiteCDRTransition(ctx, tx, cdr.Event{
		Key: cdr.UnknownEventKey(partKey, attemptID), CDRID: partKey,
		Kind: cdr.EventUnknownAfterSend, State: cdr.StateUnknownAfterSend,
		AttemptID: attemptID, OccurredAt: now,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLiteSubmitTransactionRepository) RecoverUnresolved(ctx context.Context, now time.Time) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,part_key FROM submit_attempts WHERE state IN (?,?) ORDER BY id`, submittransaction.AttemptIntent, submittransaction.AttemptSent)
	if err != nil {
		return 0, err
	}
	type unresolvedAttempt struct {
		id      int64
		partKey string
	}
	var unresolved []unresolvedAttempt
	for rows.Next() {
		var attempt unresolvedAttempt
		if err = rows.Scan(&attempt.id, &attempt.partKey); err != nil {
			rows.Close()
			return 0, err
		}
		unresolved = append(unresolved, attempt)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE submit_attempts SET state=?,resolved_at=? WHERE state IN (?,?)`, submittransaction.AttemptUnknownAfterSend, nanos(now), submittransaction.AttemptIntent, submittransaction.AttemptSent)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE submit_parts SET state=? WHERE state=? AND EXISTS(SELECT 1 FROM submit_attempts a WHERE a.part_key=submit_parts.part_key AND a.state=?)`, submittransaction.PartUnknownAfterSend, submittransaction.PartAttempting, submittransaction.AttemptUnknownAfterSend); err != nil {
		return 0, err
	}
	for _, attempt := range unresolved {
		if err = recordSQLiteCDRTransition(ctx, tx, cdr.Event{
			Key: cdr.UnknownEventKey(attempt.partKey, attempt.id), CDRID: attempt.partKey,
			Kind: cdr.EventUnknownAfterSend, State: cdr.StateUnknownAfterSend,
			AttemptID: attempt.id, OccurredAt: now,
		}); err != nil {
			return 0, err
		}
	}
	count, _ := result.RowsAffected()
	return count, tx.Commit()
}

func (r *SQLiteSubmitTransactionRepository) CommitResult(ctx context.Context, commit submittransaction.ResultCommit) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var attemptPart string
	if err = tx.QueryRowContext(ctx, `SELECT part_key FROM submit_attempts WHERE id=? AND part_key=?`, commit.Result.AttemptID, commit.Result.PartKey).Scan(&attemptPart); errors.Is(err, sql.ErrNoRows) {
		return false, submittransaction.ErrAttemptNotFound
	} else if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO submit_results(part_key,attempt_id,kind,smpp_status,smsc_message_id,committed_at) VALUES(?,?,?,?,?,?)`, commit.Result.PartKey, commit.Result.AttemptID, commit.Result.Kind, commit.Result.SMPPStatus, nullString(commit.Result.SMSCMessageID), nanos(commit.Result.CommittedAt))
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return false, nil
	}
	attemptUpdate, err := tx.ExecContext(ctx, `UPDATE submit_attempts SET state=?,resolved_at=? WHERE id=? AND part_key=?`, submittransaction.AttemptResultCommitted, nanos(commit.Result.CommittedAt), commit.Result.AttemptID, commit.Result.PartKey)
	if err != nil {
		return false, err
	}
	if updated, rowsErr := attemptUpdate.RowsAffected(); rowsErr != nil {
		return false, rowsErr
	} else if updated != 1 {
		return false, submittransaction.ErrAttemptNotFound
	}
	partState := submittransaction.PartResultCommitted
	if commit.Result.Kind == submittransaction.ResultRetry {
		partState = submittransaction.PartPending
	}
	partUpdate, err := tx.ExecContext(ctx, `UPDATE submit_parts SET state=? WHERE part_key=?`, partState, commit.Result.PartKey)
	if err != nil {
		return false, err
	}
	if updated, rowsErr := partUpdate.RowsAffected(); rowsErr != nil {
		return false, rowsErr
	} else if updated != 1 {
		return false, submittransaction.ErrPartNotFound
	}
	for _, event := range commit.Events {
		if err = insertSQLiteEvent(ctx, tx, event); err != nil {
			return false, err
		}
	}
	cdrKind := cdr.EventSMSCRejected
	cdrState := cdr.StateSMSCRejected
	switch commit.Result.Kind {
	case submittransaction.ResultSuccess:
		cdrKind, cdrState = cdr.EventSMSCAccepted, cdr.StateSMSCAccepted
		if commit.Result.LocalTermination {
			// This gateway accepted it; no carrier was involved. The statement
			// counts the two apart, so the projection must too.
			cdrKind, cdrState = cdr.EventTerminatedLocally, cdr.StateTerminatedLocally
		}
	case submittransaction.ResultRetry:
		cdrKind, cdrState = cdr.EventRetryPending, cdr.StateRetryPending
	case submittransaction.ResultTimeout:
		cdrKind, cdrState = cdr.EventTerminalTimeout, cdr.StateTerminalTimeout
	}
	if err = recordSQLiteCDRTransition(ctx, tx, cdr.Event{
		Key:   cdr.ResultEventKey(commit.Result.PartKey, commit.Result.AttemptID),
		CDRID: commit.Result.PartKey, Kind: cdrKind, State: cdrState,
		AttemptID: commit.Result.AttemptID, SMPPStatus: commit.Result.SMPPStatus,
		SMSCMessageID: commit.Result.SMSCMessageID, OccurredAt: commit.Result.CommittedAt,
	}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (r *SQLiteSubmitTransactionRepository) ClaimOutbox(ctx context.Context, owner string, limit int, now time.Time, lease time.Duration) ([]submittransaction.OutboxEvent, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT candidate.event_key,candidate.part_key,candidate.kind,candidate.exchange_name,candidate.routing_key,candidate.payload,candidate.created_at,candidate.available_at,candidate.attempts
	 FROM submit_outbox AS candidate
	 WHERE candidate.dispatched_at IS NULL AND candidate.available_at<=? AND (candidate.locked_until IS NULL OR candidate.locked_until<=?)
	 AND NOT EXISTS (
	  SELECT 1 FROM submit_outbox AS predecessor
	  WHERE predecessor.part_key=candidate.part_key AND predecessor.dispatched_at IS NULL
	  AND (predecessor.created_at<candidate.created_at OR (predecessor.created_at=candidate.created_at AND predecessor.event_key<candidate.event_key))
	 )
	 ORDER BY candidate.created_at,candidate.event_key LIMIT ?`, nanos(now), nanos(now), limit)
	if err != nil {
		return nil, err
	}
	var events []submittransaction.OutboxEvent
	for rows.Next() {
		var e submittransaction.OutboxEvent
		var created, available int64
		if err = rows.Scan(&e.Key, &e.PartKey, &e.Kind, &e.Exchange, &e.RoutingKey, &e.Payload, &created, &available, &e.Attempts); err != nil {
			rows.Close()
			return nil, err
		}
		e.CreatedAt = time.Unix(0, created).UTC()
		e.AvailableAt = time.Unix(0, available).UTC()
		events = append(events, e)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, e := range events {
		if _, err = tx.ExecContext(ctx, `UPDATE submit_outbox SET lock_owner=?,locked_until=?,attempts=attempts+1 WHERE event_key=?`, owner, nanos(now.Add(lease)), e.Key); err != nil {
			return nil, err
		}
	}
	return events, tx.Commit()
}
func (r *SQLiteSubmitTransactionRepository) MarkOutboxDispatched(ctx context.Context, key, owner string, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE submit_outbox SET dispatched_at=?,lock_owner=NULL,locked_until=NULL,last_error=NULL WHERE event_key=? AND lock_owner=?`, nanos(now), key, owner)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("outbox event %q is not owned", key)
	}
	return nil
}
func (r *SQLiteSubmitTransactionRepository) ReleaseOutbox(ctx context.Context, key, owner string, now time.Time, cause error) error {
	_, err := r.db.ExecContext(ctx, `UPDATE submit_outbox SET lock_owner=NULL,locked_until=NULL,available_at=?,last_error=? WHERE event_key=? AND lock_owner=?`, nanos(now), cause.Error(), key, owner)
	return err
}

var _ submittransaction.Repository = (*SQLiteSubmitTransactionRepository)(nil)
