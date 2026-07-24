package mo

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// Enum name tables mirror the legacy smpp.pdu constants value maps — the
// thrower forwards Enum params as value.name.
var (
	payloadTypeNames      = []string{"DEFAULT", "WCMP"}
	privacyIndicatorNames = []string{"NOT_RESTRICTED", "RESTRICTED", "CONFIDENTIAL", "SECRET"}
	languageNames         = []string{"UNSPECIFIED", "ENGLISH", "FRENCH", "SPANISH", "GERMAN", "PORTUGUESE"}
	messageStateNames     = map[byte]string{
		1: "ENROUTE", 2: "DELIVERED", 3: "EXPIRED", 4: "DELETED",
		5: "UNDELIVERABLE", 6: "ACCEPTED", 7: "UNKNOWN", 8: "REJECTED",
	}
	digitModeNames = []string{"TBCD", "ASCII"}
	addrTONNames   = []string{"UNKNOWN", "INTERNATIONAL", "NATIONAL", "NETWORK_SPECIFIC", "SUBSCRIBER_NUMBER", "ALPHANUMERIC", "ABBREVIATED"}
	addrNPINames   = map[byte]string{
		0: "UNKNOWN", 1: "ISDN", 3: "DATA", 4: "TELEX", 6: "LAND_MOBILE",
		8: "NATIONAL", 9: "PRIVATE", 10: "ERMES", 14: "INTERNET", 18: "WAP_CLIENT_ID",
	}
)

// TLVParamsFromDeliverSM projects the decoded standard optional params into
// the thrower's tlv_params entries: StandardOptionalParams order, values
// formatted the legacy way — str() for integers, Enum .name, hexlify for
// bytes, and the CallbackNum object's str() for callback_num. its_session_info
// stays listed in StandardOptionalParams for fidelity but can never be
// present: the legacy library has no encoder for it and rejects the PDU.
func TLVParamsFromDeliverSM(sm *smppwire.SMBody) []TLVParam {
	if sm == nil {
		return nil
	}
	optional := sm.Optional
	var params []TLVParam
	add := func(name, value string) {
		params = append(params, TLVParam{Name: name, Value: value})
	}
	if optional.UserMessageReference != nil {
		add("user_message_reference", strconv.Itoa(int(*optional.UserMessageReference)))
	}
	if optional.SourcePort != nil {
		add("source_port", strconv.Itoa(int(*optional.SourcePort)))
	}
	if optional.DestinationPort != nil {
		add("destination_port", strconv.Itoa(int(*optional.DestinationPort)))
	}
	if optional.SARMessageReference != nil {
		add("sar_msg_ref_num", strconv.Itoa(int(*optional.SARMessageReference)))
	}
	if optional.SARTotalSegments != nil {
		add("sar_total_segments", strconv.Itoa(int(*optional.SARTotalSegments)))
	}
	if optional.SARSegmentSequence != nil {
		add("sar_segment_seqnum", strconv.Itoa(int(*optional.SARSegmentSequence)))
	}
	if optional.PayloadType != nil && int(*optional.PayloadType) < len(payloadTypeNames) {
		add("payload_type", payloadTypeNames[*optional.PayloadType])
	}
	if optional.PrivacyIndicator != nil && int(*optional.PrivacyIndicator) < len(privacyIndicatorNames) {
		add("privacy_indicator", privacyIndicatorNames[*optional.PrivacyIndicator])
	}
	if optional.CallbackNum != nil {
		add("callback_num", callbackNumString(optional.CallbackNum))
	}
	if optional.LanguageIndicator != nil && int(*optional.LanguageIndicator) < len(languageNames) {
		add("language_indicator", languageNames[*optional.LanguageIndicator])
	}
	if optional.NetworkErrorCode != nil {
		add("network_error_code", hex.EncodeToString(optional.NetworkErrorCode))
	}
	if optional.MessageState != nil {
		if name, ok := messageStateNames[*optional.MessageState]; ok {
			add("message_state", name)
		}
	}
	if optional.ReceiptedMessageID != nil {
		add("receipted_message_id", hex.EncodeToString(optional.ReceiptedMessageID))
	}
	return params
}

// callbackNumString reproduces the legacy CallbackNum.__str__ the thrower
// forwards verbatim, including the Python bytes repr of the digits.
func callbackNumString(number *smppwire.CallbackNumber) string {
	digitMode := "?"
	if int(number.DigitMode) < len(digitModeNames) {
		digitMode = digitModeNames[number.DigitMode]
	}
	ton := "?"
	if int(number.TON) < len(addrTONNames) {
		ton = addrTONNames[number.TON]
	}
	npi := addrNPINames[number.NPI]
	return fmt.Sprintf("CallbackNum[digitModeIndicator: CallbackNumDigitModeIndicator.%s, ton: AddrTon.%s, npi: AddrNpi.%s, digits: %s]",
		digitMode, ton, npi, pythonBytesRepr(number.Digits))
}

// pythonBytesRepr renders repr(b'...') exactly as CPython does: single quotes
// unless the bytes contain a single quote and no double quote; printable
// ASCII verbatim; \t, \n, \r, backslash, and the active quote escaped; all
// other bytes as lowercase \xNN.
func pythonBytesRepr(value []byte) string {
	quote := byte('\'')
	if strings.Contains(string(value), "'") && !strings.Contains(string(value), `"`) {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte('b')
	b.WriteByte(quote)
	for _, c := range value {
		switch {
		case c == quote || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	b.WriteByte(quote)
	return b.String()
}
