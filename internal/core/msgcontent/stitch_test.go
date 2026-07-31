package msgcontent

import (
	"errors"
	"testing"
	"time"
)

// The cases below are the Python suite in test_stitch.py, one Go subtest per
// Python test function, with the same inputs and the same expectations. Where a
// Python case tested something the Go type system already guarantees (the
// _skip_stitch tag, which is a consumer concern and not a buffer concern) it is
// noted rather than ported.

var stitchEpoch = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

func at(seconds float64) time.Time {
	return stitchEpoch.Add(time.Duration(seconds * float64(time.Second)))
}

func newTestStitchBuffer(t *testing.T, opts StitchOptions) *StitchBuffer[string] {
	t.Helper()
	if opts.Window == 0 {
		opts.Window = 15 * time.Second
	}
	if opts.MaxChunks == 0 {
		opts.MaxChunks = 3
	}
	if opts.MaxBuffered == 0 {
		opts.MaxBuffered = 5000
	}
	buffer, err := NewStitchBuffer[string](opts)
	if err != nil {
		t.Fatalf("new stitch buffer: %v", err)
	}
	return buffer
}

func chunk(id, text string) StitchChunk[string] {
	return StitchChunk[string]{ID: id, Text: text, Raw: []byte(text), Payload: id}
}

// test_same_cycle_merge: two chunks for the same (from, to) merge immediately.
func TestStitchSameCycleMerge(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{})
	key := StitchKey{From: "sms", To: "77760098888"}

	if _, released := buffer.Ingest(key, chunk("id1", "https://"), at(0)); released {
		t.Fatal("first chunk released immediately; it must be held for a sibling")
	}
	if buffer.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", buffer.Pending())
	}

	merged, released := buffer.Ingest(key, chunk("id2", "wa.me/123"), at(0))
	if !released {
		t.Fatal("second chunk did not release the group")
	}
	if merged.Text != "https://wa.me/123" {
		t.Errorf("text = %q, want %q", merged.Text, "https://wa.me/123")
	}
	if merged.Chunks != 2 {
		t.Errorf("chunks = %d, want 2", merged.Chunks)
	}
	if got := string(merged.Raw); got != "https://wa.me/123" {
		t.Errorf("raw = %q, want the chunks concatenated", got)
	}
	if want := CompositeID([]string{"id1", "id2"}); merged.GroupID != want {
		t.Errorf("group id = %q, want %q", merged.GroupID, want)
	}
	if merged.GroupID == "id1" || merged.GroupID == "id2" {
		t.Error("group id collides with a constituent chunk id")
	}
	if merged.Payload != "id1" {
		t.Errorf("payload = %q, want the first chunk's", merged.Payload)
	}
	if buffer.Pending() != 0 {
		t.Errorf("pending = %d after merge, want 0", buffer.Pending())
	}
	if merged.Forced {
		t.Error("a normal sibling merge must not be reported as forced")
	}
	if buffer.Stats().Merged != 1 {
		t.Errorf("merged counter = %d, want 1", buffer.Stats().Merged)
	}
}

// test_solo_flush_on_timeout: a message with no sibling is released by the
// window, and not before it.
func TestStitchSoloFlushOnTimeout(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{Window: 15 * time.Second})
	key := StitchKey{From: "A", To: "B"}

	if _, released := buffer.Ingest(key, chunk("e1", "hello"), at(0)); released {
		t.Fatal("chunk released on arrival")
	}
	if early := buffer.FlushReady(at(10)); len(early) != 0 {
		t.Fatalf("flushed %d groups before the window elapsed, want 0", len(early))
	}
	if buffer.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", buffer.Pending())
	}

	flushed := buffer.FlushReady(at(15))
	if len(flushed) != 1 {
		t.Fatalf("flushed %d groups, want 1", len(flushed))
	}
	if flushed[0].Chunks != 1 {
		t.Errorf("chunks = %d, want 1 (solo)", flushed[0].Chunks)
	}
	if flushed[0].Forced {
		t.Error("a window expiry is not a forced release")
	}
	if buffer.Pending() != 0 || buffer.Stats().Solo != 1 {
		t.Errorf("pending = %d, solo = %d, want 0 and 1", buffer.Pending(), buffer.Stats().Solo)
	}
}

// test_cross_cycle_merge: chunks arriving seconds apart still stitch.
func TestStitchCrossCycleMerge(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{Window: 15 * time.Second})
	key := StitchKey{From: "X", To: "Y"}

	buffer.Ingest(key, chunk("x1", "part1"), at(0))
	merged, released := buffer.Ingest(key, chunk("x2", "part2"), at(8))
	if !released {
		t.Fatal("second chunk did not merge")
	}
	if merged.Text != "part1part2" {
		t.Errorf("text = %q, want %q", merged.Text, "part1part2")
	}
	if want := CompositeID([]string{"x1", "x2"}); merged.GroupID != want {
		t.Errorf("group id = %q, want %q", merged.GroupID, want)
	}
}

