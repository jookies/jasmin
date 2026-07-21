package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

var (
	ErrNoRouteMatched      = errors.New("no route matched")
	ErrInvalidSubmitConfig = errors.New("invalid submit service configuration")
	ErrInvalidEnvelopeSet  = errors.New("invalid submit envelope set")
)

type BillingUserDirectory interface {
	GetUserIdentity(username string) (*billing.User, string, error)
}

type AMQPPublisher interface {
	Publish(ctx context.Context, exchange, routingKey string, message amqpcompat.Envelope) error
}

// SubmitPublicationBoundary atomically accepts a complete logical submit for
// durable publication. submittransaction.Service implements this interface.
// Keeping this interface in core avoids coupling admission to PostgreSQL.
type SubmitPublicationBoundary interface {
	AdmitSubmit(context.Context, []amqpcompat.Envelope) error
}

type SubmitEnvelopeRequest struct {
	MessageID       string
	BillID          string
	CreatedAt       time.Time
	Username        string
	UserID          string
	ConnectorID     string
	SourceAddr      []byte
	DestinationAddr []byte
	DataCoding      uint8
	Priority        uint8
	ScheduleAt      *time.Time
	ValidityPeriod  *time.Duration
	DLR             bool
	DLRURL          string
	DLRLevel        int
	DLRMethod       string
	SourceConnector string
	Bill            billing.Bill
	Parts           []segmentation.Part
	CustomTLVs      map[uint16][]byte
}

type SubmitEnvelopeBuilder interface {
	BuildSubmitEnvelope(ctx context.Context, request SubmitEnvelopeRequest, part segmentation.Part) (amqpcompat.Envelope, error)
}

type SubmitServiceDependencies struct {
	InterceptorTable  *interceptor.Table
	InterceptorRunner interceptor.Runner
	RoutingTable      *routingtable.Table
	BillingUsers      BillingUserDirectory
	EnvelopeBuilder   SubmitEnvelopeBuilder
	Publisher         AMQPPublisher
	Transaction       SubmitPublicationBoundary
	NewMessageID      func() (string, error)
	NewBillID         func() (string, error)
	NewReference      func() (uint16, error)
	Now               func() time.Time
}

type SubmitService struct {
	dependencies SubmitServiceDependencies
}

