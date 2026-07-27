package picklecompat

import (
	"context"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/transport/gopickle"
)

// EncodeSubmitSM builds the pickled smpp.pdu SubmitSM body — the native
// counterpart of Bridge.EncodeSubmitSM. It reproduces the object graph
// SubmitSM(**kwargs) produces: the 17 mandatory params (in order, None when
// unset) plus the SAR params when segmented, with id=submit_sm, status=ESME_ROK,
// and empty custom_tlvs.
//
// The SubmitSmBill (include_bill) rides along as a minimal loadable bill (the Go
// path reads the late-bill amount from the AMQP header, not the pickle). Schedule/
// validity times and custom TLVs are ported; only GSM/scheme data_codings remain
// a bridge-only edge (poisoned here).
func (c *NativeCodec) EncodeSubmitSM(ctx context.Context, request SubmitSMEncodeRequest) (SubmitSMEncodeResult, error) {
	if err := ctx.Err(); err != nil {
		return SubmitSMEncodeResult{}, err
	}
	if request.Sequence < 1 || request.Sequence > 0x7fffffff {
		return SubmitSMEncodeResult{}, fmt.Errorf("%w: sequence %d out of SMPP range", ErrNativeCodec, request.Sequence)
	}

	dataCoding, err := dataCodingValue(request.DataCoding)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}
	priority, err := simpleEnumValue("PriorityFlag", priorityFlagWireToOrdinal, request.Priority)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}
	sourceTON, err := simpleEnumValue("AddrTon", addrTONWireToOrdinal, request.SourceAddrTON)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}
	sourceNPI, err := simpleEnumValue("AddrNpi", addrNPIWireToOrdinal, request.SourceAddrNPI)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}
	destTON, err := simpleEnumValue("AddrTon", addrTONWireToOrdinal, request.DestAddrTON)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}
	destNPI, err := simpleEnumValue("AddrNpi", addrNPIWireToOrdinal, request.DestAddrNPI)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}

	replace, err := replaceValue(request.ReplaceIfPresentFlag)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}
	var scheduleValue gopickle.Value = gopickle.None{}
	if request.ScheduleAt != "" {
		if scheduleValue, err = scheduleValidityValue(request.ScheduleAt); err != nil {
			return SubmitSMEncodeResult{}, err
		}
	}
	var validityValue gopickle.Value = gopickle.None{}
	if request.ValidityUntil != "" {
		if validityValue, err = scheduleValidityValue(request.ValidityUntil); err != nil {
			return SubmitSMEncodeResult{}, err
		}
	}
	customTLVs, err := customTLVList(request.CustomTLVs)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}

	params := gopickle.Dict{
		{Key: gopickle.Str("source_addr"), Value: gopickle.Bytes(request.SourceAddr)},
		{Key: gopickle.Str("destination_addr"), Value: gopickle.Bytes(request.DestinationAddr)},
		{Key: gopickle.Str("short_message"), Value: gopickle.Bytes(request.ShortMessage)},
		{Key: gopickle.Str("data_coding"), Value: dataCoding},
		{Key: gopickle.Str("priority_flag"), Value: priority},
		{Key: gopickle.Str("esm_class"), Value: esmClassValue(request.UDH)},
		{Key: gopickle.Str("registered_delivery"), Value: registeredDeliveryValue(request.RegisteredDelivery)},
		{Key: gopickle.Str("source_addr_ton"), Value: sourceTON},
		{Key: gopickle.Str("source_addr_npi"), Value: sourceNPI},
		{Key: gopickle.Str("dest_addr_ton"), Value: destTON},
		{Key: gopickle.Str("dest_addr_npi"), Value: destNPI},
		{Key: gopickle.Str("service_type"), Value: optionalBytes(request.ServiceType)},
		{Key: gopickle.Str("protocol_id"), Value: optionalInt(int64(request.ProtocolID))},
		{Key: gopickle.Str("schedule_delivery_time"), Value: scheduleValue},
		{Key: gopickle.Str("validity_period"), Value: validityValue},
		{Key: gopickle.Str("replace_if_present_flag"), Value: replace},
		{Key: gopickle.Str("sm_default_msg_id"), Value: optionalInt(int64(request.SmDefaultMsgID))},
	}
	if request.SAR != nil {
		params = append(params,
			gopickle.DictItem{Key: gopickle.Str("sar_msg_ref_num"), Value: gopickle.Int(int64(request.SAR.Reference))},
			gopickle.DictItem{Key: gopickle.Str("sar_total_segments"), Value: gopickle.Int(int64(request.SAR.Total))},
			gopickle.DictItem{Key: gopickle.Str("sar_segment_seqnum"), Value: gopickle.Int(int64(request.SAR.Sequence))},
		)
	}

	obj := gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.operations", Name: "SubmitSM"},
		State: gopickle.Dict{
			{Key: gopickle.Str("id"), Value: smppEnum("CommandId", commandIDSubmitSM)},
			{Key: gopickle.Str("seqNum"), Value: gopickle.Int(int64(request.Sequence))},
			{Key: gopickle.Str("status"), Value: smppEnum("CommandStatus", 1)}, // ESME_ROK default
			{Key: gopickle.Str("custom_tlvs"), Value: customTLVsState(customTLVs)},
			{Key: gopickle.Str("params"), Value: params},
		},
	}
	body, err := gopickle.Dump(obj)
	if err != nil {
		return SubmitSMEncodeResult{}, err
	}
	result := SubmitSMEncodeResult{Body: body}
	if request.IncludeBill {
		bill, err := gopickle.Dump(submitSmBill(request))
		if err != nil {
			return SubmitSMEncodeResult{}, err
		}
		result.Bill = bill
	}
	return result, nil
}

