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

// configOracleScript reads a config file with the real ConfigFile and returns
// the _get/_getint/_getfloat/_getbool results for a set of probes, so the Go
// port can be compared against the frozen semantics.
const configOracleScript = `
import json, sys, tempfile, os
from jasmin.config import ConfigFile

text = sys.stdin.read()
path = tempfile.mktemp(suffix=".cfg")
with open(path, "w") as fh:
    fh.write(text)
cf = ConfigFile(path)
out = {}
for section, option, kind, default in [
    ["amqp-broker", "host", "str", "localhost"],
    ["amqp-broker", "port", "int", 5672],
    ["amqp-broker", "spec", "str", "None-default"],
    ["dlr", "dlr_lookup_max_retries", "int", 2],
    ["dlr", "dlr_expiry", "int", 86400],
    ["smpp-server", "id", "str", "smpps_01"],
    ["sm-listener", "publish_submit_sm_resp", "bool", False],
    ["nonexistent", "missing", "str", "fallback"],
]:
    key = "%s.%s" % (section, option)
    if kind == "str":
        v = cf._get(section, option, default)
        out[key] = "" if v is None else str(v)
    elif kind == "int":
        out[key] = cf._getint(section, option, default)
    elif kind == "bool":
        out[key] = cf._getbool(section, option, default)
os.remove(path)
print(json.dumps(out))
`

func TestConfigFileDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	text := "[amqp-broker]\nhost = broker-a\nport = 5673\nspec = None\n" +
		"[dlr]\ndlr_lookup_max_retries = 5\ndlr_expiry = None\n" +
		"[smpp-server]\nid = smpps_prod\n" +
		"[sm-listener]\npublish_submit_sm_resp = yes\n"

	command := exec.CommandContext(ctx, pythonPath, "-c", configOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../..")
	command.Stdin = bytes.NewReader([]byte(text))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	file, err := config.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	// str probes
	strCases := map[string][2]string{
		"amqp-broker.host":    {"broker-a", "localhost"},
		"amqp-broker.spec":    {"", "None-default"}, // None -> ""
		"nonexistent.missing": {"fallback", "fallback"},
	}
	for key, want := range strCases {
		var section, option string
		splitKey(key, &section, &option)
		got := file.Get(section, option, want[1])
		if got != oracle[key].(string) || got != want[0] {
			t.Errorf("%s: go=%q oracle=%v", key, got, oracle[key])
		}
	}
	// int probes
	intCases := map[string][2]int{
		"amqp-broker.port":           {5673, 5672},
		"dlr.dlr_lookup_max_retries": {5, 2},
		"dlr.dlr_expiry":             {86400, 86400}, // None -> default
	}
	for key, want := range intCases {
		var section, option string
		splitKey(key, &section, &option)
		got, err := file.GetInt(section, option, want[1])
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if float64(got) != oracle[key].(float64) || got != want[0] {
			t.Errorf("%s: go=%d oracle=%v", key, got, oracle[key])
		}
	}
	// bool probe
	got, err := file.GetBool("sm-listener", "publish_submit_sm_resp", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != oracle["sm-listener.publish_submit_sm_resp"].(bool) || !got {
		t.Errorf("publish_submit_sm_resp: go=%v oracle=%v", got, oracle["sm-listener.publish_submit_sm_resp"])
	}
}

func splitKey(key string, section, option *string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			*section, *option = key[:i], key[i+1:]
			return
		}
	}
}
