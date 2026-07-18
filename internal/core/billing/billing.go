package billing

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

var (
	ErrInvalidRate         = errors.New("invalid route rate")
	ErrInvalidPercent      = errors.New("invalid early decrement percent")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrInsufficientCount   = errors.New("insufficient submit_sm_count")
	ErrBillingStateChanged = errors.New("billing state changed after calculation")
)

type User struct {
	mu                           sync.Mutex
	uid                          int64
	balance                      *float64
	earlyDecrementBalancePercent *int
	submitSmCountQuota           *int
	group                        *Group
}

func (u *User) UID() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.uid
}

type UserState struct {
	UID                          int64
	Balance                      *float64
	EarlyDecrementBalancePercent *int
	SubmitSmCountQuota           *int
	GID                          *int64
}

func (u *User) GetState() UserState {
	u.mu.Lock()
	defer u.mu.Unlock()

	var gid *int64
	if u.group != nil {
		u.group.mu.Lock()
		id := u.group.gid
		u.group.mu.Unlock()
		gid = &id
	}

	return UserState{
		UID:                          u.uid,
		Balance:                      cloneFloat64(u.balance),
		EarlyDecrementBalancePercent: cloneInt(u.earlyDecrementBalancePercent),
		SubmitSmCountQuota:           cloneInt(u.submitSmCountQuota),
		GID:                          gid,
	}
}

func (u *User) LoadState(s UserState) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.uid = s.UID
	u.balance = cloneFloat64(s.Balance)
	u.earlyDecrementBalancePercent = cloneInt(s.EarlyDecrementBalancePercent)
	u.submitSmCountQuota = cloneInt(s.SubmitSmCountQuota)
	// Group is handled separately by the caller via SetGroup
}

type GroupState struct {
	GID                int64
	Balance            *float64
	SubmitSmCountQuota *int
}

func (g *Group) GetState() GroupState {
	g.mu.Lock()
	defer g.mu.Unlock()

	return GroupState{
		GID:                g.gid,
		Balance:            cloneFloat64(g.balance),
		SubmitSmCountQuota: cloneInt(g.submitSmCountQuota),
	}
}

func (g *Group) LoadState(s GroupState) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.gid = s.GID
	g.balance = cloneFloat64(s.Balance)
	g.submitSmCountQuota = cloneInt(s.SubmitSmCountQuota)
}

func (g *Group) GID() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gid
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

// AuthorizeAndApplySubmit performs the legacy submit-time quota transition as
// one linearizable operation. Authorization requires enough balance for the
// early and late amounts, while only the early amount is deducted here.
func (u *User) AuthorizeAndApplySubmit(bill Bill) error {
	if err := validateBill(bill); err != nil {
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	group := u.group
	if group != nil {
		group.mu.Lock()
		defer group.mu.Unlock()
	}
	return u.authorizeAndApplySubmitLocked(group, bill)
}

// AuthorizeAndApplyCalculatedSubmit verifies that a bill calculated before an
// external envelope-build boundary still matches the user's current billing
// configuration, then authorizes and applies it under the same lock set.
func (u *User) AuthorizeAndApplyCalculatedSubmit(routeRate float64, segments int, expected Bill) error {
	if err := ValidateParams(routeRate, nil); err != nil {
		return err
	}
	if segments <= 0 {
		return ErrInvalidRate
	}
	if err := validateBill(expected); err != nil {
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	group := u.group
	if group != nil {
		group.mu.Lock()
		defer group.mu.Unlock()
	}
	if current := calculateBillLocked(routeRate, segments, u); current != expected {
		return ErrBillingStateChanged
	}
	return u.authorizeAndApplySubmitLocked(group, expected)
}

func (u *User) authorizeAndApplySubmitLocked(group *Group, bill Bill) error {
	requiredBalance := bill.SubmitSmAmount + bill.SubmitSmRespAmount
	if u.balance != nil && *u.balance < requiredBalance {
		return ErrInsufficientBalance
	}
	if u.submitSmCountQuota != nil && *u.submitSmCountQuota < bill.DecrementSubmitSmCount {
		return ErrInsufficientCount
	}
	if group != nil {
		if group.balance != nil && *group.balance < requiredBalance {
			return fmt.Errorf("group: %w", ErrInsufficientBalance)
		}
		if group.submitSmCountQuota != nil && *group.submitSmCountQuota < bill.DecrementSubmitSmCount {
			return fmt.Errorf("group: %w", ErrInsufficientCount)
		}
	}

	if u.balance != nil {
		*u.balance -= bill.SubmitSmAmount
	}
	if u.submitSmCountQuota != nil {
		*u.submitSmCountQuota -= bill.DecrementSubmitSmCount
	}
	if group != nil {
		if group.balance != nil {
			*group.balance -= bill.SubmitSmAmount
		}
		if group.submitSmCountQuota != nil {
			*group.submitSmCountQuota -= bill.DecrementSubmitSmCount
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
	return calculateBillLocked(routeRate, segments, u)
}

func calculateBillLocked(routeRate float64, segments int, u *User) Bill {
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

func validateBill(bill Bill) error {
	if math.IsNaN(bill.SubmitSmAmount) || math.IsInf(bill.SubmitSmAmount, 0) || bill.SubmitSmAmount < 0 {
		return ErrInvalidRate
	}
	if math.IsNaN(bill.SubmitSmRespAmount) || math.IsInf(bill.SubmitSmRespAmount, 0) || bill.SubmitSmRespAmount < 0 {
		return ErrInvalidRate
	}
	if bill.DecrementSubmitSmCount < 0 {
		return ErrInsufficientCount
	}
	return nil
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
