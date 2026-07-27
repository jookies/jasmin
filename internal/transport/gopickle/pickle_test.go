package gopickle

import (
	"bytes"
	"reflect"
	"testing"
)

// roundTripCases exercise every IR shape across the int-encoding boundaries.
func roundTripCases() []Value {
	return []Value{
		None{}, Bool(true), Bool(false),
		Int(0), Int(1), Int(255), Int(256), Int(65535), Int(65536),
		Int(-1), Int(-2147483648), Int(2147483647),
		Float(0), Float(3.14), Float(-1.5),
		Str(""), Str("hello"), Str("héllo unicode ☃"),
		Bytes(nil), Bytes([]byte("ABC")), Bytes([]byte{0x00, 0xFF, 0x80}),
		List{}, List{Int(1), Str("a"), Bytes([]byte("b"))},
		Tuple{}, Tuple{Int(1)}, Tuple{Int(1), Int(2)}, Tuple{Int(1), Int(2), Int(3)},
		Tuple{Int(1), Int(2), Int(3), Int(4)},
		Dict{}, Dict{{Str("k"), Int(1)}, {Str("k2"), Bytes([]byte("v"))}},
		Global{Module: "m", Name: "n"},
		Reduce{Callable: Global{Module: "smpp.pdu.pdu_types", Name: "CommandId"}, Args: Tuple{Int(4)}},
		Object{Class: Global{Module: "smpp.pdu.operations", Name: "SubmitSM"},
			State: Dict{{Str("seqNum"), Int(7)}, {Str("params"), Dict{}}}},
	}
}

// TestDumpLoadRoundTripIsStable proves Load∘Dump is a faithful inverse at the
// byte level: re-dumping the loaded value reproduces the same pickle. This
// sidesteps nil-vs-empty-slice equality while still proving fidelity.
func TestDumpLoadRoundTripIsStable(t *testing.T) {
	for _, c := range roundTripCases() {
		first, err := Dump(c)
		if err != nil {
			t.Fatalf("dump %#v: %v", c, err)
		}
		loaded, err := Load(first)
		if err != nil {
			t.Fatalf("load %#v: %v", c, err)
		}
		second, err := Dump(loaded)
		if err != nil {
			t.Fatalf("re-dump %#v: %v", loaded, err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("round-trip not byte-stable for %#v:\n  1: %x\n  2: %x", c, first, second)
		}
	}
}

// TestLoadReconstructsStructure checks a few decoded shapes explicitly.
func TestLoadReconstructsStructure(t *testing.T) {
	data, err := Dump(Object{
		Class: Global{Module: "smpp.pdu.operations", Name: "SubmitSM"},
		State: Dict{{Str("seqNum"), Int(7)}, {Str("source_addr"), Bytes([]byte("111"))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := got.(Object)
	if !ok {
		t.Fatalf("want Object, got %T", got)
	}
	if obj.Class.Name != "SubmitSM" {
		t.Fatalf("class = %+v", obj.Class)
	}
	state, ok := obj.State.(Dict)
	if !ok {
		t.Fatalf("state is %T", obj.State)
	}
	seq, _ := state.Get("seqNum")
	if !reflect.DeepEqual(seq, Int(7)) {
		t.Fatalf("seqNum = %#v", seq)
	}
	src, _ := state.Get("source_addr")
	if !reflect.DeepEqual(src, Bytes([]byte("111"))) {
		t.Fatalf("source_addr = %#v", src)
	}
}