// simpleEnumValue builds EnumClass(ordinal) for a wire byte via its wire->ordinal
// table, matching Encoder().decode(bytes([wire])) on the bridge.
func simpleEnumValue(enum string, table map[uint8]int, wire uint8) (gopickle.Value, error) {
	ordinal, ok := table[wire]
	if !ok {
		return nil, fmt.Errorf("%w: %s wire value %d not encodable", ErrNativeCodec, enum, wire)
	}
	return smppEnum(enum, ordinal), nil
}

// dataCodingValue reproduces DataCodingEncoder().decode(bytes([dc])) for every
// data_coding byte, matching the three shapes smpp.pdu produces:
//   - DEFAULT (0-10,13,14): DataCoding{DataCodingScheme.DEFAULT, DataCodingDefault(<coding>)}
//   - RAW (11,12,15-239):   DataCoding{DataCodingScheme.RAW, <int dc>}
//   - GSM_MESSAGE_CLASS (240-255): DataCoding{DataCodingScheme.GSM_MESSAGE_CLASS,
//     DataCodingGsmMsg(msgCoding, msgClass)} — a NEWOBJ with TUPLE2 args (no BUILD).
// The three ranges partition 0-255, so every byte is encodable (SMPPs-inbound
// submits carry unconstrained data_coding bytes, unlike the HTTP allowlist).
func dataCodingValue(dc uint8) (gopickle.Value, error) {
	var scheme int
	var schemeData gopickle.Value
	switch {
	case defaultOrdinalOK(dc):
		scheme = dataCodingDefaultSchemeOrdinal
		schemeData = smppEnum("DataCodingDefault", dataCodingDefaultOrdinal[dc])
	case dataCodingRawBytes[dc]:
		scheme = dataCodingSchemeRawOrdinal
		schemeData = gopickle.Int(int64(dc))
	default:
		pair, ok := gsmMsgOrdinals[dc]
		if !ok {
			return nil, fmt.Errorf("%w: data_coding %d unclassified", ErrNativeCodec, dc)
		}
		scheme = dataCodingSchemeGSMOrdinal
		schemeData = gopickle.Object{
			Class: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: "DataCodingGsmMsg"},
			Args: gopickle.Tuple{
				smppEnum("DataCodingGsmMsgCoding", pair.Coding),
				smppEnum("DataCodingGsmMsgClass", pair.Class),
			},
		}
	}
	return gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: "DataCoding"},
		State: gopickle.Dict{
			{Key: gopickle.Str("scheme"), Value: smppEnum("DataCodingScheme", scheme)},
			{Key: gopickle.Str("schemeData"), Value: schemeData},
		},
	}, nil
}

func defaultOrdinalOK(dc uint8) bool {
	_, ok := dataCodingDefaultOrdinal[dc]
	return ok
}

// esmClassValue builds the EsmClass the bridge constructs: STORE_AND_FORWARD/
// DEFAULT with an empty gsmFeatures set, or DEFAULT/DEFAULT with a {UDHI} set
// when the part carries a UDH. EsmClass carries its value in NEWOBJ args (no
// BUILD), and gsmFeatures is a Python set(list).
func esmClassValue(udh bool) gopickle.Value {
	mode := esmClassModeStoreForward
	features := gopickle.List{}
	if udh {
		mode = esmClassModeDefault
		features = gopickle.List{smppEnum("EsmClassGsmFeatures", esmClassGsmUDHI)}
	}
	return gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: "EsmClass"},
		Args: gopickle.Tuple{
			smppEnum("EsmClassMode", mode),
			smppEnum("EsmClassType", esmClassTypeDefault),
			pySet(features),
		},
	}
}

// registeredDeliveryValue builds the RegisteredDelivery the bridge constructs:
// receipt requested/none, an empty smeOriginatedAcks set, and False
// intermediateNotification (its NEWOBJ args).
func registeredDeliveryValue(requested bool) gopickle.Value {
	receipt := regDeliveryReceiptNone
	if requested {
		receipt = regDeliveryReceiptRequested
	}
	return gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: "RegisteredDelivery"},
		Args: gopickle.Tuple{
			smppEnum("RegisteredDeliveryReceipt", receipt),
			pySet(gopickle.List{}),
			gopickle.Bool(false),
		},
	}
}

// pySet builds a Python set(list) reduce.
func pySet(items gopickle.List) gopickle.Value {
	return gopickle.Reduce{
		Callable: gopickle.Global{Module: "builtins", Name: "set"},
		Args:     gopickle.Tuple{items},
	}
}

// optionalBytes is Bytes(s) when non-empty, else None — service_type is a
// COctetString, so SubmitSM stores it as bytes (not str); the bridge only sets
// it when truthy.
func optionalBytes(s string) gopickle.Value {
	if s == "" {
		return gopickle.None{}
	}
	return gopickle.Bytes([]byte(s))
}

// optionalInt is Int(v) when non-zero, else None — the bridge only sets
// protocol_id / sm_default_msg_id when truthy.
func optionalInt(v int64) gopickle.Value {
	if v == 0 {
		return gopickle.None{}
	}
	return gopickle.Int(v)
}

// replaceValue is ReplaceIfPresentFlag(ordinal) when the wire byte is non-zero,
// else None — the bridge only sets replace_if_present_flag when truthy.
func replaceValue(wire uint8) (gopickle.Value, error) {
	if wire == 0 {
		return gopickle.None{}, nil
	}
	ordinal, ok := replaceIfPresentWireToOrdinal[wire]
	if !ok {
		return nil, fmt.Errorf("%w: replace_if_present_flag wire %d not encodable", ErrNativeCodec, wire)
	}
	return smppEnum("ReplaceIfPresentFlag", ordinal), nil
}
