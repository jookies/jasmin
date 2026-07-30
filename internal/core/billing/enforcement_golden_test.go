package billing_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/billing"
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
		RouteRate float64 `json:"route_rate"`
		Segments  int     `json:"segments"`
		User      struct {
			Balance       *float64 `json:"balance"`
			EarlyPercent  *int     `json:"early_percent"`
			SubmitSMCount *int     `json:"submit_sm_count"`
		} `json:"user"`
	} `json:"input"`
	Bill struct {
		SubmitSMAmountPerSegment     float64 `json:"submit_sm_amount_per_segment"`
		SubmitSMRespAmountPerSegment float64 `json:"submit_sm_resp_amount_per_segment"`
		DecrementCountPerSegment     int     `json:"decrement_submit_sm_count_per_segment"`
		RequiredTotalBalance         float64 `json:"required_total_balance"`
		LateAmountText               string  `json:"late_amount_text"`
		Bits                         struct {
			EarlyPerSegment string `json:"submit_sm_amount_per_segment"`
			LatePerSegment  string `json:"submit_sm_resp_amount_per_segment"`
			RequiredTotal   string `json:"required_total_balance"`
			EarlyDebitTotal string `json:"early_debit_total"`
		} `json:"bits"`
	} `json:"bill"`
	Expected struct {
		Accepted           bool     `json:"accepted"`
		BalanceAfter       *float64 `json:"balance_after"`
		SubmitSMCountAfter *int     `json:"submit_sm_count_after"`
		BalanceAfterBits   *string  `json:"balance_after_bits"`
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
	if document.CasesSHA256 != "436e691ecd7c4513937257789b99fde5ce04d85d07c1e9fb5e5dbca901062c19" {
		t.Fatalf("unexpected corpus fingerprint: %s", document.CasesSHA256)
	}
	if len(document.Cases) != 12 {
		t.Fatalf("cases=%d want=12", len(document.Cases))
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

			bill := billing.CalculateBill(testCase.Input.RouteRate, testCase.Input.Segments, user)
			if got := fmt.Sprintf("%016x", math.Float64bits(bill.SubmitSmAmount)); got != testCase.Bill.Bits.EarlyDebitTotal {
				t.Fatalf("early debit bits=%s want=%s", got, testCase.Bill.Bits.EarlyDebitTotal)
			}
			if got := fmt.Sprintf("%016x", math.Float64bits(bill.AuthorizationAmount)); got != testCase.Bill.Bits.RequiredTotal {
				t.Fatalf("authorization bits=%s want=%s", got, testCase.Bill.Bits.RequiredTotal)
			}
			err := user.AuthorizeAndApplyCalculatedSubmit(testCase.Input.RouteRate, testCase.Input.Segments, bill)
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
			assertOptionalFloatBits(t, state.Balance, testCase.Expected.BalanceAfterBits)
			assertOptionalInt(t, state.SubmitSmCountQuota, testCase.Expected.SubmitSMCountAfter)
		})
	}
}

func assertOptionalFloatBits(t *testing.T, got *float64, want *string) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("float presence got=%v want=%v", got, want)
		}
		return
	}
	if value := fmt.Sprintf("%016x", math.Float64bits(*got)); value != *want {
		t.Fatalf("float bits got=%s want=%s", value, *want)
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
