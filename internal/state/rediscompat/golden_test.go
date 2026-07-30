package rediscompat_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
)

type goldenDocument struct {
	Cases []goldenCase `json:"cases"`
}

type goldenCase struct {
	ID         string                 `json:"id"`
	KeyPattern string                 `json:"key_pattern"`
	RedisType  string                 `json:"redis_type"`
	Fields     map[string]goldenField `json:"fields"`
	TTLSeconds int64                  `json:"ttl_seconds"`
}

type goldenField struct {
	Type   string  `json:"type"`
	Value  string  `json:"value"`
	Base64 string  `json:"base64"`
	UTF8   *string `json:"utf8"`
}

func TestGoldenRedisState(t *testing.T) {
	document := loadGolden(t)
	if len(document.Cases) != 4 {
		t.Fatalf("fixture case count = %d, want 4", len(document.Cases))
	}
	seen := make(map[string]bool, len(document.Cases))

	for _, tc := range document.Cases {
		if seen[tc.ID] {
			t.Fatalf("duplicate fixture ID %q", tc.ID)
		}
		seen[tc.ID] = true
		t.Run(tc.ID, func(t *testing.T) {
			switch tc.ID {
			case "http_dlr_request":
				key := mustDLRKey(t, "11111111-1111-4111-8111-111111111111")
				record, err := rediscompat.NewHTTPDLRRecord(key, rediscompat.HTTPDLRRequest{
					URL:           "https://example.invalid/dlr",
					Level:         3,
					Method:        "POST",
					Connector:     "connector-a",
					ExpirySeconds: 86400,
				})
				if err != nil {
					t.Fatalf("NewHTTPDLRRecord: %v", err)
				}
				assertRecordMatchesGolden(t, record, tc)

			case "smpps_dlr_request":
				key := mustDLRKey(t, "22222222-2222-4222-8222-222222222222")
				record, err := rediscompat.NewSMPPSDLRRecord(key, rediscompat.SMPPSDLRRequest{
					SystemID:                  "client-a",
					SourceAddrTON:             "AddrTon.INTERNATIONAL",
					SourceAddrNPI:             "AddrNpi.ISDN",
					SourceAddress:             "1111",
					DestinationAddrTON:        "AddrTon.INTERNATIONAL",
					DestinationAddrNPI:        "AddrNpi.ISDN",
					DestinationAddress:        "2222",
					SubmissionDate:            "2026-01-02 03:04:05.678901",
					RegisteredDeliveryReceipt: "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED",
					ExpirySeconds:             86400,
				})
				if err != nil {
					t.Fatalf("NewSMPPSDLRRecord: %v", err)
				}
				assertRecordMatchesGolden(t, record, tc)

			case "smpp_to_queue_id":
				key, err := rediscompat.BuildQueueMessageKey("436949")
				if err != nil {
					t.Fatal(err)
				}
				record, err := rediscompat.NewQueueMessageCorrelation(key, rediscompat.QueueMessageCorrelation{
					MessageID:     "11111111-1111-4111-8111-111111111111",
					ConnectorType: "httpapi",
					TTLSeconds:    86400,
				})
				if err != nil {
					t.Fatalf("NewQueueMessageCorrelation: %v", err)
				}
				assertRecordMatchesGolden(t, record, tc)

			case "multipart_deliver_sm_part":
				key, err := rediscompat.BuildLegacyMultipartKey("connector-a", 42, "4040")
				if err != nil {
					t.Fatal(err)
				}
				field, ok := tc.Fields["1"]
				if !ok || len(tc.Fields) != 1 || field.Type != "bytes" || field.UTF8 != nil {
					t.Fatalf("multipart fields = %#v", tc.Fields)
				}
				payload, err := base64.StdEncoding.DecodeString(field.Base64)
				if err != nil {
					t.Fatalf("decode multipart payload: %v", err)
				}
				metadata, err := rediscompat.InspectLegacyMultipartPart(key, 1, tc.TTLSeconds, payload)
				if err != nil {
					t.Fatalf("InspectLegacyMultipartPart: %v", err)
				}
				if metadata.RedisType() != tc.RedisType || metadata.Key().Pattern() != tc.KeyPattern {
					t.Fatalf("metadata type/pattern = (%q, %q)", metadata.RedisType(), metadata.Key().Pattern())
				}
				if metadata.TTLSeconds() != tc.TTLSeconds || metadata.SegmentSequence() != 1 {
					t.Fatalf("metadata ttl/sequence = (%d, %d)", metadata.TTLSeconds(), metadata.SegmentSequence())
				}
				if metadata.PayloadLength() != len(payload) {
					t.Fatalf("payload length = %d, want %d", metadata.PayloadLength(), len(payload))
				}
				wantSHA := sha256.Sum256(payload)
				if metadata.PayloadSHA256() != wantSHA {
					t.Fatal("multipart payload SHA-256 mismatch")
				}
				if protocol, ok := metadata.LegacyPickleProtocol(); !ok || protocol != 2 {
					t.Fatalf("pickle protocol = (%d, %v), want (2, true)", protocol, ok)
				}
				payload[0] ^= 0xff
				if metadata.PayloadSHA256() != wantSHA {
					t.Fatal("multipart metadata retained mutable payload alias")
				}

			default:
				t.Fatalf("unhandled fixture ID %q", tc.ID)
			}
		})
	}

	for _, expected := range []string{
		"http_dlr_request", "smpps_dlr_request", "smpp_to_queue_id", "multipart_deliver_sm_part",
	} {
		if !seen[expected] {
			t.Fatalf("fixture %q was not executed", expected)
		}
	}
}

