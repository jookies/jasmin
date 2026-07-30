package core_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/billing"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

type lateBillingDirectory map[string]*billing.User

func (directory lateBillingDirectory) GetUserByID(userID string) (*billing.User, error) {
	user, ok := directory[userID]
	if !ok || user == nil {
		return nil, billing.ErrUserNotFound
	}
	return user, nil
}

type lateBillingGolden struct {
	Cases []struct {
		ID    string `json:"id"`
		Input struct {
			RoutingKey string          `json:"routing_key"`
			MessageID  string          `json:"message_id"`
			UserID     string          `json:"user_id"`
			Amount     string          `json:"amount"`
			Balance    json.RawMessage `json:"balance"`
		} `json:"input"`
		Expected struct {
			Action           core.LateBillingAction `json:"action"`
			BalanceAfter     *float64               `json:"balance_after"`
			BalanceAfterBits *string                `json:"balance_after_bits"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestGoldenLateBillingService(t *testing.T) {
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
			action, err := service.Process(lateBillingEnvelope(t, test.Input.RoutingKey, test.Input.MessageID, test.Input.UserID, test.Input.Amount))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if action != test.Expected.Action {
				t.Fatalf("action=%q want %q", action, test.Expected.Action)
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
				t.Fatalf("balance=%v bits=%v want bits=%v", state.Balance, state.Balance, test.Expected.BalanceAfterBits)
			}
		})
	}
}

func TestLateBillingServiceRejectsMalformedEnvelopeWithoutMutation(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	service, err := core.NewLateBillingService(lateBillingDirectory{"1": user})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		routingKey string
		userID     string
		amount     string
	}{
		{name: "wrong route", routingKey: "submit.sm.connector-a", userID: "1", amount: "1"},
		{name: "route user mismatch", routingKey: "bill_request.submit_sm_resp.2", userID: "1", amount: "1"},
		{name: "missing user header", routingKey: "bill_request.submit_sm_resp.1", userID: "", amount: "1"},
		{name: "invalid amount", routingKey: "bill_request.submit_sm_resp.1", userID: "1", amount: "not-a-number"},
		{name: "negative amount", routingKey: "bill_request.submit_sm_resp.1", userID: "1", amount: "-1"},
		{name: "nan amount", routingKey: "bill_request.submit_sm_resp.1", userID: "1", amount: "NaN"},
		{name: "infinite amount", routingKey: "bill_request.submit_sm_resp.1", userID: "1", amount: "+Inf"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := lateBillingEnvelope(t, test.routingKey, "bill-1", test.userID, test.amount)
			action, err := service.Process(envelope)
			if !errors.Is(err, core.ErrInvalidLateBillingEnvelope) || action != core.LateBillingNone {
				t.Fatalf("action=%q error=%v", action, err)
			}
		})
	}
	if got := user.Balance(); got != 10 {
		t.Fatalf("balance=%v want 10", got)
	}
}

func TestLateBillingServiceRejectsNonStringAmountHeader(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	service, err := core.NewLateBillingService(lateBillingDirectory{"1": user})
	if err != nil {
		t.Fatal(err)
	}
	properties, err := amqpcompat.NewProperties("bill-1", map[string]amqpcompat.Field{
		"user-id": amqpcompat.StringField("1"),
		"amount":  amqpcompat.IntegerField(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("bill_request.submit_sm_resp.1", properties, []byte("bill-1"))
	if err != nil {
		t.Fatal(err)
	}
	action, err := service.Process(envelope)
	if !errors.Is(err, core.ErrInvalidLateBillingEnvelope) || action != core.LateBillingNone {
		t.Fatalf("action=%q error=%v", action, err)
	}
	if got := user.Balance(); got != 10 {
		t.Fatalf("balance=%v want 10", got)
	}
}

func lateBillingEnvelope(t *testing.T, routingKey, messageID, userID, amount string) amqpcompat.Envelope {
	t.Helper()
	headers := map[string]amqpcompat.Field{"amount": amqpcompat.StringField(amount)}
	if userID != "" {
		headers["user-id"] = amqpcompat.StringField(userID)
	}
	properties, err := amqpcompat.NewProperties(messageID, headers)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope(routingKey, properties, []byte(messageID))
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func loadLateBillingFixture(t *testing.T) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "compat", "fixtures", "late-billing", "baseline.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
