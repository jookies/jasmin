package dlr

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
)

// writeGatedHTTPDLR seeds a level-2 httpapi DLR record carrying a gate decision,
// which is what the submit path writes for a user with the registry gate on.
func writeGatedHTTPDLR(t *testing.T, client *rediscompat.Client, queueMsgID string, gate rediscompat.GateOverride) {
	t.Helper()
	key, err := rediscompat.BuildDLRKey(queueMsgID)
	if err != nil {
		t.Fatalf("build dlr key: %v", err)
	}
	record, err := rediscompat.NewHTTPDLRRecord(key, rediscompat.HTTPDLRRequest{
		URL: "http://cb/dlr", Level: 2, Method: "POST", Connector: "smpp-01", ExpirySeconds: 86400,
		Gate: gate,
	})
	if err != nil {
		t.Fatalf("build http dlr: %v", err)
	}
	if err := client.WriteHashRecord(context.Background(), record); err != nil {
		t.Fatalf("seed dlr: %v", err)
	}
}

func writeGatedSMPPSDLR(t *testing.T, client *rediscompat.Client, queueMsgID, rdReceipt string, gate rediscompat.GateOverride) {
	t.Helper()
	key, _ := rediscompat.BuildDLRKey(queueMsgID)
	record, err := rediscompat.NewSMPPSDLRRecord(key, rediscompat.SMPPSDLRRequest{
		SystemID: "demoesme", SourceAddrTON: "AddrTon.INTERNATIONAL", SourceAddrNPI: "AddrNpi.ISDN",
		SourceAddress: "12345", DestinationAddrTON: "AddrTon.INTERNATIONAL", DestinationAddrNPI: "AddrNpi.ISDN",
		DestinationAddress: "380930242105", SubmissionDate: "2101011200",
		RegisteredDeliveryReceipt: rdReceipt, ExpirySeconds: 3600, Gate: gate,
	})
	if err != nil {
		t.Fatalf("build smpps dlr: %v", err)
	}
	if err := client.WriteHashRecord(context.Background(), record); err != nil {
		t.Fatalf("seed dlr: %v", err)
	}
}

// seedMapping installs the queue-msgid correlation the terminal leg resolves.
func seedMapping(t *testing.T, client *rediscompat.Client, codedID, queueMsgID, connectorType string) {
	t.Helper()
	key, err := rediscompat.BuildQueueMessageKey(codedID)
	if err != nil {
		t.Fatalf("build queue key: %v", err)
	}
	record, err := rediscompat.NewQueueMessageCorrelation(key, rediscompat.QueueMessageCorrelation{
		MessageID: queueMsgID, ConnectorType: connectorType, TTLSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("build mapping: %v", err)
	}
	if err := client.WriteHashRecord(context.Background(), record); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
}

func TestGateOverride_HTTPTerminalReceipt(t *testing.T) {
	cases := []struct {
		name       string
		gate       rediscompat.GateOverride
		upstream   string
		upstreamOK string
		wantStatus string
		wantErr    string
		wantDlvrd  string
	}{
		{
			name:     "registered number reports the hit receipt despite an upstream failure",
			gate:     rediscompat.GateOverride{Status: "DELIVRD", Error: "000"},
			upstream: "UNDELIV", upstreamOK: "011",
			wantStatus: "DELIVRD", wantErr: "000", wantDlvrd: "001",
		},
		{
			name:     "unregistered number reports the miss receipt despite an upstream success",
			gate:     rediscompat.GateOverride{Status: "REJECTD", Error: "008"},
			upstream: "DELIVRD", upstreamOK: "000",
			wantStatus: "REJECTD", wantErr: "008", wantDlvrd: "000",
		},
		{
			name:     "no gate leaves the upstream receipt alone",
			gate:     rediscompat.GateOverride{},
			upstream: "UNDELIV", upstreamOK: "011",
			wantStatus: "UNDELIV", wantErr: "011", wantDlvrd: "001",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			correlator, client, publisher := newCorrelator(t, Config{})
			writeGatedHTTPDLR(t, client, "q", testCase.gate)
			seedMapping(t, client, "6AAD5", "q", "httpapi")

			err := correlator.OnDeliverReceipt(context.Background(), DeliverReceiptEvent{
				RawDLRID: "6aad5", Base: MsgIDBaseSame, ConnectorID: "smpp-01",
				Status: testCase.upstream, Sub: "001", Dlvrd: "001",
				SubmitDate: "2101011200", DoneDate: "2101011201", Err: testCase.upstreamOK,
			})
			if err != nil {
				t.Fatalf("OnDeliverReceipt: %v", err)
			}
			if len(publisher.forwards) != 1 {
				t.Fatalf("expected one terminal forward, got %d", len(publisher.forwards))
			}
			forward := publisher.forwards[0]
			if forward.Status != testCase.wantStatus || forward.Err != testCase.wantErr {
				t.Fatalf("forward = %s/%s, want %s/%s", forward.Status, forward.Err, testCase.wantStatus, testCase.wantErr)
			}
			// dlvrd must agree with the reported status or the partner receives a
			// self-contradictory receipt.
			if forward.Dlvrd != testCase.wantDlvrd {
				t.Fatalf("Dlvrd = %q, want %q", forward.Dlvrd, testCase.wantDlvrd)
			}
			if forward.Sub != "001" {
				t.Fatalf("Sub = %q, want the submitted part count untouched", forward.Sub)
			}
		})
	}
}

