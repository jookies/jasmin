package termination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// SubmitDecoder decodes one queued submit body. It is the same seam the SMPP
// client connector uses (smppc.SubmitDecoder), so the termination connector
// consumes the identical queue payload with the identical codec — native pickle
// today, whatever replaces it later.
type SubmitDecoder interface {
	DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error)
}

// ContentDecoder turns submitted bytes into text. Implemented by
// internal/core/msgcontent; declared here so the worker can be tested with a
// stub and so the codec table stays out of this package.
type ContentDecoder interface {
	// Decode returns the decoded text and the name of the codec that actually
	// produced it, which is not always what dataCoding claimed.
	Decode(raw []byte, dataCoding byte) (text string, encoding string, err error)
}

// Assembler joins multipart submissions. In the first phase a message arrives
// whole and the pass-through implementation is used; UDH/SAR reassembly and the
// plain-split stitch land behind this same interface.
//
// Complete reports false when more parts are still expected, in which case the
// worker settles the queue delivery without producing a message — the parts
// already held are the state.
type Assembler interface {
	Add(ctx context.Context, msg Message) (assembled Message, complete bool, err error)
}

// SpoolWriter persists the assembled message, its verdict and the receipt that
// is owed. Implemented by internal/core/msgspool.
//
// Neither method may return before its row is committed: an ack without a
// committed row is a message that existed, was charged, was promised a receipt,
// and cannot be recovered.
type SpoolWriter interface {
	// Record persists an assembled message: content, verdict and receipt.
	Record(ctx context.Context, msg Message, verdict Verdict, receiptDueAt time.Time) error
	// RecordReceiptOnly persists the receipt a segment is owed, with no content
	// and no downstream delivery. See Spool.RecordReceiptOnly for why the two are
	// separate rather than one call with a flag.
	RecordReceiptOnly(ctx context.Context, msg Message, verdict Verdict, receiptDueAt time.Time) error
}

// SubmitMetadata is what the queue envelope says about a message, beyond the
// encoded PDU in its body.
//
// It exists because the PDU does not carry the submitting user: the front door
// knows who sent the message and puts it in the envelope's user-id header
// (internal/app/outbound/submit_envelope_builder.go:132), and the decoded
// SubmitSMBody has no field for it. Without this, every spool row and every
// downstream payload reports an empty partner — and per-partner attribution is
// the reason for absorbing a single-tenant Python service in the first place.
type SubmitMetadata struct {
	// QueueMessageID is the envelope's message-id: the gateway message id, and
	// the value the receipt and every delivery retry carry.
	QueueMessageID string
	// Partner is the submitting user from the envelope's user-id header. It may
	// legitimately be empty for a message published by something that did not
	// set it; an empty value is recorded as empty rather than guessed at.
	Partner string
	// BillID and LateBillAmount are the submit envelope's billing headers, the
	// same two the SMPP client reads to raise a late-billing intent on a carrier
	// acceptance. They are carried here so a terminating connector can settle
	// the deferred half of a split-billed part instead of leaving it quoted
	// forever. Both empty is the ordinary case: nothing is deferred.
	BillID         string
	LateBillAmount string
}

// SubmitMetadataFromEnvelope reads the metadata a consumer needs from a queue
// envelope, so the two places that build it cannot disagree about which header
// carries what.
func SubmitMetadataFromEnvelope(properties amqpcompat.Properties) SubmitMetadata {
	meta := SubmitMetadata{QueueMessageID: properties.MessageID()}
	headers := properties.Headers()
	for header, target := range map[string]*string{
		submitUserIDHeader:         &meta.Partner,
		submitBillIDHeader:         &meta.BillID,
		submitLateBillAmountHeader: &meta.LateBillAmount,
	} {
		if field, ok := headers[header]; ok {
			if value, ok := field.String(); ok {
				*target = value
			}
		}
	}
	return meta
}

