package smppc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/logging"
)

func TestLogSubmitTimeout(t *testing.T) {
	var buffer bytes.Buffer
	session := newAuditSession(t, &buffer, nil, false)
	session.logSubmitTimeout(auditPending(t, nil))
	want := "SubmitSmPDU[qmsg-1] request timed out through [cid:smppc1], message requeued."
	if !strings.Contains(buffer.String(), want) {
		t.Errorf("timeout line missing:\n got %q\nwant substring %q", buffer.String(), want)
	}
	if !strings.Contains(buffer.String(), "ERROR") {
		t.Errorf("timeout line must be ERROR: %q", buffer.String())
	}
}

func TestLogExpiredDiscard(t *testing.T) {
	var buffer bytes.Buffer
	connector := &Connector{}
	connector.SetSubmitAuditLogger(logging.Logger("jasmin-sm-listener", logging.Config{Writer: &buffer}), false)
	connector.logExpiredDiscard("m1", time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))
	want := "Discarding expired message[m1]: expiration is 2026-07-26 00:00:00"
	if !strings.Contains(buffer.String(), want) {
		t.Errorf("expired-discard line missing:\n got %q\nwant substring %q", buffer.String(), want)
	}
	if !strings.Contains(buffer.String(), "INFO") {
		t.Errorf("expired-discard line must be INFO: %q", buffer.String())
	}
}

func TestLegacyDateTimeString(t *testing.T) {
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC), "2026-07-26 00:00:00"},
		{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "2026-01-02 03:04:05"},
		{time.Date(2026, 7, 26, 12, 30, 45, 123456000, time.UTC), "2026-07-26 12:30:45.123456"},
	}
	for _, testCase := range cases {
		if got := legacyDateTimeString(testCase.in); got != testCase.want {
			t.Errorf("legacyDateTimeString(%v) = %q, want %q", testCase.in, got, testCase.want)
		}
	}
}

// TestLegacyDateTimeStringDifferential confirms legacyDateTimeString matches
// Python's str(datetime) — the rendering the expired-discard line uses.
func TestLegacyDateTimeStringDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	type field struct {
		Y, Mo, D, H, Mi, S, US int
	}
	fields := []field{
		{2026, 7, 26, 0, 0, 0, 0},
		{2026, 12, 31, 23, 59, 59, 0},
		{2026, 3, 8, 2, 30, 15, 123456},
		{2000, 1, 1, 0, 0, 0, 999999},
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	const oracle = `
import sys, json, datetime
out = []
for f in json.load(sys.stdin):
    out.append(str(datetime.datetime(f['Y'], f['Mo'], f['D'], f['H'], f['Mi'], f['S'], f['US'])))
print(json.dumps(out))
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	for i, f := range fields {
		value := time.Date(f.Y, time.Month(f.Mo), f.D, f.H, f.Mi, f.S, f.US*1000, time.UTC)
		if got := legacyDateTimeString(value); got != want[i] {
			t.Errorf("field %d: go %q, py %q", i, got, want[i])
		}
	}
}
