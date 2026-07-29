package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/cdr"
)

const sqliteCDRSchema = `
CREATE TABLE IF NOT EXISTS cdr_records (
 cdr_id TEXT PRIMARY KEY, message_id TEXT NOT NULL,
 part_number INTEGER NOT NULL CHECK(part_number > 0),
 part_count INTEGER NOT NULL CHECK(part_count >= part_number),
 user_id TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL DEFAULT '',
 route_id TEXT NOT NULL, connector_id TEXT NOT NULL, ingress TEXT NOT NULL DEFAULT '',
 bill_id TEXT NOT NULL DEFAULT '', rate REAL NOT NULL CHECK(rate >= 0),
 currency TEXT NOT NULL CHECK(length(currency) = 3),
 early_amount REAL NOT NULL CHECK(early_amount >= 0),
 late_amount REAL NOT NULL CHECK(late_amount >= 0),
 billing_mode TEXT NOT NULL CHECK(billing_mode IN ('FREE','PREPAID','POSTPAID','SPLIT')),
 state TEXT NOT NULL CHECK(state IN ('ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND','SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT')),
 attempt_id INTEGER, smpp_status TEXT NOT NULL DEFAULT '', smsc_message_id TEXT,
 delivery_state TEXT CHECK(delivery_state IS NULL OR delivery_state IN ('DELIVERED','EXPIRED','DELETED','UNDELIVERABLE','REJECTED')),
 delivery_status TEXT NOT NULL DEFAULT '', delivery_error TEXT NOT NULL DEFAULT '',
 delivery_done_at INTEGER, delivery_received_at INTEGER,
 billing_outcome TEXT NOT NULL DEFAULT 'NOT_APPLICABLE' CHECK(billing_outcome IN ('NOT_APPLICABLE','PENDING','APPLIED','REJECTED')),
 actual_late_amount REAL NOT NULL DEFAULT 0 CHECK(actual_late_amount >= 0),
 late_billing_at INTEGER,
 admitted_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, terminal_at INTEGER
);
CREATE INDEX IF NOT EXISTS cdr_records_message_id ON cdr_records(message_id,part_number);
CREATE INDEX IF NOT EXISTS cdr_records_user_time ON cdr_records(user_id,admitted_at,cdr_id);
CREATE INDEX IF NOT EXISTS cdr_records_terminal_time ON cdr_records(terminal_at,cdr_id) WHERE terminal_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS cdr_events (
 event_key TEXT PRIMARY KEY, cdr_id TEXT NOT NULL REFERENCES cdr_records(cdr_id),
 kind TEXT NOT NULL CHECK(kind IN ('ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND','SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT','FINAL_DLR','LATE_BILLING_APPLIED','LATE_BILLING_REJECTED')),
 state TEXT NOT NULL CHECK(state IN ('ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND','SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT')),
 attempt_id INTEGER, smpp_status TEXT NOT NULL DEFAULT '', smsc_message_id TEXT,
 delivery_state TEXT, delivery_status TEXT NOT NULL DEFAULT '', delivery_error TEXT NOT NULL DEFAULT '',
 delivery_done_at INTEGER, billing_outcome TEXT, actual_late_amount REAL NOT NULL DEFAULT 0,
 occurred_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS cdr_events_record_time ON cdr_events(cdr_id,occurred_at,event_key);
CREATE TABLE IF NOT EXISTS cdr_access_audit (
 audit_id INTEGER PRIMARY KEY AUTOINCREMENT, subject TEXT NOT NULL, action TEXT NOT NULL,
 target TEXT NOT NULL, allowed INTEGER NOT NULL CHECK(allowed IN (0,1)), occurred_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS cdr_access_audit_time ON cdr_access_audit(occurred_at,audit_id);`

