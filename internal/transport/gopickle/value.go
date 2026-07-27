// Package gopickle is a native Go implementation of the subset of Python's
// protocol-2 pickle format the gateway needs, replacing the pickle_bridge.py
// subprocess. It exposes a small value IR plus a Pickler (IR -> bytes) and an
// Unpickler (bytes -> IR). Object codecs (smpp.pdu PDUs, jasmin Routables,
// enums) live in the transport packages and convert between this IR and the
// domain structs.
//
// The encoder is memo-free: it never emits BINPUT/BINGET, so a value that Python
// would have memoized and back-referenced is simply re-emitted. That loads to an
// equal object graph (the gateway's differentials are semantic, not byte-exact),
// and keeps the writer simple. The decoder DOES handle Python's full memo and
// opcode set, since it must read legacy-produced pickles.
package gopickle

// Value is one node of the pickle value IR — a Go view of a Python object as it
// appears in a protocol-2 pickle.
type Value interface{ isValue() }

// None is Python None.
type None struct{}

// Bool is a Python bool.
type Bool bool

// Int is a Python int within int64 range (covers BININT1/2/4 and small LONG1).
type Int int64

// Float is a Python float (BINFLOAT, IEEE-754 big-endian).
type Float float64

// Str is a Python str (BINUNICODE, UTF-8 on the wire).
type Str string

// Bytes is a Python bytes object. Python protocol 2 pickles it via
// _codecs.encode(<latin1-decoded str>, 'latin1') (or builtins.bytes() when
// empty); the Pickler emits that form and the Unpickler collapses it back.
type Bytes []byte

// List is a Python list.
type List []Value

// Tuple is a Python tuple.
type Tuple []Value

// DictItem is one key/value pair; Dict preserves insertion order.
type DictItem struct {
	Key   Value
	Value Value
}

// Dict is a Python dict (ordered).
type Dict []DictItem

// Global names a Python global (module.name), e.g. an enum class or a callable.
type Global struct {
	Module string
	Name   string
}

// Reduce is callable(*args): the REDUCE opcode. Used for smpp.pdu enums
// (Enum(value)) and any other reconstructor. Args must be a Tuple.
type Reduce struct {
	Callable Value
	Args     Value
}

// Object is a class instance built via NEWOBJ(cls, *args) and optionally
// BUILD(state) — the dominant shape for smpp.pdu PDUs and jasmin objects.
// Most objects use empty Args + a State Dict applied to __dict__ (NEWOBJ then
// BUILD); some (EsmClass, RegisteredDelivery) carry their whole value in Args
// with no BUILD. Args nil encodes as an empty tuple; State nil emits no BUILD.
type Object struct {
	Class Global
	Args  Value
	State Value
}

func (None) isValue()   {}
func (Bool) isValue()   {}
func (Int) isValue()    {}
func (Float) isValue()  {}
func (Str) isValue()    {}
func (Bytes) isValue()  {}
func (List) isValue()   {}
func (Tuple) isValue()  {}
func (Dict) isValue()   {}
func (Global) isValue() {}
func (Reduce) isValue() {}
func (Object) isValue() {}

// Get returns the value for a string key in a Dict, or nil+false if absent. A
// convenience for object codecs decoding a params/state dict.
func (d Dict) Get(key string) (Value, bool) {
	for _, item := range d {
		if s, ok := item.Key.(Str); ok && string(s) == key {
			return item.Value, true
		}
	}
	return nil, false
}

// latin1Decode maps each byte to the rune of the same value (Python
// bytes.decode('latin1')), producing the str that pickles into the bytes.
func latin1Decode(b []byte) string {
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return string(runes)
}

// latin1Encode maps each rune (which must be 0-255) back to a byte
// (str.encode('latin1')). Reports false if a rune is out of range.
func latin1Encode(s string) ([]byte, bool) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0 || r > 0xFF {
			return nil, false
		}
		out = append(out, byte(r))
	}
	return out, true
}