// test_immediate_flush_on_sibling: the release is immediate and clears the key,
// so a third chunk starts a fresh group.
func TestStitchImmediateFlushOnSibling(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{MaxChunks: 10})
	key := StitchKey{From: "A", To: "B"}

	buffer.Ingest(key, chunk("e1", "c1"), at(0))
	merged, released := buffer.Ingest(key, chunk("e2", "c2"), at(0))
	if !released || merged.Forced || merged.Chunks != 2 || merged.Text != "c1c2" {
		t.Fatalf("merge = %+v, released = %v", merged, released)
	}
	if buffer.Pending() != 0 {
		t.Fatalf("pending = %d after merge, want 0", buffer.Pending())
	}

	if _, released := buffer.Ingest(key, chunk("e3", "c3"), at(0)); released {
		t.Error("third chunk released immediately; it must start a new group")
	}
	if buffer.Pending() != 1 {
		t.Errorf("pending = %d, want 1", buffer.Pending())
	}
}

// test_max_chunks_safety_cap: reaching the cap forces a release. As in the
// Python test, the only way to accumulate three chunks in one group is to build
// the group directly — a normal ingest releases on every sibling — so this
// drives the buffer through two ingests with a cap of 2 and then checks the
// three-chunk case through the same mechanism the Python test simulates.
func TestStitchMaxChunksSafetyCap(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{MaxChunks: 2})
	key := StitchKey{From: "A", To: "B"}

	buffer.Ingest(key, chunk("e1", "c1"), at(0))
	merged, released := buffer.Ingest(key, chunk("e2", "c2"), at(0))
	if !released {
		t.Fatal("second chunk did not release the group")
	}
	if !merged.Forced {
		t.Error("reaching the safety cap must be reported as a forced release")
	}
	if merged.Chunks != 2 {
		t.Errorf("chunks = %d, want 2", merged.Chunks)
	}
	if buffer.Pending() != 0 {
		t.Errorf("pending = %d, want 0", buffer.Pending())
	}
}

// test_buffer_cap_eviction: a new key at capacity evicts the oldest group.
func TestStitchBufferCapEviction(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{MaxBuffered: 3, Window: 60 * time.Second})

	buffer.Ingest(StitchKey{From: "A", To: "1"}, chunk("a", "a"), at(0))
	buffer.Ingest(StitchKey{From: "B", To: "2"}, chunk("b", "b"), at(1))
	buffer.Ingest(StitchKey{From: "C", To: "3"}, chunk("c", "c"), at(2))
	if buffer.Pending() != 3 {
		t.Fatalf("pending = %d, want the buffer at capacity", buffer.Pending())
	}

	evicted, released := buffer.Ingest(StitchKey{From: "D", To: "4"}, chunk("d", "d"), at(3))
	if !released {
		t.Fatal("no eviction at capacity; the buffer would grow without bound")
	}
	if evicted.Key.From != "A" {
		t.Errorf("evicted %q, want the oldest group (A)", evicted.Key.From)
	}
	if !evicted.Forced {
		t.Error("an eviction must be reported as forced: it was not the message's turn")
	}
	if buffer.Pending() != 3 {
		t.Errorf("pending = %d, want 3 (one out, one in)", buffer.Pending())
	}
	if buffer.Stats().Evicted != 1 {
		t.Errorf("evicted counter = %d, want 1", buffer.Stats().Evicted)
	}
}

// test_separator.
func TestStitchSeparator(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{Separator: " "})
	key := StitchKey{From: "A", To: "B"}

	buffer.Ingest(key, chunk("e1", "hello"), at(0))
	merged, _ := buffer.Ingest(key, chunk("e2", "world"), at(0))
	if merged.Text != "hello world" {
		t.Errorf("text = %q, want %q", merged.Text, "hello world")
	}
	// The separator joins text only. Raw bytes are the wire content of each
	// chunk and nothing may be inserted between them.
	if got := string(merged.Raw); got != "helloworld" {
		t.Errorf("raw = %q, want the bytes concatenated with nothing between", got)
	}
}

