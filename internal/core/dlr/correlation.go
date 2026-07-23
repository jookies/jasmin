package dlr

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/pumpitspace/jasmin/internal/state/rediscompat"
)

// statusOK is the submit_sm_resp command_status name that means success.
const statusOK = "ESME_ROK"

// rd_receipt string forms as stored in the DLR Redis record (str(enum), confirmed by
// compat/fixtures/redis/baseline.json and the rediscompat golden test).
const (
	rdReceiptRequested           = "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED"
	rdReceiptRequestedForFailure = "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE"
)

// ErrDLRMapNotFound reports that no dlr:<msgid> record exists (Jasmin DLRMapNotFound).
// The consumer drops the message without retry, matching the legacy reject.
var ErrDLRMapNotFound = errors.New("dlr: no DLR map for msgid")

// ErrDLRMapInvalid reports a malformed DLR record — unknown sc, or an unparseable
// level/expiry (Jasmin DLRMapError). The consumer drops the message.
var ErrDLRMapInvalid = errors.New("dlr: invalid DLR map")

// ForwardTarget selects the thrower a forwarded receipt goes to.
type ForwardTarget uint8

const (
	ForwardHTTP  ForwardTarget = iota + 1 // dlr_thrower.http
	ForwardSMPPS                          // dlr_thrower.smpps
)

// Forward describes a delivery receipt to publish. The correlation engine produces it;
// the caller serializes and publishes it to the messaging exchange (a later wiring
// stage). It mirrors the constructor args of Jasmin's DLRContentForHttpapi /
// DLRContentForSmpps.
type Forward struct {
	Target     ForwardTarget
	Status     string // dlr_status (command_status name or receipt state)
	QueueMsgID string
	// HTTP fields.
	Level     int // the actual receipt level; always 1 on the submit_sm_resp leg
	URL       string
	Method    string
	Connector string
	// SMPPs fields.
	SystemID        string
	SourceAddr      string
	DestinationAddr string
	SubDate         string
	SourceAddrTON   string
	SourceAddrNPI   string
	DestAddrTON     string
	DestAddrNPI     string
}

// Publisher publishes a forwarded receipt. It is defined here (consumer side) so the
// engine stays testable; the production implementation publishes to RabbitMQ.
type Publisher interface {
	PublishDLR(ctx context.Context, forward Forward) error
}

// Config carries the DLR settings that affect correlation.
type Config struct {
	// SMPPReceiptOnSuccessSubmitSmResp mirrors [dlr] smpp_receipt_on_success_submit_sm_resp
	// (default false): whether to forward an on-success receipt to smpps users at the
	// submit_sm_resp stage.
	SMPPReceiptOnSuccessSubmitSmResp bool
}

// SubmitRespEvent is a submit_sm_resp arriving for DLR correlation.
type SubmitRespEvent struct {
	QueueMsgID string // the Jasmin queue message id (dlr:<QueueMsgID>)
	SMPPMsgID  string // the SMSC message_id from the resp (canonicalized before keying)
	Status     string // command_status name; statusOK on success
}

// Correlator implements the DLR correlation legs over the compat Redis client. This
// stage covers the submit_sm_resp leg for both the httpapi and smppsapi sc paths; the
// deliver_sm (terminal receipt) leg is the following stage.
type Correlator struct {
	redis     *rediscompat.Client
	publisher Publisher
	cfg       Config
}

// NewCorrelator wires the engine. The publisher is invoked in Jasmin's order (publish
// before mutating Redis) so failure semantics match the legacy callback.
func NewCorrelator(redis *rediscompat.Client, publisher Publisher, cfg Config) *Correlator {
	return &Correlator{redis: redis, publisher: publisher, cfg: cfg}
}

// OnSubmitResp handles the submit_sm_resp leg (jasmin managers/dlr.py
// submit_sm_resp_dlr_callback). It reads the pending DLR request, forwards the
// SMSC-level (level 1) receipt when requested, deletes the request on terminal
// conditions, and installs the queue-msgid mapping the terminal receipt will use.
//
// Returned errors classify the outcome for the consumer, matching Jasmin's except
// clauses: ErrDLRMapNotFound and ErrDLRMapInvalid → drop; a wrapped redis error → retry
// per policy; a publisher error → propagate.
func (c *Correlator) OnSubmitResp(ctx context.Context, ev SubmitRespEvent) error {
	dlrKey, err := rediscompat.BuildDLRKey(ev.QueueMsgID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDLRMapInvalid, err)
	}
	fields, err := c.redis.ReadHash(ctx, dlrKey)
	if err != nil {
		if errors.Is(err, rediscompat.ErrKeyNotFound) {
			return fmt.Errorf("%w: %s", ErrDLRMapNotFound, ev.QueueMsgID)
		}
		return err // redis/connection error → consumer requeues per retry policy
	}

	switch fields["sc"] {
	case "httpapi":
		return c.onSubmitRespHTTP(ctx, ev, dlrKey, fields)
	case "smppsapi":
		return c.onSubmitRespSMPPS(ctx, ev, fields)
	default:
		return fmt.Errorf("%w: unknown sc %q", ErrDLRMapInvalid, fields["sc"])
	}
}

