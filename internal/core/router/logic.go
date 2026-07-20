package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

var (
	ErrExpiredMessage = errors.New("message expired")
)

// CheckExpiry validates the expiration header of an AMQP envelope.
// It returns ErrExpiredMessage if the message has expired.
func CheckExpiry(now time.Time, envelope amqpcompat.Envelope) error {
	headers := envelope.Properties().Headers()
	expirationField, ok := headers["expiration"]
	if !ok {
		return nil // No expiration set
	}

	expirationText, ok := expirationField.String()
	if !ok || expirationText == "" {
		return nil
	}

	// Jasmin uses '%Y-%m-%d %H:%M:%S' format.
	// We try standard ISO first, then fall back to Jasmin format.
	expiration, err := time.Parse(time.RFC3339, expirationText)
	if err != nil {
		expiration, err = time.Parse("2006-01-02 15:04:05", expirationText)
		if err != nil {
			fmt.Printf("DEBUG: parse error for %q: %v\n", expirationText, err)
			return nil // Malformed expiration is ignored in legacy
		}
	}

	if expiration.Before(now) {
		return fmt.Errorf("%w: expiration %s", ErrExpiredMessage, expirationText)
	}

	return nil
}

// processBillingDelivery wraps the core settlement logic with expiry and error handling.
func (s *RouterService) processBillingDelivery(ctx context.Context, processor core.LateBillingDecisionProcessor, delivery core.LateBillingDelivery) error {
	// A-011: Check expiry first
	if err := CheckExpiry(time.Now(), delivery.Envelope()); err != nil {
		// Discard expired messages (Reject without requeue)
		return delivery.Reject(false)
	}

	// Process billing decision
	action, err := processor.Process(delivery.Envelope())
	if err != nil {
		// Transient errors should be requeued (e.g. database timeout)
		return delivery.Reject(true)
	}

	switch action {
	case core.LateBillingAck:
		return delivery.Ack()
	case core.LateBillingReject:
		return delivery.Reject(false)
	case core.LateBillingNone:
		// Frozen unlimited-balance behavior: no action, but we should probably 
		// reject to avoid broker accumulation.
		return delivery.Reject(true)
	default:
		return delivery.Reject(false)
	}
}

// processDeliverSM is a stub for MO/DLR dispatching (Macro 2).
func (s *RouterService) processDeliverSM(ctx context.Context, delivery *amqpcompat.Delivery) error {
	// A-011: Check expiry
	if err := CheckExpiry(time.Now(), delivery.Envelope()); err != nil {
		return delivery.Reject(false)
	}

	// @TODO: Implement Macro 2 dispatching logic here.
	return delivery.Reject(true) 
}
