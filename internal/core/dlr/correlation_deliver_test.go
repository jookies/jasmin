package dlr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/cdr"
	"github.com/pumpitspace/jasmin/internal/state/rediscompat"
)

type finalCDRRecorder struct {
	values []cdr.FinalDLR
	err    error
}

func (recorder *finalCDRRecorder) RecordFinalDLR(_ context.Context, value cdr.FinalDLR) error {
	recorder.values = append(recorder.values, value)
	return recorder.err
}

// TestDeliver_EndToEnd_HTTP runs both legs: the submit_sm_resp leg installs the mapping,
// then the deliver_sm leg resolves it, forwards the level-2 receipt, and deletes the DLR.
func TestDeliver_EndToEnd_HTTP(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	ctx := context.Background()

	writeHTTPDLR(t, client, "q", 3) // level 3: keep after resp, terminal receipt expected
	if err := c.OnSubmitResp(ctx, SubmitRespEvent{QueueMsgID: "q", SMPPMsgID: "6AAD5", Status: "ESME_ROK"}); err != nil {
		t.Fatalf("submit leg: %v", err)
	}
	pub.forwards = nil // drop the level-1 forward; we assert only the terminal one

	err := c.OnDeliverReceipt(ctx, DeliverReceiptEvent{
		RawDLRID: "6aad5", Base: MsgIDBaseSame, ConnectorID: "smpp-01", Status: "DELIVRD",
		Sub: "001", Dlvrd: "001", SubmitDate: "2101011200", DoneDate: "2101011201", Err: "000", Text: "delivered",
	})
	if err != nil {
		t.Fatalf("deliver leg: %v", err)
	}
	if len(pub.forwards) != 1 {
		t.Fatalf("expected one terminal forward, got %d", len(pub.forwards))
	}
	f := pub.forwards[0]
	if f.Target != ForwardHTTP || f.Level != 2 {
		t.Errorf("expected level-2 http forward, got %+v", f)
	}
	if f.Connector != "6aad5" { // dlr_connector = the RAW receipt id (Jasmin quirk)
		t.Errorf("Connector = %q, want raw receipt id 6aad5", f.Connector)
	}
	if f.IDSMSC != "6AAD5" { // id_smsc = the coded id
		t.Errorf("IDSMSC = %q, want coded 6AAD5", f.IDSMSC)
	}
	if f.Sub != "001" || f.Dlvrd != "001" || f.Err != "000" || f.Text != "delivered" {
		t.Errorf("receipt fields not carried: %+v", f)
	}
	if !dlrDeleted(t, client, "q") {
		t.Error("DELIVRD is final: dlr must be deleted")
	}
}

func TestDeliver_FinalCDRIsRecordedBeforeCorrelationCleanup(t *testing.T) {
	recorder := &finalCDRRecorder{}
	now := time.Date(2026, 7, 29, 14, 30, 0, 0, time.UTC)
	c, client, publisher := newCorrelator(t, Config{})
	c.cdr = recorder
	c.now = func() time.Time { return now }
	ctx := context.Background()
	writeHTTPDLR(t, client, "q-cdr", 2)
	if err := c.OnSubmitResp(ctx, SubmitRespEvent{
		QueueMsgID: "q-cdr", SMPPMsgID: "ABC", Status: "ESME_ROK",
	}); err != nil {
		t.Fatal(err)
	}
	event := DeliverReceiptEvent{
		RawDLRID: "abc", Base: MsgIDBaseSame, ConnectorID: "smsc-a",
		Status: "UNDELIV", DoneDate: "2607291430", Err: "404",
	}
	recorder.err = errors.New("postgres unavailable")
	if err := c.OnDeliverReceipt(ctx, event); !errors.Is(err, recorder.err) {
		t.Fatalf("record failure=%v", err)
	}
	if dlrDeleted(t, client, "q-cdr") {
		t.Fatal("correlation record deleted before durable CDR commit")
	}
	recorder.err = nil
	if err := c.OnDeliverReceipt(ctx, event); err != nil {
		t.Fatal(err)
	}
	if !dlrDeleted(t, client, "q-cdr") {
		t.Fatal("correlation record retained after durable CDR commit")
	}
	if len(recorder.values) != 2 {
		t.Fatalf("recorder calls=%d want retry", len(recorder.values))
	}
	if len(publisher.forwards) != 1 {
		t.Fatalf("callback forwards=%d want one after durable settlement", len(publisher.forwards))
	}
	got := recorder.values[1]
	if got.QueueMessageID != "q-cdr" || got.ConnectorID != "smsc-a" ||
		got.Status != "UNDELIV" || got.Error != "404" ||
		got.DoneAt == nil || !got.DoneAt.Equal(time.Date(2026, 7, 29, 14, 30, 0, 0, time.UTC)) ||
		!got.ReceivedAt.Equal(now) {
		t.Fatalf("final CDR=%+v", got)
	}
}

// TestDeliver_BaseConversion proves the base coding correlates the legs: a decimal receipt
// id (base 1) resolves the mapping the resp leg wrote for the hex resp id.
func TestDeliver_BaseConversion(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	ctx := context.Background()

	writeHTTPDLR(t, client, "qb", 2)
	if err := c.OnSubmitResp(ctx, SubmitRespEvent{QueueMsgID: "qb", SMPPMsgID: "6AAD5", Status: "ESME_ROK"}); err != nil {
		t.Fatalf("submit leg: %v", err)
	}
	// Base 1: decimal receipt "436949" codes to hex "6AAD5" == the resp key.
	if err := c.OnDeliverReceipt(ctx, DeliverReceiptEvent{RawDLRID: "436949", Base: MsgIDBaseReceiptDec, Status: "DELIVRD"}); err != nil {
		t.Fatalf("deliver leg: %v", err)
	}
	if len(pub.forwards) != 1 || pub.forwards[0].IDSMSC != "6AAD5" {
		t.Errorf("base-1 correlation failed: %+v", pub.forwards)
	}
}

