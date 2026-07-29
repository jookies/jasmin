package picklecompat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/gopickle"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// nativeNow is the clock for the RoutableDeliverSm datetime stamp; overridable
// in tests. The datetime value is not compared by the semantic differential.
var nativeNow = time.Now

// EncodeRoutableDeliverSM natively builds the pickled RoutableDeliverSm the MO
// router consumes: it decodes the received deliver_sm/data_sm wire, rebuilds the
// smpp.pdu object, and wraps it with the source Connector and a datetime — the
// native counterpart of Bridge.EncodeRoutableDeliverSM.
func (c *NativeCodec) EncodeRoutableDeliverSM(ctx context.Context, wire []byte, cid string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pdu, err := smppwire.Decode(wire)
	if err != nil {
		return nil, fmt.Errorf("%w: decode deliver wire: %v", ErrInvalidRouterEncode, err)
	}
	return encodeRoutableDeliverPDU(ctx, pdu, cid)
}

// EncodeRoutableDeliverPDU natively builds the RoutableDeliverSm pickle from
// an already decoded PDU.
func (c *NativeCodec) EncodeRoutableDeliverPDU(ctx context.Context, pdu smppwire.PDU, cid string) ([]byte, error) {
	return encodeRoutableDeliverPDU(ctx, pdu, cid)
}

func encodeRoutableDeliverPDU(ctx context.Context, pdu smppwire.PDU, cid string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cid == "" {
		return nil, fmt.Errorf("%w: empty cid", ErrInvalidDeliverEncode)
	}
	if pdu.SM == nil {
		return nil, fmt.Errorf("%w: deliver PDU has no mandatory body", ErrInvalidDeliverEncode)
	}
	className := "DeliverSM"
	commandID := commandIDDeliverSM
	if pdu.Header.CommandID == smppwire.CommandDataSM {
		className = "DataSM"
		commandID = commandIDDataSM
	}
	params, err := deliverParams(pdu.SM, className)
	if err != nil {
		return nil, err
	}
	deliver := gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.operations", Name: className},
		State: gopickle.Dict{
			{Key: gopickle.Str("id"), Value: smppEnum("CommandId", commandID)},
			{Key: gopickle.Str("seqNum"), Value: gopickle.Int(int64(pdu.Header.SequenceNumber))},
			{Key: gopickle.Str("status"), Value: smppEnum("CommandStatus", 1)},
			{Key: gopickle.Str("custom_tlvs"), Value: gopickle.List{}},
			{Key: gopickle.Str("params"), Value: params},
		},
	}
	routable := gopickle.Object{
		Class: gopickle.Global{Module: "jasmin.routing.Routables", Name: "RoutableDeliverSm"},
		State: gopickle.Dict{
			{Key: gopickle.Str("_tags"), Value: gopickle.List{}},
			{Key: gopickle.Str("pdu"), Value: deliver},
			{Key: gopickle.Str("connector"), Value: genericConnectorObject(cid)},
			{Key: gopickle.Str("datetime"), Value: datetimeValue(nativeNow())},
		},
	}
	return gopickle.Dump(routable)
}

// deliverParams projects a decoded SMBody into the smpp.pdu params dict, matching
// PDUEncoder().decode. Mandatory byte fields are bytes (b” when empty, not
// None); mandatory enum params become their objects. DataSM carries a 10-param
// subset (no short_message/protocol_id/priority/schedule/validity/replace/
// sm_default_msg_id); message_payload rides as an optional param when present.
func deliverParams(body *smppwire.SMBody, className string) (gopickle.Dict, error) {
	esm, err := esmClassFromWire(body.ESMClass)
	if err != nil {
		return nil, err
	}
	registered, err := regDeliveryFromWire(body.RegisteredDelivery)
	if err != nil {
		return nil, err
	}
	dataCoding, err := dataCodingFromWire(body.DataCoding)
	if err != nil {
		return nil, err
	}
	params := gopickle.Dict{
		{Key: gopickle.Str("source_addr"), Value: gopickle.Bytes(body.SourceAddress)},
		{Key: gopickle.Str("destination_addr"), Value: gopickle.Bytes(body.DestinationAddress)},
		{Key: gopickle.Str("service_type"), Value: gopickle.Bytes(body.ServiceType)},
		{Key: gopickle.Str("source_addr_ton"), Value: smppEnum("AddrTon", addrTONWireToOrdinal[body.SourceAddressTON])},
		{Key: gopickle.Str("source_addr_npi"), Value: smppEnum("AddrNpi", addrNPIWireToOrdinal[body.SourceAddressNPI])},
		{Key: gopickle.Str("dest_addr_ton"), Value: smppEnum("AddrTon", addrTONWireToOrdinal[body.DestinationAddressTON])},
		{Key: gopickle.Str("dest_addr_npi"), Value: smppEnum("AddrNpi", addrNPIWireToOrdinal[body.DestinationAddressNPI])},
		{Key: gopickle.Str("esm_class"), Value: esm},
		{Key: gopickle.Str("registered_delivery"), Value: registered},
		{Key: gopickle.Str("data_coding"), Value: dataCoding},
	}
	if className == "DeliverSM" {
		params = append(params,
			gopickle.DictItem{Key: gopickle.Str("short_message"), Value: gopickle.Bytes(body.ShortMessage)},
			gopickle.DictItem{Key: gopickle.Str("protocol_id"), Value: gopickle.Int(int64(body.ProtocolID))},
			gopickle.DictItem{Key: gopickle.Str("priority_flag"), Value: smppEnum("PriorityFlag", priorityFlagWireToOrdinal[body.PriorityFlag])},
			gopickle.DictItem{Key: gopickle.Str("schedule_delivery_time"), Value: gopickle.None{}},
			gopickle.DictItem{Key: gopickle.Str("validity_period"), Value: gopickle.None{}},
			gopickle.DictItem{Key: gopickle.Str("replace_if_present_flag"), Value: replaceFromWire(body.ReplaceIfPresentFlag)},
			gopickle.DictItem{Key: gopickle.Str("sm_default_msg_id"), Value: gopickle.Int(int64(body.SMDefaultMessageID))},
		)
	}
	if body.Optional.MessagePayload != nil {
		params = append(params, gopickle.DictItem{Key: gopickle.Str("message_payload"), Value: gopickle.Bytes(body.Optional.MessagePayload)})
	}
	return params, nil
}