func insertSQLiteCDRAdmission(ctx context.Context, tx *sql.Tx, value cdr.Admission) error {
	if err := cdr.ValidateAdmission(value); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO cdr_records(
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,billing_outcome,admitted_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.MessageID, value.PartNumber, value.PartCount,
		value.UserID, value.GroupID, value.RouteID, value.ConnectorID, value.Ingress, value.BillID,
		value.Rate, value.Currency, value.EarlyAmount, value.LateAmount, value.BillingMode,
		cdr.StateAdmitted, initialBillingOutcome(value.LateAmount),
		nanos(value.OccurredAt), nanos(value.OccurredAt),
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO cdr_events(
 event_key,cdr_id,kind,state,occurred_at
) VALUES(?,?,?,?,?)`,
		cdr.AdmissionEventKey(value.ID), value.ID, cdr.EventAdmitted,
		cdr.StateAdmitted, nanos(value.OccurredAt),
	)
	return err
}

func recordSQLiteCDRTransition(
	ctx context.Context,
	tx *sql.Tx,
	event cdr.Event,
) error {
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO cdr_events(
 event_key,cdr_id,kind,state,attempt_id,smpp_status,smsc_message_id,occurred_at
) VALUES(?,?,?,?,?,?,?,?)`,
		event.Key, event.CDRID, event.Kind, event.State, nullableInt64(event.AttemptID),
		event.SMPPStatus, nullString(event.SMSCMessageID), nanos(event.OccurredAt),
	)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted == 0 {
		return err
	}
	var terminalAt any
	if event.State.Terminal() {
		terminalAt = nanos(event.OccurredAt)
	}
	_, err = tx.ExecContext(ctx, `UPDATE cdr_records SET
 state=?,attempt_id=?,smpp_status=?,smsc_message_id=?,updated_at=?,terminal_at=?
 WHERE cdr_id=?`,
		event.State, nullableInt64(event.AttemptID), event.SMPPStatus,
		nullString(event.SMSCMessageID), nanos(event.OccurredAt), terminalAt, event.CDRID,
	)
	return err
}

