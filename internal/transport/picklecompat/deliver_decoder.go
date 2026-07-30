package picklecompat

import (
	"encoding/json"
	"errors"

	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

var ErrInvalidRoutedDeliverSM = errors.New("invalid routed deliver_sm envelope")

// MOConnector is one destination connector from the routed content's
// dst-connectors header (the legacy HttpConnector projection).
type MOConnector struct {
	CID     string `json:"cid"`
	Type    string `json:"type"`
	BaseURL string `json:"baseurl"`
	Method  string `json:"method"`
}

// RoutedDeliverSM is the typed projection of a RoutedDeliverSmContent: the
// destination connectors, the deliver_sm as a canonical wire body (typed
// standard optionals and captured vendor TLVs decoded by the frozen codec),
// the PDU's custom_tlvs tuples, and the thrower-visible validity string
// (str() of the decoded datetime — not the SMPP wire text).
type RoutedDeliverSM struct {
	Connectors   []MOConnector
	Body         smppwire.SMBody
	CustomTLVs   []tlv.TLV
	ValidityText string
}

type routedDeliverWire struct {
	ServiceType          Bytes             `json:"service_type"`
	SourceAddrTON        uint8             `json:"source_addr_ton"`
	SourceAddrNPI        uint8             `json:"source_addr_npi"`
	SourceAddr           Bytes             `json:"source_addr"`
	DestAddrTON          uint8             `json:"dest_addr_ton"`
	DestAddrNPI          uint8             `json:"dest_addr_npi"`
	DestinationAddr      Bytes             `json:"destination_addr"`
	ESMClass             uint8             `json:"esm_class"`
	ProtocolID           uint8             `json:"protocol_id"`
	PriorityFlag         uint8             `json:"priority_flag"`
	RegisteredDelivery   uint8             `json:"registered_delivery"`
	ReplaceIfPresentFlag uint8             `json:"replace_if_present_flag"`
	DataCoding           uint8             `json:"data_coding"`
	SMDefaultMessageID   uint8             `json:"sm_default_msg_id"`
	ShortMessage         Bytes             `json:"short_message"`
	OptionalSection      Bytes             `json:"optional_section"`
	CustomTLVs           []json.RawMessage `json:"custom_tlvs"`
	ValidityStr          string            `json:"validity_str"`
	Connectors           []MOConnector     `json:"connectors"`
}
