package smppc_test

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func injectAMQPProvider(c *smppc.Connector, provider smppc.AMQPProvider) {
	v := reflect.ValueOf(c).Elem()
	f := v.FieldByName("amqp")
	ptr := reflect.NewAt(f.Type(), f.Addr().UnsafePointer()).Elem()
	ptr.Set(reflect.ValueOf(provider))
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

	c, err := smppc.NewConnector(cfg, "amqp://guest:guest@localhost:5672/")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer c.Stop()

	select {
	case conn := <-connChan:
		defer conn.Close()
		// Verify BIND PDU
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
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connection")
	}

	waitForConnectorStatus(t, c, smppc.StatusBound, 2*time.Second)
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
	deliveries chan *amqpcompat.Delivery
}

func (p *mockAMQPProvider) Consume(ctx context.Context, amqpURL, cid string) (<-chan *amqpcompat.Delivery, error) {
	return p.deliveries, nil
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
	
	go c.RunConsumer(ctx)
	
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
