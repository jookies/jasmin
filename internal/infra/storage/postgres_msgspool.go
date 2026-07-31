package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

//go:embed migrations/0006_message_spool.sql migrations/0007_message_spool_receipt_only.sql migrations/0008_message_consumers.sql
var messageSpoolMigrations embed.FS

// messageSpoolMigrationFiles are applied in order by Migrate. The pull
// credentials live with the spool rather than in the admin control plane: a
// scope names connectors whose rows are in this database, and a credential that
// could outlive or diverge from the spool it authorizes reads against is a
// credential nobody can reason about.
var messageSpoolMigrationFiles = []string{
	"migrations/0006_message_spool.sql",
	"migrations/0007_message_spool_receipt_only.sql",
	"migrations/0008_message_consumers.sql",
}

// PostgresMessageSpool is the production message spool for MT termination
// connectors. It stores one row per assembled message for a bounded retention
// window; see migrations/0006_message_spool.sql for why the paging cursor is an
// allocated sequence rather than a timestamp.
type PostgresMessageSpool struct {
	db *sql.DB
}

func OpenPostgresMessageSpool(ctx context.Context, dsn string) (*PostgresMessageSpool, error) {
	if dsn == "" {
		return nil, errors.New("empty PostgreSQL DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect PostgreSQL message spool: %w", err)
	}
	return &PostgresMessageSpool{db: db}, nil
}

func NewPostgresMessageSpool(db *sql.DB) (*PostgresMessageSpool, error) {
	if db == nil {
		return nil, errors.New("nil PostgreSQL database")
	}
	return &PostgresMessageSpool{db: db}, nil
}

func (store *PostgresMessageSpool) Migrate(ctx context.Context) error {
	for _, name := range messageSpoolMigrationFiles {
		migration, err := messageSpoolMigrations.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err = store.db.ExecContext(ctx, string(migration)); err != nil {
			return fmt.Errorf("migrate PostgreSQL message spool (%s): %w", name, err)
		}
	}
	return nil
}

func (store *PostgresMessageSpool) Ping(ctx context.Context) error {
	return store.db.PingContext(ctx)
}

func (store *PostgresMessageSpool) Close() error {
	return store.db.Close()
}

// postgresSpoolMaskedColumns projects the content through $1, so a masked read
// never materializes the message text in the process at all. Post-filtering a
// full row in Go would leave the OTP body in a buffer somewhere it is not
// wanted.
const postgresSpoolMaskedColumns = `message_id,seq,connector_id,user_id,source_addr,dest_addr,
 CASE WHEN $1::boolean THEN text ELSE '' END,
 CASE WHEN $1::boolean THEN raw ELSE ''::bytea END,
 data_coding,encoding,parts,verdict_accept,verdict_stat,verdict_err,verdict_reason,gate_bypassed,
 delivery_state,delivery_attempts,received_at,delivered_at,next_attempt_at,
 receipt_due_at,receipt_sent_at,COALESCE(receipt_lock_owner,''),receipt_locked_until,created_at,
 receipt_only`

const postgresSpoolFullColumns = `message_id,seq,connector_id,user_id,source_addr,dest_addr,
 text,raw,data_coding,encoding,parts,verdict_accept,verdict_stat,verdict_err,verdict_reason,
 gate_bypassed,delivery_state,delivery_attempts,received_at,delivered_at,next_attempt_at,
 receipt_due_at,receipt_sent_at,COALESCE(receipt_lock_owner,''),receipt_locked_until,created_at,
 receipt_only`

// nextSpoolSequence allocates the paging cursor inside the writing statement.
// The row lock it takes is held until that statement's transaction commits,
// which is what forces sequence order to equal commit order.
const nextSpoolSequence = `UPDATE message_spool_sequence SET last_value=last_value+1
 WHERE name='message_spool' RETURNING last_value`

