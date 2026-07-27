package smppc

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// The deliver_sm ingestion port of SMPPClientSMListener.deliver_sm_event_*
// (jasmin/managers/listeners.py): classify receipt-vs-MO, publish dlr.deliver_sm
// or deliver.sm.<cid>, respond deliver_sm_resp. Long-message parts follow the
// legacy redis-less branch (critical log, part dropped) until the reassembly
// slice lands.

// DeliverPublisher publishes ingress publications to the shared broker.
type DeliverPublisher interface {
	Publish(ctx context.Context, exchange string, routingKey string, envelope amqpcompat.Envelope) error
}

// DeliverEncoder produces the pickled RoutableDeliverSm the legacy router
// consumes, from the received PDU's wire bytes (the bridge re-decodes them with
// smpp.pdu, so the pickled object is the legacy one by construction).
type DeliverEncoder interface {
	EncodeRoutableDeliverSM(ctx context.Context, wire []byte, cid string) ([]byte, error)
}

const (
	deliverPublishTimeout = 10 * time.Second
	dlrDeliverRoutingKey  = "dlr.deliver_sm"
	// smppStatusUnknownError is ESME_RUNKNOWNERR — the legacy generic-except
	// response status for a deliver_sm whose handling failed internally.
	smppStatusUnknownError uint32 = 0x000000FF
)

// SetDeliverUpstream wires the MO/DLR ingress publications. Call before Run,
// like the audit logger; a session without an upstream mirrors the legacy
// RouterPB-not-set branch (error log, PDU acked and dropped).
func (s *Session) SetDeliverUpstream(publisher DeliverPublisher, encoder DeliverEncoder) {
	s.deliverPublisher = publisher
	s.deliverEncoder = encoder
}

// handleDeliver processes one inbound deliver_sm. The response status mirrors
// the legacy handler: ESME_ROK on the happy paths (including the dropped-part
// branch), ESME_RUNKNOWNERR when publication fails (the generic-except path).
func (s *Session) handleDeliver(pdu smppwire.PDU) error {
	status := s.processDeliver(pdu)
	response := smppwire.PDU{Header: smppwire.Header{
		CommandID:      pdu.Header.CommandID | 0x80000000,
		CommandStatus:  status,
		SequenceNumber: pdu.Header.SequenceNumber,
	}}
	if status == 0 {
		response.SubmitResponse = &smppwire.SubmitResponseBody{MessageID: []byte{}}
	}
	return s.writePDU(response)
}

func (s *Session) processDeliver(pdu smppwire.PDU) uint32 {
	if pdu.SM == nil {
		return smppStatusUnknownError
	}
	if s.deliverPublisher == nil {
		s.logDeliverError("deliver_sm will not be routed: no upstream publisher (legacy RouterPB not set)")
		return 0
	}

	receipt, isReceipt := dlr.ParseReceipt(pdu.SM.Optional.ReceiptedMessageID, pdu.SM.Optional.MessageState, pdu.SM.ShortMessage)
	if isReceipt {
		return s.processDeliverReceipt(pdu, receipt)
	}
	return s.processDeliverMO(pdu)
}

// processDeliverReceipt publishes the DLR content for DLRLookup: routing key
// dlr.deliver_sm, body = stat, message-id = the base-coded receipt id.
func (s *Session) processDeliverReceipt(pdu smppwire.PDU, receipt dlr.Receipt) uint32 {
	codedID := s.codeReceiptID(receipt.ID)
	envelope, err := newDLRDeliverPublication(codedID, "deliver_sm", s.cfg.CID, receipt)
	if err != nil {
		s.logDeliverError(fmt.Sprintf("build dlr.deliver_sm publication: %v", err))
		return smppStatusUnknownError
	}
	ctx, cancel := context.WithTimeout(context.Background(), deliverPublishTimeout)
	defer cancel()
	if err := s.deliverPublisher.Publish(ctx, SubmitResponseExchange, dlrDeliverRoutingKey, envelope); err != nil {
		s.logDeliverError(fmt.Sprintf("publish dlr.deliver_sm: %v", err))
		return smppStatusUnknownError
	}
	return 0
}

