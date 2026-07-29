package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// Embed the tz database so America/New_York (the DST zone the differential
	// exercises) loads regardless of the host's system tzdata.
	_ "time/tzdata"
)

func TestRotatingFileWriterRollsAtBoundary(t *testing.T) {
	base := filepath.Join(t.TempDir(), "messages.log")
	clock := time.Date(2026, 7, 25, 23, 59, 50, 0, time.UTC)
	writer, err := newRotatingFileWriter(base, "midnight", func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	// Pin the zone + boundary deterministically (the constructor used time.Local).
	writer.loc = time.UTC
	writer.rolloverAt = writer.computeRollover(clock.Unix())

	if _, err := writer.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2026, 7, 26, 0, 0, 1, 0, time.UTC) // just past midnight
	if _, err := writer.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	current, err := os.ReadFile(base)
	if err != nil || string(current) != "after\n" {
		t.Fatalf("base file = %q (err %v), want %q", current, err, "after\n")
	}
	rolled, err := os.ReadFile(base + ".2026-07-25")
	if err != nil || string(rolled) != "before\n" {
		t.Fatalf("rolled file = %q (err %v), want %q", rolled, err, "before\n")
	}
}

func TestRotatingFileWriterRejectsUnsupportedWhen(t *testing.T) {
	base := filepath.Join(t.TempDir(), "x.log")
	for _, when := range []string{"", "S", "H", "D", "W7", "daily"} {
		if _, err := newRotatingFileWriter(base, when, time.Now); err == nil {
			t.Errorf("when %q: expected error", when)
		}
	}
}

func TestFileSinkIsSharedByLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.log")
	first := fileOrStderr(path, "midnight")
	second := fileOrStderr(path, "midnight")
	if first != second {
		t.Fatal("components targeting one log_file received independent rotating writers")
	}
}

// probe builds a writer for compute/suffix testing without opening a file.
func probe(when string, loc *time.Location) *rotatingFileWriter {
	writer := &rotatingFileWriter{loc: loc, now: time.Now}
	upper := strings.ToUpper(when)
	if upper == "MIDNIGHT" {
		writer.when, writer.interval = "MIDNIGHT", secondsPerDay
	} else {
		writer.when, writer.interval, writer.dayOfWeek = upper, secondsPerDay*7, int(upper[1]-'0')
	}
	return writer
}

// TestRotatingRolloverDifferential proves computeRollover and the dated suffix
// match Python's TimedRotatingFileHandler across every weekday, near-midnight, and
// both DST transitions, for midnight and W0..W6.
func TestRotatingRolloverDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// Timestamps: two 8-day windows straddling the 2026 US DST transitions
	// (spring forward 03-08, fall back 11-01), every 3h; plus fine-grained
	// ±2h around each transition midnight.
	var timestamps []int64
	for _, start := range []time.Time{
		time.Date(2026, 3, 5, 0, 30, 0, 0, loc),
		time.Date(2026, 10, 29, 0, 30, 0, 0, loc),
	} {
		for hour := 0; hour < 24*8; hour += 3 {
			timestamps = append(timestamps, start.Add(time.Duration(hour)*time.Hour).Unix())
		}
	}
	for _, midnight := range []time.Time{
		time.Date(2026, 3, 8, 0, 0, 0, 0, loc),
		time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
	} {
		for minute := -120; minute <= 120; minute += 15 {
			timestamps = append(timestamps, midnight.Add(time.Duration(minute)*time.Minute).Unix())
		}
	}

	whens := []string{"midnight", "W0", "W1", "W2", "W3", "W4", "W5", "W6"}
	type spec struct {
		When string `json:"when"`
		TS   int64  `json:"ts"`
	}
	var specs []spec
	for _, when := range whens {
		for _, ts := range timestamps {
			specs = append(specs, spec{When: when, TS: ts})
		}
	}
	payload, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}

	const oracle = `
import sys, json, time, tempfile
import logging.handlers as lh
out = []
for s in json.load(sys.stdin):
    h = lh.TimedRotatingFileHandler(tempfile.mktemp(), when=s['when'], backupCount=0)
    ra = h.computeRollover(s['ts'])
    dstNow = time.localtime(s['ts'])[-1]
    t = ra - h.interval
    tt = time.localtime(t)
    if dstNow != tt[-1]:
        tt = time.localtime(t + (3600 if dstNow else -3600))
    out.append({"rollover_at": ra, "suffix": time.strftime(h.suffix, tt)})
    h.close()
print(json.dumps(out))
`
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Env = append(os.Environ(), "TZ=America/New_York")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []struct {
		RolloverAt int64  `json:"rollover_at"`
		Suffix     string `json:"suffix"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output: %v", err)
	}
	if len(want) != len(specs) {
		t.Fatalf("oracle returned %d, want %d", len(want), len(specs))
	}
	mismatches := 0
	for i, s := range specs {
		writer := probe(s.When, loc)
		gotRollover := writer.computeRollover(s.TS)
		writer.rolloverAt = gotRollover
		gotSuffix := writer.rolloverSuffix(s.TS)
		if gotRollover != want[i].RolloverAt || gotSuffix != want[i].Suffix {
			mismatches++
			if mismatches <= 8 {
				t.Errorf("when=%s ts=%d (%s): go rollover=%d suffix=%s | py rollover=%d suffix=%s",
					s.When, s.TS, time.Unix(s.TS, 0).In(loc).Format("2006-01-02 15:04 Mon"),
					gotRollover, gotSuffix, want[i].RolloverAt, want[i].Suffix)
			}
		}
	}
	if mismatches > 0 {
		t.Fatalf("%d/%d rollover mismatches", mismatches, len(specs))
	}
}
