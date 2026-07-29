package picklecompat

import (
	"context"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// Codec is the pickle encode/decode surface the gateway runtime uses. Both the
// Python subprocess bridge (*Bridge) and the native Go codec (*NativeCodec)
// satisfy it, so the runtime selects between them by config (pickle_codec).
type Codec interface {
	DecodeSubmitSM(ctx context.Context, data []byte) (smppwire.SubmitSMBody, []tlv.TLV, error)
	DecodeSubmitSMChain(ctx context.Context, data []byte) ([]SubmitSMChainPart, error)
	EncodeSubmitSM(ctx context.Context, request SubmitSMEncodeRequest) (SubmitSMEncodeResult, error)
	EncodeSubmitSMResponse(ctx context.Context, commandStatus, sequence uint32, messageID []byte) ([]byte, error)
	EncodeRoutableDeliverSM(ctx context.Context, wire []byte, cid string) ([]byte, error)
	EncodeRoutableDeliverPDU(ctx context.Context, pdu smppwire.PDU, cid string) ([]byte, error)
	RepickleRoutablePDU(ctx context.Context, routable []byte) ([]byte, RoutableFields, error)
	EncodeConnectorList(ctx context.Context, connectors []MOConnectorSpec) ([]byte, error)
	DecodeRoutedDeliverSM(ctx context.Context, dstConnectors, body []byte) (RoutedDeliverSM, error)
	Ping(ctx context.Context) error
	Close() error
}

// Compile-time proof both implementations satisfy the runtime surface.
var (
	_ Codec = (*Bridge)(nil)
	_ Codec = (*NativeCodec)(nil)
)

// Ping is a no-op liveness probe: the native codec has no subprocess, so it is
// always ready (only a cancelled context fails). Satisfies the /health probe.
func (c *NativeCodec) Ping(ctx context.Context) error { return ctx.Err() }

// Close is a no-op: the native codec owns no OS resources.
func (c *NativeCodec) Close() error { return nil }