// codeReceiptID applies code_dlr_msgid with the legacy except fallback: an
// uncodable id degrades to the verbatim upper/lstrip form and logs the error.
func (s *Session) codeReceiptID(receiptID string) string {
	coded, err := dlr.CodeReceiptID(receiptID, dlr.MsgIDBase(s.cfg.DLRMsgIDBases))
	if err != nil {
		s.logDeliverError(fmt.Sprintf("code_dlr_msgid, cannot code msgid [%s] with dlr_msg_id_bases:%d: %v",
			receiptID, s.cfg.DLRMsgIDBases, err))
		return dlr.CanonicalizeSubmitRespID(receiptID)
	}
	return coded
}

func (s *Session) processDeliverMO(pdu smppwire.PDU) uint32 {
	msgID, err := uuid4()
	if err != nil {
		s.logDeliverError(fmt.Sprintf("generate MO message id: %v", err))
		return smppStatusUnknownError
	}
	content := deliverMessageContent(pdu.SM)

	// Long-message part? Legacy stores parts in Redis for reassembly; without a
	// Redis client it logs critical and the part is lost — that redis-less
	// branch is what this slice reproduces (reassembly is a follow-up).
	if isLongDeliverPart(pdu.SM, content) {
		if s.auditLogger != nil {
			s.auditLogger.Error(fmt.Sprintf(
				"Invalid RC found while receiving part of long DeliverSm [queue-msgid:%s], MSG IS LOST !", msgID))
		}
		return 0
	}

	if s.deliverEncoder == nil {
		s.logDeliverError("deliver_sm will not be routed: no routable encoder")
		return 0
	}
	wire, err := smppwire.Encode(pdu)
	if err != nil {
		s.logDeliverError(fmt.Sprintf("re-encode deliver_sm wire: %v", err))
		return smppStatusUnknownError
	}
	ctx, cancel := context.WithTimeout(context.Background(), deliverPublishTimeout)
	defer cancel()
	pickled, err := s.deliverEncoder.EncodeRoutableDeliverSM(ctx, wire, s.cfg.CID)
	if err != nil {
		s.logDeliverError(fmt.Sprintf("encode RoutableDeliverSm: %v", err))
		return smppStatusUnknownError
	}
	envelope, err := newDeliverSMContentPublication(msgID, s.cfg.CID, pickled)
	if err != nil {
		s.logDeliverError(fmt.Sprintf("build deliver.sm publication: %v", err))
		return smppStatusUnknownError
	}
	if err := s.deliverPublisher.Publish(ctx, SubmitResponseExchange, "deliver.sm."+s.cfg.CID, envelope); err != nil {
		s.logDeliverError(fmt.Sprintf("publish deliver.sm.%s: %v", s.cfg.CID, err))
		return smppStatusUnknownError
	}
	s.logMOAuditLine(pdu, msgID, content)
	return 0
}

// deliverMessageContent mirrors the legacy message_content selection:
// short_message when non-empty, else message_payload, else short_message.
func deliverMessageContent(body *smppwire.SMBody) []byte {
	if len(body.ShortMessage) > 0 {
		return body.ShortMessage
	}
	if body.Optional.MessagePayload != nil {
		return body.Optional.MessagePayload
	}
	return body.ShortMessage
}

// isLongDeliverPart mirrors the legacy split detection: SAR TLVs, or a UDH
// concat header (UDHI esm_class bit, not GSM class-2, 05 00 03 prefix).
func isLongDeliverPart(body *smppwire.SMBody, content []byte) bool {
	if body.Optional.SARMessageReference != nil {
		return true
	}
	udhiSet := body.ESMClass&0x40 != 0
	notClass2 := true
	// data_coding GSM_MESSAGE_CLASS scheme (0xF0..0xFF) with msgClass CLASS_2.
	if body.DataCoding&0xF0 == 0xF0 && body.DataCoding&0x03 == 0x02 {
		notClass2 = false
	}
	return udhiSet && notClass2 && len(content) >= 3 && content[0] == 0x05 && content[1] == 0x00 && content[2] == 0x03
}

