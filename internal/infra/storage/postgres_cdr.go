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

func initialBillingOutcome(lateAmount float64) cdr.BillingOutcome {
	if lateAmount > 0 {
		return cdr.BillingPending
	}
	return cdr.BillingNotApplicable
}

func insertPostgresCDRAdmission(ctx context.Context, tx *sql.Tx, value cdr.Admission) error {
	if err := cdr.ValidateAdmission(value); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO cdr_records(
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,billing_outcome,admitted_at,updated_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$18)
 ON CONFLICT(cdr_id) DO NOTHING`,
		value.ID, value.MessageID, value.PartNumber, value.PartCount,
		value.UserID, value.GroupID, value.RouteID, value.ConnectorID, value.Ingress, value.BillID,
		value.Rate, value.Currency, value.EarlyAmount, value.LateAmount, value.BillingMode,
		cdr.StateAdmitted, initialBillingOutcome(value.LateAmount), value.OccurredAt,
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
 updated_at=$5::timestamptz,
 terminal_at=CASE WHEN $6::boolean THEN $5::timestamptz ELSE NULL::timestamptz END
 WHERE cdr_id=$7`,
		event.State, event.AttemptID, event.SMPPStatus, event.SMSCMessageID,
		event.OccurredAt, event.State.Terminal(), event.CDRID,
	)
	return err
}

