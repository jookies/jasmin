package mo

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"encoding/json"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
	"strings"
)

// oracleScript replays the legacy inbound sequence on raw deliver_sm wire
// bytes: patched decode (vendor capture), the listener's resolve passthrough,
// then the deliverSmThrower's custom_tlvs_data formatting.
const oracleScript = `
import binascii, io, json, sys
import jasmin.protocols.smpp.operations  # installs the decoder patch
from jasmin.tools.tlv_encoder import resolve_tlv_types
from smpp.pdu.pdu_encoding import PDUEncoder

wire = binascii.unhexlify(sys.stdin.read().strip())
pdu = PDUEncoder().decode(io.BytesIO(wire))
tlvs = resolve_tlv_types(getattr(pdu, "custom_tlvs", []) or [], [])
custom_tlvs_data = []
for tlv in tlvs:
    if len(tlv) >= 4:
        tag, length, value_type, value = tlv[0], tlv[1], tlv[2], tlv[3]
        if isinstance(value, bytes):
            value = binascii.hexlify(value).decode()
        custom_tlvs_data.append({"tag": tag, "length": length, "type": value_type, "value": value})
print(json.dumps(custom_tlvs_data))
`

func TestVendorTLVCaptureDifferentialAgainstLegacyDecoder(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name    string
		tlvHex  string
		wantLen int
	}{
		{"single vendor tlv", "1401000474657374", 1},
		{"order and duplicates", "15000001621401000474657374140100026869", 3},
		{"standard tag excluded from capture", "020a00020fa01401000161", 1},
		{"nul and zero-length values", "140100046162630014020000", 2},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			frame := deliverFrameWithSection(t, testCase.tlvHex)

			// Legacy: patched decode + resolve passthrough + thrower formatting.
			command := exec.CommandContext(ctx, pythonPath, "-c", oracleScript)
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			command.Stdin = bytes.NewReader([]byte(hex.EncodeToString(frame)))
			output, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, output)
			}
			oracleJSON := string(bytes.TrimSpace(output))

			// Go: smppwire capture + the MO adapter's custom_tlvs projection.
			pdu, err := smppwire.Decode(frame)
			if err != nil {
				t.Fatal(err)
			}
			if len(pdu.SM.CapturedVendorTLVs) != testCase.wantLen {
				t.Fatalf("captured %d TLVs, want %d: %+v",
					len(pdu.SM.CapturedVendorTLVs), testCase.wantLen, pdu.SM.CapturedVendorTLVs)
			}
			delivery, err := DeliveryFromDeliverSM(pdu.SM, "msg-1", "smppc-a", "http://cb", "POST")
			if err != nil {
				t.Fatal(err)
			}
			goJSON := encodeCustomTLVs(delivery.CustomTLVs)
			if goJSON != oracleJSON {
				t.Fatalf("custom_tlvs JSON diverges:\n  go %s\n  py %s", goJSON, oracleJSON)
			}
		})
	}
}

func deliverFrameWithSection(t *testing.T, tlvHex string) []byte {
	t.Helper()
	body := &smppwire.SMBody{
		SourceAddress:      []byte("1111"),
		DestinationAddress: []byte("2222"),
		ShortMessage:       []byte("hello"),
	}
	frame, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 5},
		SM:     body,
	})
	if err != nil {
		t.Fatal(err)
	}
	section, err := hex.DecodeString(tlvHex)
	if err != nil {
		t.Fatal(err)
	}
	frame = append(frame, section...)
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(frame)))
	return frame
}

func TestDeliveryFromDeliverSMPopulatesCapturedTLVs(t *testing.T) {
	sm := &smppwire.SMBody{
		SourceAddress:      []byte("1111"),
		DestinationAddress: []byte("2222"),
		ShortMessage:       []byte("hi"),
		CapturedVendorTLVs: []smppwire.CapturedVendorTLV{
			{Tag: 0x1401, Value: []byte("test")},
			{Tag: 0x1401, Value: []byte{0xab, 0x00}},
		},
	}
	delivery, err := DeliveryFromDeliverSM(sm, "m", "c", "http://cb", "POST")
	if err != nil {
		t.Fatal(err)
	}
	want := []CustomTLV{
		{Tag: 0x1401, Length: 4, Type: "OctetString", Value: "74657374"},
		{Tag: 0x1401, Length: 2, Type: "OctetString", Value: "ab00"},
	}
	if len(delivery.CustomTLVs) != 2 || delivery.CustomTLVs[0] != want[0] || delivery.CustomTLVs[1] != want[1] {
		t.Fatalf("custom TLVs = %+v, want %+v", delivery.CustomTLVs, want)
	}
}

