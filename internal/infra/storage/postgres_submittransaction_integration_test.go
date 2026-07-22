package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

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
CREATE OR REPLACE FUNCTION jasmin_test_fail_second_outbox() RETURNS trigger AS $$
BEGIN
  IF NEW.part_key LIKE '%/000002' THEN RAISE EXCEPTION 'injected second outbox failure'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER jasmin_test_fail_second_outbox BEFORE INSERT ON submit_outbox
FOR EACH ROW EXECUTE FUNCTION jasmin_test_fail_second_outbox();`
	if _, err := repository.db.ExecContext(ctx, triggerSQL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = repository.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS jasmin_test_fail_second_outbox ON submit_outbox; DROP FUNCTION IF EXISTS jasmin_test_fail_second_outbox()`)
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
