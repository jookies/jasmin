// Package dlrgate decides the terminal delivery receipt a submitting user is
// told, from whether the destination MSISDN is currently in a short-lived
// registry.
//
// It is the per-user form of the activation window that
// internal/core/termination already reads for its own connectors: same Redis
// keyspace (dlr:block:<digits>), same normalization, same presence semantics
// (present means delivered, absent means rejected). What this package adds is
// the ability to switch the behaviour on per submitting user, and to write and
// enumerate the registry, which nothing in this repo could do before — the
// keys were written by a separate system entirely.
//
// The gate changes only what the partner is TOLD. Routing is untouched: the
// message still goes upstream exactly as it would without the gate, and the CDR
// still records the real upstream status. See Verdict.
package dlrgate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pumpitspace/synevyr/internal/core/dlr"
	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// ErrInvalidPolicy reports a per-user policy that cannot be applied.
var ErrInvalidPolicy = errors.New("dlrgate: invalid policy")

// Default receipt fields. They are the activation window's own pairing
// (termination.StatDelivered/ReceiptErrNone and StatRejected/ReceiptErrRejected)
// rather than values spelled again here, so a change to the window's vocabulary
// cannot leave the two gates disagreeing about what a hit looks like.
const (
	DefaultHitStatus  = termination.StatDelivered
	DefaultHitError   = termination.ReceiptErrNone
	DefaultMissStatus = termination.StatRejected
	DefaultMissError  = termination.ReceiptErrRejected
)

// Policy is one user's gate settings.
//
// A zero Policy is disabled, which is what a user with no configured block
// gets. Every field is optional; WithDefaults fills the receipt values.
type Policy struct {
	// Enabled switches the gate on for this user. False means the user's
	// receipts are whatever the upstream said, i.e. the pre-existing behaviour.
	Enabled bool
	// HitStatus/HitError are the receipt fields used when the destination is in
	// the registry. Empty means DefaultHitStatus/DefaultHitError.
	HitStatus string
	HitError  string
	// MissStatus/MissError are the receipt fields used when it is not.
	MissStatus string
	MissError  string
}

// WithDefaults fills the unset receipt fields.
func (p Policy) WithDefaults() Policy {
	if p.HitStatus == "" {
		p.HitStatus = DefaultHitStatus
	}
	if p.HitError == "" {
		p.HitError = DefaultHitError
	}
	if p.MissStatus == "" {
		p.MissStatus = DefaultMissStatus
	}
	if p.MissError == "" {
		p.MissError = DefaultMissError
	}
	return p
}

// Validate rejects a policy whose receipt statuses the DLR plane would refuse to
// publish.
//
// This check belongs at configuration time, not at receipt time: an unpublishable
// status only fails inside EncodeThrowerForward, by which point the message is
// long gone and the partner simply never receives a receipt. Failing the admin
// write instead makes the mistake visible to whoever made it.
func (p Policy) Validate() error {
	policy := p.WithDefaults()
	if !policy.Enabled {
		return nil
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"hit_status", policy.HitStatus},
		{"miss_status", policy.MissStatus},
	} {
		if !dlr.ValidMessageStatus(field.value) {
			return fmt.Errorf("%w: %s %q is not a receipt status the DLR plane can publish", ErrInvalidPolicy, field.name, field.value)
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"hit_error", policy.HitError},
		{"miss_error", policy.MissError},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%w: %s is empty", ErrInvalidPolicy, field.name)
		}
	}
	return nil
}

// Verdict is one decision.
//
// Status and Error are what the partner will be told. They are deliberately not
// named "the delivery result": on a hit the gate reports success without having
// observed one, so anything that records what actually happened — the CDR, the
// audit log — must read the upstream status instead. correlation.go applies the
// override only after the CDR hook for that reason.
type Verdict struct {
	Status string
	Error  string
	// Hit reports whether the destination was found in the registry.
	Hit bool
	// Reason is the decision trail label, reused as a metric dimension.
	Reason string
}

// Decision reasons.
const (
	ReasonInRegistry         = "in registry"
	ReasonNotInRegistry      = "not in registry"
	ReasonInvalidDestination = "invalid destination"
	ReasonRegistryUnreadable = "registry unreadable, failed open"
	// ReasonWindowOwnedByAnotherUser is a miss on a number that does have an
	// open window — one another gated user opened. It is called out separately
	// because it is the isolation boundary doing its job, not an absent window.
	ReasonWindowOwnedByAnotherUser = "window owned by another user"
)

func (p Policy) hit(reason string) Verdict {
	policy := p.WithDefaults()
	return Verdict{Status: policy.HitStatus, Error: policy.HitError, Hit: true, Reason: reason}
}

func (p Policy) miss(reason string) Verdict {
	policy := p.WithDefaults()
	return Verdict{Status: policy.MissStatus, Error: policy.MissError, Hit: false, Reason: reason}
}
