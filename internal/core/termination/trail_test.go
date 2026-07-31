package termination

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

type stubTrailReader struct {
	page    msgspool.SearchPage
	request msgspool.SearchRequest
}

func (r *stubTrailReader) Search(
	_ context.Context,
	_ msgspool.Principal,
	request msgspool.SearchRequest,
) (msgspool.SearchPage, error) {
	r.request = request
	return r.page, nil
}

func trailRecord(verdict msgspool.Verdict) msgspool.Record {
	received := time.Date(2026, 7, 30, 18, 22, 41, 0, time.UTC)
	due := received.Add(5 * time.Second)
	return msgspool.Record{
		Message: msgspool.Message{
			MessageID: "019428c1", ConnectorID: "partner-a-term", UserID: "partner-a",
			SourceAddr: "NETFLIX", DestAddr: "380671234567",
			Text: "ПРОВЕРОЧНЫЙ КОД 63125", Raw: []byte{0x04, 0x1f},
			DataCoding: 8, Encoding: "ucs2", Parts: 1,
			Verdict: verdict, ReceivedAt: received, ReceiptDueAt: &due,
		},
		Sequence: 7, DeliveryState: msgspool.DeliveryPending, DeliveryAttempts: 1,
	}
}

// TestDecisionEntryReproducesLegacyOutcomes pins the vocabulary the legacy
// dlr:audit stream used, including the third value that exists only to keep a
// fail-open accept distinguishable from a real one.
func TestDecisionEntryReproducesLegacyOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		verdict msgspool.Verdict
		outcome string
		dlvrd   int
	}{
		{
			"accepted", msgspool.Verdict{Accept: true, Stat: "DELIVRD", Err: "000"},
			OutcomeDelivered, 1,
		},
		{
			"rejected", msgspool.Verdict{Accept: false, Stat: "REJECTD", Err: "008"},
			OutcomeRejected, 0,
		},
		{
			"fail-open accept",
			msgspool.Verdict{Accept: true, Stat: "DELIVRD", Err: "000", GateBypassed: true},
			OutcomeDeliveredFailOpen, 1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			entry := DecisionEntryFromRecord(trailRecord(testCase.verdict))
			if entry.Outcome != testCase.outcome {
				t.Errorf("outcome = %q, want %q", entry.Outcome, testCase.outcome)
			}
			if entry.Dlvrd != testCase.dlvrd {
				t.Errorf("dlvrd = %d, want %d", entry.Dlvrd, testCase.dlvrd)
			}
			// The invariant the whole step exists for: the delivered count is
			// derived, so it can never contradict the status it sits beside.
			if entry.Dlvrd > 0 && entry.Stat != "DELIVRD" {
				t.Errorf("entry claims %d delivered with stat %q", entry.Dlvrd, entry.Stat)
			}
			if entry.GateBypassed != testCase.verdict.GateBypassed {
				t.Errorf("gate_bypassed = %t, want %t", entry.GateBypassed, testCase.verdict.GateBypassed)
			}
		})
	}
}

// TestDecisionTrailNeverCarriesContent is the security property: the trail is
// meant to be queried freely, so it must not become a second reveal surface for
// OTP bodies.
func TestDecisionTrailNeverCarriesContent(t *testing.T) {
	reader := &stubTrailReader{page: msgspool.SearchPage{
		Records:    []msgspool.Record{trailRecord(msgspool.Verdict{Accept: true, Stat: "DELIVRD", Err: "000"})},
		NextCursor: "cursor-2",
	}}
	page, err := DecisionTrail(context.Background(), reader, msgspool.Principal{
		Subject: "ops", Roles: []msgspool.Role{msgspool.RoleReader},
	}, TrailRequest{Partner: "partner-a", GateBypassedOnly: true, Stat: "DELIVRD", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if reader.request.IncludeContent {
		t.Error("trail asked the spool for message content")
	}
	if reader.request.UserID != "partner-a" || reader.request.VerdictStat != "DELIVRD" ||
		!reader.request.GateBypassedOnly || reader.request.Limit != 50 {
		t.Errorf("trail did not pass its filters through: %+v", reader.request)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(page.Entries))
	}
	if page.NextCursor != "cursor-2" {
		t.Errorf("next cursor = %q, want cursor-2", page.NextCursor)
	}
	// The projection has no field that could carry a body; assert on the shape
	// rather than trusting the type never grows one.
	entry := page.Entries[0]
	if entry.MessageID == "" || entry.Reason != "" && entry.Reason == "ПРОВЕРОЧНЫЙ КОД 63125" {
		t.Errorf("trail entry looks like it carries content: %+v", entry)
	}
}

func TestDecisionTrailRefusesNilReader(t *testing.T) {
	if _, err := DecisionTrail(context.Background(), nil, msgspool.Principal{}, TrailRequest{}); err == nil {
		t.Fatal("want an error for a nil reader")
	}
}

// A partner receives one receipt per segment of a long message, because each
// segment is its own submit_sm under SMPP 3.4. Those rows carry no content and
// are excluded from every other read, so without an explicit opt-in the trail
// answers "why did I get three receipts?" with one decision and no account of
// the other two.
func TestDecisionTrailCanShowSegmentReceipts(t *testing.T) {
	reader := &stubTrailReader{}
	if _, err := DecisionTrail(context.Background(), reader, msgspool.Principal{Subject: "operator"}, TrailRequest{}); err != nil {
		t.Fatalf("trail: %v", err)
	}
	if reader.request.IncludeReceiptOnly {
		t.Error("segment receipts included by default; every other read excludes them")
	}

	if _, err := DecisionTrail(context.Background(), reader, msgspool.Principal{Subject: "operator"}, TrailRequest{IncludeSegmentReceipts: true}); err != nil {
		t.Fatalf("trail: %v", err)
	}
	if !reader.request.IncludeReceiptOnly {
		t.Error("IncludeSegmentReceipts did not reach the query; the operator cannot see the segment receipts")
	}
	if reader.request.IncludeContent {
		t.Error("the trail asked for content; it must never carry OTP text")
	}
}
