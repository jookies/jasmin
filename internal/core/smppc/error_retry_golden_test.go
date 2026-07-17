package smppc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type errorRetryFixture struct {
	Defaults []ErrorRetryRule `json:"defaults"`
	Cases    []struct {
		ID    string `json:"id"`
		Input struct {
			Status         string           `json:"status"`
			CurrentAttempt int              `json:"current_attempt"`
			Rules          []ErrorRetryRule `json:"rules"`
		} `json:"input"`
		Expected struct {
			Action              ErrorRetryAction `json:"action"`
			RequeueDelaySeconds *float64         `json:"requeue_delay_seconds"`
			RetryEntryAfter     *int             `json:"retry_entry_after"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestLegacyErrorRetryGolden(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "smpp-client-error-retry", "baseline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture errorRetryFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("error-retry fixture has no cases")
	}
	wantDefaults := DefaultErrorRetryRules()
	if len(fixture.Defaults) != len(wantDefaults) {
		t.Fatalf("default rule count = %d, want %d", len(fixture.Defaults), len(wantDefaults))
	}
	for i := range wantDefaults {
		if fixture.Defaults[i] != wantDefaults[i] {
			t.Fatalf("default rule %d = %+v, want %+v", i, fixture.Defaults[i], wantDefaults[i])
		}
	}

	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			policy, err := NewErrorRetryPolicy(tc.Input.Rules)
			if err != nil {
				t.Fatal(err)
			}
			got, err := policy.Decide(tc.Input.Status, tc.Input.CurrentAttempt)
			if err != nil {
				t.Fatal(err)
			}
			if got.Action != tc.Expected.Action {
				t.Fatalf("action = %q, want %q", got.Action, tc.Expected.Action)
			}
			if tc.Expected.RequeueDelaySeconds == nil {
				if got.RequeueDelay != 0 {
					t.Fatalf("final delay = %s, want zero", got.RequeueDelay)
				}
			} else if got.RequeueDelay != time.Duration(*tc.Expected.RequeueDelaySeconds*float64(time.Second)) {
				t.Fatalf("delay = %s, want %gs", got.RequeueDelay, *tc.Expected.RequeueDelaySeconds)
			}
			if tc.Expected.RetryEntryAfter == nil {
				if got.KeepRetryEntry {
					t.Fatal("retry entry kept, want cleared")
				}
			} else {
				if *tc.Expected.RetryEntryAfter != tc.Input.CurrentAttempt {
					t.Fatalf("fixture retry entry = %d, current attempt = %d", *tc.Expected.RetryEntryAfter, tc.Input.CurrentAttempt)
				}
				if !got.KeepRetryEntry {
					t.Fatal("retry entry cleared, want kept")
				}
			}
		})
	}
}