// replaceFromWire builds the ReplaceIfPresentFlag object (wire 0 still decodes to
// DO_NOT_REPLACE on inbound, unlike the encode-side optional None).
func replaceFromWire(wire uint8) gopickle.Value {
	ordinal, ok := replaceIfPresentWireToOrdinal[wire]
	if !ok {
		return gopickle.None{}
	}
	return smppEnum("ReplaceIfPresentFlag", ordinal)
}

// esmClassFromWire builds the EsmClass object for an inbound wire byte from the
// wire->components table.
func esmClassFromWire(wire uint8) (gopickle.Value, error) {
	components, ok := esmClassWireToComponents[wire]
	if !ok {
		return nil, fmt.Errorf("%w: esm_class wire %d not decodable", ErrInvalidRouterEncode, wire)
	}
	fields := strings.SplitN(components, ":", 3)
	mode, _ := strconv.Atoi(fields[0])
	typ, _ := strconv.Atoi(fields[1])
	return gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: "EsmClass"},
		Args: gopickle.Tuple{
			smppEnum("EsmClassMode", mode),
			smppEnum("EsmClassType", typ),
			pySet(enumListFromOrdinals("EsmClassGsmFeatures", fields[2])),
		},
	}, nil
}

// regDeliveryFromWire builds the RegisteredDelivery object for an inbound wire
// byte from the wire->components table.
func regDeliveryFromWire(wire uint8) (gopickle.Value, error) {
	components, ok := regDeliveryWireToComponents[wire]
	if !ok {
		return nil, fmt.Errorf("%w: registered_delivery wire %d not decodable", ErrInvalidRouterEncode, wire)
	}
	fields := strings.SplitN(components, ":", 3)
	receipt, _ := strconv.Atoi(fields[0])
	intermediate := fields[2] == "1"
	return gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: "RegisteredDelivery"},
		Args: gopickle.Tuple{
			smppEnum("RegisteredDeliveryReceipt", receipt),
			pySet(enumListFromOrdinals("RegisteredDeliverySmeOriginatedAcks", fields[1])),
			gopickle.Bool(intermediate),
		},
	}, nil
}

// dataCodingFromWire builds the DataCoding pickle for an inbound deliver_sm's
// data_coding byte. It shares the submit-path builder (DEFAULT/RAW/GSM) so MO
// messages with any coding round-trip, wrapping failures as a router-encode error.
func dataCodingFromWire(wire uint8) (gopickle.Value, error) {
	value, err := dataCodingValue(wire)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRouterEncode, err)
	}
	return value, nil
}

// enumListFromOrdinals builds a List of Enum(ordinal) reduces from a comma-
// joined component string ("" -> empty list).
func enumListFromOrdinals(enum, joined string) gopickle.List {
	if joined == "" {
		return gopickle.List{}
	}
	parts := strings.Split(joined, ",")
	list := make(gopickle.List, 0, len(parts))
	for _, part := range parts {
		ordinal, _ := strconv.Atoi(part)
		list = append(list, smppEnum(enum, ordinal))
	}
	return list
}

// datetimeValue builds datetime.datetime(<10-byte state>): BE year(2) + month +
// day + hour + minute + second + BE microseconds(3), as Python's reduce.
func datetimeValue(t time.Time) gopickle.Value {
	microsecond := t.Nanosecond() / 1000
	packed := []byte{
		byte(t.Year() >> 8), byte(t.Year()),
		byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()),
		byte(microsecond >> 16), byte(microsecond >> 8), byte(microsecond),
	}
	return gopickle.Reduce{
		Callable: gopickle.Global{Module: "datetime", Name: "datetime"},
		Args:     gopickle.Tuple{gopickle.Bytes(packed)},
	}
}

// genericConnectorObject builds the jasmin Connector(cid) the routable wraps.
func genericConnectorObject(cid string) gopickle.Object {
	return gopickle.Object{
		Class: gopickle.Global{Module: "jasmin.routing.jasminApi", Name: "Connector"},
		State: gopickle.Dict{
			{Key: gopickle.Str("cid"), Value: gopickle.Str(cid)},
			{Key: gopickle.Str("_str"), Value: gopickle.Str("generic Connector")},
			{Key: gopickle.Str("_repr"), Value: gopickle.Str("<generic Connector>")},
		},
	}
}
