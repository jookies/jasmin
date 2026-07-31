package termination

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SourceKind names a verdict source implementation. The value is what an
// operator stores on a connector, so it is the string the admin plane, jCli and
// the console all round-trip.
type SourceKind string

const (
	// SourceStatic accepts every message. It exists for tests and for a
	// connector that terminates traffic without any gate at all.
	SourceStatic SourceKind = "static"
	// SourceRedisWindow reads the activation window from Redis. It is the only
	// mode with production parity: the legacy fake SMSC never consults the
	// downstream application for a status, an open window is DELIVRD, full stop.
	SourceRedisWindow SourceKind = "redis-window"
	// SourceHTTPGate and SourceHTTPInline are named here so the factory can
	// tell "configured a mode that is not built yet" apart from "typed the
	// source name wrong". Neither is implemented: both are new capability
	// rather than migration, and Phase A ships only what the Python path did.
	SourceHTTPGate   SourceKind = "http-gate"
	SourceHTTPInline SourceKind = "http-inline"
)

// SMPP receipt status tokens a verdict can carry. The two the activation gate
// produces are named here so receipt synthesis never spells them inline.
const (
	StatDelivered = "DELIVRD"
	StatRejected  = "REJECTD"
)

// Receipt err field values, from the legacy DLR_RECEIPT_FIELDS table
// (fake_smsc.py:49-53). err is 000 only on success; a rejection carries 008.
// They live beside the stat they belong to so the pair can never drift apart —
// the self-contradictory "stat:REJECTD dlvrd:001 err:000" receipt both emulators
// in this repo still emit is exactly what happens when they do.
const (
	ReceiptErrNone     = "000"
	ReceiptErrRejected = "008"
)

// Verdict reasons. They are recorded in the decision trail and used as a metric
// label, so they are constants rather than sentences written at each call site.
const (
	ReasonWindowOpen         = "activation window open"
	ReasonNoWindow           = "no activation window"
	ReasonGateUnreachable    = "activation gate unreachable, failed open"
	ReasonInvalidDestination = "invalid destination"
	ReasonStaticAccept       = "static verdict source accepts every message"
)

var (
	// ErrInvalidVerdictConfig reports a verdict configuration that cannot be
	// built into a source.
	ErrInvalidVerdictConfig = errors.New("termination: invalid verdict source configuration")
	// ErrSourceNotImplemented reports a known source kind that this build does
	// not provide.
	ErrSourceNotImplemented = errors.New("termination: verdict source not implemented")
)

// Defaults for the activation gate.
const (
	// DefaultActivationKeyPrefix is the legacy key namespace: the gateway sets
	// dlr:block:{digits} when it opens an activation window (fake_smsc.py:227).
	DefaultActivationKeyPrefix = "dlr:block:"
	// DefaultVerdictCacheTTL is how long one number's answer is reused. The
	// window is a property of the number and lasts ~20 minutes, so seconds of
	// staleness cannot change an answer; what it does change is that a burst of
	// OTP retries to one number becomes one lookup.
	DefaultVerdictCacheTTL = 5 * time.Second
	// DefaultGateLookupTimeout matches the legacy client's socket_timeout of 2
	// seconds (fake_smsc.py:67-68): a dead gate must fail fast into fail-open
	// rather than hold the message between submit and receipt.
	DefaultGateLookupTimeout = 2 * time.Second
)

// VerdictConfig is the per-connector verdict settings.
type VerdictConfig struct {
	Source SourceKind `json:"source"`

	// KeyPrefix overrides the activation key namespace (redis-window only).
	// Empty means DefaultActivationKeyPrefix, which is what the legacy gateway
	// writes; changing it points the gate at a different keyspace, which is how
	// a second partner gets its own.
	KeyPrefix string `json:"key_prefix,omitempty"`
	// CacheTTL is how long a decided verdict is reused for the same normalized
	// destination. Zero means DefaultVerdictCacheTTL; negative is a
	// configuration error rather than a silent "no cache", because the cache is
	// a correctness property (two messages inside one window must agree) and
	// turning it off by typo should not be possible.
	CacheTTL time.Duration `json:"cache_ttl,omitempty"`
	// LookupTimeout bounds one gate call. Zero means DefaultGateLookupTimeout.
	LookupTimeout time.Duration `json:"lookup_timeout,omitempty"`
}

// Dependencies carries the collaborators a verdict source needs. It is a struct
// rather than positional arguments so adding the HTTP client the gate modes will
// want does not change every call site.
type Dependencies struct {
	// Redis is the handle the activation gate reads. A *redis.Client satisfies
	// it directly; see RedisKeyProbe for why the interface is declared here.
	Redis RedisKeyProbe
}

// withDefaults fills the unset fields and rejects the values that cannot be
// defaulted into something sane.
func (c VerdictConfig) withDefaults() (VerdictConfig, error) {
	if c.CacheTTL < 0 {
		return VerdictConfig{}, fmt.Errorf("%w: negative cache ttl", ErrInvalidVerdictConfig)
	}
	if c.LookupTimeout < 0 {
		return VerdictConfig{}, fmt.Errorf("%w: negative lookup timeout", ErrInvalidVerdictConfig)
	}
	if c.KeyPrefix == "" {
		c.KeyPrefix = DefaultActivationKeyPrefix
	}
	if c.CacheTTL == 0 {
		c.CacheTTL = DefaultVerdictCacheTTL
	}
	if c.LookupTimeout == 0 {
		c.LookupTimeout = DefaultGateLookupTimeout
	}
	return c, nil
}

// NewVerdictSource builds the verdict source named by cfg.
//
// An empty Source is an error, not a default. The configuration layer decides
// what a connector without an explicit source gets (the plan says redis-window);
// defaulting here would mean a wiring bug that loses the field silently starts
// accepting every message, and an accept cannot be un-sent.
func NewVerdictSource(cfg VerdictConfig, deps Dependencies) (VerdictSource, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	switch cfg.Source {
	case SourceStatic:
		return staticSource{}, nil
	case SourceRedisWindow:
		return newRedisWindowSource(cfg, deps.Redis)
	case SourceHTTPGate, SourceHTTPInline:
		return nil, fmt.Errorf("%w: %q", ErrSourceNotImplemented, cfg.Source)
	case "":
		return nil, fmt.Errorf("%w: no source", ErrInvalidVerdictConfig)
	default:
		return nil, fmt.Errorf("%w: unknown source %q", ErrInvalidVerdictConfig, cfg.Source)
	}
}

// deliveredVerdict and rejectedVerdict are the only constructors of a verdict in
// this package. Every receipt field value is derived here from one decision, so
// a caller cannot assemble the impossible combinations the legacy emulators
// produce by hand.
func deliveredVerdict(reason string) Verdict {
	return Verdict{Accept: true, Stat: StatDelivered, Err: ReceiptErrNone, Reason: reason}
}

func rejectedVerdict(reason string) Verdict {
	return Verdict{Accept: false, Stat: StatRejected, Err: ReceiptErrRejected, Reason: reason}
}

// staticSource accepts everything.
//
// It deliberately does not normalize the destination: a static source has no key
// to build and no gate to protect, so rejecting a malformed number here would be
// front-door validation hiding in a verdict source. The front door (Step 12) is
// where that belongs.
type staticSource struct{}

func (staticSource) Decide(context.Context, Message) (Verdict, error) {
	return deliveredVerdict(ReasonStaticAccept), nil
}

func (staticSource) Name() string { return string(SourceStatic) }
