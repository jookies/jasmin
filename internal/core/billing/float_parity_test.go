package billing_test

import (
	"math"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/billing"
)

func TestCalculateBillPreservesLegacyUnitFirstBinary64Order(t *testing.T) {
	tests := []struct {
		name                string
		rate                float64
		percent, segments   int
		earlyBits, lateBits uint64
		requiredBits        uint64
	}{
		{"early debit discriminator", 0.1, 33, 3, 0x3fb95810624dd2f2, 0x3fc9ba5e353f7cee, 0x3fd3333333333334},
		{"authorization discriminator", 0.01, 7, 3, 0x3f613404ea4a8c16, 0x3f9c91d14e3bcd35, 0x3f9eb851eb851eb7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user := billing.NewUser(1)
			if err := user.SetBalance(1); err != nil {
				t.Fatal(err)
			}
			if err := user.SetEarlyDecrementPercent(test.percent); err != nil {
				t.Fatal(err)
			}
			bill := billing.CalculateBill(test.rate, test.segments, user)
			if got := math.Float64bits(bill.SubmitSmAmount); got != test.earlyBits {
				t.Fatalf("early bits=%016x want=%016x", got, test.earlyBits)
			}
			if got := math.Float64bits(bill.SubmitSmRespAmount); got != test.lateBits {
				t.Fatalf("late bits=%016x want=%016x", got, test.lateBits)
			}
			if got := math.Float64bits(bill.AuthorizationAmount); got != test.requiredBits {
				t.Fatalf("required bits=%016x want=%016x", got, test.requiredBits)
			}
		})
	}
}

func TestAuthorizeCalculatedSubmitUsesLegacyRequiredTotalAtULPBoundary(t *testing.T) {
	const requiredBits = uint64(0x3f9eb851eb851eb7)
	for _, test := range []struct {
		name    string
		balance float64
		accept  bool
	}{
		{"previous", math.Float64frombits(requiredBits - 1), false},
		{"equal", math.Float64frombits(requiredBits), true},
		{"next", math.Float64frombits(requiredBits + 1), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := billing.NewUser(1)
			if err := user.SetBalance(test.balance); err != nil {
				t.Fatal(err)
			}
			if err := user.SetEarlyDecrementPercent(7); err != nil {
				t.Fatal(err)
			}
			bill := billing.CalculateBill(0.01, 3, user)
			err := user.AuthorizeAndApplyCalculatedSubmit(0.01, 3, bill)
			if (err == nil) != test.accept {
				t.Fatalf("err=%v accept=%v", err, test.accept)
			}
			if test.accept {
				const earlyBits = uint64(0x3f613404ea4a8c16)
				want := test.balance - math.Float64frombits(earlyBits)
				if got := math.Float64bits(user.Balance()); got != math.Float64bits(want) {
					t.Fatalf("balance bits=%016x want=%016x", got, math.Float64bits(want))
				}
			}
		})
	}
}
