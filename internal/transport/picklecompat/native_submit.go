package picklecompat

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/gopickle"
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
	if request.RawPDU != nil {
		params, err = rawSubmitParams(request.RawPDU)
		if err != nil {
			return SubmitSMEncodeResult{}, err
		}
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

// rawSubmitParams rebuilds the exact mandatory values and retained standard
// optionals decoded from an SMPPs-originated submit_sm. The HTTP path above
// intentionally applies Jasmin defaults; this path mirrors forwarding the
// ESME's existing PDU object.
func rawSubmitParams(raw *SubmitSMRawPDU) (gopickle.Dict, error) {
	if raw == nil {
		return nil, fmt.Errorf("%w: nil raw submit_sm", ErrNativeCodec)
	}
	esm, err := esmClassFromWire(raw.ESMClass)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNativeCodec, err)
	}
	registered, err := regDeliveryFromWire(raw.RegisteredDelivery)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNativeCodec, err)
	}
	dataCoding, err := dataCodingValue(raw.DataCoding)
	if err != nil {
		return nil, err
	}
	sourceTON, err := simpleEnumValue("AddrTon", addrTONWireToOrdinal, raw.SourceAddrTON)
	if err != nil {
		return nil, err
	}
	sourceNPI, err := simpleEnumValue("AddrNpi", addrNPIWireToOrdinal, raw.SourceAddrNPI)
	if err != nil {
		return nil, err
	}
	destTON, err := simpleEnumValue("AddrTon", addrTONWireToOrdinal, raw.DestAddrTON)
	if err != nil {
		return nil, err
	}
	destNPI, err := simpleEnumValue("AddrNpi", addrNPIWireToOrdinal, raw.DestAddrNPI)
	if err != nil {
		return nil, err
	}
	priority, err := simpleEnumValue("PriorityFlag", priorityFlagWireToOrdinal, raw.PriorityFlag)
	if err != nil {
		return nil, err
	}
	replace, ok := replaceIfPresentWireToOrdinal[raw.ReplaceIfPresentFlag]
	if !ok {
		return nil, fmt.Errorf("%w: replace_if_present_flag wire %d not encodable", ErrNativeCodec, raw.ReplaceIfPresentFlag)
	}
	schedule, err := rawSMPPTimeValue(raw.ScheduleDeliveryTime)
	if err != nil {
		return nil, fmt.Errorf("%w: schedule_delivery_time: %v", ErrNativeCodec, err)
	}
	validity, err := rawSMPPTimeValue(raw.ValidityPeriod)
	if err != nil {
		return nil, fmt.Errorf("%w: validity_period: %v", ErrNativeCodec, err)
	}

	params := gopickle.Dict{
		{Key: gopickle.Str("service_type"), Value: gopickle.Bytes(raw.ServiceType)},
		{Key: gopickle.Str("source_addr_ton"), Value: sourceTON},
		{Key: gopickle.Str("source_addr_npi"), Value: sourceNPI},
		{Key: gopickle.Str("source_addr"), Value: gopickle.Bytes(raw.SourceAddr)},
		{Key: gopickle.Str("dest_addr_ton"), Value: destTON},
		{Key: gopickle.Str("dest_addr_npi"), Value: destNPI},
		{Key: gopickle.Str("destination_addr"), Value: gopickle.Bytes(raw.DestinationAddr)},
		{Key: gopickle.Str("esm_class"), Value: esm},
		{Key: gopickle.Str("protocol_id"), Value: gopickle.Int(int64(raw.ProtocolID))},
		{Key: gopickle.Str("priority_flag"), Value: priority},
		{Key: gopickle.Str("schedule_delivery_time"), Value: schedule},
		{Key: gopickle.Str("validity_period"), Value: validity},
		{Key: gopickle.Str("registered_delivery"), Value: registered},
		{Key: gopickle.Str("replace_if_present_flag"), Value: smppEnum("ReplaceIfPresentFlag", replace)},
		{Key: gopickle.Str("data_coding"), Value: dataCoding},
		{Key: gopickle.Str("sm_default_msg_id"), Value: gopickle.Int(int64(raw.SMDefaultMessageID))},
		{Key: gopickle.Str("short_message"), Value: gopickle.Bytes(raw.ShortMessage)},
	}
	return appendRawSubmitOptionals(params, raw.Optional)
}

