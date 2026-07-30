package outbound_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/billing"
	"github.com/pumpitspace/synevyr/internal/core/segmentation"
	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
	"math/big"
)

type recordingEncoder struct {
	requests []picklecompat.SubmitSMEncodeRequest
}

func (encoder *recordingEncoder) EncodeSubmitSM(_ context.Context, request picklecompat.SubmitSMEncodeRequest) (picklecompat.SubmitSMEncodeResult, error) {
	encoder.requests = append(encoder.requests, request)
	return picklecompat.SubmitSMEncodeResult{Body: []byte{0x80, 0x02, byte(request.Sequence)}, Bill: []byte{0x80, 0x02, 0x42}}, nil
}

func TestSubmitEnvelopeBuilderProjectsLegacyPropertiesAndBill(t *testing.T) {
	encoder := &recordingEncoder{}
	builder, err := outbound.NewSubmitEnvelopeBuilder(encoder)
	if err != nil {
		t.Fatal(err)
	}
	segmented, err := segmentation.Segment(segmentation.Request{
		Payload:     []byte("hello"),
		DataCoding:  0,
		SplitMethod: segmentation.SplitSAR,
		MaxParts:    5,
		Reference:   42,
		CustomTLVs:  []tlv.TLV{{Tag: big.NewInt(0x1401), Type: "Int8", Value: "2"}, {Tag: big.NewInt(0x1400), Value: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := segmented.Parts()
	validity := 5 * time.Minute
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 678901000, time.UTC)
	request := core.SubmitEnvelopeRequest{
		MessageID:       "11111111-1111-4111-8111-111111111111",
		BillID:          "bill-1",
		CreatedAt:       createdAt,
		Username:        "alice",
		UserID:          "user-opaque",
		ConnectorID:     "connector-a",
		SourceAddr:      []byte("1111"),
		DestinationAddr: []byte("2222"),
		DataCoding:      0,
		Priority:        2,
		ValidityPeriod:  &validity,
		DLR:             true,
		SourceConnector: "httpapi",
		Bill: billing.Bill{
			SubmitSmAmount:         0.5,
			SubmitSmRespAmount:     0.5,
			DecrementSubmitSmCount: 1,
		},
		Parts:      parts,
		CustomTLVs: []tlv.TLV{{Tag: big.NewInt(0x1401), Type: "Int8", Value: "2"}, {Tag: big.NewInt(0x1400), Value: "1"}},
	}

	envelope, err := builder.BuildSubmitEnvelope(context.Background(), request, parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if envelope.RoutingKey() != "submit.sm.connector-a" {
		t.Fatalf("routing key=%q", envelope.RoutingKey())
	}
	properties := envelope.Properties()
	if replyTo, ok := properties.ReplyTo(); !ok || replyTo != "submit.sm.resp.user-opaque" {
		t.Fatalf("reply-to=(%q,%v)", replyTo, ok)
	}
	if priority, ok := properties.Priority(); !ok || priority != 2 {
		t.Fatalf("priority=(%d,%v)", priority, ok)
	}
	headers := properties.Headers()
	if created, _ := headers["created_at"].String(); created != "2026-01-02 03:04:05.678901" {
		t.Fatalf("created_at=%q", created)
	}
	if expiration, _ := headers["expiration"].String(); expiration != "2026-01-02 03:09:05.678901" {
		t.Fatalf("expiration=%q", expiration)
	}
	if _, ok := headers["submit_sm_bill"].Bytes(); !ok {
		t.Fatal("missing pickled submit_sm_bill header")
	}
	if len(encoder.requests) != 1 {
		t.Fatalf("encode calls=%d", len(encoder.requests))
	}
	if !encoder.requests[0].RegisteredDelivery {
		t.Fatal("DLR must be requested on the single PDU")
	}
	if encoder.requests[0].SAR != nil {
		t.Fatalf("unexpected SAR=%+v", encoder.requests[0].SAR)
	}
	tlvs := encoder.requests[0].CustomTLVs
	if len(tlvs) != 2 || tlvs[0].Tag.Cmp(big.NewInt(0x1401)) != 0 || tlvs[1].Tag.Cmp(big.NewInt(0x1400)) != 0 {
		t.Fatalf("custom TLVs must keep caller order: %+v", tlvs)
	}
	if tlvs[0].Type != "Int8" || tlvs[0].Value != "2" || tlvs[1].Type != "" || tlvs[1].Value != "1" {
		t.Fatalf("custom TLV tuples not preserved: %+v", tlvs)
	}
	if encoder.requests[0].SubmitSMRespAmount != 0.5 || encoder.requests[0].DecrementSubmitSMCount != 1 {
		t.Fatalf("bill request=%+v", encoder.requests[0])
	}
}

func TestSubmitEnvelopeBuilderProjectsMultipartIdentityAndLastPartDLR(t *testing.T) {
	encoder := &recordingEncoder{}
	builder, err := outbound.NewSubmitEnvelopeBuilder(encoder)
	if err != nil {
		t.Fatal(err)
	}
	segmented, err := segmentation.Segment(segmentation.Request{
		Payload: make([]byte, 161), DataCoding: 0, SplitMethod: segmentation.SplitSAR,
		MaxParts: 5, Reference: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := segmented.Parts()
	request := core.SubmitEnvelopeRequest{
		MessageID: "aggregate-1", BillID: "bill-1", CreatedAt: time.Now(),
		Username: "alice", UserID: "user-1", ConnectorID: "connector-a",
		DestinationAddr: []byte("15551230000"), DLR: true, Parts: parts,
		Bill: billing.Bill{SubmitSmAmount: 0.5, SubmitSmRespAmount: 0.5, DecrementSubmitSmCount: 1},
	}
	for index, part := range parts {
		envelope, err := builder.BuildSubmitEnvelope(context.Background(), request, part)
		if err != nil {
			t.Fatalf("part %d: %v", index+1, err)
		}
		headers := envelope.Properties().Headers()
		aggregate, _ := headers["aggregate-message-id"].String()
		partNumber, _ := headers["part-number"].Integer()
		partCount, _ := headers["part-count"].Integer()
		if aggregate != "aggregate-1" || partNumber != int64(index+1) || partCount != int64(len(parts)) {
			t.Fatalf("part %d metadata=(%q,%d,%d)", index+1, aggregate, partNumber, partCount)
		}
		wantMessageID := fmt.Sprintf("aggregate-1/%06d", index+1)
		if envelope.Properties().MessageID() != wantMessageID {
			t.Fatalf("part %d message-id=%q want=%q", index+1, envelope.Properties().MessageID(), wantMessageID)
		}
	}
	if len(encoder.requests) != len(parts) {
		t.Fatalf("encoder calls=%d want=%d", len(encoder.requests), len(parts))
	}
	if encoder.requests[0].RegisteredDelivery || !encoder.requests[len(parts)-1].RegisteredDelivery {
		t.Fatalf("registered delivery first=%v last=%v", encoder.requests[0].RegisteredDelivery, encoder.requests[len(parts)-1].RegisteredDelivery)
	}
}

func TestSubmitEnvelopeBuilderCarriesSMPPsRawPDU(t *testing.T) {
	encoder := &recordingEncoder{}
	builder, err := outbound.NewSubmitEnvelopeBuilder(encoder)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 200)
	segmented, err := segmentation.Segment(segmentation.Request{
		Payload: payload, DataCoding: 0, SplitMethod: segmentation.SplitSAR,
		MaxParts: 10, Reference: 42, PreserveSinglePart: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := segmented.Parts()
	if len(parts) != 1 {
		t.Fatalf("SMPPs pre-segmented PDU became %d parts", len(parts))
	}
	sarRef := uint16(0x1234)
	total, sequence := byte(3), byte(2)
	raw := &smppwire.SubmitSMBody{
		ServiceType: []byte("svc"), SourceAddressTON: 2, SourceAddressNPI: 8,
		SourceAddress: []byte("original-source"), DestinationAddressTON: 1,
		DestinationAddressNPI: 1, DestinationAddress: []byte("original-dest"),
		ESMClass: 0x43, ProtocolID: 0x7f, PriorityFlag: 3,
		ScheduleDeliveryTime: []byte("000000000600000R"),
		ValidityPeriod:       []byte("261231235959104-"), RegisteredDelivery: 0x11,
		ReplaceIfPresentFlag: 1, DataCoding: 0xf2, SMDefaultMessageID: 0xfe,
		ShortMessage: []byte("original-content"),
		Optional: smppwire.OptionalParameters{
			SARMessageReference: &sarRef, SARTotalSegments: &total, SARSegmentSequence: &sequence,
		},
	}
	request := core.SubmitEnvelopeRequest{
		MessageID: "m", BillID: "b", CreatedAt: time.Now(), Username: "alice", UserID: "u",
		ConnectorID: "c", SourceAddr: []byte("rewritten-source"),
		DestinationAddr: []byte("rewritten-dest"), DataCoding: 0xf2, Priority: 3,
		Parts: parts, SMPPSubmit: raw,
	}
	if _, err := builder.BuildSubmitEnvelope(context.Background(), request, parts[0]); err != nil {
		t.Fatal(err)
	}
	if len(encoder.requests) != 1 || encoder.requests[0].RawPDU == nil {
		t.Fatalf("raw encode request=%+v", encoder.requests)
	}
	got := encoder.requests[0].RawPDU
	if string(got.SourceAddr) != "rewritten-source" ||
		string(got.DestinationAddr) != "rewritten-dest" ||
		len(got.ShortMessage) != len(payload) ||
		got.ESMClass != 0x43 || got.RegisteredDelivery != 0x11 ||
		got.Optional.SARMessageReference == nil || *got.Optional.SARMessageReference != sarRef {
		t.Fatalf("raw PDU projection=%+v", got)
	}
}

func TestSubmitEnvelopeBuilderPreservesTrustedLinkedPDUChain(t *testing.T) {
	encoder := &recordingEncoder{}
	builder, err := outbound.NewSubmitEnvelopeBuilder(encoder)
	if err != nil {
		t.Fatal(err)
	}
	partTLV := tlv.TLV{Tag: big.NewInt(0x1400), Type: "OctetString", Value: []byte{0x01}}
	segmented, err := segmentation.Preserve([]segmentation.PreservedPart{
		{ShortMessage: []byte("part-1"), CustomTLVs: []tlv.TLV{partTLV}},
		{ShortMessage: []byte("part-2")},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := segmented.Parts()
	first := &smppwire.SubmitSMBody{ShortMessage: []byte("part-1"), PriorityFlag: 1}
	second := &smppwire.SubmitSMBody{ShortMessage: []byte("part-2"), PriorityFlag: 2}
	request := core.SubmitEnvelopeRequest{
		MessageID: "aggregate-linked", BillID: "legacy-bill", CreatedAt: time.Now(),
		Username: "alice", UserID: "user-1", ConnectorID: "forced-cid",
		DestinationAddr: []byte("15551230000"), Priority: 3,
		Expiration: "2026-07-29 12:34:56.123456",
		Parts:      parts, SMPPSubmit: first, SMPPSubmits: []*smppwire.SubmitSMBody{first, second},
		Bill: billing.Bill{SubmitSmAmount: 0.25, SubmitSmRespAmount: 0.75, DecrementSubmitSmCount: 1},
	}
	for index, part := range parts {
		envelope, err := builder.BuildSubmitEnvelope(context.Background(), request, part)
		if err != nil {
			t.Fatalf("part %d: %v", index+1, err)
		}
		if envelope.RoutingKey() != "submit.sm.forced-cid" {
			t.Fatalf("part %d routing key=%q", index+1, envelope.RoutingKey())
		}
		if priority, _ := envelope.Properties().Priority(); priority != 3 {
			t.Fatalf("part %d AMQP priority=%d", index+1, priority)
		}
		headers := envelope.Properties().Headers()
		if aggregate, _ := headers["aggregate-message-id"].String(); aggregate != "aggregate-linked" {
			t.Fatalf("part %d aggregate id=%q", index+1, aggregate)
		}
		if expiration, _ := headers["expiration"].String(); expiration != "2026-07-29 12:34:56.123456" {
			t.Fatalf("part %d expiration=%q", index+1, expiration)
		}
	}
	if len(encoder.requests) != 2 ||
		string(encoder.requests[0].RawPDU.ShortMessage) != "part-1" ||
		string(encoder.requests[1].RawPDU.ShortMessage) != "part-2" ||
		encoder.requests[0].RawPDU.PriorityFlag != 1 ||
		encoder.requests[1].RawPDU.PriorityFlag != 2 {
		t.Fatalf("ordered raw encode requests=%+v", encoder.requests)
	}
	if len(encoder.requests[0].CustomTLVs) != 1 || len(encoder.requests[1].CustomTLVs) != 0 {
		t.Fatalf("per-part custom TLVs=%+v", encoder.requests)
	}
}

func TestSubmitEnvelopeBuilderUsesPythonCompatibleScientificLateAmount(t *testing.T) {
	encoder := &recordingEncoder{}
	builder, err := outbound.NewSubmitEnvelopeBuilder(encoder)
	if err != nil {
		t.Fatal(err)
	}
	segmented, err := segmentation.Segment(segmentation.Request{
		Payload: []byte("x"), SplitMethod: segmentation.SplitSAR, MaxParts: 1, Reference: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := segmented.Parts()
	request := core.SubmitEnvelopeRequest{
		MessageID: "m", BillID: "b", CreatedAt: time.Now(), Username: "alice", UserID: "u",
		ConnectorID: "c", DestinationAddr: []byte("1"), Parts: parts,
		Bill: billing.Bill{SubmitSmAmount: 3.3e-08, SubmitSmRespAmount: 6.7e-08, DecrementSubmitSmCount: 1},
	}
	envelope, err := builder.BuildSubmitEnvelope(context.Background(), request, parts[0])
	if err != nil {
		t.Fatal(err)
	}
	got, _ := envelope.Properties().Headers()["late-bill-amount"].String()
	if got != "6.7e-08" {
		t.Fatalf("late amount=%q want=%q", got, "6.7e-08")
	}
}

func TestSubmitEnvelopeBuilderUsesPythonCompatibleLateAmountNotationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		amount float64
		want   string
	}{
		{name: "zero keeps fractional suffix", amount: 0, want: "0.0"},
		{name: "integer keeps fractional suffix", amount: 1, want: "1.0"},
		{name: "fixed lower boundary", amount: 1e-4, want: "0.0001"},
		{name: "scientific below lower boundary", amount: 1e-5, want: "1e-05"},
		{name: "fixed upper interior", amount: 1e15, want: "1000000000000000.0"},
		{name: "scientific upper boundary", amount: 1e16, want: "1e+16"},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoder := &recordingEncoder{}
			builder, err := outbound.NewSubmitEnvelopeBuilder(encoder)
			if err != nil {
				t.Fatal(err)
			}
			segmented, err := segmentation.Segment(segmentation.Request{
				Payload: []byte("x"), SplitMethod: segmentation.SplitSAR, MaxParts: 1, Reference: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			parts := segmented.Parts()
			request := core.SubmitEnvelopeRequest{
				MessageID: "m", BillID: "b", CreatedAt: time.Now(), Username: "alice", UserID: "u",
				ConnectorID: "c", DestinationAddr: []byte("1"), Parts: parts,
				Bill: billing.Bill{SubmitSmRespAmount: test.amount, DecrementSubmitSmCount: 1},
			}
			envelope, err := builder.BuildSubmitEnvelope(context.Background(), request, parts[0])
			if err != nil {
				t.Fatal(err)
			}
			got, _ := envelope.Properties().Headers()["late-bill-amount"].String()
			if got != test.want {
				t.Fatalf("late amount=%q want=%q", got, test.want)
			}
		})
	}
}
