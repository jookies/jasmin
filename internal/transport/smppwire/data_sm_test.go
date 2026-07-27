package smppwire

import (
	"bytes"
	"testing"
)

func TestDataSMRoundTrip(t *testing.T) {
	payload := []byte("id:XYZ stat:DELIVRD")
	body := &SMBody{
		ServiceType:           []byte(""),
		SourceAddressTON:      2,
		SourceAddressNPI:      1,
		SourceAddress:         []byte("1234"),
		DestinationAddressTON: 1,
		DestinationAddressNPI: 1,
		DestinationAddress:    []byte("5678"),
		ESMClass:              0x04,
		RegisteredDelivery:    1,
		DataCoding:            0,
	}
	body.Optional.MessagePayload = payload

	pdu := PDU{Header: Header{CommandID: CommandDataSM, SequenceNumber: 5}, SM: body}
	wire, err := Encode(pdu)
	if err != nil {
		t.Fatalf("encode data_sm: %v", err)
	}
	decoded, err := Decode(wire)
	if err != nil {
		t.Fatalf("decode data_sm: %v", err)
	}
	if decoded.Header.CommandID != CommandDataSM {
		t.Fatalf("command = %#x want data_sm", decoded.Header.CommandID)
	}
	if string(decoded.SM.SourceAddress) != "1234" || string(decoded.SM.DestinationAddress) != "5678" {
		t.Fatalf("addrs src=%q dst=%q", decoded.SM.SourceAddress, decoded.SM.DestinationAddress)
	}
	if decoded.SM.ESMClass != 0x04 || decoded.SM.RegisteredDelivery != 1 {
		t.Fatalf("esm=%#x rd=%d", decoded.SM.ESMClass, decoded.SM.RegisteredDelivery)
	}
	if !bytes.Equal(decoded.SM.Optional.MessagePayload, payload) {
		t.Fatalf("message_payload = %q want %q", decoded.SM.Optional.MessagePayload, payload)
	}
	// data_sm carries no short_message (that's the whole point of the subset).
	if len(decoded.SM.ShortMessage) != 0 {
		t.Fatalf("data_sm decoded a short_message: %q", decoded.SM.ShortMessage)
	}
	// Re-encode must reproduce the exact bytes.
	rewire, err := Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rewire, wire) {
		t.Fatal("data_sm re-encode is not byte-stable")
	}
}

func TestDataSMDistinctFromDeliverSMLayout(t *testing.T) {
	// A deliver_sm and a data_sm with the same addresses encode to different
	// bodies (data_sm omits protocol_id/priority/schedule/validity/replace/
	// sm_default/sm_length), proving they are not the same wire shape.
	fields := func() *SMBody {
		return &SMBody{SourceAddress: []byte("1"), DestinationAddress: []byte("2"), DataCoding: 0}
	}
	deliver, err := Encode(PDU{Header: Header{CommandID: CommandDeliverSM}, SM: fields()})
	if err != nil {
		t.Fatal(err)
	}
	data, err := Encode(PDU{Header: Header{CommandID: CommandDataSM}, SM: fields()})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= len(deliver) {
		t.Fatalf("data_sm body (%d) not shorter than deliver_sm (%d)", len(data), len(deliver))
	}
}