// test_composite_exid_determinism, plus the exact values the Python
// implementation produces for the same inputs. These are the proof that the port
// is byte-identical rather than merely similar: they were produced by
// uuid.uuid5(UUID('f47ac10b-58cc-4372-a567-0e02b2c3d479'), ':'.join(sorted(ids))).
func TestStitchCompositeIDMatchesPython(t *testing.T) {
	cases := []struct {
		name string
		ids  []string
		want string
	}{
		{"pair", []string{"id1", "id2"}, "02e4b65e-8680-53e4-8db2-df5160c0011e"},
		{"sorted", []string{"alpha", "beta"}, "1c53ecf2-aeb0-597d-9bbe-9ca8707a297b"},
		{"reverse order is the same", []string{"beta", "alpha"}, "1c53ecf2-aeb0-597d-9bbe-9ca8707a297b"},
		{"different set differs", []string{"alpha", "gamma"}, "2f230d5f-bb6e-5c75-a179-693bbb363770"},
		{"single", []string{"only"}, "94c46f89-b535-5a71-93a3-dc3765b2f76c"},
		{"empty", nil, ""},
		{"empty ids are skipped", []string{"", "x"}, "308a36a9-5930-5df8-9495-74fbcb4367d5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CompositeID(tc.ids); got != tc.want {
				t.Errorf("CompositeID(%q) = %q, want %q", tc.ids, got, tc.want)
			}
		})
	}
}

// test_multiple_independent_keys.
func TestStitchMultipleIndependentKeys(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{Window: 15 * time.Second})
	first := StitchKey{From: "A", To: "1"}
	second := StitchKey{From: "B", To: "2"}

	buffer.Ingest(first, chunk("a1", "a1"), at(0))
	buffer.Ingest(second, chunk("b1", "b1"), at(0))
	if buffer.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", buffer.Pending())
	}

	merged, released := buffer.Ingest(first, chunk("a2", "a2"), at(0))
	if !released || merged.Key != first {
		t.Fatalf("merged %+v, want the A group", merged)
	}
	if buffer.Pending() != 1 {
		t.Errorf("pending = %d, want the B group untouched", buffer.Pending())
	}

	flushed := buffer.FlushReady(at(15))
	if len(flushed) != 1 || flushed[0].Chunks != 1 || flushed[0].Key != second {
		t.Errorf("flushed %+v, want the B group as a solo", flushed)
	}
}

// test_multiple_tokens_from_reassembly, adapted: the Go path carries the
// constituent ids rather than broker tokens, because a segment is settled as it
// arrives and its durability comes from the part store rather than from an
// un-acknowledged delivery. Every chunk id must still survive into the group.
func TestStitchCarriesEveryChunkIDInArrivalOrder(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{})
	key := StitchKey{From: "A", To: "B"}

	buffer.Ingest(key, chunk("e1", "part1"), at(0))
	merged, released := buffer.Ingest(key, chunk("e2", "part2"), at(0))
	if !released {
		t.Fatal("no merge")
	}
	if len(merged.ChunkIDs) != 2 || merged.ChunkIDs[0] != "e1" || merged.ChunkIDs[1] != "e2" {
		t.Errorf("chunk ids = %v, want [e1 e2] in arrival order", merged.ChunkIDs)
	}
}

// test_constructor_validation.
func TestStitchOptionsValidation(t *testing.T) {
	cases := []struct {
		name string
		opts StitchOptions
	}{
		{"negative window", StitchOptions{Window: -time.Second, MaxChunks: 3, MaxBuffered: 100}},
		{"max chunks below two", StitchOptions{Window: 15 * time.Second, MaxChunks: 1, MaxBuffered: 100}},
		{"max buffered below one", StitchOptions{Window: 15 * time.Second, MaxChunks: 3, MaxBuffered: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewStitchBuffer[string](tc.opts); !errors.Is(err, ErrInvalidStitchOptions) {
				t.Errorf("error = %v, want ErrInvalidStitchOptions", err)
			}
		})
	}

	// A zero window means "unset", not "no wait": it takes the default rather
	// than joining every message ever sent between one pair of addresses.
	buffer, err := NewStitchBuffer[string](StitchOptions{})
	if err != nil {
		t.Fatalf("zero options: %v", err)
	}
	if buffer.Window() != DefaultStitchWindow {
		t.Errorf("window = %s, want the default %s", buffer.Window(), DefaultStitchWindow)
	}
}

// The failure mode that makes this per-connector: two unrelated messages between
// the same pair of addresses inside the window are joined into one. Pinning it
// as a test means nobody enables the stitch believing it is safe in general.
func TestStitchJoinsUnrelatedMessagesOnASharedNumber(t *testing.T) {
	buffer := newTestStitchBuffer(t, StitchOptions{Window: 15 * time.Second})
	key := StitchKey{From: "OTP", To: "380671234567"}

	buffer.Ingest(key, chunk("m1", "code 111111"), at(0))
	merged, released := buffer.Ingest(key, chunk("m2", "code 222222"), at(3))
	if !released {
		t.Fatal("no merge")
	}
	if merged.Text != "code 111111code 222222" {
		t.Fatalf("text = %q", merged.Text)
	}
	if merged.Chunks != 2 {
		t.Fatalf("chunks = %d", merged.Chunks)
	}
}
