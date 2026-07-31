package dlr

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/state/rediscompat"
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

// ErrForwardPublish wraps a failure to publish a correlated forward. The legacy
// callbacks treat publish failures as generic exceptions — reject, never retried.
var ErrForwardPublish = errors.New("dlr: forward publish failed")

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
	Err        string // pdu_dlr_err (deliver leg; empty on the submit_sm_resp leg)
	// ErrIsInteger records the legacy smpps err header kind: the submit_sm_resp
	// leg carries the constructor's integer default (99) while the deliver leg
	// forwards the receipt's err string. Decode preserves the wire kind so an
	// encode round trip is exact.
	ErrIsInteger bool

	// HTTP fields.
	Level     int // actual receipt level: 1 on the submit_sm_resp leg, 2 on the deliver_sm leg
	URL       string
	Method    string
	Connector string // resp leg: the DLR connector; deliver leg: the raw SMSC receipt id (Jasmin quirk)
	// HTTP deliver-leg (level 2) receipt fields.
	IDSMSC     string // id_smsc: the coded SMSC msgid
	Sub        string
	Dlvrd      string
	SubmitDate string // subdate = pdu_dlr_sdate
	DoneDate   string // donedate = pdu_dlr_ddate
	Text       string

	// SMPPs fields.
	SystemID        string
	SourceAddr      string
	DestinationAddr string
	SubDate         string // stored sub_date from the DLR record
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
	cdr       cdr.FinalDLRRecorder
	now       func() time.Time
}

type CorrelatorOption func(*Correlator)

// WithFinalDLRRecorder makes final commercial settlement part of the
// correlation transaction boundary. The recorder runs before any external
// callback forward and before the Redis request is deleted, so a database
// failure remains retryable without duplicating the customer's callback.
func WithFinalDLRRecorder(recorder cdr.FinalDLRRecorder, now func() time.Time) CorrelatorOption {
	return func(correlator *Correlator) {
		correlator.cdr = recorder
		if now != nil {
			correlator.now = now
		}
	}
}

// NewCorrelator wires the engine. The publisher is invoked in Jasmin's order (publish
// before mutating Redis) so failure semantics match the legacy callback.
func NewCorrelator(redis *rediscompat.Client, publisher Publisher, cfg Config, options ...CorrelatorOption) *Correlator {
	correlator := &Correlator{redis: redis, publisher: publisher, cfg: cfg, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(correlator)
		}
	}
	return correlator
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
		publishErr := c.publisher.PublishDLR(ctx, forward)
		// The forward carries level 1 (the actual receipt level), so that is what
		// is counted; the requested level is already visible as the level-2/3
		// samples this same message produces later.
		recordForward(1, ev.Status, publishErr)
		if publishErr != nil {
			return fmt.Errorf("%w: %v", ErrForwardPublish, publishErr)
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
		publishErr := c.publisher.PublishDLR(ctx, f)
		recordForward(1, ev.Status, publishErr)
		if publishErr != nil {
			return fmt.Errorf("%w: %v", ErrForwardPublish, publishErr)
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

// DeliverReceiptEvent is a deliver_sm delivery receipt arriving for correlation.
type DeliverReceiptEvent struct {
	RawDLRID string    // pdu_dlr_id: the raw SMSC receipt id (used as the http dlr_connector)
	Base     MsgIDBase // connector dlr_msg_id_bases, to code RawDLRID into the lookup key
	// CodedID, when set, is the already-coded lookup id: the legacy dlr.deliver_sm
	// envelope carries it as message-id, coded by the publishing listener, so the
	// AMQP consumer path supplies it directly and Base is not consulted.
	CodedID     string
	ConnectorID string // cid (context/logging)
	Status      string // receipt state: DELIVRD, EXPIRED, ...
	Sub         string
	Dlvrd       string
	SubmitDate  string // pdu_dlr_sdate
	DoneDate    string // pdu_dlr_ddate
	Err         string
	Text        string
}

// OnDeliverReceipt handles the deliver_sm terminal leg (jasmin managers/dlr.py
// deliver_sm_dlr_callback). It codes the receipt id into the correlation key, resolves
// queue-msgid → the submit queue id and its DLR record, forwards the level-2 (terminal)
// receipt when requested, and deletes the DLR record on a final state.
//
// Error policy differs from OnSubmitResp: here ErrDLRMapNotFound is RETRYABLE — it covers
// the race where the terminal receipt arrives before the submit_sm_resp leg wrote the
// mapping. The caller applies retry-vs-drop per leg, matching Jasmin's per-callback
// except clauses.
func (c *Correlator) OnDeliverReceipt(ctx context.Context, ev DeliverReceiptEvent) error {
	coded := ev.CodedID
	if coded == "" {
		var err error
		coded, err = CodeReceiptID(ev.RawDLRID, ev.Base)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrDLRMapInvalid, err)
		}
	}
	qkey, err := rediscompat.BuildQueueMessageKey(coded)
	if err != nil {
		return fmt.Errorf("%w: mapping key: %v", ErrDLRMapInvalid, err)
	}
	mapping, err := c.redis.ReadHash(ctx, qkey)
	if err != nil {
		if errors.Is(err, rediscompat.ErrKeyNotFound) {
			return fmt.Errorf("%w: coded %s (raw %s)", ErrDLRMapNotFound, coded, ev.RawDLRID)
		}
		return err
	}
	// The mapping must be exactly {msgid, connector_type} (Jasmin: len(q) != 2 check).
	_, hasMsgID := mapping["msgid"]
	_, hasCT := mapping["connector_type"]
	if len(mapping) != 2 || !hasMsgID || !hasCT {
		return fmt.Errorf("%w: malformed mapping for coded %s", ErrDLRMapNotFound, coded)
	}
	submitQueueID := mapping["msgid"]
	connectorType := mapping["connector_type"]

	dlrKey, err := rediscompat.BuildDLRKey(submitQueueID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDLRMapInvalid, err)
	}
	dlr, err := c.redis.ReadHash(ctx, dlrKey)
	if err != nil {
		if errors.Is(err, rediscompat.ErrKeyNotFound) {
			return fmt.Errorf("%w: dlr for %s", ErrDLRMapNotFound, submitQueueID)
		}
		return err
	}
	if dlr["sc"] != connectorType {
		return fmt.Errorf("%w: dlr sc %q != mapping connector_type %q", ErrDLRMapInvalid, dlr["sc"], connectorType)
	}

	if isFinalState(ev.Status) && c.cdr != nil {
		if err := c.cdr.RecordFinalDLR(ctx, cdr.FinalDLR{
			QueueMessageID: submitQueueID,
			ConnectorID:    ev.ConnectorID,
			SMSCMessageID:  ev.RawDLRID,
			Status:         ev.Status,
			Error:          ev.Err,
			DoneAt:         parseCDRReceiptTime(ev.DoneDate),
			ReceivedAt:     c.now().UTC(),
		}); err != nil {
			return err
		}
	}
	switch connectorType {
	case "httpapi":
		err = c.onDeliverHTTP(ctx, ev, submitQueueID, coded, dlr)
	case "smppsapi":
		err = c.onDeliverSMPPS(ctx, ev, submitQueueID, dlr)
	default:
		return fmt.Errorf("%w: unknown connector_type %q", ErrDLRMapInvalid, connectorType)
	}
	if err != nil {
		return err
	}
	if !isFinalState(ev.Status) {
		return nil
	}
	return c.redis.Delete(ctx, dlrKey)
}

