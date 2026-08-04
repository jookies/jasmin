package dlrgate

import (
	"context"
	"log/slog"
	"time"
)

// DefaultLookupTimeout bounds one registry probe on the submit path.
//
// It matches the termination gate's own budget (termination.DefaultGateLookupTimeout):
// a dead registry must fail fast into fail-open rather than hold a submit.
const DefaultLookupTimeout = 2 * time.Second

// PolicyResolver projects a username into its gate policy. The second return is
// false for a user that has none, which is every user until an operator
// configures one.
type PolicyResolver interface {
	ResolveDLRGatePolicy(username string) (Policy, bool)
}

// RegistryProbe is the one registry operation the gate performs. *Registry
// satisfies it.
type RegistryProbe interface {
	Window(ctx context.Context, digits string) (owner string, present bool, err error)
}

// Gate decides the receipt override for one submit.
type Gate struct {
	registry RegistryProbe
	policies PolicyResolver
	timeout  time.Duration
	logger   *slog.Logger
}

// NewGate builds a gate. A nil registry or resolver yields a nil *Gate, which
// Decide handles on a nil receiver — but callers storing it in an interface must
// convert explicitly rather than assigning the typed nil, or the interface will
// compare non-nil.
func NewGate(registry RegistryProbe, policies PolicyResolver, logger *slog.Logger) *Gate {
	if registry == nil || policies == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Gate{registry: registry, policies: policies, timeout: DefaultLookupTimeout, logger: logger}
}

// Decide returns the receipt override for a submit by username to destination.
//
// The second return is false when no override applies, which is the common case:
// the gate is off for this user. Callers must leave the receipt alone then.
//
// The decision is made here, at submit, and stamped onto the DLR record rather
// than being recomputed when the receipt arrives. That is deliberate: the window
// is minutes long and the receipt can arrive after it closes, so deciding late
// would tell two partners different things about the same window.
func (g *Gate) Decide(ctx context.Context, username, destination string) (Verdict, bool) {
	if g == nil {
		return Verdict{}, false
	}
	policy, ok := g.policies.ResolveDLRGatePolicy(username)
	if !ok || !policy.Enabled {
		return Verdict{}, false
	}
	digits, err := Normalize(destination)
	if err != nil {
		// Never probe with garbage. A destination that is not a number cannot
		// have a window, and building a key from it would look up something that
		// can never exist — the same outcome, but with no record of why.
		verdict := policy.miss(ReasonInvalidDestination)
		g.log(username, destination, verdict)
		return verdict, true
	}

	lookupCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	owner, present, probeErr := g.registry.Window(lookupCtx, digits)
	var verdict Verdict
	switch {
	case probeErr != nil:
		// Fail open, matching the termination gate: an infrastructure blip must
		// not turn into a wave of rejections for traffic that was fine. The
		// operator sees it in the log, and the partner sees a success they would
		// have seen anyway had Redis been up for a registered number.
		verdict = policy.hit(ReasonRegistryUnreadable)
	case !present:
		verdict = policy.miss(ReasonNotInRegistry)
	case owner == "" || owner == username:
		// An unowned window was opened by the operator (or by another system
		// writing the keyspace) and applies to everyone; an owned one applies
		// only to the user whose token opened it.
		verdict = policy.hit(ReasonInRegistry)
	default:
		// The window exists but belongs to a different user. Treating it as a hit
		// would let any gated partner confirm another's traffic simply by
		// opening a window on the same number.
		verdict = policy.miss(ReasonWindowOwnedByAnotherUser)
	}
	g.log(username, digits, verdict)
	return verdict, true
}

func (g *Gate) log(username, destination string, verdict Verdict) {
	g.logger.Info("DLR registry gate decided a receipt",
		slog.String("user", username),
		slog.String("destination", destination),
		slog.Bool("hit", verdict.Hit),
		slog.String("reason", verdict.Reason),
		slog.String("reported_status", verdict.Status),
		slog.String("reported_error", verdict.Error),
	)
}