func TestKeyBuildersAndParser(t *testing.T) {
	tests := []struct {
		name    string
		build   func() (rediscompat.Key, error)
		kind    rediscompat.KeyKind
		value   string
		pattern string
	}{
		{
			"dlr", func() (rediscompat.Key, error) { return rediscompat.BuildDLRKey("queue-1") },
			rediscompat.KeyDLR, "dlr:queue-1", "dlr:<queue-message-id>",
		},
		{
			"correlation", func() (rediscompat.Key, error) { return rediscompat.BuildQueueMessageKey("A001") },
			rediscompat.KeyQueueMessage, "queue-msgid:A001", "queue-msgid:<normalized-smpp-message-id>",
		},
		{
			"multipart", func() (rediscompat.Key, error) {
				return rediscompat.BuildLegacyMultipartKey("connector-a", 42, "4040")
			},
			rediscompat.KeyLegacyMultipart, "longDeliverSm:connector-a:42:4040", "longDeliverSm:<connector-id>:<reference>:<destination>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			built, err := tc.build()
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if built.Kind() != tc.kind || built.String() != tc.value || built.Pattern() != tc.pattern {
				t.Fatalf("built = (%v, %q, %q)", built.Kind(), built.String(), built.Pattern())
			}
			parsed, err := rediscompat.ParseKey(tc.value)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if parsed != built {
				t.Fatalf("parsed = %#v, built = %#v", parsed, built)
			}
		})
	}
}

func TestKeyValidation(t *testing.T) {
	for _, value := range []string{
		"",
		"unknown:key",
		"dlr:",
		"dlr:a:b",
		"queue-msgid:",
		"queue-msgid:a:b",
		"longDeliverSm:connector:reference:4040",
		"longDeliverSm:connector:42:",
		"longDeliverSm:connector:42:4040:extra",
		strings.Repeat("x", rediscompat.MaxKeyBytes+1),
	} {
		t.Run(value, func(t *testing.T) {
			_, err := rediscompat.ParseKey(value)
			if !errors.Is(err, rediscompat.ErrInvalidKey) {
				t.Fatalf("error = %v, want ErrInvalidKey", err)
			}
		})
	}

	if _, err := rediscompat.BuildDLRKey("contains:colon"); !errors.Is(err, rediscompat.ErrInvalidKey) {
		t.Fatalf("BuildDLRKey error = %v", err)
	}
	if _, err := rediscompat.BuildLegacyMultipartKey("connector:bad", 1, "4040"); !errors.Is(err, rediscompat.ErrInvalidKey) {
		t.Fatalf("BuildLegacyMultipartKey error = %v", err)
	}
	maximumSubject := strings.Repeat("a", rediscompat.MaxKeyBytes-len("dlr:"))
	maximumKey, err := rediscompat.BuildDLRKey(maximumSubject)
	if err != nil || len(maximumKey.String()) != rediscompat.MaxKeyBytes {
		t.Fatalf("exact maximum key = (%d, %v)", len(maximumKey.String()), err)
	}
	if _, err := rediscompat.BuildDLRKey(maximumSubject + "a"); !errors.Is(err, rediscompat.ErrInvalidKey) {
		t.Fatalf("over-maximum key error = %v", err)
	}
}

