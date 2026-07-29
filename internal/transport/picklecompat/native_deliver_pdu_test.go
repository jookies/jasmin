package picklecompat

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestEncodeRoutableDeliverPDUPreservesDecodedContent(t *testing.T) {
	codec := NewNativeCodec()
	ctx := context.Background()
	cases := []struct {
		name    string
		message []byte
		body    smppwire.SMBody
	}{
		{
			name:    "long short_message",
			message: []byte(strings.Repeat("a", 300)),
			body: smppwire.SMBody{
				SourceAddress: []byte("111"), DestinationAddress: []byte("222"),
				ShortMessage: []byte(strings.Repeat("a", 300)),
			},
		},
		{
			name:    "message_payload",
			message: []byte("hello from message_payload"),
			body: smppwire.SMBody{
				SourceAddress: []byte("111"), DestinationAddress: []byte("222"),
				Optional: smppwire.OptionalParameters{MessagePayload: []byte("hello from message_payload")},
			},
		},
	}
	connectors, err := codec.EncodeConnectorList(ctx, []MOConnectorSpec{{
		Type: "http", CID: "http-1", URL: "http://localhost/mo", Method: "POST",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body := testCase.body
			routable, err := codec.EncodeRoutableDeliverPDU(ctx, smppwire.PDU{
				Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 7},
				SM:     &body,
			}, "cid-1")
			if err != nil {
				t.Fatal(err)
			}
			pdu, _, err := codec.RepickleRoutablePDU(ctx, routable)
			if err != nil {
				t.Fatal(err)
			}
			routed, err := codec.DecodeRoutedDeliverSM(ctx, connectors, pdu)
			if err != nil {
				t.Fatal(err)
			}
			content := routed.Body.ShortMessage
			if len(content) == 0 {
				content = routed.Body.Optional.MessagePayload
			}
			if !bytes.Equal(content, testCase.message) {
				t.Fatalf("content length=%d want %d", len(content), len(testCase.message))
			}
		})
	}
}

func TestEncodeRoutableDeliverPDUPreservesOptionalTLVs(t *testing.T) {
	codec := NewNativeCodec()
	ctx := context.Background()
	userMessageReference := uint16(513)
	body := smppwire.SMBody{
		SourceAddress:      []byte("111"),
		DestinationAddress: []byte("222"),
		ShortMessage:       []byte("hello"),
		Optional: smppwire.OptionalParameters{
			UserMessageReference: &userMessageReference,
		},
		CapturedVendorTLVs: []smppwire.CapturedVendorTLV{
			{Tag: 0x1401, Value: []byte{0x01, 0x02}},
			{Tag: 0x1401, Value: []byte{0xff}},
		},
	}
	routable, err := codec.EncodeRoutableDeliverPDU(ctx, smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 7},
		SM:     &body,
	}, "cid-1")
	if err != nil {
		t.Fatal(err)
	}
	pdu, _, err := codec.RepickleRoutablePDU(ctx, routable)
	if err != nil {
		t.Fatal(err)
	}
	connectors, err := codec.EncodeConnectorList(ctx, []MOConnectorSpec{{
		Type: "http", CID: "http-1", URL: "http://localhost/mo", Method: "POST",
	}})
	if err != nil {
		t.Fatal(err)
	}
	routed, err := codec.DecodeRoutedDeliverSM(ctx, connectors, pdu)
	if err != nil {
		t.Fatal(err)
	}
	if routed.Body.Optional.UserMessageReference == nil || *routed.Body.Optional.UserMessageReference != userMessageReference {
		t.Fatalf("user_message_reference = %v, want %d", routed.Body.Optional.UserMessageReference, userMessageReference)
	}
	if len(routed.CustomTLVs) != 2 {
		t.Fatalf("custom TLVs = %+v, want 2 captured entries", routed.CustomTLVs)
	}
	for index, want := range [][]byte{{0x01, 0x02}, {0xff}} {
		got := routed.CustomTLVs[index]
		if got.Tag == nil || got.Tag.Uint64() != 0x1401 || got.Length == nil || *got.Length != len(want) ||
			got.Type != "OctetString" || !bytes.Equal(got.Value.([]byte), want) {
			t.Fatalf("custom TLV %d = %+v, want tag 0x1401 length %d OctetString %x", index, got, len(want), want)
		}
	}
}
