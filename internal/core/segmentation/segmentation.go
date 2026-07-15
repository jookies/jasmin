package segmentation

import (
	"errors"
	"fmt"
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

type Request struct {
	Payload     []byte
	DataCoding  uint8
	SplitMethod SplitMethod
	MaxParts    uint8
	Reference   uint8
}

type Concatenation struct {
	Reference uint8
	Total     uint8
	Sequence  uint8
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
}

type Result struct {
	classification       Classification
	parts                []Part
	consumedPayloadBytes int
	truncated            bool
	reference            uint8
	hasReference         bool
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

func NextReference(previous uint8) uint8 {
	if previous >= 255 {
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
	singleBytes := classification.SingleLimit
	if classification.Bits == 16 {
		singleBytes *= 2
	}
	if len(request.Payload) <= singleBytes {
		payload := cloneBytes(request.Payload)
		return Result{
			classification:       classification,
			parts:                []Part{{sequence: 1, payload: payload, shortMessage: cloneBytes(payload)}},
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
		}
		part := Part{sequence: metadata.Sequence, payload: payload}
		if request.SplitMethod == SplitSAR {
			part.shortMessage = cloneBytes(payload)
			part.sar = metadata
			part.hasSAR = true
		} else {
			header := []byte{5, 0, 3, metadata.Reference, metadata.Total, metadata.Sequence}
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

func (result Result) Reference() (uint8, bool) {
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

func (part Part) clone() Part {
	part.payload = cloneBytes(part.payload)
	part.shortMessage = cloneBytes(part.shortMessage)
	part.udh = cloneBytes(part.udh)
	return part
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}