func recordPostgresLateBillingOutcome(
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
 WHERE b.event_key=$1 FOR UPDATE OF c`, intentKey).Scan(&id, &state, &lateAmount, &current); err != nil {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO cdr_events(
 event_key,cdr_id,kind,state,billing_outcome,actual_late_amount,occurred_at
) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(event_key) DO NOTHING`,
		cdr.LateBillingEventKey(id, outcome), id, kind, state, outcome, actual, occurredAt); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE cdr_records SET
 billing_outcome=$1,actual_late_amount=$2,late_billing_at=COALESCE(late_billing_at,$3),
 updated_at=GREATEST(updated_at,$3)
 WHERE cdr_id=$4 AND billing_outcome='PENDING'`,
		outcome, actual, occurredAt, id)
	return err
}

func (r *PostgresSubmitTransactionRepository) GetCDR(ctx context.Context, id string) (cdr.Record, error) {
	var record cdr.Record
	row := r.db.QueryRowContext(ctx, `SELECT
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,
 COALESCE(attempt_id,0),smpp_status,COALESCE(smsc_message_id,''),
 COALESCE(delivery_state,''),delivery_status,delivery_error,
 delivery_done_at,delivery_received_at,billing_outcome,actual_late_amount,late_billing_at,
 admitted_at,updated_at,terminal_at
 FROM cdr_records WHERE cdr_id=$1`, id)
	err := scanPostgresCDR(row, &record)
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
 COALESCE(smsc_message_id,''),COALESCE(delivery_state,''),delivery_status,
 delivery_error,delivery_done_at,COALESCE(billing_outcome,''),actual_late_amount,occurred_at
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
			&event.SMPPStatus, &event.SMSCMessageID, &event.DeliveryState,
			&event.DeliveryStatus, &event.DeliveryError, &event.DeliveryDoneAt,
			&event.BillingOutcome, &event.ActualLateAmount, &event.OccurredAt,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func scanPostgresCDR(scanner interface{ Scan(...any) error }, record *cdr.Record) error {
	return scanner.Scan(
		&record.ID, &record.MessageID, &record.PartNumber, &record.PartCount,
		&record.UserID, &record.GroupID, &record.RouteID, &record.ConnectorID,
		&record.Ingress, &record.BillID, &record.Rate, &record.Currency,
		&record.EarlyAmount, &record.LateAmount, &record.BillingMode, &record.State,
		&record.AttemptID, &record.SMPPStatus, &record.SMSCMessageID,
		&record.DeliveryState, &record.DeliveryStatus, &record.DeliveryError,
		&record.DeliveryDoneAt, &record.DeliveryReceivedAt, &record.BillingOutcome,
		&record.ActualLateAmount, &record.LateBillingAt,
		&record.OccurredAt, &record.UpdatedAt, &record.TerminalAt,
	)
}

func (r *PostgresSubmitTransactionRepository) RecordFinalDLR(ctx context.Context, value cdr.FinalDLR) error {
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
	// Multipart queue ids are exact CDR ids. A single-part submit uses the
	// aggregate queue id and therefore resolves through message_id.
	err = tx.QueryRowContext(ctx, `SELECT cdr_id FROM cdr_records
 WHERE cdr_id=$1 OR (message_id=$1 AND part_count=1)
 ORDER BY CASE WHEN cdr_id=$1 THEN 0 ELSE 1 END LIMIT 1 FOR UPDATE`,
		value.QueueMessageID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) && value.SMSCMessageID != "" && value.ConnectorID != "" {
		rows, lookupErr := tx.QueryContext(ctx, `SELECT cdr_id FROM cdr_records
 WHERE connector_id=$1 AND lower(smsc_message_id)=lower($2)
 ORDER BY admitted_at DESC,cdr_id LIMIT 2 FOR UPDATE`,
			value.ConnectorID, value.SMSCMessageID)
		if lookupErr != nil {
			return lookupErr
		}
		var matches []string
		for rows.Next() {
			var match string
			if lookupErr = rows.Scan(&match); lookupErr != nil {
				rows.Close()
				return lookupErr
			}
			matches = append(matches, match)
		}
		if lookupErr = rows.Close(); lookupErr != nil {
			return lookupErr
		}
		switch len(matches) {
		case 0:
			err = sql.ErrNoRows
		case 1:
			id, err = matches[0], nil
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
	var submissionState cdr.State
	if err = tx.QueryRowContext(ctx, `SELECT state FROM cdr_records WHERE cdr_id=$1`, id).Scan(&submissionState); err != nil {
		return err
	}
	if submissionState != cdr.StateSMSCAccepted {
		return fmt.Errorf("cdr %q cannot accept final DLR from submission state %s", id, submissionState)
	}
	eventKey := cdr.FinalDLREventKey(id)
	result, err := tx.ExecContext(ctx, `INSERT INTO cdr_events(
 event_key,cdr_id,kind,state,delivery_state,delivery_status,delivery_error,
 delivery_done_at,occurred_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(event_key) DO NOTHING`,
		eventKey, id, cdr.EventFinalDLR, submissionState, state,
		strings.ToUpper(value.Status), value.Error, value.DoneAt, value.ReceivedAt.UTC())
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 {
		_, err = tx.ExecContext(ctx, `UPDATE cdr_records SET
 delivery_state=$1,delivery_status=$2,delivery_error=$3,delivery_done_at=$4,
 delivery_received_at=$5,updated_at=GREATEST(updated_at,$5)
 WHERE cdr_id=$6`,
			state, strings.ToUpper(value.Status), value.Error, value.DoneAt,
			value.ReceivedAt.UTC(), id)
		if err != nil {
			return err
		}
	} else {
		var current string
		if err = tx.QueryRowContext(ctx, `SELECT delivery_status FROM cdr_records WHERE cdr_id=$1`, id).Scan(&current); err != nil {
			return err
		}
		if current != strings.ToUpper(value.Status) {
			return fmt.Errorf("%w: contradictory final DLR %s after %s", cdr.ErrInvalidInput, value.Status, current)
		}
	}
	return tx.Commit()
}

func (r *PostgresSubmitTransactionRepository) ExportCDRs(ctx context.Context, query cdr.ExportQuery) ([]cdr.Record, error) {
	if query.Limit < 1 || query.Limit > 10000 {
		return nil, cdr.ErrInvalidInput
	}
	rows, err := r.db.QueryContext(ctx, `SELECT
 cdr_id,message_id,part_number,part_count,user_id,group_id,route_id,connector_id,ingress,bill_id,
 rate,currency,early_amount,late_amount,billing_mode,state,
 COALESCE(attempt_id,0),smpp_status,COALESCE(smsc_message_id,''),
 COALESCE(delivery_state,''),delivery_status,delivery_error,
 delivery_done_at,delivery_received_at,billing_outcome,actual_late_amount,late_billing_at,
 admitted_at,updated_at,terminal_at
 FROM cdr_records
 WHERE ($1::timestamptz IS NULL OR (admitted_at,cdr_id)>($1,$2))
   AND ($3='' OR user_id=$3)
   AND ($4::timestamptz IS NULL OR admitted_at >= $4)
   AND ($5::timestamptz IS NULL OR admitted_at < $5)
 ORDER BY admitted_at,cdr_id LIMIT $6`,
		nullTime(query.After), query.AfterID, query.UserID, query.AdmittedFrom, query.AdmittedTo, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]cdr.Record, 0, query.Limit)
	for rows.Next() {
		var record cdr.Record
		if err := scanPostgresCDR(rows, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func (r *PostgresSubmitTransactionRepository) PruneCDRs(ctx context.Context, cutoff time.Time, batch int) (cdr.PruneResult, error) {
	if cutoff.IsZero() || batch < 1 || batch > 10000 {
		return cdr.PruneResult{}, cdr.ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return cdr.PruneResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT cdr_id FROM cdr_records
 WHERE COALESCE(delivery_received_at,terminal_at) < $1
   AND billing_outcome <> 'PENDING'
 ORDER BY COALESCE(delivery_received_at,terminal_at),cdr_id
 LIMIT $2 FOR UPDATE SKIP LOCKED`, cutoff.UTC(), batch)
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
	if len(ids) == 0 {
		return cdr.PruneResult{}, tx.Commit()
	}
	eventResult, err := tx.ExecContext(ctx, `DELETE FROM cdr_events WHERE cdr_id=ANY($1::text[])`, ids)
	if err != nil {
		return cdr.PruneResult{}, err
	}
	recordResult, err := tx.ExecContext(ctx, `DELETE FROM cdr_records WHERE cdr_id=ANY($1::text[])`, ids)
	if err != nil {
		return cdr.PruneResult{}, err
	}
	events, err := eventResult.RowsAffected()
	if err != nil {
		return cdr.PruneResult{}, err
	}
	records, err := recordResult.RowsAffected()
	if err != nil {
		return cdr.PruneResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return cdr.PruneResult{}, err
	}
	return cdr.PruneResult{Records: records, Events: events}, nil
}

