package billing_test

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/billing"
)

func TestApplyLateChargeBoundaries(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.SetBalance(1); err != nil {
		t.Fatal(err)
	}
	if err := user.ApplyLateCharge(1); err != nil {
		t.Fatalf("equality charge: %v", err)
	}
	if got := user.Balance(); got != 0 {
		t.Fatalf("balance=%v want 0", got)
	}
	if err := user.ApplyLateCharge(0); err != nil {
		t.Fatalf("zero charge: %v", err)
	}
	if err := user.ApplyLateCharge(0.01); !errors.Is(err, billing.ErrInsufficientBalance) {
		t.Fatalf("error=%v want insufficient balance", err)
	}
}

func TestApplyLateChargeUnlimitedIsExplicit(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.ApplyLateCharge(1); !errors.Is(err, billing.ErrUnlimitedBalance) {
		t.Fatalf("error=%v want unlimited balance", err)
	}
	if user.GetState().Balance != nil {
		t.Fatal("unlimited balance mutated")
	}
}

func TestApplyLateChargeRejectsInvalidAmountWithoutMutation(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	for _, amount := range []float64{-1, math.Inf(1), math.NaN()} {
		if err := user.ApplyLateCharge(amount); !errors.Is(err, billing.ErrInvalidRate) {
			t.Fatalf("amount=%v error=%v want invalid rate", amount, err)
		}
	}
	if got := user.Balance(); got != 10 {
		t.Fatalf("balance=%v want 10", got)
	}
}

func TestApplyLateChargeConcurrentNeverOverdraws(t *testing.T) {
	user := billing.NewUser(1)
	if err := user.SetBalance(100); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var insufficient atomic.Int64
	var unexpected atomic.Int64
	var wg sync.WaitGroup
	for range 1000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := user.ApplyLateCharge(1)
			switch {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, billing.ErrInsufficientBalance):
				insufficient.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 100 || insufficient.Load() != 900 || unexpected.Load() != 0 {
		t.Fatalf("accepted=%d insufficient=%d unexpected=%d", accepted.Load(), insufficient.Load(), unexpected.Load())
	}
	if got := user.Balance(); got != 0 {
		t.Fatalf("balance=%v want 0", got)
	}
}
