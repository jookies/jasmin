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
