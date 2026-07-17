package smppc

import (
	"errors"
	"math"
	"time"
)

var ErrInvalidErrorRetryRule = errors.New("error retry rule is invalid")
var ErrInvalidErrorRetryInput = errors.New("error retry input is invalid")

type ErrorRetryAction string

const (
	ErrorRetryAck     ErrorRetryAction = "ack"
	ErrorRetryRequeue ErrorRetryAction = "requeue"
)

type ErrorRetryRule struct {
	Status       string  `json:"status"`
	Count        int     `json:"count"`
	DelaySeconds float64 `json:"delay_seconds"`
}

type ErrorRetryDecision struct {
	Action         ErrorRetryAction
	RequeueDelay   time.Duration
	KeepRetryEntry bool
}

type errorRetryPolicyRule struct {
	count int
	delay time.Duration
}

// ErrorRetryPolicy is an immutable projection of the legacy listener's
// submit_sm_resp error retry configuration and is safe for concurrent use.
type ErrorRetryPolicy struct {
	rules map[string]errorRetryPolicyRule
}

func DefaultErrorRetryRules() []ErrorRetryRule {
	return []ErrorRetryRule{
		{Status: "ESME_RINVSCHED", Count: 2, DelaySeconds: 300},
		{Status: "ESME_RMSGQFUL", Count: 2, DelaySeconds: 180},
		{Status: "ESME_RSYSERR", Count: 2, DelaySeconds: 30},
		{Status: "ESME_RTHROTTLED", Count: 20, DelaySeconds: 30},
	}
}

func NewErrorRetryPolicy(rules []ErrorRetryRule) (*ErrorRetryPolicy, error) {
	projected := make(map[string]errorRetryPolicyRule, len(rules))
	for _, rule := range rules {
		delayNanos := rule.DelaySeconds * float64(time.Second)
		if rule.Status == "" || rule.Count < 1 || math.IsNaN(rule.DelaySeconds) ||
			math.IsInf(rule.DelaySeconds, 0) || rule.DelaySeconds < 0 ||
			math.IsInf(delayNanos, 0) || delayNanos >= float64(math.MaxInt64) {
			return nil, ErrInvalidErrorRetryRule
		}
		if _, exists := projected[rule.Status]; exists {
			return nil, ErrInvalidErrorRetryRule
		}
		projected[rule.Status] = errorRetryPolicyRule{
			count: rule.Count,
			delay: time.Duration(delayNanos),
		}
	}
	return &ErrorRetryPolicy{rules: projected}, nil
}

func (p *ErrorRetryPolicy) Decide(status string, currentAttempt int) (ErrorRetryDecision, error) {
	if p == nil || status == "" || currentAttempt < 1 {
		return ErrorRetryDecision{}, ErrInvalidErrorRetryInput
	}
	rule, configured := p.rules[status]
	if !configured {
		return ErrorRetryDecision{Action: ErrorRetryAck, KeepRetryEntry: true}, nil
	}
	if currentAttempt < rule.count {
		return ErrorRetryDecision{
			Action: ErrorRetryRequeue, RequeueDelay: rule.delay, KeepRetryEntry: true,
		}, nil
	}
	return ErrorRetryDecision{Action: ErrorRetryAck}, nil
}
