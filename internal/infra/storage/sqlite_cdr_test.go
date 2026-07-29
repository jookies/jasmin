package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/pumpitspace/jasmin/internal/core/cdr"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

func TestCDRRecordsBeginAttemptFenceAsUnknown(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repository, _ := NewSQLiteSubmitTransactionRepository(db)
	if err := repository.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	service, _ := submittransaction.NewService(repository, func() time.Time { return now })
	properties, _ := amqpcompat.NewProperties("fenced-message", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.smsc-a", properties, []byte("opaque"))
	if err := service.AdmitSubmit(context.Background(), []amqpcompat.Envelope{envelope}); err != nil {
		t.Fatal(err)
	}
	first, _, err := service.BeginAttempt(context.Background(), "fenced-message/000001")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.BeginAttempt(context.Background(), "fenced-message/000001"); !errors.Is(err, submittransaction.ErrAttemptFenced) {
		t.Fatalf("second BeginAttempt error=%v", err)
	}
	record, err := repository.GetCDR(context.Background(), "fenced-message/000001")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != cdr.StateUnknownAfterSend || record.AttemptID != first.ID {
		t.Fatalf("record=%+v", record)
	}
	events, err := repository.ListCDREvents(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Kind != cdr.EventUnknownAfterSend {
		t.Fatalf("events=%+v", events)
	}
}

func TestCDRLifecycleIsAtomicAndDeduplicatedWithSubmitTransaction(t *testing.T) {
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

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	clock := now
	service, err := submittransaction.NewService(repository, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]amqpcompat.Field{
		"user-id":          amqpcompat.StringField("user-7"),
		"bill-id":          amqpcompat.StringField("bill-9"),
		"source_connector": amqpcompat.StringField("smppsapi"),
	}
	properties, err := amqpcompat.NewProperties("message-1", headers)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm.smsc-a", properties, []byte("opaque-pickle"))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	commercial := cdr.SubmitMetadata{
		GroupID: "customers", RouteID: "mt:20", Ingress: "smppsapi",
		Rate: 0.25, Currency: "XXX", EarlyAmount: 0.1, LateAmount: 0.15,
	}
	if err := service.AdmitSubmitWithCDR(ctx, []amqpcompat.Envelope{envelope}, commercial); err != nil {
		t.Fatal(err)
	}
	// A redelivered front-door admission keeps the first commercial identity
	// and does not create a second immutable ADMITTED event.
	if err := service.AdmitSubmitWithCDR(ctx, []amqpcompat.Envelope{envelope}, commercial); err != nil {
		t.Fatal(err)
	}
	record, err := repository.GetCDR(ctx, "message-1/000001")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != cdr.StateAdmitted ||
		record.UserID != "user-7" ||
		record.GroupID != "customers" ||
		record.RouteID != "mt:20" ||
		record.ConnectorID != "smsc-a" ||
		record.Ingress != "smppsapi" ||
		record.BillID != "bill-9" ||
		record.Rate != 0.25 ||
		record.EarlyAmount != 0.1 ||
		record.LateAmount != 0.15 ||
		record.BillingMode != cdr.BillingSplit {
		t.Fatalf("admitted cdr=%+v", record)
	}

	first, committed, err := service.BeginAttempt(ctx, record.ID)
	if err != nil || committed {
		t.Fatalf("first attempt=%+v committed=%v err=%v", first, committed, err)
	}
	clock = clock.Add(time.Second)
	if err := service.MarkUnknownAfterSend(ctx, first.ID); err != nil {
		t.Fatal(err)
	}

	second, committed, err := service.BeginAttempt(ctx, record.ID)
	if err != nil || committed {
		t.Fatalf("second attempt=%+v committed=%v err=%v", second, committed, err)
	}
	clock = clock.Add(time.Second)
	fresh, err := service.CommitResponse(ctx, submittransaction.Result{
		PartKey: record.ID, AttemptID: second.ID,
		Kind: submittransaction.ResultRetry, SMPPStatus: "ESME_RTHROTTLED",
	})
	if err != nil || !fresh {
		t.Fatalf("retry commit fresh=%v err=%v", fresh, err)
	}

	third, committed, err := service.BeginAttempt(ctx, record.ID)
	if err != nil || committed {
		t.Fatalf("third attempt=%+v committed=%v err=%v", third, committed, err)
	}
	clock = clock.Add(time.Second)
	result := submittransaction.Result{
		PartKey: record.ID, AttemptID: third.ID, Kind: submittransaction.ResultSuccess,
		SMPPStatus: "ESME_ROK", SMSCMessageID: "opaque-smsc-id",
	}
	fresh, err = service.CommitResponse(ctx, result)
	if err != nil || !fresh {
		t.Fatalf("success commit fresh=%v err=%v", fresh, err)
	}
	if fresh, err = service.CommitResponse(ctx, result); err != nil || fresh {
		t.Fatalf("duplicate success fresh=%v err=%v", fresh, err)
	}

	record, err = repository.GetCDR(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != cdr.StateSMSCAccepted ||
		record.AttemptID != third.ID ||
		record.SMPPStatus != "ESME_ROK" ||
		record.SMSCMessageID != "opaque-smsc-id" ||
		record.TerminalAt == nil ||
		!record.TerminalAt.Equal(clock) {
		t.Fatalf("terminal cdr=%+v", record)
	}
	events, err := repository.ListCDREvents(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []cdr.EventKind{
		cdr.EventAdmitted,
		cdr.EventUnknownAfterSend,
		cdr.EventRetryPending,
		cdr.EventSMSCAccepted,
	}
	if len(events) != len(wantKinds) {
		t.Fatalf("events=%+v", events)
	}
	for index, want := range wantKinds {
		if events[index].Kind != want {
			t.Errorf("event[%d].kind=%q want %q", index, events[index].Kind, want)
		}
	}
}

func TestCDRTerminalFailureStates(t *testing.T) {
	for _, test := range []struct {
		name string
		kind submittransaction.ResultKind
		want cdr.State
	}{
		{name: "SMSC rejection", kind: submittransaction.ResultFailure, want: cdr.StateSMSCRejected},
		{name: "terminal timeout", kind: submittransaction.ResultTimeout, want: cdr.StateTerminalTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = db.Close() })
			repository, _ := NewSQLiteSubmitTransactionRepository(db)
			if err := repository.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
			service, _ := submittransaction.NewService(repository, func() time.Time { return now })
			properties, _ := amqpcompat.NewProperties("failure-message", nil)
			envelope, _ := amqpcompat.NewEnvelope("submit.sm.smsc-a", properties, []byte("opaque"))
			if err := service.AdmitSubmit(context.Background(), []amqpcompat.Envelope{envelope}); err != nil {
				t.Fatal(err)
			}
			attempt, _, err := service.BeginAttempt(context.Background(), "failure-message/000001")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.CommitResponse(context.Background(), submittransaction.Result{
				PartKey: "failure-message/000001", AttemptID: attempt.ID,
				Kind: test.kind, SMPPStatus: "ESME_RSYSERR",
			}); err != nil {
				t.Fatal(err)
			}
			record, err := repository.GetCDR(context.Background(), "failure-message/000001")
			if err != nil {
				t.Fatal(err)
			}
			if record.State != test.want || record.TerminalAt == nil {
				t.Fatalf("record=%+v want state=%q", record, test.want)
			}
		})
	}
}
