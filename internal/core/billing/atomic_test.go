package billing_test

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/billing"
)

func TestAuthorizeAndApplySubmitSharedGroupIsLinearizable(t *testing.T) {
	group := billing.NewGroup(1)
	if err := group.SetBalance(100); err != nil {
		t.Fatal(err)
	}
	group.SetSubmitSmCountQuota(100)

	const users = 20
	const attemptsPerUser = 10
	createdUsers := make([]*billing.User, 0, users)
	var accepted atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < users; index++ {
		user := billing.NewUser(int64(index + 1))
		if err := user.SetBalance(100); err != nil {
			t.Fatal(err)
		}
		user.SetSubmitSmCountQuota(100)
		user.SetGroup(group)
		createdUsers = append(createdUsers, user)
		wait.Add(1)
		go func() {
			defer wait.Done()
			for attempt := 0; attempt < attemptsPerUser; attempt++ {
				err := user.AuthorizeAndApplySubmit(billing.Bill{
					SubmitSmAmount:         1,
					DecrementSubmitSmCount: 1,
				})
				if err == nil {
					accepted.Add(1)
					continue
				}
				if !errors.Is(err, billing.ErrInsufficientBalance) && !errors.Is(err, billing.ErrInsufficientCount) {
					t.Errorf("unexpected billing error: %v", err)
				}
			}
		}()
	}
	wait.Wait()

	if got := accepted.Load(); got != 100 {
		t.Fatalf("accepted=%d want=100", got)
	}
	state := group.GetState()
	if state.Balance == nil || *state.Balance != 0 {
		t.Fatalf("group balance=%v want=0", state.Balance)
	}
	if state.SubmitSmCountQuota == nil || *state.SubmitSmCountQuota != 0 {
		t.Fatalf("group count=%v want=0", state.SubmitSmCountQuota)
	}
	var userBalanceTotal float64
	var userCountTotal int
	for _, user := range createdUsers {
		state := user.GetState()
		if state.Balance == nil || *state.Balance < 0 {
			t.Fatalf("user %d balance=%v want non-negative", state.UID, state.Balance)
		}
		if state.SubmitSmCountQuota == nil || *state.SubmitSmCountQuota < 0 {
			t.Fatalf("user %d count=%v want non-negative", state.UID, state.SubmitSmCountQuota)
		}
		userBalanceTotal += *state.Balance
		userCountTotal += *state.SubmitSmCountQuota
	}
	if userBalanceTotal != users*100-float64(accepted.Load()) {
		t.Fatalf("user balance total=%v accepted=%d", userBalanceTotal, accepted.Load())
	}
	if userCountTotal != users*100-int(accepted.Load()) {
		t.Fatalf("user count total=%d accepted=%d", userCountTotal, accepted.Load())
	}
}

func TestAuthorizeAndApplySubmitRejectsInvalidBillsWithoutMutation(t *testing.T) {
	tests := []struct {
		name string
		bill billing.Bill
	}{
		{name: "nan early", bill: billing.Bill{SubmitSmAmount: math.NaN()}},
		{name: "infinite early", bill: billing.Bill{SubmitSmAmount: math.Inf(1)}},
		{name: "negative early", bill: billing.Bill{SubmitSmAmount: -1}},
		{name: "nan late", bill: billing.Bill{SubmitSmRespAmount: math.NaN()}},
		{name: "infinite late", bill: billing.Bill{SubmitSmRespAmount: math.Inf(1)}},
		{name: "negative late", bill: billing.Bill{SubmitSmRespAmount: -1}},
		{name: "negative count", bill: billing.Bill{DecrementSubmitSmCount: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user := billing.NewUser(1)
			if err := user.SetBalance(10); err != nil {
				t.Fatal(err)
			}
			user.SetSubmitSmCountQuota(10)
			if err := user.AuthorizeAndApplySubmit(test.bill); err == nil {
				t.Fatal("invalid bill was accepted")
			}
			state := user.GetState()
			if state.Balance == nil || *state.Balance != 10 || state.SubmitSmCountQuota == nil || *state.SubmitSmCountQuota != 10 {
				t.Fatalf("invalid bill mutated state: %+v", state)
			}
		})
	}
}

func FuzzAuthorizeAndApplySubmitNeverProducesNegativeQuota(f *testing.F) {
	f.Add(math.Float64bits(1), math.Float64bits(1), 1)
	f.Add(math.Float64bits(math.NaN()), math.Float64bits(0), -1)
	f.Fuzz(func(t *testing.T, earlyBits, lateBits uint64, count int) {
		user := billing.NewUser(1)
		if err := user.SetBalance(100); err != nil {
			t.Fatal(err)
		}
		user.SetSubmitSmCountQuota(100)
		_ = user.AuthorizeAndApplySubmit(billing.Bill{
			SubmitSmAmount:         math.Float64frombits(earlyBits),
			SubmitSmRespAmount:     math.Float64frombits(lateBits),
			DecrementSubmitSmCount: count,
		})
		state := user.GetState()
		if state.Balance == nil || math.IsNaN(*state.Balance) || math.IsInf(*state.Balance, 0) || *state.Balance < 0 {
			t.Fatalf("invalid balance post-state: %v", state.Balance)
		}
		if state.SubmitSmCountQuota == nil || *state.SubmitSmCountQuota < 0 {
			t.Fatalf("invalid count post-state: %v", state.SubmitSmCountQuota)
		}
	})
}

func TestAuthorizeAndApplySubmitChecksLateAmountButAppliesEarlyOnly(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.SetBalance(1.5); err != nil {
		t.Fatal(err)
	}
	bill := billing.Bill{SubmitSmAmount: 0.75, SubmitSmRespAmount: 0.75}
	if err := user.AuthorizeAndApplySubmit(bill); err != nil {
		t.Fatal(err)
	}
	if got := user.Balance(); got != 0.75 {
		t.Fatalf("balance=%v want=0.75", got)
	}
}
