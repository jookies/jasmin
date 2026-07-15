package billing_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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
				u.SetBalance(*tc.User.Balance)
			}
			if tc.User.EarlyPercent != nil {
				u.SetEarlyDecrementPercent(*tc.User.EarlyPercent)
			}
			if tc.User.SmCount != nil {
				u.SetSubmitSmCountQuota(*tc.User.SmCount)
			}

			bill := billing.CalculateBill(tc.RouteRate, u)
			if bill.SubmitSmAmount != tc.Expected.SubmitSmAmount {
				t.Errorf("submit_sm_amount=%v want=%v", bill.SubmitSmAmount, tc.Expected.SubmitSmAmount)
			}
			if bill.SubmitSmRespAmount != tc.Expected.SubmitSmRespAmount {
				t.Errorf("submit_sm_resp_amount=%v want=%v", bill.SubmitSmRespAmount, tc.Expected.SubmitSmRespAmount)
			}
			if bill.DecrementSubmitSmCount != tc.Expected.DecrementSubmitSmCount {
				t.Errorf("decrement_submit_sm_count=%v want=%v", bill.DecrementSubmitSmCount, tc.Expected.DecrementSubmitSmCount)
			}
		})
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