// submitUserIDHeader is the envelope header the front door writes the submitting
// user into; the other two carry the billing identity of a split-billed part.
const (
	submitUserIDHeader         = "user-id"
	submitBillIDHeader         = "bill-id"
	submitLateBillAmountHeader = "late-bill-amount"
)

// WorkerConfig is the per-connector configuration of a termination connector.
type WorkerConfig struct {
	// CID is the connector id. It names the submit queue this worker consumes
	// and travels in every published DLR leg.
	CID string
	// ReceiptDelay and ReceiptJitter hold the receipt back so it resembles a
	// carrier delivering to a handset. Defaults 5s/2s match the legacy fake
	// SMSC; see ReceiptDelay for why this is parity rather than latency.
	ReceiptDelay  time.Duration
	ReceiptJitter time.Duration
	// InlineVerdict takes the verdict from the delivery response instead of the
	// verdict source. Off in the first phase: the legacy path never asked the
	// downstream application for a status.
	InlineVerdict bool
}

// Worker processes one message at a time from a termination connector's submit
// queue. It is safe for concurrent use across goroutines consuming the same
// queue: all mutable state lives in the spool.
type Worker struct {
	cfg       WorkerConfig
	submits   SubmitDecoder
	content   ContentDecoder
	assembler Assembler
	verdicts  VerdictSource
	spool     SpoolWriter
	smsc      *SMSCLeg
	now       func() time.Time
	// jitter returns a value in [0,1). Injected so a test gets a deterministic
	// receipt time without stubbing the clock as well.
	jitter func() float64
}

// NewWorker wires a termination worker. Every dependency is required: a nil one
// would mean silently skipping a step that the partner or the downstream
// application is entitled to.
func NewWorker(
	cfg WorkerConfig,
	submits SubmitDecoder,
	content ContentDecoder,
	assembler Assembler,
	verdicts VerdictSource,
	spool SpoolWriter,
	smsc *SMSCLeg,
	now func() time.Time,
	jitter func() float64,
) (*Worker, error) {
	if strings.TrimSpace(cfg.CID) == "" {
		return nil, errors.New("termination: empty connector id")
	}
	if submits == nil || content == nil || assembler == nil || verdicts == nil || spool == nil || smsc == nil {
		return nil, errors.New("termination: worker requires submit decoder, content decoder, assembler, verdict source, spool and smsc leg")
	}
	if now == nil {
		now = time.Now
	}
	if jitter == nil {
		jitter = defaultJitter
	}
	return &Worker{
		cfg:       cfg,
		submits:   submits,
		content:   content,
		assembler: assembler,
		verdicts:  verdicts,
		spool:     spool,
		smsc:      smsc,
		now:       now,
		jitter:    jitter,
	}, nil
}

