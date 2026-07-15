package billing_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/billing"
)

type document struct {
	SchemaVersion  int       `json:"schema_version"`
	BaselineCommit string    `json:"baseline_commit"`
	Cases          []fixture `json:"cases"`
}
type fixture struct {
	ID        string   `json:"id"`
	RouteRate float64  `json:"route_rate"`
	User      userSpec `json:"user"`
	Expected  expected `json:"expected"`
}
type userSpec struct {
	UID          int64    `json:"uid"`
	Balance      *float64 `json:"balance"`
	EarlyPercent *int     `json:"early_percent"`
	SmCount      *int     `json:"sm_count"`
}
type expected struct {
	SubmitSmAmount         float64 `json:"submit_sm_amount"`
	SubmitSmRespAmount     float64 `json:"submit_sm_resp_amount"`
	DecrementSubmitSmCount int     `json:"decrement_submit_sm_count"`
}

func TestGoldenBilling(t *testing.T) {
	doc := load(t)
	if len(doc.Cases) != 9 {
		t.Fatalf("cases=%d", len(doc.Cases))
	}
	for _, tc := range doc.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			u := billing.NewUser(tc.User.UID)
			if tc.User.Balance != nil {
				_ = u.SetBalance(*tc.User.Balance)
			}
			if tc.User.EarlyPercent != nil {
				_ = u.SetEarlyDecrementPercent(*tc.User.EarlyPercent)
			}
			if tc.User.SmCount != nil {
				u.SetSubmitSmCountQuota(*tc.User.SmCount)
			}

			bill := billing.CalculateBill(tc.RouteRate, 1, u)
			if bill.SubmitSmAmount != tc.Expected.SubmitSmAmount {
				t.Errorf("submit_sm_amount=%v want=%v", bill.SubmitSmAmount, tc.Expected.SubmitSmAmount)
			}
			if bill.SubmitSmRespAmount != tc.Expected.SubmitSmRespAmount {
				t.Errorf("submit_sm_resp_amount=%v want=%v", bill.SubmitSmRespAmount, tc.Expected.SubmitSmRespAmount)
			}
			if bill.DecrementSubmitSmCount != tc.Expected.DecrementSubmitSmCount {
				t.Errorf("decrement_submit_sm_count=%v want=%v", bill.DecrementSubmitSmCount, tc.Expected.DecrementSubmitSmCount)
			}

			// Verify ApplyBill
			initialBalance := 0.0
			if tc.User.Balance != nil {
				initialBalance = *tc.User.Balance
			}
			if err := u.ApplyBill(bill); err != nil {
				t.Fatalf("ApplyBill err=%v", err)
			}
			if tc.User.Balance != nil {
				// Use a small epsilon for float comparison
				expectedBalance := initialBalance - (tc.Expected.SubmitSmAmount + tc.Expected.SubmitSmRespAmount)
				gotBalance := u.Balance()
				if math.Abs(gotBalance-expectedBalance) > 1e-10 {
					t.Errorf("final_balance=%v want=%v", gotBalance, expectedBalance)
				}
			}
		})
	}
}

func TestBillingConcurrency(t *testing.T) {
	u := billing.NewUser(1)
	_ = u.SetBalance(1000.0)
	g := billing.NewGroup(1)
	_ = g.SetBalance(1000.0)
	u.SetGroup(g)

	bill := billing.Bill{SubmitSmAmount: 0.1, SubmitSmRespAmount: 0.0}
	const count = 1000
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			_ = u.ApplyBill(bill)
		}()
	}
	wg.Wait()

	// 1000 * 0.1 = 100.0
	// 1000.0 - 100.0 = 900.0
	if math.Abs(u.Balance()-900.0) > 1e-9 {
		t.Errorf("user balance=%v want 900.0", u.Balance())
	}
	if math.Abs(g.Balance()-900.0) > 1e-9 {
		t.Errorf("group balance=%v want 900.0", g.Balance())
	}
}

func load(t *testing.T) document {
	t.Helper()
	p := filepath.Join("..", "..", "..", "compat", "fixtures", "billing", "baseline.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var d document
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d.SchemaVersion != 1 || d.BaselineCommit != "4a7a2bcbaa5bce053f959d28992cae9bfa6f241c" {
		t.Fatal("provenance")
	}
	return d
}
