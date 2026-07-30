package segmentation

import (
	"errors"
	"fmt"

	"github.com/pumpitspace/synevyr/internal/core/tlv"
)

const MaxPayloadBytes = 1 << 20

type SplitMethod string

const (
	SplitSAR SplitMethod = "sar"
	SplitUDH SplitMethod = "udh"
)

var (
	ErrInvalidSplitMethod = errors.New("invalid split method")
	ErrInvalidMaxParts    = errors.New("invalid maximum part count")
	ErrInvalidReference   = errors.New("invalid concatenation reference")
	ErrPayloadTooLarge    = errors.New("payload exceeds compatibility cap")
)

type Classification struct {
	Bits                  uint8
	SingleLimit           int
	MultipartPayloadBytes int
}

// Request defines the input for the segmentation operation.
type Request struct {
	Payload     []byte
	DataCoding  uint8
	SplitMethod SplitMethod
	MaxParts    uint8
	Reference   uint16
	Is16Bit     bool
	// CustomTLVs are attached to every produced part in caller order — the
	// legacy factory builds each multipart PDU from the same kwargs, so every
	// part carries the full vendor TLV set.
	CustomTLVs []tlv.TLV
	// PreEncodedUDH marks a payload that already opens with a User Data Header
	// the caller did not build — an ESME's own pre-segmented part. Such a
	// payload is emitted as exactly one part, verbatim, and flagged as carrying
	// a UDH so the outbound esm_class keeps its UDHI bit.
	//
	// Re-segmenting it would prepend a second UDH to a payload that already has
	// one, and splitting it would cut the header off the remainder. Legacy never
	// faces this because it forwards the ESME's PDU object rather than
	// reconstructing it from a flattened request.
	PreEncodedUDH bool
	// PreserveSinglePart marks any submit_sm received from an ESME. SMPP has
	// already framed that PDU (including any SAR TLVs), and legacy forwards the
	// PDU object as-is. It must never be re-segmented merely because its payload
	// exceeds an HTTP-oriented character limit.
	PreserveSinglePart bool
}

type Concatenation struct {
	Reference uint16
	Total     uint8
	Sequence  uint8
	Is16Bit   bool
}

type Part struct {
	sequence     uint8
	payload      []byte
	shortMessage []byte
	sar          Concatenation
	hasSAR       bool
	udh          []byte
	udhMetadata  Concatenation
	hasUDH       bool
	customTLVs   []tlv.TLV
}

type Result struct {
	classification       Classification
	parts                []Part
	consumedPayloadBytes int
	truncated            bool
	reference            uint16
	hasReference         bool
}

// PreservedPart is one already-segmented submit_sm from a trusted protocol
// boundary. Preserve builds the orchestration Part list without changing its
// short_message bytes or per-PDU vendor TLVs; SAR/UDH metadata stays in the
// accompanying raw submit_sm body used by the envelope builder.
type PreservedPart struct {
	ShortMessage []byte
	CustomTLVs   []tlv.TLV
}

// Preserve returns an ordered Result for a caller-supplied multipart chain.
// It exists for frozen SMPPClientManagerPB, whose SubmitSM.nextPdu chain is
// already segmented and must never be flattened or segmented a second time.
func Preserve(values []PreservedPart) (Result, error) {
	if len(values) == 0 || len(values) > 255 {
		return Result{}, ErrInvalidMaxParts
	}
	parts := make([]Part, 0, len(values))
	consumed := 0
	for index, value := range values {
		if len(value.ShortMessage) > MaxPayloadBytes-consumed {
			return Result{}, ErrPayloadTooLarge
		}
		message := cloneBytes(value.ShortMessage)
		parts = append(parts, Part{
			sequence:     uint8(index + 1),
			payload:      cloneBytes(message),
			shortMessage: message,
			customTLVs:   cloneCustomTLVs(value.CustomTLVs),
		})
		consumed += len(message)
	}
	return Result{parts: parts, consumedPayloadBytes: consumed}, nil
}

func Classify(dataCoding uint8) Classification {
	switch dataCoding {
	case 3, 6, 7, 10:
		return Classification{Bits: 8, SingleLimit: 140, MultipartPayloadBytes: 134}
	case 2, 4, 5, 8, 9, 13, 14:
		return Classification{Bits: 16, SingleLimit: 70, MultipartPayloadBytes: 134}
	default:
		return Classification{Bits: 7, SingleLimit: 160, MultipartPayloadBytes: 153}
	}
}

func NextReference(previous uint16, is16Bit bool) uint16 {
	limit := uint16(255)
	if is16Bit {
		limit = 65535
	}
	if previous >= limit {
		return 1
	}
	return previous + 1
}

