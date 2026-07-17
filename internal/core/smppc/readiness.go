package smppc

import (
	"errors"
	"math"
	"time"
)

const (
	DefaultSubmitMaxAgeSeconds     int64 = 1200
	DefaultSubmitRetryDelaySeconds int64 = 30
)

var ErrInvalidReadinessConfig = errors.New("readiness durations must be non-negative and representable")
var ErrInvalidReadinessInput = errors.New("readiness input is invalid")

type ReadinessAction string

const (
	ReadinessProceed ReadinessAction = "proceed"
	ReadinessRequeue ReadinessAction = "requeue"
	ReadinessDiscard ReadinessAction = "discard"
)

type ReadinessConfig struct {
	MaxAgeSeconds     int64 `json:"max_age_seconds"`
	RetryDelaySeconds int64 `json:"retry_delay_seconds"`
}

type ReadinessInput struct {
	Now        time.Time
	CreatedAt  time.Time
	Expiration *time.Time
	Connected  bool
	Bound      bool
}

type ReadinessDecision struct {
	Action       ReadinessAction
	RequeueDelay time.Duration
}

// ReadinessPolicy preserves the legacy listener's pre-submit readiness decision.
// It is immutable after construction and safe for concurrent use.
type ReadinessPolicy struct {
	maxAgeSeconds int64
	retryDelay    time.Duration
}

func DefaultReadinessConfig() ReadinessConfig {
	return ReadinessConfig{
		MaxAgeSeconds:     DefaultSubmitMaxAgeSeconds,
		RetryDelaySeconds: DefaultSubmitRetryDelaySeconds,
	}
}

func NewReadinessPolicy(cfg ReadinessConfig) (*ReadinessPolicy, error) {
	if cfg.MaxAgeSeconds < 0 || cfg.RetryDelaySeconds < 0 ||
		cfg.RetryDelaySeconds > math.MaxInt64/int64(time.Second) {
		return nil, ErrInvalidReadinessConfig
	}
	return &ReadinessPolicy{
		maxAgeSeconds: cfg.MaxAgeSeconds,
		retryDelay:    time.Duration(cfg.RetryDelaySeconds) * time.Second,
	}, nil
}

func (p *ReadinessPolicy) Decide(input ReadinessInput) (ReadinessDecision, error) {
	if p == nil || input.Now.IsZero() || input.CreatedAt.IsZero() || (!input.Connected && input.Bound) {
		return ReadinessDecision{}, ErrInvalidReadinessInput
	}
	if input.Expiration != nil && input.Expiration.Before(input.Now) {
		return ReadinessDecision{Action: ReadinessDiscard}, nil
	}
	if input.Connected && input.Bound {
		return ReadinessDecision{Action: ReadinessProceed}, nil
	}
	ageSeconds, err := legacyTimedeltaSeconds(input.Now, input.CreatedAt)
	if err != nil {
		return ReadinessDecision{}, err
	}
	if ageSeconds > p.maxAgeSeconds {
		return ReadinessDecision{Action: ReadinessDiscard}, nil
	}
	return ReadinessDecision{Action: ReadinessRequeue, RequeueDelay: p.retryDelay}, nil
}

// legacyTimedeltaSeconds returns Python timedelta.seconds: the whole-second
// component modulo one day, not total_seconds(). This intentionally preserves
// the frozen listener's multi-day and negative-age behavior.
func legacyTimedeltaSeconds(now, createdAt time.Time) (int64, error) {
	nowMicros, err := legacyWallUnixMicros(now)
	if err != nil {
		return 0, err
	}
	createdMicros, err := legacyWallUnixMicros(createdAt)
	if err != nil {
		return 0, err
	}
	deltaMicros := nowMicros - createdMicros
	seconds := deltaMicros / 1_000_000
	if deltaMicros < 0 && deltaMicros%1_000_000 != 0 {
		seconds--
	}
	seconds %= 86_400
	if seconds < 0 {
		seconds += 86_400
	}
	return seconds, nil
}
