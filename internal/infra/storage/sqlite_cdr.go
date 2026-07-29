package storage

import (
	"context"
	"database/sql"
	"errors"
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
 admitted_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, terminal_at INTEGER
);
CREATE INDEX IF NOT EXISTS cdr_records_message_id ON cdr_records(message_id,part_number);
CREATE INDEX IF NOT EXISTS cdr_records_user_time ON cdr_records(user_id,admitted_at,cdr_id);
CREATE INDEX IF NOT EXISTS cdr_records_terminal_time ON cdr_records(terminal_at,cdr_id) WHERE terminal_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS cdr_events (
 event_key TEXT PRIMARY KEY, cdr_id TEXT NOT NULL REFERENCES cdr_records(cdr_id),
 kind TEXT NOT NULL CHECK(kind IN ('ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND','SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT')),
 state TEXT NOT NULL CHECK(state IN ('ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND','SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT')),
 attempt_id INTEGER, smpp_status TEXT NOT NULL DEFAULT '', smsc_message_id TEXT,
 occurred_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS cdr_events_record_time ON cdr_events(cdr_id,occurred_at,event_key);`

func insertSQLiteCDRAdmission(ctx context.Context, tx *sql.Tx, value cdr.Admission) error {
	if err := cdr.ValidateAdmission(value); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO cdr_records(
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,admitted_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.MessageID, value.PartNumber, value.PartCount,
		value.UserID, value.GroupID, value.RouteID, value.ConnectorID, value.Ingress, value.BillID,
		value.Rate, value.Currency, value.EarlyAmount, value.LateAmount, value.BillingMode,
		cdr.StateAdmitted, nanos(value.OccurredAt), nanos(value.OccurredAt),
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
	var terminal sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,attempt_id,smpp_status,smsc_message_id,
 admitted_at,updated_at,terminal_at
 FROM cdr_records WHERE cdr_id=?`, id).Scan(
		&record.ID, &record.MessageID, &record.PartNumber, &record.PartCount,
		&record.UserID, &record.GroupID, &record.RouteID, &record.ConnectorID,
		&record.Ingress, &record.BillID, &record.Rate, &record.Currency,
		&record.EarlyAmount, &record.LateAmount, &record.BillingMode, &record.State,
		&attempt, &record.SMPPStatus, &smsc, &admitted, &updated, &terminal,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cdr.Record{}, cdr.ErrNotFound
	}
	if err != nil {
		return cdr.Record{}, err
	}
	record.AttemptID = attempt.Int64
	record.SMSCMessageID = smsc.String
	record.OccurredAt = time.Unix(0, admitted).UTC()
	record.UpdatedAt = time.Unix(0, updated).UTC()
	record.TerminalAt = timePtr(terminal)
	return record, nil
}

func (r *SQLiteSubmitTransactionRepository) ListCDREvents(ctx context.Context, id string) ([]cdr.Event, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT
 event_key,cdr_id,kind,state,attempt_id,smpp_status,smsc_message_id,occurred_at
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
		if err := rows.Scan(
			&event.Key, &event.CDRID, &event.Kind, &event.State, &attempt,
			&event.SMPPStatus, &smsc, &occurred,
		); err != nil {
			return nil, err
		}
		event.AttemptID = attempt.Int64
		event.SMSCMessageID = smsc.String
		event.OccurredAt = time.Unix(0, occurred).UTC()
		events = append(events, event)
	}
	return events, rows.Err()
}

var _ cdr.Repository = (*SQLiteSubmitTransactionRepository)(nil)