func recordSQLiteLateBillingOutcome(
	ctx context.Context,
	tx *sql.Tx,
	intentKey string,
	outcome cdr.BillingOutcome,
	occurredAt time.Time,
) error {
	if outcome != cdr.BillingApplied && outcome != cdr.BillingRejected {
		return cdr.ErrInvalidInput
	}
	var id string
	var state cdr.State
	var lateAmount float64
	var current cdr.BillingOutcome
	if err := tx.QueryRowContext(ctx, `SELECT c.cdr_id,c.state,c.late_amount,c.billing_outcome
 FROM submit_billing_intents b JOIN cdr_records c ON c.cdr_id=b.part_key
 WHERE b.event_key=?`, intentKey).Scan(&id, &state, &lateAmount, &current); err != nil {
		return err
	}
	if current == outcome {
		return nil
	}
	if current == cdr.BillingApplied || current == cdr.BillingRejected ||
		current == cdr.BillingNotApplicable {
		return fmt.Errorf("cdr %q late billing is already %s", id, current)
	}
	kind := cdr.EventLateBillApplied
	actual := lateAmount
	if outcome == cdr.BillingRejected {
		kind = cdr.EventLateBillRejected
		actual = 0
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO cdr_events(
 event_key,cdr_id,kind,state,billing_outcome,actual_late_amount,occurred_at
) VALUES(?,?,?,?,?,?,?)`,
		cdr.LateBillingEventKey(id, outcome), id, kind, state, outcome, actual,
		nanos(occurredAt)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE cdr_records SET
 billing_outcome=?,actual_late_amount=?,late_billing_at=COALESCE(late_billing_at,?),
 updated_at=MAX(updated_at,?)
 WHERE cdr_id=? AND billing_outcome='PENDING'`,
		outcome, actual, nanos(occurredAt), nanos(occurredAt), id)
	return err
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func (r *SQLiteSubmitTransactionRepository) GetCDR(ctx context.Context, id string) (cdr.Record, error) {
	var record cdr.Record
	var attempt sql.NullInt64
	var smsc sql.NullString
	var admitted, updated int64
	var terminal, deliveryDone, deliveryReceived, lateBilling sql.NullInt64
	var deliveryState sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,attempt_id,smpp_status,smsc_message_id,
 delivery_state,delivery_status,delivery_error,delivery_done_at,delivery_received_at,
 billing_outcome,actual_late_amount,late_billing_at,
 admitted_at,updated_at,terminal_at
 FROM cdr_records WHERE cdr_id=?`, id).Scan(
		&record.ID, &record.MessageID, &record.PartNumber, &record.PartCount,
		&record.UserID, &record.GroupID, &record.RouteID, &record.ConnectorID,
		&record.Ingress, &record.BillID, &record.Rate, &record.Currency,
		&record.EarlyAmount, &record.LateAmount, &record.BillingMode, &record.State,
		&attempt, &record.SMPPStatus, &smsc, &deliveryState,
		&record.DeliveryStatus, &record.DeliveryError, &deliveryDone, &deliveryReceived,
		&record.BillingOutcome, &record.ActualLateAmount, &lateBilling,
		&admitted, &updated, &terminal,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cdr.Record{}, cdr.ErrNotFound
	}
	if err != nil {
		return cdr.Record{}, err
	}
	record.AttemptID = attempt.Int64
	record.SMSCMessageID = smsc.String
	record.DeliveryState = cdr.DeliveryState(deliveryState.String)
	record.DeliveryDoneAt = timePtr(deliveryDone)
	record.DeliveryReceivedAt = timePtr(deliveryReceived)
	record.LateBillingAt = timePtr(lateBilling)
	record.OccurredAt = time.Unix(0, admitted).UTC()
	record.UpdatedAt = time.Unix(0, updated).UTC()
	record.TerminalAt = timePtr(terminal)
	return record, nil
}

func (r *SQLiteSubmitTransactionRepository) ListCDREvents(ctx context.Context, id string) ([]cdr.Event, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT
 event_key,cdr_id,kind,state,attempt_id,smpp_status,smsc_message_id,
 delivery_state,delivery_status,delivery_error,delivery_done_at,billing_outcome,
 actual_late_amount,occurred_at
 FROM cdr_events WHERE cdr_id=? ORDER BY occurred_at,COALESCE(attempt_id,0),event_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []cdr.Event
	for rows.Next() {
		var event cdr.Event
		var attempt sql.NullInt64
		var smsc sql.NullString
		var occurred int64
		var deliveryState, billingOutcome sql.NullString
		var deliveryDone sql.NullInt64
		if err := rows.Scan(
			&event.Key, &event.CDRID, &event.Kind, &event.State, &attempt,
			&event.SMPPStatus, &smsc, &deliveryState, &event.DeliveryStatus,
			&event.DeliveryError, &deliveryDone, &billingOutcome,
			&event.ActualLateAmount, &occurred,
		); err != nil {
			return nil, err
		}
		event.AttemptID = attempt.Int64
		event.SMSCMessageID = smsc.String
		event.DeliveryState = cdr.DeliveryState(deliveryState.String)
		event.DeliveryDoneAt = timePtr(deliveryDone)
		event.BillingOutcome = cdr.BillingOutcome(billingOutcome.String)
		event.OccurredAt = time.Unix(0, occurred).UTC()
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *SQLiteSubmitTransactionRepository) RecordFinalDLR(ctx context.Context, value cdr.FinalDLR) error {
	state, err := cdr.DeliveryStateForStatus(value.Status)
	if err != nil || value.ReceivedAt.IsZero() || strings.TrimSpace(value.QueueMessageID) == "" {
		return cdr.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	var submissionState cdr.State
	err = tx.QueryRowContext(ctx, `SELECT cdr_id,state FROM cdr_records
 WHERE cdr_id=? OR (message_id=? AND part_count=1)
 ORDER BY CASE WHEN cdr_id=? THEN 0 ELSE 1 END LIMIT 1`,
		value.QueueMessageID, value.QueueMessageID, value.QueueMessageID).Scan(&id, &submissionState)
	if errors.Is(err, sql.ErrNoRows) && value.SMSCMessageID != "" && value.ConnectorID != "" {
		rows, lookupErr := tx.QueryContext(ctx, `SELECT cdr_id,state FROM cdr_records
 WHERE connector_id=? AND lower(smsc_message_id)=lower(?)
 ORDER BY admitted_at DESC,cdr_id LIMIT 2`,
			value.ConnectorID, value.SMSCMessageID)
		if lookupErr != nil {
			return lookupErr
		}
		type match struct {
			id    string
			state cdr.State
		}
		var matches []match
		for rows.Next() {
			var current match
			if lookupErr = rows.Scan(&current.id, &current.state); lookupErr != nil {
				rows.Close()
				return lookupErr
			}
			matches = append(matches, current)
		}
		if lookupErr = rows.Close(); lookupErr != nil {
			return lookupErr
		}
		switch len(matches) {
		case 0:
			err = sql.ErrNoRows
		case 1:
			id, submissionState, err = matches[0].id, matches[0].state, nil
		default:
			return fmt.Errorf("%w: ambiguous SMSC message id", cdr.ErrInvalidInput)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return cdr.ErrNotFound
	}
	if err != nil {
		return err
	}
	if submissionState != cdr.StateSMSCAccepted {
		return fmt.Errorf("cdr %q cannot accept final DLR from submission state %s", id, submissionState)
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO cdr_events(
 event_key,cdr_id,kind,state,delivery_state,delivery_status,delivery_error,
 delivery_done_at,occurred_at
) VALUES(?,?,?,?,?,?,?,?,?)`,
		cdr.FinalDLREventKey(id), id, cdr.EventFinalDLR, submissionState,
		state, strings.ToUpper(value.Status), value.Error, optionalNanos(value.DoneAt),
		nanos(value.ReceivedAt))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 {
		_, err = tx.ExecContext(ctx, `UPDATE cdr_records SET
 delivery_state=?,delivery_status=?,delivery_error=?,delivery_done_at=?,
 delivery_received_at=?,updated_at=MAX(updated_at,?) WHERE cdr_id=?`,
			state, strings.ToUpper(value.Status), value.Error, optionalNanos(value.DoneAt),
			nanos(value.ReceivedAt), nanos(value.ReceivedAt), id)
		if err != nil {
			return err
		}
	} else {
		var current string
		if err = tx.QueryRowContext(ctx, `SELECT delivery_status FROM cdr_records WHERE cdr_id=?`, id).Scan(&current); err != nil {
			return err
		}
		if current != strings.ToUpper(value.Status) {
			return fmt.Errorf("%w: contradictory final DLR %s after %s", cdr.ErrInvalidInput, value.Status, current)
		}
	}
	return tx.Commit()
}

