package picklecompat

import (
	"context"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/gopickle"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// DecodeSubmitSMChain natively decodes a pickled SubmitSM (+ nextPdu chain) into
// the canonical wire bodies + custom TLVs — the native counterpart of
// Bridge.DecodeSubmitSMChain. It projects the pickle into the same submitSMWire
// shape the bridge produces, then reuses the pure-Go buildSubmitPart.
func (c *NativeCodec) DecodeSubmitSMChain(ctx context.Context, data []byte) ([]SubmitSMChainPart, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > int(smppwire.DefaultMaxSize) {
		return nil, poisonSubmitError("pickle size %d", len(data))
	}
	value, err := gopickle.Load(data)
	if err != nil {
		return nil, poisonSubmitError("unpickle: %v", err)
	}
	var parts []SubmitSMChainPart
	node := value
	for node != nil {
		if _, isNone := node.(gopickle.None); isNone {
			break
		}
		obj, ok := node.(gopickle.Object)
		if !ok {
			return nil, poisonSubmitError("chain node is not a SubmitSM object (%T)", node)
		}
		if obj.Class.Module != "smpp.pdu.operations" || obj.Class.Name != "SubmitSM" {
			return nil, poisonSubmitError("chain node is not allowlisted SubmitSM (%s.%s)", obj.Class.Module, obj.Class.Name)
		}
		wire, next, err := projectSubmitNode(obj)
		if err != nil {
			return nil, err
		}
		body, tuples, err := buildSubmitPart(wire)
		if err != nil {
			return nil, err
		}
		parts = append(parts, SubmitSMChainPart{Body: body, CustomTLVs: tuples})
		node = next
	}
	if len(parts) == 0 {
		return nil, poisonSubmitError("no submit parts")
	}
	return parts, nil
}

// DecodeSubmitSM decodes a single-part submit (the first chain part).
func (c *NativeCodec) DecodeSubmitSM(ctx context.Context, data []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	parts, err := c.DecodeSubmitSMChain(ctx, data)
	if err != nil {
		return smppwire.SubmitSMBody{}, nil, err
	}
	return parts[0].Body, parts[0].CustomTLVs, nil
}

// projectSubmitNode projects one SubmitSM node's params into the submitSMWire
// shape (re-encoding each enum object to its wire byte) and returns the next
// chain node (nil when absent).
func projectSubmitNode(obj gopickle.Object) (submitSMWire, gopickle.Value, error) {
	state, ok := obj.State.(gopickle.Dict)
	if !ok {
		return submitSMWire{}, nil, poisonSubmitError("SubmitSM state is not a dict")
	}
	paramsValue, _ := state.Get("params")
	params, ok := paramsValue.(gopickle.Dict)
	if !ok {
		return submitSMWire{}, nil, poisonSubmitError("SubmitSM params is not a dict")
	}

	wire := submitSMWire{
		ServiceType:        paramBytes(params, "service_type"),
		SourceAddr:         paramBytes(params, "source_addr"),
		DestinationAddr:    paramBytes(params, "destination_addr"),
		ShortMessage:       paramBytes(params, "short_message"),
		ProtocolID:         paramByte(params, "protocol_id"),
		SMDefaultMessageID: paramByte(params, "sm_default_msg_id"),
	}
	var err error
	for _, field := range []struct {
		key   string
		enum  string
		table map[int]uint8
		dst   *uint8
	}{
		{"source_addr_ton", "AddrTon", addrTONOrdinalToWire, &wire.SourceAddrTON},
		{"source_addr_npi", "AddrNpi", addrNPIOrdinalToWire, &wire.SourceAddrNPI},
		{"dest_addr_ton", "AddrTon", addrTONOrdinalToWire, &wire.DestAddrTON},
		{"dest_addr_npi", "AddrNpi", addrNPIOrdinalToWire, &wire.DestAddrNPI},
		{"priority_flag", "PriorityFlag", priorityFlagOrdinalToWire, &wire.PriorityFlag},
		{"replace_if_present_flag", "ReplaceIfPresentFlag", replaceIfPresentOrdinalToWire, &wire.ReplaceIfPresentFlag},
	} {
		if *field.dst, err = simpleEnumWire(params, field.key, field.enum, field.table); err != nil {
			return submitSMWire{}, nil, err
		}
	}
	if wire.DataCoding, err = dataCodingWire(params); err != nil {
		return submitSMWire{}, nil, err
	}
	if wire.ESMClass, err = esmClassWire(params); err != nil {
		return submitSMWire{}, nil, err
	}
	if wire.RegisteredDelivery, err = regDeliveryWire(params); err != nil {
		return submitSMWire{}, nil, err
	}
	// schedule/validity times are not yet ported on encode; a legacy pickle
	// carrying them decodes here only when None (empty), else it is poison until
	// the time codec lands.
	if !paramIsNone(params, "schedule_delivery_time") || !paramIsNone(params, "validity_period") {
		return submitSMWire{}, nil, poisonSubmitError("schedule/validity time decoding not yet ported")
	}

	wire.OptionalTLVs, err = projectOptionalTLVs(params)
	if err != nil {
		return submitSMWire{}, nil, err
	}
	wire.CustomTLVs, err = projectCustomTLVTuples(state)
	if err != nil {
		return submitSMWire{}, nil, err
	}

	next, _ := state.Get("nextPdu")
	return wire, next, nil
}

// paramValue returns the params dict value for a key (nil if absent).
func paramValue(params gopickle.Dict, key string) gopickle.Value {
	v, _ := params.Get(key)
	return v
}

func paramIsNone(params gopickle.Dict, key string) bool {
	v := paramValue(params, key)
	if v == nil {
		return true
	}
	_, isNone := v.(gopickle.None)
	return isNone
}

// paramBytes reads a bytes param (empty when None/absent).
func paramBytes(params gopickle.Dict, key string) Bytes {
	if b, ok := paramValue(params, key).(gopickle.Bytes); ok {
		return Bytes(b)
	}
	return nil
}

// paramByte reads an int param as a byte (0 when None/absent) — the bridge's
// _raw_byte behaviour.
func paramByte(params gopickle.Dict, key string) uint8 {
	if i, ok := paramValue(params, key).(gopickle.Int); ok {
		return uint8(i)
	}
	return 0
}

// simpleEnumWire re-encodes a simple enum param (Enum(ordinal) reduce) to its
// wire byte via the ordinal->wire table; None/absent yields 0 (the bridge's
// _encoded_byte behaviour).
func simpleEnumWire(params gopickle.Dict, key, enum string, table map[int]uint8) (uint8, error) {
	value := paramValue(params, key)
	if value == nil {
		return 0, nil
	}
	if _, isNone := value.(gopickle.None); isNone {
		return 0, nil
	}
	ordinal, name, ok := enumReduceOrdinal(value)
	if !ok || name != enum {
		return 0, poisonSubmitError("%s is not a %s enum", key, enum)
	}
	wire, ok := table[ordinal]
	if !ok {
		return 0, poisonSubmitError("%s ordinal %d not decodable", key, ordinal)
	}
	return wire, nil
}

// enumReduceOrdinal extracts (ordinal, enumName) from an Enum(ordinal) reduce.
func enumReduceOrdinal(value gopickle.Value) (int, string, bool) {
	reduce, ok := value.(gopickle.Reduce)
	if !ok {
		return 0, "", false
	}
	global, ok := reduce.Callable.(gopickle.Global)
	if !ok || global.Module != "smpp.pdu.pdu_types" {
		return 0, "", false
	}
	args, ok := reduce.Args.(gopickle.Tuple)
	if !ok || len(args) != 1 {
		return 0, "", false
	}
	ordinal, ok := args[0].(gopickle.Int)
	if !ok {
		return 0, "", false
	}
	return int(ordinal), global.Name, true
}

// dataCodingWire re-encodes a pickled DataCoding (default scheme) to its wire
// byte. None yields 0; GSM/scheme codings are not yet ported.
func dataCodingWire(params gopickle.Dict) (uint8, error) {
	value := paramValue(params, "data_coding")
	if value == nil {
		return 0, nil
	}
	if _, isNone := value.(gopickle.None); isNone {
		return 0, nil
	}
	object, ok := value.(gopickle.Object)
	if !ok || object.Class.Name != "DataCoding" {
		return 0, poisonSubmitError("data_coding is not a DataCoding object")
	}
	state, ok := object.State.(gopickle.Dict)
	if !ok {
		return 0, poisonSubmitError("DataCoding state is not a dict")
	}
	scheme, _, ok := enumReduceOrdinal(paramValue(state, "scheme"))
	if !ok || scheme != dataCodingDefaultSchemeOrdinal {
		return 0, poisonSubmitError("non-default DataCoding scheme not yet ported")
	}
	schemeData, _, ok := enumReduceOrdinal(paramValue(state, "schemeData"))
	if !ok {
		return 0, poisonSubmitError("DataCoding schemeData missing")
	}
	for wire, ordinal := range dataCodingDefaultOrdinal {
		if ordinal == schemeData {
			return wire, nil
		}
	}
	return 0, poisonSubmitError("DataCoding schemeData ordinal %d not decodable", schemeData)
}

// esmClassWire re-encodes a pickled EsmClass (mode/type/gsmFeatures in NEWOBJ
// args) to its wire byte via the component table.
func esmClassWire(params gopickle.Dict) (uint8, error) {
	object, ok := paramValue(params, "esm_class").(gopickle.Object)
	if !ok || object.Class.Name != "EsmClass" {
		return 0, poisonSubmitError("esm_class is not an EsmClass object")
	}
	args, ok := object.Args.(gopickle.Tuple)
	if !ok || len(args) != 3 {
		return 0, poisonSubmitError("EsmClass args malformed")
	}
	mode, _, ok := enumReduceOrdinal(args[0])
	if !ok {
		return 0, poisonSubmitError("EsmClass mode malformed")
	}
	typ, _, ok := enumReduceOrdinal(args[1])
	if !ok {
		return 0, poisonSubmitError("EsmClass type malformed")
	}
	features, err := setOrdinals(args[2])
	if err != nil {
		return 0, poisonSubmitError("EsmClass gsmFeatures: %v", err)
	}
	key := fmt.Sprintf("%d:%d:%s", mode, typ, joinOrdinals(features))
	wire, ok := esmClassComponentsToWire[key]
	if !ok {
		return 0, poisonSubmitError("EsmClass %q not decodable", key)
	}
	return wire, nil
}

// regDeliveryWire re-encodes a pickled RegisteredDelivery (receipt/acks/
// intermediate in NEWOBJ args) to its wire byte via the component table.
func regDeliveryWire(params gopickle.Dict) (uint8, error) {
	object, ok := paramValue(params, "registered_delivery").(gopickle.Object)
	if !ok || object.Class.Name != "RegisteredDelivery" {
		return 0, poisonSubmitError("registered_delivery is not a RegisteredDelivery object")
	}
	args, ok := object.Args.(gopickle.Tuple)
	if !ok || len(args) != 3 {
		return 0, poisonSubmitError("RegisteredDelivery args malformed")
	}
	receipt, _, ok := enumReduceOrdinal(args[0])
	if !ok {
		return 0, poisonSubmitError("RegisteredDelivery receipt malformed")
	}
	acks, err := setOrdinals(args[1])
	if err != nil {
		return 0, poisonSubmitError("RegisteredDelivery acks: %v", err)
	}
	intermediate := 0
	if b, ok := args[2].(gopickle.Bool); ok && bool(b) {
		intermediate = 1
	}
	key := fmt.Sprintf("%d:%s:%d", receipt, joinOrdinals(acks), intermediate)
	wire, ok := regDeliveryComponentsToWire[key]
	if !ok {
		return 0, poisonSubmitError("RegisteredDelivery %q not decodable", key)
	}
	return wire, nil
}

// setOrdinals extracts the sorted enum ordinals from a Python set(list) reduce.
func setOrdinals(value gopickle.Value) ([]int, error) {
	reduce, ok := value.(gopickle.Reduce)
	if !ok {
		return nil, fmt.Errorf("not a set reduce")
	}
	args, ok := reduce.Args.(gopickle.Tuple)
	if !ok || len(args) != 1 {
		return nil, fmt.Errorf("set args malformed")
	}
	list, ok := args[0].(gopickle.List)
	if !ok {
		return nil, fmt.Errorf("set is not a list")
	}
	ordinals := make([]int, 0, len(list))
	for _, item := range list {
		ordinal, _, ok := enumReduceOrdinal(item)
		if !ok {
			return nil, fmt.Errorf("set member is not an enum")
		}
		ordinals = append(ordinals, ordinal)
	}
	sortInts(ordinals)
	return ordinals, nil
}
