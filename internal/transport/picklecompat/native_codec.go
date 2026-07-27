package picklecompat

import (
	"context"
	"errors"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/transport/gopickle"
)

// ErrNativeCodec wraps native-codec encode/decode failures.
var ErrNativeCodec = errors.New("native pickle codec")

// smpp.pdu CommandId enum ordinals (the pickled Enum value) for the PDUs the
// codec emits — from CommandIdEncoder (frozen).
const (
	commandIDSubmitSMResp = 9
	commandIDSubmitSM     = 8
	commandIDDeliverSM    = 10
	commandIDDataSM       = 26
)

// smppEnum builds the pickle form of a simple smpp.pdu enum member:
// EnumClass(ordinal) via REDUCE, matching Python's Enum __reduce_ex__.
func smppEnum(name string, ordinal int) gopickle.Value {
	return gopickle.Reduce{
		Callable: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: name},
		Args:     gopickle.Tuple{gopickle.Int(int64(ordinal))},
	}
}

// NativeCodec implements the pickle actions the gateway needs using the native
// Go protocol-2 codec (internal/transport/gopickle), with no Python subprocess.
// Its methods mirror *Bridge so it drops in where the bridge is used today; the
// object codecs land step by step (plan 010) and are proven `pickle.loads`-equal
// to the bridge before the gateway is switched over.
type NativeCodec struct{}

// NewNativeCodec returns a stateless native codec.
func NewNativeCodec() *NativeCodec { return &NativeCodec{} }

// EncodeConnectorList pickles the legacy jasminApi connector list for the
// RoutedDeliverSmContent dst-connectors header — the native counterpart of
// Bridge.EncodeConnectorList.
func (c *NativeCodec) EncodeConnectorList(ctx context.Context, connectors []MOConnectorSpec) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(connectors) == 0 {
		return nil, fmt.Errorf("%w: empty connector list", ErrInvalidRouterEncode)
	}
	items := make(gopickle.List, 0, len(connectors))
	for _, spec := range connectors {
		switch spec.Type {
		case "http":
			method := spec.Method
			if method == "" {
				method = "GET" // legacy HttpConnector default
			}
			items = append(items, httpConnectorObject(spec.CID, spec.URL, method))
		case "smpps":
			items = append(items, smppsConnectorObject(spec.SystemID))
		default:
			return nil, fmt.Errorf("%w: unknown connector type %q", ErrInvalidRouterEncode, spec.Type)
		}
	}
	return gopickle.Dump(items)
}

// httpConnectorObject builds the pickled jasmin HttpConnector object. The
// _str/_repr fields reproduce HttpConnector.__init__ (jasminApi.py) verbatim so
// the loaded object equals a legacy-constructed one; the state-dict key order
// matches the legacy pickle.
func httpConnectorObject(cid, url, method string) gopickle.Object {
	return gopickle.Object{
		Class: gopickle.Global{Module: "jasmin.routing.jasminApi", Name: "HttpConnector"},
		State: gopickle.Dict{
			{Key: gopickle.Str("cid"), Value: gopickle.Str(cid)},
			{Key: gopickle.Str("_str"), Value: gopickle.Str(fmt.Sprintf(
				"HttpConnector:\ncid = %s\nbaseurl = %s\nmethod = %s", cid, url, method))},
			{Key: gopickle.Str("_repr"), Value: gopickle.Str(fmt.Sprintf(
				"<HttpConnector (cid=%s, baseurl=%s, method=%s)>", cid, url, method))},
			{Key: gopickle.Str("baseurl"), Value: gopickle.Str(url)},
			{Key: gopickle.Str("method"), Value: gopickle.Str(method)},
		},
	}
}

// smppsConnectorObject builds the pickled jasmin SmppServerSystemIdConnector.
// Its ctor calls Connector.__init__(system_id) (so cid == system_id and _str/
// _repr use _type 'smpps'), then sets system_id.
func smppsConnectorObject(systemID string) gopickle.Object {
	return gopickle.Object{
		Class: gopickle.Global{Module: "jasmin.routing.jasminApi", Name: "SmppServerSystemIdConnector"},
		State: gopickle.Dict{
			{Key: gopickle.Str("cid"), Value: gopickle.Str(systemID)},
			{Key: gopickle.Str("_str"), Value: gopickle.Str("smpps Connector")},
			{Key: gopickle.Str("_repr"), Value: gopickle.Str("<smpps Connector>")},
			{Key: gopickle.Str("system_id"), Value: gopickle.Str(systemID)},
		},
	}
}
