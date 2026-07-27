package picklecompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrInvalidDeliverEncode = errors.New("invalid RoutableDeliverSm encode")

// EncodeRoutableDeliverSM produces the pickled RoutableDeliverSm the legacy
// DeliverSmContent carries, from the deliver_sm's wire bytes. The bridge
// re-decodes the frame with smpp.pdu and wraps it exactly like the legacy
// listener, so the pickle is the legacy object by construction.
func (b *Bridge) EncodeRoutableDeliverSM(ctx context.Context, wire []byte, cid string) ([]byte, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: nil bridge", ErrInvalidDeliverEncode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(wire) == 0 || cid == "" {
		return nil, fmt.Errorf("%w: empty wire or cid", ErrInvalidDeliverEncode)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	request := struct {
		Action string `json:"action"`
		Data   string `json:"data"`
		CID    string `json:"cid"`
	}{Action: "encode_routable_deliver_sm", Data: base64.StdEncoding.EncodeToString(wire), CID: cid}
	if err := json.NewEncoder(b.stdin).Encode(request); err != nil {
		return nil, fmt.Errorf("send RoutableDeliverSm bridge request: %w", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return nil, fmt.Errorf("read RoutableDeliverSm bridge response: %w", err)
	}
	if response.Status != "ok" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidDeliverEncode, response.Message)
	}
	pickled, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, fmt.Errorf("decode RoutableDeliverSm body: %w", err)
	}
	return pickled, nil
}