func (r *SQLiteSubmitTransactionRepository) ExportCDRs(ctx context.Context, query cdr.ExportQuery) ([]cdr.Record, error) {
	if query.Limit < 1 || query.Limit > 10000 {
		return nil, cdr.ErrInvalidInput
	}
	after := int64(0)
	if !query.After.IsZero() {
		after = nanos(query.After)
	}
	from, to := int64(0), int64(0)
	if query.AdmittedFrom != nil {
		from = nanos(*query.AdmittedFrom)
	}
	if query.AdmittedTo != nil {
		to = nanos(*query.AdmittedTo)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,attempt_id,smpp_status,smsc_message_id,
 delivery_state,delivery_status,delivery_error,delivery_done_at,delivery_received_at,
 billing_outcome,actual_late_amount,late_billing_at,admitted_at,updated_at,terminal_at
 FROM cdr_records
 WHERE (?=0 OR admitted_at>? OR (admitted_at=? AND cdr_id>?))
   AND (?='' OR user_id=?)
   AND (?=0 OR admitted_at>=?)
   AND (?=0 OR admitted_at<?)
 ORDER BY admitted_at,cdr_id LIMIT ?`,
		after, after, after, query.AfterID, query.UserID, query.UserID,
		from, from, to, to, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]cdr.Record, 0, query.Limit)
	for rows.Next() {
		var record cdr.Record
		if err := scanSQLiteCDR(rows, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func scanSQLiteCDR(scanner interface{ Scan(...any) error }, record *cdr.Record) error {
	var attempt sql.NullInt64
	var smsc, deliveryState sql.NullString
	var deliveryDone, deliveryReceived, lateBilling, terminal sql.NullInt64
	var admitted, updated int64
	if err := scanner.Scan(
		&record.ID, &record.MessageID, &record.PartNumber, &record.PartCount,
		&record.UserID, &record.GroupID, &record.RouteID, &record.ConnectorID,
		&record.Ingress, &record.BillID, &record.Rate, &record.Currency,
		&record.EarlyAmount, &record.LateAmount, &record.BillingMode, &record.State,
		&attempt, &record.SMPPStatus, &smsc, &deliveryState,
		&record.DeliveryStatus, &record.DeliveryError, &deliveryDone, &deliveryReceived,
		&record.BillingOutcome, &record.ActualLateAmount, &lateBilling,
		&admitted, &updated, &terminal,
	); err != nil {
		return err
	}
	record.AttemptID = attempt.Int64
	record.SMSCMessageID = smsc.String
	record.DeliveryState = cdr.DeliveryState(deliveryState.String)
	record.DeliveryDoneAt = timePtr(deliveryDone)
	record.DeliveryReceivedAt = timePtr(deliveryReceived)
	record.LateBillingAt = timePtr(lateBilling)
	record.OccurredAt = time.Unix(0, admitted).UTC()
	record.UpdatedAt = time.Unix(0, updated).UTC()
	record.TerminalAt = timePtr(terminal)
	return nil
}

func (r *SQLiteSubmitTransactionRepository) PruneCDRs(ctx context.Context, cutoff time.Time, batch int) (cdr.PruneResult, error) {
	if cutoff.IsZero() || batch < 1 || batch > 10000 {
		return cdr.PruneResult{}, cdr.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return cdr.PruneResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT cdr_id FROM cdr_records
 WHERE COALESCE(delivery_received_at,terminal_at)<?
   AND billing_outcome<>'PENDING'
 ORDER BY COALESCE(delivery_received_at,terminal_at),cdr_id LIMIT ?`,
		nanos(cutoff), batch)
	if err != nil {
		return cdr.PruneResult{}, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return cdr.PruneResult{}, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return cdr.PruneResult{}, err
	}
	var result cdr.PruneResult
	for _, id := range ids {
		deletedEvents, err := tx.ExecContext(ctx, `DELETE FROM cdr_events WHERE cdr_id=?`, id)
		if err != nil {
			return cdr.PruneResult{}, err
		}
		count, _ := deletedEvents.RowsAffected()
		result.Events += count
		deletedRecord, err := tx.ExecContext(ctx, `DELETE FROM cdr_records WHERE cdr_id=?`, id)
		if err != nil {
			return cdr.PruneResult{}, err
		}
		count, _ = deletedRecord.RowsAffected()
		result.Records += count
	}
	if err = tx.Commit(); err != nil {
		return cdr.PruneResult{}, err
	}
	return result, nil
}

