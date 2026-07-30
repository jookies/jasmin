package core

import "github.com/pumpitspace/synevyr/internal/transport/amqpcompat"

// LateBillingDecisionProcessor is the broker-independent late-billing decision
// boundary. LateBillingService implements it.
type LateBillingDecisionProcessor interface {
	Process(amqpcompat.Envelope) (LateBillingAction, error)
}

// LateBillingDelivery owns an unsettled broker delivery.
type LateBillingDelivery interface {
	Envelope() amqpcompat.Envelope
	Ack() error
	Reject(requeue bool) error
}

var _ LateBillingDelivery = (*amqpcompat.Delivery)(nil)

// ProcessLateBillingDelivery applies one decision and only then executes its
// legacy terminal broker action. Processing failures and LateBillingNone remain
// unsettled for outer supervision.
func ProcessLateBillingDelivery(processor LateBillingDecisionProcessor, delivery LateBillingDelivery) error {
	action, err := processor.Process(delivery.Envelope())
	if err != nil {
		return err
	}
	switch action {
	case LateBillingAck:
		return delivery.Ack()
	case LateBillingReject:
		return delivery.Reject(false)
	case LateBillingNone:
		return nil
	default:
		return ErrInvalidLateBillingEnvelope
	}
}
