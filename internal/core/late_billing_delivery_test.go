package core_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/billing"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

type fakeLateBillingDelivery struct {
	envelope    amqpcompat.Envelope
	ackCalls    int
	rejectCalls int
	requeue     []bool
	ackErr      error
	rejectErr   error
}

func (delivery *fakeLateBillingDelivery) Envelope() amqpcompat.Envelope {
	return delivery.envelope
}

func (delivery *fakeLateBillingDelivery) Ack() error {
	delivery.ackCalls++
	return delivery.ackErr
}

func (delivery *fakeLateBillingDelivery) Reject(requeue bool) error {
	delivery.rejectCalls++
	delivery.requeue = append(delivery.requeue, requeue)
	return delivery.rejectErr
}

type countingLateBillingProcessor struct {
	service *core.LateBillingService
	calls   int
}

func (processor *countingLateBillingProcessor) Process(envelope amqpcompat.Envelope) (core.LateBillingAction, error) {
	processor.calls++
	return processor.service.Process(envelope)
}

func TestGoldenLateBillingDeliverySettlement(t *testing.T) {
	var fixture lateBillingGolden
	if err := json.Unmarshal(loadLateBillingFixture(t), &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 10 {
		t.Fatalf("cases=%d want 10", len(fixture.Cases))
	}
	for index, test := range fixture.Cases {
		t.Run(test.ID, func(t *testing.T) {
			directory := billing.NewManager()
			var user *billing.User
			if string(test.Input.Balance) != `"missing"` {
				user = billing.NewUser(int64(index + 1))
				if string(test.Input.Balance) != "null" {
					var balance float64
					if err := json.Unmarshal(test.Input.Balance, &balance); err != nil {
						t.Fatal(err)
					}
					if err := user.SetBalance(balance); err != nil {
						t.Fatal(err)
					}
				}
				if err := directory.AddUserWithID("fixture-"+test.ID, test.Input.UserID, user); err != nil {
					t.Fatal(err)
				}
			}
			service, err := core.NewLateBillingService(directory)
			if err != nil {
				t.Fatal(err)
			}
			processor := &countingLateBillingProcessor{service: service}
			delivery := &fakeLateBillingDelivery{envelope: lateBillingEnvelope(t, test.Input.RoutingKey, test.Input.MessageID, test.Input.UserID, test.Input.Amount)}
			if err := core.ProcessLateBillingDelivery(processor, delivery); err != nil {
				t.Fatalf("ProcessLateBillingDelivery: %v", err)
			}
			if processor.calls != 1 {
				t.Fatalf("processor calls=%d want 1", processor.calls)
			}
			switch test.Expected.Action {
			case core.LateBillingAck:
				if delivery.ackCalls != 1 || delivery.rejectCalls != 0 {
					t.Fatalf("settlement ack=%d reject=%d", delivery.ackCalls, delivery.rejectCalls)
				}
			case core.LateBillingReject:
				if delivery.ackCalls != 0 || delivery.rejectCalls != 1 || len(delivery.requeue) != 1 || delivery.requeue[0] {
					t.Fatalf("settlement ack=%d reject=%d requeue=%v", delivery.ackCalls, delivery.rejectCalls, delivery.requeue)
				}
			case core.LateBillingNone:
				if delivery.ackCalls != 0 || delivery.rejectCalls != 0 {
					t.Fatalf("none settlement ack=%d reject=%d", delivery.ackCalls, delivery.rejectCalls)
				}
			default:
				t.Fatalf("unexpected fixture action %q", test.Expected.Action)
			}
			if user == nil {
				if test.Expected.BalanceAfter != nil {
					t.Fatal("missing user has non-null balance_after")
				}
				return
			}
			state := user.GetState()
			if test.Expected.BalanceAfter == nil {
				if state.Balance != nil {
					t.Fatalf("balance=%v want unlimited", *state.Balance)
				}
				return
			}
			if state.Balance == nil || test.Expected.BalanceAfterBits == nil || fmt.Sprintf("%016x", math.Float64bits(*state.Balance)) != *test.Expected.BalanceAfterBits {
				t.Fatalf("balance=%v want bits=%v", state.Balance, test.Expected.BalanceAfterBits)
			}
		})
	}
}

func TestLateBillingDeliveryLeavesProcessingErrorsUnsettled(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	service, err := core.NewLateBillingService(lateBillingDirectory{"1": user})
	if err != nil {
		t.Fatal(err)
	}
	processor := &countingLateBillingProcessor{service: service}
	delivery := &fakeLateBillingDelivery{envelope: lateBillingEnvelope(t, "submit.sm.connector-a", "bill-1", "1", "1")}
	if err := core.ProcessLateBillingDelivery(processor, delivery); !errors.Is(err, core.ErrInvalidLateBillingEnvelope) {
		t.Fatalf("error=%v", err)
	}
	if processor.calls != 1 || delivery.ackCalls != 0 || delivery.rejectCalls != 0 {
		t.Fatalf("calls=%d ack=%d reject=%d", processor.calls, delivery.ackCalls, delivery.rejectCalls)
	}
	if got := user.Balance(); got != 10 {
		t.Fatalf("balance=%v want 10", got)
	}
}

func TestLateBillingDeliveryReturnsSettlementErrors(t *testing.T) {
	brokerErr := errors.New("broker settlement failed")
	processor := lateBillingProcessorFunc(func(amqpcompat.Envelope) (core.LateBillingAction, error) {
		return core.LateBillingAck, nil
	})
	delivery := &fakeLateBillingDelivery{envelope: lateBillingEnvelope(t, "bill_request.submit_sm_resp.1", "bill-1", "1", "1"), ackErr: brokerErr}
	if err := core.ProcessLateBillingDelivery(processor, delivery); !errors.Is(err, brokerErr) {
		t.Fatalf("error=%v", err)
	}
	if delivery.ackCalls != 1 || delivery.rejectCalls != 0 {
		t.Fatalf("ack=%d reject=%d", delivery.ackCalls, delivery.rejectCalls)
	}
}

type lateBillingProcessorFunc func(amqpcompat.Envelope) (core.LateBillingAction, error)

func (function lateBillingProcessorFunc) Process(envelope amqpcompat.Envelope) (core.LateBillingAction, error) {
	return function(envelope)
}
