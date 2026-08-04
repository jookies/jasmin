package dlrgate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type stubPolicies map[string]Policy

func (s stubPolicies) ResolveDLRGatePolicy(username string) (Policy, bool) {
	policy, ok := s[username]
	return policy, ok
}

type stubRegistry struct {
	present map[string]bool
	owners  map[string]string
	err     error
}

func (s stubRegistry) Window(_ context.Context, digits string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	return s.owners[digits], s.present[digits], nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGateDecide(t *testing.T) {
	enabled := Policy{Enabled: true}
	custom := Policy{Enabled: true, HitStatus: "ACCEPTD", HitError: "001", MissStatus: "UNDELIV", MissError: "042"}

	cases := []struct {
		name        string
		policies    stubPolicies
		registry    stubRegistry
		username    string
		destination string
		wantApplied bool
		wantStatus  string
		wantError   string
		wantHit     bool
	}{
		{
			name:     "registered number gets the hit receipt",
			policies: stubPolicies{"demoesme": enabled},
			registry: stubRegistry{present: map[string]bool{"380930242105": true}},
			username: "demoesme", destination: "380930242105",
			wantApplied: true, wantStatus: "DELIVRD", wantError: "000", wantHit: true,
		},
		{
			name:     "unregistered number gets the miss receipt",
			policies: stubPolicies{"demoesme": enabled},
			registry: stubRegistry{present: map[string]bool{}},
			username: "demoesme", destination: "380930242105",
			wantApplied: true, wantStatus: "REJECTD", wantError: "008",
		},
		{
			name:     "leading plus is the same number",
			policies: stubPolicies{"demoesme": enabled},
			registry: stubRegistry{present: map[string]bool{"380930242105": true}},
			username: "demoesme", destination: "+380930242105",
			wantApplied: true, wantStatus: "DELIVRD", wantError: "000", wantHit: true,
		},
		{
			name:     "per-user statuses are honoured",
			policies: stubPolicies{"demoesme": custom},
			registry: stubRegistry{present: map[string]bool{}},
			username: "demoesme", destination: "380930242105",
			wantApplied: true, wantStatus: "UNDELIV", wantError: "042",
		},
		{
			name:     "a user with no policy is untouched",
			policies: stubPolicies{},
			registry: stubRegistry{present: map[string]bool{}},
			username: "someoneelse", destination: "380930242105",
			wantApplied: false,
		},
		{
			name:     "a disabled policy is untouched",
			policies: stubPolicies{"demoesme": {Enabled: false}},
			registry: stubRegistry{present: map[string]bool{}},
			username: "demoesme", destination: "380930242105",
			wantApplied: false,
		},
		{
			name:     "a destination that is not a number is a miss",
			policies: stubPolicies{"demoesme": enabled},
			registry: stubRegistry{present: map[string]bool{}},
			username: "demoesme", destination: "undefined",
			wantApplied: true, wantStatus: "REJECTD", wantError: "008",
		},
		{
			name:     "a window this user owns is a hit",
			policies: stubPolicies{"demoesme": enabled},
			registry: stubRegistry{
				present: map[string]bool{"380930242105": true},
				owners:  map[string]string{"380930242105": "demoesme"},
			},
			username: "demoesme", destination: "380930242105",
			wantApplied: true, wantStatus: "DELIVRD", wantError: "000", wantHit: true,
		},
		{
			name:     "a window another user owns is a miss",
			policies: stubPolicies{"demoesme": enabled},
			registry: stubRegistry{
				present: map[string]bool{"380930242105": true},
				owners:  map[string]string{"380930242105": "otherpartner"},
			},
			username: "demoesme", destination: "380930242105",
			wantApplied: true, wantStatus: "REJECTD", wantError: "008",
		},
		{
			name:     "an unreadable registry fails open to the hit receipt",
			policies: stubPolicies{"demoesme": enabled},
			registry: stubRegistry{err: errors.New("connection refused")},
			username: "demoesme", destination: "380930242105",
			wantApplied: true, wantStatus: "DELIVRD", wantError: "000", wantHit: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gate := NewGate(testCase.registry, testCase.policies, quietLogger())
			verdict, applied := gate.Decide(context.Background(), testCase.username, testCase.destination)
			if applied != testCase.wantApplied {
				t.Fatalf("applied = %v, want %v", applied, testCase.wantApplied)
			}
			if !applied {
				return
			}
			if verdict.Status != testCase.wantStatus || verdict.Error != testCase.wantError {
				t.Fatalf("verdict = %s/%s, want %s/%s", verdict.Status, verdict.Error, testCase.wantStatus, testCase.wantError)
			}
			if verdict.Hit != testCase.wantHit {
				t.Fatalf("hit = %v, want %v", verdict.Hit, testCase.wantHit)
			}
		})
	}
}

func TestNewGateIsNilWithoutDependencies(t *testing.T) {
	if gate := NewGate(nil, stubPolicies{}, nil); gate != nil {
		t.Fatal("NewGate with no registry should be nil")
	}
	// A nil gate must still be safe to call: the submit path holds it as an
	// interface and the nil check there cannot see a typed nil.
	var gate *Gate
	if _, applied := gate.Decide(context.Background(), "demoesme", "380930242105"); applied {
		t.Fatal("a nil gate must not apply an override")
	}
}

func TestPolicyValidateRejectsUnpublishableStatus(t *testing.T) {
	if err := (Policy{Enabled: true, HitStatus: "DEFINITELY_NOT_A_STATUS"}).Validate(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("Validate error = %v, want ErrInvalidPolicy", err)
	}
	if err := (Policy{Enabled: true}).Validate(); err != nil {
		t.Fatalf("the default policy must validate, got %v", err)
	}
	// A disabled policy is never consulted, so its fields are not checked.
	if err := (Policy{HitStatus: "NONSENSE"}).Validate(); err != nil {
		t.Fatalf("a disabled policy must validate, got %v", err)
	}
}
