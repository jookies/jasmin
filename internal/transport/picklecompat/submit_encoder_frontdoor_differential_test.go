package picklecompat_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// frontDoorConfigOracleScript builds a submit_sm the way the legacy front door
// does — SMPPOperationFactory(config) fills unset TON/NPI etc. from the routed
// connector's SMPPClientConfig — and prints the encoded wire body (offset 16+).
const frontDoorConfigOracleScript = `
import binascii
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from smpp.pdu.pdu_encoding import PDUEncoder
from smpp.pdu.constants import data_coding_default_value_map
from smpp.pdu.pdu_types import DataCoding, DataCodingDefault, AddrTon, AddrNpi
config = SMPPClientConfig(id='oracletest',
    source_addr_ton=AddrTon.NATIONAL, source_addr_npi=AddrNpi.ISDN,
    dest_addr_ton=AddrTon.INTERNATIONAL, dest_addr_npi=AddrNpi.ISDN, service_type='CMT')
pdu = SMPPOperationFactory(config).SubmitSM(source_addr=b'1111', destination_addr=b'2222', short_message=b'hi', data_coding=0)
dc = pdu.params.get('data_coding')
if isinstance(dc, int):
    name = data_coding_default_value_map.get(dc)
    pdu.params['data_coding'] = DataCoding(schemeData=getattr(DataCodingDefault, name)) if name else None
print(binascii.hexlify(PDUEncoder().encode(pdu)[16:]).decode())
`

// TestFrontDoorSubmitBodyMatchesLegacyConnectorConfig closes GAP 4's parity loop:
// the Go front-door submit (encoded with the routed connector's PDU defaults,
// decoded, and re-encoded to the wire) equals the legacy factory's wire body
// byte-for-byte, including the connector TON/NPI (NATIONAL/ISDN, INTERNATIONAL/
// ISDN) and service_type.
func TestFrontDoorSubmitBodyMatchesLegacyConnectorConfig(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, pythonPath, "-c", frontDoorConfigOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	legacyBody, err := hex.DecodeString(string(bytes.TrimSpace(output)))
	if err != nil {
		t.Fatalf("oracle hex %q: %v", output, err)
	}

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	encoded, err := bridge.EncodeSubmitSM(ctx, picklecompat.SubmitSMEncodeRequest{
		Sequence: 1, SourceAddr: picklecompat.Bytes("1111"), DestinationAddr: picklecompat.Bytes("2222"),
		ShortMessage: picklecompat.Bytes("hi"), DataCoding: 0,
		SourceAddrTON: 2, SourceAddrNPI: 1, DestAddrTON: 1, DestAddrNPI: 1, ServiceType: "CMT",
		IncludeBill: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := bridge.DecodeSubmitSM(ctx, encoded.Body)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: 1},
		SM:     &body,
	})
	if err != nil {
		t.Fatal(err)
	}
	goBody := wire[16:]
	if !bytes.Equal(goBody, legacyBody) {
		t.Fatalf("front-door body diverges:\n  go %s\n  py %s", hex.EncodeToString(goBody), hex.EncodeToString(legacyBody))
	}
}
