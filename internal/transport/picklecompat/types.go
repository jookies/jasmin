package picklecompat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
)

// Bytes handles the __type__: bytes wrapper from the bridge
type Bytes []byte

func (b *Bytes) UnmarshalJSON(data []byte) error {
	var wrapper struct {
		Type   string `json:"__type__"`
		Base64 string `json:"base64"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}
	if wrapper.Type != "bytes" {
		*b = nil
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(wrapper.Base64)
	if err != nil {
		return err
	}
	*b = decoded
	return nil
}

func (b Bytes) MarshalJSON() ([]byte, error) {
	wrapper := struct {
		Type   string `json:"__type__"`
		Base64 string `json:"base64"`
	}{
		Type:   "bytes",
		Base64: base64.StdEncoding.EncodeToString(b),
	}
	return json.Marshal(wrapper)
}

// PythonClass is a base for objects that include __class__ metadata
type PythonClass struct {
	ClassName string `json:"__class__"`
}

// SubmitSM maps to smpp.pdu.operations.SubmitSM
type SubmitSM struct {
	PythonClass
	Params struct {
		SourceAddr      Bytes   `json:"source_addr"`
		DestinationAddr Bytes   `json:"destination_addr"`
		ShortMessage    Bytes   `json:"short_message"`
		ServiceType     *string `json:"service_type"`
		ESMClass        *int    `json:"esm_class"`
		ProtocolID      *int    `json:"protocol_id"`
		PriorityFlag    *int    `json:"priority_flag"`
		DataCoding      *int    `json:"data_coding"`
	} `json:"params"`
}

// SubmitSMResp maps to smpp.pdu.operations.SubmitSMResp
type SubmitSMResp struct {
	PythonClass
	Params struct {
		MessageID Bytes `json:"message_id"`
	} `json:"params"`
}

// DeliverSM maps to smpp.pdu.operations.DeliverSM
type DeliverSM struct {
	PythonClass
	Params struct {
		SourceAddr      Bytes `json:"source_addr"`
		DestinationAddr Bytes `json:"destination_addr"`
		ShortMessage    Bytes `json:"short_message"`
	} `json:"params"`
}

// Content maps to jasmin.routing.Content.Content (common in AMQP)
type Content struct {
	PythonClass
	PDU any `json:"pdu"`
}

// SubmitSMEncodeRequest is the allowlisted input for the production
// protocol-2 SubmitSM encoder. It intentionally contains only fields accepted
// by the frozen legacy boundary.
type SubmitSMEncodeRequest struct {
	Sequence               int                 `json:"sequence"`
	SourceAddr             Bytes               `json:"source_addr"`
	DestinationAddr        Bytes               `json:"destination_addr"`
	ShortMessage           Bytes               `json:"short_message"`
	DataCoding             uint8               `json:"data_coding"`
	Priority               uint8               `json:"priority"`
	ScheduleAt             string              `json:"schedule_at,omitempty"`
	ValidityUntil          string              `json:"validity_until,omitempty"`
	RegisteredDelivery     bool                `json:"registered_delivery"`
	UDH                    bool                `json:"udh"`
	SAR                    *SubmitSMSAR        `json:"sar,omitempty"`
	CustomTLVs             []SubmitSMCustomTLV `json:"custom_tlvs,omitempty"`
	IncludeBill            bool                `json:"include_bill"`
	BillID                 string              `json:"bill_id"`
	UserID                 string              `json:"user_id"`
	Username               string              `json:"username"`
	SubmitSMAmount         float64             `json:"submit_sm_amount"`
	SubmitSMRespAmount     float64             `json:"submit_sm_resp_amount"`
	DecrementSubmitSMCount int                 `json:"decrement_submit_sm_count"`

	// Connector-config default PDU params (GAP 4).
	SourceAddrTON        uint8  `json:"source_addr_ton,omitempty"`
	SourceAddrNPI        uint8  `json:"source_addr_npi,omitempty"`
	DestAddrTON          uint8  `json:"dest_addr_ton,omitempty"`
	DestAddrNPI          uint8  `json:"dest_addr_npi,omitempty"`
	ServiceType          string `json:"service_type,omitempty"`
	ProtocolID           uint8  `json:"protocol_id,omitempty"`
	ReplaceIfPresentFlag uint8  `json:"replace_if_present_flag,omitempty"`
	SmDefaultMsgID       uint8  `json:"sm_default_msg_id,omitempty"`
	// RawPDU is set for SMPPs-originated submits. Unlike the HTTP-oriented
	// convenience fields above, every value here is the ESME's decoded wire
	// value and must survive routing unchanged (apart from fields an
	// interceptor deliberately rewrites).
	RawPDU *SubmitSMRawPDU `json:"raw_pdu,omitempty"`
}

// SubmitSMRawPDU is the wire-shaped subset accepted by both the native codec
// and the compatibility bridge for forwarding an ESME's submit_sm.
type SubmitSMRawPDU struct {
	ServiceType          Bytes                         `json:"service_type"`
	SourceAddrTON        uint8                         `json:"source_addr_ton"`
	SourceAddrNPI        uint8                         `json:"source_addr_npi"`
	SourceAddr           Bytes                         `json:"source_addr"`
	DestAddrTON          uint8                         `json:"dest_addr_ton"`
	DestAddrNPI          uint8                         `json:"dest_addr_npi"`
	DestinationAddr      Bytes                         `json:"destination_addr"`
	ESMClass             uint8                         `json:"esm_class"`
	ProtocolID           uint8                         `json:"protocol_id"`
	PriorityFlag         uint8                         `json:"priority_flag"`
	ScheduleDeliveryTime Bytes                         `json:"schedule_delivery_time"`
	ValidityPeriod       Bytes                         `json:"validity_period"`
	RegisteredDelivery   uint8                         `json:"registered_delivery"`
	ReplaceIfPresentFlag uint8                         `json:"replace_if_present_flag"`
	DataCoding           uint8                         `json:"data_coding"`
	SMDefaultMessageID   uint8                         `json:"sm_default_msg_id"`
	ShortMessage         Bytes                         `json:"short_message"`
	Optional             SubmitSMRawOptionalParameters `json:"optional"`
}

// SubmitSMRawOptionalParameters covers the standard optionals the frozen wire
// decoder retains. Pointer presence distinguishes an absent TLV from a zero
// value; Bytes likewise distinguish nil from a present empty octet string.
type SubmitSMRawOptionalParameters struct {
	SARMessageReference  *uint16                    `json:"sar_msg_ref_num,omitempty"`
	SARTotalSegments     *uint8                     `json:"sar_total_segments,omitempty"`
	SARSegmentSequence   *uint8                     `json:"sar_segment_seqnum,omitempty"`
	MoreMessagesToSend   *uint8                     `json:"more_messages_to_send,omitempty"`
	MessagePayload       Bytes                      `json:"message_payload,omitempty"`
	UserMessageReference *uint16                    `json:"user_message_reference,omitempty"`
	SourcePort           *uint16                    `json:"source_port,omitempty"`
	DestinationPort      *uint16                    `json:"destination_port,omitempty"`
	SourceAddrSubunit    *uint8                     `json:"source_addr_subunit,omitempty"`
	DestAddrSubunit      *uint8                     `json:"dest_addr_subunit,omitempty"`
	SourceSubaddress     *SubmitSMRawSubaddress     `json:"source_subaddress,omitempty"`
	DestSubaddress       *SubmitSMRawSubaddress     `json:"dest_subaddress,omitempty"`
	UserResponseCode     *uint8                     `json:"user_response_code,omitempty"`
	PayloadType          *uint8                     `json:"payload_type,omitempty"`
	PrivacyIndicator     *uint8                     `json:"privacy_indicator,omitempty"`
	LanguageIndicator    *uint8                     `json:"language_indicator,omitempty"`
	DisplayTime          *uint8                     `json:"display_time,omitempty"`
	SMSSignal            Bytes                      `json:"sms_signal,omitempty"`
	NumberOfMessages     *uint8                     `json:"number_of_messages,omitempty"`
	CallbackNum          *SubmitSMRawCallbackNumber `json:"callback_num,omitempty"`
}

type SubmitSMRawSubaddress struct {
	TypeTag uint8 `json:"type_tag"`
	Value   Bytes `json:"value"`
}

type SubmitSMRawCallbackNumber struct {
	DigitMode uint8 `json:"digit_mode"`
	TON       uint8 `json:"ton"`
	NPI       uint8 `json:"npi"`
	Digits    Bytes `json:"digits"`
}

type SubmitSMSAR struct {
	Reference uint16 `json:"reference"`
	Total     uint8  `json:"total"`
	Sequence  uint8  `json:"sequence"`
}

// SubmitSMCustomTLV is one per-message vendor TLV in the Python tuple shape
// (tag, length, type, value). It marshals as the 4-element JSON array the
// bridge tuple()izes verbatim onto pdu.custom_tlvs, so the legacy listener
// performs type resolution, rule validation, and wire encoding exactly as it
// does for Python-published submits. Order is wire order — never sort.
type SubmitSMCustomTLV struct {
	Tag    *big.Int // arbitrary precision, unmasked (Python int); wire masking is the encoder's job
	Length *int     // dead hint carried for tuple fidelity; Python never reads it
	Type   string   // "" = untyped (Python None), resolved from connector rules at submit time
	Value  any      // string, integer kinds, float64, bool, nil, []byte, or JSON-native map/slice
}

func (t SubmitSMCustomTLV) MarshalJSON() ([]byte, error) {
	if t.Tag == nil {
		return nil, fmt.Errorf("custom TLV has nil tag")
	}
	var typeField any
	if t.Type != "" {
		typeField = t.Type
	}
	value := t.Value
	if b, ok := value.([]byte); ok {
		value = Bytes(b) // reuse the bridge's {"__type__": "bytes"} wrapper
	}
	return json.Marshal([4]any{t.Tag, t.Length, typeField, value})
}

type SubmitSMEncodeResult struct {
	Body []byte
	Bill []byte
}
