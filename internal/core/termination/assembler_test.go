package termination

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"

	"github.com/pumpitspace/synevyr/internal/core/msgcontent"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// ---------------------------------------------------------------------------
// fakes
// ---------------------------------------------------------------------------

// memPartStore is an in-memory PartStore. It is deliberately keyed the same way
// the Redis implementation is, so a test that proves two messages do not collide
// proves it about the key rather than about the map.
type memPartStore struct {
	mu      sync.Mutex
	parts   map[string]map[uint32][]byte
	stored  int
	deleted int

	storeErr  error
	readErr   error
	deleteErr error
}

func newMemPartStore() *memPartStore {
	return &memPartStore{parts: map[string]map[uint32][]byte{}}
}

func (m *memPartStore) key(cid string, reference uint32, destination string) string {
	return "longDeliverSm:" + cid + ":" + strconv.FormatUint(uint64(reference), 10) + ":" + destination
}

func (m *memPartStore) StorePart(_ context.Context, cid string, reference uint32, destination string, sequence uint32, content []byte) error {
	if m.storeErr != nil {
		return m.storeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.key(cid, reference, destination)
	if _, ok := m.parts[key]; !ok {
		m.parts[key] = map[uint32][]byte{}
	}
	// Last write wins, because the store is a Redis hash and HSET overwrites the
	// field. This differs from the Python buffer, which keeps the first payload
	// for a duplicate sequence; the two agree for the case that actually happens
	// — a broker redelivering the identical segment — and the Redis behaviour is
	// what this fake must reproduce.
	m.parts[key][sequence] = append([]byte(nil), content...)
	m.stored++
	return nil
}

func (m *memPartStore) ReadParts(_ context.Context, cid string, reference uint32, destination string) (map[uint32][]byte, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[uint32][]byte{}
	for sequence, content := range m.parts[m.key(cid, reference, destination)] {
		out[sequence] = append([]byte(nil), content...)
	}
	return out, nil
}

func (m *memPartStore) DeleteParts(_ context.Context, cid string, reference uint32, destination string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.parts, m.key(cid, reference, destination))
	m.deleted++
	return nil
}

func (m *memPartStore) keys() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.parts)
}

// redisPartStore is the production adapter: the same one internal/app/gateway
// builds over rediscompat, restated here so the TTL test exercises the real key
// format and the real expiry rather than a stand-in.
type redisPartStore struct{ client *rediscompat.Client }

func (r redisPartStore) StorePart(ctx context.Context, cid string, reference uint32, destination string, sequence uint32, content []byte) error {
	key, err := rediscompat.BuildLegacyMultipartKey(cid, reference, destination)
	if err != nil {
		return err
	}
	return r.client.WriteLegacyMultipartPart(ctx, key, sequence, content)
}

func (r redisPartStore) ReadParts(ctx context.Context, cid string, reference uint32, destination string) (map[uint32][]byte, error) {
	key, err := rediscompat.BuildLegacyMultipartKey(cid, reference, destination)
	if err != nil {
		return nil, err
	}
	parts, err := r.client.ReadLegacyMultipartParts(ctx, key)
	if errors.Is(err, rediscompat.ErrKeyNotFound) {
		return map[uint32][]byte{}, nil
	}
	return parts, err
}

func (r redisPartStore) DeleteParts(ctx context.Context, cid string, reference uint32, destination string) error {
	key, err := rediscompat.BuildLegacyMultipartKey(cid, reference, destination)
	if err != nil {
		return err
	}
	return r.client.Delete(ctx, key)
}

// recordingDrain captures what the stitch released on a timer.
type recordingDrain struct {
	mu        sync.Mutex
	completed []Message
	err       error
}

func (d *recordingDrain) Complete(_ context.Context, msg Message) error {
	if d.err != nil {
		return d.err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.completed = append(d.completed, msg)
	return nil
}

func (d *recordingDrain) messages() []Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Message(nil), d.completed...)
}

func newTestAssembler(t *testing.T, parts PartStore, stitch StitchSettings, drain StitchDrain, now func() time.Time) *MultipartAssembler {
	t.Helper()
	assembler, err := NewMultipartAssembler(
		AssemblerConfig{CID: "partner-a-term", Stitch: stitch},
		parts,
		NewDefaultMsgContentDecoder(),
		drain,
		now,
	)
	if err != nil {
		t.Fatalf("new multipart assembler: %v", err)
	}
	return assembler
}

// udhSegment builds a segment carrying an 8-bit concatenation information
// element, the shape almost every real sender uses.
func udhSegment(reference byte, total, sequence byte, body []byte) []byte {
	return append([]byte{0x05, 0x00, 0x03, reference, total, sequence}, body...)
}

func segmentMessage(id, from, to string, dataCoding byte, raw []byte) Message {
	return Message{
		MessageID:  id,
		Connector:  "partner-a-term",
		Partner:    "partner-a",
		From:       from,
		To:         to,
		Raw:        raw,
		DataCoding: dataCoding,
		Parts:      1,
		ReceivedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}
}

// ---------------------------------------------------------------------------
// segment extraction — ported from the Python test_udh_concat.py cases
// ---------------------------------------------------------------------------

func TestSegmentFromUDH(t *testing.T) {
	cases := []struct {
		name     string
		raw      []byte
		want     Segment
		wantFind bool
	}{
		{
			name:     "8-bit concat IE",
			raw:      append([]byte{0x05, 0x00, 0x03, 0xab, 0x02, 0x01}, []byte("Hello part one")...),
			want:     Segment{Reference: 0xab, Total: 2, Sequence: 1, Body: []byte("Hello part one")},
			wantFind: true,
		},
		{
			name:     "16-bit concat IE",
			raw:      append([]byte{0x06, 0x08, 0x04, 0x12, 0x34, 0x03, 0x02}, []byte("middle part")...),
			want:     Segment{Reference: 0x1234, Total: 3, Sequence: 2, Body: []byte("middle part")},
			wantFind: true,
		},
		{
			name: "plain text is not a header",
			raw:  []byte("Hello World! Test message 123"),
		},
		{
			// The naive "first byte is the header length" heuristic would eat six
			// characters here and hold the rest waiting for siblings forever.
			name: "small first byte with no valid IE chain",
			raw:  []byte("\x05Hello world"),
		},
		{
			name: "declared length exceeds the available bytes",
			raw:  []byte{0x05, 0x00, 0x03, 0xab, 0x02},
		},
		{
			// Application port addressing: a valid header that says nothing about
			// concatenation.
			name: "valid header without a concat IE",
			raw:  append([]byte{0x04, 0x04, 0x02, 0x10, 0x20}, []byte("payload")...),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := SegmentFromUDH(tc.raw)
			if found != tc.wantFind {
				t.Fatalf("found = %v, want %v", found, tc.wantFind)
			}
			if !found {
				return
			}
			if got.Reference != tc.want.Reference || got.Total != tc.want.Total || got.Sequence != tc.want.Sequence {
				t.Errorf("coordinates = %d %d/%d, want %d %d/%d",
					got.Reference, got.Sequence, got.Total,
					tc.want.Reference, tc.want.Sequence, tc.want.Total)
			}
			if string(got.Body) != string(tc.want.Body) {
				t.Errorf("body = %q, want %q", got.Body, tc.want.Body)
			}
		})
	}
}

