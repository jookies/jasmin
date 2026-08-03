package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// sqliteMessageSpoolSchema mirrors migrations/0006_message_spool.sql plus
// migrations/0007_message_spool_receipt_only.sql. Timestamps are integer
// nanoseconds, matching every other SQLite table in this package.
const sqliteMessageSpoolSchema = `
CREATE TABLE IF NOT EXISTS message_spool_sequence (
 name TEXT PRIMARY KEY, last_value INTEGER NOT NULL CHECK(last_value >= 0)
);
INSERT OR IGNORE INTO message_spool_sequence(name,last_value) VALUES('message_spool',0);
CREATE TABLE IF NOT EXISTS message_spool (
 message_id TEXT PRIMARY KEY, seq INTEGER NOT NULL,
 connector_id TEXT NOT NULL CHECK(connector_id <> ''),
 user_id TEXT NOT NULL DEFAULT '', source_addr TEXT NOT NULL DEFAULT '',
 dest_addr TEXT NOT NULL CHECK(dest_addr <> ''),
 text TEXT NOT NULL DEFAULT '', raw BLOB NOT NULL DEFAULT x'',
 data_coding INTEGER NOT NULL CHECK(data_coding BETWEEN 0 AND 255),
 encoding TEXT NOT NULL DEFAULT '', parts INTEGER NOT NULL CHECK(parts > 0),
 verdict_accept INTEGER NOT NULL CHECK(verdict_accept IN (0,1)),
 verdict_stat TEXT NOT NULL CHECK(verdict_stat <> ''),
 verdict_err TEXT NOT NULL DEFAULT '', verdict_reason TEXT NOT NULL DEFAULT '',
 gate_bypassed INTEGER NOT NULL DEFAULT 0 CHECK(gate_bypassed IN (0,1)),
 delivery_state TEXT NOT NULL DEFAULT 'pending' CHECK(delivery_state IN ('pending','delivered','dlq')),
 delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK(delivery_attempts >= 0),
 received_at INTEGER NOT NULL, delivered_at INTEGER, next_attempt_at INTEGER,
 receipt_due_at INTEGER, receipt_sent_at INTEGER,
 receipt_lock_owner TEXT, receipt_locked_until INTEGER,
 created_at INTEGER NOT NULL,
 -- receipt_only marks a row that owes a receipt and holds no message; see
 -- msgspool.Message.ReceiptOnly and migration 0007. The three CHECKs are the
 -- same invariants the PostgreSQL migration adds as named constraints.
 receipt_only INTEGER NOT NULL DEFAULT 0 CHECK(receipt_only IN (0,1)),
 CHECK((delivery_state = 'delivered') = (delivered_at IS NOT NULL)),
 CHECK(receipt_only = 0 OR (text = '' AND length(raw) = 0)),
 CHECK(receipt_only = 0 OR (next_attempt_at IS NULL AND delivery_state = 'pending')),
 CHECK(receipt_only = 0 OR receipt_due_at IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS message_spool_seq ON message_spool(seq);
CREATE INDEX IF NOT EXISTS message_spool_connector_seq ON message_spool(connector_id,seq);
CREATE INDEX IF NOT EXISTS message_spool_deliverable_seq ON message_spool(connector_id,seq)
 WHERE receipt_only = 0;
CREATE INDEX IF NOT EXISTS message_spool_dest_time ON message_spool(dest_addr,received_at,message_id);
CREATE INDEX IF NOT EXISTS message_spool_received_at ON message_spool(received_at,message_id);
CREATE INDEX IF NOT EXISTS message_spool_delivery_due ON message_spool(next_attempt_at,seq)
 WHERE delivery_state='pending' AND next_attempt_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS message_spool_receipt_due ON message_spool(receipt_due_at,seq)
 WHERE receipt_sent_at IS NULL AND receipt_due_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS message_spool_access_audit (
 audit_id INTEGER PRIMARY KEY AUTOINCREMENT, subject TEXT NOT NULL, action TEXT NOT NULL,
 target TEXT NOT NULL, allowed INTEGER NOT NULL CHECK(allowed IN (0,1)),
 row_count INTEGER NOT NULL DEFAULT 0 CHECK(row_count >= 0), occurred_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS message_spool_access_audit_time
 ON message_spool_access_audit(occurred_at,audit_id);`

