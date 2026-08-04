package dlr

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

// ErrInvalidThrowerEnvelope identifies a malformed or unsupported DLR thrower message.
var ErrInvalidThrowerEnvelope = errors.New("dlr: invalid thrower envelope")

// DecodeThrowerForward projects the fixture-backed dlr_thrower.http and
// dlr_thrower.smpps envelopes into the typed correlation/thrower boundary.
func DecodeThrowerForward(envelope amqpcompat.Envelope) (Forward, error) {
	route := envelope.Route().Kind()
	if route != amqpcompat.RouteDLRHTTP && route != amqpcompat.RouteDLRSMPPS {
		return Forward{}, fmt.Errorf("%w: route %s", ErrInvalidThrowerEnvelope, route)
	}
	messageID := envelope.Properties().MessageID()
	body := envelope.Body()
	if messageID == "" || !bytes.Equal(body, []byte(messageID)) {
		return Forward{}, fmt.Errorf("%w: body/message-id mismatch", ErrInvalidThrowerEnvelope)
	}
	headers := envelope.Properties().Headers()
	if count, err := integerHeader(headers, "try-count"); err != nil || count < 0 {
		return Forward{}, fmt.Errorf("%w: invalid try-count", ErrInvalidThrowerEnvelope)
	}

	status, err := stringHeader(headers, "message_status")
	if err != nil {
		return Forward{}, err
	}
	if !validMessageStatus(status) {
		return Forward{}, fmt.Errorf("%w: invalid message_status %q", ErrInvalidThrowerEnvelope, status)
	}
	if route == amqpcompat.RouteDLRHTTP {
		return decodeHTTPForward(messageID, status, headers)
	}
	return decodeSMPPSForward(messageID, status, headers)
}

func decodeHTTPForward(messageID, status string, headers map[string]amqpcompat.Field) (Forward, error) {
	level, err := integerHeader(headers, "level")
	if err != nil || level < 1 || level > 3 {
		return Forward{}, fmt.Errorf("%w: invalid level", ErrInvalidThrowerEnvelope)
	}
	url, err := stringHeader(headers, "url")
	if err != nil {
		return Forward{}, err
	}
	method, err := stringHeader(headers, "method")
	if err != nil {
		return Forward{}, err
	}
	if method != "GET" && method != "POST" {
		return Forward{}, fmt.Errorf("%w: invalid method %q", ErrInvalidThrowerEnvelope, method)
	}
	connector, err := stringHeader(headers, "connector")
	if err != nil {
		return Forward{}, err
	}
	forward := Forward{Target: ForwardHTTP, QueueMsgID: messageID, Status: status, Level: int(level), URL: url, Method: method, Connector: connector}
	if level == 2 || level == 3 {
		for name, destination := range map[string]*string{
			"id_smsc": &forward.IDSMSC, "sub": &forward.Sub, "dlvrd": &forward.Dlvrd,
			"subdate": &forward.SubmitDate, "donedate": &forward.DoneDate,
			"err": &forward.Err, "text": &forward.Text,
		} {
			value, valueErr := optionalStringHeader(headers, name)
			if valueErr != nil {
				return Forward{}, valueErr
			}
			*destination = value
		}
	}
	return forward, nil
}

func decodeSMPPSForward(messageID, status string, headers map[string]amqpcompat.Field) (Forward, error) {
	forward := Forward{Target: ForwardSMPPS, QueueMsgID: messageID, Status: status}
	for name, destination := range map[string]*string{
		"system_id": &forward.SystemID, "source_addr": &forward.SourceAddr,
		"destination_addr": &forward.DestinationAddr, "sub_date": &forward.SubDate,
		"source_addr_ton": &forward.SourceAddrTON, "source_addr_npi": &forward.SourceAddrNPI,
		"dest_addr_ton": &forward.DestAddrTON, "dest_addr_npi": &forward.DestAddrNPI,
	} {
		value, err := stringHeader(headers, name)
		if err != nil {
			return Forward{}, err
		}
		*destination = value
	}
	if field, ok := headers["err"]; ok {
		if value, stringOK := field.String(); stringOK {
			forward.Err = value
		} else if value, integerOK := field.Integer(); integerOK {
			forward.Err = strconv.FormatInt(value, 10)
			forward.ErrIsInteger = true
		} else {
			return Forward{}, fmt.Errorf("%w: header %q has wrong kind", ErrInvalidThrowerEnvelope, "err")
		}
	} else {
		return Forward{}, fmt.Errorf("%w: missing header %q", ErrInvalidThrowerEnvelope, "err")
	}
	return forward, nil
}

