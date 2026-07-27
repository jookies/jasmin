package picklecompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrInvalidRouterEncode = errors.New("invalid router dispatch encode")

// MOConnectorSpec names one MO destination connector for the pickled
// dst-connectors header: type "http" (cid+url+method, validated by the legacy
// HttpConnector — dotted host/localhost/IP URLs only) or "smpps" (system_id).
type MOConnectorSpec struct {
	Type     string `json:"type"`
	CID      string `json:"cid,omitempty"`
	URL      string `json:"url,omitempty"`
	Method   string `json:"method,omitempty"`
	SystemID string `json:"system_id,omitempty"`
}

// RoutableFields are the MO routing fields the bridge decodes off a
// RoutableDeliverSm during repickle, so the router evaluates content filters
// without a second decode. Absent fields are nil.
type RoutableFields struct {
	SourceAddr      []byte
	DestinationAddr []byte
	ShortMessage    []byte
	Tags            []string
}

// RepickleRoutablePDU projects a DeliverSmContent body (pickled
// RoutableDeliverSm) into the RoutedDeliverSmContent body (pickled bare PDU),
// exactly like RouterPB repickles routable.pdu before throwing. It also returns
// the decoded routing fields (same round-trip) for MO content-filter routing;
// the returned pickle bytes are unaffected by the field extraction.
func (b *Bridge) RepickleRoutablePDU(ctx context.Context, routable []byte) ([]byte, RoutableFields, error) {
	if b == nil {
		return nil, RoutableFields{}, fmt.Errorf("%w: nil bridge", ErrInvalidRouterEncode)
	}
	if err := ctx.Err(); err != nil {
		return nil, RoutableFields{}, err
	}
	if len(routable) == 0 {
		return nil, RoutableFields{}, fmt.Errorf("%w: empty routable", ErrInvalidRouterEncode)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	request := bridgeRequest{Action: "repickle_routable_pdu", Data: base64.StdEncoding.EncodeToString(routable)}
	if err := json.NewEncoder(b.stdin).Encode(request); err != nil {
		return nil, RoutableFields{}, fmt.Errorf("send repickle request: %w", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return nil, RoutableFields{}, fmt.Errorf("read repickle response: %w", err)
	}
	if response.Status != "ok" {
		return nil, RoutableFields{}, fmt.Errorf("%w: %s", ErrInvalidRouterEncode, response.Message)
	}
	pickle, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, RoutableFields{}, err
	}
	fields, err := decodeRoutableFields(response.Fields)
	if err != nil {
		return nil, RoutableFields{}, err
	}
	return pickle, fields, nil
}

// decodeRoutableFields base64-decodes the bridge's routing-field view. A nil
// wire (older bridge) yields empty fields, so callers degrade to no content
// match rather than error.
func decodeRoutableFields(wire *routableFieldsWire) (RoutableFields, error) {
	if wire == nil {
		return RoutableFields{}, nil
	}
	decode := func(name, encoded string) ([]byte, error) {
		if encoded == "" {
			return nil, nil
		}
		value, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidRouterEncode, name, err)
		}
		return value, nil
	}
	source, err := decode("source_addr", wire.SourceAddr)
	if err != nil {
		return RoutableFields{}, err
	}
	destination, err := decode("destination_addr", wire.DestinationAddr)
	if err != nil {
		return RoutableFields{}, err
	}
	message, err := decode("short_message", wire.ShortMessage)
	if err != nil {
		return RoutableFields{}, err
	}
	return RoutableFields{
		SourceAddr:      source,
		DestinationAddr: destination,
		ShortMessage:    message,
		Tags:            wire.Tags,
	}, nil
}

// EncodeConnectorList pickles the legacy jasminApi connector list for the
// RoutedDeliverSmContent dst-connectors header.
func (b *Bridge) EncodeConnectorList(ctx context.Context, connectors []MOConnectorSpec) ([]byte, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: nil bridge", ErrInvalidRouterEncode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(connectors) == 0 {
		return nil, fmt.Errorf("%w: empty connector list", ErrInvalidRouterEncode)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	request := bridgeRequest{Action: "encode_connector_list", Result: connectors}
	if err := json.NewEncoder(b.stdin).Encode(request); err != nil {
		return nil, fmt.Errorf("send connector-list request: %w", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return nil, fmt.Errorf("read connector-list response: %w", err)
	}
	if response.Status != "ok" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRouterEncode, response.Message)
	}
	return base64.StdEncoding.DecodeString(response.Data)
}