func (s *Session) logDeliverError(message string) {
	if s.auditLogger == nil {
		return
	}
	s.auditLogger.Error(message)
}

// logMOAuditLine renders the legacy SMS-MO line (the non-split branch).
func (s *Session) logMOAuditLine(pdu smppwire.PDU, msgID string, content []byte) {
	if s.auditLogger == nil {
		return
	}
	s.auditLogger.Info(fmt.Sprintf(
		"SMS-MO [cid:%s] [queue-msgid:%s] [status:%s] [prio:%s] [validity:%s] [from:%s] [to:%s] [content:%s]",
		s.cfg.CID,
		msgID,
		statusForLog(pdu.Header.CommandStatus),
		priorityFlagForLog(pdu.SM.PriorityFlag),
		validityForLog(pdu.SM.ValidityPeriod),
		pythonBytesRepr(pdu.SM.SourceAddress),
		pythonBytesRepr(pdu.SM.DestinationAddress),
		contentForLog(s.auditPrivacy, content),
	))
}

// priorityFlagForLog renders the decoded priority_flag param the way Python
// str()s the smpp.pdu enum: PriorityFlag.LEVEL_<n>.
func priorityFlagForLog(priority byte) string {
	return fmt.Sprintf("PriorityFlag.LEVEL_%d", priority)
}

// validityForLog renders an absent validity_period as Python None. A present
// wire value renders as its raw text form; the legacy prints the decoded
// smpp_time object, so receipts carrying a validity may diverge here until the
// smpp_time renderer lands (rare on MO; tracked in the plan).
func validityForLog(validity []byte) string {
	if len(validity) == 0 {
		return "None"
	}
	return string(validity)
}

// newDLRDeliverPublication is the legacy managers/content.py DLR content for a
// deliver_sm/data_sm receipt: body = stat, message-id = coded receipt id,
// headers type/cid plus every receipt field as dlr_<k>.
func newDLRDeliverPublication(codedID, pduTypeName, cid string, receipt dlr.Receipt) (amqpcompat.Envelope, error) {
	headers := map[string]amqpcompat.Field{
		"type":      amqpcompat.StringField(pduTypeName),
		"cid":       amqpcompat.StringField(cid),
		"dlr_id":    amqpcompat.StringField(receipt.ID),
		"dlr_stat":  amqpcompat.StringField(receipt.Stat),
		"dlr_sub":   amqpcompat.StringField(receipt.Sub),
		"dlr_dlvrd": amqpcompat.StringField(receipt.Dlvrd),
		"dlr_sdate": amqpcompat.StringField(receipt.SDate),
		"dlr_ddate": amqpcompat.StringField(receipt.DDate),
		"dlr_err":   amqpcompat.StringField(receipt.Err),
		"dlr_text":  amqpcompat.StringField(receipt.Text),
	}
	properties, err := amqpcompat.NewProperties(codedID, headers)
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope(dlrDeliverRoutingKey, properties, []byte(receipt.Stat))
}

// newDeliverSMContentPublication is the legacy DeliverSmContent envelope: the
// pickled RoutableDeliverSm body plus the routing headers RouterPB consumes.
func newDeliverSMContentPublication(msgID, cid string, pickledRoutable []byte) (amqpcompat.Envelope, error) {
	headers := map[string]amqpcompat.Field{
		"try-count":            amqpcompat.IntegerField(0),
		"connector-id":         amqpcompat.StringField(cid),
		"concatenated":         amqpcompat.BoolField(false),
		"will_be_concatenated": amqpcompat.BoolField(false),
	}
	properties, err := amqpcompat.NewProperties(msgID, headers)
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope("deliver.sm."+cid, properties, pickledRoutable)
}

// uuid4 mirrors the legacy randomUniqueId (str(uuid.uuid4())).
func uuid4() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