// SAR carries the same coordinates in optional parameters instead of in the
// body, and SMPP 3.4 requires all three together.
func TestSegmentFromSAR(t *testing.T) {
	reference := uint16(77)
	total, sequence := byte(2), byte(2)
	body := []byte("oral con personas que no viven contigo.")

	full := smppwire.OptionalParameters{
		SARMessageReference: &reference,
		SARTotalSegments:    &total,
		SARSegmentSequence:  &sequence,
	}
	got, ok := SegmentFromSAR(full, body)
	if !ok {
		t.Fatal("complete SAR parameters were not recognised")
	}
	if got.Reference != 77 || got.Total != 2 || got.Sequence != 2 || string(got.Body) != string(body) {
		t.Errorf("segment = %+v, want ref 77 part 2/2 with the whole short_message", got)
	}

	partial := smppwire.OptionalParameters{SARMessageReference: &reference, SARTotalSegments: &total}
	if _, ok := SegmentFromSAR(partial, body); ok {
		t.Error("an incomplete SAR triple was accepted; it describes no position in a sequence")
	}
}

// ---------------------------------------------------------------------------
// reassembly — ported from the Python test_reassembly.py cases
// ---------------------------------------------------------------------------

// segmentInput is one arriving segment in a reassembly table case.
type segmentInput struct {
	id   string
	from string
	to   string
	// sar drives the SAR path instead of the UDH one.
	sar          bool
	ref          byte
	total, seq   byte
	body         string
	wantComplete bool
	wantText     string
	wantParts    int
}

func TestMultipartAssemblerJoinsSegments(t *testing.T) {
	const from, to = "NETFLIX", "593996844442"

	cases := []struct {
		name     string
		segments []segmentInput
	}{
		{
			name: "two parts arriving in order",
			segments: []segmentInput{
				{id: "p1", ref: 0xab, total: 2, seq: 1, body: "Netflix: Usa 8034 ... tem"},
				{id: "p2", ref: 0xab, total: 2, seq: 2, body: "oral con personas.",
					wantComplete: true, wantText: "Netflix: Usa 8034 ... temoral con personas.", wantParts: 2},
			},
		},
		{
			name: "parts arriving out of order still join in sequence order",
			segments: []segmentInput{
				{id: "p2", ref: 0xab, total: 2, seq: 2, body: "SECOND"},
				{id: "p1", ref: 0xab, total: 2, seq: 1, body: "FIRST",
					wantComplete: true, wantText: "FIRSTSECOND", wantParts: 2},
			},
		},
		{
			name: "three parts with the middle arriving last",
			segments: []segmentInput{
				{id: "p1", ref: 0xab, total: 3, seq: 1, body: "A"},
				{id: "p3", ref: 0xab, total: 3, seq: 3, body: "C"},
				{id: "p2", ref: 0xab, total: 3, seq: 2, body: "B",
					wantComplete: true, wantText: "ABC", wantParts: 3},
			},
		},
		{
			// A broker redelivery repeats a segment verbatim. It must not count
			// as progress — two arrivals of part 1 do not make a two-part
			// message complete — and it must not disturb the assembled result.
			name: "a redelivered duplicate segment is not progress",
			segments: []segmentInput{
				{id: "p1", ref: 0xab, total: 2, seq: 1, body: "ORIGINAL"},
				{id: "p1-again", ref: 0xab, total: 2, seq: 1, body: "ORIGINAL"},
				{id: "p2", ref: 0xab, total: 2, seq: 2, body: "+TAIL",
					wantComplete: true, wantText: "ORIGINAL+TAIL", wantParts: 2},
			},
		},
		{
			name: "two interleaved references from the same sender stay separate",
			segments: []segmentInput{
				{id: "a1", ref: 1, total: 2, seq: 1, body: "A1"},
				{id: "b1", ref: 2, total: 2, seq: 1, body: "B1"},
				{id: "b2", ref: 2, total: 2, seq: 2, body: "B2",
					wantComplete: true, wantText: "B1B2", wantParts: 2},
				{id: "a2", ref: 1, total: 2, seq: 2, body: "A2",
					wantComplete: true, wantText: "A1A2", wantParts: 2},
			},
		},
		{
			// Not a Python case: the Python buffer keys by (from, to, ref) and
			// this store keys by (connector, ref, destination). Two brands
			// messaging one handset pick their references independently, so
			// without the source in the key one brand's segments join the other's.
			name: "the same reference from two senders to one handset stays separate",
			segments: []segmentInput{
				{id: "n1", from: "NETFLIX", ref: 7, total: 2, seq: 1, body: "netflix-one "},
				{id: "u1", from: "UBER", ref: 7, total: 2, seq: 1, body: "uber-one "},
				{id: "u2", from: "UBER", ref: 7, total: 2, seq: 2, body: "uber-two",
					wantComplete: true, wantText: "uber-one uber-two", wantParts: 2},
				{id: "n2", from: "NETFLIX", ref: 7, total: 2, seq: 2, body: "netflix-two",
					wantComplete: true, wantText: "netflix-one netflix-two", wantParts: 2},
			},
		},
		{
			name: "SAR coordinates take the same path as a UDH header",
			segments: []segmentInput{
				{id: "s1", sar: true, ref: 77, total: 2, seq: 1, body: "Netflix: Usa 8034 "},
				{id: "s2", sar: true, ref: 77, total: 2, seq: 2, body: "para iniciar sesion.",
					wantComplete: true, wantText: "Netflix: Usa 8034 para iniciar sesion.", wantParts: 2},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemPartStore()
			assembler := newTestAssembler(t, store, StitchSettings{}, nil, nil)
			ctx := context.Background()

			for i, in := range tc.segments {
				sender := in.from
				if sender == "" {
					sender = from
				}
				destination := in.to
				if destination == "" {
					destination = to
				}

				var (
					assembled Message
					complete  bool
					err       error
				)
				if in.sar {
					// data_coding 1 is IA5/ASCII, which the decoder reads as
					// plain text without a detection detour.
					msg := segmentMessage(in.id, sender, destination, 1, []byte(in.body))
					reference, total, sequence := uint16(in.ref), in.total, in.seq
					segment, ok := SegmentFromSAR(smppwire.OptionalParameters{
						SARMessageReference: &reference,
						SARTotalSegments:    &total,
						SARSegmentSequence:  &sequence,
					}, msg.Raw)
					if !ok {
						t.Fatalf("segment %d: SAR parameters not recognised", i)
					}
					assembled, complete, err = assembler.AddSegment(ctx, msg, segment)
				} else {
					msg := segmentMessage(in.id, sender, destination, 1, udhSegment(in.ref, in.total, in.seq, []byte(in.body)))
					assembled, complete, err = assembler.Add(ctx, msg)
				}

				if err != nil {
					t.Fatalf("segment %d (%s): %v", i, in.id, err)
				}
				if complete != in.wantComplete {
					t.Fatalf("segment %d (%s): complete = %v, want %v", i, in.id, complete, in.wantComplete)
				}
				if !complete {
					continue
				}
				if assembled.Text != in.wantText {
					t.Errorf("segment %d (%s): text = %q, want %q", i, in.id, assembled.Text, in.wantText)
				}
				if got := string(assembled.Raw); got != in.wantText {
					t.Errorf("segment %d (%s): raw = %q, want the segment bodies concatenated (%q)", i, in.id, got, in.wantText)
				}
				if assembled.Parts != in.wantParts {
					t.Errorf("segment %d (%s): parts = %d, want %d", i, in.id, assembled.Parts, in.wantParts)
				}
				// The completing segment's queue message id is what the accept
				// leg was published under, so the assembled message must keep it
				// or its receipt correlates to nothing.
				if assembled.MessageID != in.id {
					t.Errorf("segment %d: message id = %q, want the completing segment's %q", i, assembled.MessageID, in.id)
				}
			}

			// Everything joined must leave nothing behind: held segments occupy
			// the store until their TTL, and a set left in place lets a later
			// redelivery re-complete the same message under a different id —
			// a second accept leg, a second receipt, a second delivery.
			if store.keys() != 0 {
				t.Errorf("%d segment sets left in the store after every message completed", store.keys())
			}
			completed := 0
			for _, in := range tc.segments {
				if in.wantComplete {
					completed++
				}
			}
			if store.deleted != completed {
				t.Errorf("dropped %d segment sets, want one per completed message (%d)", store.deleted, completed)
			}
		})
	}
}