func TestRecordValidation(t *testing.T) {
	dlrKey := mustDLRKey(t, "queue-1")
	queueKey, err := rediscompat.BuildQueueMessageKey("A001")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("wrong key kind", func(t *testing.T) {
		_, err := rediscompat.NewHTTPDLRRecord(queueKey, validHTTPDLRRequest())
		if !errors.Is(err, rediscompat.ErrInvalidRecord) {
			t.Fatalf("error = %v, want ErrInvalidRecord", err)
		}
	})

	t.Run("non-positive TTL", func(t *testing.T) {
		request := validHTTPDLRRequest()
		request.ExpirySeconds = 0
		_, err := rediscompat.NewHTTPDLRRecord(dlrKey, request)
		if !errors.Is(err, rediscompat.ErrInvalidTTL) {
			t.Fatalf("error = %v, want ErrInvalidTTL", err)
		}
	})

	t.Run("invalid level", func(t *testing.T) {
		request := validHTTPDLRRequest()
		request.Level = 4
		_, err := rediscompat.NewHTTPDLRRecord(dlrKey, request)
		if !errors.Is(err, rediscompat.ErrInvalidRecord) {
			t.Fatalf("error = %v, want ErrInvalidRecord", err)
		}
	})

	t.Run("invalid method", func(t *testing.T) {
		request := validHTTPDLRRequest()
		request.Method = "DELETE"
		_, err := rediscompat.NewHTTPDLRRecord(dlrKey, request)
		if !errors.Is(err, rediscompat.ErrInvalidRecord) {
			t.Fatalf("error = %v, want ErrInvalidRecord", err)
		}
	})

	t.Run("oversized typed fields", func(t *testing.T) {
		request := validHTTPDLRRequest()
		request.URL = strings.Repeat("x", rediscompat.MaxRecordFieldBytes+1)
		_, err := rediscompat.NewHTTPDLRRecord(dlrKey, request)
		if !errors.Is(err, rediscompat.ErrInvalidRecord) {
			t.Fatalf("error = %v, want ErrInvalidRecord", err)
		}
	})

	t.Run("missing destination address", func(t *testing.T) {
		// destination_addr is mandatory on submit_sm; source_addr is not (an
		// ESME may leave it empty and let the SMSC supply one), so only the
		// destination is required here.
		request := rediscompat.SMPPSDLRRequest{
			SystemID: "client-a", SourceAddrTON: "ton", SourceAddrNPI: "npi", SourceAddress: "1111",
			DestinationAddrTON: "ton", DestinationAddrNPI: "npi", DestinationAddress: "",
			SubmissionDate: "date", RegisteredDeliveryReceipt: "receipt", ExpirySeconds: 60,
		}
		_, err := rediscompat.NewSMPPSDLRRecord(dlrKey, request)
		if !errors.Is(err, rediscompat.ErrInvalidRecord) {
			t.Fatalf("error = %v, want ErrInvalidRecord", err)
		}
	})

	t.Run("alphanumeric sender id stays a string", func(t *testing.T) {
		// The frozen fixture only captured numeric addresses, so the record was
		// modelled as an integer. An alphanumeric sender id is equally legal and
		// must not be rejected or coerced.
		request := rediscompat.SMPPSDLRRequest{
			SystemID: "client-a", SourceAddrTON: "ton", SourceAddrNPI: "npi", SourceAddress: "ACME",
			DestinationAddrTON: "ton", DestinationAddrNPI: "npi", DestinationAddress: "447700900000",
			SubmissionDate: "date", RegisteredDeliveryReceipt: "receipt", ExpirySeconds: 60,
		}
		record, err := rediscompat.NewSMPPSDLRRecord(dlrKey, request)
		if err != nil {
			t.Fatalf("alphanumeric sender rejected: %v", err)
		}
		source, ok := record.Fields()["source_addr"].String()
		if !ok || source != "ACME" {
			t.Fatalf("source_addr = %v/%q, want string ACME", record.Fields()["source_addr"].Kind(), source)
		}
		if kind := record.Fields()["destination_addr"].Kind(); kind != rediscompat.FieldInteger {
			t.Fatalf("numeric destination_addr kind = %v, want integer (fixture parity)", kind)
		}
	})
}

