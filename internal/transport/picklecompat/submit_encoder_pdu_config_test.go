package picklecompat_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// TestEncodeSubmitSMCarriesConnectorPDUDefaults proves the connector-config PDU
// params (GAP 4) flow through the bridge encode -> pickle -> decode into the wire
// body: TON/NPI, service_type and protocol_id emerge on the encoded submit.
func TestEncodeSubmitSMCarriesConnectorPDUDefaults(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	encoded, err := bridge.EncodeSubmitSM(ctx, picklecompat.SubmitSMEncodeRequest{
		Sequence: 1, SourceAddr: picklecompat.Bytes("1111"), DestinationAddr: picklecompat.Bytes("2222"),
		ShortMessage: picklecompat.Bytes("hi"),
		// Legacy connector defaults: source NATIONAL/ISDN, dest INTERNATIONAL/ISDN,
		// plus a non-default service_type and protocol_id.
		SourceAddrTON: 2, SourceAddrNPI: 1, DestAddrTON: 1, DestAddrNPI: 1,
		ServiceType: "CMT", ProtocolID: 0x34,
		IncludeBill: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := bridge.DecodeSubmitSM(ctx, encoded.Body)
	if err != nil {
		t.Fatal(err)
	}
	if body.SourceAddressTON != 2 || body.SourceAddressNPI != 1 ||
		body.DestinationAddressTON != 1 || body.DestinationAddressNPI != 1 {
		t.Fatalf("TON/NPI not carried: src=%d/%d dst=%d/%d", body.SourceAddressTON, body.SourceAddressNPI,
			body.DestinationAddressTON, body.DestinationAddressNPI)
	}
	if string(body.ServiceType) != "CMT" {
		t.Fatalf("service_type = %q", body.ServiceType)
	}
	if body.ProtocolID != 0x34 {
		t.Fatalf("protocol_id = %#x", body.ProtocolID)
	}
}
