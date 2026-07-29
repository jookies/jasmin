package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pumpitspace/jasmin/internal/core/cdr"
)

func insertPostgresCDRAdmission(ctx context.Context, tx *sql.Tx, value cdr.Admission) error {
	if err := cdr.ValidateAdmission(value); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO cdr_records(
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,admitted_at,updated_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)
 ON CONFLICT(cdr_id) DO NOTHING`,
		value.ID, value.MessageID, value.PartNumber, value.PartCount,
		value.UserID, value.GroupID, value.RouteID, value.ConnectorID, value.Ingress, value.BillID,
		value.Rate, value.Currency, value.EarlyAmount, value.LateAmount, value.BillingMode,
		cdr.StateAdmitted, value.OccurredAt,
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cdr_events(
 event_key,cdr_id,kind,state,occurred_at
) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_key) DO NOTHING`,
		cdr.AdmissionEventKey(value.ID), value.ID, cdr.EventAdmitted,
		cdr.StateAdmitted, value.OccurredAt,
	)
	return err
}

func recordPostgresCDRTransition(ctx context.Context, tx *sql.Tx, event cdr.Event) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO cdr_events(
 event_key,cdr_id,kind,state,attempt_id,smpp_status,smsc_message_id,occurred_at
) VALUES($1,$2,$3,$4,NULLIF($5,0),$6,NULLIF($7,''),$8)
 ON CONFLICT(event_key) DO NOTHING`,
		event.Key, event.CDRID, event.Kind, event.State, event.AttemptID,
		event.SMPPStatus, event.SMSCMessageID, event.OccurredAt,
	)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cdr_records SET
 state=$1,attempt_id=NULLIF($2,0),smpp_status=$3,smsc_message_id=NULLIF($4,''),
 updated_at=$5,terminal_at=CASE WHEN $6 THEN $5 ELSE NULL END
 WHERE cdr_id=$7`,
		event.State, event.AttemptID, event.SMPPStatus, event.SMSCMessageID,
		event.OccurredAt, event.State.Terminal(), event.CDRID,
	)
	return err
}

func (r *PostgresSubmitTransactionRepository) GetCDR(ctx context.Context, id string) (cdr.Record, error) {
	var record cdr.Record
	err := r.db.QueryRowContext(ctx, `SELECT
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,
 COALESCE(attempt_id,0),smpp_status,COALESCE(smsc_message_id,''),
 admitted_at,updated_at,terminal_at
 FROM cdr_records WHERE cdr_id=$1`, id).Scan(
		&record.ID, &record.MessageID, &record.PartNumber, &record.PartCount,
		&record.UserID, &record.GroupID, &record.RouteID, &record.ConnectorID,
		&record.Ingress, &record.BillID, &record.Rate, &record.Currency,
		&record.EarlyAmount, &record.LateAmount, &record.BillingMode, &record.State,
		&record.AttemptID, &record.SMPPStatus, &record.SMSCMessageID,
		&record.OccurredAt, &record.UpdatedAt, &record.TerminalAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return cdr.Record{}, cdr.ErrNotFound
	}
	if err != nil {
		return cdr.Record{}, err
	}
	return record, nil
}

func (r *PostgresSubmitTransactionRepository) ListCDREvents(ctx context.Context, id string) ([]cdr.Event, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT
 event_key,cdr_id,kind,state,COALESCE(attempt_id,0),smpp_status,
 COALESCE(smsc_message_id,''),occurred_at
 FROM cdr_events WHERE cdr_id=$1 ORDER BY occurred_at,COALESCE(attempt_id,0),event_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []cdr.Event
	for rows.Next() {
		var event cdr.Event
		if err := rows.Scan(
			&event.Key, &event.CDRID, &event.Kind, &event.State, &event.AttemptID,
			&event.SMPPStatus, &event.SMSCMessageID, &event.OccurredAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

var _ cdr.Repository = (*PostgresSubmitTransactionRepository)(nil)