// The point of the whole exercise: decoding happens once, on the joined bytes.
// A UCS-2 surrogate pair (any emoji) occupies four bytes and is split down the
// middle when the segment boundary falls between its two code units. Decoding
// the halves separately yields two replacement characters and the character is
// gone for good; joining first yields the character.
func TestMultipartAssemblerDecodesAfterJoining(t *testing.T) {
	// "Hi " + U+1F600 GRINNING FACE + " ok", UTF-16BE, split so the surrogate
	// pair straddles the boundary.
	whole := []byte{0x00, 'H', 0x00, 'i', 0x00, ' ', 0xD8, 0x3D, 0xDE, 0x00, 0x00, ' ', 0x00, 'o', 0x00, 'k'}
	first, second := whole[:8], whole[8:]

	store := newMemPartStore()
	assembler := newTestAssembler(t, store, StitchSettings{}, nil, nil)
	ctx := context.Background()

	decoder := NewDefaultMsgContentDecoder()
	firstAlone, _, _ := decoder.Decode(first, 8)
	secondAlone, _, _ := decoder.Decode(second, 8)
	perSegment := firstAlone + secondAlone

	if _, complete, err := assembler.Add(ctx, segmentMessage("p1", "NETFLIX", "380671234567", 8, udhSegment(0x2A, 2, 1, first))); err != nil || complete {
		t.Fatalf("first segment: complete = %v, err = %v", complete, err)
	}
	assembled, complete, err := assembler.Add(ctx, segmentMessage("p2", "NETFLIX", "380671234567", 8, udhSegment(0x2A, 2, 2, second)))
	if err != nil || !complete {
		t.Fatalf("second segment: complete = %v, err = %v", complete, err)
	}

	if assembled.Text != "Hi \U0001F600 ok" {
		t.Errorf("joined text = %q, want %q", assembled.Text, "Hi \U0001F600 ok")
	}
	if perSegment == assembled.Text {
		t.Fatal("the per-segment decode happened to match; this case no longer proves the ordering")
	}
	t.Logf("per-segment decode would have produced %q", perSegment)
	if len(assembled.Raw) != len(whole) {
		t.Errorf("raw length = %d, want %d", len(assembled.Raw), len(whole))
	}
}

