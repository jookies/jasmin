package termination

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgcontent"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// MultipartAssembler joins a concatenated submit back into one message.
//
// A partner that sends a long SMS sends it as several submit_sm PDUs, each
// enqueued as its own queue message. PassThroughAssembler refuses them, which is
// honest but leaves the partner with an accepted submit and no receipt at all.
// This assembler holds the segments until the last one arrives and then produces
// a single message.
//
// What it joins is the *content*, and only the content. One submitted long
// message becomes one spool row carrying the joined text and one downstream
// delivery. It does not become one accept leg or one receipt: under SMPP 3.4
// each segment was its own submit_sm, answered with its own message id and
// carrying its own registered_delivery flag, so each is owed its own
// submit_sm_resp and its own delivery receipt — N of each for an N-segment
// message, which is what the legacy fake SMSC sends and what a partner
// reconciling receipts against submits counts. Worker.Process is where that
// happens; see its "Receipts belong to segments" section.
//
// # Decode after joining, never before
//
// The joined value is the concatenated *pre-decode bytes*, decoded once at the
// end. Decoding each segment and concatenating the resulting text is wrong for
// every multi-byte encoding: a UCS-2 surrogate pair, or a UTF-8 sequence in a
// body that turned out not to be UCS-2 at all, is split across the segment
// boundary, and decoding the halves separately produces two replacement
// characters where one character belongs. The worker decodes each arriving
// segment before calling Add — it cannot know yet that the message is split —
// and this assembler discards that per-segment text and decodes the joined body
// instead.
//
// # Where the segments live
//
// In the same Redis structure the inbound MO path already uses
// (longDeliverSm:<cid>:<ref>:<destination>, 300 s TTL, written through
// rediscompat). Reusing it means one place holds partial messages, one TTL
// governs how long a partner's half-sent message occupies memory, and an
// operator inspecting reassembly state finds both directions in one keyspace.
//
// # Concurrency
//
// Safe for concurrent use. The segment state lives in the part store, and the
// stitch buffer — which does not — is guarded here, because FlushExpired runs on
// a timer while Add runs on the connector's consumer goroutine.
type MultipartAssembler struct {
	cid     string
	parts   PartStore
	content ContentDecoder
	now     func() time.Time

	stitchMu sync.Mutex
	stitch   *msgcontent.StitchBuffer[Message]
	drain    StitchDrain
}

// PartStore accumulates the segments of a concatenated message until they are
// all present.
//
// It is smppc.MultipartStore, not a copy of it: the MO path and this one hold
// the same kind of state in the same keyspace with the same TTL, so one
// production implementation serves both and a fake written for either satisfies
// both. Segment content is opaque to the store.
type PartStore = smppc.MultipartStore

// StitchDrain runs the tail of the message path for a message the assembler
// released on a timer rather than in response to an arriving queue delivery.
//
// The plain-split stitch needs it and UDH/SAR reassembly does not: a UDH segment
// completes its message the moment the last segment arrives, inside Add, on the
// consumer's goroutine, so the worker carries on with the message it already
// has. A stitched group has no such moment — a message with no sibling is
// released by the window expiring, with no queue delivery in hand — so something
// has to pick it up and decide, announce, spool and receipt it.
//
// Nothing implements this yet. See NewMultipartAssembler for why that makes
// enabling the stitch an error rather than a silent hold.
type StitchDrain interface {
	Complete(ctx context.Context, msg Message) error
}

// ErrStitchDrainRequired reports a connector that enables the plain-split stitch
// without a drain wired.
var ErrStitchDrainRequired = errors.New("termination: the plain-split stitch requires a drain")