func (store *PostgresMessageSpool) Put(
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
	record := msgspool.Record{Message: message}
	var delivered, nextAttempt, receiptDue, receiptSent, lockedUntil sql.NullTime
	// One statement, so the sequence row lock is held for microseconds. Handing
	// this store an external transaction would hold it for that transaction's
	// lifetime and serialize every spool write behind it.
	//
	// The verdict columns freeze once receipt_sent_at is set, alongside the
	// lifecycle columns that were already frozen. An AMQP redelivery re-decides
	// the verdict, and a gate that has since closed would rewrite the row to say
	// REJECTD for a message the partner was already told was DELIVRD. The spool
	// is what answers a dispute about exactly that, so the row must keep what was
	// actually sent, not what would be decided now.
	//
	// A receipt-only write can never narrow a row that already holds a message.
	// That is not hypothetical: a worker that commits the assembled row and then
	// fails to ack sees the completing segment redelivered, and the assembler has
	// already dropped the joined parts, so the redelivery arrives as an
	// *incomplete* segment and would re-Put the same message id carrying no
	// content. Without the guards below that upsert would blank the text of a
	// message that had already been spooled — and, on a pull-only deployment,
	// blank it after the consumer had been told it existed. So receipt_only only
	// ever falls (never rises), and the content columns are held when the
	// incoming row is receipt-only.
	err := store.db.QueryRowContext(ctx, `WITH allocated AS (`+nextSpoolSequence+`)
 INSERT INTO message_spool(
 message_id,seq,connector_id,user_id,source_addr,dest_addr,text,raw,data_coding,encoding,parts,
 verdict_accept,verdict_stat,verdict_err,verdict_reason,gate_bypassed,
 delivery_state,delivery_attempts,received_at,next_attempt_at,receipt_due_at,created_at,receipt_only)
 SELECT $1,allocated.last_value,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
        'pending',0,$16,$17,$18,$19,$20
 FROM allocated
 ON CONFLICT(message_id) DO UPDATE SET
 seq=EXCLUDED.seq,connector_id=EXCLUDED.connector_id,user_id=EXCLUDED.user_id,
 source_addr=EXCLUDED.source_addr,dest_addr=EXCLUDED.dest_addr,
 text=CASE WHEN EXCLUDED.receipt_only THEN message_spool.text ELSE EXCLUDED.text END,
 raw=CASE WHEN EXCLUDED.receipt_only THEN message_spool.raw ELSE EXCLUDED.raw END,
 data_coding=CASE WHEN EXCLUDED.receipt_only THEN message_spool.data_coding ELSE EXCLUDED.data_coding END,
 encoding=CASE WHEN EXCLUDED.receipt_only THEN message_spool.encoding ELSE EXCLUDED.encoding END,
 parts=CASE WHEN EXCLUDED.receipt_only THEN message_spool.parts ELSE EXCLUDED.parts END,
 receipt_only=message_spool.receipt_only AND EXCLUDED.receipt_only,
 -- The one case in which a push is (re)scheduled by an upsert: a row that was
 -- a bare receipt obligation has become an assembled message. Every other
 -- conflict leaves next_attempt_at alone, so an AMQP redelivery cannot cause a
 -- second downstream push.
 next_attempt_at=CASE WHEN message_spool.receipt_only AND NOT EXCLUDED.receipt_only
   THEN EXCLUDED.next_attempt_at ELSE message_spool.next_attempt_at END,
 verdict_accept=CASE WHEN message_spool.receipt_sent_at IS NULL THEN EXCLUDED.verdict_accept ELSE message_spool.verdict_accept END,
 verdict_stat=CASE WHEN message_spool.receipt_sent_at IS NULL THEN EXCLUDED.verdict_stat ELSE message_spool.verdict_stat END,
 verdict_err=CASE WHEN message_spool.receipt_sent_at IS NULL THEN EXCLUDED.verdict_err ELSE message_spool.verdict_err END,
 verdict_reason=CASE WHEN message_spool.receipt_sent_at IS NULL THEN EXCLUDED.verdict_reason ELSE message_spool.verdict_reason END,
 gate_bypassed=CASE WHEN message_spool.receipt_sent_at IS NULL THEN EXCLUDED.gate_bypassed ELSE message_spool.gate_bypassed END,
 received_at=EXCLUDED.received_at
 RETURNING seq,delivery_state,delivery_attempts,delivered_at,next_attempt_at,
 receipt_due_at,receipt_sent_at,COALESCE(receipt_lock_owner,''),receipt_locked_until,created_at,
 receipt_only`,
		message.MessageID, message.ConnectorID, message.UserID, message.SourceAddr,
		message.DestAddr, message.Text, raw, int16(message.DataCoding), message.Encoding,
		message.Parts, message.Verdict.Accept, message.Verdict.Stat, message.Verdict.Err,
		message.Verdict.Reason, message.Verdict.GateBypassed, message.ReceivedAt.UTC(),
		nullTimePtr(message.NextAttemptAt), nullTimePtr(message.ReceiptDueAt), createdAt.UTC(),
		message.ReceiptOnly,
	).Scan(
		&record.Sequence, &record.DeliveryState, &record.DeliveryAttempts, &delivered,
		&nextAttempt, &receiptDue, &receiptSent, &record.ReceiptLockOwner, &lockedUntil,
		&record.CreatedAt, &record.ReceiptOnly,
	)
	if err != nil {
		return msgspool.Record{}, err
	}
	record.DeliveredAt = nullTimeValue(delivered)
	record.NextAttemptAt = nullTimeValue(nextAttempt)
	record.ReceiptDueAt = nullTimeValue(receiptDue)
	record.ReceiptSentAt = nullTimeValue(receiptSent)
	record.ReceiptLockedUntil = nullTimeValue(lockedUntil)
	record.CreatedAt = record.CreatedAt.UTC()
	return record, nil
}

