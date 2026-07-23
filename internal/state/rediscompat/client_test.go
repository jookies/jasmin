package rediscompat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestClient spins up an in-memory Redis (miniredis) and returns a Client plus the
// server handle (for TTL inspection and time travel). Fully hermetic — no real Redis.
func newTestClient(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewClient(rdb), mr
}

func TestWriteReadHTTPDLR(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()

	key, err := BuildDLRKey("07033084-5cfd-4812-90a4-e4d24ffb6e3d")
	if err != nil {
		t.Fatalf("build key: %v", err)
	}
	rec, err := NewHTTPDLRRecord(key, HTTPDLRRequest{
		URL: "http://cb.example/dlr", Level: 3, Method: "POST", Connector: "smpp-01", ExpirySeconds: 86400,
	})
	if err != nil {
		t.Fatalf("build record: %v", err)
	}
	if err := c.WriteHashRecord(ctx, rec); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := c.ReadHash(ctx, key)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Integer fields must be stored as decimal strings (txredisapi parity, Q-006).
	for name, want := range map[string]string{
		"sc": "httpapi", "url": "http://cb.example/dlr", "level": "3",
		"method": "POST", "connector": "smpp-01", "expiry": "86400",
	} {
		if got[name] != want {
			t.Errorf("field %q = %q, want %q", name, got[name], want)
		}
	}

	// The TTL must be set atomically with the fields (not left immortal).
	ttl := mr.TTL(key.String())
	if ttl != 86400*time.Second {
		t.Errorf("ttl = %v, want 24h", ttl)
	}
}

func TestWriteReadQueueCorrelation(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	key, err := BuildQueueMessageKey("ABC123") // already-normalized SMSC id (Q-006)
	if err != nil {
		t.Fatalf("build key: %v", err)
	}
	rec, err := NewQueueMessageCorrelation(key, QueueMessageCorrelation{
		MessageID: "queue-uuid-1", ConnectorType: "httpapi", TTLSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("build correlation: %v", err)
	}
	if err := c.WriteHashRecord(ctx, rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := c.ReadHash(ctx, key)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["msgid"] != "queue-uuid-1" || got["connector_type"] != "httpapi" {
		t.Errorf("correlation fields = %v", got)
	}
}

func TestReadMissingKeyReturnsNotFound(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	key, _ := BuildDLRKey("does-not-exist")
	if _, err := c.ReadHash(ctx, key); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("missing key: got %v, want ErrKeyNotFound", err)
	}
}

func TestExpiredKeyReturnsNotFound(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()
	key, _ := BuildQueueMessageKey("expires-soon")
	rec, err := NewQueueMessageCorrelation(key, QueueMessageCorrelation{MessageID: "m", ConnectorType: "httpapi", TTLSeconds: 10})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := c.WriteHashRecord(ctx, rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	mr.FastForward(11 * time.Second) // travel past the TTL (RD-004)
	if _, err := c.ReadHash(ctx, key); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("expired key: got %v, want ErrKeyNotFound", err)
	}
}

func TestDeleteThenReadNotFoundAndIdempotent(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	key, _ := BuildQueueMessageKey("todelete")
	rec, _ := NewQueueMessageCorrelation(key, QueueMessageCorrelation{MessageID: "m", ConnectorType: "smppsapi", TTLSeconds: 60})
	if err := c.WriteHashRecord(ctx, rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := c.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.ReadHash(ctx, key); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("after delete: got %v, want ErrKeyNotFound", err)
	}
	// Deleting a missing key must be a no-op (idempotent redelivery, RD-005).
	if err := c.Delete(ctx, key); err != nil {
		t.Errorf("idempotent delete: %v", err)
	}
}

func TestReadHashRejectsMultipartKey(t *testing.T) {
	c, _ := newTestClient(t)
	key, _ := BuildLegacyMultipartKey("conn-1", 42, "447700900000")
	if _, err := c.ReadHash(context.Background(), key); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("multipart key via ReadHash: got %v, want ErrInvalidKey", err)
	}
}

func TestLegacyMultipartRoundTripOpaque(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()
	key, err := BuildLegacyMultipartKey("conn-1", 42, "447700900000")
	if err != nil {
		t.Fatalf("build key: %v", err)
	}
	// Payloads are opaque pickle bytes (protocol-2 header 0x80 0x02); Go must not decode.
	part1 := []byte{0x80, 0x02, 'p', 'a', 'r', 't', '1', 0x2e}
	part2 := []byte{0x80, 0x02, 'p', 'a', 'r', 't', '2', 0x00, 0xff}
	if err := c.WriteLegacyMultipartPart(ctx, key, 1, part1); err != nil {
		t.Fatalf("write part 1: %v", err)
	}
	if err := c.WriteLegacyMultipartPart(ctx, key, 2, part2); err != nil {
		t.Fatalf("write part 2: %v", err)
	}

	parts, err := c.ReadLegacyMultipartParts(ctx, key)
	if err != nil {
		t.Fatalf("read parts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(parts))
	}
	if string(parts[1]) != string(part1) || string(parts[2]) != string(part2) {
		t.Errorf("payload bytes not preserved opaquely: %v", parts)
	}
	// Fixed 300s TTL (Q-007).
	if ttl := mr.TTL(key.String()); ttl != LegacyMultipartTTLSeconds*time.Second {
		t.Errorf("multipart ttl = %v, want 300s", ttl)
	}
}

func TestMultipartWriteValidation(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	key, _ := BuildLegacyMultipartKey("conn-1", 1, "dest")
	if err := c.WriteLegacyMultipartPart(ctx, key, 0, []byte{1}); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("zero sequence: got %v, want ErrInvalidRecord", err)
	}
	if err := c.WriteLegacyMultipartPart(ctx, key, 1, nil); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("empty payload: got %v, want ErrInvalidRecord", err)
	}
	dlrKey, _ := BuildDLRKey("x")
	if err := c.WriteLegacyMultipartPart(ctx, dlrKey, 1, []byte{1}); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("wrong key kind: got %v, want ErrInvalidRecord", err)
	}
}