// StitchSettings is the per-connector configuration of the plain-split stitch.
//
// It is off by default and deliberately so. Unlike UDH or SAR concatenation,
// which the sender declares, this joins messages on the guess that two messages
// sharing a sender and a destination inside a short window are one message. With
// several partners on shared brand names or short codes, that guess merges two
// unrelated OTPs into one row and loses the second code. It is enabled per
// connector, against a partner known to split messages this way.
type StitchSettings struct {
	// Window is how long a message waits for a sibling. Zero disables the stitch
	// entirely; that is the default and it is what every connector should run
	// until a specific partner proves it needs otherwise.
	Window time.Duration `json:"window,omitempty"`
	// MaxChunks caps a group before it is force-released. Zero takes the Python
	// service's default of 3.
	MaxChunks int `json:"max_chunks,omitempty"`
	// MaxBuffered caps concurrent groups. Zero takes the default of 5000.
	MaxBuffered int `json:"max_buffered,omitempty"`
	// Separator is inserted between joined texts. Empty by default: a split URL
	// must not gain a space in the middle.
	Separator string `json:"separator,omitempty"`
}

// Enabled reports whether this connector stitches plain-split messages.
func (s StitchSettings) Enabled() bool { return s.Window > 0 }

// Validate reports whether the settings can be applied.
func (s StitchSettings) Validate() error {
	if s.Window < 0 {
		return fmt.Errorf("%w: stitch window cannot be negative", ErrInvalidConnectorConfig)
	}
	if !s.Enabled() {
		// The remaining fields describe a stitch that will not run. Rejecting
		// them would refuse a configuration that says "not now, and here is how
		// I want it when it is".
		return nil
	}
	if err := s.options().Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConnectorConfig, err)
	}
	return nil
}

func (s StitchSettings) options() msgcontent.StitchOptions {
	return msgcontent.StitchOptions{
		Window:      s.Window,
		MaxChunks:   s.MaxChunks,
		MaxBuffered: s.MaxBuffered,
		Separator:   s.Separator,
	}.WithDefaults()
}

// AssemblerConfig is one connector's reassembly configuration.
type AssemblerConfig struct {
	// CID is the connector id. It namespaces the part store, so two connectors
	// cannot join each other's segments.
	CID string
	// Stitch configures the plain-split layer. The zero value disables it.
	Stitch StitchSettings
}

// NewMultipartAssembler wires the assembler.
//
// drain is required when the stitch is enabled and ignored otherwise. Enabling
// the stitch without one would hold every message that has no sibling until the
// process exits — messages the partner was told nothing about, that never reach
// the spool, and that no queue redelivery brings back, because the segment was
// settled when it was buffered. Failing to start the connector is loud; the
// alternative is a connector that quietly stops delivering ordinary traffic.
func NewMultipartAssembler(
	cfg AssemblerConfig,
	parts PartStore,
	content ContentDecoder,
	drain StitchDrain,
	now func() time.Time,
) (*MultipartAssembler, error) {
	if strings.TrimSpace(cfg.CID) == "" {
		return nil, fmt.Errorf("%w: empty cid", ErrInvalidConnectorConfig)
	}
	if parts == nil {
		return nil, errors.New("termination: multipart assembler requires a part store")
	}
	if content == nil {
		return nil, errors.New("termination: multipart assembler requires a content decoder")
	}
	if err := cfg.Stitch.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	assembler := &MultipartAssembler{cid: cfg.CID, parts: parts, content: content, now: now}
	if cfg.Stitch.Enabled() {
		if drain == nil {
			return nil, fmt.Errorf("%w: connector %q sets a stitch window of %s", ErrStitchDrainRequired, cfg.CID, cfg.Stitch.Window)
		}
		buffer, err := msgcontent.NewStitchBuffer[Message](cfg.Stitch.options())
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConnectorConfig, err)
		}
		assembler.stitch = buffer
		assembler.drain = drain
	}
	return assembler, nil
}

// Segment is one part of a concatenated message: where it sits in the sequence,
// and the body bytes that belong to the whole.
type Segment struct {
	// Reference groups the segments. It is 8-bit or 16-bit depending on which
	// information element the sender used, widened here so both fit one field.
	Reference uint32
	// Total is how many segments the whole message has, Sequence which one this
	// is, counted from 1.
	Total    byte
	Sequence byte
	// Body is this segment's contribution to the joined message: the payload
	// after the User Data Header for UDH concatenation, and the whole
	// short_message for SAR, which carries its coordinates in optional
	// parameters instead of in the body.
	Body []byte
}

