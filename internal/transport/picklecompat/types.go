package picklecompat

import (
	"encoding/base64"
	"encoding/json"
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
		SourceAddr      Bytes `json:"source_addr"`
		DestinationAddr Bytes `json:"destination_addr"`
		ShortMessage    Bytes `json:"short_message"`
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
