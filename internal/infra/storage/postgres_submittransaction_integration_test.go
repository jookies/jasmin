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