// recordingCDR captures what the commercial record was told.
type recordingCDR struct{ final []cdr.FinalDLR }

func (r *recordingCDR) RecordFinalDLR(_ context.Context, final cdr.FinalDLR) error {
	r.final = append(r.final, final)
	return nil
}

// TestGateOverride_CDRKeepsTheUpstreamTruth is the guarantee that makes this
// feature safe to ship: the partner is told the gate's status, but what actually
// happened upstream is still what lands in the commercial record.
func TestGateOverride_CDRKeepsTheUpstreamTruth(t *testing.T) {
	correlator, client, publisher := newCorrelator(t, Config{})
	recorder := &recordingCDR{}
	WithFinalDLRRecorder(recorder, func() time.Time { return time.Unix(0, 0).UTC() })(correlator)

	writeGatedHTTPDLR(t, client, "q", rediscompat.GateOverride{Status: "DELIVRD", Error: "000"})
	seedMapping(t, client, "6AAD5", "q", "httpapi")

	err := correlator.OnDeliverReceipt(context.Background(), DeliverReceiptEvent{
		RawDLRID: "6aad5", Base: MsgIDBaseSame, ConnectorID: "smpp-01",
		Status: "UNDELIV", Sub: "001", Dlvrd: "000", Err: "011",
	})
	if err != nil {
		t.Fatalf("OnDeliverReceipt: %v", err)
	}
	if len(recorder.final) != 1 {
		t.Fatalf("expected one CDR record, got %d", len(recorder.final))
	}
	if recorder.final[0].Status != "UNDELIV" || recorder.final[0].Error != "011" {
		t.Fatalf("CDR = %s/%s, want the real upstream UNDELIV/011",
			recorder.final[0].Status, recorder.final[0].Error)
	}
	if publisher.forwards[0].Status != "DELIVRD" {
		t.Fatalf("partner forward = %s, want the gate's DELIVRD", publisher.forwards[0].Status)
	}
}

// TestGateOverride_SMPPSFailureOnlyReceiptIsForwarded covers why the override is
// applied before the connector switch: an ESME that asked only for failure
// receipts gets nothing for a real DELIVRD, but must get the gate's REJECTD.
func TestGateOverride_SMPPSFailureOnlyReceiptIsForwarded(t *testing.T) {
	correlator, client, publisher := newCorrelator(t, Config{})
	writeGatedSMPPSDLR(t, client, "q", rdReceiptRequestedForFailure,
		rediscompat.GateOverride{Status: "REJECTD", Error: "008"})
	seedMapping(t, client, "6AAD5", "q", "smppsapi")

	err := correlator.OnDeliverReceipt(context.Background(), DeliverReceiptEvent{
		RawDLRID: "6aad5", Base: MsgIDBaseSame, ConnectorID: "smpp-01", Status: "DELIVRD", Err: "000",
	})
	if err != nil {
		t.Fatalf("OnDeliverReceipt: %v", err)
	}
	if len(publisher.forwards) != 1 {
		t.Fatalf("expected the overridden failure receipt to be forwarded, got %d forwards", len(publisher.forwards))
	}
	forward := publisher.forwards[0]
	if forward.Target != ForwardSMPPS || forward.Status != "REJECTD" || forward.Err != "008" {
		t.Fatalf("forward = %+v, want an smpps REJECTD/008", forward)
	}
	if forward.SystemID != "demoesme" {
		t.Fatalf("SystemID = %q, want the submitting bind", forward.SystemID)
	}
}

// TestGateOverride_SMPPSSuccessSuppressedForFailureOnly is the control for the
// test above: without a gate, a real DELIVRD is still not forwarded to an ESME
// that asked only for failures.
func TestGateOverride_SMPPSSuccessSuppressedForFailureOnly(t *testing.T) {
	correlator, client, publisher := newCorrelator(t, Config{})
	writeGatedSMPPSDLR(t, client, "q", rdReceiptRequestedForFailure, rediscompat.GateOverride{})
	seedMapping(t, client, "6AAD5", "q", "smppsapi")

	err := correlator.OnDeliverReceipt(context.Background(), DeliverReceiptEvent{
		RawDLRID: "6aad5", Base: MsgIDBaseSame, ConnectorID: "smpp-01", Status: "DELIVRD", Err: "000",
	})
	if err != nil {
		t.Fatalf("OnDeliverReceipt: %v", err)
	}
	if len(publisher.forwards) != 0 {
		t.Fatalf("expected no forward, got %+v", publisher.forwards)
	}
}
