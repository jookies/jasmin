package config_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/config"
)

// throwerOracleScript builds the real deliverSmThrowerConfig ('deliversm-thrower')
// and DLRThrowerConfig ('dlr-thrower') and dumps the retry/timeout policy the Go
// parsers reproduce. Note the INI key is http_timeout but the attribute is .timeout.
const throwerOracleScript = `
import json, sys, tempfile, os
from jasmin.routing.configs import deliverSmThrowerConfig, DLRThrowerConfig
text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
open(path, "w").write(text)
d = deliverSmThrowerConfig(path)
r = DLRThrowerConfig(path)
os.remove(path)
print(json.dumps({
    "deliversm": {"timeout": d.timeout, "retry_delay": d.retry_delay, "max_retries": d.max_retries},
    "dlr": {"timeout": r.timeout, "retry_delay": r.retry_delay, "max_retries": r.max_retries,
            "dlr_pdu": r.dlr_pdu},
}))
`

func TestThrowerSectionsDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Mix overridden and defaulted fields so both paths are checked at once.
	text := "[deliversm-thrower]\nhttp_timeout = 12\nmax_retries = 9\n" +
		"[dlr-thrower]\nretry_delay = 45\n"

	command := exec.CommandContext(ctx, pythonPath, "-c", throwerOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..")
	command.Stdin = bytes.NewReader([]byte(text))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle struct {
		DeliverSM struct {
			Timeout    int `json:"timeout"`
			RetryDelay int `json:"retry_delay"`
			MaxRetries int `json:"max_retries"`
		} `json:"deliversm"`
		DLR struct {
			Timeout    int    `json:"timeout"`
			RetryDelay int    `json:"retry_delay"`
			MaxRetries int    `json:"max_retries"`
			DLRPDU     string `json:"dlr_pdu"`
		} `json:"dlr"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := config.LoadDeliverSMThrower(file)
	if err != nil {
		t.Fatal(err)
	}
	if ds.TimeoutSecs != oracle.DeliverSM.Timeout || ds.RetryDelaySecs != oracle.DeliverSM.RetryDelay ||
		ds.MaxRetries != oracle.DeliverSM.MaxRetries {
		t.Fatalf("deliversm-thrower diverges:\n  go %+v\n  py %+v", ds, oracle.DeliverSM)
	}
	dlr, err := config.LoadDLRThrower(file)
	if err != nil {
		t.Fatal(err)
	}
	if dlr.TimeoutSecs != oracle.DLR.Timeout || dlr.RetryDelaySecs != oracle.DLR.RetryDelay ||
		dlr.MaxRetries != oracle.DLR.MaxRetries || dlr.DLRPDU != oracle.DLR.DLRPDU {
		t.Fatalf("dlr-thrower diverges:\n  go %+v\n  py %+v", dlr, oracle.DLR)
	}
}
