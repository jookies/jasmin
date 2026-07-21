package smppc

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
	amqp "github.com/rabbitmq/amqp091-go"
)

type pacingAMQPProvider struct {
	deliveries <-chan *amqpcompat.Delivery
	done       <-chan struct{}
}

func (p *pacingAMQPProvider) Consume(context.Context, string, string) (AMQPDeliveryStream, error) {
	return AMQPDeliveryStream{Deliveries: p.deliveries, Done: p.done}, nil
}

type pacingSettlement struct {
	action  string
	requeue bool
}

type pacingAcknowledger struct {
	settled chan<- pacingSettlement
}

func (a *pacingAcknowledger) Ack(uint64, bool) error {
	a.settled <- pacingSettlement{action: "ack"}
	return nil
}

func (a *pacingAcknowledger) Nack(_ uint64, _ bool, requeue bool) error {
	a.settled <- pacingSettlement{action: "nack", requeue: requeue}
	return nil
}

func (a *pacingAcknowledger) Reject(_ uint64, requeue bool) error {
	a.settled <- pacingSettlement{action: "reject", requeue: requeue}
	return nil
}

func newPacingDelivery(t *testing.T, id string, headers map[string]amqpcompat.Field, settled chan<- pacingSettlement) *amqpcompat.Delivery {
	t.Helper()
	properties, err := amqpcompat.NewProperties(id, headers)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm.pacing", properties, []byte(id))
	if err != nil {
		t.Fatal(err)
	}
	raw := amqp.Delivery{
		Acknowledger: &pacingAcknowledger{settled: settled},
		DeliveryTag:  1,
		MessageId:    envelope.Properties().MessageID(),
		RoutingKey:   envelope.RoutingKey(),
		Body:         envelope.Body(),
		Headers:      make(amqp.Table),
	}
	for key, value := range envelope.Properties().Headers() {
		text, ok := value.String()
		if !ok {
			t.Fatalf("header %q is not a string", key)
		}
		raw.Headers[key] = text
	}
	delivery, err := amqpcompat.NewDelivery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return delivery
}