func TestRecordFieldsAreDefensivelyCopied(t *testing.T) {
	record, err := rediscompat.NewHTTPDLRRecord(mustDLRKey(t, "queue-1"), validHTTPDLRRequest())
	if err != nil {
		t.Fatal(err)
	}
	fields := record.Fields()
	fields["url"] = rediscompat.StringField("mutated")
	delete(fields, "connector")

	fresh := record.Fields()
	assertStringField(t, fresh["url"], "https://example.invalid/dlr")
	assertStringField(t, fresh["connector"], "connector-a")
}

func TestLegacyMultipartInspectionValidation(t *testing.T) {
	key, err := rediscompat.BuildLegacyMultipartKey("connector-a", 42, "4040")
	if err != nil {
		t.Fatal(err)
	}
	dlrKey := mustDLRKey(t, "queue-1")

	for _, tc := range []struct {
		name     string
		key      rediscompat.Key
		sequence uint32
		ttl      int64
		payload  []byte
		want     error
	}{
		{"wrong key", dlrKey, 1, 300, []byte{0x80, 2}, rediscompat.ErrInvalidRecord},
		{"zero sequence", key, 0, 300, []byte{0x80, 2}, rediscompat.ErrInvalidRecord},
		{"zero TTL", key, 1, 0, []byte{0x80, 2}, rediscompat.ErrInvalidTTL},
		{"empty payload", key, 1, 300, nil, rediscompat.ErrInvalidRecord},
		{"oversized payload", key, 1, 300, make([]byte, rediscompat.MaxLegacyMultipartPayload+1), rediscompat.ErrPayloadTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rediscompat.InspectLegacyMultipartPart(tc.key, tc.sequence, tc.ttl, tc.payload)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	exactMaximum := make([]byte, rediscompat.MaxLegacyMultipartPayload)
	if _, err := rediscompat.InspectLegacyMultipartPart(key, 1, 300, exactMaximum); err != nil {
		t.Fatalf("exact maximum payload: %v", err)
	}
}

func TestLegacyPickleDetectionIsPassive(t *testing.T) {
	key, err := rediscompat.BuildLegacyMultipartKey("connector-a", 42, "4040")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		payload  []byte
		protocol uint8
		detected bool
	}{
		{[]byte{'x'}, 0, false},
		{[]byte{0x80}, 0, false},
		{[]byte{0x80, 2}, 2, true},
		{[]byte{0x80, 0xff}, 0xff, true},
	} {
		metadata, err := rediscompat.InspectLegacyMultipartPart(key, 1, 300, tc.payload)
		if err != nil {
			t.Fatal(err)
		}
		protocol, detected := metadata.LegacyPickleProtocol()
		if protocol != tc.protocol || detected != tc.detected {
			t.Fatalf("payload %x: got (%d, %v), want (%d, %v)", tc.payload, protocol, detected, tc.protocol, tc.detected)
		}
	}
}

func FuzzRedisBoundaryNeverPanics(f *testing.F) {
	for _, key := range []string{
		"", "dlr:queue-1", "queue-msgid:A001", "longDeliverSm:connector-a:42:4040", "unknown:key",
	} {
		f.Add(key, []byte{0x80, 2, 'x'})
	}
	multipartKey, err := rediscompat.BuildLegacyMultipartKey("connector-a", 42, "4040")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, key string, payload []byte) {
		_, _ = rediscompat.ParseKey(key)
		metadata, err := rediscompat.InspectLegacyMultipartPart(multipartKey, 1, 300, payload)
		if err == nil {
			_ = metadata.PayloadSHA256()
			_, _ = metadata.LegacyPickleProtocol()
		}
	})
}