// A partner that sends two of three segments must not hold state forever. The
// TTL that bounds it is the part store's, exercised here through the real Redis
// key format rather than a stand-in.
func TestMultipartAssemblerMissingSegmentNeverCompletesAndExpires(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	parts := redisPartStore{client: rediscompat.NewClient(client)}
	assembler := newTestAssembler(t, parts, StitchSettings{}, nil, nil)
	ctx := context.Background()

	for _, sequence := range []byte{1, 3} {
		msg := segmentMessage(fmt.Sprintf("p%d", sequence), "NETFLIX", "380671234567", 1,
			udhSegment(0x2A, 3, sequence, []byte(fmt.Sprintf("part-%d ", sequence))))
		assembled, complete, err := assembler.Add(ctx, msg)
		if err != nil {
			t.Fatalf("segment %d: %v", sequence, err)
		}
		if complete {
			t.Fatalf("segment %d completed a message that is missing its middle", sequence)
		}
		if assembled.MessageID != "" {
			t.Errorf("an incomplete message returned content: %v", assembled)
		}
	}

	key, err := rediscompat.BuildLegacyMultipartKey("partner-a-term", 0x2A, partKeyDestination("NETFLIX", "380671234567"))
	if err != nil {
		t.Fatalf("build key: %v", err)
	}
	held, err := parts.client.ReadLegacyMultipartParts(ctx, key)
	if err != nil {
		t.Fatalf("read parts: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("held %d segments, want the two that arrived", len(held))
	}
	if ttl := server.TTL(key.String()); ttl != rediscompat.LegacyMultipartTTLSeconds*time.Second {
		t.Fatalf("ttl = %s, want %ds", ttl, rediscompat.LegacyMultipartTTLSeconds)
	}

	// Past the TTL the partial message is gone: the partner's half-sent message
	// does not occupy the gateway indefinitely.
	server.FastForward((rediscompat.LegacyMultipartTTLSeconds + 1) * time.Second)
	if server.Exists(key.String()) {
		t.Fatal("the segment set outlived its TTL")
	}
	after, err := parts.ReadParts(ctx, "partner-a-term", 0x2A, partKeyDestination("NETFLIX", "380671234567"))
	if err != nil {
		t.Fatalf("read after expiry: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("read %d segments after expiry, want none", len(after))
	}
}

// A message that declares no concatenation must come back exactly as it went in,
// with no part-store traffic at all: a connector that does not opt into the
// stitch behaves as it did before this assembler existed.
func TestMultipartAssemblerPassesWholeMessagesThrough(t *testing.T) {
	store := newMemPartStore()
	assembler := newTestAssembler(t, store, StitchSettings{}, nil, nil)

	msg := segmentMessage("msg-1", "NETFLIX", "380671234567", 1, []byte("code 63125"))
	msg.Text = "code 63125"
	msg.Encoding = "ascii"

	assembled, complete, err := assembler.Add(context.Background(), msg)
	if err != nil || !complete {
		t.Fatalf("complete = %v, err = %v", complete, err)
	}
	if assembled.Text != msg.Text || assembled.Encoding != msg.Encoding || string(assembled.Raw) != string(msg.Raw) {
		t.Errorf("message was altered: %+v", assembled)
	}
	if assembled.Parts != 1 {
		t.Errorf("parts = %d, want 1", assembled.Parts)
	}
	if store.stored != 0 || store.keys() != 0 {
		t.Errorf("a whole message touched the part store (%d writes)", store.stored)
	}
}

// A store failure is transient, not terminal: the message must be redelivered
// rather than dead-lettered, or a Redis blip loses traffic.
func TestMultipartAssemblerReportsStoreFailuresAsRetryable(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*memPartStore)
		// segments is how many segments to feed before the failure is expected.
		segments int
	}{
		{"store", func(s *memPartStore) { s.storeErr = errors.New("redis down") }, 1},
		{"read", func(s *memPartStore) { s.readErr = errors.New("redis down") }, 1},
		{"delete", func(s *memPartStore) { s.deleteErr = errors.New("redis down") }, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemPartStore()
			assembler := newTestAssembler(t, store, StitchSettings{}, nil, nil)
			ctx := context.Background()

			var err error
			for sequence := 1; sequence <= tc.segments; sequence++ {
				if sequence == tc.segments {
					tc.prepare(store)
				}
				msg := segmentMessage(fmt.Sprintf("p%d", sequence), "NETFLIX", "380671234567", 1,
					udhSegment(0x2A, 2, byte(sequence), []byte("body")))
				_, _, err = assembler.Add(ctx, msg)
			}
			if err == nil {
				t.Fatal("a part-store failure was swallowed")
			}
			if IsTerminal(err) {
				t.Errorf("error %v is terminal; a Redis blip would dead-letter the message", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// the plain-split stitch
// ---------------------------------------------------------------------------

// With the stitch off — the default — two messages between the same pair of
// addresses inside any window stay two messages. This is the failure mode that
// makes the stitch per-connector rather than global.
func TestStitchDisabledKeepsSameSenderMessagesSeparate(t *testing.T) {
	assembler := newTestAssembler(t, newMemPartStore(), StitchSettings{}, nil, nil)
	ctx := context.Background()

	first := segmentMessage("m1", "OTP", "380671234567", 1, []byte("code 111111"))
	first.Text = "code 111111"
	second := segmentMessage("m2", "OTP", "380671234567", 1, []byte("code 222222"))
	second.Text = "code 222222"

	one, complete, err := assembler.Add(ctx, first)
	if err != nil || !complete {
		t.Fatalf("first: complete = %v, err = %v", complete, err)
	}
	two, complete, err := assembler.Add(ctx, second)
	if err != nil || !complete {
		t.Fatalf("second: complete = %v, err = %v", complete, err)
	}
	if one.Text != "code 111111" || two.Text != "code 222222" {
		t.Errorf("messages were joined: %q and %q", one.Text, two.Text)
	}
	if one.MessageID == two.MessageID {
		t.Error("two messages collapsed onto one id")
	}
}

// Enabling the stitch without a drain must fail loudly. A held message has
// already been settled on the queue: no redelivery brings it back, and with
// nothing draining the buffer it is simply never seen again.
func TestStitchRequiresADrain(t *testing.T) {
	_, err := NewMultipartAssembler(
		AssemblerConfig{CID: "partner-a-term", Stitch: StitchSettings{Window: 15 * time.Second}},
		newMemPartStore(),
		NewDefaultMsgContentDecoder(),
		nil,
		nil,
	)
	if !errors.Is(err, ErrStitchDrainRequired) {
		t.Fatalf("error = %v, want ErrStitchDrainRequired", err)
	}
}

func TestStitchMergesSiblingsAndFlushesSolos(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	drain := &recordingDrain{}
	assembler := newTestAssembler(t, newMemPartStore(), StitchSettings{Window: 15 * time.Second}, drain, clock)
	ctx := context.Background()

	first := segmentMessage("m1", "sms", "77760098888", 1, []byte("https://"))
	first.Text = "https://"
	if _, complete, err := assembler.Add(ctx, first); err != nil || complete {
		t.Fatalf("first chunk: complete = %v, err = %v", complete, err)
	}
	if assembler.PendingStitchGroups() != 1 {
		t.Fatalf("pending groups = %d, want 1", assembler.PendingStitchGroups())
	}

	second := segmentMessage("m2", "sms", "77760098888", 1, []byte("wa.me/123"))
	second.Text = "wa.me/123"
	merged, complete, err := assembler.Add(ctx, second)
	if err != nil || !complete {
		t.Fatalf("second chunk: complete = %v, err = %v", complete, err)
	}
	if merged.Text != "https://wa.me/123" {
		t.Errorf("text = %q, want the chunks joined", merged.Text)
	}
	if merged.Parts != 2 {
		t.Errorf("parts = %d, want 2", merged.Parts)
	}
	// The completing chunk's id, because that is the id the accept leg for this
	// delivery was published under.
	if merged.MessageID != "m2" {
		t.Errorf("message id = %q, want the completing chunk's", merged.MessageID)
	}

	// A message with no sibling is released only by the window, through the
	// drain: there is no queue delivery in hand at that moment.
	solo := segmentMessage("m3", "sms", "380671234567", 1, []byte("alone"))
	solo.Text = "alone"
	if _, complete, err := assembler.Add(ctx, solo); err != nil || complete {
		t.Fatalf("solo chunk: complete = %v, err = %v", complete, err)
	}
	if released, err := assembler.FlushExpired(ctx); err != nil || released != 0 {
		t.Fatalf("released %d before the window elapsed (err %v)", released, err)
	}

	now = now.Add(15 * time.Second)
	released, err := assembler.FlushExpired(ctx)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if released != 1 {
		t.Fatalf("released %d groups, want 1", released)
	}
	completed := drain.messages()
	if len(completed) != 1 || completed[0].MessageID != "m3" || completed[0].Text != "alone" || completed[0].Parts != 1 {
		t.Errorf("drained %+v, want the solo message unchanged", completed)
	}
	if assembler.PendingStitchGroups() != 0 {
		t.Errorf("pending groups = %d after the flush, want 0", assembler.PendingStitchGroups())
	}
}

// A reassembled multipart message does not then enter the stitch: its
// concatenation was declared by the sender and is already complete. The Python
// service marks these _skip_stitch for the same reason.
func TestStitchSkipsReassembledMultipart(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	drain := &recordingDrain{}
	assembler := newTestAssembler(t, newMemPartStore(), StitchSettings{Window: 15 * time.Second}, drain, func() time.Time { return now })
	ctx := context.Background()

	if _, complete, err := assembler.Add(ctx, segmentMessage("p1", "NETFLIX", "380671234567", 1, udhSegment(0x2A, 2, 1, []byte("first ")))); err != nil || complete {
		t.Fatalf("first segment: complete = %v, err = %v", complete, err)
	}
	assembled, complete, err := assembler.Add(ctx, segmentMessage("p2", "NETFLIX", "380671234567", 1, udhSegment(0x2A, 2, 2, []byte("second"))))
	if err != nil {
		t.Fatalf("second segment: %v", err)
	}
	if !complete {
		t.Fatal("the reassembled message was held by the stitch instead of being released")
	}
	if assembled.Text != "first second" {
		t.Errorf("text = %q", assembled.Text)
	}
	if assembler.PendingStitchGroups() != 0 {
		t.Errorf("the reassembled message entered the stitch buffer")
	}
}

func TestStitchSettingsValidation(t *testing.T) {
	cases := []struct {
		name     string
		settings StitchSettings
		wantErr  bool
	}{
		{"off", StitchSettings{}, false},
		{"off but pre-configured", StitchSettings{MaxChunks: 4, Separator: " "}, false},
		{"enabled", StitchSettings{Window: 15 * time.Second}, false},
		{"negative window", StitchSettings{Window: -time.Second}, true},
		{"max chunks below two", StitchSettings{Window: time.Second, MaxChunks: 1}, true},
		{"max buffered below one", StitchSettings{Window: time.Second, MaxBuffered: -1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ConnectorConfig{
				CID:     "partner-a-term",
				Verdict: VerdictConfig{Source: SourceStatic},
				Stitch:  tc.settings,
			}.Validate()
			if tc.wantErr && !errors.Is(err, ErrInvalidConnectorConfig) {
				t.Fatalf("error = %v, want ErrInvalidConnectorConfig", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// end to end: three segments, one of everything
// ---------------------------------------------------------------------------

// memSpoolStore is the part of msgspool.Repository the worker and the receipt
// runner use, in memory. Put upserts on the message id, like the real store.
type memSpoolStore struct {
	mu       sync.Mutex
	rows     map[string]*msgspool.Record
	order    []string
	sequence int64
	puts     int
}

func newMemSpoolStore() *memSpoolStore {
	return &memSpoolStore{rows: map[string]*msgspool.Record{}}
}

func (s *memSpoolStore) Put(_ context.Context, message msgspool.Message, _ time.Time) (msgspool.Record, error) {
	if err := msgspool.ValidateMessage(message); err != nil {
		return msgspool.Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	s.sequence++
	record, existing := s.rows[message.MessageID]
	if !existing {
		record = &msgspool.Record{DeliveryState: msgspool.DeliveryPending}
		s.rows[message.MessageID] = record
		s.order = append(s.order, message.MessageID)
		record.Message = message
		record.Sequence = s.sequence
		return *record, nil
	}
	// The same conflict rules both real backends apply. Without them this fake
	// would accept a redelivered segment blanking the text of a message that was
	// already spooled, and the test suite would be proving a contract production
	// does not have. See PostgresMessageSpool.Put.
	previous := record.Message
	record.Message = message
	if message.ReceiptOnly {
		record.Text, record.Raw = previous.Text, previous.Raw
		record.DataCoding, record.Encoding = previous.DataCoding, previous.Encoding
		record.Parts = previous.Parts
	}
	record.ReceiptOnly = previous.ReceiptOnly && message.ReceiptOnly
	if previous.ReceiptOnly && !message.ReceiptOnly {
		record.NextAttemptAt = message.NextAttemptAt
	} else {
		record.NextAttemptAt = previous.NextAttemptAt
	}
	record.ReceiptDueAt = previous.ReceiptDueAt
	record.Sequence = s.sequence
	return *record, nil
}

func (s *memSpoolStore) DueForDelivery(_ context.Context, now time.Time, limit int) ([]msgspool.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []msgspool.Record
	for _, id := range s.order {
		record := s.rows[id]
		if record.DeliveryState != msgspool.DeliveryPending || record.NextAttemptAt == nil || record.NextAttemptAt.After(now) {
			continue
		}
		due = append(due, *record)
		if limit > 0 && len(due) >= limit {
			break
		}
	}
	return due, nil
}

func (s *memSpoolStore) MarkDelivered(_ context.Context, messageID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.rows[messageID]
	if !ok {
		return msgspool.ErrNotFound
	}
	record.DeliveryState = msgspool.DeliveryDelivered
	record.DeliveredAt = &at
	record.NextAttemptAt = nil
	return nil
}

func (s *memSpoolStore) MarkAttemptFailed(_ context.Context, messageID string, nextAttemptAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.rows[messageID]
	if !ok {
		return msgspool.ErrNotFound
	}
	record.DeliveryAttempts++
	record.NextAttemptAt = &nextAttemptAt
	return nil
}

func (s *memSpoolStore) MarkDeadLettered(_ context.Context, messageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.rows[messageID]
	if !ok {
		return msgspool.ErrNotFound
	}
	record.DeliveryAttempts++
	record.DeliveryState = msgspool.DeliveryDeadLettered
	record.NextAttemptAt = nil
	return nil
}

func (s *memSpoolStore) ClaimDueReceipts(_ context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]msgspool.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var claimed []msgspool.Record
	until := now.Add(lease)
	for _, id := range s.order {
		record := s.rows[id]
		if record.ReceiptDueAt == nil || record.ReceiptDueAt.After(now) || record.ReceiptSentAt != nil {
			continue
		}
		if record.ReceiptLockedUntil != nil && record.ReceiptLockedUntil.After(now) {
			continue
		}
		record.ReceiptLockOwner = owner
		record.ReceiptLockedUntil = &until
		claimed = append(claimed, *record)
		if limit > 0 && len(claimed) >= limit {
			break
		}
	}
	return claimed, nil
}

func (s *memSpoolStore) MarkReceiptSent(_ context.Context, messageID, owner string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.rows[messageID]
	if !ok {
		return msgspool.ErrNotFound
	}
	if record.ReceiptLockOwner != owner {
		return msgspool.ErrClaimLost
	}
	sent := at
	record.ReceiptSentAt = &sent
	record.ReceiptLockOwner = ""
	record.ReceiptLockedUntil = nil
	return nil
}

func (s *memSpoolStore) records() []msgspool.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]msgspool.Record, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, *s.rows[id])
	}
	return out
}

// legCapturingPublisher records the routing key of every published envelope, so
// accept legs and receipt legs can be counted apart.
type legCapturingPublisher struct {
	mu   sync.Mutex
	keys []string
}

func (p *legCapturingPublisher) Publish(_ context.Context, _ string, routingKey string, _ amqpcompat.Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append(p.keys, routingKey)
	return nil
}

func (p *legCapturingPublisher) count(routingKey string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	for _, key := range p.keys {
		if key == routingKey {
			total++
		}
	}
	return total
}

// bodySubmits decodes a queued submit by looking the body up, so each segment of
// a concatenated message can carry its own PDU.
type bodySubmits struct {
	bodies map[string]smppwire.SubmitSMBody
}

func (d bodySubmits) DecodeSubmitSM(_ context.Context, body []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	decoded, ok := d.bodies[string(body)]
	if !ok {
		return smppwire.SubmitSMBody{}, nil, fmt.Errorf("no stub submit for body %q", body)
	}
	return decoded, nil, nil
}

// segmentWorker wires a worker over a real multipart assembler and a real
// redis-window verdict source, so a test can count gate lookups rather than
// Decide calls: the property that matters is that three segments of one message
// ask the activation gate once, which is also what makes their three receipts
// agree with each other.
type segmentWorker struct {
	worker    *Worker
	store     *memSpoolStore
	publisher *legCapturingPublisher
	probe     *fakeWindowProbe
}

func newSegmentWorker(t *testing.T, clock func() time.Time, bodies map[string]smppwire.SubmitSMBody) segmentWorker {
	t.Helper()
	const cid = "partner-a-term"
	store := newMemSpoolStore()
	spool, err := NewSpool(store, clock, func(string) bool { return true })
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	publisher := &legCapturingPublisher{}
	leg, err := NewSMSCLeg(publisher, cid, clock)
	if err != nil {
		t.Fatalf("new smsc leg: %v", err)
	}
	probe := probeWindowOpen()
	verdicts, err := NewVerdictSource(VerdictConfig{Source: SourceRedisWindow}, Dependencies{Redis: probe})
	if err != nil {
		t.Fatalf("new verdict source: %v", err)
	}
	worker, err := NewWorker(
		WorkerConfig{CID: cid, ReceiptDelay: 5 * time.Second, ReceiptJitter: 2 * time.Second},
		bodySubmits{bodies: bodies},
		NewDefaultMsgContentDecoder(),
		newTestAssembler(t, newMemPartStore(), StitchSettings{}, nil, clock),
		verdicts,
		spool,
		leg,
		clock,
		func() float64 { return 0.5 },
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	return segmentWorker{worker: worker, store: store, publisher: publisher, probe: probe}
}

// threeSegmentBodies is one long message split across three submit_sm PDUs,
// keyed by the queue body each arrives in.
func threeSegmentBodies() (map[string]smppwire.SubmitSMBody, []string) {
	segments := []string{"Netflix: Usa 8034 ", "para iniciar sesion. ", "No compartas este codigo."}
	bodies := map[string]smppwire.SubmitSMBody{}
	for i, text := range segments {
		bodies[fmt.Sprintf("queue-body-%d", i+1)] = smppwire.SubmitSMBody{
			SourceAddress:      []byte("NETFLIX"),
			DestinationAddress: []byte("+380671234567"),
			ShortMessage:       udhSegment(0x2A, 3, byte(i+1), []byte(text)),
			DataCoding:         1,
		}
	}
	return bodies, segments
}

// This is what the whole step is for, and it is where the first implementation
// got it wrong.
//
// A partner submits one long message as three segments. Each is its own
// submit_sm, so each is answered with its own message id, each carries its own
// registered_delivery flag, and each is owed its own receipt — three accept legs
// and three receipts, which is exactly what the legacy fake SMSC sends and what
// the partner's reconciliation counts. The *content*, however, is one message:
// one spool row carrying the joined text, and one downstream delivery.
//
// Collapsing the receipts into one — which is what "return early on an
// incomplete segment" did — silently cut the partner's receipt count from N to 1
// per long message, with nothing on either side to explain the gap.
func TestThreeSegmentsProduceThreeReceiptsAndOneMessage(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	bodies, segments := threeSegmentBodies()
	wired := newSegmentWorker(t, clock, bodies)

	ctx := context.Background()
	for i := range segments {
		meta := SubmitMetadata{QueueMessageID: fmt.Sprintf("queue-msg-%d", i+1), Partner: "partner-a"}
		if err := wired.worker.Process(ctx, meta, []byte(fmt.Sprintf("queue-body-%d", i+1))); err != nil {
			t.Fatalf("segment %d: %v", i+1, err)
		}
	}

	// One accept leg per submit_sm: the partner's client is waiting for a
	// submit_sm_resp on each, and each id is what its receipt will reference.
	if got := wired.publisher.count("dlr.submit_sm_resp"); got != 3 {
		t.Errorf("published %d accept legs, want 3 — one per submit_sm", got)
	}

	// One gate lookup, not three. The verdict source caches by normalized
	// destination and all three segments share one, so the segments cannot
	// disagree about whether the activation window was open.
	if got := wired.probe.lookups(); got != 1 {
		t.Errorf("consulted the activation gate %d times, want 1 for one message", got)
	}

	rows := wired.store.records()
	if len(rows) != 3 {
		t.Fatalf("spool holds %d rows, want 3: two receipt obligations and one message", len(rows))
	}
	var content []msgspool.Record
	var receiptRows []msgspool.Record
	for _, row := range rows {
		if row.ReceiptOnly {
			receiptRows = append(receiptRows, row)
			continue
		}
		content = append(content, row)
	}
	if len(content) != 1 {
		t.Fatalf("spool holds %d content rows, want exactly one for one submitted message", len(content))
	}
	if len(receiptRows) != 2 {
		t.Fatalf("spool holds %d receipt-only rows, want 2", len(receiptRows))
	}

	row := content[0]
	if row.Text != "Netflix: Usa 8034 para iniciar sesion. No compartas este codigo." {
		t.Errorf("spooled text = %q", row.Text)
	}
	if row.Parts != 3 {
		t.Errorf("parts = %d, want 3", row.Parts)
	}
	if row.MessageID != "queue-msg-3" {
		t.Errorf("content row id = %q, want the completing segment's queue id", row.MessageID)
	}
	if row.DestAddr != "380671234567" {
		t.Errorf("destination = %q, want it normalized", row.DestAddr)
	}

	// The fragments' bytes are not kept. Half a message is unreadable on its own
	// — split mid-rune for any multi-byte encoding — and the assembled row holds
	// the whole of it, so a second copy would only be a second place an OTP body
	// can leak from.
	for _, held := range receiptRows {
		if held.Text != "" || len(held.Raw) != 0 {
			t.Errorf("receipt row %s stored fragment content (text_len=%d raw_len=%d)",
				held.MessageID, len(held.Text), len(held.Raw))
		}
		if held.NextAttemptAt != nil {
			t.Errorf("receipt row %s was scheduled for a downstream push", held.MessageID)
		}
		if held.ReceiptDueAt == nil {
			t.Errorf("receipt row %s owes no receipt, so it exists for nothing", held.MessageID)
		}
	}

	// Three receipts, owed by three committed rows rather than by anything held
	// in this process.
	receipts, err := NewReceiptRunner(wired.store, wired.publisher, ReceiptRunnerConfig{Owner: "test-owner"}, clock)
	if err != nil {
		t.Fatalf("new receipt runner: %v", err)
	}
	if sent, err := receipts.RunOnce(ctx); err != nil || sent != 0 {
		t.Fatalf("sent %d receipts before the delay elapsed (err %v)", sent, err)
	}
	now = now.Add(10 * time.Second)
	sent, err := receipts.RunOnce(ctx)
	if err != nil {
		t.Fatalf("receipt run: %v", err)
	}
	if sent != 3 {
		t.Fatalf("sent %d receipts, want 3 — one per submit_sm the partner registered delivery on", sent)
	}
	if again, err := receipts.RunOnce(ctx); err != nil || again != 0 {
		t.Fatalf("a second pass sent %d more receipts (err %v)", again, err)
	}
	if got := wired.publisher.count("dlr.deliver_sm"); got != 3 {
		t.Errorf("published %d receipt legs, want 3", got)
	}

	// And exactly one downstream delivery, from the one content row. A receipt
	// obligation is never pushed: there is nothing to push, and scheduling one
	// would walk a fragment through its retry budget and dead-letter it.
	due, err := wired.store.DueForDelivery(ctx, now, 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("due for delivery = %d rows (err %v), want 1", len(due), err)
	}
	if due[0].MessageID != "queue-msg-3" || due[0].ReceiptOnly {
		t.Errorf("due row = %s (receipt_only=%v), want the assembled message",
			due[0].MessageID, due[0].ReceiptOnly)
	}
}

// Every receipt references a distinct SMSC message id, derived from the segment's
// own queue message id. Two segments sharing one would correlate to the same
// mapping and the partner would receive one receipt where two were published, or
// two against the same submit — which is the failure mode a per-segment receipt
// exists to avoid, arrived at from the other direction.
func TestEachSegmentReceiptCarriesItsOwnSMSCMessageID(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	bodies, segments := threeSegmentBodies()
	wired := newSegmentWorker(t, clock, bodies)

	ctx := context.Background()
	for i := range segments {
		meta := SubmitMetadata{QueueMessageID: fmt.Sprintf("queue-msg-%d", i+1), Partner: "partner-a"}
		if err := wired.worker.Process(ctx, meta, []byte(fmt.Sprintf("queue-body-%d", i+1))); err != nil {
			t.Fatalf("segment %d: %v", i+1, err)
		}
	}

	seen := map[string]string{}
	for _, row := range wired.store.records() {
		id := SMSCMessageID(row.MessageID)
		if previous, clash := seen[id]; clash {
			t.Fatalf("%s and %s both correlate to smsc id %q", previous, row.MessageID, id)
		}
		seen[id] = row.MessageID
	}
	if len(seen) != 3 {
		t.Fatalf("got %d distinct smsc ids, want 3", len(seen))
	}
}

// A single-part message must behave exactly as it did before segments were
// receipted separately: one accept leg, one content row that is not receipt-only,
// one receipt, one delivery.
func TestSinglePartMessageIsUnchanged(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	bodies := map[string]smppwire.SubmitSMBody{
		"queue-body-1": {
			SourceAddress:      []byte("NETFLIX"),
			DestinationAddress: []byte("+380671234567"),
			ShortMessage:       []byte("Netflix: Usa 8034"),
			DataCoding:         1,
		},
	}
	wired := newSegmentWorker(t, clock, bodies)

	ctx := context.Background()
	meta := SubmitMetadata{QueueMessageID: "queue-msg-1", Partner: "partner-a"}
	if err := wired.worker.Process(ctx, meta, []byte("queue-body-1")); err != nil {
		t.Fatalf("process: %v", err)
	}

	rows := wired.store.records()
	if len(rows) != 1 {
		t.Fatalf("spool holds %d rows, want 1", len(rows))
	}
	if rows[0].ReceiptOnly {
		t.Error("a whole message was spooled as a receipt obligation")
	}
	if rows[0].Text != "Netflix: Usa 8034" || rows[0].Parts != 1 {
		t.Errorf("row = text %q parts %d, want the whole message in one part", rows[0].Text, rows[0].Parts)
	}
	if rows[0].NextAttemptAt == nil {
		t.Error("a whole message was not scheduled for delivery")
	}
	if got := wired.publisher.count("dlr.submit_sm_resp"); got != 1 {
		t.Errorf("published %d accept legs, want 1", got)
	}

	receipts, err := NewReceiptRunner(wired.store, wired.publisher, ReceiptRunnerConfig{Owner: "test-owner"}, clock)
	if err != nil {
		t.Fatalf("new receipt runner: %v", err)
	}
	now = now.Add(10 * time.Second)
	if sent, err := receipts.RunOnce(ctx); err != nil || sent != 1 {
		t.Fatalf("sent %d receipts (err %v), want 1", sent, err)
	}
	if due, err := wired.store.DueForDelivery(ctx, now, 10); err != nil || len(due) != 1 {
		t.Fatalf("due for delivery = %d rows (err %v), want 1", len(due), err)
	}
}

// The point of persisting the receipt rather than holding it in a timer: a
// gateway that dies between two segments still owes — and still emits — the
// receipt for the segment it had already accepted.
//
// The second half of the test is the redelivery hazard that comes with
// per-segment rows. A worker that commits the assembled row and then fails to
// ack sees the completing segment redelivered, and the assembler has already
// dropped the joined parts, so it arrives looking *incomplete*. Re-spooling it
// as a receipt obligation must not blank the message that was already stored.
func TestSegmentReceiptSurvivesARestartBetweenSegments(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	bodies, _ := threeSegmentBodies()

	// One store and one part store outlive the worker, exactly as a database and
	// a Redis outlive a process.
	store := newMemSpoolStore()
	parts := newMemPartStore()
	publisher := &legCapturingPublisher{}
	ctx := context.Background()

	newWorker := func() *Worker {
		t.Helper()
		spool, err := NewSpool(store, clock, func(string) bool { return true })
		if err != nil {
			t.Fatalf("new spool: %v", err)
		}
		leg, err := NewSMSCLeg(publisher, "partner-a-term", clock)
		if err != nil {
			t.Fatalf("new smsc leg: %v", err)
		}
		verdicts, err := NewVerdictSource(VerdictConfig{Source: SourceRedisWindow}, Dependencies{Redis: probeWindowOpen()})
		if err != nil {
			t.Fatalf("new verdict source: %v", err)
		}
		worker, err := NewWorker(
			WorkerConfig{CID: "partner-a-term", ReceiptDelay: 5 * time.Second, ReceiptJitter: 2 * time.Second},
			bodySubmits{bodies: bodies},
			NewDefaultMsgContentDecoder(),
			newTestAssembler(t, parts, StitchSettings{}, nil, clock),
			verdicts,
			spool,
			leg,
			clock,
			func() float64 { return 0.5 },
		)
		if err != nil {
			t.Fatalf("new worker: %v", err)
		}
		return worker
	}

	// Segment 1 lands, and the process dies.
	if err := newWorker().Process(ctx,
		SubmitMetadata{QueueMessageID: "queue-msg-1", Partner: "partner-a"},
		[]byte("queue-body-1")); err != nil {
		t.Fatalf("segment 1: %v", err)
	}

	// A different process picks the receipt up from the committed row. Nothing
	// about the first worker is still alive.
	receipts, err := NewReceiptRunner(store, publisher, ReceiptRunnerConfig{Owner: "worker-after-restart"}, clock)
	if err != nil {
		t.Fatalf("new receipt runner: %v", err)
	}
	now = now.Add(10 * time.Second)
	if sent, err := receipts.RunOnce(ctx); err != nil || sent != 1 {
		t.Fatalf("after restart sent %d receipts (err %v), want the held segment's 1", sent, err)
	}

	// The remaining segments arrive on the new process and complete the message.
	restarted := newWorker()
	for _, i := range []int{2, 3} {
		meta := SubmitMetadata{QueueMessageID: fmt.Sprintf("queue-msg-%d", i), Partner: "partner-a"}
		if err := restarted.Process(ctx, meta, []byte(fmt.Sprintf("queue-body-%d", i))); err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
	}
	content, ok := findSpoolRow(store, "queue-msg-3")
	if !ok || content.ReceiptOnly || content.Text == "" {
		t.Fatalf("assembled row after restart = %+v, want the joined message", content.MessageID)
	}
	joined := content.Text

	// Now the ack for segment 3 is lost and the broker redelivers it. The parts
	// were already dropped, so the assembler reports it incomplete.
	if err := restarted.Process(ctx,
		SubmitMetadata{QueueMessageID: "queue-msg-3", Partner: "partner-a"},
		[]byte("queue-body-3")); err != nil {
		t.Fatalf("redelivered segment 3: %v", err)
	}
	after, ok := findSpoolRow(store, "queue-msg-3")
	if !ok {
		t.Fatal("the assembled row disappeared on redelivery")
	}
	if after.ReceiptOnly {
		t.Error("a redelivered segment demoted an assembled message to a receipt obligation")
	}
	if after.Text != joined {
		t.Errorf("text after redelivery = %q, want the joined message %q", after.Text, joined)
	}
}

func findSpoolRow(store *memSpoolStore, messageID string) (msgspool.Record, bool) {
	for _, row := range store.records() {
		if row.MessageID == messageID {
			return row, true
		}
	}
	return msgspool.Record{}, false
}

// partKeyDestination has to be injective: two different address pairs that
// produced one key would join one partner's message to another's.
func TestPartKeyDestinationIsUnambiguous(t *testing.T) {
	pairs := [][2]string{
		{"NETFLIX", "380671234567"},
		{"UBER", "380671234567"},
		{"NETFLIX", "3806712345670"},
		{"", "380671234567"},
		{"a|b", "380671234567"},
		{"a", "b|380671234567"},
		{"a:b", "380671234567"},
		{"a", "b:380671234567"},
	}
	seen := map[string][2]string{}
	for _, pair := range pairs {
		key := partKeyDestination(pair[0], pair[1])
		if previous, clash := seen[key]; clash {
			t.Fatalf("(%q,%q) and (%q,%q) produce the same key %q", previous[0], previous[1], pair[0], pair[1], key)
		}
		seen[key] = pair
		// The store's key format forbids ':' in a component and rejects an empty
		// one, so a source address containing one must not reach it raw.
		if _, err := rediscompat.BuildLegacyMultipartKey("partner-a-term", 1, key); err != nil {
			t.Fatalf("(%q,%q) produced an unusable key component %q: %v", pair[0], pair[1], key, err)
		}
	}
}

// The stitch groups by sender AND destination, the same key the Python service
// uses. One brand messaging two handsets must not have its two messages joined.
func TestStitchGroupsBySenderAndDestination(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	drain := &recordingDrain{}
	assembler := newTestAssembler(t, newMemPartStore(), StitchSettings{Window: 15 * time.Second}, drain, func() time.Time { return now })
	ctx := context.Background()

	first := segmentMessage("m1", "OTP", "380671111111", 1, []byte("code 111111"))
	first.Text = "code 111111"
	second := segmentMessage("m2", "OTP", "380672222222", 1, []byte("code 222222"))
	second.Text = "code 222222"

	if _, complete, err := assembler.Add(ctx, first); err != nil || complete {
		t.Fatalf("first: complete = %v, err = %v", complete, err)
	}
	if _, complete, err := assembler.Add(ctx, second); err != nil || complete {
		t.Fatalf("second: complete = %v, err = %v", complete, err)
	}
	if assembler.PendingStitchGroups() != 2 {
		t.Fatalf("pending groups = %d, want 2: different destinations are different groups", assembler.PendingStitchGroups())
	}

	now = now.Add(15 * time.Second)
	if released, err := assembler.FlushExpired(ctx); err != nil || released != 2 {
		t.Fatalf("released %d groups (err %v), want both as solos", released, err)
	}
	completed := drain.messages()
	if len(completed) != 2 || completed[0].Text != "code 111111" || completed[1].Text != "code 222222" {
		t.Errorf("drained %+v, want two separate messages", completed)
	}
	// A key type from the ported package, asserted here so the assembler and the
	// buffer cannot drift apart on what a group is.
	var _ msgcontent.StitchKey = msgcontent.StitchKey{From: first.From, To: first.To}
}

// TestStitchBufferIsFlushedOnShutdown is the regression for messages that were
// accepted, charged, and then vanished on restart.
//
// A message held in the plain-split stitch was settled on the queue the moment
// it arrived, so nothing redelivers it: this process is the only place it
// exists. Run returned as soon as its context was cancelled, dropping up to
// MaxBuffered of them per restart with no spool row, no receipt, no dead letter
// and no log line.
func TestStitchBufferIsFlushedOnShutdown(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	drain := &recordingDrain{}
	assembler := newTestAssembler(t, newMemPartStore(),
		StitchSettings{Window: 30 * time.Second}, drain, func() time.Time { return now })

	// One message with no sibling: it waits for its window, which will not
	// elapse before the process is asked to stop.
	solo := segmentMessage("m1", "sms", "77760098888", 1, []byte("orphan"))
	solo.Text = "orphan"
	if _, complete, err := assembler.Add(context.Background(), solo); err != nil || complete {
		t.Fatalf("add: complete = %v, err = %v", complete, err)
	}
	if assembler.PendingStitchGroups() != 1 {
		t.Fatalf("pending groups = %d, want 1", assembler.PendingStitchGroups())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assembler.Run(ctx, func(err error) { t.Errorf("flush error: %v", err) })
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	if got := assembler.PendingStitchGroups(); got != 0 {
		t.Errorf("pending groups = %d after shutdown, want 0", got)
	}
	messages := drain.messages()
	if len(messages) != 1 {
		t.Fatalf("drained %d messages on shutdown, want 1: buffered messages are already ACKed and nothing redelivers them", len(messages))
	}
	if messages[0].Text != "orphan" {
		t.Errorf("drained text = %q, want %q", messages[0].Text, "orphan")
	}
}