// Valid reports whether the coordinates can describe a real message. A segment
// claiming to be part 0 of 0, or part 4 of 3, is a broken header rather than a
// fragment, and waiting for its siblings would hold it until the TTL expired.
func (s Segment) Valid() bool {
	return s.Total > 0 && s.Sequence > 0 && s.Sequence <= s.Total
}

// SegmentFromUDH reads concatenation coordinates from a User Data Header.
//
// It goes through msgcontent.ParseUDH, which walks the whole information-element
// chain and requires it to end exactly on the declared header length. The
// strictness is what keeps an ordinary message whose first byte happens to be
// small from being mistaken for a fragment and held for siblings that do not
// exist. It also means the 16-bit reference form (IEI 0x08) is handled, which
// the hardcoded 05 00 03 prefix check on the MO path does not cover.
func SegmentFromUDH(raw []byte) (Segment, bool) {
	udh, ok := msgcontent.ParseUDH(raw)
	if !ok || !udh.HasConcat {
		return Segment{}, false
	}
	var body []byte
	if start := int(udh.Length) + 1; start < len(raw) {
		body = raw[start:]
	}
	return Segment{
		Reference: uint32(udh.ConcatRef),
		Total:     udh.ConcatTotal,
		Sequence:  udh.ConcatSeq,
		Body:      body,
	}, true
}

// SegmentFromSAR reads concatenation coordinates from the SAR optional
// parameters, the other way a sender declares a concatenated message.
//
// All three must be present: SMPP 3.4 requires them together, and a partial set
// describes nothing. The body is the whole short_message, because SAR keeps the
// coordinates out of it.
func SegmentFromSAR(optional smppwire.OptionalParameters, body []byte) (Segment, bool) {
	if optional.SARMessageReference == nil || optional.SARTotalSegments == nil || optional.SARSegmentSequence == nil {
		return Segment{}, false
	}
	return Segment{
		Reference: uint32(*optional.SARMessageReference),
		Total:     *optional.SARTotalSegments,
		Sequence:  *optional.SARSegmentSequence,
		Body:      body,
	}, true
}

// DeclaredSegment reports the concatenation coordinates a submit declares,
// resolving the two mechanisms in exactly the order MultipartAssembler.Add
// resolves them: SAR first, then a UDH in the body.
//
// It exists so the worker can tell apart the two reasons an assembler reports
// "not complete", which look identical at that interface and must not be
// handled identically:
//
//   - a segment of a message the sender declared as concatenated. It is one
//     submit_sm of several, it was answered with its own message id and it
//     carries its own registered_delivery flag, so it is owed its own receipt
//     now — see Worker.recordSegmentReceipt.
//   - a whole, undeclared message being held by the plain-split stitch, which
//     is a guess that two messages are one. That one is announced and receipted
//     when its group is released, by the drain, under the released message's id.
//
// Invalid coordinates report false, matching AddSegment: a segment claiming to
// be part 4 of 3 is a broken header rather than a fragment, and the assembler
// treats it as a whole message.
func DeclaredSegment(msg Message) (Segment, bool) {
	if msg.SAR != nil {
		return *msg.SAR, msg.SAR.Valid()
	}
	if segment, ok := SegmentFromUDH(msg.Raw); ok {
		return segment, segment.Valid()
	}
	return Segment{}, false
}

// Add joins a concatenated submit, or passes an ordinary message through.
//
// Complete is false while segments are still missing: the worker settles the
// queue delivery and produces nothing, and the segments already stored are the
// state. It is true with the joined message once the last segment arrives.
//
// A message with neither a UDH concatenation header nor SAR coordinates is
// returned exactly as it came in when the stitch is off, so a connector that
// does not opt into the stitch behaves as it did before this existed.
func (a *MultipartAssembler) Add(ctx context.Context, msg Message) (Message, bool, error) {
	// SAR first: when a submit declares concatenation in its optional parameters
	// the body carries no UDH to find, and the coordinates only reach here on the
	// message. A submit that somehow declares both is treated as SAR, matching
	// the order the decoder resolves them in.
	if msg.SAR != nil {
		return a.AddSegment(ctx, msg, *msg.SAR)
	}
	if segment, ok := SegmentFromUDH(msg.Raw); ok {
		return a.AddSegment(ctx, msg, segment)
	}
	return a.addWhole(ctx, msg)
}

