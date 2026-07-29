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
