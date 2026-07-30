package smppc

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/dlr"
	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
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
// consumes directly from the decoded PDU.
type DeliverEncoder interface {
	EncodeRoutableDeliverPDU(ctx context.Context, pdu smppwire.PDU, cid string) ([]byte, error)
}

// MultipartStore accumulates inbound long-message (SAR/UDH) segments keyed by
// connector/reference/destination until every segment arrives, then the session
// reassembles and publishes one whole MO. Nil disables reassembly (the legacy
// redis-less drop). Segment content is opaque to the store.
type MultipartStore interface {
	StorePart(ctx context.Context, connectorID string, reference uint32, destination string, sequence uint32, content []byte) error
	ReadParts(ctx context.Context, connectorID string, reference uint32, destination string) (map[uint32][]byte, error)
	DeleteParts(ctx context.Context, connectorID string, reference uint32, destination string) error
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

// SetMultipartStore enables inbound long-message reassembly. Nil (the default)
// reproduces the legacy redis-less drop.
func (s *Session) SetMultipartStore(store MultipartStore) {
	s.multipartStore = store
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

	// Long-message part: publish each stored segment for SMPPs destinations,
	// then publish the reassembled whole for HTTP. Without a multipart store,
	// reproduce the legacy redis-less drop (MSG IS LOST).
	if isLongDeliverPart(pdu.SM, content) {
		return s.handleLongDeliverPart(pdu, content, msgID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), deliverPublishTimeout)
	defer cancel()
	// MO-direction interception runs on the whole single-part message before
	// publish: a reject drops it (ack, ESME_ROK), a mutation rewrites the body.
	intercepted, dropped, errStatus := s.interceptMO(ctx, pdu.SM, msgID)
	if errStatus != 0 {
		return errStatus
	}
	if dropped {
		return 0
	}
	return s.publishMO(ctx, pdu, msgID, intercepted, false, false)
}

// multipartInfo extracts (reference, total, sequence, part content) from a long
// deliver_sm part, for either SAR TLVs or a UDH concatenation header. The part
// content is what gets concatenated: SAR keeps the whole short_message; UDH
// strips its 6-byte header (05 00 03 ref total seq).
func multipartInfo(body *smppwire.SMBody, content []byte) (reference uint32, total, sequence byte, part []byte, ok bool) {
	if body.Optional.SARMessageReference != nil && body.Optional.SARTotalSegments != nil && body.Optional.SARSegmentSequence != nil {
		return uint32(*body.Optional.SARMessageReference), *body.Optional.SARTotalSegments, *body.Optional.SARSegmentSequence, content, true
	}
	if len(content) >= 6 && content[0] == 0x05 && content[1] == 0x00 && content[2] == 0x03 {
		return uint32(content[3]), content[4], content[5], content[6:], true
	}
	return 0, 0, 0, nil, false
}

// handleLongDeliverPart stores and publishes one marked segment and, when the
// whole message has arrived, reassembles and publishes the marked whole. The
// reader is single-threaded per session, so segments accumulate without a race.
func (s *Session) handleLongDeliverPart(pdu smppwire.PDU, content []byte, msgID string) uint32 {
	if s.multipartStore == nil {
		if s.auditLogger != nil {
			s.auditLogger.Error(fmt.Sprintf(
				"Invalid RC found while receiving part of long DeliverSm [queue-msgid:%s], MSG IS LOST !", msgID))
		}
		return 0
	}
	reference, total, sequence, part, ok := multipartInfo(pdu.SM, content)
	if !ok || total == 0 || sequence == 0 || sequence > total {
		s.logDeliverError(fmt.Sprintf("malformed long deliver_sm part [queue-msgid:%s]", msgID))
		return 0
	}
	destination := string(pdu.SM.DestinationAddress)
	ctx, cancel := context.WithTimeout(context.Background(), deliverPublishTimeout)
	defer cancel()
	if err := s.multipartStore.StorePart(ctx, s.cfg.CID, reference, destination, uint32(sequence), part); err != nil {
		s.logDeliverError(fmt.Sprintf("store long deliver_sm part [ref:%d seq:%d]: %v", reference, sequence, err))
		return smppStatusUnknownError
	}
	// Each segment is intercepted before it is published, the way the oracle
	// does it (deliver_sm_event_interceptor runs per arriving PDU, and only then
	// does deliver_sm_event_post_interception publish the will_be_concatenated
	// part). Publishing segments unintercepted would let an MO interceptor's
	// reject be bypassed entirely: the whole is suppressed, but the segments
	// have already reached every SMPPs-bound destination.
	segmentContent, segmentDropped, segmentErr := s.interceptMO(ctx, pdu.SM, msgID)
	if segmentErr != 0 {
		return segmentErr
	}
	if !segmentDropped {
		if status := s.publishMO(ctx, pdu, msgID, segmentContent, false, true); status != 0 {
			return status
		}
	}
	parts, err := s.multipartStore.ReadParts(ctx, s.cfg.CID, reference, destination)
	if err != nil {
		s.logDeliverError(fmt.Sprintf("read long deliver_sm parts [ref:%d]: %v", reference, err))
		return smppStatusUnknownError
	}
	if len(parts) < int(total) {
		return 0 // wait for the remaining segments
	}
	// Complete: concatenate in sequence order and publish one whole MO.
	assembled := make([]byte, 0)
	for seq := byte(1); seq <= total; seq++ {
		segment, present := parts[uint32(seq)]
		if !present {
			return 0 // a gap (duplicate count without segment 'seq'); keep waiting
		}
		assembled = append(assembled, segment...)
	}
	whole := s.reassembledDeliverSM(pdu.SM, assembled)
	wholeMsgID, err := uuid4()
	if err != nil {
		s.logDeliverError(fmt.Sprintf("generate concatenated MO message id: %v", err))
		return smppStatusUnknownError
	}
	// Intercept the reassembled whole message, not the individual parts.
	intercepted, dropped, errStatus := s.interceptMO(ctx, whole.SM, wholeMsgID)
	if errStatus != 0 {
		return errStatus
	}
	if dropped {
		s.deleteMultipartParts(reference, destination)
		return 0
	}
	status := s.publishMO(ctx, whole, wholeMsgID, intercepted, true, false)
	if status == 0 {
		s.deleteMultipartParts(reference, destination)
	}
	return status
}

// reassembledDeliverSM builds the whole-message deliver_sm from a received part,
// with the concatenated content and the SAR TLVs / UDH indicator cleared.
func (s *Session) reassembledDeliverSM(part *smppwire.SMBody, assembled []byte) smppwire.PDU {
	body := *part
	if len(body.ShortMessage) > 0 || body.Optional.MessagePayload == nil {
		body.ShortMessage = assembled
	} else {
		body.Optional.MessagePayload = assembled
	}
	body.ESMClass &^= 0x40 // clear the UDHI indicator
	body.Optional.SARMessageReference = nil
	body.Optional.SARTotalSegments = nil
	body.Optional.SARSegmentSequence = nil
	return smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM}, SM: &body}
}

