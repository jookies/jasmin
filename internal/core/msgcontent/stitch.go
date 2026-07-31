package msgcontent

import (
	// SHA-1 is not a security choice here: RFC 4122 defines a version-5 UUID as
	// SHA-1 over namespace||name, and the composite id must match the Python
	// service's uuid.uuid5 byte for byte.
	"crypto/sha1"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The plain-split stitch, ported from the Python queue tap's app/stitch.py.
//
// Some connectors split one logical SMS into ordinary messages with no UDH and
// no SAR parameters — nothing on the wire says they belong together. The only
// available signal is that they share a sender and a destination and arrive
// within a few seconds of each other, so this buffer joins messages on that
// heuristic.
//
// # Why this is opt-in and off by default
//
// The heuristic is wrong whenever two genuinely separate messages travel the
// same (from, to) pair inside the window, which is exactly what happens when
// several partners send through shared brand names or short codes: two OTPs
// become one message with both codes in it, and the second one is never
// reported. Concatenation carried in a UDH or in SAR parameters has no such
// ambiguity, which is why that path is always on and this one is a per-connector
// decision made against a known partner's behaviour.
//
// # What is joined
//
// Chunks are joined as decoded text, not as bytes. Each chunk here is a whole,
// independently decodable message — unlike a UDH segment, whose bytes are a
// fragment of one encoded body and must be concatenated before decoding or a
// UCS-2 character straddling the boundary is destroyed. Raw bytes are carried
// alongside and concatenated too, so the spooled row still shows exactly what
// arrived, but they are not re-decoded.

// StitchNamespace is the UUID namespace the composite id is derived in. It is
// the arbitrary-but-fixed value from stitch.py, kept so a message stitched by
// either implementation carries the same id.
var StitchNamespace = [16]byte{
	0xf4, 0x7a, 0xc1, 0x0b, 0x58, 0xcc, 0x43, 0x72,
	0xa5, 0x67, 0x0e, 0x02, 0xb2, 0xc3, 0xd4, 0x79,
}

// Stitch defaults, matching the Python service's configuration defaults
// (config.py: STITCH_TIMEOUT 15, STITCH_MAX_CHUNKS 3, STITCH_MAX_BUFFER_SIZE
// 5000, STITCH_SEPARATOR "").
const (
	DefaultStitchWindow      = 15 * time.Second
	DefaultStitchMaxChunks   = 3
	DefaultStitchMaxBuffered = 5000
)

// ErrInvalidStitchOptions reports a stitch configuration that cannot be applied.
var ErrInvalidStitchOptions = errors.New("msgcontent: invalid stitch options")

// StitchOptions tunes the buffer. The zero value is not usable: Window must be
// positive, because a zero window would join every message ever sent between a
// pair of addresses.
type StitchOptions struct {
	// Window is how long a chunk waits for a sibling.
	Window time.Duration
	// MaxChunks caps how many chunks one group may accumulate before it is
	// force-flushed. It is a safety valve against unbounded growth on a reused
	// key, not a tuning knob; it must be at least 2.
	MaxChunks int
	// MaxBuffered caps concurrent groups. Reaching it evicts the single oldest
	// group rather than flushing a percentage, so memory pressure stays
	// predictable and no burst of downstream writes is created.
	MaxBuffered int
	// Separator is inserted between chunk texts. Empty is the default: a split
	// URL must not gain a space in the middle.
	Separator string
}

// WithDefaults fills unset values.
func (o StitchOptions) WithDefaults() StitchOptions {
	if o.Window == 0 {
		o.Window = DefaultStitchWindow
	}
	if o.MaxChunks == 0 {
		o.MaxChunks = DefaultStitchMaxChunks
	}
	if o.MaxBuffered == 0 {
		o.MaxBuffered = DefaultStitchMaxBuffered
	}
	return o
}

// Validate reports whether the options can be applied. The bounds are the
// Python constructor's, which raises on each of them.
func (o StitchOptions) Validate() error {
	if o.Window <= 0 {
		return fmt.Errorf("%w: window must be positive, got %s", ErrInvalidStitchOptions, o.Window)
	}
	if o.MaxChunks < 2 {
		return fmt.Errorf("%w: max chunks must be >= 2, got %d", ErrInvalidStitchOptions, o.MaxChunks)
	}
	if o.MaxBuffered < 1 {
		return fmt.Errorf("%w: max buffered must be >= 1, got %d", ErrInvalidStitchOptions, o.MaxBuffered)
	}
	return nil
}

// StitchKey groups chunks that might be one message.
//
// It is (from, to) and nothing else, which is the whole risk: two unrelated
// messages between the same pair inside the window share this key.
type StitchKey struct {
	From string
	To   string
}

// StitchChunk is one message offered to the buffer.
type StitchChunk[T any] struct {
	// ID identifies this chunk. It feeds the composite id and is reported back
	// in arrival order, so a caller that needs the first or the last chunk's
	// identity can take it from there.
	ID string
	// Text is the decoded message.
	Text string
	// Raw is the pre-decode payload.
	Raw []byte
	// Payload is whatever the caller needs carried through. The FIRST chunk's
	// payload is the one returned, matching the Python buffer, which copies the
	// first fragment's dict and only replaces its text and id.
	Payload T
}

// Stitched is a released group: either two or more joined chunks, or a single
// chunk whose window expired with no sibling.
type Stitched[T any] struct {
	Key StitchKey
	// Payload is the first chunk's payload.
	Payload T
	// GroupID is the deterministic composite id over every constituent chunk id,
	// order-independent so a redelivery in a different order produces the same
	// value. It is what the Python service uses as the row's external id.
	GroupID string
	// ChunkIDs are the chunk ids in arrival order.
	ChunkIDs []string
	// Text is the joined text and Raw the concatenated pre-decode bytes.
	Text string
	Raw  []byte
	// Chunks is how many chunks were joined. 1 means no stitching happened.
	Chunks int
	// Forced reports a release caused by the safety cap or by an eviction rather
	// than by a sibling arriving or the window expiring.
	Forced bool
}

// StitchStats are lifetime counters, for metrics.
type StitchStats struct {
	Merged  int
	Solo    int
	Evicted int
}

type stitchEntry[T any] struct {
	payload   T
	text      string
	raw       []byte
	chunkIDs  []string
	firstSeen time.Time
	chunks    int
}

// StitchBuffer holds messages that arrived with no concatenation header, in case
// the next one is the rest of the same message.
//
// It is pure logic and holds no I/O. It is NOT safe for concurrent use: the
// caller serializes access, because the caller also owns the ordering between an
// arriving message and a timer-driven flush, and a mutex here would hide that
// ordering without making it correct.
type StitchBuffer[T any] struct {
	opts    StitchOptions
	entries map[StitchKey]*stitchEntry[T]
	stats   StitchStats
}

// NewStitchBuffer validates the options and builds the buffer.
func NewStitchBuffer[T any](opts StitchOptions) (*StitchBuffer[T], error) {
	opts = opts.WithDefaults()
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	return &StitchBuffer[T]{opts: opts, entries: map[StitchKey]*stitchEntry[T]{}}, nil
}

// Pending is how many groups are currently held.
func (b *StitchBuffer[T]) Pending() int { return len(b.entries) }

// Stats returns the lifetime counters.
func (b *StitchBuffer[T]) Stats() StitchStats { return b.stats }

// Window is the configured wait, so a caller can size its flush interval from
// the buffer rather than from a second copy of the setting.
func (b *StitchBuffer[T]) Window() time.Duration { return b.opts.Window }

// Ingest offers one chunk.
//
// It returns a released group in three cases, matching the Python buffer: a
// sibling completed a group, a group hit the safety cap, or a new key evicted
// the oldest held group. Otherwise the chunk is held and nothing is returned —
// the caller must produce nothing for it now, and will see it again from
// FlushReady when the window expires.
func (b *StitchBuffer[T]) Ingest(key StitchKey, chunk StitchChunk[T], now time.Time) (Stitched[T], bool) {
	entry, held := b.entries[key]
	if !held {
		// A new key may have to make room first. The evicted group is returned
		// so it is released rather than dropped; nothing else can be returned on
		// this path, because the arriving chunk is the group's first.
		evicted, didEvict := b.evictIfFull()
		b.entries[key] = &stitchEntry[T]{
			payload:   chunk.Payload,
			text:      chunk.Text,
			raw:       append([]byte(nil), chunk.Raw...),
			chunkIDs:  []string{chunk.ID},
			firstSeen: now,
			chunks:    1,
		}
		return evicted, didEvict
	}

	entry.chunkIDs = append(entry.chunkIDs, chunk.ID)
	entry.chunks++
	if entry.text == "" {
		entry.text = chunk.Text
	} else {
		entry.text += b.opts.Separator + chunk.Text
	}
	entry.raw = append(entry.raw, chunk.Raw...)

	// Released on every sibling match — the binary split is the case this exists
	// for, and holding the pair back for the rest of the window would delay it
	// for nothing. MaxChunks only matters for a group that accumulated more than
	// one sibling without being released, which takes a redelivery or a reused
	// key to reach.
	return b.pop(key, entry, entry.chunks >= b.opts.MaxChunks), true
}

// FlushReady releases every group whose window has elapsed. It must be called on
// a timer: a chunk with no sibling is only ever released from here, so a caller
// that never calls it holds solo messages until the process exits.
func (b *StitchBuffer[T]) FlushReady(now time.Time) []Stitched[T] {
	var expired []StitchKey
	for key, entry := range b.entries {
		if now.Sub(entry.firstSeen) >= b.opts.Window {
			expired = append(expired, key)
		}
	}
	// Map iteration order is random; releasing in a stable order keeps a flush
	// reproducible in tests and in logs.
	sort.Slice(expired, func(i, j int) bool {
		left, right := b.entries[expired[i]], b.entries[expired[j]]
		if !left.firstSeen.Equal(right.firstSeen) {
			return left.firstSeen.Before(right.firstSeen)
		}
		if expired[i].From != expired[j].From {
			return expired[i].From < expired[j].From
		}
		return expired[i].To < expired[j].To
	})
	released := make([]Stitched[T], 0, len(expired))
	for _, key := range expired {
		released = append(released, b.pop(key, b.entries[key], false))
	}
	return released
}

// PendingKeys reports the groups currently held, oldest first. It exists for
// shutdown logging: the Python service deliberately does not flush on exit, and
// knowing how many messages are still buffered is the operator's only signal.
func (b *StitchBuffer[T]) PendingKeys() []StitchKey {
	keys := make([]StitchKey, 0, len(b.entries))
	for key := range b.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := b.entries[keys[i]], b.entries[keys[j]]
		if !left.firstSeen.Equal(right.firstSeen) {
			return left.firstSeen.Before(right.firstSeen)
		}
		if keys[i].From != keys[j].From {
			return keys[i].From < keys[j].From
		}
		return keys[i].To < keys[j].To
	})
	return keys
}