func stringHeader(headers map[string]amqpcompat.Field, name string) (string, error) {
	value, err := optionalStringHeader(headers, name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%w: empty string header %q", ErrInvalidThrowerEnvelope, name)
	}
	return value, nil
}

func optionalStringHeader(headers map[string]amqpcompat.Field, name string) (string, error) {
	field, ok := headers[name]
	if !ok {
		return "", fmt.Errorf("%w: missing header %q", ErrInvalidThrowerEnvelope, name)
	}
	value, ok := field.String()
	if !ok {
		return "", fmt.Errorf("%w: invalid string header %q", ErrInvalidThrowerEnvelope, name)
	}
	return value, nil
}

// ValidMessageStatus reports whether status is one the thrower envelope will
// carry. It is exported so configuration surfaces that let an operator choose a
// receipt status can reject an unpublishable one at write time rather than
// silently dropping the receipt inside EncodeThrowerForward.
func ValidMessageStatus(status string) bool { return validMessageStatus(status) }

func validMessageStatus(status string) bool {
	// Legacy DLRContentForHttpapi/DLRContentForSmpps accept every value whose
	// first five characters are exactly "ESME_"; they do not validate the
	// suffix against CommandStatus. Preserve that observable constructor rule.
	if strings.HasPrefix(status, "ESME_") {
		return true
	}
	switch status {
	case "DELIVRD", "EXPIRED", "DELETED", "UNDELIV", "ACCEPTD", "UNKNOWN", "REJECTD":
		return true
	default:
		return false
	}
}

func integerHeader(headers map[string]amqpcompat.Field, name string) (int64, error) {
	field, ok := headers[name]
	if !ok {
		return 0, fmt.Errorf("%w: missing header %q", ErrInvalidThrowerEnvelope, name)
	}
	value, ok := field.Integer()
	if !ok {
		return 0, fmt.Errorf("%w: header %q has wrong kind", ErrInvalidThrowerEnvelope, name)
	}
	return value, nil
}

// SMPPSReceiptParamsFromForward converts a typed SMPPS thrower projection into
// the receipt builder's wire-facing parameters.
func SMPPSReceiptParamsFromForward(forward Forward) (SMPPSReceiptParams, error) {
	if forward.Target != ForwardSMPPS {
		return SMPPSReceiptParams{}, fmt.Errorf("%w: forward target is not smpps", ErrInvalidThrowerEnvelope)
	}
	sourceTON, err := parseAddrTON(forward.SourceAddrTON)
	if err != nil {
		return SMPPSReceiptParams{}, err
	}
	sourceNPI, err := parseAddrNPI(forward.SourceAddrNPI)
	if err != nil {
		return SMPPSReceiptParams{}, err
	}
	destTON, err := parseAddrTON(forward.DestAddrTON)
	if err != nil {
		return SMPPSReceiptParams{}, err
	}
	destNPI, err := parseAddrNPI(forward.DestAddrNPI)
	if err != nil {
		return SMPPSReceiptParams{}, err
	}
	if forward.QueueMsgID == "" || forward.Status == "" || forward.SubDate == "" || forward.SourceAddr == "" || forward.DestinationAddr == "" {
		return SMPPSReceiptParams{}, fmt.Errorf("%w: incomplete smpps forward", ErrInvalidThrowerEnvelope)
	}
	return SMPPSReceiptParams{MsgID: forward.QueueMsgID, SystemID: forward.SystemID, MessageStatus: forward.Status, Err: forward.Err, SubDate: forward.SubDate, SourceAddr: forward.SourceAddr, DestAddr: forward.DestinationAddr, SourceAddrTON: sourceTON, SourceAddrNPI: sourceNPI, DestAddrTON: destTON, DestAddrNPI: destNPI}, nil
}

