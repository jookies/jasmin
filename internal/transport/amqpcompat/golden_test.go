package amqpcompat_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type goldenDocument struct {
	Cases []goldenCase `json:"cases"`
}

type goldenCase struct {
	ID         string                     `json:"id"`
	RoutingKey string                     `json:"routing_key"`
	Properties map[string]json.RawMessage `json:"properties"`
	Body       goldenBody                 `json:"body"`
}

type goldenBody struct {
	WireBase64     string `json:"wire_base64"`
	WireSHA256     string `json:"wire_sha256"`
	PickleProtocol *int   `json:"pickle_protocol"`
}

func TestGoldenAMQPEnvelopes(t *testing.T) {
	document := loadGolden(t)
	if len(document.Cases) != 7 {
		t.Fatalf("fixture case count = %d, want 7", len(document.Cases))
	}

	for _, tc := range document.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			body, err := base64.StdEncoding.DecodeString(tc.Body.WireBase64)
			if err != nil {
				t.Fatalf("decode body: %v", err)
			}
			originalBody := append([]byte(nil), body...)
			properties := propertiesFromGolden(t, tc)

			envelope, err := amqpcompat.NewEnvelope(tc.RoutingKey, properties, body)
			if err != nil {
				t.Fatalf("NewEnvelope: %v", err)
			}
			if envelope.RoutingKey() != tc.RoutingKey {
				t.Fatalf("routing key = %q, want %q", envelope.RoutingKey(), tc.RoutingKey)
			}

			wantRoute := expectedRoutes()[tc.ID]
			route := envelope.Route()
			if route.Kind() != wantRoute.kind || route.Target() != wantRoute.target {
				t.Fatalf("route = (%v, %q), want (%v, %q)", route.Kind(), route.Target(), wantRoute.kind, wantRoute.target)
			}
			if !bytes.Equal(envelope.Body(), originalBody) {
				t.Fatal("body does not match fixture bytes")
			}
			sha := envelope.BodySHA256()
			if got := hex.EncodeToString(sha[:]); got != tc.Body.WireSHA256 {
				t.Fatalf("body SHA-256 = %s, want %s", got, tc.Body.WireSHA256)
			}
			assertPickleProtocol(t, envelope, tc.Body.PickleProtocol)
			assertPropertiesMatchGolden(t, envelope.Properties(), tc.Properties)

			if len(body) != 0 {
				body[0] ^= 0xff
				if !bytes.Equal(envelope.Body(), originalBody) {
					t.Fatal("constructor input mutation changed envelope body")
				}
				returned := envelope.Body()
				returned[0] ^= 0xff
				if !bytes.Equal(envelope.Body(), originalBody) {
					t.Fatal("body accessor exposed envelope storage")
				}
			}
		})
	}
}

func TestParseRoutingKey(t *testing.T) {
	tests := []struct {
		key    string
		kind   amqpcompat.RouteKind
		target string
	}{
		{"submit.sm.connector-a", amqpcompat.RouteSubmitSM, "connector-a"},
		{"submit.sm.resp.user-1", amqpcompat.RouteSubmitSMResponse, "user-1"},
		{"dlr.submit_sm_resp", amqpcompat.RouteDLRSubmitSMResponse, ""},
		{"dlr_thrower.http", amqpcompat.RouteDLRHTTP, ""},
		{"dlr_thrower.smpps", amqpcompat.RouteDLRSMPPS, ""},
		{"bill_request.submit_sm_resp.user-1", amqpcompat.RouteBillingSubmitSMResponse, "user-1"},
		{"deliver_sm_thrower.http", amqpcompat.RouteDeliverSMHTTP, ""},
		{"deliver_sm_thrower.smpps", amqpcompat.RouteDeliverSMSMPPS, ""},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			route, err := amqpcompat.ParseRoutingKey(tc.key)
			if err != nil {
				t.Fatalf("ParseRoutingKey: %v", err)
			}
			if route.Kind() != tc.kind || route.Target() != tc.target {
				t.Fatalf("route = (%v, %q), want (%v, %q)", route.Kind(), route.Target(), tc.kind, tc.target)
			}
		})
	}
}

func TestParseRoutingKeyRejectsMalformedValues(t *testing.T) {
	for _, key := range []string{
		"",
		"unknown.route",
		"submit.sm.",
		"submit.sm.resp.",
		"submit.sm.connector.extra",
		"bill_request.submit_sm_resp.",
		strings.Repeat("a", 256),
	} {
		t.Run(key, func(t *testing.T) {
			_, err := amqpcompat.ParseRoutingKey(key)
			if !errors.Is(err, amqpcompat.ErrInvalidRoutingKey) {
				t.Fatalf("error = %v, want ErrInvalidRoutingKey", err)
			}
		})
	}
}

