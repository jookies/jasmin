package smppc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestNewDLRSubmitRespPublicationROK(t *testing.T) {
	envelope, err := newDLRSubmitRespPublication("msg-1", "ESME_ROK", "000ABC123")
	if err != nil {
		t.Fatal(err)
	}
	if envelope.RoutingKey() != "dlr.submit_sm_resp" {
		t.Fatalf("routing key = %q", envelope.RoutingKey())
	}
	if string(envelope.Body()) != "ESME_ROK" {
		t.Fatalf("body = %q", envelope.Body())
	}
	if envelope.Properties().MessageID() != "msg-1" {
		t.Fatalf("message-id = %q", envelope.Properties().MessageID())
	}
	headers := envelope.Properties().Headers()
	if typ, ok := headers["type"].String(); !ok || typ != "submit_sm_resp" {
		t.Fatalf("type header = (%q, %v)", typ, ok)
	}
	// upper-cased, leading zeros stripped.
	if id, ok := headers["smpp_msgid"].String(); !ok || id != "ABC123" {
		t.Fatalf("smpp_msgid header = (%q, %v)", id, ok)
	}
}

func TestNewDLRSubmitRespPublicationError(t *testing.T) {
	envelope, err := newDLRSubmitRespPublication("msg-2", "ESME_RSUBMITFAIL", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(envelope.Body()) != "ESME_RSUBMITFAIL" || envelope.Properties().MessageID() != "msg-2" {
		t.Fatalf("body/msgid = %q / %q", envelope.Body(), envelope.Properties().MessageID())
	}
	headers := envelope.Properties().Headers()
	if _, ok := headers["smpp_msgid"]; ok {
		t.Fatal("non-ROK response must not carry smpp_msgid")
	}
	if typ, ok := headers["type"].String(); !ok || typ != "submit_sm_resp" {
		t.Fatalf("type header = (%q, %v)", typ, ok)
	}
}

func TestNewDLRSubmitRespPublicationROKWithoutSMSCIDErrors(t *testing.T) {
	if _, err := newDLRSubmitRespPublication("msg-3", "ESME_ROK", ""); !errors.Is(err, ErrInvalidSubmitResponsePublication) {
		t.Fatalf("error = %v, want ErrInvalidSubmitResponsePublication", err)
	}
}

func TestNormalizeSMPPMsgID(t *testing.T) {
	cases := map[string]string{"000ABC": "ABC", "abc": "ABC", "0000": "", "12": "12", "0f0": "F0"}
	for in, want := range cases {
		if got := normalizeSMPPMsgID(in); got != want {
			t.Errorf("normalizeSMPPMsgID(%q) = %q, want %q", in, got, want)
		}
	}
}

const dlrContentOracleScript = `
import json
# jasmin.managers.content does plugin discovery via the Python 3.10+
# entry_points(group=...) API at import time; backport it for older
# interpreters (no jasmin.content plugins are installed under test, so the
# group query is empty).
import importlib.metadata as _ilm
_orig_entry_points = _ilm.entry_points
def _entry_points_compat(*args, **kwargs):
    if 'group' in kwargs:
        return []
    return _orig_entry_points(*args, **kwargs)
_ilm.entry_points = _entry_points_compat

from jasmin.managers.content import DLR
from smpp.pdu.pdu_types import CommandId, CommandStatus

def dump(d):
    body = d.body
    if isinstance(body, bytes):
        body = body.decode()
    headers = d.properties['headers']
    return {"body": body, "message_id": str(d.properties['message-id']),
            "type": headers['type'], "smpp_msgid": headers.get('smpp_msgid')}

ok = DLR(pdu_type=CommandId.submit_sm_resp, msgid='msg-1', status=CommandStatus.ESME_ROK, smpp_msgid=b'000ABC123')
err = DLR(pdu_type=CommandId.submit_sm_resp, msgid='msg-2', status=CommandStatus.ESME_RSUBMITFAIL)
print(json.dumps({"ok": dump(ok), "err": dump(err)}))
`

// TestDLRSubmitRespDifferentialAgainstLegacy proves the Go DLR envelope matches
// the legacy managers/content.py DLR for both ROK (with smpp_msgid) and error.
func TestDLRSubmitRespDifferentialAgainstLegacy(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", dlrContentOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	type content struct {
		Body      string  `json:"body"`
		MessageID string  `json:"message_id"`
		Type      string  `json:"type"`
		SMPPMsgID *string `json:"smpp_msgid"`
	}
	var oracle struct {
		OK  content `json:"ok"`
		Err content `json:"err"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}

	assertMatch := func(name string, msgID, status, smsc string, want content) {
		t.Helper()
		envelope, err := newDLRSubmitRespPublication(msgID, status, smsc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		headers := envelope.Properties().Headers()
		typ, _ := headers["type"].String()
		gotID, hasID := headers["smpp_msgid"].String()
		if string(envelope.Body()) != want.Body || envelope.Properties().MessageID() != want.MessageID || typ != want.Type {
			t.Errorf("%s: go(body=%q msgid=%q type=%q) oracle(%q %q %q)", name,
				envelope.Body(), envelope.Properties().MessageID(), typ, want.Body, want.MessageID, want.Type)
		}
		switch {
		case want.SMPPMsgID == nil && hasID:
			t.Errorf("%s: unexpected smpp_msgid %q (oracle omits it)", name, gotID)
		case want.SMPPMsgID != nil && (!hasID || gotID != *want.SMPPMsgID):
			t.Errorf("%s: smpp_msgid go=(%q,%v) oracle=%q", name, gotID, hasID, *want.SMPPMsgID)
		}
	}
	assertMatch("rok", "msg-1", "ESME_ROK", "000ABC123", oracle.OK)
	assertMatch("err", "msg-2", "ESME_RSUBMITFAIL", "", oracle.Err)
}