// Process handles one queued submit.
//
// Order matters and is not arbitrary:
//
//  1. decode and assemble;
//  2. decide the verdict — before anything is published, so a gate failure
//     cannot leave a half-announced message;
//  3. publish the accept leg with a message id derived from the queue id, so a
//     redelivery after a crash reproduces byte-identical events instead of
//     minting a second id and orphaning the receipt;
//  4. record in the spool, including when the receipt is due;
//  5. only then may the caller acknowledge the queue delivery.
//
// Delivery to the downstream application and emission of the receipt are driven
// from the spool, not from here: both must survive this process dying.
//
// # Receipts belong to segments, content belongs to the message
//
// A queued submit that turns out to be one segment of a concatenated message
// still runs steps 2 to 4. It was its own submit_sm — answered with its own
// message id, carrying its own registered_delivery flag — and a delivery receipt
// references a message id, so a partner that registers delivery on every segment
// is asking for N receipts and the legacy fake SMSC sends them N. What the
// segment does not get is a content row or a downstream delivery: those belong
// to the assembled message and happen once, on the segment that completes the
// group.
func (w *Worker) Process(ctx context.Context, meta SubmitMetadata, body []byte) error {
	queueMsgID := meta.QueueMessageID
	if strings.TrimSpace(queueMsgID) == "" {
		return errors.New("termination: empty queue message id")
	}
	submit, _, err := w.submits.DecodeSubmitSM(ctx, body)
	if err != nil {
		return fmt.Errorf("termination: decode submit: %w", err)
	}

	text, encoding, err := w.content.Decode(submit.ShortMessage, submit.DataCoding)
	if err != nil {
		// An undecodable payload is not a reason to lose the message: the raw
		// bytes are still recorded and still delivered, with the decode failure
		// named in the encoding field. The legacy service hex-dumps and
		// dead-letters these; here the message survives and stays inspectable.
		text, encoding = "", fmt.Sprintf("undecodable: %v", err)
	}

	msg := Message{
		MessageID:      queueMsgID,
		BillID:         meta.BillID,
		LateBillAmount: meta.LateBillAmount,
		Connector:      w.cfg.CID,
		Partner:        meta.Partner,
		From:           string(submit.SourceAddress),
		To:         string(submit.DestinationAddress),
		Text:       text,
		Raw:        append([]byte(nil), submit.ShortMessage...),
		DataCoding: submit.DataCoding,
		Encoding:   encoding,
		Parts:      1,
		ReceivedAt: w.now(),
	}
	if normalized, normErr := NormalizeDestination(msg.To); normErr == nil {
		msg.To = normalized
	}
	// Concatenation declared out of band. A UDH is inline in the body and the
	// assembler finds it there; SAR lives in the optional parameters, which only
	// this function has, so it has to be carried across explicitly. Without it a
	// SAR-split submit is treated as one whole message per fragment: the
	// application receives half a message as if it were the whole one.
	if segment, ok := SegmentFromSAR(submit.Optional, submit.ShortMessage); ok {
		msg.SAR = &segment
	}

	assembled, complete, err := w.assembler.Add(ctx, msg)
	if err != nil {
		return fmt.Errorf("termination: assemble: %w", err)
	}
	if complete {
		return w.Complete(ctx, assembled)
	}
	if _, declared := DeclaredSegment(msg); declared {
		// A segment of a message the sender declared as concatenated. Its
		// siblings have not all arrived, so there is no content to spool and
		// nothing to deliver — but this submit_sm exists, was accepted, and is
		// owed a receipt of its own.
		return w.recordSegmentReceipt(ctx, msg)
	}
	// Not a declared segment, so the assembler is holding a whole message in the
	// plain-split stitch: a guess that this message and a later one are halves of
	// one. It is announced, spooled and receipted when its group is released,
	// under the released message's id (see stitchedMessage and StitchDrain), so
	// doing any of it here would announce the same id twice.
	//
	// That path still emits one receipt per released group rather than one per
	// submit_sm. It is the same shape of gap this function's segment branch
	// closes, on a heuristic that is off by default and enabled per connector
	// against a partner known to split messages; closing it means giving a
	// released group an identity of its own, which is a change to what a stitched
	// message *is*, not to how a fragment is receipted.
	return nil
}

// recordSegmentReceipt runs the decide-announce-spool tail for one segment of a
// concatenated submit that has not completed its group.
//
// It is deliberately the same three steps in the same order as Complete, minus
// the content: the verdict is decided before anything is published so a gate
// failure cannot leave a half-announced segment, the accept leg carries an id
// derived from this segment's own queue message id so a redelivery reproduces
// it byte for byte, and the receipt is owed by a committed row rather than by
// anything held in this process.
//
// The gate is not consulted once per segment in practice. The verdict source
// caches by normalized destination (see windowCache), and every segment of one
// message shares a destination, so a three-segment message is one gate lookup —
// which is also what makes the three receipts agree with each other, since an
// activation window that closed mid-message would otherwise produce DELIVRD for
// the first segments and REJECTD for the last.
func (w *Worker) recordSegmentReceipt(ctx context.Context, msg Message) error {
	verdict, err := w.verdicts.Decide(ctx, msg)
	if err != nil {
		return fmt.Errorf("termination: verdict: %w", err)
	}
	recordVerdict(w.cfg.CID, w.verdicts.Name(), verdict)

	if err := w.smsc.Accept(ctx, msg.MessageID, SMSCMessageID(msg.MessageID)); err != nil {
		return err
	}

	receiptDueAt := w.now().Add(ReceiptDelay(w.cfg.ReceiptDelay, w.cfg.ReceiptJitter, w.jitter()))
	if err := w.spool.RecordReceiptOnly(ctx, msg, verdict, receiptDueAt); err != nil {
		return fmt.Errorf("termination: spool: %w", err)
	}
	return nil
}

