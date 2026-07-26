package smppc

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/pumpitspace/jasmin/internal/core/logging"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// The SMS-MT audit lines emitted by the legacy SMPPClientSMListener on a final
// submit_sm_resp (jasmin/managers/listeners.py). They are byte-format-compatible
// with the legacy `jasmin-sm-listener` logger, so operator grep/log-shipping
// tooling keeps working while a connector runs on Go (KNOWN quirks reproduced:
// see pythonBytesRepr and the fully-qualified enum renderings below).
const (
	submitAuditSuccessFmt = "SMS-MT [cid:%s] [queue-msgid:%s] [smpp-msgid:%s] [status:%s] [prio:%d] [dlr:%s] [validity:%s] [from:%s] [to:%s] [content:%s] [tlvs:%s]"
	submitAuditErrorFmt   = "SMS-MT [cid:%s] [queue-msgid:%s] [status:ERROR/%s] [retry:%s] [prio:%d] [dlr:%s] [validity:%s] [from:%s] [to:%s] [content:%s] [tlvs:%s]"
)

// submitAuditFields carries the values for one SMS-MT audit line. Byte / enum
// fields are formatted through the helpers below to match Python's rendering.
type submitAuditFields struct {
	ConnectorID        string
	QueueMsgID         string
	SMPPMsgID          []byte // success only: r.response.params['message_id'] (bytes; logged as its bytes-repr)
	Status             uint32 // command_status of the response
	WillRetry          bool   // error variant only
	Priority           uint8
	RegisteredDelivery byte   // the request octet; receipt = low two bits
	Validity           string // "none" or the expiration header value
	SourceAddr         []byte
	DestAddr           []byte
	ShortMessage       []byte
	Privacy            bool   // log_privacy
	TLVs               string // pre-formatted format_tlvs_for_log output ("none" when empty)
}

// submitAuditLineSuccess renders the ESME_ROK SMS-MT line (listeners.py:361).
func submitAuditLineSuccess(f submitAuditFields) string {
	return fmt.Sprintf(submitAuditSuccessFmt,
		f.ConnectorID, f.QueueMsgID, pythonBytesRepr(f.SMPPMsgID), statusForLog(f.Status), f.Priority,
		receiptForLog(f.RegisteredDelivery), f.Validity,
		pythonBytesRepr(f.SourceAddr), pythonBytesRepr(f.DestAddr),
		contentForLog(f.Privacy, f.ShortMessage), tlvsOrNone(f.TLVs))
}

// submitAuditLineError renders the non-ROK SMS-MT line (listeners.py:399).
func submitAuditLineError(f submitAuditFields) string {
	return fmt.Sprintf(submitAuditErrorFmt,
		f.ConnectorID, f.QueueMsgID, statusForLog(f.Status), pythonBool(f.WillRetry), f.Priority,
		receiptForLog(f.RegisteredDelivery), f.Validity,
		pythonBytesRepr(f.SourceAddr), pythonBytesRepr(f.DestAddr),
		contentForLog(f.Privacy, f.ShortMessage), tlvsOrNone(f.TLVs))
}

// statusForLog renders a command_status as Python's `%s` of a CommandStatus enum
// member: the fully-qualified `CommandStatus.<NAME>` (not the bare name).
func statusForLog(status uint32) string {
	return "CommandStatus." + smppStatusName(status)
}

// receiptForLog renders the request registered_delivery's receipt (low two bits)
// as Python's `%s` of the RegisteredDeliveryReceipt enum member. Wire value 3 is
// rejected at decode by the legacy encoder, so a decoded PDU never reaches it.
func receiptForLog(registeredDelivery byte) string {
	name := "NO_SMSC_DELIVERY_RECEIPT_REQUESTED"
	switch registeredDelivery & 0x03 {
	case 1:
		name = "SMSC_DELIVERY_RECEIPT_REQUESTED"
	case 2:
		name = "SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE"
	}
	return "RegisteredDeliveryReceipt." + name
}

// contentForLog renders the short_message as the legacy does: with log_privacy on
// it is redacted to "** N byte content **" (see logging.Redact); off it is
// Python's `%r` of the bytes, i.e. the bytes repr.
func contentForLog(privacy bool, short []byte) string {
	if privacy {
		return logging.Redact(true, short)
	}
	return pythonBytesRepr(short)
}

