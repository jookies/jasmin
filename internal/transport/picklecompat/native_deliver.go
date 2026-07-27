package picklecompat

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pumpitspace/jasmin/internal/transport/gopickle"
)

// RepickleRoutablePDU natively projects a DeliverSmContent body (pickled
// RoutableDeliverSm) into the RoutedDeliverSmContent body (pickled bare PDU) and
// returns the decoded routing fields — the native counterpart of
// Bridge.RepickleRoutablePDU. The pickle bytes differ from the bridge's (memo)
// but load to the same PDU; the routing fields are byte-identical.
func (c *NativeCodec) RepickleRoutablePDU(ctx context.Context, routable []byte) ([]byte, RoutableFields, error) {
	if err := ctx.Err(); err != nil {
		return nil, RoutableFields{}, err
	}
	if len(routable) == 0 {
		return nil, RoutableFields{}, fmt.Errorf("%w: empty routable", ErrInvalidRouterEncode)
	}
	value, err := gopickle.Load(routable)
	if err != nil {
		return nil, RoutableFields{}, fmt.Errorf("%w: unpickle routable: %v", ErrInvalidRouterEncode, err)
	}
	// getattr(routable, "pdu", routable): a RoutableDeliverSm wraps the pdu; a
	// bare pdu pickle is used as-is.
	pdu := value
	var routableState gopickle.Dict
	if object, ok := value.(gopickle.Object); ok {
		if state, ok := object.State.(gopickle.Dict); ok {
			if inner, found := state.Get("pdu"); found {
				pdu = inner
				routableState = state
			}
		}
	}
	pickle, err := gopickle.Dump(pdu)
	if err != nil {
		return nil, RoutableFields{}, err
	}
	return pickle, extractRoutableFields(pdu, routableState), nil
}

// extractRoutableFields reads source/destination/short_message off the pdu's
// params and the tags off the routable, for MO content-filter routing.
func extractRoutableFields(pdu gopickle.Value, routableState gopickle.Dict) RoutableFields {
	fields := RoutableFields{Tags: extractTags(routableState)}
	object, ok := pdu.(gopickle.Object)
	if !ok {
		return fields
	}
	state, ok := object.State.(gopickle.Dict)
	if !ok {
		return fields
	}
	params, ok := paramValue(state, "params").(gopickle.Dict)
	if !ok {
		return fields
	}
	fields.SourceAddr = bytesOrNil(paramValue(params, "source_addr"))
	fields.DestinationAddr = bytesOrNil(paramValue(params, "destination_addr"))
	fields.ShortMessage = bytesOrNil(paramValue(params, "short_message"))
	return fields
}

// extractTags renders the routable's _tags list the way the bridge does
// (str(getattr(t, "tag", t))). Empty/absent yields nil.
func extractTags(routableState gopickle.Dict) []string {
	if routableState == nil {
		return nil
	}
	list, ok := paramValue(routableState, "_tags").(gopickle.List)
	if !ok || len(list) == 0 {
		return nil
	}
	tags := make([]string, 0, len(list))
	for _, item := range list {
		tags = append(tags, tagString(item))
	}
	return tags
}

// tagString mirrors str(getattr(t, "tag", t)) for a pickled Tag or scalar.
func tagString(value gopickle.Value) string {
	if object, ok := value.(gopickle.Object); ok {
		if state, ok := object.State.(gopickle.Dict); ok {
			if inner, found := state.Get("tag"); found {
				return scalarString(inner)
			}
		}
	}
	return scalarString(value)
}

func scalarString(value gopickle.Value) string {
	switch typed := value.(type) {
	case gopickle.Str:
		return string(typed)
	case gopickle.Int:
		return strconv.FormatInt(int64(typed), 10)
	case gopickle.Bytes:
		return string(typed)
	default:
		return ""
	}
}

// bytesOrNil returns the field's bytes, or nil for empty/absent — matching the
// bridge's decodeRoutableFields, which maps an empty field to nil.
func bytesOrNil(value gopickle.Value) []byte {
	if b, ok := value.(gopickle.Bytes); ok && len(b) > 0 {
		return []byte(b)
	}
	return nil
}
