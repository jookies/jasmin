package smppc

import (
	"context"
	"errors"
	"math"
	"net"
	"sync"
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

func TestPacerCancellationWinningTimerBoundaryDoesNotAdvanceCursor(t *testing.T) {
	type cancelNilClock struct {
		now    time.Time
		cancel context.CancelFunc
	}
	clock := &cancelNilClock{now: time.Unix(100, 0).UTC()}
	ctx, cancel := context.WithCancel(context.Background())
	clock.cancel = cancel
	pacer, err := newPacer(2, pacingClockFunc{
		now: func() time.Time { return clock.now },
		wait: func(context.Context, time.Duration) error {
			clock.cancel()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pacer.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}
	if !pacer.last.IsZero() {
		t.Fatalf("timer/cancel boundary advanced cursor to %s", pacer.last)
	}
}

type pacingClockFunc struct {
	now  func() time.Time
	wait func(context.Context, time.Duration) error
}

func (c pacingClockFunc) Now() time.Time { return c.now() }
func (c pacingClockFunc) Wait(ctx context.Context, delay time.Duration) error {
	return c.wait(ctx, delay)
}

func TestConnectorConsumerLossDuringPacingCannotSubmitOrSettleStaleDelivery(t *testing.T) {
	throughput := 2.0
	connector, err := NewConnector(Config{CID: "consumer-loss-pacing", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	release := make(chan struct{})
	clock := pacingClockFunc{
		now: func() time.Time { return time.Unix(100, 0).UTC() },
		wait: func(ctx context.Context, _ time.Duration) error {
			started <- struct{}{}
			<-ctx.Done()
			canceled <- struct{}{}
			<-release
			// Adversarial timer winner: the clock reports success after the
			// consumer-liveness context has already been canceled.
			return nil
		},
	}
	connector.pacer, err = newPacer(throughput, clock)
	if err != nil {
		t.Fatal(err)
	}
	connector.setStatus(StatusBound)

	deliveries := make(chan *amqpcompat.Delivery, 1)
	consumerDone := make(chan struct{})
	settled := make(chan pacingSettlement, 1)
	deliveries <- newPacingDelivery(t, "stale-owned", nil, settled)
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries, done: consumerDone}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	session := NewSession(client, Config{}, nil, nil, nil)

	runDone := make(chan struct{})
	go func() {
		connector.runConsumer(context.Background(), session)
		close(runDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("consumer did not enter pacing wait")
	}
	close(consumerDone)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("consumer loss did not cancel pacing context")
	}
	close(release)
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop after ownership loss")
	}
	select {
	case settlement := <-settled:
		t.Fatalf("stale broker delivery received explicit settlement: %+v", settlement)
	default:
	}
	if !connector.pacer.last.IsZero() {
		t.Fatalf("consumer loss advanced pacing cursor to %s", connector.pacer.last)
	}
	if err := server.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err == nil {
		if n, readErr := server.Read(make([]byte, 1)); readErr == nil || n != 0 {
			t.Fatalf("consumer loss allowed stale SMPP socket data: bytes=%d error=%v", n, readErr)
		}
	}
}

type abortableBlockingConn struct {
	net.Conn
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newAbortableBlockingConn(conn net.Conn) *abortableBlockingConn {
	return &abortableBlockingConn{Conn: conn, started: make(chan struct{}), closed: make(chan struct{})}
}

func (c *abortableBlockingConn) Write([]byte) (int, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-c.closed
	return 0, net.ErrClosed
}

func (c *abortableBlockingConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.Conn.Close()
	})
	return nil
}

func TestConnectorClosedDeliveryStreamAlwaysFencesSession(t *testing.T) {
	for attempt := 0; attempt < 10_000; attempt++ {
		connector, err := NewConnector(Config{CID: "closed-stream-fence"}, "")
		if err != nil {
			t.Fatal(err)
		}
		deliveries := make(chan *amqpcompat.Delivery)
		done := make(chan struct{})
		close(done)
		close(deliveries)
		connector.amqp = &pacingAMQPProvider{deliveries: deliveries, done: done}
		client, server := net.Pipe()
		session := NewSession(client, Config{}, nil, nil, nil)
		connector.runConsumer(context.Background(), session)
		if !session.consumerGenerationLost() {
			_ = client.Close()
			_ = server.Close()
			t.Fatalf("attempt %d: closed stream did not fence session", attempt)
		}
		_ = client.Close()
		_ = server.Close()
	}
}

func TestConnectorConsumerLossDuringReadinessCannotSettleDeadGeneration(t *testing.T) {
	throughput := 2.0
	connector, err := NewConnector(Config{CID: "consumer-loss-readiness", SubmitSMThroughput: &throughput}, "")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	clock := pacingClockFunc{
		now: func() time.Time { return time.Unix(100, 0).UTC() },
		wait: func(context.Context, time.Duration) error {
			started <- struct{}{}
			<-release
			return nil
		},
	}
	connector.pacer, err = newPacer(throughput, clock)
	if err != nil {
		t.Fatal(err)
	}
	connector.setStatus(StatusDisconnected) // readiness will choose requeue.

	deliveries := make(chan *amqpcompat.Delivery, 1)
	consumerDone := make(chan struct{})
	settled := make(chan pacingSettlement, 1)
	delivery := newPacingDelivery(t, "readiness-stale", nil, settled)
	deliveries <- delivery
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries, done: consumerDone}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	session := NewSession(client, Config{}, nil, nil, nil)

	runDone := make(chan struct{})
	go func() {
		connector.runConsumer(context.Background(), session)
		close(runDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("delivery did not reach pacing gate")
	}
	connector.mu.Lock() // release pacing into a blocked Status/readiness read.
	close(release)
	// The connector mutex keeps the worker inside the current delivery after
	// pacing; allow it to reach the blocked Status read before losing ownership.
	time.Sleep(20 * time.Millisecond)
	close(consumerDone)
	deadline := time.Now().Add(time.Second)
	for !session.consumerGenerationLost() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !session.consumerGenerationLost() {
		connector.mu.Unlock()
		t.Fatal("consumer generation was not fenced during readiness")
	}
	connector.mu.Unlock()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("consumer did not exit after readiness-generation loss")
	}
	select {
	case settlement := <-settled:
		t.Fatalf("dead generation received readiness settlement: %+v", settlement)
	default:
	}
	if err := delivery.Ack(); !errors.Is(err, amqpcompat.ErrDeliverySettled) {
		t.Fatalf("delivery was not locally abandoned: Ack error = %v", err)
	}
}

func TestConnectorConsumerLossAbortsInFlightSubmitWrite(t *testing.T) {
	throughput := 2.0
	connector, err := NewConnector(Config{CID: "consumer-loss-write", SubmitSMThroughput: &throughput}, "")
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
	consumerDone := make(chan struct{})
	settled := make(chan pacingSettlement, 1)
	delivery := newPacingDelivery(t, "in-flight-stale", nil, settled)
	deliveries <- delivery
	connector.amqp = &pacingAMQPProvider{deliveries: deliveries, done: consumerDone}
	client, server := net.Pipe()
	defer server.Close()
	blockingConn := newAbortableBlockingConn(client)
	defer blockingConn.Close()
	session := NewSession(blockingConn, Config{}, nil, nil, nil)

	runDone := make(chan struct{})
	go func() {
		connector.runConsumer(context.Background(), session)
		close(runDone)
	}()
	select {
	case <-blockingConn.started:
	case <-time.After(time.Second):
		t.Fatal("submit did not enter blocked SMPP write")
	}
	close(consumerDone)
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("consumer loss did not abort in-flight SMPP write")
	}
	select {
	case settlement := <-settled:
		t.Fatalf("dead consumer generation received settlement: %+v", settlement)
	default:
	}
	if err := delivery.Ack(); !errors.Is(err, amqpcompat.ErrDeliverySettled) {
		t.Fatalf("delivery was not locally abandoned: Ack error = %v", err)
	}
	if !session.consumerGenerationLost() {
		t.Fatal("session was not fenced after AMQP consumer loss")
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
	close(deliveries)
	select {
	case <-consumerDone:
	case <-time.After(time.Second):
		t.Fatal("consumer did not finish after real paced submissions")
	}
}
