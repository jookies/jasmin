package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

func newSubmitStore(t *testing.T) (*SQLiteSubmitTransactionRepository, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repository, err := NewSQLiteSubmitTransactionRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repository, db
}

func submitEnvelope(t *testing.T, messageID, connector string) amqpcompat.Envelope {
	t.Helper()
	properties, err := amqpcompat.NewProperties(messageID, map[string]amqpcompat.Field{
		"created_at": amqpcompat.StringField("2026-07-21 12:00:00"),
	}, amqpcompat.WithReplyTo("submit.sm.resp.user-1"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm."+connector, properties, []byte{0x80, 2, 'x'})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestSQLiteSubmitTransactionIdempotentResponseAndRecovery(t *testing.T) {
	repository, db := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewService(repository, func() time.Time { return now })
	ctx := context.Background()
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{submitEnvelope(t, "message-1", "connector-a")}); err != nil {
		t.Fatal(err)
	}
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{submitEnvelope(t, "message-1", "connector-a")}); err != nil {
		t.Fatalf("duplicate admission: %v", err)
	}
	attempt, committed, err := service.BeginAttempt(ctx, "message-1/000001")
	if err != nil || committed {
		t.Fatalf("BeginAttempt=(%+v,%v,%v)", attempt, committed, err)
	}
	if recovered, err := service.Recover(ctx); err != nil || recovered != 1 {
		t.Fatalf("Recover=(%d,%v)", recovered, err)
	}
	second, committed, err := service.BeginAttempt(ctx, "message-1/000001")
	if err != nil || committed || second.Number != 2 {
		t.Fatalf("retry=(%+v,%v,%v)", second, committed, err)
	}
	if err := service.MarkSent(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	response := submitEnvelope(t, "response-1", "resp-user")
	event, err := submittransaction.NewEnvelopeEvent("message-1/000001:response", "message-1/000001", submittransaction.EventSubmitResponse, "messaging", response, now)
	if err != nil {
		t.Fatal(err)
	}
	result := submittransaction.Result{PartKey: "message-1/000001", AttemptID: second.ID, Kind: submittransaction.ResultSuccess, SMPPStatus: "ESME_ROK", SMSCMessageID: "opaque-smsc-id"}
	if fresh, err := service.CommitResponse(ctx, result, event); err != nil || !fresh {
		t.Fatalf("first commit=(%v,%v)", fresh, err)
	}
	if fresh, err := service.CommitResponse(ctx, result, event); err != nil || fresh {
		t.Fatalf("duplicate commit=(%v,%v)", fresh, err)
	}
	var results, events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM submit_results`).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM submit_outbox`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if results != 1 || events != 2 {
		t.Fatalf("results=%d events=%d want 1,2", results, events)
	}
}

type recordingPublisher struct {
	failAt int
	calls  []string
}

func (p *recordingPublisher) Publish(_ context.Context, _ string, routingKey string, _ amqpcompat.Envelope) error {
	p.calls = append(p.calls, routingKey)
	if p.failAt > 0 && len(p.calls) == p.failAt {
		return errors.New("broker unavailable")
	}
	return nil
}

func TestOutboxPublishFailureRecoveryAndOrdering(t *testing.T) {
	repository, _ := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewService(repository, func() time.Time { return now })
	ctx := context.Background()
	envelopes := []amqpcompat.Envelope{submitEnvelope(t, "message-a", "a"), submitEnvelope(t, "message-b", "b")}
	if err := service.AdmitSubmit(ctx, envelopes); err != nil {
		t.Fatal(err)
	}
	failing := &recordingPublisher{failAt: 1}
	dispatcher, _ := submittransaction.NewDispatcher(repository, failing, "worker-1", 10, time.Minute, func() time.Time { return now })
	if count, err := dispatcher.DispatchOnce(ctx); err == nil || count != 0 {
		t.Fatalf("failed dispatch=(%d,%v)", count, err)
	}
	success := &recordingPublisher{}
	dispatcher, _ = submittransaction.NewDispatcher(repository, success, "worker-2", 10, time.Minute, func() time.Time { return now.Add(time.Second) })
	if count, err := dispatcher.DispatchOnce(ctx); err != nil || count != 2 {
		t.Fatalf("recovery dispatch=(%d,%v)", count, err)
	}
	if want := []string{"submit.sm.a", "submit.sm.b"}; !reflect.DeepEqual(success.calls, want) {
		t.Fatalf("order=%v want %v", success.calls, want)
	}
}

func TestProductionRepositoryRejectsSQLiteProjection(t *testing.T) {
	repository, _ := newSubmitStore(t)
	if _, err := submittransaction.RequireProductionRepository(repository); !errors.Is(err, submittransaction.ErrProductionRequiresPostgres) {
		t.Fatalf("error=%v", err)
	}
}

func TestDurableResponseDuplicateCreatesOneResponseAndLateBillingIntent(t *testing.T) {
	repository, db := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewService(repository, func() time.Time { return now })
	ctx := context.Background()
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{submitEnvelope(t, "message-response", "connector-a")}); err != nil {
		t.Fatal(err)
	}
	attempt, _, err := service.BeginAttempt(ctx, "message-response/000001")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := smppc.NewDurableResponseLifecycle(service, policy, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	input := smppc.DurableResponseInput{
		PartKey: "message-response/000001", AttemptID: attempt.ID, Status: "ESME_ROK",
		SMSCMessageID: "smsc-opaque", ReplyEnabled: true, ReplyTo: "submit.sm.resp.user-1",
		MessageID: "message-response", CreatedAt: "2026-07-21 12:00:00", Body: []byte{0x80, 2, 'r'},
		UserID: "user-1", BillID: "bill-1", LateBillAmount: "0.75", RetryAttempt: 1,
	}
	if fresh, err := lifecycle.Commit(ctx, input); err != nil || !fresh {
		t.Fatalf("first response=(%v,%v)", fresh, err)
	}
	if fresh, err := lifecycle.Commit(ctx, input); err != nil || fresh {
		t.Fatalf("duplicate response=(%v,%v)", fresh, err)
	}
	var results, billingIntents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM submit_results`).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM submit_billing_intents`).Scan(&billingIntents); err != nil {
		t.Fatal(err)
	}
	if results != 1 || billingIntents != 1 {
		t.Fatalf("results=%d billing_intents=%d", results, billingIntents)
	}
	publisher := &recordingPublisher{}
	dispatcher, _ := submittransaction.NewDispatcher(repository, publisher, "response-worker", 10, time.Minute, func() time.Time { return now })
	if count, err := dispatcher.DispatchOnce(ctx); err != nil || count != 3 {
		t.Fatalf("dispatch=(%d,%v)", count, err)
	}
	want := []string{"submit.sm.connector-a", "submit.sm.resp.user-1", "bill_request.submit_sm_resp.user-1"}
	if !reflect.DeepEqual(publisher.calls, want) {
		t.Fatalf("order=%v want %v", publisher.calls, want)
	}
}

