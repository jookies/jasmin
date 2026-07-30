package smppc_test

import (
	"context"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// serveSMPPBind answers one connection's bind and then serves it until it drops:
// bind_*_resp OK, then enquire_link_resp for keepalives, ignoring everything
// else. It is the minimal SMSC the soak binds against.
func serveSMPPBind(conn net.Conn) {
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
		pdu, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
		if err != nil {
			return
		}
		if pdu.Header.CommandID == smppwire.CommandEnquireLink {
			ack, encodeErr := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
				CommandID: smppwire.CommandEnquireLinkResp, SequenceNumber: pdu.Header.SequenceNumber}})
			if encodeErr != nil {
				return
			}
			if _, writeErr := conn.Write(ack); writeErr != nil {
				return
			}
		}
	}
}

// TestConnectorSoakReconnectNoGoroutineLeak drives many bind -> force-drop ->
// re-bind cycles and asserts the connector recovers every time without leaking
// goroutines. It proves the reconnect machinery (session reader + AMQP consumer
// generation + liveness watcher) tears each generation down cleanly. Gated
// behind SMPP_SOAK so the goroutine-count assertion never flakes the fast CI
// gate; run with: SMPP_SOAK=1 go test ./internal/core/smppc/ -run Soak -race.
func TestConnectorSoakReconnectNoGoroutineLeak(t *testing.T) {
	if os.Getenv("SMPP_SOAK") == "" {
		t.Skip("set SMPP_SOAK=1 to run the reconnect soak")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID: "soak", Host: address.IP.String(), Port: address.Port,
		SystemID: "client", Password: "password",
		ConLossDelay: 0.05, ConFailDelay: 0.05,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	// Accept every connection and serve its bind; publish each accepted conn so
	// the test can force-drop it to trigger a reconnect.
	accepted := make(chan net.Conn, 8)
	serverCtx, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveSMPPBind(conn)
			select {
			case accepted <- conn:
			case <-serverCtx.Done():
				_ = conn.Close()
				return
			}
		}
	}()

	connector, err := smppc.NewConnector(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	// A mock AMQP provider keeps the transceiver's submit consumer running (so
	// its per-generation goroutines are part of the leak check) without a broker.
	provider := &mockAMQPProvider{deliveries: make(chan *amqpcompat.Delivery)}
	injectAMQPProvider(connector, provider)
	if err := connector.Start(); err != nil {
		t.Fatal(err)
	}
	defer connector.Stop()

	const cycles = 25
	const warmup = 5
	baseline := 0
	for cycle := 0; cycle < cycles; cycle++ {
		var conn net.Conn
		select {
		case conn = <-accepted:
		case <-time.After(3 * time.Second):
			t.Fatalf("cycle %d: no connection accepted (reconnect stalled)", cycle)
		}
		waitForConnectorStatus(t, connector, smppc.StatusBound, 3*time.Second)
		_ = conn.Close() // force-drop -> connection-loss reconnect
		if cycle == warmup {
			runtime.GC()
			baseline = runtime.NumGoroutine()
		}
	}
	// Let the final reconnect settle, then confirm goroutines did not grow with
	// the cycle count (a per-cycle leak would add ~3 goroutines each).
	select {
	case conn := <-accepted:
		waitForConnectorStatus(t, connector, smppc.StatusBound, 3*time.Second)
		_ = conn.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("final reconnect stalled")
	}
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	final := runtime.NumGoroutine()
	if final > baseline+8 {
		t.Fatalf("goroutine leak across %d reconnects: baseline=%d final=%d", cycles, baseline, final)
	}
}