// AddSegment is Add for a segment whose coordinates the caller already has.
//
// It exists for SAR: those coordinates live in the submit's optional parameters,
// which Add cannot see because Message does not carry them. A caller that has
// the decoded PDU passes SegmentFromSAR's result here; everything downstream —
// the key, the store, the joining, the decode — is the path a UDH segment takes.
func (a *MultipartAssembler) AddSegment(ctx context.Context, msg Message, segment Segment) (Message, bool, error) {
	if !segment.Valid() {
		// A nonsensical header is not a fragment. Treating it as a whole message
		// delivers something the operator can look at; holding it would wait for
		// siblings that cannot exist and lose it at the TTL.
		return a.addWhole(ctx, msg)
	}

	destination := partKeyDestination(msg.From, msg.To)
	if err := a.parts.StorePart(ctx, a.cid, segment.Reference, destination, uint32(segment.Sequence), segment.Body); err != nil {
		return Message{}, false, fmt.Errorf("termination: store segment %d/%d: %w", segment.Sequence, segment.Total, err)
	}
	stored, err := a.parts.ReadParts(ctx, a.cid, segment.Reference, destination)
	if err != nil {
		return Message{}, false, fmt.Errorf("termination: read segments of %d: %w", segment.Total, err)
	}
	if len(stored) < int(segment.Total) {
		return Message{}, false, nil
	}

	joined := make([]byte, 0, len(msg.Raw)*int(segment.Total))
	for sequence := 1; sequence <= int(segment.Total); sequence++ {
		part, present := stored[uint32(sequence)]
		if !present {
			// The count is there but a sequence is not, which means a duplicate
			// arrived before a sibling. Keep waiting rather than joining a
			// message with a hole in it.
			return Message{}, false, nil
		}
		joined = append(joined, part...)
	}

	// Dropping the segments before the message is spooled is deliberate. The
	// alternative — leaving them to expire — lets any later redelivery of any
	// segment re-complete the same message under a different queue message id,
	// which is a second accept leg, a second receipt and a second delivery to the
	// application. Deleting bounds that to the narrow window where this process
	// dies between here and the spool commit; a failure to delete is returned so
	// the delivery is redelivered and the whole step is retried, which is
	// idempotent.
	if err := a.parts.DeleteParts(ctx, a.cid, segment.Reference, destination); err != nil {
		return Message{}, false, fmt.Errorf("termination: drop joined segments of %d: %w", segment.Total, err)
	}

	assembled := msg
	assembled.Raw = joined
	assembled.Parts = int(segment.Total)
	assembled.Text, assembled.Encoding = a.decode(joined, msg.DataCoding)
	// A reassembled message does not then go through the stitch: its
	// concatenation was declared by the sender and is complete. The Python
	// service marks these _skip_stitch for the same reason.
	return assembled, true, nil
}

// addWhole handles a message that declared no concatenation.
func (a *MultipartAssembler) addWhole(ctx context.Context, msg Message) (Message, bool, error) {
	if a.stitch == nil {
		return msg, true, nil
	}
	_ = ctx
	a.stitchMu.Lock()
	defer a.stitchMu.Unlock()
	group, released := a.stitch.Ingest(
		msgcontent.StitchKey{From: msg.From, To: msg.To},
		msgcontent.StitchChunk[Message]{ID: msg.MessageID, Text: msg.Text, Raw: msg.Raw, Payload: msg},
		a.now(),
	)
	if !released {
		return Message{}, false, nil
	}
	return stitchedMessage(group), true, nil
}