func TestConnectorPacesBeforeReadinessDiscard(t *testing.T) {
	throughput := 2.0
	connector, err := NewConnector(Config{CID: "pacing-order", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakePacingClock{now: time.Unix(100, 0).UTC()}
	pacer, err := newPacer(throughput, clock)
	if err != nil {
		t.Fatal(err)
	}
	connector.pacer = pacer
	connector.setStatus(StatusBound)

	deliveries := make(chan *amqpcompat.Delivery, 2)
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries}
	settled := make(chan pacingSettlement, 2)
	expired := map[string]amqpcompat.Field{
		"expiration": amqpcompat.StringField(time.Unix(1, 0).UTC().Format(time.RFC3339)),
	}
	deliveries <- newPacingDelivery(t, "expired-1", expired, settled)

	done := make(chan struct{})
	go func() {
		connector.runConsumer(context.Background(), nil)
		close(done)
	}()
	select {
	case settlement := <-settled:
		if settlement.action != "reject" || settlement.requeue {
			t.Fatalf("first expired settlement = %+v, want reject without requeue", settlement)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first readiness discard")
	}
	clock.advance(100 * time.Millisecond)
	deliveries <- newPacingDelivery(t, "expired-2", expired, settled)
	close(deliveries)
	select {
	case settlement := <-settled:
		if settlement.action != "reject" || settlement.requeue {
			t.Fatalf("second expired settlement = %+v, want reject without requeue", settlement)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second readiness discard")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not finish after delivery stream closed")
	}
	waits := clock.recordedWaits()
	want := []time.Duration{0, 400 * time.Millisecond}
	if len(waits) != len(want) {
		t.Fatalf("pacing waits = %v, want %v", waits, want)
	}
	for index := range want {
		if waits[index] != want[index] {
			t.Fatalf("pacing wait[%d] = %s, want %s", index, waits[index], want[index])
		}
	}
}

func TestConnectorDisabledThroughputDoesNotUsePacingClock(t *testing.T) {
	throughput := 0.0
	connector, err := NewConnector(Config{CID: "pacing-disabled", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakePacingClock{now: time.Unix(100, 0).UTC()}
	pacer, err := newPacer(throughput, clock)
	if err != nil {
		t.Fatal(err)
	}
	connector.pacer = pacer
	connector.setStatus(StatusBound)

	deliveries := make(chan *amqpcompat.Delivery, 1)
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries}
	settled := make(chan pacingSettlement, 1)
	expired := map[string]amqpcompat.Field{
		"expiration": amqpcompat.StringField(time.Unix(1, 0).UTC().Format(time.RFC3339)),
	}
	deliveries <- newPacingDelivery(t, "disabled", expired, settled)
	close(deliveries)
	done := make(chan struct{})
	go func() {
		connector.runConsumer(context.Background(), nil)
		close(done)
	}()
	select {
	case settlement := <-settled:
		if settlement.action != "reject" || settlement.requeue {
			t.Fatalf("disabled-throughput settlement = %+v, want readiness discard", settlement)
		}
	case <-time.After(time.Second):
		t.Fatal("disabled-throughput delivery was not processed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disabled-throughput consumer did not finish")
	}
	if waits := clock.recordedWaits(); len(waits) != 0 {
		t.Fatalf("disabled-throughput production consumer waits = %v, want none", waits)
	}
}

type cancelPacingClock struct {
	started chan<- struct{}
	once    sync.Once
	now     time.Time
}

func (c *cancelPacingClock) Now() time.Time { return c.now }
func (c *cancelPacingClock) Wait(ctx context.Context, _ time.Duration) error {
	c.once.Do(func() { c.started <- struct{}{} })
	<-ctx.Done()
	return ctx.Err()
}

func TestConnectorPacingCancellationRequeuesOnceAndStopsConsumer(t *testing.T) {
	throughput := 1.0
	connector, err := NewConnector(Config{CID: "pacing-cancel", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	pacer, err := newPacer(throughput, &cancelPacingClock{started: started, now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	connector.pacer = pacer
	connector.setStatus(StatusBound)

	deliveries := make(chan *amqpcompat.Delivery, 1)
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries}
	settled := make(chan pacingSettlement, 2)
	deliveries <- newPacingDelivery(t, "cancel-owned", nil, settled)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	session := NewSession(client, Config{}, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		connector.runConsumer(ctx, session)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("consumer did not enter pacing wait")
	}
	cancel()
	select {
	case settlement := <-settled:
		if settlement.action != "reject" || !settlement.requeue {
			t.Fatalf("canceled pacing settlement = %+v, want reject with requeue", settlement)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled pacing did not settle owned delivery")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pacing cancellation did not stop consumer")
	}
	select {
	case duplicate := <-settled:
		t.Fatalf("delivery settled more than once: %+v", duplicate)
	default:
	}
	if !pacer.last.IsZero() {
		t.Fatalf("canceled pacing advanced timestamp to %s", pacer.last)
	}
	if err := server.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Read(make([]byte, 1)); err == nil {
		t.Fatal("pacing cancellation unexpectedly wrote to SMPP socket")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("SMPP socket read error = %v, want timeout proving no write", err)
	}
}

func TestConnectorPacedSubmitStillSettlesOnCorrelatedResponse(t *testing.T) {
	throughput := 2.0
	connector, err := NewConnector(Config{CID: "paced-submit", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakePacingClock{now: time.Unix(100, 0).UTC()}
	pacer, err := newPacer(throughput, clock)
	if err != nil {
		t.Fatal(err)
	}
	connector.pacer = pacer
	connector.setStatus(StatusBound)

	client, server := net.Pipe()
	defer server.Close()
	retry, _ := NewErrorRetryPolicy(DefaultErrorRetryRules())
	readiness, _ := NewReadinessPolicy(DefaultReadinessConfig())
	session := NewSession(client, Config{ResTimeout: 1}, retry, readiness, nil)
	sessionCtx, stopSession := context.WithCancel(context.Background())
	sessionDone := make(chan error, 1)
	go func() { sessionDone <- session.Run(sessionCtx) }()
	defer func() {
		stopSession()
		_ = server.Close()
		<-sessionDone
	}()

	deliveries := make(chan *amqpcompat.Delivery, 2)
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries}
	settled := make(chan pacingSettlement, 2)
	deliveries <- newPacingDelivery(t, "submit-1", nil, settled)
	deliveries <- newPacingDelivery(t, "submit-2", nil, settled)
	consumerDone := make(chan struct{})
	go func() {
		connector.runConsumer(sessionCtx, session)
		close(consumerDone)
	}()

	for index := 1; index <= 2; index++ {
		pdu, readErr := smppwire.Read(server, smppwire.DefaultMaxSize)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if pdu.Header.CommandID != smppwire.CommandSubmitSM {
			t.Fatalf("PDU %d command = %#x, want submit_sm", index, pdu.Header.CommandID)
		}
		response, encodeErr := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
			CommandID: smppwire.CommandSubmitSMResp, SequenceNumber: pdu.Header.SequenceNumber,
		}, SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte("smsc-id")}})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if _, writeErr := server.Write(response); writeErr != nil {
			t.Fatal(writeErr)
		}
		select {
		case settlement := <-settled:
			if settlement.action != "ack" || settlement.requeue {
				t.Fatalf("submit %d settlement = %+v, want ack", index, settlement)
			}
		case <-time.After(time.Second):
			t.Fatalf("submit %d was not settled by correlated response", index)
		}
	}
	close(deliveries)
	select {
	case <-consumerDone:
	case <-time.After(time.Second):
		t.Fatal("consumer did not finish after paced submits")
	}
	waits := clock.recordedWaits()
	want := []time.Duration{0, 500 * time.Millisecond}
	if len(waits) != len(want) {
		t.Fatalf("pacing waits = %v, want %v", waits, want)
	}
	for index := range want {
		if waits[index] != want[index] {
			t.Fatalf("pacing wait[%d] = %s, want %s", index, waits[index], want[index])
		}
	}
}
