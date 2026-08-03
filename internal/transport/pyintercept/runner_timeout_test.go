package pyintercept

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/interceptor"
	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
)

func testRoutable(t *testing.T) routingfilter.Routable {
	t.Helper()
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       routingfilter.MT,
		SourceAddr:      routingfilter.BytesField{Present: true, Value: []byte("1000")},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: []byte("380671234567")},
		ShortMessage:    routingfilter.BytesField{Present: true, Value: []byte("hello")},
		Timestamp:       time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("new routable: %v", err)
	}
	return routable
}

// TestHungScriptDoesNotWedgeTheRunner is the regression for the interceptor
// head-of-line block.
//
// Every Run holds one process-wide mutex across a round trip through a single
// shared subprocess, and nothing bounded the script itself: net/http's
// WriteTimeout does not cancel a running handler's context, so an operator
// script that looped forever held that mutex forever and every MT and MO
// interception in the process queued behind it permanently.
//
// The runner must now abandon the script on its own budget and, critically,
// still serve the next request — the subprocess is respawned after the kill.
func TestHungScriptDoesNotWedgeTheRunner(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	runner, err := NewRunner(context.Background(), "python3")
	if err != nil {
		t.Skipf("interceptor runner unavailable: %v", err)
	}
	defer func() { _ = runner.Close() }()

	if runner.timeout != DefaultScriptTimeout {
		t.Errorf("default timeout = %v, want %v", runner.timeout, DefaultScriptTimeout)
	}
	runner.SetScriptTimeout(300 * time.Millisecond)

	// A caller context that never ends, exactly like the HTTP submit path's.
	hung := interceptor.Script{IDValue: "hung", PyCode: "import time\ntime.sleep(60)"}
	started := time.Now()
	_, err = runner.Run(context.Background(), hung, interceptor.Context{Routable: testRoutable(t)})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("a script sleeping for a minute returned without error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Run blocked for %s, want abandonment near the 300ms budget", elapsed)
	}

	// The mutex is released and the runtime recovers: a well-behaved script
	// submitted afterwards must still run.
	runner.SetScriptTimeout(10 * time.Second)
	ok := interceptor.Script{IDValue: "ok", PyCode: "routable.pdu.params['short_message'] = b'rewritten'"}
	result, err := runner.Run(context.Background(), ok, interceptor.Context{Routable: testRoutable(t)})
	if err != nil {
		t.Fatalf("runner did not recover after killing a hung script: %v", err)
	}
	if got := result.Routable.ShortMessage(); string(got.Value) != "rewritten" {
		t.Errorf("short_message = %q, want %q", got.Value, "rewritten")
	}
}

// TestMemoryBombIsContainedNotFatal proves the runner's resource limits bound a
// hostile or runaway script's memory to a MemoryError inside the subprocess
// rather than an OOM kill that takes the gateway with it.
//
// This is containment, not a sandbox: scripts keep full builtins by contract,
// so authoring one remains equivalent to shell access as the gateway's user.
func TestMemoryBombIsContainedNotFatal(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	// Linux only, deliberately. macOS reports RLIMIT_AS but refuses to set it
	// ("current limit exceeds maximum limit"), so the runner's setrlimit is a
	// no-op there and there is nothing to assert. Production is python:3.12-slim,
	// where it is set and enforced.
	if runtime.GOOS != "linux" {
		t.Skip("RLIMIT_AS is only settable on Linux; production runs Linux")
	}
	runner, err := NewRunner(context.Background(), "python3")
	if err != nil {
		t.Skipf("interceptor runner unavailable: %v", err)
	}
	defer func() { _ = runner.Close() }()
	runner.SetScriptTimeout(20 * time.Second)

	// Well past RLIMIT_AS; without the limit this is an unbounded allocation.
	bomb := interceptor.Script{IDValue: "bomb", PyCode: "x = bytearray(4 * 1024 * 1024 * 1024)"}
	if _, err := runner.Run(context.Background(), bomb, interceptor.Context{Routable: testRoutable(t)}); err == nil {
		t.Fatal("a 4 GiB allocation succeeded; the address-space limit is not applied")
	}

	// And the runner still serves the next script.
	ok := interceptor.Script{IDValue: "ok", PyCode: "routable.pdu.params['short_message'] = b'alive'"}
	result, err := runner.Run(context.Background(), ok, interceptor.Context{Routable: testRoutable(t)})
	if err != nil {
		t.Fatalf("runner did not recover after a memory bomb: %v", err)
	}
	if got := result.Routable.ShortMessage(); string(got.Value) != "alive" {
		t.Errorf("short_message = %q, want %q", got.Value, "alive")
	}
}
