package segmentation_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"math/big"
)

const baselineCommit = "0aac58e466d583d0f0436df7b8afa3dc96191263"

type goldenDocument struct {
	SchemaVersion  int          `json:"schema_version"`
	BaselineCommit string       `json:"baseline_commit"`
	Cases          []goldenCase `json:"cases"`
}

type goldenCase struct {
	ID                   string               `json:"id"`
	Input                goldenInput          `json:"input"`
	Classification       goldenClassification `json:"classification"`
	EmittedReference     *uint8               `json:"emitted_reference"`
	ConsumedPayloadBytes int                  `json:"consumed_payload_bytes"`
	Truncated            bool                 `json:"truncated"`
	Parts                []goldenPart         `json:"parts"`
}

type goldenInput struct {
	DataCoding       uint8       `json:"data_coding"`
	SplitMethod      string      `json:"split_method"`
	MaxParts         uint8       `json:"max_parts"`
	InitialReference uint8       `json:"initial_reference"`
	Payload          goldenBytes `json:"payload"`
}

type goldenClassification struct {
	Bits                  uint8 `json:"bits"`
	SingleLimit           int   `json:"single_limit"`
	MultipartPayloadBytes int   `json:"multipart_payload_bytes"`
}

type goldenPart struct {
	Sequence     uint8                `json:"sequence"`
	Payload      goldenBytes          `json:"payload"`
	ShortMessage goldenBytes          `json:"short_message"`
	SAR          *goldenConcatenation `json:"sar"`
	UDH          *goldenConcatenation `json:"udh"`
}

type goldenConcatenation struct {
	Reference uint16       `json:"reference"`
	Total     uint8        `json:"total"`
	Sequence  uint8        `json:"sequence"`
	Bytes     *goldenBytes `json:"bytes,omitempty"`
}

