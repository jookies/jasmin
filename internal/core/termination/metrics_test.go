package termination

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/stats"
)

// sample reads one metric sample out of the process-wide registry.
//
// The registry is process-wide and monotonic, so these tests assert on the
// DELTA across an operation rather than on an absolute value: another test in
// this package running first would otherwise decide whether this one passes.
func sample(t *testing.T, name string, labels string) uint64 {
	t.Helper()
	pattern := regexp.MustCompile("(?m)^" + regexp.QuoteMeta(name+"{"+labels+"} ") + `(\d+)$`)
	match := pattern.FindStringSubmatch(string(stats.DefaultPrometheus().RenderPrometheus()))
	if match == nil {
		return 0
	}
	value, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		t.Fatalf("unparseable sample for %s{%s}: %v", name, labels, err)
	}
	return value
}

// TestWorkerProcessMovesTheVerdictCounters is the check this whole step exists
// for: a metric that is never observed incrementing is indistinguishable from an
// inert one, which is exactly the state these counters were in.
func TestWorkerProcessMovesTheVerdictCounters(t *testing.T) {
	const (
		verdictSeries = "synevyr_termination_verdicts_total"
		bypassSeries  = "synevyr_termination_gate_bypass_total"
		labels        = `connector="partner-a-term",outcome="delivrd"`
		bypassLabels  = `connector="partner-a-term",source="stub"`
	)
	beforeVerdicts := sample(t, verdictSeries, labels)
	beforeBypasses := sample(t, bypassSeries, bypassLabels)

	spool := &workerStubSpool{}
	verdicts := &workerStubVerdicts{verdict: Verdict{Accept: true, Stat: StatDelivered, Err: ReceiptErrNone}}
	worker := newTestWorker(t, spool, verdicts, &workerStubPublisher{}, true)
	if err := worker.Process(context.Background(),
		SubmitMetadata{QueueMessageID: "metrics-1", Partner: "partner-a"}, []byte("body")); err != nil {
		t.Fatalf("process: %v", err)
	}

	if got := sample(t, verdictSeries, labels) - beforeVerdicts; got != 1 {
		t.Errorf("verdict counter moved by %d, want 1", got)
	}
	// A verdict taken with a working gate must not touch the bypass counter:
	// the whole value of that counter is that it reads zero in normal operation.
	if got := sample(t, bypassSeries, bypassLabels) - beforeBypasses; got != 0 {
		t.Errorf("gate bypass counter moved by %d on a healthy gate, want 0", got)
	}
}

// TestWorkerProcessCountsAGateBypass covers the fail-open path: the partner is
// told DELIVRD and nothing else in the system looks wrong, so this counter is
// the only signal that traffic was accepted blind.
func TestWorkerProcessCountsAGateBypass(t *testing.T) {
	const (
		verdictSeries = "synevyr_termination_verdicts_total"
		bypassSeries  = "synevyr_termination_gate_bypass_total"
		labels        = `connector="partner-a-term",outcome="delivrd"`
		bypassLabels  = `connector="partner-a-term",source="stub"`
	)
	beforeVerdicts := sample(t, verdictSeries, labels)
	beforeBypasses := sample(t, bypassSeries, bypassLabels)

	bypassed := Verdict{
		Accept: true, Stat: StatDelivered, Err: ReceiptErrNone,
		Reason: ReasonGateUnreachable, GateBypassed: true,
	}
	worker := newTestWorker(t, &workerStubSpool{}, &workerStubVerdicts{verdict: bypassed}, &workerStubPublisher{}, true)
	if err := worker.Process(context.Background(),
		SubmitMetadata{QueueMessageID: "metrics-2", Partner: "partner-a"}, []byte("body")); err != nil {
		t.Fatalf("process: %v", err)
	}

	if got := sample(t, bypassSeries, bypassLabels) - beforeBypasses; got != 1 {
		t.Errorf("gate bypass counter moved by %d, want 1", got)
	}
	// It is counted in both: the partner really was told DELIVRD, and the
	// verdict series is what reports what they were told.
	if got := sample(t, verdictSeries, labels) - beforeVerdicts; got != 1 {
		t.Errorf("verdict counter moved by %d for a bypassed accept, want 1", got)
	}
}

func TestDeliveryRunnerMovesTheDeliveryCounters(t *testing.T) {
	const series = "synevyr_termination_delivery_total"
	attemptLabels := `connector="partner-a-term",outcome="attempt"`
	successLabels := `connector="partner-a-term",outcome="success"`
	failureLabels := `connector="partner-a-term",outcome="failure"`
	deadLabels := `connector="partner-a-term",outcome="dead_letter"`

	beforeAttempts := sample(t, series, attemptLabels)
	beforeSuccess := sample(t, series, successLabels)
	beforeFailures := sample(t, series, failureLabels)
	beforeDead := sample(t, series, deadLabels)

	store := newRunnerStubStore()
	store.due = []msgspool.Record{spoolRecord("deliver-ok", 0)}
	runner, err := NewDeliveryRunner(store, &runnerStubSink{}, DeliveryRunnerConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A permanently failing delivery on its last attempt: one attempt, one
	// failure, and one dead-letter. The failure and the dead-letter are separate
	// so a flapping endpoint and a permanently broken one look different.
	failStore := newRunnerStubStore()
	failStore.due = []msgspool.Record{spoolRecord("deliver-dead", 4)}
	failRunner, err := NewDeliveryRunner(failStore,
		&runnerStubSink{err: errors.New("downstream unreachable")},
		DeliveryRunnerConfig{MaxAttempts: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failRunner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := sample(t, series, attemptLabels) - beforeAttempts; got != 2 {
		t.Errorf("attempts moved by %d, want 2", got)
	}
	if got := sample(t, series, successLabels) - beforeSuccess; got != 1 {
		t.Errorf("successes moved by %d, want 1", got)
	}
	if got := sample(t, series, failureLabels) - beforeFailures; got != 1 {
		t.Errorf("failures moved by %d, want 1", got)
	}
	if got := sample(t, series, deadLabels) - beforeDead; got != 1 {
		t.Errorf("dead letters moved by %d, want 1", got)
	}
}