// SQLiteMessageSpool is the unit and local-run message spool. It is behaviourally
// identical to PostgresMessageSpool, including cursor allocation order, so a
// test that passes here is testing the same contract production runs.
type SQLiteMessageSpool struct{ db *sql.DB }

func NewSQLiteMessageSpool(db *sql.DB) (*SQLiteMessageSpool, error) {
	if db == nil {
		return nil, errors.New("nil SQLite database")
	}
	return &SQLiteMessageSpool{db: db}, nil
}

// Init applies the spool schema and the pull-credential schema together, the
// same pair PostgresMessageSpool.Migrate applies: a spool without the
// credentials that authorize reading it is a half-built store, and discovering
// that at the first poll rather than at startup is the wrong time.
func (store *SQLiteMessageSpool) Init(ctx context.Context) error {
	// Columns added after the table's first shape have to be reconciled before
	// the schema runs, not after: CREATE TABLE IF NOT EXISTS is a no-op on an
	// existing spool file, so a column added later would never appear, and the
	// partial index below references it. Getting this wrong is silent at startup
	// and loud at the first Put.
	if err := sqliteAddMissingSpoolColumns(ctx, store.db); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, sqliteMessageSpoolSchema); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, sqliteMessageConsumerSchema)
	return err
}

// sqliteAddMissingSpoolColumns brings an existing spool file up to the current
// column set. It is the SQLite counterpart of the ADD COLUMN IF NOT EXISTS in
// migrations/0007_message_spool_receipt_only.sql.
//
// The CHECK constraints that migration adds cannot follow: SQLite has no
// ALTER TABLE ADD CONSTRAINT, and rebuilding the table to get them would risk a
// local spool file for a guard that msgspool.ValidateMessage already applies on
// every write. A file created fresh gets them from the schema above.
func sqliteAddMissingSpoolColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(message_spool)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	// A database that has never been initialized reports no columns, and the
	// schema below creates the table complete. Only an existing one needs work.
	existing := map[string]bool{}
	for rows.Next() {
		var index int
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&index, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(existing) == 0 || existing["receipt_only"] {
		return nil
	}
	_, err = db.ExecContext(ctx,
		`ALTER TABLE message_spool ADD COLUMN receipt_only INTEGER NOT NULL DEFAULT 0`)
	return err
}

const sqliteSpoolMaskedColumns = `message_id,seq,connector_id,user_id,source_addr,dest_addr,
 CASE WHEN ?=1 THEN text ELSE '' END, CASE WHEN ?=1 THEN raw ELSE x'' END,
 data_coding,encoding,parts,verdict_accept,verdict_stat,verdict_err,verdict_reason,gate_bypassed,
 delivery_state,delivery_attempts,received_at,delivered_at,next_attempt_at,
 receipt_due_at,receipt_sent_at,COALESCE(receipt_lock_owner,''),receipt_locked_until,created_at,
 receipt_only`

const sqliteSpoolFullColumns = `message_id,seq,connector_id,user_id,source_addr,dest_addr,
 text,raw,data_coding,encoding,parts,verdict_accept,verdict_stat,verdict_err,verdict_reason,
 gate_bypassed,delivery_state,delivery_attempts,received_at,delivered_at,next_attempt_at,
 receipt_due_at,receipt_sent_at,COALESCE(receipt_lock_owner,''),receipt_locked_until,created_at,
 receipt_only`