func appendRawSubmitOptionals(params gopickle.Dict, optional SubmitSMRawOptionalParameters) (gopickle.Dict, error) {
	if optional.UserMessageReference != nil {
		params = append(params, gopickle.DictItem{Key: gopickle.Str("user_message_reference"), Value: gopickle.Int(int64(*optional.UserMessageReference))})
	}
	if optional.SourcePort != nil {
		params = append(params, gopickle.DictItem{Key: gopickle.Str("source_port"), Value: gopickle.Int(int64(*optional.SourcePort))})
	}
	if optional.SourceAddrSubunit != nil {
		if *optional.SourceAddrSubunit > 4 {
			return nil, fmt.Errorf("%w: source_addr_subunit wire %d not encodable", ErrNativeCodec, *optional.SourceAddrSubunit)
		}
		params = append(params, gopickle.DictItem{
			Key:   gopickle.Str("source_addr_subunit"),
			Value: smppEnum("AddrSubunit", int(*optional.SourceAddrSubunit)+1),
		})
	}
	if optional.DestinationPort != nil {
		params = append(params, gopickle.DictItem{Key: gopickle.Str("destination_port"), Value: gopickle.Int(int64(*optional.DestinationPort))})
	}
	if optional.DestAddrSubunit != nil {
		if *optional.DestAddrSubunit > 4 {
			return nil, fmt.Errorf("%w: dest_addr_subunit wire %d not encodable", ErrNativeCodec, *optional.DestAddrSubunit)
		}
		params = append(params, gopickle.DictItem{
			Key:   gopickle.Str("dest_addr_subunit"),
			Value: smppEnum("AddrSubunit", int(*optional.DestAddrSubunit)+1),
		})
	}
	if optional.SARMessageReference != nil {
		params = append(params, gopickle.DictItem{Key: gopickle.Str("sar_msg_ref_num"), Value: gopickle.Int(int64(*optional.SARMessageReference))})
	}
	if optional.SARTotalSegments != nil {
		params = append(params, gopickle.DictItem{Key: gopickle.Str("sar_total_segments"), Value: gopickle.Int(int64(*optional.SARTotalSegments))})
	}
	if optional.SARSegmentSequence != nil {
		params = append(params, gopickle.DictItem{Key: gopickle.Str("sar_segment_seqnum"), Value: gopickle.Int(int64(*optional.SARSegmentSequence))})
	}
	if optional.MoreMessagesToSend != nil {
		if *optional.MoreMessagesToSend > 1 {
			return nil, fmt.Errorf("%w: more_messages_to_send wire %d not encodable", ErrNativeCodec, *optional.MoreMessagesToSend)
		}
		params = append(params, gopickle.DictItem{
			Key:   gopickle.Str("more_messages_to_send"),
			Value: smppEnum("MoreMessagesToSend", int(*optional.MoreMessagesToSend)+1),
		})
	}
	if optional.PayloadType != nil {
		if *optional.PayloadType > 1 {
			return nil, fmt.Errorf("%w: payload_type wire %d not encodable", ErrNativeCodec, *optional.PayloadType)
		}
		params = append(params, gopickle.DictItem{
			Key:   gopickle.Str("payload_type"),
			Value: smppEnum("PayloadType", int(*optional.PayloadType)+1),
		})
	}
	if optional.MessagePayload != nil {
		params = append(params, gopickle.DictItem{Key: gopickle.Str("message_payload"), Value: gopickle.Bytes(optional.MessagePayload)})
	}
	if optional.PrivacyIndicator != nil {
		if *optional.PrivacyIndicator > 3 {
			return nil, fmt.Errorf("%w: privacy_indicator wire %d not encodable", ErrNativeCodec, *optional.PrivacyIndicator)
		}
		params = append(params, gopickle.DictItem{
			Key:   gopickle.Str("privacy_indicator"),
			Value: smppEnum("PrivacyIndicator", int(*optional.PrivacyIndicator)+1),
		})
	}
	if optional.CallbackNum != nil {
		params = append(params, gopickle.DictItem{
			Key:   gopickle.Str("callback_num"),
			Value: rawCallbackNumber(*optional.CallbackNum),
		})
	}
	if optional.SourceSubaddress != nil {
		value, err := rawSubaddress(*optional.SourceSubaddress)
		if err != nil {
			return nil, fmt.Errorf("%w: source_subaddress: %v", ErrNativeCodec, err)
		}
		params = append(params, gopickle.DictItem{Key: gopickle.Str("source_subaddress"), Value: value})
	}
	if optional.DestSubaddress != nil {
		value, err := rawSubaddress(*optional.DestSubaddress)
		if err != nil {
			return nil, fmt.Errorf("%w: dest_subaddress: %v", ErrNativeCodec, err)
		}
		params = append(params, gopickle.DictItem{Key: gopickle.Str("dest_subaddress"), Value: value})
	}
	if optional.UserResponseCode != nil {
		params = append(params, gopickle.DictItem{
			Key: gopickle.Str("user_response_code"), Value: gopickle.Int(int64(*optional.UserResponseCode)),
		})
	}
	if optional.DisplayTime != nil {
		if *optional.DisplayTime > 2 {
			return nil, fmt.Errorf("%w: display_time wire %d not encodable", ErrNativeCodec, *optional.DisplayTime)
		}
		params = append(params, gopickle.DictItem{
			Key: gopickle.Str("display_time"), Value: smppEnum("DisplayTime", int(*optional.DisplayTime)+1),
		})
	}
	if optional.SMSSignal != nil {
		params = append(params, gopickle.DictItem{
			Key: gopickle.Str("sms_signal"), Value: gopickle.Bytes(optional.SMSSignal),
		})
	}
	if optional.NumberOfMessages != nil {
		if *optional.NumberOfMessages > 99 {
			return nil, fmt.Errorf("%w: number_of_messages %d not encodable", ErrNativeCodec, *optional.NumberOfMessages)
		}
		params = append(params, gopickle.DictItem{
			Key: gopickle.Str("number_of_messages"), Value: gopickle.Int(int64(*optional.NumberOfMessages)),
		})
	}
	if optional.LanguageIndicator != nil {
		if *optional.LanguageIndicator > 5 {
			return nil, fmt.Errorf("%w: language_indicator wire %d not encodable", ErrNativeCodec, *optional.LanguageIndicator)
		}
		params = append(params, gopickle.DictItem{
			Key:   gopickle.Str("language_indicator"),
			Value: smppEnum("LanguageIndicator", int(*optional.LanguageIndicator)+1),
		})
	}
	return params, nil
}

