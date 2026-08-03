package gateway

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/core/termination"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

// admitSplitBilledPart admits one MT part exactly as the front door does for a
// user with early_decrement_balance_percent set: half the rate taken at submit,
// half quoted for acceptance. Returns the part key.
func admitSplitBilledPart(
	t *testing.T,
	service *submittransaction.Service,
	messageID string,
	lateAmount float64,
) string {
	t.Helper()
	properties, err := amqpcompat.NewProperties(messageID, map[string]amqpcompat.Field{
		"user-id":          amqpcompat.StringField("partner-1"),
		"bill-id":          amqpcompat.StringField("bill-" + messageID),
		"late-bill-amount": amqpcompat.StringField("0.5"),
		"source_connector": amqpcompat.StringField("smpps"),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm.terminate-local", properties, []byte("opaque"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AdmitSubmitWithCDR(context.Background(),
		[]amqpcompat.Envelope{envelope}, cdr.SubmitMetadata{
			RouteID: "mt:10", Ingress: "smpps", Rate: 1, Currency: "EUR",
			EarlyAmount: 1 - lateAmount, LateAmount: lateAmount,
		}); err != nil {
		t.Fatal(err)
	}
	return messageID + "/000001"
}

func settlementFixture(t *testing.T) (*submittransaction.Service, *storage.SQLiteSubmitTransactionRepository, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repository, err := storage.NewSQLiteSubmitTransactionRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	service, err := submittransaction.NewService(repository, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, db
}

// TestTerminatedPartSettlesTheLedgerAndItsDeferredCharge is the regression for
// the two things the termination plane never did.
//
// It consumed the same submit queue as the SMPP client but called none of
// BeginAttempt/CommitResponse, so every terminated part stayed PENDING in
// submit_parts for the rest of its retention — /api/message-status reported
// PENDING for messages the billing console showed as delivered days earlier.
// And because only a carrier acceptance ever raised a late-billing intent, the
// deferred half of a split-billed part was quoted at admission and then never
// charged, never prunable, and shown as unsettled money on every statement.
func TestTerminatedPartSettlesTheLedgerAndItsDeferredCharge(t *testing.T) {
	service, repository, db := settlementFixture(t)
	partKey := admitSplitBilledPart(t, service, "term-a", 0.5)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	status, err := service.AggregateStatus(context.Background(), "term-a")
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 1 {
		t.Fatalf("admitted part pending=%d, want 1", status.Pending)
	}

	msg := termination.Message{
		MessageID: partKey, Connector: "terminate-local", Partner: "partner-1",
		BillID: "bill-term-a", LateBillAmount: "0.5",
	}
	if err := settleTerminatedPart(
		context.Background(), service, logger, "terminate-local", partKey, msg,
	); err != nil {
		t.Fatalf("settle: %v", err)
	}

	status, err = service.AggregateStatus(context.Background(), "term-a")
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 || status.ResultCommitted != 1 {
		t.Errorf("after settlement pending=%d committed=%d, want 0 and 1",
			status.Pending, status.ResultCommitted)
	}

	// The deferred charge must exist as an idempotent intent, keyed exactly as
	// the SMPP client keys it, so the billing ledger settles it the same way.
	var intents int
	if err := db.QueryRow(
		`SELECT count(*) FROM submit_billing_intents WHERE part_key=?`, partKey,
	).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 1 {
		t.Fatalf("late billing intents = %d, want 1", intents)
	}
	var routingKey string
	if err := db.QueryRow(
		`SELECT routing_key FROM submit_outbox WHERE event_key=?`, partKey+":20-late-billing",
	).Scan(&routingKey); err != nil {
		t.Fatalf("late billing outbox event missing: %v", err)
	}
	if routingKey != "bill_request.submit_sm_resp.partner-1" {
		t.Errorf("routing key = %q", routingKey)
	}

	// Once the ledger applies it, the CDR leaves PENDING and becomes prunable.
	if err := repository.MarkBillingApplied(
		context.Background(), partKey+":20-late-billing", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	record, err := repository.GetCDR(context.Background(), partKey)
	if err != nil {
		t.Fatal(err)
	}
	if record.BillingOutcome != cdr.BillingApplied || record.ActualLateAmount != 0.5 {
		t.Errorf("billing outcome=%s actual=%v, want APPLIED and 0.5",
			record.BillingOutcome, record.ActualLateAmount)
	}
}

// A redelivery of the same message must not charge twice or open a second
// attempt: the broker redelivers freely and the spool write is idempotent, so
// settlement has to be too.
func TestTerminatedPartSettlementIsIdempotent(t *testing.T) {
	service, _, db := settlementFixture(t)
	partKey := admitSplitBilledPart(t, service, "term-b", 0.5)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	msg := termination.Message{
		MessageID: partKey, Connector: "terminate-local", Partner: "partner-1",
		BillID: "bill-term-b", LateBillAmount: "0.5",
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := settleTerminatedPart(
			context.Background(), service, logger, "terminate-local", partKey, msg,
		); err != nil {
			t.Fatalf("settle %d: %v", attempt, err)
		}
	}
	var intents int
	if err := db.QueryRow(
		`SELECT count(*) FROM submit_billing_intents WHERE part_key=?`, partKey,
	).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 1 {
		t.Errorf("late billing intents = %d after three deliveries, want 1", intents)
	}
	status, err := service.AggregateStatus(context.Background(), "term-b")
	if err != nil {
		t.Fatal(err)
	}
	if status.ResultCommitted != 1 {
		t.Errorf("committed=%d, want 1", status.ResultCommitted)
	}
}

// A part with no late-billing amount raises no intent: the ordinary case, where
// the whole rate was taken at submit.
func TestTerminatedPartWithoutDeferredChargeRaisesNoIntent(t *testing.T) {
	service, _, db := settlementFixture(t)
	partKey := admitSplitBilledPart(t, service, "term-c", 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	msg := termination.Message{
		MessageID: partKey, Connector: "terminate-local", Partner: "partner-1",
	}
	if err := settleTerminatedPart(
		context.Background(), service, logger, "terminate-local", partKey, msg,
	); err != nil {
		t.Fatalf("settle: %v", err)
	}
	var intents int
	if err := db.QueryRow(
		`SELECT count(*) FROM submit_billing_intents WHERE part_key=?`, partKey,
	).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 0 {
		t.Errorf("late billing intents = %d, want 0", intents)
	}
	status, err := service.AggregateStatus(context.Background(), "term-c")
	if err != nil {
		t.Fatal(err)
	}
	if status.ResultCommitted != 1 {
		t.Errorf("committed=%d, want 1", status.ResultCommitted)
	}
}

// A part with no ledger row must not requeue forever: the spool row is already
// committed, so a key that will never exist has to be reported and dropped.
func TestTerminatedPartWithNoLedgerRowDoesNotRequeue(t *testing.T) {
	service, _, _ := settlementFixture(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := settleTerminatedPart(context.Background(), service, logger,
		"terminate-local", "never-admitted/000001",
		termination.Message{MessageID: "never-admitted/000001"})
	if err != nil {
		t.Fatalf("settle returned %v, want nil so the delivery is not requeued", err)
	}
}

// TestTerminatedPartStaysTerminatedInTheCDR guards the regression the live
// gateway exposed: settlement commits a successful result, and CommitResult
// projects success as SMSC_ACCEPTED. Applied unconditionally that overwrote the
// TERMINATED_LOCALLY the acceptance hook had just recorded, relabelling every
// locally terminated message as carrier-accepted and undoing the distinction
// the usage statement is built on.
func TestTerminatedPartStaysTerminatedInTheCDR(t *testing.T) {
	service, repository, _ := settlementFixture(t)
	partKey := admitSplitBilledPart(t, service, "term-d", 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The acceptance hook runs first, exactly as the spool wires it.
	if err := repository.MarkCDRTerminated(context.Background(), partKey, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := settleTerminatedPart(context.Background(), service, logger,
		"terminate-local", partKey,
		termination.Message{MessageID: partKey, Partner: "partner-1"}); err != nil {
		t.Fatalf("settle: %v", err)
	}

	record, err := repository.GetCDR(context.Background(), partKey)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != cdr.StateTerminatedLocally {
		t.Errorf("state = %s, want %s: settlement must not relabel local termination as a carrier acceptance",
			record.State, cdr.StateTerminatedLocally)
	}
}

// TestReconciliationAcceptsTerminatedSuccess guards the check that went red the
// moment terminated parts started committing results: a SUCCESS result reaches
// SMSC_ACCEPTED for relayed traffic and TERMINATED_LOCALLY for traffic this
// gateway accepted itself, and FINAL_RESULT_STATE_MISMATCH only knew the first.
func TestReconciliationAcceptsTerminatedSuccess(t *testing.T) {
	service, repository, _ := settlementFixture(t)
	partKey := admitSplitBilledPart(t, service, "term-e", 0)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := repository.MarkCDRTerminated(context.Background(), partKey, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := settleTerminatedPart(context.Background(), service, logger,
		"terminate-local", partKey,
		termination.Message{MessageID: partKey, Partner: "partner-1"}); err != nil {
		t.Fatalf("settle: %v", err)
	}

	report, err := repository.ReconcileCDRs(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Healthy() {
		t.Errorf("reconciliation unhealthy after an ordinary terminated submit: %+v", report.Issues)
	}
}