func (s *Session) deleteMultipartParts(reference uint32, destination string) {
	ctx, cancel := context.WithTimeout(context.Background(), deliverPublishTimeout)
	defer cancel()
	if err := s.multipartStore.DeleteParts(ctx, s.cfg.CID, reference, destination); err != nil {
		s.logDeliverError(fmt.Sprintf("delete reassembled long deliver_sm [ref:%d]: %v", reference, err))
	}
}

// publishMO pickles and publishes an MO deliver_sm to deliver.sm.<cid>. Segment
// publications carry the routing markers but do not emit the whole-message
// SMS-MO audit line.
func (s *Session) publishMO(
	ctx context.Context,
	pdu smppwire.PDU,
	msgID string,
	content []byte,
	concatenated bool,
	willBeConcatenated bool,
) uint32 {
	if s.deliverEncoder == nil {
		s.logDeliverError("deliver_sm will not be routed: no routable encoder")
		return smppStatusUnknownError
	}
	pickled, err := s.deliverEncoder.EncodeRoutableDeliverPDU(ctx, pdu, s.cfg.CID)
	if err != nil {
		s.logDeliverError(fmt.Sprintf("encode RoutableDeliverSm: %v", err))
		return smppStatusUnknownError
	}
	envelope, err := newDeliverSMContentPublication(
		msgID, s.cfg.CID, pickled, concatenated, willBeConcatenated,
	)
	if err != nil {
		s.logDeliverError(fmt.Sprintf("build deliver.sm publication: %v", err))
		return smppStatusUnknownError
	}
	if err := s.deliverPublisher.Publish(ctx, SubmitResponseExchange, "deliver.sm."+s.cfg.CID, envelope); err != nil {
		s.logDeliverError(fmt.Sprintf("publish deliver.sm.%s: %v", s.cfg.CID, err))
		stats.DefaultPrometheus().RecordMO(s.cfg.CID, "publish_failed")
		return smppStatusUnknownError
	}
	if !willBeConcatenated {
		s.logMOAuditLine(pdu, msgID, content)
	}
	stats.DefaultPrometheus().RecordMO(s.cfg.CID, "published")
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
func newDeliverSMContentPublication(
	msgID string,
	cid string,
	pickledRoutable []byte,
	concatenated bool,
	willBeConcatenated bool,
) (amqpcompat.Envelope, error) {
	headers := map[string]amqpcompat.Field{
		"try-count":            amqpcompat.IntegerField(0),
		"connector-id":         amqpcompat.StringField(cid),
		"concatenated":         amqpcompat.BoolField(concatenated),
		"will_be_concatenated": amqpcompat.BoolField(willBeConcatenated),
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