var subaddressTypeTagWireToOrdinal = map[uint8]int{
	0x80: 1,
	0x88: 2,
	0xa0: 3,
	0x00: 4,
}

func rawSubaddress(subaddress SubmitSMRawSubaddress) (gopickle.Value, error) {
	ordinal, known := subaddressTypeTagWireToOrdinal[subaddress.TypeTag]
	if !known {
		return nil, fmt.Errorf("type tag %#x not encodable", subaddress.TypeTag)
	}
	return gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: "Subaddress"},
		Args: gopickle.Tuple{
			smppEnum("SubaddressTypeTag", ordinal),
			gopickle.Bytes(subaddress.Value),
		},
	}, nil
}

func rawCallbackNumber(callback SubmitSMRawCallbackNumber) gopickle.Value {
	return gopickle.Object{
		Class: gopickle.Global{Module: "smpp.pdu.pdu_types", Name: "CallbackNum"},
		Args: gopickle.Tuple{
			smppEnum("CallbackNumDigitModeIndicator", int(callback.DigitMode)+1),
			smppEnum("AddrTon", addrTONWireToOrdinal[callback.TON]),
			smppEnum("AddrNpi", addrNPIWireToOrdinal[callback.NPI]),
			gopickle.Bytes(callback.Digits),
		},
	}
}

// rawSMPPTimeValue parses the 16-octet SMPP absolute/relative wire form into
// the same Python object PDUDecoder stores in SubmitSM.params.
func rawSMPPTimeValue(raw []byte) (gopickle.Value, error) {
	if len(raw) == 0 {
		return gopickle.None{}, nil
	}
	if len(raw) != 16 {
		return nil, fmt.Errorf("wire value length %d, want 16", len(raw))
	}
	component := func(start, stop int) (int, error) {
		value, err := strconv.Atoi(string(raw[start:stop]))
		if err != nil {
			return 0, fmt.Errorf("invalid digits %q", raw[start:stop])
		}
		return value, nil
	}
	values := make([]int, 7)
	for index, bounds := range [][2]int{{0, 2}, {2, 4}, {4, 6}, {6, 8}, {8, 10}, {10, 12}, {12, 13}} {
		value, err := component(bounds[0], bounds[1])
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	if raw[15] == 'R' {
		if values[6] != 0 {
			return nil, fmt.Errorf("relative tenths must be zero")
		}
		return gopickle.Object{
			Class: gopickle.Global{Module: "smpp.pdu.smpp_time", Name: "SMPPRelativeTime"},
			Args: gopickle.Tuple{
				gopickle.Int(int64(values[0])), gopickle.Int(int64(values[1])),
				gopickle.Int(int64(values[2])), gopickle.Int(int64(values[3])),
				gopickle.Int(int64(values[4])), gopickle.Int(int64(values[5])),
			},
		}, nil
	}
	if raw[15] != '+' && raw[15] != '-' {
		return nil, fmt.Errorf("invalid offset indicator %q", raw[15])
	}
	quarterHours, err := component(13, 15)
	if err != nil || quarterHours > 48 {
		return nil, fmt.Errorf("invalid quarter-hour offset %q", raw[13:15])
	}
	offset := quarterHours * 15 * 60
	if raw[15] == '-' {
		offset = -offset
	}
	parsed, err := time.ParseInLocation("060102150405",
		string(raw[:12]), time.FixedZone("", offset))
	if err != nil {
		return nil, err
	}
	parsed = parsed.Add(time.Duration(values[6]) * 100 * time.Millisecond)
	return awareDatetimeValue(parsed), nil
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
//
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
