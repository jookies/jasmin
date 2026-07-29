package smppc

import (
	"testing"
	"time"
)

func TestReconnectBackoffDefaultsAndValidation(t *testing.T) {
	config := Config{CID: "backoff", Host: "127.0.0.1", Port: 2775, SystemID: "client"}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if config.ReconnectBackoffMax != 60 {
		t.Fatalf("reconnect_backoff_max = %g, want 60", config.ReconnectBackoffMax)
	}
	if config.ReconnectBackoffJitter == nil || *config.ReconnectBackoffJitter != 0.2 {
		t.Fatalf("reconnect_backoff_jitter = %v, want 0.2", config.ReconnectBackoffJitter)
	}

	for name, mutate := range map[string]func(*Config){
		"negative max": func(c *Config) { c.ReconnectBackoffMax = -1 },
		"negative jitter": func(c *Config) {
			value := -0.1
			c.ReconnectBackoffJitter = &value
		},
		"jitter one": func(c *Config) {
			value := 1.0
			c.ReconnectBackoffJitter = &value
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := Config{CID: "backoff", Host: "127.0.0.1", Port: 2775, SystemID: "client"}
			mutate(&invalid)
			if err := invalid.Validate(); err == nil {
				t.Fatal("invalid reconnect backoff config was accepted")
			}
		})
	}
}

func TestReconnectBackoffIsExponentialCappedAndResettable(t *testing.T) {
	backoff := newReconnectBackoff(func() float64 { return 1 })
	base := 10 * time.Second
	maximum := 60 * time.Second
	for index, want := range []time.Duration{
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		60 * time.Second,
		60 * time.Second,
	} {
		if got := backoff.next(base, maximum, 0.2); got != want {
			t.Fatalf("attempt %d delay = %s, want %s", index+1, got, want)
		}
	}
	backoff.reset()
	if got := backoff.next(base, maximum, 0.2); got != base {
		t.Fatalf("delay after reset = %s, want %s", got, base)
	}
}

func TestReconnectBackoffAddsBoundedDownwardJitter(t *testing.T) {
	backoff := newReconnectBackoff(func() float64 { return 0 })
	if got, want := backoff.next(10*time.Second, time.Minute, 0.2), 8*time.Second; got != want {
		t.Fatalf("jittered first delay = %s, want %s", got, want)
	}
}
