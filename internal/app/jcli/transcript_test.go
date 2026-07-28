package jcli

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Transcript fixtures are byte-for-byte recordings of the frozen Python
// console, captured by scripts/compat/capture_jcli_transcript.py. Replaying the
// same input lines against this package must produce the same bytes: a single
// differing space is a failed test, which is the entire point of the exercise.
//
// Regenerate with:
//
//	JASMIN_AMQP_PORT=5673 JASMIN_AMQP_VHOST=jcli-oracle \
//	  .venv-oracle/bin/python scripts/compat/capture_jcli_transcript.py
const fixtureDir = "../../../spec/compatibility/fixtures/jcli"

type transcriptHeader struct {
	Fixture        string `json:"fixture"`
	Title          string `json:"title"`
	Authentication bool   `json:"authentication"`
	Release        string `json:"release"`
	Oracle         string `json:"oracle"`
}

type transcriptStep struct {
	Step      int     `json:"step"`
	Input     *string `json:"input"`
	OutputB64 string  `json:"output_b64"`
}

type transcript struct {
	header transcriptHeader
	steps  []transcriptStep
}

func loadTranscript(t *testing.T, path string) transcript {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = file.Close() }()

	var loaded transcript
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if first {
			if err := json.Unmarshal(line, &loaded.header); err != nil {
				t.Fatalf("parse fixture header: %v", err)
			}
			first = false
			continue
		}
		var step transcriptStep
		if err := json.Unmarshal(line, &step); err != nil {
			t.Fatalf("parse fixture step: %v", err)
		}
		loaded.steps = append(loaded.steps, step)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return loaded
}

func (s transcriptStep) output(t *testing.T) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(s.OutputB64)
	if err != nil {
		t.Fatalf("decode fixture output: %v", err)
	}
	return decoded
}

// drainQuiet reads until the connection goes quiet for settleWindow. The console
// answers a line with one burst of writes, so "no more bytes for a moment" is a
// sound end-of-reply signal and keeps the test independent of prompt shapes.
const settleWindow = 150 * time.Millisecond

func (c *client) drainQuiet(t *testing.T) []byte {
	t.Helper()
	var collected []byte
	buffer := make([]byte, 4096)
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(settleWindow))
		n, err := c.conn.Read(buffer)
		if n > 0 {
			collected = append(collected, buffer[:n]...)
		}
		if err != nil {
			_ = c.conn.SetReadDeadline(time.Time{})
			return collected
		}
	}
}

// TestOracleTranscripts replays every captured fixture. A fixture whose
// commands this console does not implement yet is reported as a skip naming the
// first divergence, so the suite tracks progress instead of failing wholesale.
func TestOracleTranscripts(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.jsonl"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no jCli fixtures found; run scripts/compat/capture_jcli_transcript.py")
	}

	for _, path := range paths {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		t.Run(name, func(t *testing.T) {
			loaded := loadTranscript(t, path)
			if loaded.header.Release != LegacyRelease {
				t.Fatalf("fixture release %q != console release %q",
					loaded.header.Release, LegacyRelease)
			}

			fixture := newConsoleFixtureAuth(t, loaded.header.Authentication)
			console := fixture.dial()

			for _, step := range loaded.steps {
				if step.Input != nil {
					input := *step.Input
					if strings.HasSuffix(input, "\t") {
						// Completion input is sent as typed, with no RETURN.
						console.writeRaw(t, input)
					} else {
						console.writeRaw(t, input+"\r\n")
					}
				}
				want := step.output(t)
				got := console.drainQuiet(t)
				if string(got) != string(want) {
					label := "(connect)"
					if step.Input != nil {
						label = *step.Input
					}
					t.Fatalf("step %d after %q:\n want %s\n  got %s",
						step.Step, label, quoteBytes(want), quoteBytes(got))
				}
			}
		})
	}
}

func (c *client) writeRaw(t *testing.T, text string) {
	t.Helper()
	if _, err := c.conn.Write([]byte(text)); err != nil {
		t.Fatalf("write %q: %v", text, err)
	}
}

// quoteBytes renders a transcript chunk readably: control bytes stay visible
// (they are the contract) but line breaks are marked so diffs are legible.
func quoteBytes(data []byte) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for i := 0; i < len(data); i++ {
		switch {
		case data[i] == '\r':
			builder.WriteString("\\r")
		case data[i] == '\n':
			builder.WriteString("\\n")
		case data[i] == 0x1b:
			builder.WriteString("\\e")
		case data[i] < 0x20 || data[i] > 0x7e:
			builder.WriteString(fmt.Sprintf("\\x%02x", data[i]))
		default:
			builder.WriteByte(data[i])
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
