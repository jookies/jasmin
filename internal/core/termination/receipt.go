package termination

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/dlr"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

// messagingExchange is the exchange every DLR leg is published to
// (amqpcompat.NewTopology declares it).
const messagingExchange = "messaging"

// receiptDateLayout renders Python strftime "%y%m%d%H%M", the submit/done date
// format inside a receipt's text field. It duplicates the unexported constant in
// package dlr on purpose: a receipt whose dates are formatted differently from
// the correlation engine's would parse back as a different message.
const receiptDateLayout = "0601021504"

// receiptSubCount is the sub field every synthesized receipt carries. One
// submit, one message: the legacy fake SMSC hardcodes sub:001 and so does every
// real receipt this platform has ever forwarded.
const receiptSubCount = "001"

// EnvelopePublisher publishes an AMQP envelope. Declared here, at the consumer,
// so the receipt path can be tested without a broker;
// *amqpcompat.Publisher satisfies it.
type EnvelopePublisher interface {
	Publish(ctx context.Context, exchange string, routingKey string, msg amqpcompat.Envelope) error
}

// SMSCLeg emits the two events a real upstream SMSC would have caused, for a
// message this platform terminates itself.
//
// Nothing downstream is told the SMSC is synthetic. The submit_sm_resp leg is
// what installs the smpp_msgid → queue msgid mapping that receipt correlation
// depends on, drives the CDR to SMSC_ACCEPTED and fires level-1 callbacks; the
// deliver leg is what produces the partner's terminal receipt. Emitting both
// through the legacy content constructors (smppc.NewDLRSubmitRespPublication and
// smppc.NewDLRDeliverPublication) is what makes the partner-visible bytes
// identical to a carrier's, rather than similar to them.
type SMSCLeg struct {
	publisher EnvelopePublisher
	cid       string
	now       func() time.Time
}

// NewSMSCLeg wires the leg. cid is the termination connector id, which travels
// in the deliver publication exactly as an SMPP client connector's cid does.
func NewSMSCLeg(publisher EnvelopePublisher, cid string, now func() time.Time) (*SMSCLeg, error) {
	if publisher == nil {
		return nil, fmt.Errorf("termination: nil envelope publisher")
	}
	if strings.TrimSpace(cid) == "" {
		return nil, fmt.Errorf("termination: empty connector id")
	}
	if now == nil {
		now = time.Now
	}
	return &SMSCLeg{publisher: publisher, cid: cid, now: now}, nil
}

// Accept emits the successful submit_sm_resp leg for a terminated message.
//
// smscMessageID is this platform's own id for the message, standing in for the
// one a carrier would have returned. It must be stable for the life of the
// message: it is the key the terminal receipt is correlated by, so minting a
// second one later orphans the receipt.
func (l *SMSCLeg) Accept(ctx context.Context, queueMsgID, smscMessageID string) error {
	envelope, err := smppc.NewDLRSubmitRespPublication(queueMsgID, "ESME_ROK", smscMessageID)
	if err != nil {
		return fmt.Errorf("termination: submit_sm_resp leg: %w", err)
	}
	if err := l.publisher.Publish(ctx, messagingExchange, envelope.RoutingKey(), envelope); err != nil {
		return fmt.Errorf("termination: publish submit_sm_resp leg: %w", err)
	}
	return nil
}

// Receipt builds the terminal receipt for a verdict. submittedAt is the original
// submission time and doneAt the moment the verdict became final, so the text
// carries a real interval rather than two identical stamps.
//
// The dlvrd and err fields are derived from the verdict, never fixed: both Go
// emulators in this repo hardcode "dlvrd:001 err:000" and emit it alongside
// stat:UNDELIV, which claims one message was delivered by a receipt that says
// none was. A partner parsing the counters instead of stat sees a contradiction.
func (l *SMSCLeg) Receipt(smscMessageID string, v Verdict, submittedAt, doneAt time.Time) dlr.Receipt {
	return dlr.Receipt{
		ID:    smscMessageID,
		Stat:  v.Stat,
		Sub:   receiptSubCount,
		Dlvrd: fmt.Sprintf("%03d", v.Delivered()),
		SDate: submittedAt.Format(receiptDateLayout),
		DDate: doneAt.Format(receiptDateLayout),
		Err:   v.Err,
		// Text is the first 20 characters of the original message in a carrier
		// receipt. It stays empty here: this is OTP content, and a receipt
		// travels back to the partner that already knows what it sent, so
		// including it would leak the code into logs on both sides for no gain.
		Text: "",
	}
}

// Deliver emits the terminal receipt leg. The coded id is the SMSC message id,
// matching how a carrier receipt is correlated.
func (l *SMSCLeg) Deliver(ctx context.Context, smscMessageID string, receipt dlr.Receipt) error {
	envelope, err := smppc.NewDLRDeliverPublication(smscMessageID, "deliver_sm", l.cid, receipt)
	if err != nil {
		return fmt.Errorf("termination: deliver receipt leg: %w", err)
	}
	if err := l.publisher.Publish(ctx, messagingExchange, envelope.RoutingKey(), envelope); err != nil {
		return fmt.Errorf("termination: publish deliver receipt leg: %w", err)
	}
	return nil
}

// ReceiptDelay computes when a verdict's receipt is due.
//
// The delay is deliberate parity, not latency the implementation failed to
// remove: the legacy fake SMSC holds the receipt 5 s plus up to 2 s of jitter so
// it looks like a carrier delivering to a handset, and a partner's monitoring
// can flag an instantly-returned receipt as synthetic. jitterFraction is a value
// in [0,1) supplied by the caller — the connector draws it once per message —
// keeping this function deterministic and testable.
func ReceiptDelay(delay, jitter time.Duration, jitterFraction float64) time.Duration {
	if delay < 0 {
		delay = 0
	}
	if jitter <= 0 || jitterFraction <= 0 {
		return delay
	}
	if jitterFraction >= 1 {
		jitterFraction = 1
	}
	return delay + time.Duration(float64(jitter)*jitterFraction)
}
