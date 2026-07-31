package termination

import (
	"context"
	"errors"
	"fmt"

	"github.com/pumpitspace/synevyr/internal/core/msgcontent"
)

// MsgContentDecoder adapts internal/core/msgcontent to the worker's
// ContentDecoder.
//
// The adapter exists so this package does not depend on the codec table's
// option struct: which codecs are tried, and in what order, is the decoder's
// business, and the ordering there is load-bearing (8-bit codecs must run before
// charset detection or Cyrillic restoration breaks).
type MsgContentDecoder struct {
	opts msgcontent.Options
}

// NewMsgContentDecoder wires the decoder with the given options. The zero
// Options is the legacy-parity configuration, which the differential harness
// needs and production does not want — see DefaultDecodeOptions.
func NewMsgContentDecoder(opts msgcontent.Options) MsgContentDecoder {
	return MsgContentDecoder{opts: opts}
}

// DefaultDecodeOptions is what a termination connector decodes with.
//
// It turns on PreserveOTPDigits, which is the one place this connector
// deliberately does not reproduce the legacy behaviour: the Python decoder
// rewrites an OTP code's digits as Cyrillic letters, and shipping that
// faithfully would mean knowingly corrupting the payload this platform exists to
// carry. Every other difference from the legacy decoder is a bug in this port.
//
// The differential harness must use msgcontent.Options{} instead, so the
// comparison stays byte-exact and this divergence shows up as the single
// intended one.
func DefaultDecodeOptions() msgcontent.Options {
	return msgcontent.Options{PreserveOTPDigits: true}
}

// NewDefaultMsgContentDecoder wires the decoder production uses.
func NewDefaultMsgContentDecoder() MsgContentDecoder {
	return NewMsgContentDecoder(DefaultDecodeOptions())
}

// Decode returns the decoded text and the codec that produced it.
func (d MsgContentDecoder) Decode(raw []byte, dataCoding byte) (string, string, error) {
	return msgcontent.DecodeWithOptions(raw, dataCoding, d.opts)
}

// ErrMultipartUnsupported reports a concatenated segment reaching an assembler
// that cannot join it.
//
// It is terminal: redelivering the same segment produces the same answer, so a
// caller must dead-letter it rather than requeue it forever. See IsTerminal.
var ErrMultipartUnsupported = errors.New("termination: multipart reassembly is not enabled on this connector")

// PassThroughAssembler treats every message as arriving whole, and refuses one
// that plainly is not.
//
// It is the first-phase Assembler: UDH/SAR reassembly and the plain-split stitch
// land behind the same interface, and msgcontent.ParseUDH already exists for
// them, so nothing here needs a second UDH parser.
//
// The refusal matters more than it looks. Each segment of a concatenated submit
// is enqueued as its own queue message, so a pass-through would produce one
// content-bearing spool row and one downstream delivery per fragment — the
// application would receive half a message as if it were whole, possibly split
// mid-rune in UCS-2, and store it as the message. Silently.
//
// Note that the several accept legs and several receipts a pass-through also
// produces are not the problem: a segment really is its own submit_sm and really
// is owed its own receipt, which is what MultipartAssembler keeps doing while it
// joins the content. What a pass-through gets wrong is the content, and content
// is the part nothing downstream can repair. Refusing is worse for availability
// and much better for truth, and it is visible: the message is dead-lettered
// where an operator can see it.
//
// Until the stitch lands, a termination connector must not be given routes that
// carry concatenated traffic.
type PassThroughAssembler struct{}

// Add returns the message unchanged and complete, or refuses a segment of a
// concatenated message.
func (PassThroughAssembler) Add(_ context.Context, msg Message) (Message, bool, error) {
	if udh, ok := msgcontent.ParseUDH(msg.Raw); ok && udh.HasConcat {
		return Message{}, false, fmt.Errorf("%w: segment %d of %d", ErrMultipartUnsupported, udh.ConcatSeq, udh.ConcatTotal)
	}
	return msg, true, nil
}

// IsTerminal reports whether an error will produce the same result on every
// retry, so the caller must settle the message instead of requeueing it.
//
// A queue consumer that requeues everything turns one unprocessable message into
// a hot loop that starves the connector. The distinction has to live here,
// because only this package knows which of its failures are about the message
// rather than about the moment.
func IsTerminal(err error) bool {
	return errors.Is(err, ErrMultipartUnsupported) || errors.Is(err, ErrInvalidConnectorConfig)
}
