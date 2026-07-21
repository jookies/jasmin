package smppc

import (
	"context"
	"errors"
	"math"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestNewConnectorRejectsInvalidPacingConfiguration(t *testing.T) {
	throughput := math.NaN()
	_, err := NewConnector(Config{CID: "invalid-pacing", SubmitSMThroughput: &throughput}, "")
	if !errors.Is(err, ErrInvalidThroughput) {
		t.Fatalf("NewConnector error = %v, want ErrInvalidThroughput", err)
	}
}

func TestConnectorUnlimitedPacingDoesNotUseClock(t *testing.T) {
	throughput := 0.0
	connector, err := NewConnector(Config{CID: "unlimited-pacing", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakePacingClock{now: time.Unix(100, 0).UTC()}
	connector.pacer, err = newPacer(throughput, clock)
	if err != nil {
		t.Fatal(err)
	}
	connector.setStatus(StatusBound)

	deliveries := make(chan *amqpcompat.Delivery, 1)
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries}
	settled := make(chan pacingSettlement, 1)
	expired := map[string]amqpcompat.Field{
		"expiration": amqpcompat.StringField(time.Unix(1, 0).UTC().Format(time.RFC3339)),
	}
	deliveries <- newPacingDelivery(t, "unlimited-expired", expired, settled)
	close(deliveries)
	connector.runConsumer(context.Background(), nil)

	select {
	case settlement := <-settled:
		if settlement.action != "reject" || settlement.requeue {
			t.Fatalf("settlement = %+v, want discard", settlement)
		}
	case <-time.After(time.Second):
		t.Fatal("unlimited delivery was not settled")
	}
	if waits := clock.recordedWaits(); len(waits) != 0 {
		t.Fatalf("unlimited connector used pacing clock: %v", waits)
	}
}

func TestConnectorPacingErrorRequeuesOnceAndStopsBeforeNextDelivery(t *testing.T) {
	throughput := 2.0
	connector, err := NewConnector(Config{CID: "pacing-error", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	pacingFailure := errors.New("pacing clock failed")
	clock := &fakePacingClock{now: time.Unix(100, 0).UTC(), waitErr: pacingFailure}
	connector.pacer, err = newPacer(throughput, clock)
	if err != nil {
		t.Fatal(err)
	}
	deliveries := make(chan *amqpcompat.Delivery, 2)
	settled := make(chan pacingSettlement, 2)
	deliveries <- newPacingDelivery(t, "pacing-error-1", nil, settled)
	deliveries <- newPacingDelivery(t, "pacing-error-2", nil, settled)
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries}
	connector.runConsumer(context.Background(), nil)

	select {
	case settlement := <-settled:
		if settlement.action != "reject" || !settlement.requeue {
			t.Fatalf("pacing error settlement = %+v, want requeue", settlement)
		}
	case <-time.After(time.Second):
		t.Fatal("pacing error did not settle owned delivery")
	}
	select {
	case duplicate := <-settled:
		t.Fatalf("consumer processed or settled another delivery after pacing error: %+v", duplicate)
	default:
	}
	if !connector.pacer.last.IsZero() {
		t.Fatalf("pacing error advanced timestamp to %s", connector.pacer.last)
	}
	if waits := clock.recordedWaits(); len(waits) != 1 {
		t.Fatalf("pacing error waits = %v, want exactly one", waits)
	}
}

func TestConnectorPacerStateSurvivesConsumerRestart(t *testing.T) {
	throughput := 2.0
	connector, err := NewConnector(Config{CID: "pacing-restart", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakePacingClock{now: time.Unix(100, 0).UTC()}
	connector.pacer, err = newPacer(throughput, clock)
	if err != nil {
		t.Fatal(err)
	}
	connector.setStatus(StatusBound)
	expired := map[string]amqpcompat.Field{
		"expiration": amqpcompat.StringField(time.Unix(1, 0).UTC().Format(time.RFC3339)),
	}
	settled := make(chan pacingSettlement, 2)

	first := make(chan *amqpcompat.Delivery, 1)
	first <- newPacingDelivery(t, "restart-1", expired, settled)
	close(first)
	connector.amqp = &pacingAMQPProvider{deliveries: first}
	connector.runConsumer(context.Background(), nil)
	clock.advance(100 * time.Millisecond)
	second := make(chan *amqpcompat.Delivery, 1)
	second <- newPacingDelivery(t, "restart-2", expired, settled)
	close(second)
	connector.amqp = &pacingAMQPProvider{deliveries: second}
	connector.runConsumer(context.Background(), nil)

	for index := 0; index < 2; index++ {
		select {
		case settlement := <-settled:
			if settlement.action != "reject" || settlement.requeue {
				t.Fatalf("restart settlement %d = %+v, want discard", index, settlement)
			}
		case <-time.After(time.Second):
			t.Fatalf("restart delivery %d was not settled", index)
		}
	}
	waits := clock.recordedWaits()
	want := []time.Duration{0, 400 * time.Millisecond}
	if len(waits) != len(want) {
		t.Fatalf("restart waits = %v, want %v", waits, want)
	}
	for index := range want {
		if waits[index] != want[index] {
			t.Fatalf("restart wait[%d] = %s, want %s", index, waits[index], want[index])
		}
	}
}

func TestNewConnectorOwnsEffectivePacingConfiguration(t *testing.T) {
	throughput := 2.0
	connector, err := NewConnector(Config{CID: "owned-pacing", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	throughput = 9
	if connector.pacer.throughput != 2 {
		t.Fatalf("pacer throughput changed through input alias: %v", connector.pacer.throughput)
	}
	if got := connector.Config().EffectiveSubmitSMThroughput(); got != 2 {
		t.Fatalf("owned config throughput = %v, want 2", got)
	}
	defaultConnector, err := NewConnector(Config{CID: "default-pacing"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if defaultConnector.pacer.throughput != DefaultSubmitSMThroughput {
		t.Fatalf("default pacer throughput = %v, want %v", defaultConnector.pacer.throughput, DefaultSubmitSMThroughput)
	}
}

func TestConnectorProductionPacerGatesRealSubmitWrites(t *testing.T) {
	throughput := 10.0
	connector, err := NewConnector(Config{CID: "real-pacing", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	connector.setStatus(StatusBound)

	client, server := net.Pipe()
	defer server.Close()
	retry, _ := NewErrorRetryPolicy(DefaultErrorRetryRules())
	readiness, _ := NewReadinessPolicy(DefaultReadinessConfig())
	session := NewSession(client, Config{CID: "real-pacing", ResTimeout: 2}, retry, readiness, nil)
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
	deliveries <- newPacingDelivery(t, "real-submit-1", nil, settled)
	deliveries <- newPacingDelivery(t, "real-submit-2", nil, settled)
	close(deliveries)
	consumerDone := make(chan struct{})
	go func() {
		connector.runConsumer(sessionCtx, session)
		close(consumerDone)
	}()

	first, err := smppwire.Read(server, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Now()
	second, err := smppwire.Read(server, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(firstAt); elapsed < 70*time.Millisecond {
		t.Fatalf("second submit arrived after %s, want production pacing near 100ms", elapsed)
	}

	for _, request := range []smppwire.PDU{first, second} {
		response, encodeErr := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{
				CommandID:      smppwire.CommandSubmitSMResp,
				SequenceNumber: request.Header.SequenceNumber,
			},
			SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte("smsc-id")},
		})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if _, writeErr := server.Write(response); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	for index := 0; index < 2; index++ {
		select {
		case settlement := <-settled:
			if settlement.action != "ack" || settlement.requeue {
				t.Fatalf("settlement %d = %+v, want ack", index, settlement)
			}
		case <-time.After(time.Second):
			t.Fatalf("submit %d was not settled", index)
		}
	}
	select {
	case <-consumerDone:
	case <-time.After(time.Second):
		t.Fatal("consumer did not finish after real paced submissions")
	}
}
