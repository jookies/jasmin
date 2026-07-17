package smppc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type readinessFixture struct {
	Defaults ReadinessConfig `json:"defaults"`
	Cases    []struct {
		ID    string `json:"id"`
		Input struct {
			Connected               bool   `json:"connected"`
			Bound                   bool   `json:"bound"`
			CreatedAgeSeconds       *int64 `json:"created_age_seconds"`
			ExpirationOffsetSeconds *int64 `json:"expiration_offset_seconds"`
			MaxAgeSeconds           int64  `json:"max_age_seconds"`
			RetryDelaySeconds       int64  `json:"retry_delay_seconds"`
		} `json:"input"`
		Expected struct {
			Action              ReadinessAction `json:"action"`
			RequeueDelaySeconds *int64          `json:"requeue_delay_seconds"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestLegacyReadinessGolden(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "smpp-client-readiness", "baseline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture readinessFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("readiness fixture has no cases")
	}
	if fixture.Defaults != (ReadinessConfig{MaxAgeSeconds: 1200, RetryDelaySeconds: 30}) {
		t.Fatalf("defaults = %+v", fixture.Defaults)
	}

	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			policy, err := NewReadinessPolicy(ReadinessConfig{
				MaxAgeSeconds: tc.Input.MaxAgeSeconds, RetryDelaySeconds: tc.Input.RetryDelaySeconds,
			})
			if err != nil {
				t.Fatal(err)
			}
			input := ReadinessInput{Now: now, Connected: tc.Input.Connected, Bound: tc.Input.Bound}
			if tc.Input.CreatedAgeSeconds != nil {
				input.CreatedAt = now.Add(-time.Duration(*tc.Input.CreatedAgeSeconds) * time.Second)
			}
			if tc.Input.ExpirationOffsetSeconds != nil {
				expiration := now.Add(time.Duration(*tc.Input.ExpirationOffsetSeconds) * time.Second)
				input.Expiration = &expiration
			}
			got, err := policy.Decide(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Action != tc.Expected.Action {
				t.Fatalf("action = %q, want %q", got.Action, tc.Expected.Action)
			}
			if tc.Expected.RequeueDelaySeconds == nil {
				if got.RequeueDelay != 0 {
					t.Fatalf("discard delay = %s, want zero", got.RequeueDelay)
				}
			} else if got.RequeueDelay != time.Duration(*tc.Expected.RequeueDelaySeconds)*time.Second {
				t.Fatalf("delay = %s, want %ds", got.RequeueDelay, *tc.Expected.RequeueDelaySeconds)
			}
		})
	}
}
