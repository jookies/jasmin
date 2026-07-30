package core

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/pumpitspace/synevyr/internal/core/billing"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

var (
	ErrInvalidLateBillingConfig   = errors.New("invalid late billing service configuration")
	ErrInvalidLateBillingEnvelope = errors.New("invalid late billing envelope")
)

type LateBillingAction string

const (
	LateBillingNone   LateBillingAction = "none"
	LateBillingAck    LateBillingAction = "ack"
	LateBillingReject LateBillingAction = "reject"
)

type LateBillingUserDirectory interface {
	GetUserByID(userID string) (*billing.User, error)
}

type LateBillingService struct {
	users LateBillingUserDirectory
}

func NewLateBillingService(users LateBillingUserDirectory) (*LateBillingService, error) {
	if users == nil {
		return nil, ErrInvalidLateBillingConfig
	}
	return &LateBillingService{users: users}, nil
}

// Process validates one opaque AMQP compatibility envelope and applies only
// the frozen callback's in-memory decision. Broker ACK/reject execution remains
// the caller's responsibility.
func (service *LateBillingService) Process(envelope amqpcompat.Envelope) (LateBillingAction, error) {
	route := envelope.Route()
	if route.Kind() != amqpcompat.RouteBillingSubmitSMResponse {
		return LateBillingNone, fmt.Errorf("%w: route %s", ErrInvalidLateBillingEnvelope, route.Kind())
	}
	headers := envelope.Properties().Headers()
	userID, ok := stringHeader(headers, "user-id")
	if !ok || userID == "" || userID != route.Target() {
		return LateBillingNone, fmt.Errorf("%w: invalid user-id", ErrInvalidLateBillingEnvelope)
	}
	amountText, ok := stringHeader(headers, "amount")
	if !ok {
		return LateBillingNone, fmt.Errorf("%w: invalid amount header", ErrInvalidLateBillingEnvelope)
	}
	amount, err := strconv.ParseFloat(amountText, 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return LateBillingNone, fmt.Errorf("%w: invalid amount %q", ErrInvalidLateBillingEnvelope, amountText)
	}

	user, err := service.users.GetUserByID(userID)
	if errors.Is(err, billing.ErrUserNotFound) {
		return LateBillingReject, nil
	}
	if err != nil {
		return LateBillingNone, err
	}
	err = user.ApplyLateCharge(amount)
	switch {
	case err == nil:
		return LateBillingAck, nil
	case errors.Is(err, billing.ErrInsufficientBalance):
		return LateBillingReject, nil
	case errors.Is(err, billing.ErrUnlimitedBalance):
		return LateBillingNone, nil
	default:
		return LateBillingNone, err
	}
}

func stringHeader(headers map[string]amqpcompat.Field, name string) (string, bool) {
	field, ok := headers[name]
	if !ok {
		return "", false
	}
	return field.String()
}
