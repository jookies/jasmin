package gopickle

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Decoder opcodes beyond the encoder's set — the Unpickler reads what Python
// emits (memo, short/8-byte variants, proto-3 bytes, proto-4 frame/global).
const (
	opFrame         = 0x95
	opLong1         = 0x8a
	opLong4         = 0x8b
	opShortBinUni   = 0x8c
	opBinUnicode8   = 0x8d
	opBinBytes      = 'B'
	opShortBinBytes = 'C'
	opBinBytes8     = 0x8e
	opAppend        = 'a'
	opSetItem       = 's'
	opBinPut        = 'q'
	opLongBinPut    = 'r'
	opMemoize       = 0x94
	opBinGet        = 'h'
	opLongBinGet    = 'j'
	opStackGlobal   = 0x93
)

// mark is the MARK sentinel on the stack.
type mark struct{}

func (mark) isValue() {}

// Load parses a protocol-2 (or lower proto-3/4 opcode) pickle into the value IR.
// It collapses the two Python bytes reducers (_codecs.encode / builtins.bytes)
// back to Bytes; other REDUCEs (enums, etc.) stay as Reduce for object codecs.
func Load(data []byte) (Value, error) {
	u := &unpickler{data: data, memo: map[int]Value{}}
	return u.run()
}

type unpickler struct {
	data  []byte
	pos   int
	stack []Value
	memo  map[int]Value
}

func (u *unpickler) errf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format+" (at byte %d)", append([]any{ErrPickle}, append(args, u.pos)...)...)
}

func (u *unpickler) push(v Value) { u.stack = append(u.stack, v) }

func (u *unpickler) pop() (Value, error) {
	if len(u.stack) == 0 {
		return nil, u.errf("pop from empty stack")
	}
	v := u.stack[len(u.stack)-1]
	u.stack = u.stack[:len(u.stack)-1]
	return v, nil
}

func (u *unpickler) top() (Value, error) {
	if len(u.stack) == 0 {
		return nil, u.errf("top of empty stack")
	}
	return u.stack[len(u.stack)-1], nil
}

// popMark returns the values pushed since the last MARK and removes them + the
// mark from the stack.
func (u *unpickler) popMark() ([]Value, error) {
	for i := len(u.stack) - 1; i >= 0; i-- {
		if _, ok := u.stack[i].(mark); ok {
			items := append([]Value(nil), u.stack[i+1:]...)
			u.stack = u.stack[:i]
			return items, nil
		}
	}
	return nil, u.errf("no MARK on stack")
}

func (u *unpickler) readByte() (byte, error) {
	if u.pos >= len(u.data) {
		return 0, u.errf("unexpected end of data")
	}
	b := u.data[u.pos]
	u.pos++
	return b, nil
}

func (u *unpickler) readN(n int) ([]byte, error) {
	if n < 0 || u.pos+n > len(u.data) {
		return nil, u.errf("unexpected end of data reading %d bytes", n)
	}
	b := u.data[u.pos : u.pos+n]
	u.pos += n
	return b, nil
}

func (u *unpickler) readLine() (string, error) {
	start := u.pos
	for u.pos < len(u.data) {
		if u.data[u.pos] == '\n' {
			s := string(u.data[start:u.pos])
			u.pos++
			return s, nil
		}
		u.pos++
	}
	return "", u.errf("unterminated line")
}