func (r *SQLiteSubmitTransactionRepository) ReconcileCDRs(ctx context.Context, now time.Time) (cdr.ReconciliationReport, error) {
	report := cdr.ReconciliationReport{CheckedAt: now.UTC()}
	checks := []struct {
		code string
		sql  string
	}{
		{"CDR_WITHOUT_SUBMIT_PART", `SELECT count(*) FROM cdr_records c LEFT JOIN submit_parts p ON p.part_key=c.cdr_id WHERE p.part_key IS NULL`},
		{"SUBMIT_PART_WITHOUT_CDR", `SELECT count(*) FROM submit_parts p LEFT JOIN cdr_records c ON c.cdr_id=p.part_key WHERE c.cdr_id IS NULL`},
		{"FINAL_RESULT_STATE_MISMATCH", `SELECT count(*) FROM submit_results r JOIN cdr_records c ON c.cdr_id=r.part_key
		 WHERE r.kind<>'RETRY' AND ((r.kind='SUCCESS' AND c.state<>'SMSC_ACCEPTED') OR
		 (r.kind='FAILURE' AND c.state<>'SMSC_REJECTED') OR
		 (r.kind='TIMEOUT' AND c.state<>'TERMINAL_TIMEOUT'))`},
		{"ACCEPTED_LATE_INTENT_MISSING", `SELECT count(*) FROM cdr_records c LEFT JOIN submit_billing_intents b
		 ON b.part_key=c.cdr_id WHERE c.state='SMSC_ACCEPTED' AND c.late_amount>0 AND b.event_key IS NULL`},
		{"BILLING_LEDGER_PROJECTION_MISMATCH", `SELECT count(*) FROM cdr_records c JOIN submit_billing_intents b
		 ON b.part_key=c.cdr_id WHERE (b.applied_at IS NOT NULL)<>(c.billing_outcome='APPLIED')`},
		{"FINAL_DLR_EVENT_PROJECTION_MISMATCH", `SELECT count(*) FROM cdr_records c
		 WHERE (c.delivery_received_at IS NOT NULL)<>EXISTS (
		 SELECT 1 FROM cdr_events e WHERE e.cdr_id=c.cdr_id AND e.kind='FINAL_DLR')`},
	}
	for _, check := range checks {
		var count int64
		if err := r.db.QueryRowContext(ctx, check.sql).Scan(&count); err != nil {
			return cdr.ReconciliationReport{}, fmt.Errorf("cdr reconciliation %s: %w", check.code, err)
		}
		report.Issues = append(report.Issues, cdr.ReconciliationIssue{Code: check.code, Count: count})
	}
	return report, nil
}

func (r *SQLiteSubmitTransactionRepository) AuditCDRAccess(ctx context.Context, audit cdr.AccessAudit) error {
	if audit.OccurredAt.IsZero() || strings.TrimSpace(string(audit.Action)) == "" {
		return cdr.ErrInvalidInput
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO cdr_access_audit(
 subject,action,target,allowed,occurred_at) VALUES(?,?,?,?,?)`,
		audit.Subject, audit.Action, audit.Target, audit.Allowed, nanos(audit.OccurredAt))
	return err
}

var (
	_ cdr.Repository           = (*SQLiteSubmitTransactionRepository)(nil)
	_ cdr.FinalDLRRecorder     = (*SQLiteSubmitTransactionRepository)(nil)
	_ cdr.OperationsRepository = (*SQLiteSubmitTransactionRepository)(nil)
)