func parseAddrTON(value string) (byte, error) {
	values := map[string]byte{"AddrTon.UNKNOWN": 0, "AddrTon.INTERNATIONAL": 1, "AddrTon.NATIONAL": 2, "AddrTon.NETWORK_SPECIFIC": 3, "AddrTon.SUBSCRIBER_NUMBER": 4, "AddrTon.ALPHANUMERIC": 5, "AddrTon.ABBREVIATED": 6}
	if result, ok := values[value]; ok {
		return result, nil
	}
	return 0, fmt.Errorf("%w: unknown TON %q", ErrInvalidThrowerEnvelope, value)
}

func parseAddrNPI(value string) (byte, error) {
	values := map[string]byte{"AddrNpi.UNKNOWN": 0, "AddrNpi.ISDN": 1, "AddrNpi.DATA": 3, "AddrNpi.TELEX": 4, "AddrNpi.LAND_MOBILE": 6, "AddrNpi.NATIONAL": 8, "AddrNpi.PRIVATE": 9, "AddrNpi.ERMES": 10, "AddrNpi.INTERNET": 14, "AddrNpi.WAP_CLIENT_ID": 18}
	if result, ok := values[value]; ok {
		return result, nil
	}
	return 0, fmt.Errorf("%w: unknown NPI %q", ErrInvalidThrowerEnvelope, value)
}

// FormatAddrTON renders a wire TON byte as the legacy enum name the frozen
// Redis record and thrower envelopes carry ("AddrTon.INTERNATIONAL"). It is the
// inverse of parseAddrTON; an unrecognised value falls back to UNKNOWN, which is
// what smpp.pdu's enum lookup yields for a reserved code.
func FormatAddrTON(value byte) string {
	names := map[byte]string{0: "AddrTon.UNKNOWN", 1: "AddrTon.INTERNATIONAL", 2: "AddrTon.NATIONAL", 3: "AddrTon.NETWORK_SPECIFIC", 4: "AddrTon.SUBSCRIBER_NUMBER", 5: "AddrTon.ALPHANUMERIC", 6: "AddrTon.ABBREVIATED"}
	if name, ok := names[value]; ok {
		return name
	}
	return "AddrTon.UNKNOWN"
}

// FormatAddrNPI is the NPI counterpart of FormatAddrTON.
func FormatAddrNPI(value byte) string {
	names := map[byte]string{0: "AddrNpi.UNKNOWN", 1: "AddrNpi.ISDN", 3: "AddrNpi.DATA", 4: "AddrNpi.TELEX", 6: "AddrNpi.LAND_MOBILE", 8: "AddrNpi.NATIONAL", 9: "AddrNpi.PRIVATE", 10: "AddrNpi.ERMES", 14: "AddrNpi.INTERNET", 18: "AddrNpi.WAP_CLIENT_ID"}
	if name, ok := names[value]; ok {
		return name
	}
	return "AddrNpi.UNKNOWN"
}

// FormatRegisteredDeliveryReceipt renders the receipt bits of registered_delivery
// as the legacy RegisteredDeliveryReceipt enum name stored in rd_receipt. Only
// the low two bits select the receipt mode (SMPP 3.4 §5.2.17).
func FormatRegisteredDeliveryReceipt(registeredDelivery byte) string {
	switch registeredDelivery & 0x03 {
	case 1:
		return rdReceiptRequested
	case 2:
		return rdReceiptRequestedForFailure
	default:
		return "RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED"
	}
}
