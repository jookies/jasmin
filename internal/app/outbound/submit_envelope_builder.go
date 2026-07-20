package outbound

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

var ErrInvalidSubmitEnvelope = errors.New("invalid production submit envelope")

type SubmitSMEncoder interface {
	EncodeSubmitSM(context.Context, picklecompat.SubmitSMEncodeRequest) (picklecompat.SubmitSMEncodeResult, error)
}

// SubmitEnvelopeBuilder converts the proven core orchestration request into
// the legacy protocol-2 SubmitSM body and exact AMQP properties expected by
// Python Jasmin consumers.
type SubmitEnvelopeBuilder struct {
	encoder SubmitSMEncoder
}

func NewSubmitEnvelopeBuilder(encoder SubmitSMEncoder) (*SubmitEnvelopeBuilder, error) {
	if encoder == nil {
		return nil, fmt.Errorf("%w: nil SubmitSM encoder", ErrInvalidSubmitEnvelope)
	}
	return &SubmitEnvelopeBuilder{encoder: encoder}, nil
}

func (builder *SubmitEnvelopeBuilder) BuildSubmitEnvelope(
	ctx context.Context,
	request core.SubmitEnvelopeRequest,
	part segmentation.Part,
) (amqpcompat.Envelope, error) {
	if request.MessageID == "" || request.BillID == "" || request.UserID == "" ||
		request.Username == "" || request.ConnectorID == "" || len(request.DestinationAddr) == 0 ||
		request.CreatedAt.IsZero() {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: missing identity or destination", ErrInvalidSubmitEnvelope)
	}
	if len(request.Parts) == 0 || part.Sequence() == 0 || int(part.Sequence()) > len(request.Parts) {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: invalid part sequence %d", ErrInvalidSubmitEnvelope, part.Sequence())
	}

	encodeRequest := picklecompat.SubmitSMEncodeRequest{
		Sequence:               int(part.Sequence()),
		SourceAddr:             picklecompat.Bytes(request.SourceAddr),
		DestinationAddr:        picklecompat.Bytes(request.DestinationAddr),
		ShortMessage:           picklecompat.Bytes(part.ShortMessage()),
		DataCoding:             request.DataCoding,
		Priority:               request.Priority,
		RegisteredDelivery:     request.DLR && int(part.Sequence()) == len(request.Parts),
		IncludeBill:            true,
		BillID:                 request.BillID,
		UserID:                 request.UserID,
		Username:               request.Username,
		SubmitSMAmount:         request.Bill.SubmitSmAmount,
		SubmitSMRespAmount:     request.Bill.SubmitSmRespAmount,
		DecrementSubmitSMCount: request.Bill.DecrementSubmitSmCount,
	}
	if request.ScheduleAt != nil {
		encodeRequest.ScheduleAt = request.ScheduleAt.Format(time.RFC3339Nano)
	}
	if request.ValidityPeriod != nil {
		if *request.ValidityPeriod < 0 {
			return amqpcompat.Envelope{}, fmt.Errorf("%w: negative validity period", ErrInvalidSubmitEnvelope)
		}
		encodeRequest.ValidityUntil = request.CreatedAt.Add(*request.ValidityPeriod).Format(time.RFC3339Nano)
	}
	if sar, ok := part.SAR(); ok {
		encodeRequest.SAR = &picklecompat.SubmitSMSAR{
			Reference: uint16(sar.Reference),
			Total:     sar.Total,
			Sequence:  sar.Sequence,
		}
	}
	if _, _, ok := part.UDH(); ok {
		encodeRequest.UDH = true
	}
	encodeRequest.CustomTLVs = sortedTLVs(request.CustomTLVs)

	encoded, err := builder.encoder.EncodeSubmitSM(ctx, encodeRequest)
	if err != nil {
		return amqpcompat.Envelope{}, fmt.Errorf("encode legacy SubmitSM: %w", err)
	}
	if len(encoded.Body) == 0 || len(encoded.Bill) == 0 {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: encoder returned empty body or bill", ErrInvalidSubmitEnvelope)
	}

	headers := map[string]amqpcompat.Field{
		"created_at":       amqpcompat.StringField(legacyDateTime(request.CreatedAt)),
		"source_connector": amqpcompat.StringField(sourceConnector(request.SourceConnector)),
		"submit_sm_bill":   amqpcompat.BytesField(encoded.Bill),
	}
	if request.ValidityPeriod != nil {
		headers["expiration"] = amqpcompat.StringField(legacyDateTime(request.CreatedAt.Add(*request.ValidityPeriod)))
	}
	properties, err := amqpcompat.NewProperties(
		request.MessageID,
		headers,
		amqpcompat.WithReplyTo("submit.sm.resp."+request.UserID),
		amqpcompat.WithPriority(request.Priority),
	)
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope("submit.sm."+request.ConnectorID, properties, encoded.Body)
}

func sortedTLVs(values map[uint16][]byte) []picklecompat.SubmitSMCustomTLV {
	tags := make([]int, 0, len(values))
	for tag := range values {
		tags = append(tags, int(tag))
	}
	sort.Ints(tags)
	result := make([]picklecompat.SubmitSMCustomTLV, 0, len(tags))
	for _, rawTag := range tags {
		tag := uint16(rawTag)
		result = append(result, picklecompat.SubmitSMCustomTLV{
			Tag:   tag,
			Value: picklecompat.Bytes(append([]byte(nil), values[tag]...)),
		})
	}
	return result
}

func legacyDateTime(value time.Time) string {
	value = value.Round(0)
	if value.Nanosecond() == 0 {
		return value.Format("2006-01-02 15:04:05")
	}
	return value.Format("2006-01-02 15:04:05.000000")
}

func sourceConnector(value string) string {
	if value == "smppsapi" {
		return value
	}
	return "httpapi"
}
