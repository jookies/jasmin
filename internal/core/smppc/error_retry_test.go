package smppc

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestDefaultErrorRetryRules(t *testing.T) {
	got := DefaultErrorRetryRules()
	want := []ErrorRetryRule{
		{Status: "ESME_RINVSCHED", Count: 2, DelaySeconds: 300},
		{Status: "ESME_RMSGQFUL", Count: 2, DelaySeconds: 180},
		{Status: "ESME_RSYSERR", Count: 2, DelaySeconds: 30},
		{Status: "ESME_RTHROTTLED", Count: 20, DelaySeconds: 30},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rule %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestErrorRetryPolicyValidation(t *testing.T) {
	tests := []struct {
		name  string
		rules []ErrorRetryRule
	}{
		{name: "empty status", rules: []ErrorRetryRule{{Count: 1}}},
		{name: "zero count", rules: []ErrorRetryRule{{Status: "X", Count: 0}}},
		{name: "negative delay", rules: []ErrorRetryRule{{Status: "X", Count: 1, DelaySeconds: -1}}},
		{name: "nan delay", rules: []ErrorRetryRule{{Status: "X", Count: 1, DelaySeconds: math.NaN()}}},
		{name: "infinite delay", rules: []ErrorRetryRule{{Status: "X", Count: 1, DelaySeconds: math.Inf(1)}}},
		{name: "overflow delay", rules: []ErrorRetryRule{{Status: "X", Count: 1, DelaySeconds: float64(math.MaxInt64)/float64(time.Second) + 1}}},
		{name: "duplicate status", rules: []ErrorRetryRule{{Status: "X", Count: 1}, {Status: "X", Count: 2}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewErrorRetryPolicy(tc.rules); !errors.Is(err, ErrInvalidErrorRetryRule) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidErrorRetryRule)
			}
		})
	}
}

func TestErrorRetryPolicyInputValidation(t *testing.T) {
	policy, err := NewErrorRetryPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		status  string
		attempt int
	}{{"", 1}, {"X", 0}, {"X", -1}} {
		if _, err := policy.Decide(input.status, input.attempt); !errors.Is(err, ErrInvalidErrorRetryInput) {
			t.Fatalf("Decide(%q, %d) error = %v", input.status, input.attempt, err)
		}
	}
	var nilPolicy *ErrorRetryPolicy
	if _, err := nilPolicy.Decide("X", 1); !errors.Is(err, ErrInvalidErrorRetryInput) {
		t.Fatalf("nil policy error = %v", err)
	}
}

func TestErrorRetryPolicyCopiesRules(t *testing.T) {
	rules := []ErrorRetryRule{{Status: "X", Count: 2, DelaySeconds: 3}}
	policy, err := NewErrorRetryPolicy(rules)
	if err != nil {
		t.Fatal(err)
	}
	rules[0] = ErrorRetryRule{Status: "Y", Count: 99, DelaySeconds: 99}
	got, err := policy.Decide("X", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != ErrorRetryRequeue || got.RequeueDelay != 3*time.Second {
		t.Fatalf("decision after caller mutation = %+v", got)
	}
}

func TestErrorRetryPolicyConcurrentDecide(t *testing.T) {
	policy, err := NewErrorRetryPolicy(DefaultErrorRetryRules())
	if err != nil {
		t.Fatal(err)
	}
	const workers = 64
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := policy.Decide("ESME_RTHROTTLED", 19)
			if err != nil {
				errCh <- err
				return
			}
			if got.Action != ErrorRetryRequeue || got.RequeueDelay != 30*time.Second || !got.KeepRetryEntry {
				errCh <- errors.New("unexpected concurrent decision")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func FuzzErrorRetryPolicyNeverPanics(f *testing.F) {
	f.Add("ESME_RSYSERR", 1, 2, float64(30))
	f.Add("", 0, 0, math.NaN())
	f.Fuzz(func(t *testing.T, status string, attempt, count int, delay float64) {
		policy, err := NewErrorRetryPolicy([]ErrorRetryRule{{Status: status, Count: count, DelaySeconds: delay}})
		if err != nil {
			return
		}
		_, _ = policy.Decide(status, attempt)
	})
}
