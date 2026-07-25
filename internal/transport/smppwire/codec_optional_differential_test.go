package smppwire_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// optionalEncoderScript builds a deliver_sm with the requested standard
// optional params, encodes it through the frozen patched encoder, and prints
// the full frame hex. The Go codec must decode and re-encode that frame
// byte-identically.
const optionalEncoderScript = `
import binascii, json, sys
import jasmin.protocols.smpp.operations  # installs the encoder/decoder patches
from smpp.pdu.pdu_encoding import PDUEncoder
from smpp.pdu.operations import DeliverSM
from smpp.pdu import pdu_types

req = json.load(sys.stdin)
pdu = DeliverSM(seqNum=1, source_addr="1111", destination_addr="2222", short_message=b"hi")
p = req["params"]
if "user_message_reference" in p: pdu.params["user_message_reference"] = p["user_message_reference"]
if "source_port" in p: pdu.params["source_port"] = p["source_port"]
if "destination_port" in p: pdu.params["destination_port"] = p["destination_port"]
if "sar_msg_ref_num" in p: pdu.params["sar_msg_ref_num"] = p["sar_msg_ref_num"]
if "sar_total_segments" in p: pdu.params["sar_total_segments"] = p["sar_total_segments"]
if "sar_segment_seqnum" in p: pdu.params["sar_segment_seqnum"] = p["sar_segment_seqnum"]
if "privacy_indicator" in p: pdu.params["privacy_indicator"] = getattr(pdu_types.PrivacyIndicator, p["privacy_indicator"])
if "payload_type" in p: pdu.params["payload_type"] = getattr(pdu_types.PayloadType, p["payload_type"])
if "message_payload" in p: pdu.params["message_payload"] = binascii.unhexlify(p["message_payload"])
if "language_indicator" in p: pdu.params["language_indicator"] = getattr(pdu_types.LanguageIndicator, p["language_indicator"])
if "network_error_code" in p: pdu.params["network_error_code"] = binascii.unhexlify(p["network_error_code"])
if "message_state" in p: pdu.params["message_state"] = getattr(pdu_types.MessageState, p["message_state"])
if "receipted_message_id" in p: pdu.params["receipted_message_id"] = p["receipted_message_id"]

print(binascii.hexlify(PDUEncoder().encode(pdu)).decode())
`

func TestOptionalReEmissionRoundTripsLegacyEncoder(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name    string
		request map[string]any
	}{
		{
			// message_payload is excluded: the frozen codec preserves its
			// decode-success/re-encode-reject boundary (KNOWN_QUIRKS Q-015).
			name: "sar and receipt optionals",
			request: map[string]any{"params": map[string]any{
				"sar_msg_ref_num": 0x1234, "sar_total_segments": 3, "sar_segment_seqnum": 2,
				"message_state": "DELIVERED", "receipted_message_id": "abc12",
			}},
		},
		{
			name: "extended standard optionals",
			request: map[string]any{"params": map[string]any{
				"user_message_reference": 0x0FA0, "source_port": 0x1F90, "destination_port": 0x270F,
				"privacy_indicator": "CONFIDENTIAL", "payload_type": "WCMP",
				"language_indicator": "FRENCH", "network_error_code": "030001",
			}},
		},
		{
			name: "many standard optionals interleaved",
			request: map[string]any{
				"params": map[string]any{
					"source_port": 0x1F90, "sar_msg_ref_num": 0x1234, "sar_total_segments": 2,
					"sar_segment_seqnum": 1, "message_state": "DELIVERED",
					"network_error_code": "030001", "receipted_message_id": "id-9",
				},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := json.Marshal(testCase.request)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, pythonPath, "-c", optionalEncoderScript)
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			command.Stdin = bytes.NewReader(body)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, output)
			}
			legacyFrame, err := hex.DecodeString(string(bytes.TrimSpace(output)))
			if err != nil {
				t.Fatalf("oracle hex %q: %v", output, err)
			}

			pdu, err := smppwire.Decode(legacyFrame)
			if err != nil {
				t.Fatalf("Go decode of legacy frame: %v", err)
			}
			reencoded, err := smppwire.Encode(pdu)
			if err != nil {
				t.Fatalf("Go re-encode: %v", err)
			}
			if !bytes.Equal(reencoded, legacyFrame) {
				t.Fatalf("round trip diverges:\n  go %s\n  py %s",
					hex.EncodeToString(reencoded), hex.EncodeToString(legacyFrame))
			}
		})
	}
}
