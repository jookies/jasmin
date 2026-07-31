package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/stats"
)

func TestQueueDepthObserverPublishesDepths(t *testing.T) {
	registry := stats.NewPrometheusRegistry()
	observer := newQueueDepthObserver(
		func(context.Context, []string) (map[string]int, error) {
			return map[string]int{"submit.sm.smsc-primary": 7, "DLRLookup-main": 0}, nil
		},
		func() []string { return []string{"submit.sm.smsc-primary", "DLRLookup-main"} },
		0, nil, registry,
	)
	if err := observer.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	out := string(registry.RenderPrometheus())
	for _, want := range []string{
		`synevyr_queue_depth{queue="submit.sm.smsc-primary"} 7`,
		`synevyr_queue_depth{queue="DLRLookup-main"} 0`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// TestQueueDepthObserverZeroesUnreadableQueues is the failure mode this observer
// is shaped around: a gauge that stops updating reads as a healthy steady state,
// so a queue that disappears from the observation must go to zero rather than
// freeze at its last value.
func TestQueueDepthObserverZeroesUnreadableQueues(t *testing.T) {
	registry := stats.NewPrometheusRegistry()
	depths := map[string]int{"submit.sm.gone": 40}
	observer := newQueueDepthObserver(
		func(context.Context, []string) (map[string]int, error) {
			if len(depths) == 0 {
				return nil, errors.New("queue not found")
			}
			return depths, nil
		},
		func() []string { return []string{"submit.sm.gone"} },
		0, nil, registry,
	)
	if err := observer.runOnce(context.Background()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if !strings.Contains(string(registry.RenderPrometheus()), `synevyr_queue_depth{queue="submit.sm.gone"} 40`+"\n") {
		t.Fatal("first pass did not record the depth")
	}

	depths = map[string]int{}
	if err := observer.runOnce(context.Background()); err == nil {
		t.Fatal("want the observation error surfaced, got nil")
	}
	if !strings.Contains(string(registry.RenderPrometheus()), `synevyr_queue_depth{queue="submit.sm.gone"} 0`+"\n") {
		t.Errorf("unreadable queue kept its stale depth:\n%s", registry.RenderPrometheus())
	}
}

func TestQueueDepthObserverSkipsWhenNothingToObserve(t *testing.T) {
	called := false
	observer := newQueueDepthObserver(
		func(context.Context, []string) (map[string]int, error) {
			called = true
			return nil, nil
		},
		func() []string { return nil },
		0, nil, stats.NewPrometheusRegistry(),
	)
	if err := observer.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if called {
		t.Error("observed an empty queue list instead of skipping the broker round trip")
	}
}
