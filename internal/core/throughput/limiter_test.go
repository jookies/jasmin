package throughput

import (
	"testing"
	"time"
)

func TestAllowSpacingMatchesTheOracle(t *testing.T) {
	quota := func(v float64) *float64 { return &v }
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	for _, testCase := range []struct {
		name  string
		quota *float64
		// gaps are the offsets from base at which submits arrive.
		gaps []time.Duration
		want []bool
		why  string
	}{
		{
			name:  "unset quota never throttles",
			quota: nil,
			gaps:  []time.Duration{0, time.Nanosecond, 2 * time.Nanosecond},
			want:  []bool{true, true, true},
			why:   "no ceiling was provisioned",
		},
		{
			name:  "zero means unlimited, not blocked",
			quota: quota(0),
			gaps:  []time.Duration{0, time.Nanosecond},
			want:  []bool{true, true},
			why:   "Python's `if quota and ...` treats 0.0 as falsy",
		},
		{
			name:  "negative means unlimited",
			quota: quota(-1),
			gaps:  []time.Duration{0, time.Nanosecond},
			want:  []bool{true, true},
			why:   "the `quota >= 0` arm skips the check",
		},
		{
			name:  "first submit always passes",
			quota: quota(1),
			gaps:  []time.Duration{0},
			want:  []bool{true},
			why:   "legacy also requires qos_last_submit_sm_at != 0",
		},
		{
			name:  "one per second rejects the message that is too early",
			quota: quota(1),
			gaps:  []time.Duration{0, 999 * time.Millisecond, 2 * time.Second},
			want:  []bool{true, false, true},
			why:   "999ms is inside the 1s spacing; 2s is outside",
		},
		{
			name:  "exactly on the boundary is accepted",
			quota: quota(2),
			gaps:  []time.Duration{0, 500 * time.Millisecond},
			want:  []bool{true, true},
			why:   "legacy rejects on `<`, so an exact interval passes",
		},
		{
			name:  "a rejection does not move the clock",
			quota: quota(1),
			gaps:  []time.Duration{0, 600 * time.Millisecond, 1100 * time.Millisecond},
			want:  []bool{true, false, true},
			why:   "the third is 1.1s after the accepted first, not 0.5s after the rejected second",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			limiter := New()
			for index, gap := range testCase.gaps {
				got := limiter.Allow("alice:httpapi", testCase.quota, base.Add(gap))
				if got != testCase.want[index] {
					t.Fatalf("submit %d at +%s = %v, want %v (%s)",
						index, gap, got, testCase.want[index], testCase.why)
				}
			}
		})
	}
}

// TestKeysAreIndependent pins that the ceiling is per key. Legacy holds
// qos_last_submit_sm_at in separate httpapi and smpps maps on CnxStatus, so a
// user's HTTP traffic must not consume their SMPPs allowance or vice versa,
// and one user must never throttle another.
func TestKeysAreIndependent(t *testing.T) {
	quota := 1.0
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	limiter := New()

	if !limiter.Allow("alice:httpapi", &quota, base) {
		t.Fatal("first HTTP submit was refused")
	}
	if !limiter.Allow("alice:smpps", &quota, base) {
		t.Fatal("SMPPs submit was throttled by the user's HTTP traffic")
	}
	if !limiter.Allow("bob:httpapi", &quota, base) {
		t.Fatal("one user's traffic throttled another")
	}
	if limiter.Allow("alice:httpapi", &quota, base) {
		t.Fatal("a second same-instant HTTP submit was accepted")
	}
}

func TestForgetClearsSpacing(t *testing.T) {
	quota := 1.0
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	limiter := New()

	if !limiter.Allow("alice:httpapi", &quota, base) {
		t.Fatal("first submit was refused")
	}
	limiter.Forget("alice:httpapi")
	if !limiter.Allow("alice:httpapi", &quota, base) {
		t.Fatal("a forgotten key still carried its spacing")
	}
}
