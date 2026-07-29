package smppwire_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// dataSMOracleScript decodes our Go-encoded data_sm wire with smpp.pdu (the
// library the bridge uses) and reports the parsed params, proving the Go
// data_sm mandatory-body layout matches the standard the bridge relies on.
const dataSMOracleScript = `
import sys, json, base64, io, contextlib
from smpp.pdu.pdu_encoding import PDUEncoder

wire = base64.b64decode(sys.argv[1])
with contextlib.redirect_stdout(io.StringIO()):
    pdu = PDUEncoder().decode(io.BytesIO(wire))
p = pdu.params
def b(x):
    return base64.b64encode(bytes(x)).decode() if x is not None else None
print(json.dumps({
    "command": pdu.id.name,
    "source_addr": b(p.get("source_addr")),
    "destination_addr": b(p.get("destination_addr")),
    "message_payload": b(p.get("message_payload")),
}))
`

// dataSMOptionalOracleScript exercises the complete DataSM optional-parameter
// intersection that the frozen OptionEncoder can actually encode. The Go
// decoder must retain and re-emit the frame byte-for-byte in DataSM's declared
// order.
const dataSMOptionalOracleScript = `
import binascii
import jasmin.protocols.smpp.operations
from smpp.pdu.pdu_encoding import PDUEncoder
from smpp.pdu.operations import DataSM
from smpp.pdu import pdu_types

pdu = DataSM(seqNum=7, source_addr="1111", destination_addr="2222")
pdu.params.update({
    "source_port": 9200,
    "source_addr_subunit": pdu_types.AddrSubunit.EXTERNAL_UNIT_1,
    "source_network_type": pdu_types.NetworkType.GSM,
    "source_bearer_type": pdu_types.BearerType.USSD,
    "source_telematics_id": 0x1234,
    "destination_port": 9201,
    "dest_addr_subunit": pdu_types.AddrSubunit.MOBILE_EQUIPMENT,
    "dest_network_type": pdu_types.NetworkType.CDMA,
    "dest_bearer_type": pdu_types.BearerType.PACKET_DATA,
    "dest_telematics_id": 0x4321,
    "sar_msg_ref_num": 0x2345,
    "sar_total_segments": 3,
    "sar_segment_seqnum": 2,
    "more_messages_to_send": pdu_types.MoreMessagesToSend.MORE_MESSAGES,
    "qos_time_to_live": 0x01020304,
    "payload_type": pdu_types.PayloadType.WCMP,
    "message_payload": b"data-sm-payload",
    "receipted_message_id": "receipt-1",
    "message_state": pdu_types.MessageState.DELIVERED,
    "network_error_code": b"\x03\x00\x01",
    "user_message_reference": 0x3456,
    "privacy_indicator": pdu_types.PrivacyIndicator.SECRET,
    "callback_num": pdu_types.CallbackNum(
        pdu_types.CallbackNumDigitModeIndicator.ASCII,
        pdu_types.AddrTon.INTERNATIONAL,
        pdu_types.AddrNpi.ISDN,
        b"18005550199",
    ),
    "source_subaddress": pdu_types.Subaddress(
        pdu_types.SubaddressTypeTag.NSAP_ODD, b"source-subaddress"),
    "dest_subaddress": pdu_types.Subaddress(
        pdu_types.SubaddressTypeTag.RESERVED, b"dest-subaddress"),
    "user_response_code": 255,
    "display_time": pdu_types.DisplayTime.INVOKE,
    "sms_signal": b"\xde\xad",
    "number_of_messages": 99,
    "language_indicator": pdu_types.LanguageIndicator.PORTUGUESE,
})
print(binascii.hexlify(PDUEncoder().encode(pdu)).decode())
`

// TestDataSMEncodingMatchesSmppPdu proves a Go-encoded data_sm decodes under
// smpp.pdu to the same fields — the bridge decodes MO data_sm this way, so the
// layout must agree.
func TestDataSMEncodingMatchesSmppPdu(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	body := &smppwire.SMBody{
		SourceAddressTON: 2, SourceAddressNPI: 1, SourceAddress: []byte("31612345678"),
		DestinationAddressTON: 1, DestinationAddressNPI: 1, DestinationAddress: []byte("2255"),
		ESMClass: 0, RegisteredDelivery: 0, DataCoding: 0,
	}
	body.Optional.MessagePayload = []byte("hello via data_sm")
	wire, err := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandDataSM, SequenceNumber: 1}, SM: body})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", dataSMOracleScript, base64.StdEncoding.EncodeToString(wire))
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	var got struct {
		Command         string `json:"command"`
		SourceAddr      string `json:"source_addr"`
		DestinationAddr string `json:"destination_addr"`
		MessagePayload  string `json:"message_payload"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &got); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if got.Command != "data_sm" {
		t.Fatalf("smpp.pdu parsed command %q want data_sm", got.Command)
	}
	assertB64(t, "source_addr", got.SourceAddr, "31612345678")
	assertB64(t, "destination_addr", got.DestinationAddr, "2255")
	assertB64(t, "message_payload", got.MessagePayload, "hello via data_sm")
}

func TestDataSMAllSupportedOptionalsRoundTripFrozenEncoder(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", dataSMOptionalOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	legacyFrame, err := hex.DecodeString(string(bytes.TrimSpace(output)))
	if err != nil {
		t.Fatal(err)
	}
	pdu, err := smppwire.Decode(legacyFrame)
	if err != nil {
		t.Fatalf("Go decode of legacy data_sm: %v", err)
	}
	reencoded, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatalf("Go re-encode of legacy data_sm: %v", err)
	}
	if !bytes.Equal(reencoded, legacyFrame) {
		t.Fatalf("data_sm optional round trip diverges:\n  go %s\n  py %s",
			hex.EncodeToString(reencoded), hex.EncodeToString(legacyFrame))
	}
}

func assertB64(t *testing.T, field, encoded, want string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("%s decode: %v", field, err)
	}
	if string(raw) != want {
		t.Fatalf("%s = %q want %q", field, raw, want)
	}
}
