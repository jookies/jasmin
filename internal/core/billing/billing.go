package billing

import (
	"errors"
	"math"
	"sync"
)

var (
	ErrInvalidRate    = errors.New("invalid route rate")
	ErrInvalidPercent = errors.New("invalid early decrement percent")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrInsufficientCount   = errors.New("insufficient submit_sm_count")
)

type User struct {
	mu                            sync.Mutex
	uid                           int64
	balance                       *float64
	earlyDecrementBalancePercent *int
	submitSmCountQuota            *int
	group                         *Group
}

type Group struct {
	mu                 sync.Mutex
	gid                int64
	balance            *float64
	submitSmCountQuota *int
}

func NewGroup(gid int64) *Group {
	return &Group{gid: gid}
}

func (g *Group) SetBalance(balance float64) error {
	if err := ValidateParams(balance, nil); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.balance = &balance
	return nil
}

func (g *Group) Balance() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.balance == nil {
		return 0
	}
	return *g.balance
}

func (g *Group) CanApply(bill Bill) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.balance != nil {
		total := (bill.SubmitSmAmount + bill.SubmitSmRespAmount)
		if *g.balance < total {
			return ErrInsufficientBalance
		}
	}
	if g.submitSmCountQuota != nil {
		total := bill.DecrementSubmitSmCount
		if *g.submitSmCountQuota < total {
			return ErrInsufficientCount
		}
	}
	return nil
}

func (g *Group) SetSubmitSmCountQuota(count int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.submitSmCountQuota = &count
}

func NewUser(uid int64) *User {
	return &User{uid: uid}
}

func (u *User) SetGroup(g *Group) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.group = g
}

func (u *User) SetBalance(balance float64) error {
	if err := ValidateParams(balance, nil); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.balance = &balance
	return nil
}

func (u *User) Balance() float64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.balance == nil {
		return 0
	}
	return *u.balance
}

func (u *User) CanApply(bill Bill) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.balance != nil {
		total := (bill.SubmitSmAmount + bill.SubmitSmRespAmount)
		if *u.balance < total {
			return ErrInsufficientBalance
		}
	}
	if u.submitSmCountQuota != nil {
		total := bill.DecrementSubmitSmCount
		if *u.submitSmCountQuota < total {
			return ErrInsufficientCount
		}
	}

	if u.group != nil {
		return u.group.CanApply(bill)
	}

	return nil
}

func (u *User) SetEarlyDecrementPercent(percent int) error {
	if err := ValidateParams(0, &percent); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.earlyDecrementBalancePercent = &percent
	return nil
}

func (u *User) SetSubmitSmCountQuota(count int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.submitSmCountQuota = &count
}

func (u *User) ApplyBill(bill Bill) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.balance != nil {
		*u.balance -= (bill.SubmitSmAmount + bill.SubmitSmRespAmount)
	}
	if u.submitSmCountQuota != nil {
		*u.submitSmCountQuota -= bill.DecrementSubmitSmCount
	}
	if u.group != nil {
		u.group.mu.Lock()
		defer u.group.mu.Unlock()
		if u.group.balance != nil {
			*u.group.balance -= (bill.SubmitSmAmount + bill.SubmitSmRespAmount)
		}
		if u.group.submitSmCountQuota != nil {
			*u.group.submitSmCountQuota -= bill.DecrementSubmitSmCount
		}
	}
	return nil
}

type Bill struct {
	SubmitSmAmount         float64
	SubmitSmRespAmount     float64
	DecrementSubmitSmCount int
}

func CalculateBill(routeRate float64, segments int, u *User) Bill {
	u.mu.Lock()
	defer u.mu.Unlock()
	bill := Bill{}

	// B-001/B-006/B-007: Rate calculation
	// Jasmin Rule 1: If route is rated and user's balance is not unlimited (balance != None)
	if routeRate > 0 && u.balance != nil {
		totalRate := routeRate * float64(segments)
		if u.earlyDecrementBalancePercent != nil {
			// Early decrement percentage applied (B-006, B-007)
			percent := float64(*u.earlyDecrementBalancePercent)
			bill.SubmitSmAmount = totalRate * percent / 100.0
			bill.SubmitSmRespAmount = totalRate - bill.SubmitSmAmount
		} else {
			// Default: 100% early decrement
			bill.SubmitSmAmount = totalRate
			bill.SubmitSmRespAmount = 0
		}
	}

	// B-003: Unlimited balance (u.balance == nil) results in 0.0 amounts, handled by check above.

	// Jasmin Rule 2: Decrement submit_sm_count if not unlimited
	if u.submitSmCountQuota != nil {
		bill.DecrementSubmitSmCount = segments
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