func TestRetryResultAllowsNextAttemptAndFinalResult(t *testing.T) {
	repository, db := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, err := submittransaction.NewService(repository, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{submitEnvelope(t, "message-retry", "connector-a")}); err != nil {
		t.Fatal(err)
	}
	partKey := "message-retry/000001"
	first, committed, err := service.BeginAttempt(ctx, partKey)
	if err != nil || committed || first.Number != 1 {
		t.Fatalf("first attempt=%+v committed=%v err=%v", first, committed, err)
	}
	if fresh, err := service.CommitResponse(ctx, submittransaction.Result{
		PartKey: partKey, AttemptID: first.ID, Kind: submittransaction.ResultRetry, SMPPStatus: "ESME_RSYSERR",
	}); err != nil || !fresh {
		t.Fatalf("retry commit=(%v,%v)", fresh, err)
	}
	second, committed, err := service.BeginAttempt(ctx, partKey)
	if err != nil || committed || second.Number != 2 {
		t.Fatalf("second attempt=%+v committed=%v err=%v", second, committed, err)
	}
	if fresh, err := service.CommitResponse(ctx, submittransaction.Result{
		PartKey: partKey, AttemptID: second.ID, Kind: submittransaction.ResultSuccess, SMPPStatus: "ESME_ROK", SMSCMessageID: "smsc-final",
	}); err != nil || !fresh {
		t.Fatalf("final commit=(%v,%v)", fresh, err)
	}
	if _, committed, err := service.BeginAttempt(ctx, partKey); err != nil || !committed {
		t.Fatalf("post-final committed=%v err=%v", committed, err)
	}
	var results int
	if err := db.QueryRow(`SELECT COUNT(*) FROM submit_results WHERE part_key=?`, partKey).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if results != 2 {
		t.Fatalf("result rows=%d want=2", results)
	}
}
