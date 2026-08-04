package dlrgate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/pumpitspace/synevyr/internal/core/termination"
)

func newTestRegistry(t *testing.T) (*Registry, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	registry, err := NewRegistry(client, "")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry, server, client
}

func TestRegistryAddListRemove(t *testing.T) {
	registry, _, _ := newTestRegistry(t)
	ctx := context.Background()

	entry, err := registry.Add(ctx, "+380930242105", AddOptions{TTL: 10 * time.Minute, AddedBy: "operator", Note: "otp flow"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if entry.MSISDN != "380930242105" {
		t.Fatalf("MSISDN = %q, want the normalized digits", entry.MSISDN)
	}

	// The '+' form and the bare form are the same entry.
	_, present, err := registry.Window(ctx, "380930242105")
	if err != nil || !present {
		t.Fatalf("Window present = %v, %v; want true, nil", present, err)
	}

	entries, err := registry.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if entries[0].AddedBy != "operator" || entries[0].Note != "otp flow" {
		t.Fatalf("provenance lost: %+v", entries[0])
	}

	if err := registry.Remove(ctx, "380930242105"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	entries, err = registry.List(ctx, 0)
	if err != nil {
		t.Fatalf("List after remove: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("List returned %d entries after remove, want 0", len(entries))
	}
	// Removing what is already gone is the caller's stated intent, not an error.
	if err := registry.Remove(ctx, "380930242105"); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func TestRegistryClampsTTLToMax(t *testing.T) {
	registry, server, _ := newTestRegistry(t)
	ctx := context.Background()

	entry, err := registry.Add(ctx, "380930242105", AddOptions{TTL: time.Hour, AddedBy: "operator"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := entry.ExpiresAt.Sub(entry.AddedAt); got != MaxTTL {
		t.Fatalf("window = %s, want it clamped to %s", got, MaxTTL)
	}
	if got := server.TTL("dlr:block:380930242105"); got != MaxTTL {
		t.Fatalf("redis TTL = %s, want %s", got, MaxTTL)
	}
}

func TestRegistryListDropsExpiredEntries(t *testing.T) {
	registry, server, _ := newTestRegistry(t)
	ctx := context.Background()

	if _, err := registry.Add(ctx, "380930242105", AddOptions{TTL: time.Minute, AddedBy: "operator"}); err != nil {
		t.Fatalf("Add short: %v", err)
	}
	if _, err := registry.Add(ctx, "380930242106", AddOptions{TTL: 10 * time.Minute, AddedBy: "operator"}); err != nil {
		t.Fatalf("Add long: %v", err)
	}

	// Redis expires the value key; the index member has to be pruned by score,
	// which is what this asserts.
	server.FastForward(2 * time.Minute)
	registry.now = func() time.Time { return time.Now().Add(2 * time.Minute) }

	entries, err := registry.List(ctx, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].MSISDN != "380930242106" {
		t.Fatalf("List = %+v, want only the unexpired number", entries)
	}
	indexed, err := server.ZMembers("dlr:block:index")
	if err != nil {
		t.Fatalf("ZMembers: %v", err)
	}
	if len(indexed) != 1 {
		t.Fatalf("index still holds %v, want the expired member pruned", indexed)
	}
}

func TestRegistryRejectsNonNumericDestination(t *testing.T) {
	registry, _, _ := newTestRegistry(t)
	ctx := context.Background()

	if _, err := registry.Add(ctx, "undefined", AddOptions{TTL: time.Minute, AddedBy: "operator"}); !errors.Is(err, ErrInvalidMSISDN) {
		t.Fatalf("Add(%q) error = %v, want ErrInvalidMSISDN", "undefined", err)
	}
}

func TestRegistryGetReportsMissingAsNotFound(t *testing.T) {
	registry, _, _ := newTestRegistry(t)
	if _, err := registry.Get(context.Background(), "380930242105"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get error = %v, want ErrNotFound", err)
	}
}

// TestRegistryEntryIsReadableByTerminationGate is the compatibility assertion
// that justifies the whole storage choice: the entries this package writes are
// the activation window the termination plane already reads. If the value were
// stored as a hash, GET would return WRONGTYPE and that gate would fail open —
// silently delivering everything — so this must keep passing.
func TestRegistryEntryIsReadableByTerminationGate(t *testing.T) {
	registry, _, client := newTestRegistry(t)
	ctx := context.Background()

	if _, err := registry.Add(ctx, "380930242105", AddOptions{TTL: time.Minute, AddedBy: "operator", Note: "note"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	source, err := termination.NewVerdictSource(
		termination.VerdictConfig{Source: termination.SourceRedisWindow},
		termination.Dependencies{Redis: client},
	)
	if err != nil {
		t.Fatalf("NewVerdictSource: %v", err)
	}

	registered, err := source.Decide(ctx, termination.Message{To: "380930242105"})
	if err != nil {
		t.Fatalf("Decide(registered): %v", err)
	}
	if !registered.Accept || registered.Stat != termination.StatDelivered {
		t.Fatalf("registered number verdict = %+v, want an accept", registered)
	}

	absent, err := source.Decide(ctx, termination.Message{To: "380930242199"})
	if err != nil {
		t.Fatalf("Decide(absent): %v", err)
	}
	if absent.Accept || absent.Stat != termination.StatRejected {
		t.Fatalf("unregistered number verdict = %+v, want a reject", absent)
	}
}