// Complete runs the decide-announce-spool tail for an assembled message.
//
// It is separate from Process because not every completed message arrives on a
// queue delivery. A UDH or SAR group completes inside Process, on the consumer's
// goroutine, when its last segment lands. A plain-split group has no such moment:
// it is released when its window expires, with no delivery in hand, so the
// assembler calls this directly (see StitchDrain). Both paths must decide,
// announce, spool and receipt identically — which they do by being the same
// function rather than two that merely look alike.
func (w *Worker) Complete(ctx context.Context, msg Message) error {
	verdict, err := w.verdicts.Decide(ctx, msg)
	if err != nil {
		return fmt.Errorf("termination: verdict: %w", err)
	}
	// Counted before anything is published, so a verdict that was decided is
	// visible even if the accept leg then fails and the message is redelivered.
	// The redelivery re-counts it, which is correct: the gate really was
	// consulted twice, and a metric that hid that would hide a redelivery storm.
	recordVerdict(w.cfg.CID, w.verdicts.Name(), verdict)

	smscID := SMSCMessageID(msg.MessageID)
	if err := w.smsc.Accept(ctx, msg.MessageID, smscID); err != nil {
		return err
	}

	receiptDueAt := w.now().Add(ReceiptDelay(w.cfg.ReceiptDelay, w.cfg.ReceiptJitter, w.jitter()))
	if err := w.spool.Record(ctx, msg, verdict, receiptDueAt); err != nil {
		return fmt.Errorf("termination: spool: %w", err)
	}
	return nil
}

// SMSCMessageID derives this platform's stand-in for the id a carrier would have
// returned.
//
// It is a pure function of the queue message id, which is what makes the whole
// path idempotent under redelivery: a worker that crashes after publishing the
// accept leg but before committing the spool row will, on redelivery, publish
// exactly the same leg with exactly the same id. Minting a random id instead
// would leave the first receipt correlated to an id nothing will ever reference.
//
// The id is returned already canonical — uppercase, no leading zeros — because
// the two legs canonicalize differently and a mismatch silently loses receipts.
// The accept leg's publisher applies smpp_msgid.upper().lstrip('0') before the
// correlator writes the queue-msgid mapping
// (smppc.NewDLRSubmitRespPublication), while the deliver leg publishes the coded
// id verbatim and OnDeliverReceipt looks it up as given. A sha256 hex id starts
// with '0' about one time in sixteen, so a raw id would have made roughly 6% of
// terminal receipts unroutable — DLRMapNotFound, no receipt, no error the
// partner or the operator would ever see. Producing an id that is a fixed point
// of that normalization removes the class of bug rather than adding a second
// place that has to remember to strip.
func SMSCMessageID(queueMsgID string) string {
	sum := sha256.Sum256([]byte(queueMsgID))
	id := strings.TrimLeft(strings.ToUpper(hex.EncodeToString(sum[:8])), "0")
	if id == "" {
		// Every byte was zero. Astronomically unlikely, but an empty message id
		// is rejected by the publisher, so the message would fail rather than
		// mis-correlate: pick a value that is still a fixed point of the
		// normalization.
		return "1"
	}
	return id
}

// defaultJitter spreads receipts across the configured jitter window. It is
// deliberately not cryptographic: this shapes timing, it does not protect
// anything.
func defaultJitter() float64 {
	return float64(time.Now().UnixNano()%1000) / 1000.0
}
