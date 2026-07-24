package smppc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// oracleScript replays the legacy submit-time sequence on an envelope pickle:
// resolve connector types, validate rules, then wire-encode with the fork's
// patches installed. It prints the full PDU hex and the PDU hex without the
// custom section, so the differential isolates the vendor-TLV bytes.
const oracleScript = `
import base64, json, pickle, sys
import jasmin.protocols.smpp.operations  # installs the encoder/decoder patches
from jasmin.tools.tlv_encoder import resolve_tlv_types, validate_custom_tlvs
from smpp.pdu.pdu_encoding import PDUEncoder

request = json.load(sys.stdin)
pdu = pickle.loads(base64.b64decode(request["pickle"]))
rules = request["rules"]
pdu.custom_tlvs = resolve_tlv_types(getattr(pdu, "custom_tlvs", []) or [], rules)
ok, err = validate_custom_tlvs(pdu.custom_tlvs, rules)
if not ok:
    print(json.dumps({"error": err}))
    sys.exit(0)
full = PDUEncoder().encode(pdu)
pdu.custom_tlvs = []
base = PDUEncoder().encode(pdu)
print(json.dumps({"full": full.hex(), "base": base.hex()}))
`

func TestVendorTLVWireDifferentialAgainstLegacyEncoder(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	length32 := 32
	cases := []struct {
		name   string
		tuples []picklecompat.SubmitSMCustomTLV
		rules  []CustomTLVRule
	}{
		{
			name: "untyped 19-digit value resolved to Int8 by rules",
			tuples: []picklecompat.SubmitSMCustomTLV{
				{Tag: big.NewInt(0x1400), Value: "1707167205648943173"},
			},
			rules: []CustomTLVRule{{Tag: 0x1400, Type: tlv.TypeInt8, Required: true}},
		},
		{
			name: "typed octet hint plus rule-typed integer value",
			tuples: []picklecompat.SubmitSMCustomTLV{
				{Tag: big.NewInt(0x1401), Type: tlv.TypeOctetString, Value: "1401778070000018542"},
				{Tag: big.NewInt(0x1402), Value: int64(123)},
			},
			rules: []CustomTLVRule{{Tag: 0x1402, Type: tlv.TypeInt2}},
		},
		{
			name: "untyped without rules defaults to octets",
			tuples: []picklecompat.SubmitSMCustomTLV{
				{Tag: big.NewInt(0x1500), Value: "hello"},
			},
		},
		{
			name: "COctetString rule with bounded length",
			tuples: []picklecompat.SubmitSMCustomTLV{
				{Tag: big.NewInt(0x1401), Value: "ascii-id"},
			},
			rules: []CustomTLVRule{{Tag: 0x1401, Type: tlv.TypeCOctetString, Length: &length32}},
		},
		{
			name: "mixed batch reorders Int8 entries to the tail like the patch",
			tuples: []picklecompat.SubmitSMCustomTLV{
				{Tag: big.NewInt(0x1400), Value: "7"},
				{Tag: big.NewInt(0x1500), Value: "b"},
				{Tag: big.NewInt(0x1401), Value: int64(1)},
			},
			rules: []CustomTLVRule{
				{Tag: 0x1400, Type: tlv.TypeInt8},
				{Tag: 0x1401, Type: tlv.TypeInt1},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := bridge.EncodeSubmitSM(ctx, picklecompat.SubmitSMEncodeRequest{
				Sequence:        9,
				SourceAddr:      picklecompat.Bytes("1111"),
				DestinationAddr: picklecompat.Bytes("2222"),
				ShortMessage:    picklecompat.Bytes("hello"),
				CustomTLVs:      testCase.tuples,
				IncludeBill:     false,
			})
			if err != nil {
				t.Fatal(err)
			}

			// Legacy path: resolve + validate + patched wire encoder.
			cfg := Config{CustomTLVs: testCase.rules}
			rulesJSON := make([]map[string]any, 0, len(testCase.rules))
			for _, rule := range testCase.rules {
				var ruleLength any
				if rule.Length != nil {
					ruleLength = *rule.Length
				}
				rulesJSON = append(rulesJSON, map[string]any{
					"tag": rule.Tag, "type": rule.Type, "length": ruleLength, "required": rule.Required,
				})
			}
			request, err := json.Marshal(map[string]any{
				"pickle": base64.StdEncoding.EncodeToString(encoded.Body),
				"rules":  rulesJSON,
			})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, pythonPath, "-c", oracleScript)
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			command.Stdin = bytes.NewReader(request)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, output)
			}
			var oracle struct {
				Full  string `json:"full"`
				Base  string `json:"base"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
				t.Fatalf("oracle output %q: %v", output, err)
			}
			if oracle.Error != "" {
				t.Fatalf("oracle validation rejected: %s", oracle.Error)
			}
			// Compare bodies: the 16-byte header carries command_length, which
			// differs between the two encodes.
			const headerHex = 32
			if len(oracle.Full) < headerHex || len(oracle.Base) < headerHex {
				t.Fatalf("oracle PDUs too short:\nfull %s\nbase %s", oracle.Full, oracle.Base)
			}
			fullBody, baseBody := oracle.Full[headerHex:], oracle.Base[headerHex:]
			if len(fullBody) < len(baseBody) || fullBody[:len(baseBody)] != baseBody {
				t.Fatalf("oracle full body is not base + custom section:\nfull %s\nbase %s", fullBody, baseBody)
			}
			oracleSection := fullBody[len(baseBody):]

			// Go path: the session's decode + resolve/validate/wire sequence.
			body, tuples, err := bridge.DecodeSubmitSM(ctx, encoded.Body)
			if err != nil {
				t.Fatal(err)
			}
			section, err := prepareVendorTLVs(tuples, cfg.ConnectorTLVRules())
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(section); got != oracleSection {
				t.Fatalf("vendor section diverges:\n  go %s\n  py %s", got, oracleSection)
			}
			body.VendorTLVs = section
			goFull, err := smppwire.Encode(smppwire.PDU{
				Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: 9},
				SM:     &body,
			})
			if err != nil {
				t.Fatal(err)
			}
			goHex := hex.EncodeToString(goFull)
			if len(goHex) < len(oracleSection) || goHex[len(goHex)-len(oracleSection):] != oracleSection {
				t.Fatalf("Go frame does not end with the oracle section:\n  go %s\n  py section %s", goHex, oracleSection)
			}
			if goHex == oracle.Full {
				t.Logf("full PDU parity: byte-identical to the legacy encoder")
			} else {
				t.Logf("full PDU differs outside the custom section (pre-existing body formatting):\n  go %s\n  py %s", goHex, oracle.Full)
			}
		})
	}
}