const sqliteSpoolRedactedClaimColumns = `message_id,seq,connector_id,user_id,source_addr,dest_addr,
 '',x'',data_coding,encoding,parts,verdict_accept,verdict_stat,verdict_err,verdict_reason,
 gate_bypassed,delivery_state,delivery_attempts,received_at,delivered_at,next_attempt_at,
 receipt_due_at,receipt_sent_at,COALESCE(receipt_lock_owner,''),receipt_locked_until,created_at,
 receipt_only`

func (store *SQLiteMessageSpool) Put(
	ctx context.Context,
	message msgspool.Message,
	createdAt time.Time,
) (msgspool.Record, error) {
	if err := msgspool.ValidateMessage(message); err != nil {
		return msgspool.Record{}, err
	}
	raw := message.Raw
	if raw == nil {
		raw = []byte{}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return msgspool.Record{}, err
	}
	defer tx.Rollback()
	// SQLite has one writer at a time, so allocating the cursor inside the
	// writing transaction gives the same commit-order guarantee the PostgreSQL
	// row lock gives. SQLite has no writable CTE, hence the two statements.
	sequence, err := nextSQLiteSpoolSequence(ctx, tx)
	if err != nil {
		return msgspool.Record{}, err
	}
	// The conflict branch deliberately leaves delivery_state, delivery_attempts,
	// delivered_at, receipt_due_at, receipt_sent_at and created_at alone.
	// Re-spooling after an AMQP redelivery must not un-deliver a message or
	// re-arm a receipt that already went out.
	//
	// The receipt_only guards mirror PostgresMessageSpool.Put exactly; see the
	// comment there for the redelivery that would otherwise blank the text of an
	// already-spooled message. next_attempt_at moves in one direction only: a row
	// that was a bare receipt obligation and has become an assembled message
	// arms its push, and nothing else re-arms one.
	if _, err = tx.ExecContext(ctx, `INSERT INTO message_spool(
 message_id,seq,connector_id,user_id,source_addr,dest_addr,text,raw,data_coding,encoding,parts,
 verdict_accept,verdict_stat,verdict_err,verdict_reason,gate_bypassed,
 delivery_state,delivery_attempts,received_at,next_attempt_at,receipt_due_at,created_at,receipt_only)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'pending',0,?,?,?,?,?)
 ON CONFLICT(message_id) DO UPDATE SET
 seq=excluded.seq,connector_id=excluded.connector_id,user_id=excluded.user_id,
 source_addr=excluded.source_addr,dest_addr=excluded.dest_addr,
 text=CASE WHEN excluded.receipt_only=1 THEN message_spool.text ELSE excluded.text END,
 raw=CASE WHEN excluded.receipt_only=1 THEN message_spool.raw ELSE excluded.raw END,
 data_coding=CASE WHEN excluded.receipt_only=1 THEN message_spool.data_coding ELSE excluded.data_coding END,
 encoding=CASE WHEN excluded.receipt_only=1 THEN message_spool.encoding ELSE excluded.encoding END,
 parts=CASE WHEN excluded.receipt_only=1 THEN message_spool.parts ELSE excluded.parts END,
 receipt_only=CASE WHEN message_spool.receipt_only=1 AND excluded.receipt_only=1 THEN 1 ELSE 0 END,
 next_attempt_at=CASE WHEN message_spool.receipt_only=1 AND excluded.receipt_only=0
   THEN excluded.next_attempt_at ELSE message_spool.next_attempt_at END,
 verdict_accept=CASE WHEN message_spool.receipt_sent_at IS NULL THEN excluded.verdict_accept ELSE message_spool.verdict_accept END,
 verdict_stat=CASE WHEN message_spool.receipt_sent_at IS NULL THEN excluded.verdict_stat ELSE message_spool.verdict_stat END,
 verdict_err=CASE WHEN message_spool.receipt_sent_at IS NULL THEN excluded.verdict_err ELSE message_spool.verdict_err END,
 verdict_reason=CASE WHEN message_spool.receipt_sent_at IS NULL THEN excluded.verdict_reason ELSE message_spool.verdict_reason END,
 gate_bypassed=CASE WHEN message_spool.receipt_sent_at IS NULL THEN excluded.gate_bypassed ELSE message_spool.gate_bypassed END,
 received_at=excluded.received_at`,
		message.MessageID, sequence, message.ConnectorID, message.UserID, message.SourceAddr,
		message.DestAddr, message.Text, raw, int64(message.DataCoding), message.Encoding,
		message.Parts, message.Verdict.Accept, message.Verdict.Stat, message.Verdict.Err,
		message.Verdict.Reason, message.Verdict.GateBypassed, nanos(message.ReceivedAt),
		optionalNanos(message.NextAttemptAt), optionalNanos(message.ReceiptDueAt),
		nanos(createdAt), message.ReceiptOnly); err != nil {
		return msgspool.Record{}, err
	}
	var record msgspool.Record
	if err = scanSQLiteSpoolRecord(tx.QueryRowContext(ctx,
		`SELECT `+sqliteSpoolFullColumns+` FROM message_spool WHERE message_id=?`,
		message.MessageID), &record); err != nil {
		return msgspool.Record{}, err
	}
	if err = tx.Commit(); err != nil {
		return msgspool.Record{}, err
	}
	return record, nil
}