func TestDeliver_NonFinalKeepsDLR(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	ctx := context.Background()
	writeHTTPDLR(t, client, "qa", 2)
	_ = c.OnSubmitResp(ctx, SubmitRespEvent{QueueMsgID: "qa", SMPPMsgID: "ABC", Status: "ESME_ROK"})
	if err := c.OnDeliverReceipt(ctx, DeliverReceiptEvent{RawDLRID: "abc", Base: MsgIDBaseSame, Status: "ACCEPTD"}); err != nil {
		t.Fatalf("deliver leg: %v", err)
	}
	if len(pub.forwards) != 1 {
		t.Fatalf("expected one forward, got %d", len(pub.forwards))
	}
	if dlrDeleted(t, client, "qa") {
		t.Error("ACCEPTD is not final: dlr must be kept")
	}
}

func TestDeliver_MapNotFoundRetryable(t *testing.T) {
	c, _, _ := newCorrelator(t, Config{})
	err := c.OnDeliverReceipt(context.Background(), DeliverReceiptEvent{RawDLRID: "deadbeef", Base: MsgIDBaseSame, Status: "DELIVRD"})
	if !errors.Is(err, ErrDLRMapNotFound) {
		t.Errorf("missing mapping: got %v, want ErrDLRMapNotFound (retryable on this leg)", err)
	}
}

func TestDeliver_SCMismatch(t *testing.T) {
	c, client, _ := newCorrelator(t, Config{})
	ctx := context.Background()
	// Mapping says smppsapi, but the DLR record is httpapi -> DLRMapError.
	qkey, _ := rediscompat.BuildQueueMessageKey("ABC")
	rec, _ := rediscompat.NewQueueMessageCorrelation(qkey, rediscompat.QueueMessageCorrelation{MessageID: "qm", ConnectorType: "smppsapi", TTLSeconds: 60})
	_ = client.WriteHashRecord(ctx, rec)
	writeHTTPDLR(t, client, "qm", 2)
	err := c.OnDeliverReceipt(ctx, DeliverReceiptEvent{RawDLRID: "abc", Base: MsgIDBaseSame, Status: "DELIVRD"})
	if !errors.Is(err, ErrDLRMapInvalid) {
		t.Errorf("sc mismatch: got %v, want ErrDLRMapInvalid", err)
	}
}

func TestDeliver_SMPPS_Delivrd_Requested_ForwardAndDelete(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	ctx := context.Background()
	writeSMPPSDLR(t, client, "sd", rdReceiptRequested)
	_ = c.OnSubmitResp(ctx, SubmitRespEvent{QueueMsgID: "sd", SMPPMsgID: "ABC", Status: "ESME_ROK"})
	if err := c.OnDeliverReceipt(ctx, DeliverReceiptEvent{RawDLRID: "abc", Base: MsgIDBaseSame, Status: "DELIVRD", Err: "000"}); err != nil {
		t.Fatalf("deliver leg: %v", err)
	}
	if len(pub.forwards) != 1 || pub.forwards[0].Target != ForwardSMPPS || pub.forwards[0].SystemID != "sys1" || pub.forwards[0].Err != "000" {
		t.Fatalf("expected one smpps forward for sys1 with err, got %+v", pub.forwards)
	}
	if !dlrDeleted(t, client, "sd") {
		t.Error("DELIVRD is final: smpps dlr must be deleted")
	}
}

func TestDeliver_SMPPS_Success_ForFailureOnly_NoForward(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	ctx := context.Background()
	writeSMPPSDLR(t, client, "sf", rdReceiptRequestedForFailure)
	_ = c.OnSubmitResp(ctx, SubmitRespEvent{QueueMsgID: "sf", SMPPMsgID: "ABC", Status: "ESME_ROK"})
	// DELIVRD is a success state; FOR_FAILURE-only must NOT forward a success receipt.
	if err := c.OnDeliverReceipt(ctx, DeliverReceiptEvent{RawDLRID: "abc", Base: MsgIDBaseSame, Status: "DELIVRD"}); err != nil {
		t.Fatalf("deliver leg: %v", err)
	}
	if len(pub.forwards) != 0 {
		t.Errorf("success + FOR_FAILURE-only: expected no forward, got %d", len(pub.forwards))
	}
}

func TestDeliver_SMPPS_Undeliv_ForFailure_ForwardAndDelete(t *testing.T) {
	c, client, pub := newCorrelator(t, Config{})
	ctx := context.Background()
	writeSMPPSDLR(t, client, "su", rdReceiptRequestedForFailure)
	_ = c.OnSubmitResp(ctx, SubmitRespEvent{QueueMsgID: "su", SMPPMsgID: "ABC", Status: "ESME_ROK"})
	// UNDELIV is a non-success final state; FOR_FAILURE forwards it and deletes on final.
	if err := c.OnDeliverReceipt(ctx, DeliverReceiptEvent{RawDLRID: "abc", Base: MsgIDBaseSame, Status: "UNDELIV"}); err != nil {
		t.Fatalf("deliver leg: %v", err)
	}
	if len(pub.forwards) != 1 {
		t.Fatalf("undeliv + FOR_FAILURE: expected one forward, got %d", len(pub.forwards))
	}
	if !dlrDeleted(t, client, "su") {
		t.Error("UNDELIV is final: dlr must be deleted")
	}
}