func assertRecordMatchesGolden(t *testing.T, record rediscompat.HashRecord, tc goldenCase) {
	t.Helper()
	if record.RedisType() != tc.RedisType {
		t.Fatalf("Redis type = %q, want %q", record.RedisType(), tc.RedisType)
	}
	if record.Key().Pattern() != tc.KeyPattern {
		t.Fatalf("key pattern = %q, want %q", record.Key().Pattern(), tc.KeyPattern)
	}
	if record.TTLSeconds() != tc.TTLSeconds {
		t.Fatalf("TTL = %d, want %d", record.TTLSeconds(), tc.TTLSeconds)
	}
	actual := record.Fields()
	if len(actual) != len(tc.Fields) {
		t.Fatalf("field count = %d, want %d", len(actual), len(tc.Fields))
	}
	for name, expected := range tc.Fields {
		field, ok := actual[name]
		if !ok {
			t.Fatalf("missing field %q", name)
		}
		switch expected.Type {
		case "str":
			assertStringField(t, field, expected.Value)
		case "int":
			assertIntegerField(t, field, expected.Value)
		default:
			t.Fatalf("field %q unexpected fixture type %q", name, expected.Type)
		}
	}
}

func assertStringField(t *testing.T, field rediscompat.Field, want string) {
	t.Helper()
	if field.Kind() != rediscompat.FieldString {
		t.Fatalf("field kind = %v, want string", field.Kind())
	}
	got, ok := field.String()
	if !ok || got != want {
		t.Fatalf("string field = (%q, %v), want %q", got, ok, want)
	}
}

func assertIntegerField(t *testing.T, field rediscompat.Field, want string) {
	t.Helper()
	if field.Kind() != rediscompat.FieldInteger {
		t.Fatalf("field kind = %v, want integer", field.Kind())
	}
	expected, err := strconv.ParseInt(want, 10, 64)
	if err != nil {
		t.Fatalf("parse expected integer %q: %v", want, err)
	}
	got, ok := field.Integer()
	if !ok || got != expected {
		t.Fatalf("integer field = (%d, %v), want %d", got, ok, expected)
	}
}

func validHTTPDLRRequest() rediscompat.HTTPDLRRequest {
	return rediscompat.HTTPDLRRequest{
		URL:           "https://example.invalid/dlr",
		Level:         3,
		Method:        "POST",
		Connector:     "connector-a",
		ExpirySeconds: 86400,
	}
}

func mustDLRKey(t *testing.T, messageID string) rediscompat.Key {
	t.Helper()
	key, err := rediscompat.BuildDLRKey(messageID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func loadGolden(t *testing.T) goldenDocument {
	t.Helper()
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "redis", "baseline.json")
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

func TestFixtureMultipartBase64IsStable(t *testing.T) {
	for _, tc := range loadGolden(t).Cases {
		if tc.ID != "multipart_deliver_sm_part" {
			continue
		}
		payload, err := base64.StdEncoding.DecodeString(tc.Fields["1"].Base64)
		if err != nil {
			t.Fatal(err)
		}
		if len(payload) < 2 || !bytes.Equal(payload[:2], []byte{0x80, 2}) {
			t.Fatalf("fixture pickle prefix = %x", payload)
		}
		return
	}
	t.Fatal("multipart fixture not found")
}
