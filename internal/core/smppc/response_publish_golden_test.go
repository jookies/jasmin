package smppc

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type responsePublishFixture struct {
	Cases []struct {
		ID    string `json:"id"`
		Input struct {
			Enabled bool   `json:"enabled"`
			ReplyTo string `json:"reply_to"`
		} `json:"input"`
		Expected struct {
			Action      SubmitResponseAction `json:"action"`
			Publication *struct {
				Exchange       string `json:"exchange"`
				RoutingKey     string `json:"routing_key"`
				BodyBase64     string `json:"body_base64"`
				BodySHA256     string `json:"body_sha256"`
				PickleProtocol int    `json:"pickle_protocol"`
				Properties     struct {
					MessageID string            `json:"message-id"`
					Headers   map[string]string `json:"headers"`
				} `json:"properties"`
			} `json:"publication"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestLegacySubmitResponsePublicationGolden(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "smpp-client-response-publish", "baseline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture responsePublishFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 4 {
		t.Fatalf("response-publication fixture case count = %d, want 4", len(fixture.Cases))
	}
	seen := make(map[string]struct{}, len(fixture.Cases))
	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			if _, duplicate := seen[tc.ID]; duplicate {
				t.Fatalf("duplicate fixture ID %q", tc.ID)
			}
			seen[tc.ID] = struct{}{}

			var body []byte
			var messageID, createdAt string
			if tc.Expected.Publication != nil {
				messageID = tc.Expected.Publication.Properties.MessageID
				createdAt = tc.Expected.Publication.Properties.Headers["created_at"]
				body, err = base64.StdEncoding.DecodeString(tc.Expected.Publication.BodyBase64)
				if err != nil {
					t.Fatal(err)
				}
			}

			publication, err := NewSubmitResponsePublication(
				tc.Input.Enabled, tc.Expected.Action, tc.Input.ReplyTo, messageID, createdAt, body,
			)
			if err != nil {
				t.Fatal(err)
			}
			if tc.Expected.Publication == nil {
				if publication != nil {
					t.Fatal("publication present, want disabled")
				}
				return
			}
			if publication == nil {
				t.Fatal("publication absent, want enabled")
			}
			want := tc.Expected.Publication
			if SubmitResponseExchange != want.Exchange {
				t.Fatalf("exchange = %q, want %q", SubmitResponseExchange, want.Exchange)
			}
			if publication.RoutingKey() != want.RoutingKey || publication.RoutingKey() != tc.Input.ReplyTo {
				t.Fatalf("routing key = %q, want %q", publication.RoutingKey(), want.RoutingKey)
			}
			if publication.Route().Kind() != amqpcompat.RouteSubmitSMResponse {
				t.Fatalf("route kind = %v", publication.Route().Kind())
			}
			properties := publication.Properties()
			if properties.MessageID() != want.Properties.MessageID {
				t.Fatalf("message ID = %q, want %q", properties.MessageID(), want.Properties.MessageID)
			}
			actualHeaders := properties.Headers()
			if len(actualHeaders) != 1 {
				t.Fatalf("header count = %d, want 1", len(actualHeaders))
			}
			actualCreatedAt, ok := actualHeaders["created_at"].String()
			if !ok || actualCreatedAt != want.Properties.Headers["created_at"] {
				t.Fatalf("created_at = (%q, %v), want %q", actualCreatedAt, ok, want.Properties.Headers["created_at"])
			}
			if _, ok := properties.ReplyTo(); ok {
				t.Fatal("unexpected reply-to property")
			}
			if _, ok := properties.Priority(); ok {
				t.Fatal("unexpected priority property")
			}
			sha := publication.BodySHA256()
			if hex.EncodeToString(sha[:]) != want.BodySHA256 {
				t.Fatalf("body SHA-256 = %x, want %s", sha, want.BodySHA256)
			}
			protocol, ok := publication.LegacyPickleProtocol()
			if !ok || int(protocol) != want.PickleProtocol {
				t.Fatalf("pickle protocol = (%d, %v), want %d", protocol, ok, want.PickleProtocol)
			}
		})
	}
}
