package picklecompat

import (
	"encoding/json"
	"math"
	"math/big"

	"github.com/pumpitspace/synevyr/internal/transport/gopickle"
)

// customTLVList builds pdu.custom_tlvs — a list of (tag, length, type, value)
// tuples — from the request's custom TLVs, reproducing what the bridge builds
// via tuple(item) over each [tag, length, type, value] JSON entry. length and
// type are None when unset (the bridge's typeField-is-None / null-length rule).
func customTLVList(tlvs []SubmitSMCustomTLV) (gopickle.List, error) {
	if len(tlvs) == 0 {
		return nil, nil
	}
	list := make(gopickle.List, 0, len(tlvs))
	for _, item := range tlvs {
		if item.Tag == nil {
			return nil, poisonSubmitError("custom TLV has nil tag")
		}
		if !item.Tag.IsInt64() {
			return nil, poisonSubmitError("custom TLV tag %s exceeds int64", item.Tag)
		}
		var lengthValue gopickle.Value = gopickle.None{}
		if item.Length != nil {
			lengthValue = gopickle.Int(int64(*item.Length))
		}
		var typeValue gopickle.Value = gopickle.None{}
		if item.Type != "" {
			typeValue = gopickle.Str(item.Type)
		}
		valueValue, err := customTLVValueIR(item.Value)
		if err != nil {
			return nil, err
		}
		list = append(list, gopickle.Tuple{
			gopickle.Int(item.Tag.Int64()),
			lengthValue,
			typeValue,
			valueValue,
		})
	}
	return list, nil
}

// customTLVsState wraps the encoded list for the object state: the PDU always
// carries a custom_tlvs list (empty by default), so nil becomes an empty list.
func customTLVsState(list gopickle.List) gopickle.Value {
	if list == nil {
		return gopickle.List{}
	}
	return list
}

// customTLVValueIR maps a Go scalar custom-TLV value to its pickle IR. Objects
// and arrays are poison (the legacy wire encoder crashes on them), matching the
// decode-side tupleValueField allowlist.
func customTLVValueIR(value any) (gopickle.Value, error) {
	switch v := value.(type) {
	case nil:
		return gopickle.None{}, nil
	case string:
		return gopickle.Str(v), nil
	case bool:
		return gopickle.Bool(v), nil
	case []byte:
		return gopickle.Bytes(v), nil
	case int:
		return gopickle.Int(int64(v)), nil
	case int8:
		return gopickle.Int(int64(v)), nil
	case int16:
		return gopickle.Int(int64(v)), nil
	case int32:
		return gopickle.Int(int64(v)), nil
	case int64:
		return gopickle.Int(v), nil
	case uint8:
		return gopickle.Int(int64(v)), nil
	case uint16:
		return gopickle.Int(int64(v)), nil
	case uint32:
		return gopickle.Int(int64(v)), nil
	case uint:
		if uint64(v) > math.MaxInt64 {
			return nil, poisonSubmitError("custom TLV value %d exceeds int64", v)
		}
		return gopickle.Int(int64(v)), nil
	case uint64:
		if v > math.MaxInt64 {
			return nil, poisonSubmitError("custom TLV value %d exceeds int64", v)
		}
		return gopickle.Int(int64(v)), nil
	case float32:
		return gopickle.Float(float64(v)), nil
	case float64:
		return gopickle.Float(v), nil
	default:
		return nil, poisonSubmitError("custom TLV value type %T not encodable", value)
	}
}

// customTLVTuplesJSON projects a pickled custom_tlvs list into the bridge's
// [tag, length, type, value] JSON entries, which decodeWireCustomTLVs then
// validates and wire-encodes. It is the decode counterpart of customTLVList and
// is shared by the submit and deliver paths.
func customTLVTuplesJSON(list gopickle.List) ([]json.RawMessage, error) {
	if len(list) == 0 {
		return nil, nil
	}
	entries := make([]json.RawMessage, 0, len(list))
	for index, elem := range list {
		tuple, ok := elem.(gopickle.Tuple)
		if !ok || len(tuple) != 4 {
			return nil, poisonSubmitError("custom TLV %d is not a 4-tuple", index)
		}
		tag, ok := tuple[0].(gopickle.Int)
		if !ok {
			return nil, poisonSubmitError("custom TLV %d tag is not an int", index)
		}
		length, err := customTLVLengthGo(tuple[1])
		if err != nil {
			return nil, poisonSubmitError("custom TLV %d: %v", index, err)
		}
		typeName, err := customTLVTypeGo(tuple[2])
		if err != nil {
			return nil, poisonSubmitError("custom TLV %d: %v", index, err)
		}
		value, err := customTLVValueGo(tuple[3])
		if err != nil {
			return nil, poisonSubmitError("custom TLV %d: %v", index, err)
		}
		raw, err := json.Marshal(SubmitSMCustomTLV{
			Tag:    big.NewInt(int64(tag)),
			Length: length,
			Type:   typeName,
			Value:  value,
		})
		if err != nil {
			return nil, poisonSubmitError("custom TLV %d: marshal: %v", index, err)
		}
		entries = append(entries, raw)
	}
	return entries, nil
}

func customTLVLengthGo(value gopickle.Value) (*int, error) {
	if _, isNone := value.(gopickle.None); isNone {
		return nil, nil
	}
	integer, ok := value.(gopickle.Int)
	if !ok {
		return nil, poisonSubmitError("length is not int or None")
	}
	length := int(integer)
	return &length, nil
}

func customTLVTypeGo(value gopickle.Value) (string, error) {
	if _, isNone := value.(gopickle.None); isNone {
		return "", nil
	}
	text, ok := value.(gopickle.Str)
	if !ok {
		return "", poisonSubmitError("type is not str or None")
	}
	return string(text), nil
}

// customTLVValueGo mirrors customTLVValueIR in reverse for the scalar shapes
// tupleValueField accepts.
func customTLVValueGo(value gopickle.Value) (any, error) {
	switch v := value.(type) {
	case gopickle.None:
		return nil, nil
	case gopickle.Str:
		return string(v), nil
	case gopickle.Bool:
		return bool(v), nil
	case gopickle.Int:
		return int64(v), nil
	case gopickle.Float:
		return float64(v), nil
	case gopickle.Bytes:
		return []byte(v), nil
	default:
		return nil, poisonSubmitError("value type %T not decodable", value)
	}
}