// FlushExpired releases the stitch groups whose window has elapsed and hands
// each to the drain. It reports how many were released.
//
// A message with no sibling is only ever released from here, so this must run on
// a timer for as long as the stitch is enabled. Run does that; a caller with its
// own loop can call this directly.
func (a *MultipartAssembler) FlushExpired(ctx context.Context) (int, error) {
	if a.stitch == nil {
		return 0, nil
	}
	a.stitchMu.Lock()
	groups := a.stitch.FlushReady(a.now())
	a.stitchMu.Unlock()

	released := 0
	var failures []error
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return released, err
		}
		if err := a.drain.Complete(ctx, stitchedMessage(group)); err != nil {
			// The group is already out of the buffer, so it cannot be retried
			// from here. Reporting it is the only way it is not lost silently.
			failures = append(failures, fmt.Errorf("termination: drain stitched message %s: %w", group.GroupID, err))
			continue
		}
		released++
	}
	return released, errors.Join(failures...)
}

// Run flushes expired stitch groups until the context is cancelled. It returns
// immediately when the stitch is disabled, so a caller can start it
// unconditionally.
//
// The interval is a fraction of the window rather than the window itself: a
// group released one whole window late has waited twice as long as configured.
func (a *MultipartAssembler) Run(ctx context.Context, onError func(error)) {
	if a.stitch == nil {
		return
	}
	interval := a.stitch.Window() / 4
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := a.FlushExpired(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

// PendingStitchGroups is how many messages are held waiting for a sibling. It is
// the number an operator needs at shutdown: those messages have been settled on
// the queue and exist only in this process.
func (a *MultipartAssembler) PendingStitchGroups() int {
	if a.stitch == nil {
		return 0
	}
	a.stitchMu.Lock()
	defer a.stitchMu.Unlock()
	return a.stitch.Pending()
}

// stitchedMessage turns a released group into the message the rest of the path
// sees.
//
// The message id is the LAST chunk's, not the first and not the composite id the
// Python service uses. That is forced by receipt correlation: the worker
// publishes the accept leg under the queue message id of the delivery it is
// currently handling, and the receipt runner derives the SMSC id from the spool
// row's message id. If those two disagree the terminal receipt is published
// against a correlation key nothing ever wrote, and the partner silently never
// receives it. The composite id is still carried, as the group's identity, for
// the decision trail.
func stitchedMessage(group msgcontent.Stitched[Message]) Message {
	assembled := group.Payload
	if last := len(group.ChunkIDs) - 1; last >= 0 {
		assembled.MessageID = group.ChunkIDs[last]
	}
	assembled.Text = group.Text
	assembled.Raw = group.Raw
	assembled.Parts = group.Chunks
	return assembled
}

// decode reads the joined body, mirroring the worker's handling of an
// undecodable payload: the raw bytes are still recorded and still delivered,
// with the failure named in the encoding field, because a message that cannot be
// read is not a message that may be dropped.
func (a *MultipartAssembler) decode(raw []byte, dataCoding byte) (string, string) {
	text, encoding, err := a.content.Decode(raw, dataCoding)
	if err != nil {
		return "", fmt.Sprintf("undecodable: %v", err)
	}
	return text, encoding
}

// partKeyDestination builds the part store's destination component from both
// addresses.
//
// The store keys segments by (connector, reference, destination), which is
// enough for the MO path but not here: a concatenation reference is 8 or 16 bits
// chosen by the sender, so two brands messaging the same handset collide often
// enough to matter, and the collision joins one brand's segments to another's.
// Including the source is what the Python service does — it keys by (from, to,
// ref) — and folding it into this component is how that fits an interface the MO
// path also uses.
//
// Both halves are escaped because the key format forbids ':' in a component and
// a source address is whatever the partner put in the PDU. The escaping is
// injective and never emits ':' or '|', so two different address pairs cannot
// produce one key.
func partKeyDestination(from, to string) string {
	return url.QueryEscape(to) + "|" + url.QueryEscape(from)
}
