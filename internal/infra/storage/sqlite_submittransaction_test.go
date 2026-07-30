package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
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

func multipartEnvelope(t *testing.T, aggregate, connector string, part, count int) amqpcompat.Envelope {
	t.Helper()
	messageID := aggregate
	if count > 1 {
		messageID = fmt.Sprintf("%s/%06d", aggregate, part)
	}
	properties, err := amqpcompat.NewProperties(messageID, map[string]amqpcompat.Field{
		"created_at":           amqpcompat.StringField("2026-07-21 12:00:00"),
		"aggregate-message-id": amqpcompat.StringField(aggregate),
		"part-number":          amqpcompat.IntegerField(int64(part)),
		"part-count":           amqpcompat.IntegerField(int64(count)),
	}, amqpcompat.WithReplyTo("submit.sm.resp.user-1"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm."+connector, properties, []byte{0x80, 2, byte(part)})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestMultipartAdmissionPersistsContiguousPartsAtomically(t *testing.T) {
	repository, db := newSubmitStore(t)
	service, _ := submittransaction.NewService(repository, nil)
	envelopes := []amqpcompat.Envelope{
		multipartEnvelope(t, "aggregate-1", "connector-a", 1, 2),
		multipartEnvelope(t, "aggregate-1", "connector-a", 2, 2),
	}
	if err := service.AdmitSubmit(context.Background(), envelopes); err != nil {
		t.Fatal(err)
	}
	var parts, events int
	if err := db.QueryRow(`SELECT count(*) FROM submit_parts WHERE message_id='aggregate-1'`).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM submit_outbox WHERE part_key LIKE 'aggregate-1/%'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if parts != 2 || events != 2 {
		t.Fatalf("parts=%d events=%d", parts, events)
	}

	malformed := []amqpcompat.Envelope{
		multipartEnvelope(t, "aggregate-2", "connector-a", 1, 2),
		multipartEnvelope(t, "aggregate-2", "connector-a", 1, 2),
	}
	if err := service.AdmitSubmit(context.Background(), malformed); !errors.Is(err, submittransaction.ErrInvalidInput) {
		t.Fatalf("malformed admission error=%v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM submit_parts WHERE message_id='aggregate-2'`).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if parts != 0 {
		t.Fatalf("malformed admission persisted %d parts", parts)
	}
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

func TestSQLiteCommitResponseRejectsAttemptOwnedByDifferentPart(t *testing.T) {
	repository, db := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewService(repository, func() time.Time { return now })
	ctx := context.Background()
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{
		multipartEnvelope(t, "cross-part", "connector-a", 1, 2),
		multipartEnvelope(t, "cross-part", "connector-a", 2, 2),
	}); err != nil {
		t.Fatal(err)
	}
	attempt, _, err := service.BeginAttempt(ctx, "cross-part/000001")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := service.CommitResponse(ctx, submittransaction.Result{
		PartKey: "cross-part/000002", AttemptID: attempt.ID,
		Kind: submittransaction.ResultSuccess, SMPPStatus: "ESME_ROK",
	})
	if fresh || !errors.Is(err, submittransaction.ErrAttemptNotFound) {
		t.Fatalf("cross-part commit=(%v,%v), want false ErrAttemptNotFound", fresh, err)
	}
	var results int
	if err := db.QueryRow(`SELECT COUNT(*) FROM submit_results`).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if results != 0 {
		t.Fatalf("cross-part commit persisted %d results", results)
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
	for _, envelope := range []amqpcompat.Envelope{submitEnvelope(t, "message-a", "a"), submitEnvelope(t, "message-b", "b")} {
		if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{envelope}); err != nil {
			t.Fatal(err)
		}
	}
	failing := &recordingPublisher{failAt: 1}
	dispatcher, _ := submittransaction.NewDispatcher(repository, failing, "worker-1", 10, time.Minute, func() time.Time { return now })
	if count, err := dispatcher.DispatchOnce(ctx); err == nil || count != 1 {
		t.Fatalf("failed dispatch=(%d,%v)", count, err)
	}
	if want := []string{"submit.sm.a", "submit.sm.b"}; !reflect.DeepEqual(failing.calls, want) {
		t.Fatalf("failed batch calls=%v want %v; one poison event must not block the next event", failing.calls, want)
	}
	success := &recordingPublisher{}
	dispatcher, _ = submittransaction.NewDispatcher(repository, success, "worker-2", 10, time.Minute, func() time.Time { return now.Add(time.Second) })
	if count, err := dispatcher.DispatchOnce(ctx); err != nil || count != 1 {
		t.Fatalf("recovery dispatch=(%d,%v)", count, err)
	}
	if want := []string{"submit.sm.a"}; !reflect.DeepEqual(success.calls, want) {
		t.Fatalf("order=%v want %v", success.calls, want)
	}
}

func admitOrderedOutbox(t *testing.T, repository *SQLiteSubmitTransactionRepository, now time.Time) {
	t.Helper()
	envelope := submitEnvelope(t, "ordered-message", "connector-a")
	payload, err := submittransaction.SnapshotEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	partKey := "ordered-message/000001"
	part := submittransaction.LogicalPart{
		Key: partKey, MessageID: "ordered-message", PartNumber: 1, ConnectorID: "connector-a",
		State: submittransaction.PartPending, CreatedAt: now,
	}
	events := []submittransaction.OutboxEvent{
		{Key: partKey + ":00-first", PartKey: partKey, Kind: submittransaction.EventSubmitRequest, Exchange: "messaging", RoutingKey: "submit.sm.first", Payload: payload, CreatedAt: now, AvailableAt: now},
		{Key: partKey + ":01-second", PartKey: partKey, Kind: submittransaction.EventSubmitResponse, Exchange: "messaging", RoutingKey: "submit.sm.second", Payload: payload, CreatedAt: now, AvailableAt: now},
	}
	if err := repository.Admit(context.Background(), []submittransaction.LogicalPart{part}, events); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxRetryDelayedPredecessorBlocksSamePartAcrossBatches(t *testing.T) {
	repository, _ := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	admitOrderedOutbox(t, repository, now)

	failing := &recordingPublisher{failAt: 1}
	first, _ := submittransaction.NewDispatcher(repository, failing, "worker-1", 1, time.Minute, func() time.Time { return now })
	if count, err := first.DispatchOnce(context.Background()); err == nil || count != 0 {
		t.Fatalf("first dispatch=(%d,%v)", count, err)
	}

	beforeRetry := &recordingPublisher{}
	second, _ := submittransaction.NewDispatcher(repository, beforeRetry, "worker-2", 1, time.Minute, func() time.Time { return now.Add(500 * time.Millisecond) })
	if count, err := second.DispatchOnce(context.Background()); err != nil || count != 0 {
		t.Fatalf("predecessor delay dispatch=(%d,%v)", count, err)
	}
	if len(beforeRetry.calls) != 0 {
		t.Fatalf("same-part successor bypassed delayed predecessor: %v", beforeRetry.calls)
	}

	afterRetry := &recordingPublisher{}
	retry, _ := submittransaction.NewDispatcher(repository, afterRetry, "worker-3", 1, time.Minute, func() time.Time { return now.Add(time.Second) })
	if count, err := retry.DispatchOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("retry predecessor=(%d,%v)", count, err)
	}
	if count, err := retry.DispatchOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("successor after predecessor=(%d,%v)", count, err)
	}
	if want := []string{"submit.sm.first", "submit.sm.second"}; !reflect.DeepEqual(afterRetry.calls, want) {
		t.Fatalf("same-part order=%v want %v", afterRetry.calls, want)
	}
}

func TestOutboxClaimedPredecessorBlocksSecondOwner(t *testing.T) {
	repository, _ := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	admitOrderedOutbox(t, repository, now)

	claimed, err := repository.ClaimOutbox(context.Background(), "worker-1", 1, now, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].RoutingKey != "submit.sm.first" {
		t.Fatalf("first claim=%v err=%v", claimed, err)
	}
	second, err := repository.ClaimOutbox(context.Background(), "worker-2", 1, now, time.Minute)
	if err != nil || len(second) != 0 {
		t.Fatalf("second owner bypassed claimed predecessor: events=%v err=%v", second, err)
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
	published := 0
	for range 4 {
		count, err := dispatcher.DispatchOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		published += count
	}
	if published != 4 {
		t.Fatalf("published=%d, want 4", published)
	}
	// The dlr.submit_sm_resp event (key :15-dlr) dispatches between the response
	// (:10) and the late-billing intent (:20).
	want := []string{"submit.sm.connector-a", "submit.sm.resp.user-1", "dlr.submit_sm_resp", "bill_request.submit_sm_resp.user-1"}
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

func TestSMSCMessageIDMayRepeatAcrossConnectorParts(t *testing.T) {
	repository, db := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewService(repository, func() time.Time { return now })
	ctx := context.Background()
	for _, item := range []struct{ messageID, connectorID string }{{"message-a", "connector-a"}, {"message-b", "connector-b"}} {
		if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{submitEnvelope(t, item.messageID, item.connectorID)}); err != nil {
			t.Fatal(err)
		}
		partKey := item.messageID + "/000001"
		attempt, committed, err := service.BeginAttempt(ctx, partKey)
		if err != nil || committed {
			t.Fatalf("begin %s=(%+v,%v,%v)", partKey, attempt, committed, err)
		}
		fresh, err := service.CommitResponse(ctx, submittransaction.Result{
			PartKey: partKey, AttemptID: attempt.ID, Kind: submittransaction.ResultSuccess,
			SMPPStatus: "ESME_ROK", SMSCMessageID: "1",
		})
		if err != nil || !fresh {
			t.Fatalf("commit %s=(%v,%v)", partKey, fresh, err)
		}
	}
	var results int
	if err := db.QueryRow(`SELECT COUNT(*) FROM submit_results WHERE smsc_message_id='1'`).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if results != 2 {
		t.Fatalf("same opaque SMSC id results=%d want=2", results)
	}
}

func TestUnknownAfterSendAllocatesNewAttemptIdentity(t *testing.T) {
	repository, db := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewService(repository, func() time.Time { return now })
	ctx := context.Background()
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{submitEnvelope(t, "message-timeout", "connector-a")}); err != nil {
		t.Fatal(err)
	}
	partKey := "message-timeout/000001"
	first, _, err := service.BeginAttempt(ctx, partKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkSent(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkUnknownAfterSend(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second, committed, err := service.BeginAttempt(ctx, partKey)
	if err != nil || committed || second.Number != 2 || second.ID == first.ID {
		t.Fatalf("second attempt=%+v committed=%v err=%v first=%+v", second, committed, err, first)
	}
	var attemptState, partState string
	if err := db.QueryRow(`SELECT state FROM submit_attempts WHERE id=?`, first.ID).Scan(&attemptState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT state FROM submit_parts WHERE part_key=?`, partKey).Scan(&partState); err != nil {
		t.Fatal(err)
	}
	if attemptState != string(submittransaction.AttemptUnknownAfterSend) || partState != string(submittransaction.PartAttempting) {
		t.Fatalf("states after new attempt: old=%s part=%s", attemptState, partState)
	}
}

func TestActiveAttemptIsFencedBeforeRedeliveryWriteAuthority(t *testing.T) {
	repository, _ := newSubmitStore(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewService(repository, func() time.Time { return now })
	ctx := context.Background()
	if err := service.AdmitSubmit(ctx, []amqpcompat.Envelope{submitEnvelope(t, "message-active", "connector-a")}); err != nil {
		t.Fatal(err)
	}
	partKey := "message-active/000001"
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

func TestSQLiteInitDropsLegacyUniqueSMSCMessageIDIndex(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository, _ := NewSQLiteSubmitTransactionRepository(db)
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP INDEX submit_results_smsc_id_lookup; CREATE UNIQUE INDEX submit_results_smsc_id ON submit_results(smsc_message_id) WHERE smsc_message_id IS NOT NULL AND smsc_message_id <> '';`); err != nil {
		t.Fatal(err)
	}
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	var legacyIndexes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list('submit_results') WHERE name='submit_results_smsc_id'`).Scan(&legacyIndexes); err != nil {
		t.Fatal(err)
	}
	if legacyIndexes != 0 {
		t.Fatal("legacy globally unique SMSC message-id index survived Init upgrade")
	}
}
