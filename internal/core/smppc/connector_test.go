package smppc_test

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
	amqp "github.com/rabbitmq/amqp091-go"
)

func injectAMQPProvider(c *smppc.Connector, provider smppc.AMQPProvider) {
	v := reflect.ValueOf(c).Elem()
	f := v.FieldByName("amqp")
	ptr := reflect.NewAt(f.Type(), f.Addr().UnsafePointer()).Elem()
	ptr.Set(reflect.ValueOf(provider))
}

type exactSettlement struct {
	action  string
	requeue bool
}

type exactAcknowledger struct {
	settled chan<- exactSettlement
}

func (a *exactAcknowledger) Ack(tag uint64, multiple bool) error {
	a.settled <- exactSettlement{action: "ack"}
	return nil
}
func (a *exactAcknowledger) Nack(tag uint64, multiple, requeue bool) error {
	a.settled <- exactSettlement{action: "nack", requeue: requeue}
	return nil
}
func (a *exactAcknowledger) Reject(tag uint64, requeue bool) error {
	a.settled <- exactSettlement{action: "reject", requeue: requeue}
	return nil
}

func injectExactDelivery(envelope amqpcompat.Envelope, settled chan<- exactSettlement) *amqpcompat.Delivery {
	raw := amqp.Delivery{
		Acknowledger: &exactAcknowledger{settled: settled},
		MessageId:    envelope.Properties().MessageID(), RoutingKey: envelope.RoutingKey(), Body: envelope.Body(),
		Headers: make(amqp.Table),
	}
	for key, value := range envelope.Properties().Headers() {
		text, _ := value.String()
		raw.Headers[key] = text
	}
	delivery, _ := amqpcompat.NewDelivery(raw)
	return delivery
}

type recordingAcknowledger struct {
	settle func(requeue bool)
}

func (a *recordingAcknowledger) Ack(tag uint64, multiple bool) error { a.settle(false); return nil }
func (a *recordingAcknowledger) Nack(tag uint64, multiple, requeue bool) error {
	a.settle(requeue)
	return nil
}
func (a *recordingAcknowledger) Reject(tag uint64, requeue bool) error { a.settle(requeue); return nil }

func injectDelivery(envelope amqpcompat.Envelope, settle func(requeue bool)) *amqpcompat.Delivery {
	raw := amqp.Delivery{
		Acknowledger: &recordingAcknowledger{settle: settle},
		MessageId:    envelope.Properties().MessageID(),
		RoutingKey:   envelope.RoutingKey(),
		Body:         envelope.Body(),
		Headers:      make(amqp.Table),
	}
	for k, v := range envelope.Properties().Headers() {
		s, _ := v.String()
		raw.Headers[k] = s
	}
	d, _ := amqpcompat.NewDelivery(raw)
	return d
}

func TestConnectorConnectionSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID:      "test",
		Host:     addr.IP.String(),
		Port:     addr.Port,
		SystemID: "client",
		Password: "password",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	connChan := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			connChan <- conn
		}
	}()

	c, err := smppc.NewConnector(cfg, "amqp://unused")
	if err != nil {
		t.Fatal(err)
	}
	injectAMQPProvider(c, &mockAMQPProvider{deliveries: make(chan *amqpcompat.Delivery)})
	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer c.Stop()

	select {
	case conn := <-connChan:
		defer conn.Close()
		// Verify BIND PDU and prove BOUND is gated on the matching response.
		pdu, err := smppwire.Read(conn, 1024)
		if err != nil {
			t.Fatalf("Read BIND failed: %v", err)
		}
		if pdu.Header.CommandID != smppwire.CommandBindTransceiver {
			t.Errorf("expected BIND_TRANSCEIVER, got %#x", pdu.Header.CommandID)
		}
		if string(pdu.Bind.SystemID) != "client" {
			t.Errorf("expected SystemID 'client', got %q", pdu.Bind.SystemID)
		}
		if got := c.Status(); got == smppc.StatusBound {
			t.Fatal("connector became BOUND before bind_transceiver_resp")
		}
		response, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{
				CommandID:      smppwire.CommandBindTransceiverResp,
				CommandStatus:  0,
				SequenceNumber: pdu.Header.SequenceNumber,
			},
			BindResponse: &smppwire.BindResponseBody{SystemID: []byte("smsc")},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(response); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connection")
	}

	waitForConnectorStatus(t, c, smppc.StatusBound, 2*time.Second)
}