func (c *Correlator) onSubmitRespHTTP(ctx context.Context, ev SubmitRespEvent, dlrKey rediscompat.Key, fields map[string]string) error {
	level, err := strconv.Atoi(fields["level"])
	if err != nil {
		return fmt.Errorf("%w: level %q", ErrDLRMapInvalid, fields["level"])
	}

	// Levels 1 and 3 want the SMSC-level receipt now; the forward always carries level 1
	// (the actual receipt level), never the requested level.
	if level == 1 || level == 3 {
		connector := fields["connector"]
		if connector == "" {
			connector = "unknown" // dlr.get('connector', 'unknown')
		}
		forward := Forward{
			Target: ForwardHTTP, Status: ev.Status, QueueMsgID: ev.QueueMsgID, Level: 1,
			URL: fields["url"], Method: fields["method"], Connector: connector,
		}
		if err := c.publisher.PublishDLR(ctx, forward); err != nil {
			return err
		}
		// Remove the request when the SMSC level is all that was asked (level 1), or the
		// submit failed (no terminal receipt will follow).
		if level == 1 || ev.Status != statusOK {
			if err := c.redis.Delete(ctx, dlrKey); err != nil {
				return err
			}
		}
	}

	// Levels 2 and 3 need the terminal receipt later; install the SMSC-id → queue-id map.
	if (level == 2 || level == 3) && ev.Status == statusOK {
		if err := c.writeMapping(ctx, ev.SMPPMsgID, ev.QueueMsgID, "httpapi", fields["expiry"]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Correlator) onSubmitRespSMPPS(ctx context.Context, ev SubmitRespEvent, fields map[string]string) error {
	rd := fields["rd_receipt"]
	ok := ev.Status == statusOK
	forward := (ok && (rd == rdReceiptRequested || rd == rdReceiptRequestedForFailure)) ||
		(!ok && rd == rdReceiptRequestedForFailure)
	if !forward {
		return nil
	}

	// Send the receipt now only on failure, or on success when explicitly configured.
	if !ok || c.cfg.SMPPReceiptOnSuccessSubmitSmResp {
		f := Forward{
			Target: ForwardSMPPS, Status: ev.Status, QueueMsgID: ev.QueueMsgID,
			SystemID: fields["system_id"], SourceAddr: fields["source_addr"],
			DestinationAddr: fields["destination_addr"], SubDate: fields["sub_date"],
			SourceAddrTON: fields["source_addr_ton"], SourceAddrNPI: fields["source_addr_npi"],
			DestAddrTON: fields["dest_addr_ton"], DestAddrNPI: fields["dest_addr_npi"],
		}
		if err := c.publisher.PublishDLR(ctx, f); err != nil {
			return err
		}
	}
	if ok {
		if err := c.writeMapping(ctx, ev.SMPPMsgID, ev.QueueMsgID, "smppsapi", fields["expiry"]); err != nil {
			return err
		}
	}
	return nil
}

// writeMapping installs queue-msgid:<canonical smpp id> → {msgid, connector_type} with
// the DLR record's expiry, so the terminal deliver_sm receipt can find its queue msgid.
func (c *Correlator) writeMapping(ctx context.Context, smppMsgID, queueMsgID, connectorType, expiryField string) error {
	expiry, err := strconv.ParseInt(expiryField, 10, 64)
	if err != nil || expiry <= 0 {
		return fmt.Errorf("%w: expiry %q", ErrDLRMapInvalid, expiryField)
	}
	qkey, err := rediscompat.BuildQueueMessageKey(CanonicalizeSubmitRespID(smppMsgID))
	if err != nil {
		return fmt.Errorf("%w: mapping key: %v", ErrDLRMapInvalid, err)
	}
	rec, err := rediscompat.NewQueueMessageCorrelation(qkey, rediscompat.QueueMessageCorrelation{
		MessageID: queueMsgID, ConnectorType: connectorType, TTLSeconds: expiry,
	})
	if err != nil {
		return fmt.Errorf("%w: mapping record: %v", ErrDLRMapInvalid, err)
	}
	return c.redis.WriteHashRecord(ctx, rec)
}
