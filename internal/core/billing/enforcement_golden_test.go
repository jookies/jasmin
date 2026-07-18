package billing_test

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/billing"
)

type enforcementDocument struct {
	SchemaVersion  int               `json:"schema_version"`
	BaselineCommit string            `json:"baseline_commit"`
	CasesSHA256    string            `json:"cases_sha256"`
	Cases          []enforcementCase `json:"cases"`
}

type enforcementCase struct {
	ID    string `json:"id"`
	Input struct {
		Segments int `json:"segments"`
		User     struct {
			Balance       *float64 `json:"balance"`
			EarlyPercent  *int     `json:"early_percent"`
			SubmitSMCount *int     `json:"submit_sm_count"`
		} `json:"user"`
	} `json:"input"`
	Bill struct {
		SubmitSMAmountPerSegment     float64 `json:"submit_sm_amount_per_segment"`
		SubmitSMRespAmountPerSegment float64 `json:"submit_sm_resp_amount_per_segment"`
		DecrementCountPerSegment     int     `json:"decrement_submit_sm_count_per_segment"`
	} `json:"bill"`
	Expected struct {
		Accepted           bool     `json:"accepted"`
		BalanceAfter       *float64 `json:"balance_after"`
		SubmitSMCountAfter *int     `json:"submit_sm_count_after"`
	} `json:"expected"`
}

func TestGoldenSubmitBillingEnforcement(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "billing-enforcement", "baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document enforcementDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || document.BaselineCommit != "0aac58e466d583d0f0436df7b8afa3dc96191263" {
		t.Fatalf("unexpected provenance: version=%d baseline=%s", document.SchemaVersion, document.BaselineCommit)
	}
	if document.CasesSHA256 != "90eedbd9a5add4ce95a4c28a2fa2fd4528744aff9f28af168b87b69e46e1bf44" {
		t.Fatalf("unexpected corpus fingerprint: %s", document.CasesSHA256)
	}
	if len(document.Cases) != 7 {
		t.Fatalf("cases=%d want=7", len(document.Cases))
	}

	for index, testCase := range document.Cases {
		testCase := testCase
		t.Run(testCase.ID, func(t *testing.T) {
			user := billing.NewUser(int64(index + 1))
			if testCase.Input.User.Balance != nil {
				if err := user.SetBalance(*testCase.Input.User.Balance); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.Input.User.EarlyPercent != nil {
				if err := user.SetEarlyDecrementPercent(*testCase.Input.User.EarlyPercent); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.Input.User.SubmitSMCount != nil {
				user.SetSubmitSmCountQuota(*testCase.Input.User.SubmitSMCount)
			}

			bill := billing.Bill{
				SubmitSmAmount:         testCase.Bill.SubmitSMAmountPerSegment * float64(testCase.Input.Segments),
				SubmitSmRespAmount:     testCase.Bill.SubmitSMRespAmountPerSegment * float64(testCase.Input.Segments),
				DecrementSubmitSmCount: testCase.Bill.DecrementCountPerSegment * testCase.Input.Segments,
			}
			err := user.AuthorizeAndApplySubmit(bill)
			if testCase.Expected.Accepted && err != nil {
				t.Fatalf("accepted fixture rejected: %v", err)
			}
			if !testCase.Expected.Accepted && err == nil {
				t.Fatal("rejected fixture accepted")
			}
			if !testCase.Expected.Accepted && !errors.Is(err, billing.ErrInsufficientBalance) && !errors.Is(err, billing.ErrInsufficientCount) {
				t.Fatalf("unexpected rejection: %v", err)
			}

			state := user.GetState()
			assertOptionalFloat(t, state.Balance, testCase.Expected.BalanceAfter)
			assertOptionalInt(t, state.SubmitSmCountQuota, testCase.Expected.SubmitSMCountAfter)
		})
	}
}

func assertOptionalFloat(t *testing.T, got, want *float64) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("float presence got=%v want=%v", got, want)
		}
		return
	}
	if math.Abs(*got-*want) > 1e-12 {
		t.Fatalf("float got=%v want=%v", *got, *want)
	}
}

func assertOptionalInt(t *testing.T, got, want *int) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("int presence got=%v want=%v", got, want)
		}
		return
	}
	if *got != *want {
		t.Fatalf("int got=%v want=%v", *got, *want)
	}
}