type goldenBytes struct {
	Base64 string `json:"base64"`
	Hex    string `json:"hex"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

func TestGoldenSegmentationCompatibility(t *testing.T) {
	document := loadGolden(t)
	if len(document.Cases) != 14 {
		t.Fatalf("case count = %d, want 14", len(document.Cases))
	}
	seen := make(map[string]struct{}, len(document.Cases))
	for _, fixture := range document.Cases {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			if _, duplicate := seen[fixture.ID]; duplicate {
				t.Fatalf("duplicate fixture %q", fixture.ID)
			}
			seen[fixture.ID] = struct{}{}
			payload := decodeGoldenBytes(t, fixture.Input.Payload)
			reference := uint16(0)
			if fixture.EmittedReference != nil {
				reference = uint16(*fixture.EmittedReference)
				if got := segmentation.NextReference(uint16(fixture.Input.InitialReference), false); got != reference {
					t.Fatalf("NextReference(%d) = %d, want %d", fixture.Input.InitialReference, got, reference)
				}
			}
			result, err := segmentation.Segment(segmentation.Request{
				Payload:     payload,
				DataCoding:  fixture.Input.DataCoding,
				SplitMethod: segmentation.SplitMethod(fixture.Input.SplitMethod),
				MaxParts:    fixture.Input.MaxParts,
				Reference:   reference,
			})
			if err != nil {
				t.Fatal(err)
			}
			class := result.Classification()
			if class.Bits != fixture.Classification.Bits || class.SingleLimit != fixture.Classification.SingleLimit || class.MultipartPayloadBytes != fixture.Classification.MultipartPayloadBytes {
				t.Fatalf("classification = %#v, want %#v", class, fixture.Classification)
			}
			gotReference, hasReference := result.Reference()
			if fixture.EmittedReference == nil {
				if hasReference {
					t.Fatalf("unexpected reference %d", gotReference)
				}
			} else if !hasReference || gotReference != uint16(*fixture.EmittedReference) {
				t.Fatalf("reference = (%d,%v), want %d", gotReference, hasReference, *fixture.EmittedReference)
			}
			if result.ConsumedPayloadBytes() != fixture.ConsumedPayloadBytes || result.Truncated() != fixture.Truncated {
				t.Fatalf("consumption = (%d,%v), want (%d,%v)", result.ConsumedPayloadBytes(), result.Truncated(), fixture.ConsumedPayloadBytes, fixture.Truncated)
			}
			parts := result.Parts()
			if len(parts) != len(fixture.Parts) {
				t.Fatalf("part count = %d, want %d", len(parts), len(fixture.Parts))
			}
			for index := range parts {
				assertPart(t, parts[index], fixture.Parts[index])
			}
		})
	}
	if len(seen) != 14 {
		t.Fatalf("seen fixtures = %d, want 14", len(seen))
	}
}

func TestClassificationAllLegacyDCSClasses(t *testing.T) {
	for _, dcs := range []uint8{3, 6, 7, 10} {
		if got := segmentation.Classify(dcs); got.Bits != 8 || got.SingleLimit != 140 || got.MultipartPayloadBytes != 134 {
			t.Fatalf("Classify(%d) = %#v", dcs, got)
		}
	}
	for _, dcs := range []uint8{2, 4, 5, 8, 9, 13, 14} {
		if got := segmentation.Classify(dcs); got.Bits != 16 || got.SingleLimit != 70 || got.MultipartPayloadBytes != 134 {
			t.Fatalf("Classify(%d) = %#v", dcs, got)
		}
	}
	for _, dcs := range []uint8{0, 1, 11, 255} {
		if got := segmentation.Classify(dcs); got.Bits != 7 || got.SingleLimit != 160 || got.MultipartPayloadBytes != 153 {
			t.Fatalf("Classify(%d) = %#v", dcs, got)
		}
	}
}

func TestSegmentationValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request segmentation.Request
		want    error
	}{
		{"invalid split", segmentation.Request{Payload: []byte("x"), SplitMethod: "other", MaxParts: 1}, segmentation.ErrInvalidSplitMethod},
		{"zero max parts", segmentation.Request{Payload: []byte("x"), SplitMethod: segmentation.SplitSAR}, segmentation.ErrInvalidMaxParts},
		{"multipart zero reference", segmentation.Request{Payload: make([]byte, 161), SplitMethod: segmentation.SplitSAR, MaxParts: 5}, segmentation.ErrInvalidReference},
		{"oversized", segmentation.Request{Payload: make([]byte, segmentation.MaxPayloadBytes+1), SplitMethod: segmentation.SplitSAR, MaxParts: 1}, segmentation.ErrPayloadTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := segmentation.Segment(tc.request)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestResultAndAccessorImmutability(t *testing.T) {
	input := make([]byte, 161)
	for index := range input {
		input[index] = byte(index)
	}
	result, err := segmentation.Segment(segmentation.Request{Payload: input, SplitMethod: segmentation.SplitUDH, MaxParts: 5, Reference: 9})
	if err != nil {
		t.Fatal(err)
	}
	input[0] ^= 0xff
	parts := result.Parts()
	original := parts[0].Payload()[0]
	if original != 0 {
		t.Fatalf("stored payload changed to %d", original)
	}
	payload := parts[0].Payload()
	shortMessage := parts[0].ShortMessage()
	udh, _, ok := parts[0].UDH()
	if !ok {
		t.Fatal("missing UDH")
	}
	payload[0] ^= 0xff
	shortMessage[0] ^= 0xff
	udh[0] ^= 0xff
	parts[0] = segmentation.Part{}
	again := result.Parts()
	if again[0].Payload()[0] != 0 || again[0].ShortMessage()[0] != 5 {
		t.Fatal("accessor mutation changed result")
	}
	gotUDH, _, _ := again[0].UDH()
	if gotUDH[0] != 5 {
		t.Fatal("UDH accessor mutation changed result")
	}
}

func TestEmptyPayloadIsSinglePart(t *testing.T) {
	result, err := segmentation.Segment(segmentation.Request{SplitMethod: segmentation.SplitSAR, MaxParts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Parts()) != 1 || result.ConsumedPayloadBytes() != 0 || result.Truncated() {
		t.Fatalf("empty result = %#v", result)
	}
}

func TestCustomTLVPropagation(t *testing.T) {
	tlvs := []tlv.TLV{{Tag: big.NewInt(0x1234), Value: "hello"}}
	req := segmentation.Request{
		Payload:     []byte("world"),
		DataCoding:  0,
		MaxParts:    1,
		SplitMethod: segmentation.SplitSAR,
		CustomTLVs:  tlvs,
	}
	result, err := segmentation.Segment(req)
	if err != nil {
		t.Fatal(err)
	}
	parts := result.Parts()
	if len(parts) != 1 {
		t.Fatal("expected 1 part")
	}
	got := parts[0].CustomTLVs()
	if len(got) != 1 || got[0].Tag.Cmp(big.NewInt(0x1234)) != 0 || got[0].Value != "hello" {
		t.Fatalf("TLV not propagated correctly: %+v", got)
	}
}

func TestUDH16BitReference(t *testing.T) {
	req := segmentation.Request{
		Payload:     make([]byte, 161),
		DataCoding:  0,
		SplitMethod: segmentation.SplitUDH,
		MaxParts:    5,
		Reference:   0x1234,
		Is16Bit:     true,
	}
	result, err := segmentation.Segment(req)
	if err != nil {
		t.Fatal(err)
	}
	parts := result.Parts()
	udh, metadata, ok := parts[0].UDH()
	if !ok {
		t.Fatal("expected UDH")
	}
	if !metadata.Is16Bit || metadata.Reference != 0x1234 {
		t.Fatalf("invalid metadata: %#v", metadata)
	}
	// IEI 08, Length 4, Ref(2), Total(1), Seq(1) => 6 bytes following length byte => 7 bytes total
	if len(udh) != 7 || udh[1] != 8 || udh[2] != 4 {
		t.Fatalf("invalid UDH bytes: %x", udh)
	}
	if udh[3] != 0x12 || udh[4] != 0x34 {
		t.Fatalf("invalid reference in UDH: %x", udh)
	}
}

func FuzzSegmentNeverPanics(f *testing.F) {
	f.Add([]byte("hello"), uint8(0), uint16(1), false)
	f.Add(make([]byte, 161), uint8(255), uint16(42), true)
	f.Fuzz(func(t *testing.T, payload []byte, dataCoding uint8, reference uint16, useUDH bool) {
		method := segmentation.SplitSAR
		if useUDH {
			method = segmentation.SplitUDH
		}
		_, _ = segmentation.Segment(segmentation.Request{Payload: payload, DataCoding: dataCoding, SplitMethod: method, MaxParts: 5, Reference: reference})
	})
}

func assertPart(t *testing.T, got segmentation.Part, want goldenPart) {
	t.Helper()
	if got.Sequence() != want.Sequence {
		t.Fatalf("sequence = %d, want %d", got.Sequence(), want.Sequence)
	}
	if !bytes.Equal(got.Payload(), decodeGoldenBytes(t, want.Payload)) || !bytes.Equal(got.ShortMessage(), decodeGoldenBytes(t, want.ShortMessage)) {
		t.Fatal("part bytes differ")
	}
	assertMetadata(t, got, want)
}

func assertMetadata(t *testing.T, got segmentation.Part, want goldenPart) {
	t.Helper()
	sar, hasSAR := got.SAR()
	if want.SAR == nil {
		if hasSAR {
			t.Fatalf("unexpected SAR %#v", sar)
		}
	} else if !hasSAR || sar.Reference != want.SAR.Reference || sar.Total != want.SAR.Total || sar.Sequence != want.SAR.Sequence {
		t.Fatalf("SAR = (%#v,%v), want %#v", sar, hasSAR, want.SAR)
	}
	udh, metadata, hasUDH := got.UDH()
	if want.UDH == nil {
		if hasUDH {
			t.Fatalf("unexpected UDH %x %#v", udh, metadata)
		}
		return
	}
	if !hasUDH || metadata.Reference != want.UDH.Reference || metadata.Total != want.UDH.Total || metadata.Sequence != want.UDH.Sequence {
		t.Fatalf("UDH metadata = (%#v,%v), want %#v", metadata, hasUDH, want.UDH)
	}
	if want.UDH.Bytes == nil || !bytes.Equal(udh, decodeGoldenBytes(t, *want.UDH.Bytes)) {
		t.Fatalf("UDH bytes = %x", udh)
	}
}

func decodeGoldenBytes(t *testing.T, descriptor goldenBytes) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(descriptor.Base64)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != descriptor.Length || hex.EncodeToString(raw) != descriptor.Hex {
		t.Fatalf("invalid fixture byte descriptor")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != descriptor.SHA256 {
		t.Fatalf("fixture SHA-256 mismatch")
	}
	return raw
}

func loadGolden(t *testing.T) goldenDocument {
	t.Helper()
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "segmentation", "baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document goldenDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || document.BaselineCommit != baselineCommit {
		t.Fatalf("fixture provenance = (%d,%q)", document.SchemaVersion, document.BaselineCommit)
	}
	return document
}
