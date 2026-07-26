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

// chainOracleScript builds a long message via SMPPOperationFactory (default SAR
// split) — the nextPdu chain the legacy client sends part by part — and dumps
// the pickle plus each part's SAR fields and short_message.
const chainOracleScript = `
import json, pickle, base64
from jasmin.protocols.smpp.operations import SMPPOperationFactory
f = SMPPOperationFactory()
pdu = f.SubmitSM(source_addr=b'1111', destination_addr=b'2222', short_message=b'A'*300)
parts = []
node = pdu
while node is not None:
    p = node.params
    parts.append({
        "sar_ref": p.get('sar_msg_ref_num'),
        "sar_total": p.get('sar_total_segments'),
        "sar_seq": p.get('sar_segment_seqnum'),
        "short_message": base64.b64encode(p['short_message']).decode(),
    })
    node = getattr(node, 'nextPdu', None)
print(json.dumps({"pickle": base64.b64encode(pickle.dumps(pdu, 2)).decode(), "parts": parts}))
`

// TestDecodeSubmitSMChainDifferential proves the bridge projects the full nextPdu
// chain the legacy client would send: same part count, per-part SAR ref/total/seq
// and short_message bytes.
func TestDecodeSubmitSMChainDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, pythonPath, "-c", chainOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle struct {
		Pickle string `json:"pickle"`
		Parts  []struct {
			SARRef       uint16 `json:"sar_ref"`
			SARTotal     byte   `json:"sar_total"`
			SARSeq       byte   `json:"sar_seq"`
			ShortMessage string `json:"short_message"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if len(oracle.Parts) < 2 {
		t.Fatalf("expected a multipart chain, got %d parts", len(oracle.Parts))
	}
	pickled, err := base64.StdEncoding.DecodeString(oracle.Pickle)
	if err != nil {
		t.Fatal(err)
	}

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	parts, err := bridge.DecodeSubmitSMChain(ctx, pickled)
	if err != nil {
		t.Fatalf("DecodeSubmitSMChain: %v", err)
	}
	if len(parts) != len(oracle.Parts) {
		t.Fatalf("part count go=%d oracle=%d", len(parts), len(oracle.Parts))
	}
	for i := range parts {
		optional := parts[i].Body.Optional
		want := oracle.Parts[i]
		if optional.SARMessageReference == nil || *optional.SARMessageReference != want.SARRef ||
			optional.SARTotalSegments == nil || *optional.SARTotalSegments != want.SARTotal ||
			optional.SARSegmentSequence == nil || *optional.SARSegmentSequence != want.SARSeq {
			t.Errorf("part %d SAR go=%+v oracle=(ref=%d total=%d seq=%d)", i+1, optional, want.SARRef, want.SARTotal, want.SARSeq)
		}
		wantMessage, _ := base64.StdEncoding.DecodeString(want.ShortMessage)
		if !bytes.Equal(parts[i].Body.ShortMessage, wantMessage) {
			t.Errorf("part %d short_message go=%x oracle=%x", i+1, parts[i].Body.ShortMessage, wantMessage)
		}
	}
}

// udhChainOracleScript builds a long message in UDH split mode; each part carries
// a more_messages_to_send (0x0426) whose 1-byte wire value the bridge must
// project (MORE_MESSAGES -> 1 on non-final parts, NO_MORE_MESSAGES -> 0 last).
const udhChainOracleScript = `
import json, pickle, base64
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from smpp.pdu.pdu_encoding import MoreMessagesToSendEncoder
f = SMPPOperationFactory()
f.long_content_split = 'udh'
pdu = f.SubmitSM(source_addr=b'1111', destination_addr=b'2222', short_message=b'A'*300)
parts = []
node = pdu
while node is not None:
    mm = node.params.get('more_messages_to_send')
    parts.append({"more": MoreMessagesToSendEncoder().encode(mm)[0] if mm is not None else None})
    node = getattr(node, 'nextPdu', None)
print(json.dumps({"pickle": base64.b64encode(pickle.dumps(pdu, 2)).decode(), "parts": parts}))
`

// TestDecodeSubmitSMChainDifferentialUDH proves the bridge projects the
// more_messages_to_send hint per UDH part with the exact legacy wire byte.
func TestDecodeSubmitSMChainDifferentialUDH(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, pythonPath, "-c", udhChainOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var oracle struct {
		Pickle string `json:"pickle"`
		Parts  []struct {
			More *byte `json:"more"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if len(oracle.Parts) < 2 {
		t.Fatalf("expected a UDH multipart chain, got %d parts", len(oracle.Parts))
	}
	pickled, err := base64.StdEncoding.DecodeString(oracle.Pickle)
	if err != nil {
		t.Fatal(err)
	}

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	parts, err := bridge.DecodeSubmitSMChain(ctx, pickled)
	if err != nil {
		t.Fatalf("DecodeSubmitSMChain: %v", err)
	}
	if len(parts) != len(oracle.Parts) {
		t.Fatalf("part count go=%d oracle=%d", len(parts), len(oracle.Parts))
	}
	for i := range parts {
		got := parts[i].Body.Optional.MoreMessagesToSend
		want := oracle.Parts[i].More
		switch {
		case want == nil && got != nil:
			t.Errorf("part %d has unexpected more_messages_to_send %d", i+1, *got)
		case want != nil && (got == nil || *got != *want):
			t.Errorf("part %d more_messages_to_send go=%v oracle=%d", i+1, got, *want)
		}
	}
}
