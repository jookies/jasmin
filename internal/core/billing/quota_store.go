package billing

import (
	"context"
	"fmt"
)

// QuotaScope names which kind of principal a durable quota row describes.
// Users and groups share one table because they share one shape (a balance
// plus a submit_sm_count ceiling) and are always written together: a charge
// decrements both in the same locked section, so restoring one without the
// other would resurrect a ceiling the customer has already spent through.
type QuotaScope string

const (
	QuotaScopeUser  QuotaScope = "user"
	QuotaScopeGroup QuotaScope = "group"
)

// Quota is the mutable half of a billing principal: exactly what
// AuthorizeAndApplySubmit and ApplyLateCharge decrement. Everything else a
// User carries (early decrement percent, group membership, credentials) is
// provisioning state owned by the config and admin planes and is deliberately
// absent here — a durable row must never be able to override a deliberate
// provisioning change. A nil member is legacy "unlimited" (Python None), which
// is not the same as zero.
type Quota struct {
	Balance       *float64
	SubmitSmCount *int
}

// Clone detaches a Quota from the pointers its caller supplied, so a stored
// baseline cannot be mutated through the config struct it came from.
func (quota Quota) Clone() Quota {
	return Quota{Balance: cloneFloat64(quota.Balance), SubmitSmCount: cloneInt(quota.SubmitSmCount)}
}

// QuotaRecord is one durable row: the live (spent-down) quota together with
// the provisioned quota that was in effect when the row was written. The
// baseline is what makes an operator top-up distinguishable from a plain
// restart; see Restore for the precedence rule it buys.
type QuotaRecord struct {
	Scope       QuotaScope
	Key         string
	Live        Quota
	Provisioned Quota
}

// QuotaStore is the durable boundary for billing quotas. Implementations must
// write a batch atomically: a group ceiling and the user balance it backs are
// decremented together, so a crash between the two writes would leave a
// customer able to spend the difference twice.
type QuotaStore interface {
	SaveQuotas(ctx context.Context, records []QuotaRecord) error
	LoadQuotas(ctx context.Context) ([]QuotaRecord, error)
}

// QuotaKey identifies one live billing principal for durable-row pruning.
// Pruning is deliberately a separate capability from QuotaStore: production
// stores implement it, while compatibility/unit stores that only exercise
// save/restore do not have to pretend account lifecycle exists.
type QuotaKey struct {
	Scope QuotaScope
	Key   string
}

// QuotaPruner removes durable rows for principals that no longer exist after
// every config and admin account has been replayed at boot. Without this pass,
// deleting and later recreating the same username with the same provisioned
// grant resurrects the deleted account's spent-down balance.
type QuotaPruner interface {
	PruneQuotas(ctx context.Context, active []QuotaKey) (int64, error)
}

// Restore resolves the value a principal must boot with, given what the
// operator has provisioned it with now.
//
// The provisioned value is doing two jobs at once: it is the initial grant for
// a new account, and it is also how an operator tops an existing account up.
// So neither side can simply win. Each durable row therefore carries the
// provisioned value that was in effect when it was written, and precedence is
// decided per field:
//
//   - baseline still matches what is provisioned -> the durable value wins.
//     Nothing about the spec changed, so this is a plain restart and the
//     customer must keep what they have already spent.
//   - baseline differs from what is provisioned -> the provisioned value wins.
//     The operator edited the spec since the last flush; that edit is the
//     re-grant gesture and must take effect.
//
// Per field rather than per row, so topping up a balance does not silently
// also reset submit_sm_count (or the reverse).
//
// Foot-gun this leaves: re-granting the *same* number the account was last
// provisioned with is a no-op across a restart, because by construction
// nothing about the spec changed. Operators topping up must either change the
// number or do it online through the admin plane, which mutates the live user
// directly and never consults this rule.
func (record QuotaRecord) Restore(provisioned Quota) Quota {
	restored := provisioned.Clone()
	if sameFloat(record.Provisioned.Balance, provisioned.Balance) {
		restored.Balance = cloneFloat64(record.Live.Balance)
	}
	if sameInt(record.Provisioned.SubmitSmCount, provisioned.SubmitSmCount) {
		restored.SubmitSmCount = cloneInt(record.Live.SubmitSmCount)
	}
	return restored
}

// QuotaIndex is the boot-time lookup built from a store load. It is consumed
// destructively (see Take) and is not safe for concurrent use; the caller that
// owns provisioning guards it with its own lock.
type QuotaIndex map[QuotaScope]map[string]QuotaRecord

// NewQuotaIndex indexes loaded rows by scope and key. A duplicate (scope,key)
// keeps the last row, which cannot happen against a store with the primary key
// this package expects.
func NewQuotaIndex(records []QuotaRecord) QuotaIndex {
	index := make(QuotaIndex, 2)
	for _, record := range records {
		scoped, known := index[record.Scope]
		if !known {
			scoped = make(map[string]QuotaRecord)
			index[record.Scope] = scoped
		}
		scoped[record.Key] = record
	}
	return index
}

// Take resolves a principal's boot quota and consumes the row. Consuming is
// what confines restoration to boot: the first provisioning of a key (config
// load, or the admin plane replaying its stored users) restores, and any later
// re-provisioning of the same key — an operator editing the account while the
// gateway runs — takes the value the operator just supplied.
func (index QuotaIndex) Take(scope QuotaScope, key string, provisioned Quota) Quota {
	scoped, known := index[scope]
	if !known {
		return provisioned.Clone()
	}
	record, known := scoped[key]
	if !known {
		return provisioned.Clone()
	}
	delete(scoped, key)
	return record.Restore(provisioned)
}

// LoadQuotaIndex reads the whole durable quota set and indexes it for boot.
func LoadQuotaIndex(ctx context.Context, store QuotaStore) (QuotaIndex, error) {
	if store == nil {
		return NewQuotaIndex(nil), nil
	}
	records, err := store.LoadQuotas(ctx)
	if err != nil {
		return nil, fmt.Errorf("load durable billing quotas: %w", err)
	}
	return NewQuotaIndex(records), nil
}

// sameFloat and sameInt compare legacy optional values, where nil ("unlimited")
// is a distinct value rather than a missing one. Provisioned balances are
// validated non-NaN by ValidateParams before they ever reach here, so plain
// equality is sound.
func sameFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
