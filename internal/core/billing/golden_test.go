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
	Segments  int      `json:"segments"`
	User      userSpec `json:"user"`
	Expected  expected `json:"expected"`
}
type userSpec struct {
	UID          int64      `json:"uid"`
	Balance      *float64   `json:"balance"`
	EarlyPercent *int       `json:"early_percent"`
	SmCount      *int       `json:"sm_count"`
	Group        *groupSpec `json:"group"`
}
type groupSpec struct {
	GID     int64    `json:"gid"`
	Balance *float64 `json:"balance"`
}
type expected struct {
	SubmitSmAmount         float64 `json:"submit_sm_amount"`
	SubmitSmRespAmount     float64 `json:"submit_sm_resp_amount"`
	DecrementSubmitSmCount int     `json:"decrement_submit_sm_count"`
	CanApply               bool    `json:"can_apply"`
	Error                  string  `json:"error"`
}

func TestGoldenBilling(t *testing.T) {
	doc := load(t)
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
			if tc.User.Group != nil {
				g := billing.NewGroup(tc.User.Group.GID)
				if tc.User.Group.Balance != nil {
					_ = g.SetBalance(*tc.User.Group.Balance)
				}
				u.SetGroup(g)
			}

			segments := tc.Segments
			if segments == 0 {
				segments = 1
			}

			bill := billing.CalculateBill(tc.RouteRate, segments, u)
			if math.Abs(bill.SubmitSmAmount-tc.Expected.SubmitSmAmount) > 1e-10 {
				t.Errorf("submit_sm_amount=%v want=%v", bill.SubmitSmAmount, tc.Expected.SubmitSmAmount)
			}
			if math.Abs(bill.SubmitSmRespAmount-tc.Expected.SubmitSmRespAmount) > 1e-10 {
				t.Errorf("submit_sm_resp_amount=%v want=%v", bill.SubmitSmRespAmount, tc.Expected.SubmitSmRespAmount)
			}
			if bill.DecrementSubmitSmCount != tc.Expected.DecrementSubmitSmCount {
				t.Errorf("decrement_submit_sm_count=%v want=%v", bill.DecrementSubmitSmCount, tc.Expected.DecrementSubmitSmCount)
			}

			err := u.CanApply(bill)
			if tc.Expected.CanApply {
				if err != nil {
					t.Errorf("CanApply expected true, got err: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("CanApply expected false, got nil")
				} else if err.Error() != tc.Expected.Error {
					t.Errorf("CanApply err=%q want=%q", err.Error(), tc.Expected.Error)
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
	if d.SchemaVersion != 2 || d.BaselineCommit != "b4e3b64dfd580a4d9bd80cda04299e1ded29adf3" {
		t.Fatalf("provenance: version=%d commit=%s", d.SchemaVersion, d.BaselineCommit)
	}
	return d
}
