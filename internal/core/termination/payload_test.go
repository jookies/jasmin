package termination

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

// payloadTestMessage is a fully populated Message: two-part Cyrillic UCS-2, the
// shape the delivery contract's example was written from.
func payloadTestMessage(t *testing.T) Message {
	t.Helper()
	raw, err := hex.DecodeString("041f0420")
	if err != nil {
		t.Fatalf("decode raw fixture: %v", err)
	}
	return Message{
		MessageID:  "019428c1-7f3a-7b21-9c04-8d1e2f6a5b40",
		Connector:  "partner-a-term",
		Partner:    "partner-a",
		From:       "NETFLIX",
		To:         "380671234567",
		Text:       "ПРОВЕРОЧНЫЙ КОД 63125",
		Raw:        raw,
		DataCoding: 8,
		Encoding:   "ucs2",
		Parts:      2,
		ReceivedAt: time.Date(2026, 7, 30, 18, 22, 41, 113_000_000, time.UTC),
	}
}

func TestBuildDeliveryPayloadMatchesContract(t *testing.T) {
	msg := payloadTestMessage(t)
	verdict := Verdict{Accept: true, Stat: "DELIVRD", Err: "000", Reason: "activation window open"}

	got := BuildDeliveryPayload(msg, verdict, true)

	want := DeliveryPayload{
		MessageID:  "019428c1-7f3a-7b21-9c04-8d1e2f6a5b40",
		Connector:  "partner-a-term",
		Partner:    "partner-a",
		From:       "NETFLIX",
		To:         "380671234567",
		Text:       "ПРОВЕРОЧНЫЙ КОД 63125",
		RawHex:     "041f0420",
		DataCoding: 8,
		Encoding:   "ucs2",
		Parts:      2,
		ReceivedAt: "2026-07-30T18:22:41.113Z",
		Verdict:    "delivrd",
	}
	if got != want {
		t.Fatalf("payload mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// The JSON key set is the wire contract. A renamed or dropped field breaks every
// receiver's signature verification and its parser at once, so it is asserted
// exactly rather than by spot-checking a few fields.
func TestDeliveryPayloadJSONKeySetIsExact(t *testing.T) {
	body, err := MarshalDeliveryPayload(BuildDeliveryPayload(payloadTestMessage(t), Verdict{Stat: "REJECTD"}, true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := make([]string, 0, len(fields))
	for key := range fields {
		got = append(got, key)
	}
	sort.Strings(got)

	want := []string{
		"connector", "dcs", "encoding", "from", "message_id", "partner",
		"parts", "raw_hex", "received_at", "text", "to", "verdict",
	}
	if len(got) != len(want) {
		t.Fatalf("key set mismatch:\n got %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key set mismatch:\n got %v\nwant %v", got, want)
		}
	}

	// dcs must be a JSON number, not a quoted string: the contract's example is
	// "dcs": 8.
	if string(fields["dcs"]) != "8" {
		t.Fatalf("dcs = %s, want 8", fields["dcs"])
	}
}

func TestBuildDeliveryPayloadReceivedAtFormat(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{
			name: "milliseconds preserved",
			at:   time.Date(2026, 7, 30, 18, 22, 41, 113_000_000, time.UTC),
			want: "2026-07-30T18:22:41.113Z",
		},
		{
			// time.RFC3339Nano would render this as "...:41Z" and the receiver's
			// fixed-width parser would break on it.
			name: "round second keeps three digits",
			at:   time.Date(2026, 7, 30, 18, 22, 41, 0, time.UTC),
			want: "2026-07-30T18:22:41.000Z",
		},
		{
			name: "non-UTC location is converted",
			at:   time.Date(2026, 7, 30, 21, 22, 41, 113_000_000, time.FixedZone("EEST", 3*60*60)),
			want: "2026-07-30T18:22:41.113Z",
		},
		{
			name: "sub-millisecond precision is truncated",
			at:   time.Date(2026, 7, 30, 18, 22, 41, 113_999_999, time.UTC),
			want: "2026-07-30T18:22:41.113Z",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := payloadTestMessage(t)
			msg.ReceivedAt = tc.at
			if got := BuildDeliveryPayload(msg, Verdict{}, false).ReceivedAt; got != tc.want {
				t.Fatalf("received_at = %q, want %q", got, tc.want)
			}
		})
	}
}

// http-inline sends the payload before any verdict exists. An empty string says
// "not decided"; a zero Verdict must not be reported as a decision.
func TestBuildDeliveryPayloadWithoutVerdict(t *testing.T) {
	got := BuildDeliveryPayload(payloadTestMessage(t), Verdict{Accept: true, Stat: "DELIVRD"}, false)
	if got.Verdict != "" {
		t.Fatalf("verdict = %q, want empty when no verdict is decided", got.Verdict)
	}
}

func TestBuildDeliveryPayloadClampsPartCount(t *testing.T) {
	msg := payloadTestMessage(t)
	msg.Parts = 0
	if got := BuildDeliveryPayload(msg, Verdict{}, false).Parts; got != 1 {
		t.Fatalf("parts = %d, want 1", got)
	}
}

// The signature is computed over the marshalled bytes, so those bytes must be
// reproducible. A struct gives declaration order; a map would not.
func TestMarshalDeliveryPayloadIsByteStable(t *testing.T) {
	payload := BuildDeliveryPayload(payloadTestMessage(t), Verdict{Stat: "DELIVRD"}, true)
	first, err := MarshalDeliveryPayload(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 32; i++ {
		again, err := MarshalDeliveryPayload(payload)
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		if string(again) != string(first) {
			t.Fatalf("marshal is not byte-stable:\n first %s\nagain %s", first, again)
		}
	}
	if !strings.HasPrefix(string(first), `{"message_id":`) {
		t.Fatalf("field order changed, signatures over this body will not match a receiver's: %s", first)
	}
}

func TestBuildLegacyDeliveryForm(t *testing.T) {
	msg := payloadTestMessage(t)
	form := BuildLegacyDeliveryForm(msg, Verdict{Accept: true, Stat: "DELIVRD"}, true)

	// The first six mirror internal/core/mo/http_thrower.go:58 exactly.
	want := map[string]string{
		"id":               msg.MessageID,
		"from":             "NETFLIX",
		"to":               "380671234567",
		"origin-connector": "partner-a-term",
		"content":          msg.Text,
		"binary":           "041f0420",
		// The MO thrower sends the raw data_coding byte, not a decimal
		// rendering. An existing handler was written against that quirk.
		"coding":      string([]byte{8}),
		"partner":     "partner-a",
		"encoding":    "ucs2",
		"parts":       "2",
		"verdict":     "delivrd",
		"received_at": "2026-07-30T18:22:41.113Z",
	}
	for key, value := range want {
		if got := form.Get(key); got != value {
			t.Fatalf("form[%q] = %q, want %q", key, got, value)
		}
	}
	if len(form) != len(want) {
		t.Fatalf("form has %d keys, want %d: %v", len(form), len(want), form)
	}
}

func TestBuildLegacyDeliveryFormOmitsUndecidedVerdict(t *testing.T) {
	form := BuildLegacyDeliveryForm(payloadTestMessage(t), Verdict{Stat: "DELIVRD"}, false)
	if _, ok := form["verdict"]; ok {
		t.Fatalf("verdict present with no decision: %v", form)
	}
}

// An independent recomputation of the HMAC, written the way a downstream
// application would write it, over the documented signing input.
func TestDeliverySignatureIndependentRecomputation(t *testing.T) {
	secret := []byte("per-connector-secret")
	timestamp := "1785412800"
	body, err := MarshalDeliveryPayload(BuildDeliveryPayload(payloadTestMessage(t), Verdict{Stat: "DELIVRD"}, true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp + "." + string(body)))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if got := DeliverySignature(secret, timestamp, body); got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
	if !DeliveryVerifySignature(secret, timestamp, body, want) {
		t.Fatal("DeliveryVerifySignature rejected its own signature")
	}
}

func TestDeliverySignatureBindsEveryInput(t *testing.T) {
	secret := []byte("per-connector-secret")
	timestamp := "1785412800"
	body := []byte(`{"message_id":"a"}`)
	base := DeliverySignature(secret, timestamp, body)

	if DeliverySignature([]byte("other-secret"), timestamp, body) == base {
		t.Fatal("signature did not change with the secret")
	}
	if DeliverySignature(secret, "1785412801", body) == base {
		t.Fatal("signature did not change with the timestamp")
	}
	if DeliverySignature(secret, timestamp, []byte(`{"message_id":"b"}`)) == base {
		t.Fatal("signature did not change with the body")
	}
	// The separator is what stops a different timestamp/body split from signing
	// the same bytes: without it "1785412800"+"0X" and "17854128000"+"X" are
	// indistinguishable and the timestamp stops being bound to its body.
	if DeliverySignature(secret, "1785412800", []byte("0X")) == DeliverySignature(secret, "17854128000", []byte("X")) {
		t.Fatal("signing input is ambiguous across a timestamp/body split")
	}
}

func TestDeliveryVerifySignatureRejectsTampering(t *testing.T) {
	secret := []byte("per-connector-secret")
	timestamp := "1785412800"
	body := []byte(`{"message_id":"a"}`)
	signature := DeliverySignature(secret, timestamp, body)

	tests := []struct {
		name      string
		secret    []byte
		timestamp string
		body      []byte
		header    string
	}{
		{"wrong secret", []byte("nope"), timestamp, body, signature},
		{"replayed timestamp", secret, "1785412801", body, signature},
		{"tampered body", secret, timestamp, []byte(`{"message_id":"b"}`), signature},
		{"empty header", secret, timestamp, body, ""},
		{"missing prefix", secret, timestamp, body, strings.TrimPrefix(signature, "sha256=")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if DeliveryVerifySignature(tc.secret, tc.timestamp, tc.body, tc.header) {
				t.Fatal("verification accepted an invalid signature")
			}
		})
	}
}

func TestPayloadVerdictFuncAdapter(t *testing.T) {
	lookup := PayloadVerdictFunc(func(_ context.Context, msg Message) (Verdict, bool) {
		if msg.MessageID == "known" {
			return Verdict{Accept: true, Stat: "DELIVRD"}, true
		}
		return Verdict{}, false
	})

	if verdict, ok := lookup.VerdictFor(context.Background(), Message{MessageID: "known"}); !ok || verdict.Stat != "DELIVRD" {
		t.Fatalf("VerdictFor(known) = %+v, %v", verdict, ok)
	}
	if _, ok := lookup.VerdictFor(context.Background(), Message{MessageID: "other"}); ok {
		t.Fatal("VerdictFor(other) reported a verdict")
	}
}
