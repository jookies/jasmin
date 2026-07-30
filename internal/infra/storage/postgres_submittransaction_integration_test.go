package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

func TestPostgresCDRCompletionLifecycleAndOperations(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	repository, err := OpenPostgresSubmitTransactionRepository(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.db.ExecContext(ctx, `TRUNCATE
 cdr_access_audit,cdr_events,cdr_records,submit_billing_intents,submit_outbox,
 submit_results,submit_attempts,submit_parts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	service, err := submittransaction.NewProductionService(repository, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	properties, _ := amqpcompat.NewProperties("pg-cdr-complete", map[string]amqpcompat.Field{
		"user-id": amqpcompat.StringField("finance-user"),
		"bill-id": amqpcompat.StringField("bill-pg-cdr"),
	})
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.connector-a", properties, []byte("opaque"))
	if err = service.AdmitSubmitWithCDR(ctx, []amqpcompat.Envelope{envelope}, cdr.SubmitMetadata{
		RouteID: "mt:10", Ingress: "httpapi", Rate: 1, Currency: "GBP",
		EarlyAmount: 0.5, LateAmount: 0.5,
	}); err != nil {
		t.Fatal(err)
	}
	id := "pg-cdr-complete/000001"
	attempt, _, err := service.BeginAttempt(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Second)
	billingProperties, _ := amqpcompat.NewProperties("bill-pg-cdr", map[string]amqpcompat.Field{
		"event-key": amqpcompat.StringField(id + ":20-late-billing"),
	})
	billingEnvelope, _ := amqpcompat.NewEnvelope(
		"bill_request.submit_sm_resp.finance-user", billingProperties, nil)
	billingEvent, err := submittransaction.NewEnvelopeEvent(
		id+":20-late-billing", id, submittransaction.EventLateBilling,
		"billing", billingEnvelope, clock)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := service.CommitResponse(ctx, submittransaction.Result{
		PartKey: id, AttemptID: attempt.ID, Kind: submittransaction.ResultSuccess,
		SMPPStatus: "ESME_ROK", SMSCMessageID: "ABC",
	}, billingEvent)
	if err != nil || !fresh {
		t.Fatalf("commit=(%v,%v)", fresh, err)
	}
	if err = repository.MarkBillingApplied(ctx, id+":20-late-billing", clock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = repository.RecordFinalDLR(ctx, cdr.FinalDLR{
		QueueMessageID: "pg-cdr-complete", ConnectorID: "connector-a",
		SMSCMessageID: "ABC", Status: "DELIVRD", Error: "000",
		ReceivedAt: clock.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	record, err := repository.GetCDR(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if record.Currency != "GBP" || record.BillingOutcome != cdr.BillingApplied ||
		record.ActualLateAmount != 0.5 || record.DeliveryState != cdr.DeliveryDelivered {
		t.Fatalf("record=%+v", record)
	}
	records, err := repository.ExportCDRs(ctx, cdr.ExportQuery{Limit: 10})
	if err != nil || len(records) != 1 {
		t.Fatalf("export=(%d,%v)", len(records), err)
	}
	report, err := repository.ReconcileCDRs(ctx, clock.Add(3*time.Second))
	if err != nil || !report.Healthy() {
		t.Fatalf("reconcile=(%+v,%v)", report, err)
	}
	pruned, err := repository.PruneCDRs(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 10)
	if err != nil || pruned.Records != 1 || pruned.Events < 4 {
		t.Fatalf("prune=(%+v,%v)", pruned, err)
	}
}

func TestPostgresSubmitTransactionMigrationAndRecovery(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	repository, err := OpenPostgresSubmitTransactionRepository(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repository.Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	if _, err := repository.db.ExecContext(ctx, `TRUNCATE submit_billing_intents,submit_outbox,submit_results,submit_attempts,submit_parts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := submittransaction.NewProductionService(repository, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewProductionService(repository, func() time.Time { return now })
	properties, _ := amqpcompat.NewProperties("postgres-message", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.connector-a", properties, []byte{0x80, 2, 'x'})
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{envelope}); err != nil {
		t.Fatal(err)
	}
	attempt, committed, err := service.BeginAttempt(ctx, "postgres-message/000001")
	if err != nil || committed {
		t.Fatalf("attempt=(%+v,%v,%v)", attempt, committed, err)
	}
	if recovered, err := service.Recover(ctx); err != nil || recovered != 1 {
		t.Fatalf("recover=(%d,%v)", recovered, err)
	}
}

func TestPostgresSMSCMessageIDMayRepeatAcrossConnectorParts(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	repository, err := OpenPostgresSubmitTransactionRepository(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `TRUNCATE submit_billing_intents,submit_outbox,submit_results,submit_attempts,submit_parts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewProductionService(repository, func() time.Time { return now })
	for _, item := range []struct{ messageID, connectorID string }{{"pg-message-a", "connector-a"}, {"pg-message-b", "connector-b"}} {
		properties, _ := amqpcompat.NewProperties(item.messageID, nil)
		envelope, _ := amqpcompat.NewEnvelope("submit.sm."+item.connectorID, properties, []byte{0x80, 2, 'x'})
		if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{envelope}); err != nil {
			t.Fatal(err)
		}
		partKey := item.messageID + "/000001"
		attempt, _, err := service.BeginAttempt(ctx, partKey)
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := service.CommitResponse(ctx, submittransaction.Result{PartKey: partKey, AttemptID: attempt.ID, Kind: submittransaction.ResultSuccess, SMPPStatus: "ESME_ROK", SMSCMessageID: "1"})
		if err != nil || !fresh {
			t.Fatalf("commit %s=(%v,%v)", partKey, fresh, err)
		}
	}
}

func TestPostgresMultipartAdmissionRollsBackOnSecondOutboxFailure(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	repository, err := OpenPostgresSubmitTransactionRepository(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `TRUNCATE submit_billing_intents,submit_outbox,submit_results,submit_attempts,submit_parts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	const triggerSQL = `
CREATE OR REPLACE FUNCTION synevyr_test_fail_second_outbox() RETURNS trigger AS $$
BEGIN
  IF NEW.part_key LIKE '%/000002' THEN RAISE EXCEPTION 'injected second outbox failure'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER synevyr_test_fail_second_outbox BEFORE INSERT ON submit_outbox
FOR EACH ROW EXECUTE FUNCTION synevyr_test_fail_second_outbox();`
	if _, err := repository.db.ExecContext(ctx, triggerSQL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = repository.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS synevyr_test_fail_second_outbox ON submit_outbox; DROP FUNCTION IF EXISTS synevyr_test_fail_second_outbox()`)
	})
	service, _ := submittransaction.NewProductionService(repository, nil)
	envelopes := []amqpcompat.Envelope{
		multipartEnvelope(t, "pg-multipart-rollback", "connector-a", 1, 2),
		multipartEnvelope(t, "pg-multipart-rollback", "connector-a", 2, 2),
	}
	if err := service.AdmitSubmit(ctx, envelopes); err == nil {
		t.Fatal("injected second outbox failure unexpectedly succeeded")
	}
	var parts, events int
	if err := repository.db.QueryRowContext(ctx, `SELECT count(*) FROM submit_parts WHERE message_id='pg-multipart-rollback'`).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.QueryRowContext(ctx, `SELECT count(*) FROM submit_outbox WHERE part_key LIKE 'pg-multipart-rollback/%'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if parts != 0 || events != 0 {
		t.Fatalf("rollback leaked parts=%d events=%d", parts, events)
	}
}

func TestPostgresActiveAttemptIsFencedBeforeRedelivery(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	repository, err := OpenPostgresSubmitTransactionRepository(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `TRUNCATE submit_billing_intents,submit_outbox,submit_results,submit_attempts,submit_parts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewProductionService(repository, func() time.Time { return now })
	properties, _ := amqpcompat.NewProperties("pg-active", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.connector-a", properties, []byte{0x80, 2, 'x'})
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{envelope}); err != nil {
		t.Fatal(err)
	}
	partKey := "pg-active/000001"
	first, _, err := service.BeginAttempt(ctx, partKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkSent(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.BeginAttempt(ctx, partKey); !errors.Is(err, submittransaction.ErrAttemptFenced) {
		t.Fatalf("active redelivery error=%v want ErrAttemptFenced", err)
	}
	second, committed, err := service.BeginAttempt(ctx, partKey)
	if err != nil || committed || second.Number != 2 || second.ID == first.ID {
		t.Fatalf("post-fence attempt=%+v committed=%v err=%v", second, committed, err)
	}
}

func TestPostgresCommitResponseRejectsAttemptOwnedByDifferentPart(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	repository, err := OpenPostgresSubmitTransactionRepository(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `TRUNCATE submit_billing_intents,submit_outbox,submit_results,submit_attempts,submit_parts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewProductionService(repository, func() time.Time { return now })
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{
		multipartEnvelope(t, "pg-cross-part", "connector-a", 1, 2),
		multipartEnvelope(t, "pg-cross-part", "connector-a", 2, 2),
	}); err != nil {
		t.Fatal(err)
	}
	attempt, _, err := service.BeginAttempt(ctx, "pg-cross-part/000001")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := service.CommitResponse(ctx, submittransaction.Result{
		PartKey: "pg-cross-part/000002", AttemptID: attempt.ID,
		Kind: submittransaction.ResultSuccess, SMPPStatus: "ESME_ROK",
	})
	if fresh || !errors.Is(err, submittransaction.ErrAttemptNotFound) {
		t.Fatalf("cross-part commit=(%v,%v), want false ErrAttemptNotFound", fresh, err)
	}
	var results int
	if err := repository.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM submit_results`).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if results != 0 {
		t.Fatalf("cross-part commit persisted %d results", results)
	}
}
