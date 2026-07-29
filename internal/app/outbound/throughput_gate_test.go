package outbound

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

// TestThroughputGateEnforcesTheProvisionedCeiling closes a gap that was worse
// than a missing feature: http_throughput and smpps_throughput were accepted by
// jCli, stored, and rendered back in the user list, while nothing anywhere
// enforced them. An operator could rate-limit a customer, see the limit
// reported, and have it do nothing.
func TestThroughputGateEnforcesTheProvisionedCeiling(t *testing.T) {
	digest := sha256.Sum256([]byte("secret"))
	hexDigest := hex.EncodeToString(digest[:])
	quota := func(v float64) *float64 { return &v }

	directory, err := newRuntimeDirectory(Config{Users: []UserConfig{
		{
			Username: "capped", ExternalID: "capped", PasswordSHA256: hexDigest,
			MTCredential: &MTCredentialConfig{
				HTTPThroughput:  quota(1),
				SMPPSThroughput: quota(2),
			},
		},
		{Username: "uncapped", ExternalID: "uncapped", PasswordSHA256: hexDigest},
		{
			Username: "zero", ExternalID: "zero", PasswordSHA256: hexDigest,
			MTCredential: &MTCredentialConfig{HTTPThroughput: quota(0)},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gate := newThroughputGate(directory)
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	// One per second on HTTP.
	if !gate.AllowSubmit("capped", "httpapi", base) {
		t.Fatal("first HTTP submit was refused")
	}
	if gate.AllowSubmit("capped", "httpapi", base.Add(500*time.Millisecond)) {
		t.Fatal("a submit at 2/s was accepted under a 1/s HTTP ceiling")
	}
	if !gate.AllowSubmit("capped", "httpapi", base.Add(time.Second)) {
		t.Fatal("a submit exactly one second later was refused")
	}

	// The SMPPs ceiling is separate and looser; HTTP traffic must not have
	// consumed it. The ingress name is the submit request's source connector.
	if !gate.AllowSubmit("capped", "smppsapi", base) {
		t.Fatal("first SMPPs submit was refused")
	}
	if !gate.AllowSubmit("capped", "smppsapi", base.Add(500*time.Millisecond)) {
		t.Fatal("2/s was refused under a 2/s SMPPs ceiling — the HTTP quota was applied to SMPPs")
	}
	if gate.AllowSubmit("capped", "smppsapi", base.Add(600*time.Millisecond)) {
		t.Fatal("a submit inside the 500ms SMPPs spacing was accepted")
	}

	// No quota provisioned, and an explicit zero, are both unlimited.
	for _, username := range []string{"uncapped", "zero"} {
		for index := range 5 {
			if !gate.AllowSubmit(username, "httpapi", base.Add(time.Duration(index))) {
				t.Fatalf("user %q was throttled without an effective ceiling", username)
			}
		}
	}
}

// TestThroughputSurvivesReprovisioning pins that editing a user changes their
// ceiling without handing them a free submit. The quota is provisioning data
// the admin plane rewrites freely; the spacing is live state.
func TestThroughputSurvivesReprovisioning(t *testing.T) {
	digest := sha256.Sum256([]byte("secret"))
	hexDigest := hex.EncodeToString(digest[:])
	quota := 1.0

	directory, err := newRuntimeDirectory(Config{Users: []UserConfig{{
		Username: "alice", ExternalID: "alice", PasswordSHA256: hexDigest,
		MTCredential: &MTCredentialConfig{HTTPThroughput: &quota},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	gate := newThroughputGate(directory)
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	if !gate.AllowSubmit("alice", "httpapi", base) {
		t.Fatal("first submit was refused")
	}
	if gate.AllowSubmit("alice", "httpapi", base.Add(100*time.Millisecond)) {
		t.Fatal("second submit was accepted inside the spacing")
	}

	// Re-provision the same user with a looser ceiling.
	if err := directory.removeUser("alice"); err != nil {
		t.Fatal(err)
	}
	loose := 100.0
	if err := directory.applyUser(UserConfig{
		Username: "alice", ExternalID: "alice", PasswordSHA256: hexDigest,
		MTCredential: &MTCredentialConfig{HTTPThroughput: &loose},
	}, 1); err != nil {
		t.Fatal(err)
	}
	if !gate.AllowSubmit("alice", "httpapi", base.Add(110*time.Millisecond)) {
		t.Fatal("the new, looser ceiling was not picked up")
	}
}
