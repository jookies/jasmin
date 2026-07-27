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

// RepickleRoutablePDU projects a DeliverSmContent body (pickled
// RoutableDeliverSm) into the RoutedDeliverSmContent body (pickled bare PDU),
// exactly like RouterPB repickles routable.pdu before throwing.
func (b *Bridge) RepickleRoutablePDU(ctx context.Context, routable []byte) ([]byte, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: nil bridge", ErrInvalidRouterEncode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(routable) == 0 {
		return nil, fmt.Errorf("%w: empty routable", ErrInvalidRouterEncode)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	request := bridgeRequest{Action: "repickle_routable_pdu", Data: base64.StdEncoding.EncodeToString(routable)}
	if err := json.NewEncoder(b.stdin).Encode(request); err != nil {
		return nil, fmt.Errorf("send repickle request: %w", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return nil, fmt.Errorf("read repickle response: %w", err)
	}
	if response.Status != "ok" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRouterEncode, response.Message)
	}
	return base64.StdEncoding.DecodeString(response.Data)
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
