package outbound_test

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

type recordingEncoder struct {
	requests []picklecompat.SubmitSMEncodeRequest
}

func (encoder *recordingEncoder) EncodeSubmitSM(_ context.Context, request picklecompat.SubmitSMEncodeRequest) (picklecompat.SubmitSMEncodeResult, error) {
	encoder.requests = append(encoder.requests, request)
	return picklecompat.SubmitSMEncodeResult{Body: []byte{0x80, 0x02, byte(request.Sequence)}, Bill: []byte{0x80, 0x02, 0x42}}, nil
}

func TestSubmitEnvelopeBuilderProjectsLegacyPropertiesAndPerPartBill(t *testing.T) {
	encoder := &recordingEncoder{}
	builder, err := outbound.NewSubmitEnvelopeBuilder(encoder)
	if err != nil {
		t.Fatal(err)
	}
	segmented, err := segmentation.Segment(segmentation.Request{
		Payload:     make([]byte, 161),
		DataCoding:  0,
		SplitMethod: segmentation.SplitSAR,
		MaxParts:    5,
		Reference:   42,
		CustomTLVs:  map[uint16][]byte{0x1401: {0x02}, 0x1400: {0x01}},
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
		CustomTLVs: map[uint16][]byte{0x1401: {0x02}, 0x1400: {0x01}},
	}

	first, err := builder.BuildSubmitEnvelope(context.Background(), request, parts[0])
	if err != nil {
		t.Fatal(err)
	}
	last, err := builder.BuildSubmitEnvelope(context.Background(), request, parts[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, envelope := range []struct {
		name string
		env  interface {
			RoutingKey() string
		}
	}{{"first", first}, {"last", last}} {
		if envelope.env.RoutingKey() != "submit.sm.connector-a" {
			t.Fatalf("%s routing key=%q", envelope.name, envelope.env.RoutingKey())
		}
	}
	properties := first.Properties()
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
	if len(encoder.requests) != 2 {
		t.Fatalf("encode calls=%d", len(encoder.requests))
	}
	if encoder.requests[0].RegisteredDelivery {
		t.Fatal("DLR must not be requested on the first multipart PDU")
	}
	if !encoder.requests[1].RegisteredDelivery {
		t.Fatal("DLR must be requested on the last multipart PDU")
	}
	if encoder.requests[0].SAR == nil || encoder.requests[0].SAR.Reference != 42 || encoder.requests[0].SAR.Sequence != 1 {
		t.Fatalf("SAR=%+v", encoder.requests[0].SAR)
	}
	if len(encoder.requests[0].CustomTLVs) != 2 || encoder.requests[0].CustomTLVs[0].Tag != 0x1400 {
		t.Fatalf("custom TLVs not deterministic: %+v", encoder.requests[0].CustomTLVs)
	}
	if encoder.requests[0].SubmitSMRespAmount != 0.5 || encoder.requests[0].DecrementSubmitSMCount != 1 {
		t.Fatalf("per-part bill request=%+v", encoder.requests[0])
	}
}