func (r *PostgresSubmitTransactionRepository) ReconcileCDRs(ctx context.Context, now time.Time) (cdr.ReconciliationReport, error) {
	report := cdr.ReconciliationReport{CheckedAt: now.UTC()}
	checks := []struct {
		code string
		sql  string
	}{
		{"CDR_WITHOUT_SUBMIT_PART", `SELECT count(*) FROM cdr_records c LEFT JOIN submit_parts p ON p.part_key=c.cdr_id WHERE p.part_key IS NULL`},
		{"SUBMIT_PART_WITHOUT_CDR", `SELECT count(*) FROM submit_parts p LEFT JOIN cdr_records c ON c.cdr_id=p.part_key WHERE c.cdr_id IS NULL`},
		{"FINAL_RESULT_STATE_MISMATCH", `SELECT count(*) FROM submit_results r JOIN cdr_records c ON c.cdr_id=r.part_key
		 WHERE r.kind <> 'RETRY' AND (
		   (r.kind='SUCCESS' AND c.state<>'SMSC_ACCEPTED') OR
		   (r.kind='FAILURE' AND c.state<>'SMSC_REJECTED') OR
		   (r.kind='TIMEOUT' AND c.state<>'TERMINAL_TIMEOUT'))`},
		{"ACCEPTED_LATE_INTENT_MISSING", `SELECT count(*) FROM cdr_records c LEFT JOIN submit_billing_intents b
		 ON b.part_key=c.cdr_id WHERE c.state='SMSC_ACCEPTED' AND c.late_amount>0 AND b.event_key IS NULL`},
		{"BILLING_LEDGER_PROJECTION_MISMATCH", `SELECT count(*) FROM cdr_records c JOIN submit_billing_intents b
		 ON b.part_key=c.cdr_id WHERE (b.applied_at IS NOT NULL) <> (c.billing_outcome='APPLIED')`},
		{"FINAL_DLR_EVENT_PROJECTION_MISMATCH", `SELECT count(*) FROM cdr_records c
		 WHERE (c.delivery_received_at IS NOT NULL) <> EXISTS (
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

func (r *PostgresSubmitTransactionRepository) AuditCDRAccess(ctx context.Context, audit cdr.AccessAudit) error {
	if audit.OccurredAt.IsZero() || strings.TrimSpace(string(audit.Action)) == "" {
		return cdr.ErrInvalidInput
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO cdr_access_audit(
 subject,action,target,allowed,occurred_at) VALUES($1,$2,$3,$4,$5)`,
		audit.Subject, audit.Action, audit.Target, audit.Allowed, audit.OccurredAt.UTC())
	return err
}

var (
	_ cdr.Repository           = (*PostgresSubmitTransactionRepository)(nil)
	_ cdr.FinalDLRRecorder     = (*PostgresSubmitTransactionRepository)(nil)
	_ cdr.OperationsRepository = (*PostgresSubmitTransactionRepository)(nil)
)
