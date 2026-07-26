package picklecompat_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// relativeTimeOracleScript builds submit_sm PDUs carrying an SMPP relative time
// (SMPPRelativeTime, the normal form an ESME sends over a bind) in the
// validity_period and schedule_delivery_time params, pickles them protocol 2,
// and records the wire bytes the legacy client would send (TimeEncoder output
// minus its trailing NUL, exactly what the bridge's _time_bytes returns). An
// absolute datetime case guards that the fix does not disturb the already-working
// datetime path.
const relativeTimeOracleScript = `
import json, sys, pickle, base64, io, contextlib
from smpp.pdu import smpp_time
from smpp.pdu.pdu_encoding import TimeEncoder
from jasmin.protocols.smpp.operations import SMPPOperationFactory

factory = SMPPOperationFactory()

def wire(t):
    return TimeEncoder().encode(t, 'x')[:-1].decode('ascii')

# smpp_time.parse prints the format char to stdout; suppress it so only the
# JSON payload reaches stdout.
with contextlib.redirect_stdout(io.StringIO()):
    rel = smpp_time.parse('000000000600000R')
    absolute = smpp_time.parse('251231235959000+')
cases = []
p = factory.SubmitSM(source_addr=b'1111', destination_addr=b'2222', short_message=b'hi', validity_period=rel)
cases.append({"name": "relative_validity", "pickle": base64.b64encode(pickle.dumps(p, 2)).decode(),
              "validity": wire(rel), "schedule": ""})
p = factory.SubmitSM(source_addr=b'1111', destination_addr=b'2222', short_message=b'hi', schedule_delivery_time=rel)
cases.append({"name": "relative_schedule", "pickle": base64.b64encode(pickle.dumps(p, 2)).decode(),
              "validity": "", "schedule": wire(rel)})
p = factory.SubmitSM(source_addr=b'1111', destination_addr=b'2222', short_message=b'hi', validity_period=absolute)
cases.append({"name": "absolute_validity", "pickle": base64.b64encode(pickle.dumps(p, 2)).decode(),
              "validity": wire(absolute), "schedule": ""})
print(json.dumps(cases))
`

// TestDecodeSubmitSMRelativeTime proves GAP 5: a submit_sm whose validity_period
// or schedule_delivery_time is an SMPP relative time pickles an
// smpp.pdu.smpp_time.SMPPRelativeTime global, which the restricted unpickler
// rejected ("forbidden global") — poisoning every relative-time submit (the
// normal SMPP-origin form). After allowlisting it, the bridge projects the same
// 16-byte wire the legacy TimeEncoder produces. Absolute datetimes were already
// allowlisted and must keep working.
func TestDecodeSubmitSMRelativeTime(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, pythonPath, "-c", relativeTimeOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var cases []struct {
		Name     string `json:"name"`
		Pickle   string `json:"pickle"`
		Validity string `json:"validity"`
		Schedule string `json:"schedule"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &cases); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if len(cases) == 0 {
		t.Fatal("oracle produced no cases")
	}

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	for _, testCase := range cases {
		pickled, err := base64.StdEncoding.DecodeString(testCase.Pickle)
		if err != nil {
			t.Fatalf("%s: bad pickle: %v", testCase.Name, err)
		}
		body, _, err := bridge.DecodeSubmitSM(ctx, pickled)
		if err != nil {
			t.Errorf("%s: decode failed (GAP 5): %v", testCase.Name, err)
			continue
		}
		if string(body.ValidityPeriod) != testCase.Validity {
			t.Errorf("%s: validity_period go=%q oracle=%q", testCase.Name, body.ValidityPeriod, testCase.Validity)
		}
		if string(body.ScheduleDeliveryTime) != testCase.Schedule {
			t.Errorf("%s: schedule_delivery_time go=%q oracle=%q", testCase.Name, body.ScheduleDeliveryTime, testCase.Schedule)
		}
	}
}