func (store *PostgresMessageSpool) Get(
	ctx context.Context,
	messageID string,
	includeContent bool,
) (msgspool.Record, error) {
	row := store.db.QueryRowContext(ctx, `SELECT `+postgresSpoolMaskedColumns+
		` FROM message_spool WHERE message_id=$2`, includeContent, messageID)
	var record msgspool.Record
	err := scanPostgresSpoolRecord(row, &record)
	if errors.Is(err, sql.ErrNoRows) {
		return msgspool.Record{}, msgspool.ErrNotFound
	}
	if err != nil {
		return msgspool.Record{}, err
	}
	record.ContentRedacted = !includeContent
	return record, nil
}

func (store *PostgresMessageSpool) Search(
	ctx context.Context,
	query msgspool.Query,
) ([]msgspool.Record, error) {
	if query.Limit < 1 || query.Limit > 1000 {
		return nil, msgspool.ErrInvalidInput
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+postgresSpoolMaskedColumns+
		` FROM message_spool
 WHERE `+spoolSequenceBound(query.Descending, "$2")+`
   AND ($3='' OR connector_id=$3)
   AND ($4='' OR user_id=$4)
   AND ($5='' OR dest_addr=$5)
   AND ($6='' OR delivery_state=$6)
   AND ($7::timestamptz IS NULL OR received_at>=$7)
   AND ($8::timestamptz IS NULL OR received_at<$8)
   AND ($9='' OR verdict_stat=$9)
   AND (NOT $10::boolean OR gate_bypassed)
   AND ($12::text[] IS NULL OR connector_id = ANY($12::text[]))`+
		spoolReceiptOnlyClause(query.IncludeReceiptOnly)+`
 `+spoolSequenceOrder(query.Descending)+` LIMIT $11`,
		query.IncludeContent, query.AfterSequence, query.ConnectorID, query.UserID,
		query.DestAddr, string(query.DeliveryState),
		nullTimePtr(query.ReceivedFrom), nullTimePtr(query.ReceivedTo),
		query.VerdictStat, query.GateBypassedOnly, query.Limit,
		// The scoped-consumer allow-list, compiled into the predicate. NULL
		// rather than an empty array for "no filter": an empty array would make
		// = ANY() match nothing, and a scope that silently matched nothing would
		// look exactly like a quiet connector.
		connectorIDArray(query.ConnectorIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPostgresSpoolRecords(rows, query.Limit, !query.IncludeContent)
}

func (store *PostgresMessageSpool) DueForDelivery(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]msgspool.Record, error) {
	if limit < 1 || limit > 1000 {
		return nil, msgspool.ErrInvalidInput
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+postgresSpoolFullColumns+
		` FROM message_spool
 WHERE delivery_state='pending' AND next_attempt_at IS NOT NULL AND next_attempt_at<=$1
 ORDER BY next_attempt_at,seq LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPostgresSpoolRecords(rows, limit, false)
}

func (store *PostgresMessageSpool) MarkDelivered(
	ctx context.Context,
	messageID string,
	at time.Time,
) error {
	return store.mutate(ctx, `SET seq=allocated.last_value,delivery_state='delivered',
 delivered_at=$2,delivery_attempts=message_spool.delivery_attempts+1,next_attempt_at=NULL`,
		messageID, at.UTC())
}

func (store *PostgresMessageSpool) MarkAttemptFailed(
	ctx context.Context,
	messageID string,
	nextAttemptAt time.Time,
) error {
	return store.mutate(ctx, `SET seq=allocated.last_value,delivery_state='pending',
 delivered_at=NULL,delivery_attempts=message_spool.delivery_attempts+1,next_attempt_at=$2`,
		messageID, nextAttemptAt.UTC())
}

func (store *PostgresMessageSpool) MarkDeadLettered(ctx context.Context, messageID string) error {
	return store.mutate(ctx, `SET seq=allocated.last_value,delivery_state='dlq',
 delivered_at=NULL,delivery_attempts=message_spool.delivery_attempts+1,next_attempt_at=NULL`,
		messageID, nil)
}

// mutate re-allocates the cursor alongside the state change, which is what
// hands a retried row back to a consumer that already paged past it.
func (store *PostgresMessageSpool) mutate(
	ctx context.Context,
	assignments string,
	messageID string,
	argument any,
) error {
	statement := `WITH allocated AS (` + nextSpoolSequence + `)
 UPDATE message_spool ` + assignments + ` FROM allocated WHERE message_id=$1`
	var result sql.Result
	var err error
	if argument == nil {
		result, err = store.db.ExecContext(ctx, statement, messageID)
	} else {
		result, err = store.db.ExecContext(ctx, statement, messageID, argument)
	}
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
	return nil
}

// ClaimDueReceipts leases owed receipts exclusively, mirroring the SKIP LOCKED
// claim rest_batch_tasks uses. An expired lease is re-claimable in the same
// predicate rather than through a separate recovery sweep, so a receipt cannot
// sit unclaimed because the process that leased it died.
//
// The claim deliberately does not re-allocate seq: a receipt lease is gateway
// bookkeeping, and bumping the cursor would re-deliver every message to pull
// consumers a second time for no reason they could act on.
func (store *PostgresMessageSpool) ClaimDueReceipts(
	ctx context.Context,
	owner string,
	now time.Time,
	lease time.Duration,
	limit int,
) ([]msgspool.Record, error) {
	if owner == "" || lease <= 0 || limit < 1 || limit > 1000 {
		return nil, msgspool.ErrInvalidInput
	}
	rows, err := store.db.QueryContext(ctx, `WITH candidate AS (
 SELECT message_id FROM message_spool
 WHERE receipt_due_at IS NOT NULL AND receipt_sent_at IS NULL AND receipt_due_at<=$2
   AND (receipt_locked_until IS NULL OR receipt_locked_until<=$2)
 ORDER BY receipt_due_at,seq
 FOR UPDATE SKIP LOCKED
 LIMIT $4)
 UPDATE message_spool AS spool
 SET receipt_lock_owner=$1,receipt_locked_until=$3
 FROM candidate WHERE spool.message_id=candidate.message_id
 RETURNING spool.message_id,spool.seq,spool.connector_id,spool.user_id,spool.source_addr,
 spool.dest_addr,'',''::bytea,spool.data_coding,spool.encoding,spool.parts,
 spool.verdict_accept,spool.verdict_stat,spool.verdict_err,spool.verdict_reason,
 spool.gate_bypassed,spool.delivery_state,spool.delivery_attempts,spool.received_at,
 spool.delivered_at,spool.next_attempt_at,spool.receipt_due_at,spool.receipt_sent_at,
 COALESCE(spool.receipt_lock_owner,''),spool.receipt_locked_until,spool.created_at,
 spool.receipt_only`,
		owner, now.UTC(), now.Add(lease).UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// The receipt carries no message content, so the claim does not read any:
	// the DLR path has no business holding an OTP body.
	claimed, err := collectPostgresSpoolRecords(rows, limit, true)
	if err != nil {
		return nil, err
	}
	sortSpoolReceiptClaims(claimed)
	return claimed, nil
}

func (store *PostgresMessageSpool) MarkReceiptSent(
	ctx context.Context,
	messageID string,
	owner string,
	at time.Time,
) error {
	if owner == "" {
		return msgspool.ErrInvalidInput
	}
	result, err := store.db.ExecContext(ctx, `UPDATE message_spool
 SET receipt_sent_at=$3,receipt_lock_owner=NULL,receipt_locked_until=NULL
 WHERE message_id=$1 AND receipt_lock_owner=$2 AND receipt_sent_at IS NULL`,
		messageID, owner, at.UTC())
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

// Prune enforces the retention window on message age. Rows are taken in batches
// with SKIP LOCKED so a prune running beside live writes never blocks them, and
// pending or dead-lettered rows are not exempt: the retention window is what
// bounds spool growth during a downstream outage.
func (store *PostgresMessageSpool) Prune(
	ctx context.Context,
	olderThan time.Time,
	batch int,
) (msgspool.PruneResult, error) {
	if olderThan.IsZero() || batch < 1 || batch > 10000 {
		return msgspool.PruneResult{}, msgspool.ErrInvalidInput
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return msgspool.PruneResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT message_id FROM message_spool
 WHERE received_at<$1 ORDER BY received_at,message_id LIMIT $2 FOR UPDATE SKIP LOCKED`,
		olderThan.UTC(), batch)
	if err != nil {
		return msgspool.PruneResult{}, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return msgspool.PruneResult{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return msgspool.PruneResult{}, err
	}
	if len(ids) == 0 {
		return msgspool.PruneResult{}, tx.Commit()
	}
	result, err := tx.ExecContext(ctx,
		`DELETE FROM message_spool WHERE message_id=ANY($1::text[])`, ids)
	if err != nil {
		return msgspool.PruneResult{}, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return msgspool.PruneResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return msgspool.PruneResult{}, err
	}
	return msgspool.PruneResult{Records: deleted}, nil
}

// Census counts rows per connector for the operator gauges.
//
// One grouped scan, not three counting queries: the three numbers are read
// together on a dashboard, and taking them from separate statements would let a
// concurrent delivery land between them and report a dead-letter depth larger
// than the spool that holds it.
func (store *PostgresMessageSpool) Census(
	ctx context.Context,
	now time.Time,
) ([]msgspool.ConnectorCensus, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT connector_id,
 COUNT(*),
 COUNT(*) FILTER (WHERE delivery_state='dlq'),
 COUNT(*) FILTER (WHERE receipt_sent_at IS NULL AND receipt_due_at IS NOT NULL AND receipt_due_at<=$1)
 FROM message_spool GROUP BY connector_id ORDER BY connector_id`, now.UTC())
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

func (store *PostgresMessageSpool) AuditAccess(ctx context.Context, audit msgspool.AccessAudit) error {
	if audit.OccurredAt.IsZero() || audit.Action == "" {
		return msgspool.ErrInvalidInput
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO message_spool_access_audit(
 subject,action,target,allowed,row_count,occurred_at) VALUES($1,$2,$3,$4,$5,$6)`,
		audit.Subject, audit.Action, audit.Target, audit.Allowed,
		audit.RowCount, audit.OccurredAt.UTC())
	return err
}

func collectPostgresSpoolRecords(
	rows *sql.Rows,
	limit int,
	redacted bool,
) ([]msgspool.Record, error) {
	records := make([]msgspool.Record, 0, limit)
	for rows.Next() {
		var record msgspool.Record
		if err := scanPostgresSpoolRecord(rows, &record); err != nil {
			return nil, err
		}
		record.ContentRedacted = redacted
		records = append(records, record)
	}
	return records, rows.Err()
}

func scanPostgresSpoolRecord(
	scanner interface{ Scan(...any) error },
	record *msgspool.Record,
) error {
	var dataCoding int16
	var delivered, nextAttempt, receiptDue, receiptSent, lockedUntil sql.NullTime
	if err := scanner.Scan(
		&record.MessageID, &record.Sequence, &record.ConnectorID, &record.UserID,
		&record.SourceAddr, &record.DestAddr, &record.Text, &record.Raw, &dataCoding,
		&record.Encoding, &record.Parts, &record.Verdict.Accept, &record.Verdict.Stat,
		&record.Verdict.Err, &record.Verdict.Reason, &record.Verdict.GateBypassed,
		&record.DeliveryState, &record.DeliveryAttempts, &record.ReceivedAt, &delivered,
		&nextAttempt, &receiptDue, &receiptSent, &record.ReceiptLockOwner, &lockedUntil,
		&record.CreatedAt, &record.ReceiptOnly,
	); err != nil {
		return err
	}
	record.DataCoding = byte(dataCoding)
	record.ReceivedAt = record.ReceivedAt.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	record.DeliveredAt = nullTimeValue(delivered)
	record.NextAttemptAt = nullTimeValue(nextAttempt)
	record.ReceiptDueAt = nullTimeValue(receiptDue)
	record.ReceiptSentAt = nullTimeValue(receiptSent)
	record.ReceiptLockedUntil = nullTimeValue(lockedUntil)
	return nil
}

// sortSpoolReceiptClaims puts a claimed batch back into due order. Neither
// backend guarantees RETURNING order, and a receipt batch emitted out of order
// would hand two partners their receipts in the wrong sequence.
func sortSpoolReceiptClaims(records []msgspool.Record) {
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i].ReceiptDueAt, records[j].ReceiptDueAt
		switch {
		case left == nil && right == nil:
			return records[i].Sequence < records[j].Sequence
		case left == nil:
			return false
		case right == nil:
			return true
		case left.Equal(*right):
			return records[i].Sequence < records[j].Sequence
		default:
			return left.Before(*right)
		}
	})
}

