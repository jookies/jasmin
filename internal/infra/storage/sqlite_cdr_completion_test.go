package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

func admitAcceptedCDR(
	t *testing.T,
	repository *SQLiteSubmitTransactionRepository,
	messageID string,
	now time.Time,
	lateAmount float64,
) string {
	t.Helper()
	clock := now
	service, err := submittransaction.NewService(repository, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]amqpcompat.Field{
		"user-id":          amqpcompat.StringField("user-1"),
		"bill-id":          amqpcompat.StringField("bill-" + messageID),
		"source_connector": amqpcompat.StringField("httpapi"),
	}
	properties, err := amqpcompat.NewProperties(messageID, headers)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm.smsc-a", properties, []byte("opaque"))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.AdmitSubmitWithCDR(context.Background(), []amqpcompat.Envelope{envelope}, cdr.SubmitMetadata{
		RouteID: "mt:10", Ingress: "httpapi", Rate: 1, Currency: "EUR",
		EarlyAmount: 1 - lateAmount, LateAmount: lateAmount,
	}); err != nil {
		t.Fatal(err)
	}
	id := messageID + "/000001"
	attempt, _, err := service.BeginAttempt(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Second)
	billingProperties, err := amqpcompat.NewProperties("bill-"+messageID, map[string]amqpcompat.Field{
		"event-key": amqpcompat.StringField(id + ":20-late-billing"),
		"user-id":   amqpcompat.StringField("user-1"),
		"amount":    amqpcompat.StringField("0.5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	billingEnvelope, err := amqpcompat.NewEnvelope(
		"bill_request.submit_sm_resp.user-1", billingProperties, nil)
	if err != nil {
		t.Fatal(err)
	}
	event, err := submittransaction.NewEnvelopeEvent(
		id+":20-late-billing", id, submittransaction.EventLateBilling,
		"billing", billingEnvelope, clock)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := service.CommitResponse(context.Background(), submittransaction.Result{
		PartKey: id, AttemptID: attempt.ID, Kind: submittransaction.ResultSuccess,
		SMPPStatus: "ESME_ROK", SMSCMessageID: "smsc-" + messageID,
	}, event)
	if err != nil || !fresh {
		t.Fatalf("commit accepted fresh=%v err=%v", fresh, err)
	}
	return id
}

func TestCDRFinalDLRAndLateBillingOutcomesAreDurableAndDeduplicated(t *testing.T) {
	repository, _ := newSubmitStore(t)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	id := admitAcceptedCDR(t, repository, "settled", now, 0.5)

	appliedAt := now.Add(2 * time.Second)
	if err := repository.MarkBillingApplied(
		context.Background(), id+":20-late-billing", appliedAt,
	); err != nil {
		t.Fatal(err)
	}
	receivedAt := now.Add(3 * time.Second)
	doneAt := now.Add(1500 * time.Millisecond)
	final := cdr.FinalDLR{
		QueueMessageID: "settled", ConnectorID: "smsc-a",
		SMSCMessageID: "smsc-settled", Status: "DELIVRD", Error: "000",
		DoneAt: &doneAt, ReceivedAt: receivedAt,
	}
	if err := repository.RecordFinalDLR(context.Background(), final); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordFinalDLR(context.Background(), final); err != nil {
		t.Fatalf("duplicate final DLR: %v", err)
	}
	contradictory := final
	contradictory.Status = "UNDELIV"
	if err := repository.RecordFinalDLR(context.Background(), contradictory); !errors.Is(err, cdr.ErrInvalidInput) {
		t.Fatalf("contradictory final DLR=%v", err)
	}
	record, err := repository.GetCDR(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != cdr.StateSMSCAccepted ||
		record.BillingOutcome != cdr.BillingApplied ||
		record.ActualLateAmount != 0.5 ||
		record.LateBillingAt == nil || !record.LateBillingAt.Equal(appliedAt) ||
		record.DeliveryState != cdr.DeliveryDelivered ||
		record.DeliveryStatus != "DELIVRD" ||
		record.DeliveryDoneAt == nil || !record.DeliveryDoneAt.Equal(doneAt) ||
		record.DeliveryReceivedAt == nil || !record.DeliveryReceivedAt.Equal(receivedAt) {
		t.Fatalf("settled record=%+v", record)
	}
	events, err := repository.ListCDREvents(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[cdr.EventKind]int)
	for _, event := range events {
		counts[event.Kind]++
	}
	if counts[cdr.EventLateBillApplied] != 1 || counts[cdr.EventFinalDLR] != 1 {
		t.Fatalf("event counts=%v events=%+v", counts, events)
	}
	report, err := repository.ReconcileCDRs(context.Background(), receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Healthy() {
		t.Fatalf("reconciliation=%+v", report)
	}
}

func TestCDRRejectedLateBillingIsRecorded(t *testing.T) {
	repository, _ := newSubmitStore(t)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	id := admitAcceptedCDR(t, repository, "rejected-billing", now, 0.5)
	if err := repository.MarkBillingRejected(
		context.Background(), id+":20-late-billing", now.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	record, err := repository.GetCDR(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if record.BillingOutcome != cdr.BillingRejected || record.ActualLateAmount != 0 {
		t.Fatalf("record=%+v", record)
	}
}

func TestCDRVersionedExportAuthorizationAuditCursorAndRetention(t *testing.T) {
	repository, db := newSubmitStore(t)
	old := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	firstID := admitAcceptedCDR(t, repository, "export-a", old, 0)
	secondID := admitAcceptedCDR(t, repository, "export-b", old.Add(time.Second), 0)
	for index, messageID := range []string{"export-a", "export-b"} {
		received := old.Add(time.Duration(index+1) * time.Minute)
		if err := repository.RecordFinalDLR(context.Background(), cdr.FinalDLR{
			QueueMessageID: messageID, ConnectorID: "smsc-a",
			Status: "UNDELIV", Error: "404", ReceivedAt: received,
		}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	service, err := cdr.NewService(repository, cdr.RetentionPolicy{Days: 365, BatchSize: 10}, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(context.Background(), cdr.Principal{Subject: "intruder"}, firstID); !errors.Is(err, cdr.ErrForbidden) {
		t.Fatalf("unauthorized read=%v", err)
	}
	exporter := cdr.Principal{Subject: "finance", Roles: []cdr.Role{cdr.RoleExporter}}
	firstPage, err := service.Export(context.Background(), exporter, cdr.ExportRequest{
		Limit: 1, Format: cdr.ExportJSONL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstPage.SchemaVersion != 1 || firstPage.RecordCount != 1 || firstPage.NextCursor == "" ||
		firstPage.ContentType != "application/x-ndjson; charset=utf-8" {
		t.Fatalf("first page=%+v", firstPage)
	}
	var projected map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(firstPage.Payload))), &projected); err != nil {
		t.Fatal(err)
	}
	if projected["schema_version"] != float64(1) || projected["cdr_id"] != firstID ||
		projected["currency"] != "EUR" {
		t.Fatalf("export=%v", projected)
	}
	for _, forbidden := range []string{"source_address", "destination_address", "message_content", "password", "callback_url"} {
		if _, present := projected[forbidden]; present {
			t.Fatalf("content/privacy field %q leaked", forbidden)
		}
	}
	secondPage, err := service.Export(context.Background(), exporter, cdr.ExportRequest{
		Cursor: firstPage.NextCursor, Limit: 1, Format: cdr.ExportCSV,
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondPage.RecordCount != 1 || !strings.Contains(string(secondPage.Payload), secondID) {
		t.Fatalf("second page=%+v payload=%s", secondPage, secondPage.Payload)
	}
	operator := cdr.Principal{Subject: "retention-job", Roles: []cdr.Role{cdr.RoleOperator}}
	result, err := service.Prune(context.Background(), operator)
	if err != nil {
		t.Fatal(err)
	}
	if result.Records != 2 || result.Events < 4 {
		t.Fatalf("prune=%+v", result)
	}
	if _, err := repository.GetCDR(context.Background(), firstID); !errors.Is(err, cdr.ErrNotFound) {
		t.Fatalf("pruned record=%v", err)
	}
	var auditCount int
	if err := db.QueryRow(`SELECT count(*) FROM cdr_access_audit`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 4 { // denied read, two exports, retention
		t.Fatalf("audit rows=%d want 4", auditCount)
	}
}

func TestCDRExportRejectsTamperedCursorAfterAuditing(t *testing.T) {
	repository, db := newSubmitStore(t)
	service, err := cdr.NewService(repository, cdr.RetentionPolicy{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Export(context.Background(),
		cdr.Principal{Subject: "finance", Roles: []cdr.Role{cdr.RoleExporter}},
		cdr.ExportRequest{Cursor: "not-base64", Limit: 1})
	if !errors.Is(err, cdr.ErrInvalidInput) {
		t.Fatalf("cursor error=%v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM cdr_access_audit`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit count=%d", count)
	}
}