func (u *unpickler) run() (Value, error) {
	for {
		op, err := u.readByte()
		if err != nil {
			return nil, u.errf("no STOP opcode")
		}
		switch op {
		case opProto:
			if _, err := u.readByte(); err != nil {
				return nil, err
			}
		case opFrame:
			if _, err := u.readN(8); err != nil {
				return nil, err
			}
		case opStop:
			return u.pop()
		case opNone:
			u.push(None{})
		case opNewTrue:
			u.push(Bool(true))
		case opNewFalse:
			u.push(Bool(false))
		case opBinInt1:
			b, err := u.readByte()
			if err != nil {
				return nil, err
			}
			u.push(Int(b))
		case opBinInt2:
			b, err := u.readN(2)
			if err != nil {
				return nil, err
			}
			u.push(Int(binary.LittleEndian.Uint16(b)))
		case opBinInt:
			b, err := u.readN(4)
			if err != nil {
				return nil, err
			}
			u.push(Int(int32(binary.LittleEndian.Uint32(b))))
		case opLong1:
			n, err := u.readByte()
			if err != nil {
				return nil, err
			}
			raw, err := u.readN(int(n))
			if err != nil {
				return nil, err
			}
			v, err := decodeLong(raw)
			if err != nil {
				return nil, u.errf("%v", err)
			}
			u.push(Int(v))
		case opLong4:
			lb, err := u.readN(4)
			if err != nil {
				return nil, err
			}
			raw, err := u.readN(int(binary.LittleEndian.Uint32(lb)))
			if err != nil {
				return nil, err
			}
			v, err := decodeLong(raw)
			if err != nil {
				return nil, u.errf("%v", err)
			}
			u.push(Int(v))
		case opBinFloat:
			b, err := u.readN(8)
			if err != nil {
				return nil, err
			}
			u.push(Float(math.Float64frombits(binary.BigEndian.Uint64(b))))
		case opBinUnicode:
			if err := u.readUnicode(4); err != nil {
				return nil, err
			}
		case opShortBinUni:
			if err := u.readUnicode(1); err != nil {
				return nil, err
			}
		case opBinUnicode8:
			if err := u.readUnicode(8); err != nil {
				return nil, err
			}
		case opBinBytes:
			if err := u.readBytes(4); err != nil {
				return nil, err
			}
		case opShortBinBytes:
			if err := u.readBytes(1); err != nil {
				return nil, err
			}
		case opBinBytes8:
			if err := u.readBytes(8); err != nil {
				return nil, err
			}
		case opEmptyList:
			u.push(List{})
		case opEmptyDict:
			u.push(Dict{})
		case opEmptyTuple:
			u.push(Tuple{})
		case opMark:
			u.push(mark{})
		case opAppend:
			if err := u.appendOne(); err != nil {
				return nil, err
			}
		case opAppends:
			if err := u.appendMany(); err != nil {
				return nil, err
			}
		case opSetItem:
			if err := u.setOne(); err != nil {
				return nil, err
			}
		case opSetItems:
			if err := u.setMany(); err != nil {
				return nil, err
			}
		case opTuple1, opTuple2, opTuple3:
			n := int(op - opTuple1 + 1)
			if len(u.stack) < n {
				return nil, u.errf("TUPLE%d underflow", n)
			}
			items := append(Tuple(nil), u.stack[len(u.stack)-n:]...)
			u.stack = u.stack[:len(u.stack)-n]
			u.push(items)
		case opTuple:
			items, err := u.popMark()
			if err != nil {
				return nil, err
			}
			u.push(Tuple(items))
		case opGlobal:
			module, err := u.readLine()
			if err != nil {
				return nil, err
			}
			name, err := u.readLine()
			if err != nil {
				return nil, err
			}
			u.push(Global{Module: module, Name: name})
		case opStackGlobal:
			name, err := u.pop()
			if err != nil {
				return nil, err
			}
			module, err := u.pop()
			if err != nil {
				return nil, err
			}
			ms, mok := module.(Str)
			ns, nok := name.(Str)
			if !mok || !nok {
				return nil, u.errf("STACK_GLOBAL non-str operands")
			}
			u.push(Global{Module: string(ms), Name: string(ns)})
		case opReduce:
			if err := u.reduce(); err != nil {
				return nil, err
			}
		case opNewObj:
			if _, err := u.pop(); err != nil { // args tuple (ignored: __new__ with no args)
				return nil, err
			}
			cls, err := u.pop()
			if err != nil {
				return nil, err
			}
			g, ok := cls.(Global)
			if !ok {
				return nil, u.errf("NEWOBJ on non-Global %T", cls)
			}
			u.push(Object{Class: g, State: None{}})
		case opBuild:
			state, err := u.pop()
			if err != nil {
				return nil, err
			}
			obj, err := u.pop()
			if err != nil {
				return nil, err
			}
			o, ok := obj.(Object)
			if !ok {
				return nil, u.errf("BUILD on non-Object %T", obj)
			}
			o.State = state
			u.push(o)
		case opBinPut:
			idx, err := u.readByte()
			if err != nil {
				return nil, err
			}
			if err := u.memoize(int(idx)); err != nil {
				return nil, err
			}
		case opLongBinPut:
			b, err := u.readN(4)
			if err != nil {
				return nil, err
			}
			if err := u.memoize(int(binary.LittleEndian.Uint32(b))); err != nil {
				return nil, err
			}
		case opMemoize:
			if err := u.memoize(len(u.memo)); err != nil {
				return nil, err
			}
		case opBinGet:
			idx, err := u.readByte()
			if err != nil {
				return nil, err
			}
			v, ok := u.memo[int(idx)]
			if !ok {
				return nil, u.errf("BINGET missing memo %d", idx)
			}
			u.push(v)
		case opLongBinGet:
			b, err := u.readN(4)
			if err != nil {
				return nil, err
			}
			idx := int(binary.LittleEndian.Uint32(b))
			v, ok := u.memo[idx]
			if !ok {
				return nil, u.errf("LONG_BINGET missing memo %d", idx)
			}
			u.push(v)
		default:
			return nil, u.errf("unsupported opcode %#x %q", op, string(op))
		}
	}
}

