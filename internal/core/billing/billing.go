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
	ErrUnlimitedBalance    = errors.New("unlimited balance has no late charge transition")
)

type User struct {
	mu                           sync.Mutex
	uid                          int64
	balance                      *float64
	earlyDecrementBalancePercent *int
	submitSmCountQuota           *int
	group                        *Group
	quotaVersion                 uint64
	persistedQuotaVersion        uint64
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
	u.quotaVersion = 0
	u.persistedQuotaVersion = 0
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
		total := bill.requiredBalance()
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

// GroupReprovision is an in-progress update of a live group. BeginReprovision
// keeps the group locked until Commit or Rollback, allowing the admin store
// write and the live mutation to succeed or fail as one logical operation.
type GroupReprovision struct {
	group              *Group
	balance            *float64
	submitSmCountQuota *int
	active             bool
}

// BeginReprovision updates a live group without replacing its pointer and
// returns a transaction that still owns the group lock. Every member keeps
// charging this same object, and no charge can observe an edit until its
// durable admin-store write has committed.
//
// Unchanged provisioning fields preserve their spent-down values; a changed
// field is the operator's explicit reset/top-up.
func (g *Group) BeginReprovision(previous, next Quota) (*GroupReprovision, error) {
	if next.Balance != nil {
		if err := ValidateParams(*next.Balance, nil); err != nil {
			return nil, err
		}
	}
	if next.SubmitSmCount != nil && *next.SubmitSmCount < 0 {
		return nil, ErrInsufficientCount
	}
	g.mu.Lock()
	transaction := &GroupReprovision{
		group:              g,
		balance:            cloneFloat64(g.balance),
		submitSmCountQuota: cloneInt(g.submitSmCountQuota),
		active:             true,
	}
	if !sameFloat(previous.Balance, next.Balance) {
		g.balance = cloneFloat64(next.Balance)
	}
	if !sameInt(previous.SubmitSmCount, next.SubmitSmCount) {
		g.submitSmCountQuota = cloneInt(next.SubmitSmCount)
	}
	return transaction, nil
}

// Commit makes the provisional group update visible to waiting charges.
func (transaction *GroupReprovision) Commit() {
	if transaction == nil || !transaction.active {
		return
	}
	transaction.active = false
	transaction.group.mu.Unlock()
}

// Rollback restores the exact pre-edit group quota and releases the lock.
func (transaction *GroupReprovision) Rollback() {
	if transaction == nil || !transaction.active {
		return
	}
	transaction.group.balance = cloneFloat64(transaction.balance)
	transaction.group.submitSmCountQuota = cloneInt(transaction.submitSmCountQuota)
	transaction.active = false
	transaction.group.mu.Unlock()
}

// Reprovision performs an immediately committed update. Admin persistence uses
// BeginReprovision directly so it can roll back an unsuccessful store write.
func (g *Group) Reprovision(previous, next Quota) error {
	transaction, err := g.BeginReprovision(previous, next)
	if err != nil {
		return err
	}
	transaction.Commit()
	return nil
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
	u.bumpQuotaVersionLocked()
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
		total := bill.requiredBalance()
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
	u.bumpQuotaVersionLocked()
	return nil
}

func (u *User) SetSubmitSmCountQuota(count int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.submitSmCountQuota = &count
	u.bumpQuotaVersionLocked()
}

// UserReprovision is an in-progress update of a live user. It retains the user
// lock until Commit or Rollback, so a failed admin-store write can restore the
// exact spent state without racing a submit or late charge.
type UserReprovision struct {
	user                         *User
	balance                      *float64
	earlyDecrementBalancePercent *int
	submitSmCountQuota           *int
	group                        *Group
	quotaVersion                 uint64
	persistedQuotaVersion        uint64
	active                       bool
}

// BeginReprovision applies an online admin edit without replacing the live
// User pointer and returns a transaction that still owns the user lock.
// Charges and this update therefore serialize on the same object: an in-flight
// submit cannot debit an orphaned object or observe an edit whose SQLite write
// later fails.
//
// Quota fields whose provisioned baseline is unchanged preserve their current
// spent-down value. A changed baseline is the operator's explicit reset/top-up
// gesture and installs the new value. Non-quota billing settings always take
// the newly provisioned value.
func (u *User) BeginReprovision(previous, next Quota, earlyPercent *int, group *Group) (*UserReprovision, error) {
	if next.Balance != nil {
		if err := ValidateParams(*next.Balance, nil); err != nil {
			return nil, err
		}
	}
	if next.SubmitSmCount != nil && *next.SubmitSmCount < 0 {
		return nil, ErrInsufficientCount
	}
	if earlyPercent != nil {
		if err := ValidateParams(0, earlyPercent); err != nil {
			return nil, err
		}
	}

	u.mu.Lock()
	transaction := &UserReprovision{
		user:                         u,
		balance:                      cloneFloat64(u.balance),
		earlyDecrementBalancePercent: cloneInt(u.earlyDecrementBalancePercent),
		submitSmCountQuota:           cloneInt(u.submitSmCountQuota),
		group:                        u.group,
		quotaVersion:                 u.quotaVersion,
		persistedQuotaVersion:        u.persistedQuotaVersion,
		active:                       true,
	}
	if !sameFloat(previous.Balance, next.Balance) {
		u.balance = cloneFloat64(next.Balance)
	}
	if !sameInt(previous.SubmitSmCount, next.SubmitSmCount) {
		u.submitSmCountQuota = cloneInt(next.SubmitSmCount)
	}
	u.earlyDecrementBalancePercent = cloneInt(earlyPercent)
	u.group = group
	u.bumpQuotaVersionLocked()
	return transaction, nil
}

// Commit makes the provisional user update visible to waiting billing work.
func (transaction *UserReprovision) Commit() {
	if transaction == nil || !transaction.active {
		return
	}
	transaction.active = false
	transaction.user.mu.Unlock()
}

// Rollback restores the exact pre-edit user state and releases the lock.
func (transaction *UserReprovision) Rollback() {
	if transaction == nil || !transaction.active {
		return
	}
	transaction.user.balance = cloneFloat64(transaction.balance)
	transaction.user.earlyDecrementBalancePercent = cloneInt(transaction.earlyDecrementBalancePercent)
	transaction.user.submitSmCountQuota = cloneInt(transaction.submitSmCountQuota)
	transaction.user.group = transaction.group
	transaction.user.quotaVersion = transaction.quotaVersion
	transaction.user.persistedQuotaVersion = transaction.persistedQuotaVersion
	transaction.active = false
	transaction.user.mu.Unlock()
}

// Reprovision performs an immediately committed update. Admin persistence uses
// BeginReprovision directly so it can roll back an unsuccessful store write.
func (u *User) Reprovision(previous, next Quota, earlyPercent *int, group *Group) error {
	transaction, err := u.BeginReprovision(previous, next, earlyPercent, group)
	if err != nil {
		return err
	}
	transaction.Commit()
	return nil
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
	u.bumpQuotaVersionLocked()
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

// ApplyLateCharge atomically reproduces the finite-balance mutation performed
// by RouterPB.bill_request_submit_sm_resp_callback. An unlimited balance is
// reported explicitly so the orchestration layer can preserve the legacy
// callback's lack of a terminal ACK/reject action for that branch.
func (u *User) ApplyLateCharge(amount float64) error {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return ErrInvalidRate
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.balance == nil {
		return ErrUnlimitedBalance
	}
	if *u.balance < amount {
		return ErrInsufficientBalance
	}
	*u.balance -= amount
	u.bumpQuotaVersionLocked()
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
	requiredBalance := bill.requiredBalance()
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
	u.bumpQuotaVersionLocked()
	return nil
}

func (u *User) bumpQuotaVersionLocked() {
	u.quotaVersion++
}

type Bill struct {
	SubmitSmAmount         float64
	SubmitSmRespAmount     float64
	DecrementSubmitSmCount int
	// AuthorizationAmount preserves Bill.getTotalAmounts()*segments in the
	// legacy unit-first operation order. Zero falls back to the sum for bills
	// manually constructed by existing callers.
	AuthorizationAmount float64
}

func (bill Bill) requiredBalance() float64 {
	if bill.AuthorizationAmount != 0 || (bill.SubmitSmAmount == 0 && bill.SubmitSmRespAmount == 0) {
		return bill.AuthorizationAmount
	}
	return bill.SubmitSmAmount + bill.SubmitSmRespAmount
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
		if u.earlyDecrementBalancePercent != nil {
			// Legacy B-008 order: split one unit rate first, then multiply
			// each projected amount by the segment count.
			percent := float64(*u.earlyDecrementBalancePercent)
			unitEarly := routeRate * percent / 100.0
			unitLate := routeRate - unitEarly
			bill.AuthorizationAmount = (unitEarly + unitLate) * float64(segments)
			bill.SubmitSmAmount = unitEarly * float64(segments)
			bill.SubmitSmRespAmount = unitLate * float64(segments)
		} else {
			// Default: 100% early decrement
			bill.AuthorizationAmount = routeRate * float64(segments)
			bill.SubmitSmAmount = bill.AuthorizationAmount
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
	if math.IsNaN(bill.AuthorizationAmount) || math.IsInf(bill.AuthorizationAmount, 0) || bill.AuthorizationAmount < 0 {
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
