package billing

import (
	"errors"
	"math"
)

var (
	ErrInvalidRate    = errors.New("invalid route rate")
	ErrInvalidPercent = errors.New("invalid early decrement percent")
)

type User struct {
	uid                           int64
	balance                       *float64
	earlyDecrementBalancePercent *int
	submitSmCountQuota            *int
}

func NewUser(uid int64) *User {
	return &User{uid: uid}
}

func (u *User) SetBalance(balance float64) {
	u.balance = &balance
}

func (u *User) SetEarlyDecrementPercent(percent int) {
	u.earlyDecrementBalancePercent = &percent
}

func (u *User) SetSubmitSmCountQuota(count int) {
	u.submitSmCountQuota = &count
}

type Bill struct {
	SubmitSmAmount         float64
	SubmitSmRespAmount     float64
	DecrementSubmitSmCount int
}

func CalculateBill(routeRate float64, u *User) Bill {
	bill := Bill{}

	// B-001/B-006/B-007: Rate calculation
	// Jasmin Rule 1: If route is rated and user's balance is not unlimited (balance != None)
	if routeRate > 0 && u.balance != nil {
		if u.earlyDecrementBalancePercent != nil {
			// Early decrement percentage applied (B-006, B-007)
			percent := float64(*u.earlyDecrementBalancePercent)
			bill.SubmitSmAmount = routeRate * percent / 100.0
			bill.SubmitSmRespAmount = routeRate - bill.SubmitSmAmount
		} else {
			// Default: 100% early decrement
			bill.SubmitSmAmount = routeRate
			bill.SubmitSmRespAmount = 0
		}
	}

	// B-003: Unlimited balance (u.balance == nil) results in 0.0 amounts, handled by check above.

	// Jasmin Rule 2: Decrement submit_sm_count if not unlimited
	if u.submitSmCountQuota != nil {
		bill.DecrementSubmitSmCount = 1
	}

	return bill
}

func ValidateParams(routeRate float64, earlyPercent *int) error {
	if math.IsNaN(routeRate) || math.IsInf(routeRate, 0) || routeRate < 0 {
		return ErrInvalidRate
	}
	if earlyPercent != nil && (*earlyPercent < 1 || *earlyPercent > 100) {
		return ErrInvalidPercent
	}
	return nil
}
