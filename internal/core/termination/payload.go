package termination

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// payloadTimeFormat renders received_at as RFC 3339 with a fixed three-digit
// fractional second, matching the delivery contract's
// "2026-07-30T18:22:41.113Z". time.RFC3339Nano is deliberately not used: it
// trims trailing zeros, so the same instant would sometimes serialize with
// milliseconds and sometimes without, and a receiver parsing by string length
// would break on the round second.
const payloadTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// PayloadVerdictLookup supplies the verdict a delivery payload reports.
//
// The verdict is decided before delivery in every mode except http-inline —
// where the delivery response is what carries it — so the sink cannot derive it
// from the Message alone and the connector worker provides this seam instead.
// Returning false means "not decided yet", which is the normal state in
// http-inline mode; the payload then omits the value rather than guessing one.
type PayloadVerdictLookup interface {
	VerdictFor(ctx context.Context, msg Message) (Verdict, bool)
}

// PayloadVerdictFunc adapts a plain function to PayloadVerdictLookup.
type PayloadVerdictFunc func(ctx context.Context, msg Message) (Verdict, bool)

// VerdictFor implements PayloadVerdictLookup.
func (f PayloadVerdictFunc) VerdictFor(ctx context.Context, msg Message) (Verdict, bool) {
	return f(ctx, msg)
}

// DeliveryPayload is the JSON body of a downstream delivery, defined by the
// "Delivery contract" section of docs/plans/021-mt-termination-connector.md.
//
// Field names and their order here are the wire contract: a receiver verifies
// the signature over these exact bytes, so renaming or reordering a field is a
// breaking change for every integration, not a refactor.
type DeliveryPayload struct {
	// MessageID is the gateway id and the idempotency key. The same value
	// appears in the X-Synevyr-Message-Id header, in the receipt the partner
	// receives, and in every retry of this delivery.
	MessageID string `json:"message_id"`
	// Connector is the termination connector that handled the message.
	Connector string `json:"connector"`
	// Partner is the submitting user.
	Partner string `json:"partner"`
	// From is the source address as submitted, frequently alphanumeric.
	From string `json:"from"`
	// To is the destination, normalized to digits.
	To string `json:"to"`
	// Text is the decoded message with all parts joined.
	Text string `json:"text"`
	// RawHex is the pre-decode payload, all parts concatenated, lowercase hex.
	// It is carried deliberately: a disagreement about what a message said is
	// only settleable with the bytes.
	RawHex string `json:"raw_hex"`
	// DataCoding is the submitted data_coding value, rendered as a number.
	DataCoding byte `json:"dcs"`
	// Encoding names the codec that actually produced Text, which is not always
	// what DataCoding claimed.
	Encoding string `json:"encoding"`
	// Parts is how many segments were reassembled.
	Parts int `json:"parts"`
	// ReceivedAt is when the last segment arrived, UTC, millisecond precision.
	ReceivedAt string `json:"received_at"`
	// Verdict is what the partner was told, or will be told, lowercased
	// ("delivrd", "rejectd"). It is empty when no verdict has been decided yet,
	// which is the normal state in http-inline mode where this very request is
	// what asks for it.
	Verdict string `json:"verdict"`
}

// BuildDeliveryPayload projects a Message and its verdict onto the wire
// contract. hasVerdict distinguishes "no verdict decided" from a zero Verdict,
// which would otherwise be indistinguishable and would send an empty status as
// if it were a decision.
func BuildDeliveryPayload(msg Message, verdict Verdict, hasVerdict bool) DeliveryPayload {
	stat := ""
	if hasVerdict {
		stat = strings.ToLower(strings.TrimSpace(verdict.Stat))
	}
	return DeliveryPayload{
		MessageID:  msg.MessageID,
		Connector:  msg.Connector,
		Partner:    msg.Partner,
		From:       msg.From,
		To:         msg.To,
		Text:       msg.Text,
		RawHex:     hex.EncodeToString(msg.Raw),
		DataCoding: msg.DataCoding,
		Encoding:   msg.Encoding,
		Parts:      payloadPartCount(msg.Parts),
		ReceivedAt: msg.ReceivedAt.UTC().Format(payloadTimeFormat),
		Verdict:    stat,
	}
}

// MarshalDeliveryPayload renders the JSON body.
//
// The result is the byte sequence that is both signed and sent. Callers must
// marshal once and reuse the buffer: re-marshalling for the signature would let
// any encoder difference — a Go version's map ordering, an added field, HTML
// escaping — produce a signature over bytes the receiver never saw, which fails
// as "every signature is invalid" rather than as an error here.
func MarshalDeliveryPayload(payload DeliveryPayload) ([]byte, error) {
	return json.Marshal(payload)
}

// BuildLegacyDeliveryForm renders the legacy Jasmin form-encoded body, kept
// selectable per connector so an application already written against the MO
// callback shape can receive terminated traffic without a new handler.
//
// The first six values and their spellings mirror mo.Delivery.args
// (internal/core/mo/http_thrower.go:58) exactly, including "coding" being the
// raw data_coding byte percent-encoded rather than a decimal rendering — that
// quirk is what an existing handler was written against. The remaining values
// are additive: the MO shape has no field for the partner, the part count or
// the verdict, and a form parser ignores keys it does not know.
func BuildLegacyDeliveryForm(msg Message, verdict Verdict, hasVerdict bool) url.Values {
	v := url.Values{}
	v.Set("id", msg.MessageID)
	v.Set("from", msg.From)
	v.Set("to", msg.To)
	v.Set("origin-connector", msg.Connector)
	v.Set("content", msg.Text)
	v.Set("binary", hex.EncodeToString(msg.Raw))
	v.Set("coding", string([]byte{msg.DataCoding}))
	v.Set("partner", msg.Partner)
	v.Set("encoding", msg.Encoding)
	v.Set("parts", strconv.Itoa(payloadPartCount(msg.Parts)))
	v.Set("received_at", msg.ReceivedAt.UTC().Format(payloadTimeFormat))
	if hasVerdict {
		v.Set("verdict", strings.ToLower(strings.TrimSpace(verdict.Stat)))
	}
	return v
}

// payloadPartCount floors the reported part count at 1. A whole message carries
// Parts == 1; a zero would reach the application as "this message has no parts",
// which is never true of a message that was assembled and delivered.
func payloadPartCount(parts int) int {
	if parts < 1 {
		return 1
	}
	return parts
}

// deliveryTimestamp renders the value of X-Synevyr-Timestamp: Unix seconds,
// which is what the signature covers alongside the body.
func deliveryTimestamp(at time.Time) string {
	return strconv.FormatInt(at.UTC().Unix(), 10)
}
