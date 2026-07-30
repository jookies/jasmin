package picklecompat

import (
	"encoding/base64"
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

// decodeRoutableFields base64-decodes the bridge's routing-field view. A nil
// wire (older bridge) yields empty fields, so callers degrade to no content
// match rather than error.
// routableFieldsWire carries the decoded source/destination/content/tags the MO
// router needs for content filters, so dispatch does not decode a second time.
type routableFieldsWire struct {
	SourceAddr      string   `json:"source_addr"`
	DestinationAddr string   `json:"destination_addr"`
	ShortMessage    string   `json:"short_message"`
	Tags            []string `json:"tags"`
}

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
