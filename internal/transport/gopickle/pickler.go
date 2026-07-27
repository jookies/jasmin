package gopickle

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Protocol-2 opcodes (subset the encoder emits; the decoder handles a wider set).
const (
	opProto      = 0x80
	opStop       = '.'
	opNone       = 'N'
	opNewTrue    = 0x88
	opNewFalse   = 0x89
	opBinInt1    = 'K'
	opBinInt2    = 'M'
	opBinInt     = 'J'
	opBinFloat   = 'G'
	opBinUnicode = 'X'
	opEmptyList  = ']'
	opEmptyDict  = '}'
	opEmptyTuple = ')'
	opMark       = '('
	opAppends    = 'e'
	opSetItems   = 'u'
	opTuple1     = 0x85
	opTuple2     = 0x86
	opTuple3     = 0x87
	opTuple      = 't'
	opGlobal     = 'c'
	opReduce     = 'R'
	opNewObj     = 0x81
	opBuild      = 'b'
)

// ErrPickle wraps encode/decode failures.
var ErrPickle = errors.New("gopickle")

// Dump serialises an IR value as a protocol-2 pickle. The output is memo-free
// (no BINPUT/BINGET) but valid: Python's pickle.loads reconstructs an equal
// object graph.
func Dump(v Value) ([]byte, error) {
	p := &pickler{buf: []byte{opProto, 2}}
	if err := p.encode(v); err != nil {
		return nil, err
	}
	p.buf = append(p.buf, opStop)
	return p.buf, nil
}

type pickler struct{ buf []byte }

func (p *pickler) encode(v Value) error {
	switch t := v.(type) {
	case nil:
		return fmt.Errorf("%w: nil value", ErrPickle)
	case None:
		p.buf = append(p.buf, opNone)
	case Bool:
		if t {
			p.buf = append(p.buf, opNewTrue)
		} else {
			p.buf = append(p.buf, opNewFalse)
		}
	case Int:
		return p.encodeInt(int64(t))
	case Float:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(float64(t)))
		p.buf = append(p.buf, opBinFloat)
		p.buf = append(p.buf, b[:]...)
	case Str:
		p.encodeStr(string(t))
	case Bytes:
		p.encodeBytes([]byte(t))
	case List:
		p.buf = append(p.buf, opEmptyList)
		if len(t) > 0 {
			p.buf = append(p.buf, opMark)
			for _, e := range t {
				if err := p.encode(e); err != nil {
					return err
				}
			}
			p.buf = append(p.buf, opAppends)
		}
	case Tuple:
		return p.encodeTuple(t)
	case Dict:
		p.buf = append(p.buf, opEmptyDict)
		if len(t) > 0 {
			p.buf = append(p.buf, opMark)
			for _, it := range t {
				if err := p.encode(it.Key); err != nil {
					return err
				}
				if err := p.encode(it.Value); err != nil {
					return err
				}
			}
			p.buf = append(p.buf, opSetItems)
		}
	case Global:
		p.encodeGlobal(t)
	case Reduce:
		if err := p.encode(t.Callable); err != nil {
			return err
		}
		if err := p.encode(t.Args); err != nil {
			return err
		}
		p.buf = append(p.buf, opReduce)
	case Object:
		p.encodeGlobal(t.Class)
		args := t.Args
		if args == nil {
			args = Tuple{}
		}
		if err := p.encode(args); err != nil {
			return err
		}
		p.buf = append(p.buf, opNewObj)
		if t.State != nil {
			if err := p.encode(t.State); err != nil {
				return err
			}
			p.buf = append(p.buf, opBuild)
		}
	default:
		return fmt.Errorf("%w: unsupported value type %T", ErrPickle, v)
	}
	return nil
}

func (p *pickler) encodeInt(i int64) error {
	switch {
	case i >= 0 && i < 256:
		p.buf = append(p.buf, opBinInt1, byte(i))
	case i >= 0 && i < 65536:
		p.buf = append(p.buf, opBinInt2, byte(i), byte(i>>8))
	case i >= math.MinInt32 && i <= math.MaxInt32:
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(int32(i)))
		p.buf = append(p.buf, opBinInt)
		p.buf = append(p.buf, b[:]...)
	default:
		// Our object graphs never carry ints beyond int32 (seqNum/status/TON
		// etc. are small; amounts are floats). LONG1 is intentionally not
		// emitted; add it here if a future object needs it.
		return fmt.Errorf("%w: int %d out of supported BININT range", ErrPickle, i)
	}
	return nil
}

func (p *pickler) encodeStr(s string) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(len(s)))
	p.buf = append(p.buf, opBinUnicode)
	p.buf = append(p.buf, b[:]...)
	p.buf = append(p.buf, s...)
}

// encodeBytes emits the protocol-2 shape for Python bytes: builtins.bytes() for
// empty, else _codecs.encode(<latin1 str>, 'latin1').
func (p *pickler) encodeBytes(b []byte) {
	if len(b) == 0 {
		p.encodeGlobal(Global{Module: "builtins", Name: "bytes"})
		p.buf = append(p.buf, opEmptyTuple, opReduce)
		return
	}
	p.encodeGlobal(Global{Module: "_codecs", Name: "encode"})
	p.encodeStr(latin1Decode(b))
	p.encodeStr("latin1")
	p.buf = append(p.buf, opTuple2, opReduce)
}

func (p *pickler) encodeGlobal(g Global) {
	p.buf = append(p.buf, opGlobal)
	p.buf = append(p.buf, g.Module...)
	p.buf = append(p.buf, '\n')
	p.buf = append(p.buf, g.Name...)
	p.buf = append(p.buf, '\n')
}

func (p *pickler) encodeTuple(t Tuple) error {
	switch len(t) {
	case 0:
		p.buf = append(p.buf, opEmptyTuple)
	case 1:
		if err := p.encode(t[0]); err != nil {
			return err
		}
		p.buf = append(p.buf, opTuple1)
	case 2, 3:
		for _, e := range t {
			if err := p.encode(e); err != nil {
				return err
			}
		}
		if len(t) == 2 {
			p.buf = append(p.buf, opTuple2)
		} else {
			p.buf = append(p.buf, opTuple3)
		}
	default:
		p.buf = append(p.buf, opMark)
		for _, e := range t {
			if err := p.encode(e); err != nil {
				return err
			}
		}
		p.buf = append(p.buf, opTuple)
	}
	return nil
}
