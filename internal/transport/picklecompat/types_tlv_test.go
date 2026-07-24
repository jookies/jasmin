package picklecompat

import (
	"encoding/json"
	"math/big"
	"testing"
)

// The bridge tuple()izes each marshaled entry verbatim onto pdu.custom_tlvs,
// so this JSON is a wire contract: [tag, length|null, type|null, value] with
// bytes values using the bridge's {"__type__": "bytes"} wrapper.
func TestSubmitSMCustomTLVMarshalTupleShape(t *testing.T) {
	length := 8
	cases := []struct {
		name string
		in   SubmitSMCustomTLV
		want string
	}{
		{
			name: "typed with digit-string value",
			in:   SubmitSMCustomTLV{Tag: big.NewInt(5121), Type: "Int8", Value: "1707167205648943173"},
			want: `[5121,null,"Int8","1707167205648943173"]`,
		},
		{
			name: "untyped nil value",
			in:   SubmitSMCustomTLV{Tag: big.NewInt(0x1400)},
			want: `[5120,null,null,null]`,
		},
		{
			name: "length hint carried",
			in:   SubmitSMCustomTLV{Tag: big.NewInt(5121), Length: &length, Type: "Int8", Value: "7"},
			want: `[5121,8,"Int8","7"]`,
		},
		{
			name: "bytes value uses bridge wrapper",
			in:   SubmitSMCustomTLV{Tag: big.NewInt(0x1403), Value: []byte("vendor")},
			want: `[5123,null,null,{"__type__":"bytes","base64":"dmVuZG9y"}]`,
		},
		{
			name: "arbitrary-precision tag stays unmasked",
			in:   SubmitSMCustomTLV{Tag: new(big.Int).SetUint64(0xFFFF1401), Value: "z"},
			want: `[4294906881,null,null,"z"]`,
		},
		{
			name: "float value stays a JSON number",
			in:   SubmitSMCustomTLV{Tag: big.NewInt(5121), Value: 3.7},
			want: `[5121,null,null,3.7]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("marshal = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSubmitSMCustomTLVMarshalNilTagErrors(t *testing.T) {
	if _, err := json.Marshal(SubmitSMCustomTLV{Value: "x"}); err == nil {
		t.Error("nil tag must not marshal")
	}
}