// tlvsOrNone returns the pre-formatted tlvs string, or "none" when empty, matching
// format_tlvs_for_log's empty fallback.
func tlvsOrNone(tlvs string) string {
	if tlvs == "" {
		return "none"
	}
	return tlvs
}

// pythonBool renders a bool as Python's `%s` of a bool: "True"/"False".
func pythonBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// moreMessagesToSendNames maps the wire value (low bit) to Python's
// MoreMessagesToSend enum member name.
var moreMessagesToSendNames = map[byte]string{0: "NO_MORE_MESSAGES", 1: "MORE_MESSAGES"}

// formatTLVsForLog reproduces jasmin/tools/tlv.py format_tlvs_for_log for the
// optional params + vendor custom TLVs a Go-decoded submit carries: each present
// standard optional as key:value (in the bridge's projection order), then each
// custom TLV as 0x%04X:value, joined by ','. With log_privacy the values are
// omitted (keys/tags only). Empty renders as "none". Only the optionals the
// legacy MT path surfaces are handled (sar_*, more_messages_to_send,
// message_payload); other decoded fields never appear on a submit audit line.
func formatTLVsForLog(optional smppwire.OptionalParameters, customTLVs []tlv.TLV, privacy bool) string {
	var parts []string
	add := func(key, value string) {
		if privacy {
			parts = append(parts, key)
		} else {
			parts = append(parts, key+":"+value)
		}
	}
	if optional.SARMessageReference != nil {
		add("sar_msg_ref_num", strconv.FormatUint(uint64(*optional.SARMessageReference), 10))
	}
	if optional.SARTotalSegments != nil {
		add("sar_total_segments", strconv.FormatUint(uint64(*optional.SARTotalSegments), 10))
	}
	if optional.SARSegmentSequence != nil {
		add("sar_segment_seqnum", strconv.FormatUint(uint64(*optional.SARSegmentSequence), 10))
	}
	if optional.MoreMessagesToSend != nil {
		add("more_messages_to_send", "MoreMessagesToSend."+moreMessagesToSendNames[*optional.MoreMessagesToSend&0x01])
	}
	if optional.MessagePayload != nil {
		add("message_payload", pythonBytesRepr(optional.MessagePayload))
	}
	for _, custom := range customTLVs {
		tag := fmt.Sprintf("0x%04X", custom.Tag.Uint64())
		if privacy {
			parts = append(parts, tag)
		} else {
			parts = append(parts, tag+":"+customTLVValue(custom.Value))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

// customTLVValue renders a custom TLV's value as Python's `%s` of the pickled
// value: bytes as their repr, a string as-is, an int/bool/None as Python prints
// them. Mirrors the concrete types tupleValueField decodes.
func customTLVValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case bool:
		return pythonBool(v)
	case string:
		return v
	case []byte:
		return pythonBytesRepr(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// pythonBytesRepr reproduces CPython's repr()/str() of a bytes object (they
// coincide for bytes): a `b'...'` literal. The quote is single by default, but
// double when the bytes contain a single quote and no double quote; the chosen
// quote, backslash, and \t\n\r are escaped, printable ASCII (0x20..0x7e) is kept
// verbatim, and every other byte becomes a lowercase \xNN. This intentionally
// reproduces the legacy log's Python artifact for byte-for-byte grep parity.
func pythonBytesRepr(b []byte) string {
	quote := byte('\'')
	if bytes.IndexByte(b, '\'') >= 0 && bytes.IndexByte(b, '"') < 0 {
		quote = '"'
	}
	var sb strings.Builder
	sb.Grow(len(b) + 3)
	sb.WriteByte('b')
	sb.WriteByte(quote)
	for _, c := range b {
		switch {
		case c == '\\':
			sb.WriteString(`\\`)
		case c == quote:
			sb.WriteByte('\\')
			sb.WriteByte(quote)
		case c == '\t':
			sb.WriteString(`\t`)
		case c == '\n':
			sb.WriteString(`\n`)
		case c == '\r':
			sb.WriteString(`\r`)
		case c >= 0x20 && c <= 0x7e:
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, `\x%02x`, c)
		}
	}
	sb.WriteByte(quote)
	return sb.String()
}