func Segment(request Request) (Result, error) {
	if request.SplitMethod != SplitSAR && request.SplitMethod != SplitUDH {
		return Result{}, fmt.Errorf("%w: %q", ErrInvalidSplitMethod, request.SplitMethod)
	}
	if request.MaxParts == 0 {
		return Result{}, ErrInvalidMaxParts
	}
	if len(request.Payload) > MaxPayloadBytes {
		return Result{}, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(request.Payload), MaxPayloadBytes)
	}

	classification := Classify(request.DataCoding)
	// A payload that already carries the ESME's UDH is passed through whole,
	// before the length classification can decide to split it.
	if request.PreEncodedUDH || request.PreserveSinglePart {
		payload := cloneBytes(request.Payload)
		return Result{
			classification: classification,
			parts: []Part{{
				sequence:     1,
				payload:      payload,
				shortMessage: cloneBytes(payload),
				hasUDH:       request.PreEncodedUDH,
				customTLVs:   cloneCustomTLVs(request.CustomTLVs),
			}},
			consumedPayloadBytes: len(payload),
		}, nil
	}
	singleBytes := classification.SingleLimit
	if classification.Bits == 16 {
		singleBytes *= 2
	}
	if len(request.Payload) <= singleBytes {
		payload := cloneBytes(request.Payload)
		return Result{
			classification: classification,
			parts: []Part{{
				sequence:     1,
				payload:      payload,
				shortMessage: cloneBytes(payload),
				customTLVs:   cloneCustomTLVs(request.CustomTLVs),
			}},
			consumedPayloadBytes: len(payload),
		}, nil
	}
	if request.Reference == 0 {
		return Result{}, ErrInvalidReference
	}

	sliceBytes := classification.MultipartPayloadBytes
	partCount := (len(request.Payload) + sliceBytes - 1) / sliceBytes
	if partCount > int(request.MaxParts) {
		partCount = int(request.MaxParts)
	}
	parts := make([]Part, 0, partCount)
	for index := 0; index < partCount; index++ {
		start := index * sliceBytes
		stop := start + sliceBytes
		if stop > len(request.Payload) {
			stop = len(request.Payload)
		}
		payload := cloneBytes(request.Payload[start:stop])
		metadata := Concatenation{
			Reference: request.Reference,
			Total:     uint8(partCount),
			Sequence:  uint8(index + 1),
			Is16Bit:   request.Is16Bit,
		}
		part := Part{
			sequence:   metadata.Sequence,
			payload:    payload,
			customTLVs: cloneCustomTLVs(request.CustomTLVs),
		}
		if request.SplitMethod == SplitSAR {
			part.shortMessage = cloneBytes(payload)
			part.sar = metadata
			part.hasSAR = true
		} else {
			var header []byte
			if request.Is16Bit {
				header = []byte{
					6, 8, 4,
					byte(metadata.Reference >> 8),
					byte(metadata.Reference),
					metadata.Total,
					metadata.Sequence,
				}
			} else {
				header = []byte{
					5, 0, 3,
					byte(metadata.Reference),
					metadata.Total,
					metadata.Sequence,
				}
			}
			part.udh = cloneBytes(header)
			part.udhMetadata = metadata
			part.hasUDH = true
			part.shortMessage = make([]byte, 0, len(header)+len(payload))
			part.shortMessage = append(part.shortMessage, header...)
			part.shortMessage = append(part.shortMessage, payload...)
		}
		parts = append(parts, part)
	}
	consumed := partCount * sliceBytes
	if consumed > len(request.Payload) {
		consumed = len(request.Payload)
	}
	return Result{
		classification:       classification,
		parts:                parts,
		consumedPayloadBytes: consumed,
		truncated:            consumed != len(request.Payload),
		reference:            request.Reference,
		hasReference:         true,
	}, nil
}

func (result Result) Classification() Classification {
	return result.classification
}

func (result Result) Parts() []Part {
	parts := make([]Part, len(result.parts))
	for index, part := range result.parts {
		parts[index] = part.clone()
	}
	return parts
}

func (result Result) ConsumedPayloadBytes() int {
	return result.consumedPayloadBytes
}

func (result Result) Truncated() bool {
	return result.truncated
}

func (result Result) Reference() (uint16, bool) {
	return result.reference, result.hasReference
}

func (part Part) Sequence() uint8 {
	return part.sequence
}

func (part Part) Payload() []byte {
	return cloneBytes(part.payload)
}

func (part Part) ShortMessage() []byte {
	return cloneBytes(part.shortMessage)
}

func (part Part) SAR() (Concatenation, bool) {
	return part.sar, part.hasSAR
}

func (part Part) UDH() ([]byte, Concatenation, bool) {
	return cloneBytes(part.udh), part.udhMetadata, part.hasUDH
}

func (part Part) CustomTLVs() []tlv.TLV {
	return cloneCustomTLVs(part.customTLVs)
}

func (part Part) clone() Part {
	part.payload = cloneBytes(part.payload)
	part.shortMessage = cloneBytes(part.shortMessage)
	part.udh = cloneBytes(part.udh)
	part.customTLVs = cloneCustomTLVs(part.customTLVs)
	return part
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

// cloneCustomTLVs copies the tuple list; fields are shared as immutable,
// mirroring Python's shared tuples.
func cloneCustomTLVs(value []tlv.TLV) []tlv.TLV {
	if value == nil {
		return nil
	}
	return append([]tlv.TLV(nil), value...)
}
