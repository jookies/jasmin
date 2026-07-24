package picklecompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
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

// DecodeRoutedDeliverSM projects a routed MO content through the bridge: the
// dst-connectors header pickle and the deliver_sm body pickle become the
// typed thrower input. Poison shapes reject like the legacy thrower's
// pre-try crashes.
func (b *Bridge) DecodeRoutedDeliverSM(ctx context.Context, dstConnectors, body []byte) (RoutedDeliverSM, error) {
	if b == nil {
		return RoutedDeliverSM{}, transientSubmitError("nil bridge")
	}
	if err := ctx.Err(); err != nil {
		return RoutedDeliverSM{}, err
	}
	if len(dstConnectors) == 0 || len(body) == 0 ||
		len(dstConnectors) > int(smppwire.DefaultMaxSize) || len(body) > int(smppwire.DefaultMaxSize) {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: pickle sizes %d/%d",
			ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison, len(dstConnectors), len(body))
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	request := struct {
		Action     string `json:"action"`
		Connectors string `json:"connectors"`
		Data       string `json:"data"`
	}{
		Action:     "decode_routed_deliver_sm",
		Connectors: base64.StdEncoding.EncodeToString(dstConnectors),
		Data:       base64.StdEncoding.EncodeToString(body),
	}
	if err := json.NewEncoder(b.stdin).Encode(request); err != nil {
		return RoutedDeliverSM{}, transientSubmitError("send bridge request: %v", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return RoutedDeliverSM{}, transientSubmitError("read bridge response: %v", err)
	}
	if response.Status != "ok" {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: %s", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison, response.Message)
	}
	var wire routedDeliverWire
	if err := json.Unmarshal(response.Result, &wire); err != nil {
		return RoutedDeliverSM{}, transientSubmitError("decode projection: %v", err)
	}
	if len(wire.Connectors) == 0 {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: empty connector list", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison)
	}

	result := RoutedDeliverSM{
		Connectors:   wire.Connectors,
		ValidityText: wire.ValidityStr,
		Body: smppwire.SMBody{
			ServiceType:           cloneBytes(wire.ServiceType),
			SourceAddressTON:      wire.SourceAddrTON,
			SourceAddressNPI:      wire.SourceAddrNPI,
			SourceAddress:         cloneBytes(wire.SourceAddr),
			DestinationAddressTON: wire.DestAddrTON,
			DestinationAddressNPI: wire.DestAddrNPI,
			DestinationAddress:    cloneBytes(wire.DestinationAddr),
			ESMClass:              wire.ESMClass,
			ProtocolID:            wire.ProtocolID,
			PriorityFlag:          wire.PriorityFlag,
			RegisteredDelivery:    wire.RegisteredDelivery,
			ReplaceIfPresentFlag:  wire.ReplaceIfPresentFlag,
			DataCoding:            wire.DataCoding,
			SMDefaultMessageID:    wire.SMDefaultMessageID,
			ShortMessage:          cloneBytes(wire.ShortMessage),
		},
	}
	// The frozen codec decodes the re-encoded optional section: typed standard
	// optionals, vendor capture, and the legacy validation errors.
	if err := smppwire.DecodeOptionalSection(wire.OptionalSection, &result.Body); err != nil {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: %v", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison, err)
	}
	tuples, err := decodeWireCustomTLVs(wire.CustomTLVs)
	if err != nil {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %v", ErrInvalidRoutedDeliverSM, err)
	}
	result.CustomTLVs = tuples
	return result, nil
}
