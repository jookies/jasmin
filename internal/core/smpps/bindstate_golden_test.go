package smpps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type bindStateFixture struct {
	Cases []struct {
		ID    string `json:"id"`
		Input struct {
			State      string `json:"state"`
			CommandID  uint32 `json:"command_id"`
			Command    string `json:"command"`
			EntryPoint string `json:"entry_point"`
		} `json:"input"`
		Expected struct {
			Action        string `json:"action"`
			CommandStatus uint32 `json:"command_status"`
			Status        string `json:"status"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestLegacyBindStateGolden(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "smpps-bind-state", "baseline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture bindStateFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("bind-state fixture has no cases")
	}

	states := map[string]SessionState{
		"OPEN": StateOpen, "BOUND_RX": StateBoundRX, "BOUND_TX": StateBoundTX,
		"BOUND_TRX": StateBoundTRX, "UNBOUND": StateUnbound,
	}
	seen := make(map[string]struct{}, len(fixture.Cases))
	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			if _, duplicate := seen[tc.ID]; duplicate {
				t.Fatalf("duplicate fixture id %q", tc.ID)
			}
			seen[tc.ID] = struct{}{}
			state, ok := states[tc.Input.State]
			if !ok {
				t.Fatalf("unknown fixture state %q", tc.Input.State)
			}
			allowed, status := CommandAllowed(state, tc.Input.CommandID)
			wantAllowed := tc.Expected.Action == "delegate"
			if tc.Expected.Action != "delegate" && tc.Expected.Action != "reject" {
				t.Fatalf("unknown fixture action %q", tc.Expected.Action)
			}
			if allowed != wantAllowed || status != tc.Expected.CommandStatus {
				t.Fatalf("CommandAllowed(%s, %s/%#x) = (%v,%#x), want (%v,%#x; %s)",
					state, tc.Input.Command, tc.Input.CommandID, allowed, status,
					wantAllowed, tc.Expected.CommandStatus, tc.Expected.Status)
			}
		})
	}
	if len(seen) != len(fixture.Cases) {
		t.Fatalf("executed %d/%d fixture cases", len(seen), len(fixture.Cases))
	}
}