func TestEnvelopeAndPropertiesRejectInvalidValues(t *testing.T) {
	t.Run("missing message ID", func(t *testing.T) {
		_, err := amqpcompat.NewProperties("", nil)
		if !errors.Is(err, amqpcompat.ErrInvalidProperties) {
			t.Fatalf("error = %v, want ErrInvalidProperties", err)
		}
	})

	t.Run("empty header name", func(t *testing.T) {
		_, err := amqpcompat.NewProperties("message-1", map[string]amqpcompat.Field{
			"": amqpcompat.StringField("value"),
		})
		if !errors.Is(err, amqpcompat.ErrInvalidProperties) {
			t.Fatalf("error = %v, want ErrInvalidProperties", err)
		}
	})

	t.Run("too many headers", func(t *testing.T) {
		headers := make(map[string]amqpcompat.Field, amqpcompat.MaxHeaders+1)
		for index := 0; index <= amqpcompat.MaxHeaders; index++ {
			headers[string(rune(index+1))] = amqpcompat.IntegerField(int64(index))
		}
		_, err := amqpcompat.NewProperties("message-1", headers)
		if !errors.Is(err, amqpcompat.ErrInvalidProperties) {
			t.Fatalf("error = %v, want ErrInvalidProperties", err)
		}
	})

	t.Run("oversized header table", func(t *testing.T) {
		_, err := amqpcompat.NewProperties("message-1", map[string]amqpcompat.Field{
			"binary": amqpcompat.BytesField(make([]byte, amqpcompat.MaxHeaderBytes+1)),
		})
		if !errors.Is(err, amqpcompat.ErrInvalidProperties) {
			t.Fatalf("error = %v, want ErrInvalidProperties", err)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		properties, err := amqpcompat.NewProperties("message-1", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = amqpcompat.NewEnvelope("dlr.submit_sm_resp", properties, make([]byte, amqpcompat.MaxBodySize+1))
		if !errors.Is(err, amqpcompat.ErrBodyTooLarge) {
			t.Fatalf("error = %v, want ErrBodyTooLarge", err)
		}
	})

	t.Run("zero-value properties", func(t *testing.T) {
		_, err := amqpcompat.NewEnvelope("dlr.submit_sm_resp", amqpcompat.Properties{}, nil)
		if !errors.Is(err, amqpcompat.ErrInvalidProperties) {
			t.Fatalf("error = %v, want ErrInvalidProperties", err)
		}
	})
}

func TestPropertiesAreDefensivelyCopied(t *testing.T) {
	binaryValue := []byte("opaque")
	headers := map[string]amqpcompat.Field{
		"text":   amqpcompat.StringField("value"),
		"number": amqpcompat.IntegerField(7),
		"binary": amqpcompat.BytesField(binaryValue),
	}
	properties, err := amqpcompat.NewProperties(
		"message-1",
		headers,
		amqpcompat.WithReplyTo("reply.queue"),
		amqpcompat.WithPriority(2),
	)
	if err != nil {
		t.Fatal(err)
	}

	binaryValue[0] = 'X'
	headers["binary"] = amqpcompat.StringField("replaced")
	assertFieldBytes(t, properties.Headers()["binary"], []byte("opaque"))

	returned := properties.Headers()
	returned["text"] = amqpcompat.StringField("mutated")
	binaryField := returned["binary"]
	binaryBytes, ok := binaryField.Bytes()
	if !ok {
		t.Fatal("binary field did not expose bytes kind")
	}
	binaryBytes[0] = 'X'

	actual := properties.Headers()
	assertFieldString(t, actual["text"], "value")
	assertFieldBytes(t, actual["binary"], []byte("opaque"))
	if messageID := properties.MessageID(); messageID != "message-1" {
		t.Fatalf("message ID = %q", messageID)
	}
	if replyTo, ok := properties.ReplyTo(); !ok || replyTo != "reply.queue" {
		t.Fatalf("reply-to = (%q, %v)", replyTo, ok)
	}
	if priority, ok := properties.Priority(); !ok || priority != 2 {
		t.Fatalf("priority = (%d, %v)", priority, ok)
	}
}

func TestLegacyPickleDetectionIsPassive(t *testing.T) {
	properties, err := amqpcompat.NewProperties("message-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body     []byte
		protocol uint8
		detected bool
	}{
		{nil, 0, false},
		{[]byte{0x80}, 0, false},
		{[]byte{0x80, 2, 'x'}, 2, true},
		{[]byte{0x80, 5, 'x'}, 5, true},
		{[]byte{0x80, 0xff}, 0xff, true},
		{[]byte{'x', 2}, 0, false},
	} {
		envelope, err := amqpcompat.NewEnvelope("dlr.submit_sm_resp", properties, tc.body)
		if err != nil {
			t.Fatal(err)
		}
		protocol, detected := envelope.LegacyPickleProtocol()
		if protocol != tc.protocol || detected != tc.detected {
			t.Fatalf("body %x: got (%d, %v), want (%d, %v)", tc.body, protocol, detected, tc.protocol, tc.detected)
		}
	}
}

func FuzzEnvelopeNeverPanics(f *testing.F) {
	for _, key := range []string{
		"", "submit.sm.connector-a", "submit.sm.resp.user-1", "dlr.submit_sm_resp", "unknown.route",
	} {
		f.Add(key, []byte{0x80, 2, 'x'})
	}
	properties, err := amqpcompat.NewProperties("message-1", map[string]amqpcompat.Field{
		"binary": amqpcompat.BytesField([]byte("seed")),
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, routingKey string, body []byte) {
		_, _ = amqpcompat.ParseRoutingKey(routingKey)
		envelope, err := amqpcompat.NewEnvelope(routingKey, properties, body)
		if err == nil {
			_ = envelope.Body()
			_ = envelope.Properties().Headers()
			_, _ = envelope.LegacyPickleProtocol()
			_ = envelope.BodySHA256()
		}
	})
}

type expectedRoute struct {
	kind   amqpcompat.RouteKind
	target string
}

func expectedRoutes() map[string]expectedRoute {
	return map[string]expectedRoute{
		"submit_sm_httpapi":         {amqpcompat.RouteSubmitSM, "connector-a"},
		"submit_sm_resp":            {amqpcompat.RouteSubmitSMResponse, "user-1"},
		"dlr_lookup_submit_sm_resp": {amqpcompat.RouteDLRSubmitSMResponse, ""},
		"dlr_http_thrower":          {amqpcompat.RouteDLRHTTP, ""},
		"dlr_smpps_thrower":         {amqpcompat.RouteDLRSMPPS, ""},
		"bill_submit_sm_resp":       {amqpcompat.RouteBillingSubmitSMResponse, "user-1"},
		"routed_deliver_sm_http":    {amqpcompat.RouteDeliverSMHTTP, ""},
	}
}

func propertiesFromGolden(t *testing.T, tc goldenCase) amqpcompat.Properties {
	t.Helper()
	messageID := decodeJSONString(t, tc.Properties["message-id"])

	var headersRaw map[string]json.RawMessage
	if err := json.Unmarshal(tc.Properties["headers"], &headersRaw); err != nil {
		t.Fatalf("decode headers: %v", err)
	}
	headers := make(map[string]amqpcompat.Field, len(headersRaw))
	for name, raw := range headersRaw {
		headers[name] = fieldFromGolden(t, name, raw)
	}

	options := make([]amqpcompat.PropertyOption, 0, 2)
	if raw, ok := tc.Properties["reply-to"]; ok {
		options = append(options, amqpcompat.WithReplyTo(decodeJSONString(t, raw)))
	}
	if raw, ok := tc.Properties["priority"]; ok {
		var priority uint8
		if err := json.Unmarshal(raw, &priority); err != nil {
			t.Fatalf("decode priority: %v", err)
		}
		options = append(options, amqpcompat.WithPriority(priority))
	}
	properties, err := amqpcompat.NewProperties(messageID, headers, options...)
	if err != nil {
		t.Fatalf("NewProperties: %v", err)
	}
	return properties
}

func fieldFromGolden(t *testing.T, name string, raw json.RawMessage) amqpcompat.Field {
	t.Helper()
	if len(raw) != 0 && raw[0] == '"' {
		return amqpcompat.StringField(decodeJSONString(t, raw))
	}
	if len(raw) != 0 && raw[0] == '{' {
		var descriptor struct {
			Type   string `json:"type"`
			Base64 string `json:"base64"`
		}
		if err := json.Unmarshal(raw, &descriptor); err != nil {
			t.Fatalf("decode header %s: %v", name, err)
		}
		if descriptor.Type != "bytes" {
			t.Fatalf("header %s object type = %q", name, descriptor.Type)
		}
		value, err := base64.StdEncoding.DecodeString(descriptor.Base64)
		if err != nil {
			t.Fatalf("decode header %s bytes: %v", name, err)
		}
		return amqpcompat.BytesField(value)
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode integer header %s: %v", name, err)
	}
	return amqpcompat.IntegerField(value)
}

func assertPropertiesMatchGolden(t *testing.T, got amqpcompat.Properties, raw map[string]json.RawMessage) {
	t.Helper()
	if got.MessageID() != decodeJSONString(t, raw["message-id"]) {
		t.Fatalf("message ID = %q", got.MessageID())
	}
	wantReply, hasReply := raw["reply-to"]
	actualReply, actualHasReply := got.ReplyTo()
	if actualHasReply != hasReply || hasReply && actualReply != decodeJSONString(t, wantReply) {
		t.Fatalf("reply-to = (%q, %v), want presence %v", actualReply, actualHasReply, hasReply)
	}
	wantPriority, hasPriority := raw["priority"]
	actualPriority, actualHasPriority := got.Priority()
	if actualHasPriority != hasPriority {
		t.Fatalf("priority presence = %v, want %v", actualHasPriority, hasPriority)
	}
	if hasPriority {
		var expected uint8
		if err := json.Unmarshal(wantPriority, &expected); err != nil {
			t.Fatal(err)
		}
		if actualPriority != expected {
			t.Fatalf("priority = %d, want %d", actualPriority, expected)
		}
	}

	var expectedHeaders map[string]json.RawMessage
	if err := json.Unmarshal(raw["headers"], &expectedHeaders); err != nil {
		t.Fatal(err)
	}
	actualHeaders := got.Headers()
	if len(actualHeaders) != len(expectedHeaders) {
		t.Fatalf("header count = %d, want %d", len(actualHeaders), len(expectedHeaders))
	}
	for name, expectedRaw := range expectedHeaders {
		expected := fieldFromGolden(t, name, expectedRaw)
		assertFieldEqual(t, name, actualHeaders[name], expected)
	}
}

func assertFieldEqual(t *testing.T, name string, got, want amqpcompat.Field) {
	t.Helper()
	if got.Kind() != want.Kind() {
		t.Fatalf("header %s kind = %v, want %v", name, got.Kind(), want.Kind())
	}
	switch want.Kind() {
	case amqpcompat.FieldString:
		value, _ := want.String()
		assertFieldString(t, got, value)
	case amqpcompat.FieldInteger:
		value, _ := want.Integer()
		actual, ok := got.Integer()
		if !ok || actual != value {
			t.Fatalf("header %s integer = (%d, %v), want %d", name, actual, ok, value)
		}
	case amqpcompat.FieldBytes:
		value, _ := want.Bytes()
		assertFieldBytes(t, got, value)
	default:
		t.Fatalf("header %s unexpected kind %v", name, want.Kind())
	}
}

func assertFieldString(t *testing.T, field amqpcompat.Field, want string) {
	t.Helper()
	got, ok := field.String()
	if !ok || got != want {
		t.Fatalf("string field = (%q, %v), want %q", got, ok, want)
	}
}

func assertFieldBytes(t *testing.T, field amqpcompat.Field, want []byte) {
	t.Helper()
	got, ok := field.Bytes()
	if !ok || !bytes.Equal(got, want) {
		t.Fatalf("bytes field = (%x, %v), want %x", got, ok, want)
	}
}

func assertPickleProtocol(t *testing.T, envelope amqpcompat.Envelope, want *int) {
	t.Helper()
	got, ok := envelope.LegacyPickleProtocol()
	if want == nil {
		if ok {
			t.Fatalf("pickle protocol detected as %d, want none", got)
		}
		return
	}
	if !ok || int(got) != *want {
		t.Fatalf("pickle protocol = (%d, %v), want %d", got, ok, *want)
	}
}

func decodeJSONString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode JSON string: %v", err)
	}
	return value
}

func loadGolden(t *testing.T) goldenDocument {
	t.Helper()
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "amqp", "baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var document goldenDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return document
}

func TestFixtureBodyHashesAreInternallyConsistent(t *testing.T) {
	for _, tc := range loadGolden(t).Cases {
		body, err := base64.StdEncoding.DecodeString(tc.Body.WireBase64)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != tc.Body.WireSHA256 {
			t.Fatalf("fixture %s has inconsistent body hash", tc.ID)
		}
	}
}