func TestConnectorRejectsInvalidBindResponsesBeforeConsumerStart(t *testing.T) {
	cases := []struct {
		name     string
		response func(sequence uint32) smppwire.PDU
	}{
		{"nonzero-status", func(sequence uint32) smppwire.PDU {
			return smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandBindTransceiverResp, CommandStatus: 0x0e, SequenceNumber: sequence}}
		}},
		{"wrong-sequence", func(sequence uint32) smppwire.PDU {
			return smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandBindTransceiverResp, SequenceNumber: sequence + 1}, BindResponse: &smppwire.BindResponseBody{SystemID: []byte("smsc")}}
		}},
		{"wrong-command", func(sequence uint32) smppwire.PDU {
			return smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandEnquireLinkResp, SequenceNumber: sequence}}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			address := listener.Addr().(*net.TCPAddr)
			cfg := smppc.Config{CID: "bad-bind", Host: address.IP.String(), Port: address.Port, SystemID: "client", Password: "password", TrxTimeout: 0.2, ConFailDelay: 1}
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
			server, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			bind, err := smppwire.Read(server, 1024)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := smppwire.Encode(testCase.response(bind.Header.SequenceNumber))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := server.Write(wire); err != nil {
				t.Fatal(err)
			}
			_ = server.Close()
			select {
			case <-provider.consumeCalls:
				t.Fatal("consumer started after invalid bind response")
			case <-time.After(50 * time.Millisecond):
			}
			if connector.Status() == smppc.StatusBound {
				t.Fatal("connector became BOUND after invalid bind response")
			}
			if err := connector.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConnectorFullBindConsumeSubmitAndSettle(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	cfg := smppc.Config{CID: "full-path", Host: address.IP.String(), Port: address.Port, SystemID: "client", Password: "password"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	connector, err := smppc.NewConnector(cfg, "amqp://unused")
	if err != nil {
		t.Fatal(err)
	}
	provider := &mockAMQPProvider{deliveries: make(chan *amqpcompat.Delivery, 1), consumeCalls: make(chan struct{}, 1)}
	injectAMQPProvider(connector, provider)
	if err := connector.Start(); err != nil {
		t.Fatal(err)
	}
	defer connector.Stop()

	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	bind, err := smppwire.Read(server, 1024)
	if err != nil {
		t.Fatal(err)
	}
	bindResponse, err := smppwire.Encode(smppwire.PDU{
		Header:       smppwire.Header{CommandID: smppwire.CommandBindTransceiverResp, SequenceNumber: bind.Header.SequenceNumber},
		BindResponse: &smppwire.BindResponseBody{SystemID: []byte("smsc")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(bindResponse); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.consumeCalls:
	case <-time.After(time.Second):
		t.Fatal("AMQP consumer did not start after successful bind")
	}
	waitForConnectorStatus(t, connector, smppc.StatusBound, time.Second)

	properties, _ := amqpcompat.NewProperties("full-path-message", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.full-path", properties, []byte("hello"))
	settled := make(chan exactSettlement, 1)
	provider.deliveries <- injectExactDelivery(envelope, settled)
	submit, err := smppwire.Read(server, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if submit.Header.CommandID != smppwire.CommandSubmitSM || string(submit.SM.ShortMessage) != "hello" {
		t.Fatalf("unexpected submit PDU: %+v", submit)
	}
	submitResponse, err := smppwire.Encode(smppwire.PDU{
		Header:         smppwire.Header{CommandID: smppwire.CommandSubmitSMResp, SequenceNumber: submit.Header.SequenceNumber},
		SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte("smsc-id")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(submitResponse); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-settled:
		if result.action != "ack" || result.requeue {
			t.Fatalf("settlement = %+v, want exact Ack", result)
		}
	case <-time.After(time.Second):
		t.Fatal("correlated response did not settle delivery")
	}
}

func waitForConnectorStatus(t *testing.T, c *smppc.Connector, want smppc.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := c.Status()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected status %s, got %s after %s", want, got, timeout)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConnectorReconnectLoop(t *testing.T) {
	// Start without listener
	cfg := smppc.Config{
		CID:          "test",
		Host:         "127.0.0.1",
		Port:         12345, // Likely closed
		SystemID:     "client",
		Password:     "password",
		ConFailDelay: 0.1, // Short delay for test
	}
	_ = cfg.Validate()

	c, err := smppc.NewConnector(cfg, "amqp://guest:guest@localhost:5672/")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	time.Sleep(200 * time.Millisecond)
	if c.Status() != smppc.StatusConnecting {
		t.Errorf("expected status CONNECTING during retries, got %s", c.Status())
	}

	// Now start listener on that port
	ln, err := net.Listen("tcp", "127.0.0.1:12345")
	if err != nil {
		// Port might be taken, skip or use a better strategy
		t.Skip("could not bind to 12345")
	}
	defer ln.Close()

	connChan := make(chan net.Conn, 1)
	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			connChan <- conn
		}
	}()

	select {
	case conn := <-connChan:
		conn.Close()
		// Wait for connector to transition to BOUND (or at least try again)
		time.Sleep(200 * time.Millisecond)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect")
	}
}

func TestConnectorReconnectOnConnectionLoss(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID:          "test",
		Host:         addr.IP.String(),
		Port:         addr.Port,
		SystemID:     "client",
		Password:     "password",
		ConFailDelay: 0.1,
	}
	_ = cfg.Validate()

	connChan := make(chan net.Conn, 2)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connChan <- conn
		}
	}()

	c, err := smppc.NewConnector(cfg, "amqp://guest:guest@localhost:5672/")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	// First connection
	var conn1 net.Conn
	select {
	case conn1 = <-connChan:
		_ = conn1.Close() // Simulate loss
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first connection")
	}

	// Second connection (reconnect)
	select {
	case conn2 := <-connChan:
		defer conn2.Close()
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect")
	}
}

type mockAMQPProvider struct {
	deliveries   chan *amqpcompat.Delivery
	consumeCalls chan struct{}
}

func (p *mockAMQPProvider) Consume(ctx context.Context, amqpURL, cid string) (smppc.AMQPDeliveryStream, error) {
	if p.consumeCalls != nil {
		select {
		case p.consumeCalls <- struct{}{}:
		default:
		}
	}
	return smppc.AMQPDeliveryStream{Deliveries: p.deliveries}, nil
}

func TestConnectorStartDuringReconnectBackoffDoesNotCreateSecondSupervisor(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID: "single-supervisor", Host: address.IP.String(), Port: address.Port,
		SystemID: "client", Password: "password", TrxTimeout: 0.2,
		ConLossDelay: 0.3, ConFailDelay: 0.3,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	connector, err := smppc.NewConnector(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	provider := &mockAMQPProvider{deliveries: make(chan *amqpcompat.Delivery)}
	injectAMQPProvider(connector, provider)
	if err := connector.Start(); err != nil {
		t.Fatal(err)
	}

	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	bind, err := smppwire.Read(server, 1024)
	if err != nil {
		t.Fatal(err)
	}
	response, err := smppwire.Encode(smppwire.PDU{
		Header:       smppwire.Header{CommandID: smppwire.CommandBindTransceiverResp, SequenceNumber: bind.Header.SequenceNumber},
		BindResponse: &smppwire.BindResponseBody{SystemID: []byte("smsc")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(response); err != nil {
		t.Fatal(err)
	}
	waitForConnectorStatus(t, connector, smppc.StatusBound, time.Second)
	_ = server.Close()
	waitForConnectorStatus(t, connector, smppc.StatusDisconnected, time.Second)

	// DISCONNECTED describes the transport during backoff, not supervisor
	// lifecycle. Repeated Start must remain idempotent until that loop exits.
	if err := connector.Start(); err != nil {
		t.Fatal(err)
	}
	if err := connector.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- connector.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked because Start created a second supervisor")
	}
}

func TestConnectorRunConsumerReadiness(t *testing.T) {
	cfg := smppc.Config{CID: "test-ready"}
	c, _ := smppc.NewConnector(cfg, "")

	mock := &mockAMQPProvider{deliveries: make(chan *amqpcompat.Delivery, 1)}
	// Using reflect or a setter to inject mock
	injectAMQPProvider(c, mock)

	// Create a delivery with past expiration
	headers := map[string]amqpcompat.Field{
		"expiration": amqpcompat.StringField(time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")),
		"created_at": amqpcompat.StringField(time.Now().Add(-2 * time.Hour).Format("2006-01-02 15:04:05")),
	}
	props, _ := amqpcompat.NewProperties("msg-1", headers)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.test-ready", props, []byte("body"))

	// mock delivery
	ackChan := make(chan bool, 1)
	delivery := injectDelivery(envelope, func(requeue bool) { ackChan <- requeue })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c.SetStatus(smppc.StatusBound)

	go c.RunConsumer(ctx, nil)

	mock.deliveries <- delivery

	select {
	case requeue := <-ackChan:
		if requeue {
			t.Errorf("expected discard (requeue=false) for expired message")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readiness decision")
	}
}

func TestConnectorConcurrentStartStopRemainsRestartable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().(*net.TCPAddr)
	_ = listener.Close()
	cfg := smppc.Config{
		CID: "lifecycle-race", Host: address.IP.String(), Port: address.Port,
		SystemID: "client", Password: "password", ConFailDelay: 0.001,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	connector, err := smppc.NewConnector(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 100; iteration++ {
		if err := connector.Start(); err != nil {
			t.Fatal(err)
		}
		stopped := make(chan error, 1)
		started := make(chan error, 1)
		go func() { stopped <- connector.Stop() }()
		go func() { started <- connector.Start() }()
		select {
		case err := <-stopped:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatalf("concurrent Stop blocked at iteration %d", iteration)
		}
		select {
		case err := <-started:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatalf("concurrent Start blocked at iteration %d", iteration)
		}
		if err := connector.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConnectorStopCancelsStalledAMQPHandshake(t *testing.T) {
	amqpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer amqpListener.Close()
	amqpAccepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := amqpListener.Accept()
		if acceptErr == nil {
			amqpAccepted <- connection
		}
	}()

	smppListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer smppListener.Close()
	address := smppListener.Addr().(*net.TCPAddr)
	cfg := smppc.Config{CID: "stop-amqp", Host: address.IP.String(), Port: address.Port, SystemID: "client", Password: "password"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	connector, err := smppc.NewConnector(cfg, "amqp://guest:guest@"+amqpListener.Addr().String()+"/")
	if err != nil {
		t.Fatal(err)
	}
	if err := connector.Start(); err != nil {
		t.Fatal(err)
	}
	smppServer, err := smppListener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer smppServer.Close()
	bind, err := smppwire.Read(smppServer, 1024)
	if err != nil {
		t.Fatal(err)
	}
	response, err := smppwire.Encode(smppwire.PDU{
		Header:       smppwire.Header{CommandID: smppwire.CommandBindTransceiverResp, SequenceNumber: bind.Header.SequenceNumber},
		BindResponse: &smppwire.BindResponseBody{SystemID: []byte("smsc")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := smppServer.Write(response); err != nil {
		t.Fatal(err)
	}
	var stalledAMQP net.Conn
	select {
	case stalledAMQP = <-amqpAccepted:
		defer stalledAMQP.Close()
	case <-time.After(time.Second):
		t.Fatal("connector did not begin AMQP handshake")
	}
	stopped := make(chan error, 1)
	unbindHandled := make(chan error, 1)
	go func() {
		request, readErr := smppwire.Read(smppServer, 1024)
		if readErr != nil {
			unbindHandled <- readErr
			return
		}
		response, encodeErr := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
			CommandID: smppwire.CommandUnbindResp, SequenceNumber: request.Header.SequenceNumber,
		}})
		if encodeErr == nil {
			_, encodeErr = smppServer.Write(response)
		}
		unbindHandled <- encodeErr
	}()
	go func() { stopped <- connector.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel stalled AMQP handshake")
	}
	if err := <-unbindHandled; err != nil {
		t.Fatal(err)
	}
}

func TestConnectorStopCancelsPendingBindRead(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID: "stop-bind", Host: address.IP.String(), Port: address.Port,
		SystemID: "client", Password: "password", TrxTimeout: 30,
	}
	connector, err := smppc.NewConnector(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	if err := connector.Start(); err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer server.Close()
	if _, err := smppwire.Read(server, 1024); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- connector.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked on bind response read")
	}
}