func nextSQLiteSpoolSequence(ctx context.Context, tx *sql.Tx) (int64, error) {
	if _, err := tx.ExecContext(ctx,
		`UPDATE message_spool_sequence SET last_value=last_value+1 WHERE name='message_spool'`); err != nil {
		return 0, err
	}
	var sequence int64
	err := tx.QueryRowContext(ctx,
		`SELECT last_value FROM message_spool_sequence WHERE name='message_spool'`).Scan(&sequence)
	return sequence, err
}

func (store *SQLiteMessageSpool) Get(
	ctx context.Context,
	messageID string,
	includeContent bool,
) (msgspool.Record, error) {
	var record msgspool.Record
	err := scanSQLiteSpoolRecord(store.db.QueryRowContext(ctx,
		`SELECT `+sqliteSpoolMaskedColumns+` FROM message_spool WHERE message_id=?`,
		includeContent, includeContent, messageID), &record)
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Record{}, msgspool.ErrNotFound
	}
	if err != nil {
		return msgspool.Record{}, err
	}
	record.ContentRedacted = !includeContent
	return record, nil
}

func (store *SQLiteMessageSpool) Search(
	ctx context.Context,
	query msgspool.Query,
) ([]msgspool.Record, error) {
	if query.Limit < 1 || query.Limit > 1000 {
		return nil, msgspool.ErrInvalidInput
	}
	from, to := int64(0), int64(0)
	if query.ReceivedFrom != nil {
		from = nanos(*query.ReceivedFrom)
	}
	if query.ReceivedTo != nil {
		to = nanos(*query.ReceivedTo)
	}
	arguments := []any{
		query.IncludeContent, query.IncludeContent, query.AfterSequence,
		query.ConnectorID, query.ConnectorID, query.UserID, query.UserID,
		query.DestAddr, query.DestAddr, string(query.DeliveryState), string(query.DeliveryState),
		from, from, to, to, query.VerdictStat, query.VerdictStat,
		query.GateBypassedOnly,
	}
	// The scoped-consumer allow-list is part of the predicate, never a filter
	// over fetched rows. SQLite has no array parameter, so the IN list is built
	// from placeholders — the values themselves are still bound, never
	// interpolated.
	scope := ""
	if len(query.ConnectorIDs) > 0 {
		scope = " AND connector_id IN (?" + strings.Repeat(",?", len(query.ConnectorIDs)-1) + ")"
		for _, connector := range query.ConnectorIDs {
			arguments = append(arguments, connector)
		}
	}
	scope += spoolReceiptOnlyClause(query.IncludeReceiptOnly)
	arguments = append(arguments, query.Limit)
	rows, err := store.db.QueryContext(ctx, `SELECT `+sqliteSpoolMaskedColumns+
		` FROM message_spool
 WHERE `+spoolSequenceBound(query.Descending, "?")+`
   AND (?='' OR connector_id=?)
   AND (?='' OR user_id=?)
   AND (?='' OR dest_addr=?)
   AND (?='' OR delivery_state=?)
   AND (?=0 OR received_at>=?)
   AND (?=0 OR received_at<?)
   AND (?='' OR verdict_stat=?)
   AND (?=0 OR gate_bypassed=1)`+scope+`
 `+spoolSequenceOrder(query.Descending)+` LIMIT ?`, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSQLiteSpoolRecords(rows, query.Limit, !query.IncludeContent)
}

func (store *SQLiteMessageSpool) DueForDelivery(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]msgspool.Record, error) {
	if limit < 1 || limit > 1000 {
		return nil, msgspool.ErrInvalidInput
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+sqliteSpoolFullColumns+
		` FROM message_spool
 WHERE delivery_state='pending' AND next_attempt_at IS NOT NULL AND next_attempt_at<=?
 ORDER BY next_attempt_at,seq LIMIT ?`, nanos(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSQLiteSpoolRecords(rows, limit, false)
}

func (store *SQLiteMessageSpool) MarkDelivered(
	ctx context.Context,
	messageID string,
	at time.Time,
) error {
	return store.mutate(ctx, `delivery_state='delivered',delivered_at=?,
 delivery_attempts=delivery_attempts+1,next_attempt_at=NULL`, messageID, nanos(at))
}

func (store *SQLiteMessageSpool) MarkAttemptFailed(
	ctx context.Context,
	messageID string,
	nextAttemptAt time.Time,
) error {
	return store.mutate(ctx, `delivery_state='pending',delivered_at=NULL,
 delivery_attempts=delivery_attempts+1,next_attempt_at=?`, messageID, nanos(nextAttemptAt))
}

func (store *SQLiteMessageSpool) MarkDeadLettered(ctx context.Context, messageID string) error {
	return store.mutate(ctx, `delivery_state='dlq',delivered_at=NULL,
 delivery_attempts=delivery_attempts+1,next_attempt_at=NULL`, messageID, nil)
}

// mutate re-allocates the cursor alongside the state change, which is what
// hands a retried row back to a consumer that already paged past it.
func (store *SQLiteMessageSpool) mutate(
	ctx context.Context,
	assignments string,
	messageID string,
	argument any,
) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sequence, err := nextSQLiteSpoolSequence(ctx, tx)
	if err != nil {
		return err
	}
	arguments := []any{sequence}
	if argument != nil {
		arguments = append(arguments, argument)
	}
	arguments = append(arguments, messageID)
	result, err := tx.ExecContext(ctx,
		`UPDATE message_spool SET seq=?,`+assignments+` WHERE message_id=?`, arguments...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return msgspool.ErrNotFound
	}
	return tx.Commit()
}

// ClaimDueReceipts leases owed receipts exclusively. SQLite serializes writers,
// so the single UPDATE ... RETURNING is the equivalent of PostgreSQL's
// FOR UPDATE SKIP LOCKED claim: two callers cannot take the same row.
//
// As in PostgreSQL, an expired lease is re-claimable through the same predicate
// and the claim does not re-allocate seq.
func (store *SQLiteMessageSpool) ClaimDueReceipts(
	ctx context.Context,
	owner string,
	now time.Time,
	lease time.Duration,
	limit int,
) ([]msgspool.Record, error) {
	if owner == "" || lease <= 0 || limit < 1 || limit > 1000 {
		return nil, msgspool.ErrInvalidInput
	}
	rows, err := store.db.QueryContext(ctx, `UPDATE message_spool
 SET receipt_lock_owner=?,receipt_locked_until=?
 WHERE message_id IN (
  SELECT message_id FROM message_spool
  WHERE receipt_due_at IS NOT NULL AND receipt_sent_at IS NULL AND receipt_due_at<=?
    AND (receipt_locked_until IS NULL OR receipt_locked_until<=?)
  ORDER BY receipt_due_at,seq LIMIT ?)
 RETURNING `+sqliteSpoolRedactedClaimColumns,
		owner, nanos(now.Add(lease)), nanos(now), nanos(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claimed, err := collectSQLiteSpoolRecords(rows, limit, true)
	if err != nil {
		return nil, err
	}
	sortSpoolReceiptClaims(claimed)
	return claimed, nil
}

func (store *SQLiteMessageSpool) MarkReceiptSent(
	ctx context.Context,
	messageID string,
	owner string,
	at time.Time,
) error {
	if owner == "" {
		return msgspool.ErrInvalidInput
	}
	result, err := store.db.ExecContext(ctx, `UPDATE message_spool
 SET receipt_sent_at=?,receipt_lock_owner=NULL,receipt_locked_until=NULL
 WHERE message_id=? AND receipt_lock_owner=? AND receipt_sent_at IS NULL`,
		nanos(at), messageID, owner)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return msgspool.ErrClaimLost
	}
	return nil
}

func (store *SQLiteMessageSpool) Prune(
	ctx context.Context,
	olderThan time.Time,
	batch int,
) (msgspool.PruneResult, error) {
	if olderThan.IsZero() || batch < 1 || batch > 10000 {
		return msgspool.PruneResult{}, msgspool.ErrInvalidInput
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM message_spool WHERE message_id IN (
 SELECT message_id FROM message_spool WHERE received_at<?
 ORDER BY received_at,message_id LIMIT ?)`, nanos(olderThan), batch)
	if err != nil {
		return msgspool.PruneResult{}, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return msgspool.PruneResult{}, err
	}
	// See the Postgres twin: the access trail is written per pull-API read and
	// had no prune path, and the audit is fail-closed.
	if _, err = store.db.ExecContext(ctx,
		`DELETE FROM message_spool_access_audit WHERE occurred_at<?`, nanos(olderThan),
	); err != nil {
		return msgspool.PruneResult{}, err
	}
	return msgspool.PruneResult{Records: deleted}, nil
}

// Census counts rows per connector. Behaviourally identical to the PostgreSQL
// backend; SQLite has no FILTER clause, so the conditional counts are SUMs over
// a boolean expression.
func (store *SQLiteMessageSpool) Census(
	ctx context.Context,
	now time.Time,
) ([]msgspool.ConnectorCensus, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT connector_id,
 COUNT(*),
 SUM(CASE WHEN delivery_state='dlq' THEN 1 ELSE 0 END),
 SUM(CASE WHEN receipt_sent_at IS NULL AND receipt_due_at IS NOT NULL AND receipt_due_at<=?
     THEN 1 ELSE 0 END)
 FROM message_spool GROUP BY connector_id ORDER BY connector_id`, nanos(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var census []msgspool.ConnectorCensus
	for rows.Next() {
		var entry msgspool.ConnectorCensus
		if err := rows.Scan(&entry.ConnectorID, &entry.Rows,
			&entry.DeadLettered, &entry.ReceiptsOverdue); err != nil {
			return nil, err
		}
		census = append(census, entry)
	}
	return census, rows.Err()
}

func (store *SQLiteMessageSpool) AuditAccess(ctx context.Context, audit msgspool.AccessAudit) error {
	if audit.OccurredAt.IsZero() || audit.Action == "" {
		return msgspool.ErrInvalidInput
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO message_spool_access_audit(
 subject,action,target,allowed,row_count,occurred_at) VALUES(?,?,?,?,?,?)`,
		audit.Subject, audit.Action, audit.Target, audit.Allowed,
		audit.RowCount, nanos(audit.OccurredAt))
	return err
}

func collectSQLiteSpoolRecords(
	rows *sql.Rows,
	limit int,
	redacted bool,
) ([]msgspool.Record, error) {
	records := make([]msgspool.Record, 0, limit)
	for rows.Next() {
		var record msgspool.Record
		if err := scanSQLiteSpoolRecord(rows, &record); err != nil {
			return nil, err
		}
		record.ContentRedacted = redacted
		records = append(records, record)
	}
	return records, rows.Err()
}

func scanSQLiteSpoolRecord(
	scanner interface{ Scan(...any) error },
	record *msgspool.Record,
) error {
	var dataCoding int64
	var received, created int64
	var delivered, nextAttempt, receiptDue, receiptSent, lockedUntil sql.NullInt64
	if err := scanner.Scan(
		&record.MessageID, &record.Sequence, &record.ConnectorID, &record.UserID,
		&record.SourceAddr, &record.DestAddr, &record.Text, &record.Raw, &dataCoding,
		&record.Encoding, &record.Parts, &record.Verdict.Accept, &record.Verdict.Stat,
		&record.Verdict.Err, &record.Verdict.Reason, &record.Verdict.GateBypassed,
		&record.DeliveryState, &record.DeliveryAttempts, &received, &delivered,
		&nextAttempt, &receiptDue, &receiptSent, &record.ReceiptLockOwner, &lockedUntil,
		&created, &record.ReceiptOnly,
	); err != nil {
		return err
	}
	record.DataCoding = byte(dataCoding)
	record.ReceivedAt = time.Unix(0, received).UTC()
	record.CreatedAt = time.Unix(0, created).UTC()
	record.DeliveredAt = timePtr(delivered)
	record.NextAttemptAt = timePtr(nextAttempt)
	record.ReceiptDueAt = timePtr(receiptDue)
	record.ReceiptSentAt = timePtr(receiptSent)
	record.ReceiptLockedUntil = timePtr(lockedUntil)
	return nil
}

var _ msgspool.Repository = (*SQLiteMessageSpool)(nil)

// AccessActivity aggregates the audit table for the named subjects.
//
// Grouping in SQL rather than streaming rows is deliberate: the audit table is
// append-only and unbounded, and an operator opening a console page must not
// pull every row a busy consumer ever wrote.
func (store *SQLiteMessageSpool) AccessActivity(
	ctx context.Context,
	subjects []string,
	since time.Time,
) ([]msgspool.SubjectActivity, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(subjects))
	args := make([]any, 0, len(subjects)+1)
	for _, subject := range subjects {
		placeholders = append(placeholders, "?")
		args = append(args, subject)
	}
	_ = len(placeholders)
	query := `SELECT subject,
 COALESCE(SUM(CASE WHEN allowed THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN allowed THEN row_count ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN allowed THEN 0 ELSE 1 END),0),
 MAX(CASE WHEN allowed THEN occurred_at END)
 FROM message_spool_access_audit
 WHERE subject IN (` + strings.Join(placeholders, ",") + `)`
	if !since.IsZero() {
		query += " AND occurred_at >= " + "?"
		args = append(args, nanos(since))
	}
	query += " GROUP BY subject"

	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	activity := make([]msgspool.SubjectActivity, 0, len(subjects))
	for rows.Next() {
		var entry msgspool.SubjectActivity
		var last sql.NullInt64
		if err := rows.Scan(&entry.Subject, &entry.Reads, &entry.Rows, &entry.Denied, &last); err != nil {
			return nil, err
		}
		if last.Valid {
			at := time.Unix(0, last.Int64).UTC()
			entry.LastReadAt = &at
		}
		activity = append(activity, entry)
	}
	return activity, rows.Err()
}
