package restcompat_test

import (
	"testing"

	"github.com/pumpitspace/synevyr/internal/transport/restcompat"
)

func TestRESTConfigDefaultsAndExplicitQoSDisable(t *testing.T) {
	defaults := restcompat.Config{}
	if err := defaults.Validate(); err != nil {
		t.Fatal(err)
	}
	if defaults.EffectiveHTTPThroughputPerWorker() != 8 || !defaults.EffectiveSmartQoS() {
		t.Fatalf("defaults = throughput:%v smart:%v",
			defaults.EffectiveHTTPThroughputPerWorker(), defaults.EffectiveSmartQoS())
	}
	zero := 0.0
	no := false
	disabled := restcompat.Config{HTTPThroughputPerWorker: &zero, SmartQoS: &no}
	if err := disabled.Validate(); err != nil {
		t.Fatal(err)
	}
	if disabled.EffectiveHTTPThroughputPerWorker() != 0 || disabled.EffectiveSmartQoS() {
		t.Fatalf("disabled = throughput:%v smart:%v",
			disabled.EffectiveHTTPThroughputPerWorker(), disabled.EffectiveSmartQoS())
	}
}

func TestRESTConfigRejectsUnsafeQueueAndRetryValues(t *testing.T) {
	negative := -1.0
	for name, config := range map[string]restcompat.Config{
		"throughput": {HTTPThroughputPerWorker: &negative},
		"pending":    {MaxPendingTasks: -1},
		"attempts":   {MaxAttempts: -1},
		"retry":      {RetryDelaySeconds: -1},
		"callbacks":  {CallbackMaxAttempts: -1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(); err == nil {
				t.Fatal("invalid REST config accepted")
			}
		})
	}
}
