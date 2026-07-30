package smppc_test

import (
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// TestConnectorBindModeWire proves each bind role sends the matching bind PDU
// on the wire, and that only submit-capable roles start the submit consumer:
// a transmitter binds bind_transmitter and consumes; a receiver binds
// bind_receiver and never consumes.
func TestConnectorBindModeWire(t *testing.T) {
	cases := []struct {
		bind         smppc.BindType
		wantBind     uint32
		respCommand  uint32
		wantConsumer bool
	}{
		{smppc.BindTransmitter, smppwire.CommandBindTransmitter, smppwire.CommandBindTransmitterResp, true},
		{smppc.BindReceiver, smppwire.CommandBindReceiver, smppwire.CommandBindReceiverResp, false},
		{smppc.BindTransceiver, smppwire.CommandBindTransceiver, smppwire.CommandBindTransceiverResp, true},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.bind), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			address := listener.Addr().(*net.TCPAddr)
			cfg := smppc.Config{
				CID: "role", Host: address.IP.String(), Port: address.Port,
				SystemID: "client", Password: "pw", Bind: testCase.bind, TrxTimeout: 1, ConFailDelay: 1,
			}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			connector, err := smppc.NewConnector(cfg, "")
			if err != nil {
				t.Fatal(err)
			}
			provider := &mockAMQPProvider{deliveries: make(chan *amqpcompat.Delivery), consumeCalls: make(chan struct{}, 1)}
			injectAMQPProvider(connector, provider)
			if err := connector.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = connector.Stop() })

			server, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			bind, err := smppwire.Read(server, 1024)
			if err != nil {
				t.Fatal(err)
			}
			if bind.Header.CommandID != testCase.wantBind {
				t.Fatalf("bind command = %#x want %#x", bind.Header.CommandID, testCase.wantBind)
			}
			resp, _ := smppwire.Encode(smppwire.PDU{
				Header:       smppwire.Header{CommandID: testCase.respCommand, SequenceNumber: bind.Header.SequenceNumber},
				BindResponse: &smppwire.BindResponseBody{SystemID: []byte("smsc")},
			})
			if _, err := server.Write(resp); err != nil {
				t.Fatal(err)
			}

			// The submit consumer starts only for submit-capable roles.
			select {
			case <-provider.consumeCalls:
				if !testCase.wantConsumer {
					t.Fatal("receiver bind started the submit consumer")
				}
			case <-time.After(300 * time.Millisecond):
				if testCase.wantConsumer {
					t.Fatal("submit-capable bind did not start the consumer")
				}
			}
			// All roles reach BOUND.
			deadline := time.Now().Add(time.Second)
			for connector.Status() != smppc.StatusBound {
				if time.Now().After(deadline) {
					t.Fatalf("connector not bound; status=%s", connector.Status())
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}