// connectorIDArray renders a scoped-consumer allow-list as a PostgreSQL text[]
// parameter, or NULL when there is no restriction.
func connectorIDArray(connectors []string) any {
	if len(connectors) == 0 {
		return nil
	}
	return connectors
}

// spoolReceiptOnlyClause is the msgspool.Query.IncludeReceiptOnly predicate.
//
// One shared function for both backends because the clause is identical SQL and
// must never diverge: a restriction enforced in PostgreSQL and not in SQLite is
// a leak that only shows up in production.
//
// Note the inversion — the restriction is the default. A caller that forgets
// this filter gets messages; a caller that wants per-segment receipt rows has to
// ask. That is the safe direction: handing a pull consumer a fragment it would
// store as the message is worse than never handing it the message.
func spoolReceiptOnlyClause(includeReceiptOnly bool) string {
	if includeReceiptOnly {
		return ""
	}
	return " AND NOT receipt_only"
}

func nullTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullTimeValue(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	converted := value.Time.UTC()
	return &converted
}

var _ msgspool.Repository = (*PostgresMessageSpool)(nil)

// AccessActivity aggregates the audit table for the named subjects.
//
// Grouping in SQL rather than streaming rows is deliberate: the audit table is
// append-only and unbounded, and an operator opening a console page must not
// pull every row a busy consumer ever wrote.
func (store *PostgresMessageSpool) AccessActivity(
	ctx context.Context,
	subjects []string,
	since time.Time,
) ([]msgspool.SubjectActivity, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(subjects))
	args := make([]any, 0, len(subjects)+1)
	for index, subject := range subjects {
		placeholders = append(placeholders, fmt.Sprintf("$%d", index+1))
		args = append(args, subject)
	}
	_ = len(placeholders)
	query := `SELECT subject,
 COUNT(*) FILTER (WHERE allowed),
 COALESCE(SUM(CASE WHEN allowed THEN row_count ELSE 0 END),0),
 COUNT(*) FILTER (WHERE NOT allowed),
 MAX(CASE WHEN allowed THEN occurred_at END)
 FROM message_spool_access_audit
 WHERE subject IN (` + strings.Join(placeholders, ",") + `)`
	if !since.IsZero() {
		query += " AND occurred_at >= " + fmt.Sprintf("$%d", len(args)+1)
		args = append(args, since.UTC())
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
		var last sql.NullTime
		if err := rows.Scan(&entry.Subject, &entry.Reads, &entry.Rows, &entry.Denied, &last); err != nil {
			return nil, err
		}
		if last.Valid {
			at := last.Time.UTC()
			entry.LastReadAt = &at
		}
		activity = append(activity, entry)
	}
	return activity, rows.Err()
}

// spoolSequenceBound and spoolSequenceOrder page the spool in either direction
// off the same cursor column.
//
// Ascending is the partner pull API: seq strictly above the cursor, walking
// forward through everything exactly once. Descending is the operator console:
// seq strictly below it, newest first. "Start at the newest" arrives as a
// sentinel cursor of MaxInt64 rather than a conditional bound, because SQLite's
// positional ? cannot reuse one argument across two comparisons.
func spoolSequenceBound(descending bool, placeholder string) string {
	if descending {
		return "seq<" + placeholder
	}
	return "seq>" + placeholder
}

func spoolSequenceOrder(descending bool) string {
	if descending {
		return "ORDER BY seq DESC"
	}
	return "ORDER BY seq"
}
