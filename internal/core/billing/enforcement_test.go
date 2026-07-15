package billing_test

import (
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
)

func TestEnforcement(t *testing.T) {
	u := billing.NewUser(1)
	_ = u.SetBalance(1.0)
	u.SetSubmitSmCountQuota(10)

	// Case 1: Sufficient balance and count
	bill := billing.Bill{SubmitSmAmount: 0.1, DecrementSubmitSmCount: 1}
	if err := u.CanApply(bill); err != nil {
		t.Errorf("CanApply failed: %v", err)
	}

	// Case 2: Insufficient balance
	billLarge := billing.Bill{SubmitSmAmount: 1.1}
	if err := u.CanApply(billLarge); err == nil {
		t.Error("CanApply should fail for insufficient balance")
	}

	// Case 3: Insufficient count
	bill2 := billing.Bill{SubmitSmAmount: 0.0, DecrementSubmitSmCount: 11}
	if err := u.CanApply(bill2); err == nil {
		t.Error("CanApply should fail for insufficient count")
	}

	// Case 4: Group enforcement
	g := billing.NewGroup(1)
	_ = g.SetBalance(0.5)
	u.SetGroup(g)

	billGroup := billing.Bill{SubmitSmAmount: 0.6}
	if err := u.CanApply(billGroup); err == nil {
		t.Error("CanApply should fail for insufficient group balance")
	}
}

func TestMultipartBilling(t *testing.T) {
	u := billing.NewUser(1)
	_ = u.SetBalance(10.0)

	routeRate := 1.5
	segments := 3
	bill := billing.CalculateBill(routeRate, segments, u)

	expectedAmount := 1.5 * 3
	if bill.SubmitSmAmount != expectedAmount {
		t.Errorf("amount=%v want=%v", bill.SubmitSmAmount, expectedAmount)
	}
}

func TestSegmentationIntegration(t *testing.T) {
	u := billing.NewUser(1)
	_ = u.SetBalance(10.0)

	// 161 bytes DCS 0 -> 2 parts
	req := segmentation.Request{
		Payload:     make([]byte, 161),
		DataCoding:  0,
		SplitMethod: segmentation.SplitSAR,
		MaxParts:    5,
		Reference:   1,
	}
	res, err := segmentation.Segment(req)
	if err != nil {
		t.Fatalf("Segment failed: %v", err)
	}

	parts := res.Parts()
	if len(parts) != 2 {
		t.Fatalf("parts=%d want 2", len(parts))
	}

	routeRate := 1.0
	bill := billing.CalculateBill(routeRate, len(parts), u)
	if bill.SubmitSmAmount != 2.0 {
		t.Errorf("amount=%v want 2.0", bill.SubmitSmAmount)
	}
}