func (u *unpickler) readUnicode(lenBytes int) error {
	raw, err := u.readN(lenBytes)
	if err != nil {
		return err
	}
	var n uint64
	for i := 0; i < lenBytes; i++ {
		n |= uint64(raw[i]) << (8 * i)
	}
	body, err := u.readN(int(n))
	if err != nil {
		return err
	}
	u.push(Str(string(body)))
	return nil
}

func (u *unpickler) readBytes(lenBytes int) error {
	raw, err := u.readN(lenBytes)
	if err != nil {
		return err
	}
	var n uint64
	for i := 0; i < lenBytes; i++ {
		n |= uint64(raw[i]) << (8 * i)
	}
	body, err := u.readN(int(n))
	if err != nil {
		return err
	}
	u.push(Bytes(append([]byte(nil), body...)))
	return nil
}

func (u *unpickler) memoize(idx int) error {
	v, err := u.top()
	if err != nil {
		return err
	}
	u.memo[idx] = v
	return nil
}

func (u *unpickler) appendOne() error {
	v, err := u.pop()
	if err != nil {
		return err
	}
	lst, err := u.pop()
	if err != nil {
		return err
	}
	l, ok := lst.(List)
	if !ok {
		return u.errf("APPEND on non-List %T", lst)
	}
	u.push(append(l, v))
	return nil
}

func (u *unpickler) appendMany() error {
	items, err := u.popMark()
	if err != nil {
		return err
	}
	lst, err := u.pop()
	if err != nil {
		return err
	}
	l, ok := lst.(List)
	if !ok {
		return u.errf("APPENDS on non-List %T", lst)
	}
	u.push(append(l, items...))
	return nil
}

func (u *unpickler) setOne() error {
	value, err := u.pop()
	if err != nil {
		return err
	}
	key, err := u.pop()
	if err != nil {
		return err
	}
	d, err := u.pop()
	if err != nil {
		return err
	}
	dict, ok := d.(Dict)
	if !ok {
		return u.errf("SETITEM on non-Dict %T", d)
	}
	u.push(append(dict, DictItem{Key: key, Value: value}))
	return nil
}

func (u *unpickler) setMany() error {
	items, err := u.popMark()
	if err != nil {
		return err
	}
	if len(items)%2 != 0 {
		return u.errf("SETITEMS odd operand count")
	}
	d, err := u.pop()
	if err != nil {
		return err
	}
	dict, ok := d.(Dict)
	if !ok {
		return u.errf("SETITEMS on non-Dict %T", d)
	}
	for i := 0; i < len(items); i += 2 {
		dict = append(dict, DictItem{Key: items[i], Value: items[i+1]})
	}
	u.push(dict)
	return nil
}

// reduce applies callable(*args), collapsing the two Python bytes reducers to
// Bytes and leaving any other REDUCE as a generic Reduce value for object codecs.
func (u *unpickler) reduce() error {
	args, err := u.pop()
	if err != nil {
		return err
	}
	callable, err := u.pop()
	if err != nil {
		return err
	}
	g, ok := callable.(Global)
	if ok {
		tuple, _ := args.(Tuple)
		switch {
		case g.Module == "_codecs" && g.Name == "encode" && len(tuple) == 2:
			if s, sok := tuple[0].(Str); sok {
				if enc, eok := tuple[1].(Str); eok && string(enc) == "latin1" {
					b, valid := latin1Encode(string(s))
					if !valid {
						return u.errf("_codecs.encode latin1 arg out of range")
					}
					u.push(Bytes(b))
					return nil
				}
			}
		case (g.Module == "builtins" || g.Module == "__builtin__") && g.Name == "bytes" && len(tuple) == 0:
			u.push(Bytes{})
			return nil
		}
	}
	u.push(Reduce{Callable: callable, Args: args})
	return nil
}

// decodeLong decodes Python's little-endian two's-complement long payload into
// an int64 (our object graphs never exceed that).
func decodeLong(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if len(b) > 8 {
		return 0, fmt.Errorf("long of %d bytes exceeds int64", len(b))
	}
	var v uint64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	if b[len(b)-1]&0x80 != 0 && len(b) < 8 {
		v |= ^uint64(0) << (uint(len(b)) * 8) // sign-extend
	}
	return int64(v), nil
}