func (b *StitchBuffer[T]) pop(key StitchKey, entry *stitchEntry[T], forced bool) Stitched[T] {
	delete(b.entries, key)
	if entry.chunks > 1 {
		b.stats.Merged++
	} else {
		b.stats.Solo++
	}
	return Stitched[T]{
		Key:      key,
		Payload:  entry.payload,
		GroupID:  CompositeID(entry.chunkIDs),
		ChunkIDs: entry.chunkIDs,
		Text:     entry.text,
		Raw:      entry.raw,
		Chunks:   entry.chunks,
		Forced:   forced,
	}
}

// evictIfFull releases the single oldest group when the buffer is at capacity.
func (b *StitchBuffer[T]) evictIfFull() (Stitched[T], bool) {
	if len(b.entries) < b.opts.MaxBuffered {
		return Stitched[T]{}, false
	}
	oldest := b.PendingKeys()[0]
	b.stats.Evicted++
	return b.pop(oldest, b.entries[oldest], true), true
}

// CompositeID is a deterministic UUIDv5 over the sorted constituent chunk ids.
//
// Sorted, so the same set of chunks produces the same id no matter which order
// they arrived in: a crash that redelivers the pair in the other order must not
// produce a second, different message. Empty ids are skipped, and an empty set
// yields an empty string rather than the UUID of the empty name — the caller
// then has nothing to dedupe on and must say so, instead of every empty group
// colliding on one id.
func CompositeID(chunkIDs []string) string {
	present := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if id != "" {
			present = append(present, id)
		}
	}
	if len(present) == 0 {
		return ""
	}
	sort.Strings(present)
	return uuidV5(StitchNamespace, strings.Join(present, ":"))
}

// uuidV5 is RFC 4122 §4.3: SHA-1 over the namespace bytes followed by the name,
// truncated to 16 bytes, with the version and variant bits overwritten.
func uuidV5(namespace [16]byte, name string) string {
	buffer := make([]byte, 0, len(namespace)+len(name))
	buffer = append(buffer, namespace[:]...)
	buffer = append(buffer, name...)
	sum := sha1.Sum(buffer)
	var id [16]byte
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x50 // version 5
	id[8] = (id[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}
