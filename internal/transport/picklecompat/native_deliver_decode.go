package picklecompat

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/transport/gopickle"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// connectorType maps a jasminApi connector class to its _type (a class attribute
// the pickle state does not carry).
var connectorType = map[string]string{
	"HttpConnector":               "http",
	"SmppServerSystemIdConnector": "smpps",
	"Connector":                   "generic",
}

// DecodeRoutedDeliverSM natively projects a RoutedDeliverSmContent (the pickled
// dst-connectors list + the bare deliver_sm/data_sm PDU) into the typed thrower
// input — the native counterpart of Bridge.DecodeRoutedDeliverSM. Standard
// optionals are re-encoded from the PDU IR through the frozen wire decoder.
func (c *NativeCodec) DecodeRoutedDeliverSM(ctx context.Context, dstConnectors, body []byte) (RoutedDeliverSM, error) {
	if err := ctx.Err(); err != nil {
		return RoutedDeliverSM{}, err
	}
	if len(dstConnectors) == 0 || len(body) == 0 ||
		len(dstConnectors) > int(smppwire.DefaultMaxSize) || len(body) > int(smppwire.DefaultMaxSize) {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: pickle sizes %d/%d", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison, len(dstConnectors), len(body))
	}
	connectors, err := projectConnectorList(dstConnectors)
	if err != nil {
		return RoutedDeliverSM{}, err
	}
	if len(connectors) == 0 {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: empty connector list", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison)
	}

	value, err := gopickle.Load(body)
	if err != nil {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: unpickle deliver body: %v", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison, err)
	}
	object, ok := value.(gopickle.Object)
	if !ok || (object.Class.Name != "DeliverSM" && object.Class.Name != "DataSM") {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: root object is not a deliver pdu", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison)
	}
	state, ok := object.State.(gopickle.Dict)
	if !ok {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: deliver state is not a dict", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison)
	}
	params, ok := paramValue(state, "params").(gopickle.Dict)
	if !ok {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %w: deliver params are not a dict", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison)
	}

	body2, err := projectDeliverMandatory(params)
	if err != nil {
		return RoutedDeliverSM{}, err
	}
	if err := populateDeliverOptional(params, &body2); err != nil {
		return RoutedDeliverSM{}, err
	}
	customEntries, err := projectCustomTLVTuples(state)
	if err != nil {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %v", ErrInvalidRoutedDeliverSM, err)
	}
	customTLVs, err := decodeWireCustomTLVs(customEntries)
	if err != nil {
		return RoutedDeliverSM{}, fmt.Errorf("%w: %v", ErrInvalidRoutedDeliverSM, err)
	}
	return RoutedDeliverSM{Connectors: connectors, Body: body2, CustomTLVs: customTLVs}, nil
}

// projectConnectorList decodes the pickled connector list into MOConnectors.
func projectConnectorList(data []byte) ([]MOConnector, error) {
	value, err := gopickle.Load(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w: unpickle connectors: %v", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison, err)
	}
	list, ok := value.(gopickle.List)
	if !ok {
		return nil, fmt.Errorf("%w: %w: dst-connectors is not a list", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison)
	}
	connectors := make([]MOConnector, 0, len(list))
	for _, item := range list {
		object, ok := item.(gopickle.Object)
		if !ok {
			return nil, fmt.Errorf("%w: %w: connector is not an object", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison)
		}
		state, _ := object.State.(gopickle.Dict)
		connectors = append(connectors, MOConnector{
			CID:     stateString(state, "cid"),
			Type:    connectorType[object.Class.Name],
			BaseURL: stateString(state, "baseurl"),
			Method:  stateString(state, "method"),
		})
	}
	return connectors, nil
}

// projectDeliverMandatory projects the deliver pdu's mandatory params into an
// SMBody, re-encoding each enum object back to its wire byte (reuses the submit
// decode helpers).
func projectDeliverMandatory(params gopickle.Dict) (smppwire.SMBody, error) {
	body := smppwire.SMBody{
		ServiceType:        paramBytes(params, "service_type"),
		SourceAddress:      paramBytes(params, "source_addr"),
		DestinationAddress: paramBytes(params, "destination_addr"),
		ShortMessage:       paramBytes(params, "short_message"),
		ProtocolID:         paramByte(params, "protocol_id"),
		SMDefaultMessageID: paramByte(params, "sm_default_msg_id"),
	}
	var err error
	for _, field := range []struct {
		key   string
		enum  string
		table map[int]uint8
		dst   *byte
	}{
		{"source_addr_ton", "AddrTon", addrTONOrdinalToWire, &body.SourceAddressTON},
		{"source_addr_npi", "AddrNpi", addrNPIOrdinalToWire, &body.SourceAddressNPI},
		{"dest_addr_ton", "AddrTon", addrTONOrdinalToWire, &body.DestinationAddressTON},
		{"dest_addr_npi", "AddrNpi", addrNPIOrdinalToWire, &body.DestinationAddressNPI},
		{"priority_flag", "PriorityFlag", priorityFlagOrdinalToWire, &body.PriorityFlag},
		{"replace_if_present_flag", "ReplaceIfPresentFlag", replaceIfPresentOrdinalToWire, &body.ReplaceIfPresentFlag},
	} {
		if *field.dst, err = simpleEnumWire(params, field.key, field.enum, field.table); err != nil {
			return smppwire.SMBody{}, wrapRoutedPoison(err)
		}
	}
	if body.ESMClass, err = esmClassWire(params); err != nil {
		return smppwire.SMBody{}, wrapRoutedPoison(err)
	}
	if body.RegisteredDelivery, err = regDeliveryWire(params); err != nil {
		return smppwire.SMBody{}, wrapRoutedPoison(err)
	}
	if body.DataCoding, err = dataCodingWire(params); err != nil {
		return smppwire.SMBody{}, wrapRoutedPoison(err)
	}
	return body, nil
}

// populateDeliverOptional re-encodes the PDU IR's standard optionals and lets
// the frozen wire decoder restore the typed body fields.
func populateDeliverOptional(params gopickle.Dict, body *smppwire.SMBody) error {
	optionals, err := projectOptionalTLVs(params)
	if err != nil {
		return wrapRoutedPoison(err)
	}
	var section []byte
	for _, option := range optionals {
		if len(option.Value) > 0xffff {
			return wrapRoutedPoison(fmt.Errorf("optional %#04x length %d exceeds 65535", option.Tag, len(option.Value)))
		}
		header := make([]byte, 4)
		binary.BigEndian.PutUint16(header[:2], option.Tag)
		binary.BigEndian.PutUint16(header[2:], uint16(len(option.Value)))
		section = append(section, header...)
		section = append(section, option.Value...)
	}
	if err := smppwire.DecodeOptionalSection(section, body); err != nil {
		return wrapRoutedPoison(err)
	}
	return nil
}

func wrapRoutedPoison(err error) error {
	return fmt.Errorf("%w: %w: %v", ErrInvalidRoutedDeliverSM, ErrSubmitSMPoison, err)
}

// stateString reads a Str field off a pickled object's state dict.
func stateString(state gopickle.Dict, key string) string {
	if state == nil {
		return ""
	}
	if s, ok := paramValue(state, key).(gopickle.Str); ok {
		return string(s)
	}
	return ""
}

// paramUint reads a non-negative int param (false when absent/None/negative).
func paramUint(params gopickle.Dict, key string) (uint64, bool) {
	if i, ok := paramValue(params, key).(gopickle.Int); ok && i >= 0 {
		return uint64(i), true
	}
	return 0, false
}
