package mo

import (
	"errors"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// ErrNoContent reports a deliver_sm with neither a short_message nor a message_payload.
var ErrNoContent = errors.New("mo: deliver_sm has no content")

// SelectContent picks the MO message body from a deliver_sm, matching jasmin's
// deliverSmThrower precedence (routing/throwers.py): a non-empty short_message wins; else
// message_payload; else an empty-but-present short_message; else ErrNoContent. Presence is
// signalled by a non-nil slice (nil = the field is absent).
func SelectContent(shortMessage, messagePayload []byte) ([]byte, error) {
	switch {
	case len(shortMessage) > 0:
		return shortMessage, nil
	case messagePayload != nil:
		return messagePayload, nil
	case shortMessage != nil:
		return shortMessage, nil // present but empty
	default:
		return nil, ErrNoContent
	}
}

// DeliveryFromDeliverSM builds an MO Delivery from a routed deliver_sm for forwarding to an
// HttpConnector. It selects the content per SelectContent and maps source/destination plus
// the simple optionals (priority_flag, data_coding, validity_period), which a deliver_sm
// always carries. TLV forwarding (tlv_params / custom_tlvs) is populated separately by the
// caller from the PDU's optional and custom parameters.
func DeliveryFromDeliverSM(sm *smppwire.SMBody, msgID, originConnector, connectorURL, method string) (Delivery, error) {
	if sm == nil {
		return Delivery{}, fmt.Errorf("mo: nil deliver_sm body")
	}
	content, err := SelectContent(sm.ShortMessage, sm.Optional.MessagePayload)
	if err != nil {
		return Delivery{}, err
	}
	priority := sm.PriorityFlag
	coding := sm.DataCoding
	d := Delivery{
		MsgID:           msgID,
		From:            string(sm.SourceAddress),
		To:              string(sm.DestinationAddress),
		OriginConnector: originConnector,
		Content:         content,
		Priority:        &priority,
		Coding:          &coding,
		URL:             connectorURL,
		Method:          method,
	}
	if len(sm.ValidityPeriod) > 0 {
		d.Validity = string(sm.ValidityPeriod)
	}
	return d, nil
}
