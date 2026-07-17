package smppc

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReadinessProceedAndExpirationBoundary(t *testing.T) {
	policy, err := NewReadinessPolicy(DefaultReadinessConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	exact := now
	got, err := policy.Decide(ReadinessInput{
		Now: now, CreatedAt: now.Add(-time.Hour), Expiration: &exact, Connected: true, Bound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != ReadinessProceed {
		t.Fatalf("expiration equal to now action = %q", got.Action)
	}
	expired := now.Add(-time.Nanosecond)
	got, err = policy.Decide(ReadinessInput{
		Now: now, CreatedAt: now, Expiration: &expired, Connected: true, Bound: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != ReadinessDiscard {
		t.Fatalf("expired action = %q", got.Action)
	}
}

func TestLegacyTimedeltaSecondsSubsecondAndDays(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		created time.Time
		want    int64
	}{
		{"one_day_plus_ten", now.Add(-24*time.Hour - 10*time.Second), 10},
		{"future_one_second", now.Add(time.Second), 86399},
		{"future_one_microsecond", now.Add(time.Microsecond), 86399},
		{"future_submicrosecond_truncated", now.Add(time.Nanosecond), 0},
		{"past_subsecond", now.Add(-time.Nanosecond), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := legacyTimedeltaSeconds(now, tc.created)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("seconds = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReadinessValidation(t *testing.T) {
	for _, cfg := range []ReadinessConfig{
		{MaxAgeSeconds: -1},
		{RetryDelaySeconds: -1},
		{RetryDelaySeconds: int64(^uint64(0)>>1)/int64(time.Second) + 1},
	} {
		if _, err := NewReadinessPolicy(cfg); !errors.Is(err, ErrInvalidReadinessConfig) {
			t.Fatalf("config %+v error = %v", cfg, err)
		}
	}
	policy, err := NewReadinessPolicy(DefaultReadinessConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	invalid := []ReadinessInput{
		{},
		{Now: now, CreatedAt: now, Bound: true},
	}
	for _, input := range invalid {
		if _, err := policy.Decide(input); !errors.Is(err, ErrInvalidReadinessInput) {
			t.Fatalf("input %+v error = %v", input, err)
		}
	}
}

func TestReadinessPolicyConcurrent(t *testing.T) {
	policy, err := NewReadinessPolicy(DefaultReadinessConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const workers = 64
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := policy.Decide(ReadinessInput{Now: now, CreatedAt: now, Connected: false})
			if err != nil {
				errs <- err
				return
			}
			if decision.Action != ReadinessRequeue || decision.RequeueDelay != 30*time.Second {
				errs <- errors.New("unexpected concurrent decision")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func FuzzLegacyTimedeltaSeconds(f *testing.F) {
	f.Add(int64(0), int32(0))
	f.Add(int64(86_410), int32(0))
	f.Add(int64(-1), int32(1_000))
	f.Fuzz(func(t *testing.T, ageSeconds int64, nanos int32) {
		if ageSeconds < -172_800 || ageSeconds > 172_800 || nanos < 0 || nanos >= int32(time.Second) {
			t.Skip()
		}
		now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
		created := now.Add(-time.Duration(ageSeconds)*time.Second + time.Duration(nanos))
		got, err := legacyTimedeltaSeconds(now, created)
		if err != nil {
			t.Fatal(err)
		}
		if got < 0 || got >= 86_400 {
			t.Fatalf("seconds component out of range: %d", got)
		}
	})
}
