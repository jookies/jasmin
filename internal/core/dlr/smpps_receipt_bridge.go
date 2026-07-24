package dlr

import (
	"errors"
	"fmt"
	"strings"
)

// SMPP TON/NPI name → wire value (smpp.pdu constants.addr_ton_name_map / addr_npi_name_map).
var (
	addrTONByName = map[string]byte{
		"UNKNOWN": 0x00, "INTERNATIONAL": 0x01, "NATIONAL": 0x02, "NETWORK_SPECIFIC": 0x03,
		"SUBSCRIBER_NUMBER": 0x04, "ALPHANUMERIC": 0x05, "ABBREVIATED": 0x06,
	}
	addrNPIByName = map[string]byte{
		"UNKNOWN": 0x00, "ISDN": 0x01, "DATA": 0x03, "TELEX": 0x04, "LAND_MOBILE": 0x06,
		"NATIONAL": 0x08, "PRIVATE": 0x09, "ERMES": 0x0a, "INTERNET": 0x0e, "WAP_CLIENT_ID": 0x12,
	}
)

// defaultSMPPSReceiptErr is the DLRContentForSmpps err default (99) applied when a receipt
// carries no explicit err — i.e. on the submit_sm_resp leg.
const defaultSMPPSReceiptErr = "99"

// ErrInvalidAddrEnum reports a TON/NPI enum string that maps to no wire value.
var ErrInvalidAddrEnum = errors.New("dlr: invalid TON/NPI enum string")

// enumMember extracts the member name from a stored enum string, matching jasmin's
// get_enum: "AddrTon.INTERNATIONAL" → "INTERNATIONAL", or a bare "INTERNATIONAL".
func enumMember(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// ParseAddrTON maps a stored TON enum string to its SMPP wire value.
func ParseAddrTON(s string) (byte, error) {
	if v, ok := addrTONByName[enumMember(s)]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("%w: ton %q", ErrInvalidAddrEnum, s)
}

// ParseAddrNPI maps a stored NPI enum string to its SMPP wire value.
func ParseAddrNPI(s string) (byte, error) {
	if v, ok := addrNPIByName[enumMember(s)]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("%w: npi %q", ErrInvalidAddrEnum, s)
}

// SMPPSReceiptFromForward projects a ForwardSMPPS (from the correlation engine) into the
// receipt params for BuildDeliverSMReceipt, parsing the stored TON/NPI enum strings into
// wire bytes and applying the DLRContentForSmpps err=99 default when the forward has no
// explicit err (the submit_sm_resp leg). It mirrors HTTPDLRCallbackFromForward.
func SMPPSReceiptFromForward(f Forward) (SMPPSReceiptParams, error) {
	if f.Target != ForwardSMPPS {
		return SMPPSReceiptParams{}, fmt.Errorf("dlr: forward target is not smpps")
	}
	sourceTON, err := ParseAddrTON(f.SourceAddrTON)
	if err != nil {
		return SMPPSReceiptParams{}, err
	}
	sourceNPI, err := ParseAddrNPI(f.SourceAddrNPI)
	if err != nil {
		return SMPPSReceiptParams{}, err
	}
	destTON, err := ParseAddrTON(f.DestAddrTON)
	if err != nil {
		return SMPPSReceiptParams{}, err
	}
	destNPI, err := ParseAddrNPI(f.DestAddrNPI)
	if err != nil {
		return SMPPSReceiptParams{}, err
	}

	errCode := f.Err
	if errCode == "" {
		errCode = defaultSMPPSReceiptErr
	}
	return SMPPSReceiptParams{
		MsgID: f.QueueMsgID, MessageStatus: f.Status, Err: errCode, SubDate: f.SubDate,
		SourceAddr: f.SourceAddr, DestAddr: f.DestinationAddr,
		SourceAddrTON: sourceTON, SourceAddrNPI: sourceNPI,
		DestAddrTON: destTON, DestAddrNPI: destNPI,
	}, nil
}
