package smppc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestSMPPStatusNameRangesAndNamed(t *testing.T) {
	cases := map[uint32]string{
		0x00:       "ESME_ROK",
		0x45:       "ESME_RSUBMITFAIL",
		0xff:       "ESME_RUNKNOWNERR",
		0x112:      "ESME_RINVBCASTCHANIND",
		0x09:       "RESERVEDSTATUS_UNKNOWN_STATUS", // gap below 0x100
		0x16:       "RESERVEDSTATUS_UNKNOWN_STATUS",
		0x113:      "RESERVEDSTATUS_SMPP_EXTENSION", // 0x100-0x3FF, unnamed
		0x3ff:      "RESERVEDSTATUS_SMPP_EXTENSION",
		0x400:      "RESERVEDSTATUS_VENDOR_SPECIFIC", // 0x400-0x4FF
		0x4ff:      "RESERVEDSTATUS_VENDOR_SPECIFIC",
		0x500:      "RESERVEDSTATUS", // 0x500+
		0xffffffff: "RESERVEDSTATUS",
	}
	for status, want := range cases {
		if got := smppStatusName(status); got != want {
			t.Errorf("smppStatusName(%#x) = %q, want %q", status, got, want)
		}
	}
}

// commandStatusOracleScript decodes a spread of command_status wire values with
// the real CommandStatusEncoder and returns their CommandStatus names.
const commandStatusOracleScript = `
import json, struct, io
from smpp.pdu.pdu_encoding import CommandStatusEncoder
enc = CommandStatusEncoder()
def name_of(v):
    return enc.decode(io.BytesIO(struct.pack('>I', v))).name
vals = list(range(0x00, 0x121)) + [0x1FF, 0x200, 0x3FF, 0x400, 0x401, 0x4FF, 0x500, 0x501, 0xFFFF, 0x10000, 0xFFFFFFFF]
print(json.dumps({str(v): name_of(v) for v in vals}))
`

func TestSMPPStatusNameDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, pythonPath, "-c", commandStatusOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if len(oracle) == 0 {
		t.Fatal("oracle produced no values")
	}
	for key, want := range oracle {
		value, err := strconv.ParseUint(key, 10, 32)
		if err != nil {
			t.Fatalf("bad oracle key %q: %v", key, err)
		}
		if got := smppStatusName(uint32(value)); got != want {
			t.Errorf("smppStatusName(%#x) go=%q oracle=%q", value, got, want)
		}
	}
}