// tlvParamsOracleScript replays the thrower's tlv_params formatting on raw
// deliver_sm wire bytes through the legacy patched decoder.
const tlvParamsOracleScript = `
import binascii, io, json, sys
from enum import Enum
import jasmin.protocols.smpp.operations  # installs patches
from smpp.pdu.pdu_encoding import PDUEncoder

standard_optional_params = [
    "user_message_reference", "source_port", "destination_port",
    "sar_msg_ref_num", "sar_total_segments", "sar_segment_seqnum",
    "payload_type", "privacy_indicator", "callback_num",
    "language_indicator", "its_session_info", "network_error_code",
    "message_state", "receipted_message_id",
]

wire = binascii.unhexlify(sys.stdin.read().strip())
try:
    pdu = PDUEncoder().decode(io.BytesIO(wire))
except Exception as e:
    print(json.dumps({"error": type(e).__name__}))
    sys.exit(0)
tlv_params = {}
for name in standard_optional_params:
    if name in pdu.params and pdu.params[name] is not None:
        value = pdu.params[name]
        if isinstance(value, bytes):
            tlv_params[name] = binascii.hexlify(value).decode()
        elif isinstance(value, Enum):
            tlv_params[name] = value.name
        else:
            tlv_params[name] = str(value)
print(json.dumps({"tlv_params": json.dumps(tlv_params)}))
`

func TestTLVParamsDifferentialAgainstLegacyDecoder(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name          string
		tlvHex        string
		wantError     bool
		wantSpecError bool
	}{
		{name: "integer ports and references", tlvHex: "020400020fa0020a00021f90020b0002270f"},
		{name: "sar triplet", tlvHex: "020c00021234020e000103020f000102"},
		{name: "enum params", tlvHex: "00190001010201000103020d000102"},
		{name: "message state and receipt", tlvHex: "042700010200 1e00066162633132 00"},
		{name: "callback number ascii digits", tlvHex: "038100080100013132333435"},
		{name: "callback number binary digits", tlvHex: "038100070100013100ff32"},
		{name: "network error code", tlvHex: "04230003030001"},
		// The Jasmin reference accepts an empty value, but SMPP 3.4 defines
		// network_error_code as exactly three octets.
		{name: "empty network error code rejects per SMPP 3.4", tlvHex: "04230000", wantSpecError: true},
		{name: "everything combined with vendor tlv", tlvHex: "020400020fa000190001000427000102038100080102063132333435140100026869"},
		{name: "class-b tag rejects", tlvHex: "138300020a01", wantError: true},
		{name: "unknown enum byte rejects", tlvHex: "00190001ff", wantError: true},
		{name: "oversize receipted id rejects", tlvHex: "001e0042" + strings.Repeat("61", 65) + "00", wantError: true},
		{name: "bad callback npi rejects", tlvHex: "038100080100023132333435", wantError: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			frame := deliverFrameWithSection(t, strings.ReplaceAll(testCase.tlvHex, " ", ""))

			command := exec.CommandContext(ctx, pythonPath, "-c", tlvParamsOracleScript)
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			command.Stdin = bytes.NewReader([]byte(hex.EncodeToString(frame)))
			output, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, output)
			}
			var oracle struct {
				TLVParams string `json:"tlv_params"`
				Error     string `json:"error"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output), &oracle); err != nil {
				t.Fatalf("oracle output %q: %v", output, err)
			}

			pdu, decodeErr := smppwire.Decode(frame)
			if testCase.wantSpecError {
				if !errors.Is(decodeErr, smppwire.ErrInvalidOptionalParameterLength) {
					t.Fatalf("Go error = %v, want SMPP invalid optional parameter length", decodeErr)
				}
				return
			}
			if testCase.wantError {
				if oracle.Error == "" {
					t.Fatalf("oracle accepted a frame expected to fail")
				}
				if decodeErr == nil {
					t.Fatalf("Go accepted a frame the legacy decoder rejects (%s)", oracle.Error)
				}
				return
			}
			if oracle.Error != "" {
				t.Fatalf("oracle rejected the frame: %s", oracle.Error)
			}
			if decodeErr != nil {
				t.Fatalf("Go rejected a frame the legacy decoder accepts: %v", decodeErr)
			}
			delivery, err := DeliveryFromDeliverSM(pdu.SM, "m", "c", "http://cb", "POST")
			if err != nil {
				t.Fatal(err)
			}
			goJSON := encodeTLVParams(delivery.TLVParams)
			if len(delivery.TLVParams) == 0 {
				goJSON = "{}"
			}
			if goJSON != oracle.TLVParams {
				t.Fatalf("tlv_params diverges:\n  go %s\n  py %s", goJSON, oracle.TLVParams)
			}
		})
	}
}