func (c *Correlator) onDeliverHTTP(ctx context.Context, ev DeliverReceiptEvent, submitQueueID, coded string, dlr map[string]string) error {
	level, err := strconv.Atoi(dlr["level"])
	if err != nil {
		return fmt.Errorf("%w: level %q", ErrDLRMapInvalid, dlr["level"])
	}
	if level != 2 && level != 3 {
		return nil // a level-1 request has no terminal-receipt tracking
	}
	forward := Forward{
		Target: ForwardHTTP, Status: ev.Status, QueueMsgID: submitQueueID, Level: 2,
		URL: dlr["url"], Method: dlr["method"],
		Connector:  ev.RawDLRID, // dlr_connector = the raw receipt id (Jasmin level-2 quirk)
		IDSMSC:     coded,
		Sub:        ev.Sub,
		Dlvrd:      ev.Dlvrd,
		SubmitDate: ev.SubmitDate,
		DoneDate:   ev.DoneDate,
		Err:        ev.Err,
		Text:       ev.Text,
	}
	publishErr := c.publisher.PublishDLR(ctx, forward)
	recordForward(2, ev.Status, publishErr)
	if publishErr != nil {
		return fmt.Errorf("%w: %v", ErrForwardPublish, publishErr)
	}
	return nil
}

func (c *Correlator) onDeliverSMPPS(ctx context.Context, ev DeliverReceiptEvent, submitQueueID string, dlr map[string]string) error {
	rd := dlr["rd_receipt"]
	success := isSuccessState(ev.Status)
	forward := (success && rd == rdReceiptRequested) ||
		(!success && (rd == rdReceiptRequested || rd == rdReceiptRequestedForFailure))
	if !forward {
		return nil
	}
	f := Forward{
		Target: ForwardSMPPS, Status: ev.Status, QueueMsgID: submitQueueID, Err: ev.Err,
		SystemID: dlr["system_id"], SourceAddr: dlr["source_addr"], DestinationAddr: dlr["destination_addr"],
		SubDate: dlr["sub_date"], SourceAddrTON: dlr["source_addr_ton"], SourceAddrNPI: dlr["source_addr_npi"],
		DestAddrTON: dlr["dest_addr_ton"], DestAddrNPI: dlr["dest_addr_npi"],
	}
	publishErr := c.publisher.PublishDLR(ctx, f)
	// The SMPPS terminal leg carries no level field of its own; the requested
	// level from the record is the only one there is.
	recordForward(dlrLevel(dlr), ev.Status, publishErr)
	if publishErr != nil {
		return fmt.Errorf("%w: %v", ErrForwardPublish, publishErr)
	}
	return nil
}

func parseCDRReceiptTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"060102150405", "0601021504"} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

// isSuccessState reports the deliver_sm receipt success states (jasmin dlr.py:363).
func isSuccessState(s string) bool {
	return s == "ACCEPTD" || s == "DELIVRD"
}

// isFinalState reports the terminal receipt states that remove the DLR map (dlr.py:364).
func isFinalState(s string) bool {
	switch s {
	case "DELIVRD", "EXPIRED", "DELETED", "UNDELIV", "REJECTD":
		return true
	default:
		return false
	}
}
