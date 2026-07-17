package smppc

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type pacingFixture struct {
	Cases []struct {
		ID    string `json:"id"`
		Input struct {
			Throughput     json.RawMessage `json:"throughput"`
			ElapsedSeconds *float64        `json:"elapsed_seconds"`
		} `json:"input"`
		Expected struct {
			Config struct {
				Value     *float64 `json:"value"`
				ErrorType *string  `json:"error_type"`
			} `json:"config"`
			WaitSeconds *float64 `json:"wait_seconds"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestLegacyPacingGolden(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "smpp-client-pacing", "baseline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture pacingFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("pacing fixture has no cases")
	}

	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			var throughput float64
			if err := json.Unmarshal(tc.Input.Throughput, &throughput); err != nil {
				var marker string
				if markerErr := json.Unmarshal(tc.Input.Throughput, &marker); markerErr != nil {
					t.Fatalf("throughput is neither number nor marker: %v", markerErr)
				}
				switch marker {
				case "default":
					cfg := Config{}
					if got := cfg.EffectiveSubmitSMThroughput(); tc.Expected.Config.Value == nil || got != *tc.Expected.Config.Value {
						t.Fatalf("default throughput = %v, want %v", got, tc.Expected.Config.Value)
					}
				case "fast":
					if tc.Expected.Config.ErrorType == nil || *tc.Expected.Config.ErrorType != "TypeMismatch" {
						t.Fatalf("non-numeric error = %v, want TypeMismatch", tc.Expected.Config.ErrorType)
					}
					var cfg Config
					payload, err := json.Marshal(map[string]json.RawMessage{"submit_sm_throughput": tc.Input.Throughput})
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(payload, &cfg); err == nil {
						t.Fatal("Go config accepted non-numeric submit_sm_throughput")
					}
				default:
					t.Fatalf("unknown throughput marker %q", marker)
				}
				return
			}

			cfg := Config{
				CID: "fixture", Host: "localhost", Port: 2775, SystemID: "fixture",
				SubmitSMThroughput: &throughput,
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Go config rejected oracle-accepted throughput %v: %v", throughput, err)
			}
			if got := cfg.EffectiveSubmitSMThroughput(); tc.Expected.Config.Value == nil || got != *tc.Expected.Config.Value {
				t.Fatalf("config throughput = %v, want %v", got, tc.Expected.Config.Value)
			}
			if tc.Expected.WaitSeconds == nil {
				t.Fatal("numeric pacing case has no expected wait")
			}
			var elapsed time.Duration
			first := tc.Input.ElapsedSeconds == nil
			if !first {
				elapsed = time.Duration(*tc.Input.ElapsedSeconds * float64(time.Second))
			}
			clock := &fakePacingClock{now: time.Unix(100, 0).UTC()}
			pacer, err := newPacer(throughput, clock)
			if err != nil {
				t.Fatal(err)
			}
			if !first {
				pacer.last = clock.now.Add(-elapsed).Truncate(time.Microsecond)
			}
			if err := pacer.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			var got float64
			waits := clock.recordedWaits()
			if throughput > 0 {
				if len(waits) != 1 {
					t.Fatalf("production pacer wait count = %d, want 1", len(waits))
				}
				got = waits[0].Seconds()
			} else if len(waits) != 0 {
				t.Fatalf("disabled production pacer waits = %v, want none", waits)
			}
			if math.Abs(got-*tc.Expected.WaitSeconds) > 1e-9 {
				t.Fatalf("wait = %.9f, want %.9f", got, *tc.Expected.WaitSeconds)
			}
		})
	}
}