func NewSubmitService(dependencies SubmitServiceDependencies) (*SubmitService, error) {
	if dependencies.InterceptorTable == nil || dependencies.RoutingTable == nil ||
		dependencies.BillingUsers == nil || dependencies.EnvelopeBuilder == nil ||
		(dependencies.Publisher == nil && dependencies.Transaction == nil) {
		return nil, ErrInvalidSubmitConfig
	}
	if dependencies.NewMessageID == nil {
		dependencies.NewMessageID = randomUUID
	}
	if dependencies.NewBillID == nil {
		dependencies.NewBillID = randomUUID
	}
	if dependencies.NewReference == nil {
		dependencies.NewReference = randomReference
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	return &SubmitService{dependencies: dependencies}, nil
}

func (service *SubmitService) Submit(ctx context.Context, request SubmitRequest) (string, error) {
	user, externalUserID, err := service.dependencies.BillingUsers.GetUserIdentity(request.Username)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthentication, err)
	}

	payload, err := submitPayload(request)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidParameter, err)
	}
	state := user.GetState()
	createdAt := service.dependencies.Now()
	var groupID int64
	if state.GID != nil {
		groupID = *state.GID
	}
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MT,
		UserID:          user.UID(),
		GroupID:         groupID,
		SourceAddr:      routingfilter.BytesField{Present: request.From != "", Value: []byte(request.From)},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte(request.Destination)},
		ShortMessage:    routingfilter.BytesField{Present: request.HexContent == "", Value: payload},
		MessagePayload:  routingfilter.BytesField{Present: request.HexContent != "", Value: payload},
		Timestamp:       createdAt,
		Tags:            append([]string(nil), request.Tags...),
	})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidParameter, err)
	}

	intercepted, err := service.dependencies.InterceptorTable.Intercept(ctx, service.dependencies.InterceptorRunner, routable)
	if err != nil {
		return "", err
	}
	if intercepted.Action == interceptor.ActionReject {
		return "", ErrFilterRejected
	}
	route, found, err := service.dependencies.RoutingTable.Select(intercepted.Routable)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrNoRouteMatched
	}

	reference, err := service.dependencies.NewReference()
	if err != nil {
		return "", err
	}
	messageField := intercepted.Routable.ShortMessage()
	if !messageField.Present {
		messageField = intercepted.Routable.MessagePayload()
	}
	segmented, err := segmentation.Segment(segmentation.Request{
		Payload:     messageField.Value,
		DataCoding:  uint8(request.Coding),
		SplitMethod: segmentation.SplitSAR,
		MaxParts:    10,
		Reference:   reference,
		CustomTLVs:  request.CustomTLVs,
	})
	if err != nil {
		return "", err
	}
	parts := segmented.Parts()
	aggregateBill := billing.CalculateBill(route.Rate(), len(parts), user)
	perPartBill := billing.CalculateBill(route.Rate(), 1, user)
	messageID, err := service.dependencies.NewMessageID()
	if err != nil {
		return "", err
	}
	billID, err := service.dependencies.NewBillID()
	if err != nil {
		return "", err
	}
	priority := request.Priority
	if priority < 0 || priority > 3 {
		return "", fmt.Errorf("%w: priority %d", ErrInvalidParameter, priority)
	}
	envelopeRequest := SubmitEnvelopeRequest{
		MessageID:       messageID,
		BillID:          billID,
		CreatedAt:       createdAt,
		Username:        request.Username,
		UserID:          externalUserID,
		ConnectorID:     route.Connector().ID(),
		SourceAddr:      intercepted.Routable.SourceAddr().Value,
		DestinationAddr: intercepted.Routable.DestinationAddr().Value,
		DataCoding:      uint8(request.Coding),
		Priority:        uint8(priority),
		ScheduleAt:      cloneTime(request.SDT),
		ValidityPeriod:  cloneDuration(request.ValidityPeriod),
		DLR:             request.DLR,
		DLRURL:          request.DLRUrl,
		DLRLevel:        request.DLRLevel,
		DLRMethod:       request.DLRMethod,
		SourceConnector: "httpapi",
		Bill:            perPartBill,
		Parts:           parts,
		CustomTLVs:      cloneTLVs(request.CustomTLVs),
	}
	envelopes := make([]amqpcompat.Envelope, 0, len(parts))
	for index, part := range parts {
		if part.Sequence() != uint8(index+1) {
			return "", fmt.Errorf("%w: part %d has sequence %d", ErrInvalidEnvelopeSet, index, part.Sequence())
		}
		envelope, err := service.dependencies.EnvelopeBuilder.BuildSubmitEnvelope(ctx, envelopeRequest, part)
		if err != nil {
			return "", err
		}
		if err := validateSubmitEnvelope(envelope, route.Connector().ID(), index); err != nil {
			return "", err
		}
		envelopes = append(envelopes, envelope)
	}

	if err := user.AuthorizeAndApplyCalculatedSubmit(route.Rate(), len(parts), aggregateBill); err != nil {
		return "", fmt.Errorf("%w: %v", ErrQuotaExceeded, err)
	}
	if service.dependencies.Transaction != nil {
		if err := service.dependencies.Transaction.AdmitSubmit(ctx, envelopes); err != nil {
			// Legacy HTTP/SMPP paths charge before invoking the client manager and
			// never refund on a downstream/durable-admission failure.
			return "", err
		}
	} else {
		for _, envelope := range envelopes {
			if err := service.dependencies.Publisher.Publish(ctx, "messaging", envelope.RoutingKey(), envelope); err != nil {
				// Explicit compatibility fallback for non-production callers. The
				// no-refund behavior is intentionally preserved.
				return "", err
			}
		}
	}
	return messageID, nil
}

func submitPayload(request SubmitRequest) ([]byte, error) {
	if request.HexContent != "" {
		payload, err := hex.DecodeString(request.HexContent)
		if err != nil {
			return nil, err
		}
		return payload, nil
	}
	return []byte(request.Content), nil
}

func validateSubmitEnvelope(envelope amqpcompat.Envelope, connectorID string, index int) error {
	route := envelope.Route()
	if route.Kind() != amqpcompat.RouteSubmitSM || route.Target() != connectorID {
		return fmt.Errorf("%w: envelope %d routes to %q", ErrInvalidEnvelopeSet, index, envelope.RoutingKey())
	}
	return nil
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func randomReference() (uint16, error) {
	var value [2]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, err
	}
	return uint16(value[0])<<8 | uint16(value[1]), nil
}

func cloneTLVs(values map[uint16][]byte) map[uint16][]byte {
	copy := make(map[uint16][]byte, len(values))
	for tag, value := range values {
		copy[tag] = append([]byte(nil), value...)
	}
	return copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneDuration(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
