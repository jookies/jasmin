package smppc_test

import (
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// bindThenGoSilent answers the bind then drains input WITHOUT ever replying to
// enquire_link, so the client's response timer (ResTimeout) must terminate the
// session — the keepalive-detection path that drives a reconnect.
func bindThenGoSilent(conn net.Conn) {
	defer conn.Close()
	bind, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
	if err != nil {
		return
	}
	response, err := smppwire.Encode(smppwire.PDU{
		Header:       smppwire.Header{CommandID: bind.Header.CommandID | 0x80000000, SequenceNumber: bind.Header.SequenceNumber},
		BindResponse: &smppwire.BindResponseBody{SystemID: []byte("smsc")},
	})
	if err != nil {
		return
	}
	if _, err := conn.Write(response); err != nil {
		return
	}
	for {
		if _, err := smppwire.Read(conn, smppwire.DefaultMaxSize); err != nil {
			return
		}
	}
}

// TestConnectorReconnectsOnKeepaliveTimeout proves the full keepalive->reconnect
// chain at the connector level: the SMSC binds but never answers enquire_link,
// so the client's enquire_link_resp timer fires (handleControlTimeout closes the
// socket) and the connector loop rebinds. The session-level termination is
// covered by TestSessionMissingEnquireLinkResponseTerminates; this proves it
// actually reconnects, backing the audit that the chain needs no code change.
func TestConnectorReconnectsOnKeepaliveTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	// Fast keepalive: probe every 20ms, give up on the response after 50ms.
	cfg := smppc.Config{
		CID: "keepalive", Host: address.IP.String(), Port: address.Port,
		SystemID: "client", Password: "password",
		PDUTimeout: 0.02, ResTimeout: 0.05, ConLossDelay: 0.05,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go bindThenGoSilent(conn)
			select {
			case accepted <- conn:
			default:
			}
		}
	}()

	connector, err := smppc.NewConnector(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	provider := &mockAMQPProvider{deliveries: make(chan *amqpcompat.Delivery)}
	injectAMQPProvider(connector, provider)
	if err := connector.Start(); err != nil {
		t.Fatal(err)
	}
	defer connector.Stop()

	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("no initial bind")
	}
	// The server never answers enquire_link; the connector must detect the dead
	// peer via the response timeout and reconnect (a second bind).
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("connector did not reconnect after enquire_link (keepalive) timeout")
	}
}
