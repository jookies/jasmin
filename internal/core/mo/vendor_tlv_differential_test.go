package mo

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
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
