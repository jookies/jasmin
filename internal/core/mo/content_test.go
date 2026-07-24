package mo

import (
	"errors"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestSelectContent(t *testing.T) {
	// short_message non-empty wins.
	if c, err := SelectContent([]byte("hi"), []byte("payload")); err != nil || string(c) != "hi" {
		t.Errorf("short_message should win: got %q, %v", c, err)
	}
	// empty short_message falls through to message_payload.
	if c, err := SelectContent([]byte{}, []byte("payload")); err != nil || string(c) != "payload" {
		t.Errorf("empty short_message -> payload: got %q, %v", c, err)
	}
	// nil short_message + payload -> payload.
	if c, err := SelectContent(nil, []byte("payload")); err != nil || string(c) != "payload" {
		t.Errorf("nil short_message -> payload: got %q, %v", c, err)
	}
	// empty-but-present short_message, no payload -> empty content.
	if c, err := SelectContent([]byte{}, nil); err != nil || len(c) != 0 || c == nil {
		t.Errorf("empty short_message present: got %q (nil=%v), %v", c, c == nil, err)
	}
	// neither present -> ErrNoContent.
	if _, err := SelectContent(nil, nil); !errors.Is(err, ErrNoContent) {
		t.Errorf("no content: want ErrNoContent, got %v", err)
	}
}

func TestDeliveryFromDeliverSM(t *testing.T) {
	sm := &smppwire.SMBody{
		SourceAddress:      []byte("447700900000"),
		DestinationAddress: []byte("12345"),
		PriorityFlag:       2,
		DataCoding:         8,
		ValidityPeriod:     []byte("000000000100000R"),
		ShortMessage:       []byte("hello from the network"),
	}
	d, err := DeliveryFromDeliverSM(sm, "msg-1", "smpp-in", "http://cb", "POST")
	if err != nil {
		t.Fatalf("DeliveryFromDeliverSM: %v", err)
	}
	if d.MsgID != "msg-1" || d.From != "447700900000" || d.To != "12345" || d.OriginConnector != "smpp-in" {
		t.Errorf("fields wrong: %+v", d)
	}
	if string(d.Content) != "hello from the network" {
		t.Errorf("content = %q", d.Content)
	}
	if d.Priority == nil || *d.Priority != 2 || d.Coding == nil || *d.Coding != 8 {
		t.Errorf("priority/coding wrong: %+v", d)
	}
	if d.Validity != "000000000100000R" {
		t.Errorf("validity = %q", d.Validity)
	}
	if d.URL != "http://cb" || d.Method != "POST" {
		t.Errorf("connector fields wrong: %+v", d)
	}
}

func TestDeliveryFromDeliverSM_MessagePayload(t *testing.T) {
	sm := &smppwire.SMBody{
		SourceAddress:      []byte("a"),
		DestinationAddress: []byte("b"),
		Optional:           smppwire.OptionalParameters{MessagePayload: []byte("long content via payload")},
	}
	d, err := DeliveryFromDeliverSM(sm, "m", "c", "http://x", "GET")
	if err != nil {
		t.Fatalf("DeliveryFromDeliverSM: %v", err)
	}
	if string(d.Content) != "long content via payload" {
		t.Errorf("content = %q, want the message_payload", d.Content)
	}
	// No validity present -> omitted.
	if d.Validity != "" {
		t.Errorf("validity should be empty, got %q", d.Validity)
	}
}

func TestDeliveryFromDeliverSM_Errors(t *testing.T) {
	if _, err := DeliveryFromDeliverSM(nil, "m", "c", "u", "POST"); err == nil {
		t.Errorf("nil body: expected error")
	}
	sm := &smppwire.SMBody{SourceAddress: []byte("a"), DestinationAddress: []byte("b")} // no content
	if _, err := DeliveryFromDeliverSM(sm, "m", "c", "u", "POST"); !errors.Is(err, ErrNoContent) {
		t.Errorf("no content: want ErrNoContent, got %v", err)
	}
}
