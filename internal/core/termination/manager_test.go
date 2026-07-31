package termination

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

// managerStubProvider is the AMQP seam under test control. It hands out one
// stream per Consume call and records how many generations were established, so
// a test can distinguish "consumed once" from "reconnected in a loop".
type managerStubProvider struct {
	mu         sync.Mutex
	deliveries chan *amqpcompat.Delivery
	done       chan struct{}
	consumes   int
	consumed   chan struct{}
	err        error
}

func newManagerStubProvider() *managerStubProvider {
	return &managerStubProvider{
		deliveries: make(chan *amqpcompat.Delivery, 4),
		done:       make(chan struct{}),
		consumed:   make(chan struct{}, 4),
	}
}

func (p *managerStubProvider) Consume(_ context.Context, _, _ string) (smppc.AMQPDeliveryStream, error) {
	p.mu.Lock()
	p.consumes++
	err := p.err
	p.mu.Unlock()
	if err != nil {
		return smppc.AMQPDeliveryStream{}, err
	}
	select {
	case p.consumed <- struct{}{}:
	default:
	}
	return smppc.AMQPDeliveryStream{Deliveries: p.deliveries, Done: p.done}, nil
}

// managerStubAcknowledger stands in for the broker's settlement channel.
// observe runs inside the settlement, which is what lets a test assert what was
// true at the instant of the ack rather than afterwards.
type managerStubAcknowledger struct {
	mu       sync.Mutex
	acks     int
	rejects  int
	requeued bool
	observe  func(operation string)
	settled  chan string
}

func newManagerStubAcknowledger() *managerStubAcknowledger {
	return &managerStubAcknowledger{settled: make(chan string, 4)}
}

func (a *managerStubAcknowledger) record(operation string) {
	a.mu.Lock()
	switch operation {
	case "ack":
		a.acks++
	case "reject":
		a.rejects++
	}
	observe := a.observe
	a.mu.Unlock()
	if observe != nil {
		observe(operation)
	}
	select {
	case a.settled <- operation:
	default:
	}
}

func (a *managerStubAcknowledger) Ack(uint64, bool) error {
	a.record("ack")
	return nil
}

func (a *managerStubAcknowledger) Nack(_ uint64, _, requeue bool) error {
	a.mu.Lock()
	a.requeued = requeue
	a.mu.Unlock()
	a.record("reject")
	return nil
}

func (a *managerStubAcknowledger) Reject(_ uint64, requeue bool) error {
	a.mu.Lock()
	a.requeued = requeue
	a.mu.Unlock()
	a.record("reject")
	return nil
}

func (a *managerStubAcknowledger) counts() (acks, rejects int, requeued bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.acks, a.rejects, a.requeued
}

func newManagerStubDelivery(t *testing.T, cid, messageID string, ack amqp091.Acknowledger) *amqpcompat.Delivery {
	t.Helper()
	delivery, err := amqpcompat.NewDelivery(amqp091.Delivery{
		Acknowledger: ack,
		MessageId:    messageID,
		RoutingKey:   amqpcompat.ConnectorSubmitRoutingKey(cid),
		Body:         []byte("pickled-submit"),
	})
	if err != nil {
		t.Fatalf("build delivery: %v", err)
	}
	return delivery
}

// managerStubSpool is the worker's SpoolWriter under test control. before runs
// inside Record, which is how the lost-generation test arranges for the broker
// connection to die between the decode and the commit.
type managerStubSpool struct {
	mu       sync.Mutex
	recorded []Message
	err      error
	before   func()
}

func (s *managerStubSpool) Record(_ context.Context, msg Message, _ Verdict, _ time.Time) error {
	s.mu.Lock()
	before := s.before
	err := s.err
	s.mu.Unlock()
	if before != nil {
		before()
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.recorded = append(s.recorded, msg)
	s.mu.Unlock()
	return nil
}

// RecordReceiptOnly is unreachable in these tests — they drive whole messages
// through a pass-through assembler — but it fails loudly rather than silently
// if that ever stops being true.
func (s *managerStubSpool) RecordReceiptOnly(context.Context, Message, Verdict, time.Time) error {
	return errors.New("managerStubSpool: unexpected per-segment receipt")
}

func (s *managerStubSpool) rows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recorded)
}

func newManagerTestConfig(cid string) ConnectorConfig {
	return ConnectorConfig{CID: cid, Verdict: VerdictConfig{Source: SourceStatic}}
}

// newManagerTestConnector wires a connector over the stub provider and a worker
// whose submit decoder, content decoder, assembler and verdict source are the
// stubs from connector_test.go.
func newManagerTestConnector(
	t *testing.T,
	cfg ConnectorConfig,
	provider AMQPProvider,
	spool *managerStubSpool,
	failures chan<- error,
) *Connector {
	t.Helper()
	fixed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	leg, err := NewSMSCLeg(&workerStubPublisher{}, cfg.CID, func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("new smsc leg: %v", err)
	}
	worker, err := NewWorker(
		WorkerConfig{CID: cfg.CID, ReceiptDelay: cfg.ReceiptDelay, ReceiptJitter: cfg.ReceiptJitter},
		workerStubSubmits{body: smppwire.SubmitSMBody{
			SourceAddress:      []byte("NETFLIX"),
			DestinationAddress: []byte("+380671234567"),
			ShortMessage:       []byte("code 63125"),
			DataCoding:         8,
		}},
		workerStubContent{text: "code 63125", encoding: "ucs2"},
		workerStubAssembler{complete: true},
		&workerStubVerdicts{verdict: Verdict{Accept: true, Stat: StatDelivered, Err: ReceiptErrNone}},
		spool,
		leg,
		func() time.Time { return fixed },
		func() float64 { return 0.5 },
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	connector, err := NewConnector(cfg, "amqp://stub", worker, ConnectorOptions{
		AMQP:         provider,
		RetryDelay:   10 * time.Millisecond,
		FailurePause: 10 * time.Millisecond,
		jitter:       func() float64 { return 0 },
		OnError: func(err error) {
			if failures == nil {
				return
			}
			select {
			case failures <- err:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	return connector
}

func waitForStatus(t *testing.T, connector *Connector, want Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if connector.Status() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("connector [%s] status = %s, want %s", connector.CID(), connector.Status(), want)
}

func waitForSettlement(t *testing.T, ack *managerStubAcknowledger) string {
	t.Helper()
	select {
	case operation := <-ack.settled:
		return operation
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the delivery to be settled")
		return ""
	}
}

// TestConnectorAcksOnlyAfterSpoolCommit is the settlement contract: the queue
// may forget a message only once its spool row is committed. The assertion runs
// inside the ack, so it cannot be satisfied by a row that appears afterwards.
func TestConnectorAcksOnlyAfterSpoolCommit(t *testing.T) {
	provider := newManagerStubProvider()
	spool := &managerStubSpool{}
	ack := newManagerStubAcknowledger()

	var (
		mu           sync.Mutex
		rowsAtSettle int
	)
	ack.observe = func(string) {
		mu.Lock()
		rowsAtSettle = spool.rows()
		mu.Unlock()
	}

	connector := newManagerTestConnector(t, newManagerTestConfig("partner-a-term"), provider, spool, nil)
	if err := connector.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = connector.Stop() }()
	waitForStatus(t, connector, StatusConsuming)

	provider.deliveries <- newManagerStubDelivery(t, "partner-a-term", "msg-1", ack)

	if operation := waitForSettlement(t, ack); operation != "ack" {
		t.Fatalf("settlement = %q, want ack", operation)
	}
	mu.Lock()
	observed := rowsAtSettle
	mu.Unlock()
	if observed != 1 {
		t.Fatalf("spool rows at ack = %d, want 1: the ack must follow the spool commit", observed)
	}
	acks, rejects, _ := ack.counts()
	if acks != 1 || rejects != 0 {
		t.Fatalf("acks=%d rejects=%d, want exactly one ack", acks, rejects)
	}
}

// TestConnectorRequeuesWhenProcessFails is the other half of the contract: a
// message whose spool write failed must stay on the queue. Acking it would
// destroy a message that was already charged and already promised a receipt.
func TestConnectorRequeuesWhenProcessFails(t *testing.T) {
	provider := newManagerStubProvider()
	spool := &managerStubSpool{err: errors.New("spool unavailable")}
	ack := newManagerStubAcknowledger()
	failures := make(chan error, 4)

	connector := newManagerTestConnector(t, newManagerTestConfig("partner-a-term"), provider, spool, failures)
	if err := connector.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = connector.Stop() }()
	waitForStatus(t, connector, StatusConsuming)

	provider.deliveries <- newManagerStubDelivery(t, "partner-a-term", "msg-1", ack)

	if operation := waitForSettlement(t, ack); operation != "reject" {
		t.Fatalf("settlement = %q, want reject", operation)
	}
	acks, rejects, requeued := ack.counts()
	if acks != 0 {
		t.Fatalf("acks = %d, want 0: a failed process must leave the delivery unacknowledged", acks)
	}
	if rejects != 1 || !requeued {
		t.Fatalf("rejects=%d requeue=%t, want one requeueing reject", rejects, requeued)
	}
	if spool.rows() != 0 {
		t.Fatalf("spool rows = %d, want none", spool.rows())
	}
	select {
	case err := <-failures:
		if err == nil {
			t.Fatal("want the process failure reported")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the process failure was never reported")
	}
}

// TestConnectorSettlesTerminalFailuresWithoutRequeue is the other side of the
// requeue rule. A concatenated segment reaching the pass-through assembler
// fails identically on every redelivery, so requeueing it would starve the
// connector with one poison message while the partner's other traffic waits
// behind it.
func TestConnectorSettlesTerminalFailuresWithoutRequeue(t *testing.T) {
	provider := newManagerStubProvider()
	spool := &managerStubSpool{}
	ack := newManagerStubAcknowledger()
	failures := make(chan error, 4)

	cfg := newManagerTestConfig("partner-a-term")
	fixed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	leg, err := NewSMSCLeg(&workerStubPublisher{}, cfg.CID, func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("new smsc leg: %v", err)
	}
	// A real UDH: 05 00 03 <ref> <total> <seq>, segment 1 of 2.
	segment := []byte{0x05, 0x00, 0x03, 0x2A, 0x02, 0x01, 'h', 'i'}
	worker, err := NewWorker(
		WorkerConfig{CID: cfg.CID},
		workerStubSubmits{body: smppwire.SubmitSMBody{
			SourceAddress:      []byte("NETFLIX"),
			DestinationAddress: []byte("380671234567"),
			ShortMessage:       segment,
		}},
		workerStubContent{text: "hi", encoding: "gsm7"},
		// The real assembler, so this exercises the refusal rather than a stub
		// of it.
		PassThroughAssembler{},
		&workerStubVerdicts{verdict: Verdict{Accept: true, Stat: StatDelivered, Err: ReceiptErrNone}},
		spool,
		leg,
		func() time.Time { return fixed },
		func() float64 { return 0 },
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	connector, err := NewConnector(cfg, "amqp://stub", worker, ConnectorOptions{
		AMQP:         provider,
		RetryDelay:   10 * time.Millisecond,
		FailurePause: 10 * time.Millisecond,
		jitter:       func() float64 { return 0 },
		OnError: func(err error) {
			select {
			case failures <- err:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	if err := connector.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = connector.Stop() }()
	waitForStatus(t, connector, StatusConsuming)

	provider.deliveries <- newManagerStubDelivery(t, cfg.CID, "msg-1", ack)

	if operation := waitForSettlement(t, ack); operation != "reject" {
		t.Fatalf("settlement = %q, want reject", operation)
	}
	acks, rejects, requeued := ack.counts()
	if acks != 0 {
		t.Fatalf("acks = %d, want 0: a terminal failure is not a delivery", acks)
	}
	if rejects != 1 {
		t.Fatalf("rejects = %d, want 1", rejects)
	}
	if requeued {
		t.Fatal("a terminal failure must be settled WITHOUT requeue, or one poison message starves the connector")
	}
	if spool.rows() != 0 {
		t.Fatalf("spool rows = %d, want none", spool.rows())
	}
	select {
	case err := <-failures:
		if !errors.Is(err, ErrMultipartUnsupported) {
			t.Fatalf("reported error = %v, want ErrMultipartUnsupported", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the terminal failure was never reported")
	}
}

// TestConnectorAbandonsWhenGenerationIsLost proves a delivery is neither acked
// nor rejected on a torn-down consumer generation: the broker owns redelivery,
// and settling on a dead channel would only produce an error.
func TestConnectorAbandonsWhenGenerationIsLost(t *testing.T) {
	provider := newManagerStubProvider()
	spool := &managerStubSpool{}
	ack := newManagerStubAcknowledger()

	release := make(chan struct{})
	spool.before = func() {
		close(provider.done)
		<-release
	}

	connector := newManagerTestConnector(t, newManagerTestConfig("partner-a-term"), provider, spool, nil)
	if err := connector.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = connector.Stop() }()
	waitForStatus(t, connector, StatusConsuming)

	provider.deliveries <- newManagerStubDelivery(t, "partner-a-term", "msg-1", ack)
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if spool.rows() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// Give the connector a moment to settle if it were going to.
	time.Sleep(50 * time.Millisecond)
	acks, rejects, _ := ack.counts()
	if acks != 0 || rejects != 0 {
		t.Fatalf("acks=%d rejects=%d, want neither on a lost generation", acks, rejects)
	}
}

// TestManagerAvailableFollowsLifecycle is what routing consults: a stopped
// termination connector must drop out of selection exactly as a stopped SMPP
// client connector does.
func TestManagerAvailableFollowsLifecycle(t *testing.T) {
	provider := newManagerStubProvider()
	manager := newTestManager(t, provider)

	cfg := newManagerTestConfig("partner-a-term")
	if err := manager.Add(cfg); err != nil {
		t.Fatalf("add: %v", err)
	}
	if manager.Available(cfg.CID) {
		t.Fatal("an added but unstarted connector must not be routable")
	}
	if err := manager.Start(cfg.CID); err != nil {
		t.Fatalf("start: %v", err)
	}
	connector, err := manager.Get(cfg.CID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	waitForStatus(t, connector, StatusConsuming)
	if !manager.Available(cfg.CID) {
		t.Fatal("a started, consuming connector must be routable")
	}
	status, err := manager.Status(cfg.CID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Desired || status.Observed != StatusConsuming {
		t.Fatalf("status = %+v, want desired and consuming", status)
	}
	if err := manager.Stop(cfg.CID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if manager.Available(cfg.CID) {
		t.Fatal("a stopped connector must drop out of route selection")
	}
	if manager.Available("nonexistent") {
		t.Fatal("an unknown cid is never available")
	}
}

func TestManagerAddGetListRemove(t *testing.T) {
	provider := newManagerStubProvider()
	manager := newTestManager(t, provider)

	if err := manager.Add(newManagerTestConfig("b-term")); err != nil {
		t.Fatalf("add b: %v", err)
	}
	if err := manager.Add(newManagerTestConfig("a-term")); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := manager.Add(newManagerTestConfig("a-term")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate add error = %v, want ErrAlreadyExists", err)
	}
	if err := manager.Add(ConnectorConfig{CID: ""}); !errors.Is(err, ErrInvalidConnectorConfig) {
		t.Fatalf("invalid add error = %v, want ErrInvalidConnectorConfig", err)
	}
	list := manager.List()
	if len(list) != 2 || list[0].CID != "a-term" || list[1].CID != "b-term" {
		t.Fatalf("list = %+v, want sorted [a-term b-term]", list)
	}
	// Defaults are applied on the way in, so a stored connector reports the
	// timings it actually runs with rather than zeros.
	if list[0].ReceiptDelay != DefaultReceiptDelay || list[0].ReceiptJitter != DefaultReceiptJitter {
		t.Fatalf("defaults not applied: %+v", list[0])
	}
	if _, err := manager.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing error = %v, want ErrNotFound", err)
	}
	if err := manager.Remove("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing error = %v, want ErrNotFound", err)
	}

	if err := manager.Start("a-term"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := manager.Remove("a-term"); err == nil {
		t.Fatal("removing a running connector must be refused")
	}
	if err := manager.Stop("a-term"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := manager.Remove("a-term"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(manager.List()) != 1 {
		t.Fatalf("list after removal = %+v, want one entry", manager.List())
	}
}

// TestManagerReservedConnectorsAreImmutable protects a config-declared
// connector from the admin API: deleting one would only make it return at the
// next restart, and the operator would not know why.
func TestManagerReservedConnectorsAreImmutable(t *testing.T) {
	provider := newManagerStubProvider()
	manager := newTestManager(t, provider)

	cfg := newManagerTestConfig("config-term")
	if err := manager.Add(cfg); err != nil {
		t.Fatalf("add: %v", err)
	}
	manager.Reserve(cfg.CID)

	if !manager.Reserved(cfg.CID) {
		t.Fatal("cid must report as reserved")
	}
	if got := manager.ReservedCIDs(); len(got) != 1 || got[0] != cfg.CID {
		t.Fatalf("reserved cids = %v, want [%s]", got, cfg.CID)
	}
	if err := manager.Remove(cfg.CID); !errors.Is(err, ErrReserved) {
		t.Fatalf("remove reserved error = %v, want ErrReserved", err)
	}
	if err := manager.Update(cfg); !errors.Is(err, ErrReserved) {
		t.Fatalf("update reserved error = %v, want ErrReserved", err)
	}
	status, err := manager.Status(cfg.CID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Reserved {
		t.Fatal("status must report the connector as reserved")
	}
	// Lifecycle control is still allowed: an operator must be able to take a
	// misbehaving config connector out of routing without editing a file.
	if err := manager.Start(cfg.CID); err != nil {
		t.Fatalf("start reserved: %v", err)
	}
	if err := manager.Stop(cfg.CID); err != nil {
		t.Fatalf("stop reserved: %v", err)
	}
}

func TestManagerUpdateReplacesAndPreservesDesiredState(t *testing.T) {
	provider := newManagerStubProvider()
	manager := newTestManager(t, provider)

	cfg := newManagerTestConfig("partner-a-term")
	if err := manager.Add(cfg); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := manager.Start(cfg.CID); err != nil {
		t.Fatalf("start: %v", err)
	}
	original, err := manager.Get(cfg.CID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	waitForStatus(t, original, StatusConsuming)

	updated := cfg
	updated.ReceiptDelay = 9 * time.Second
	if err := manager.Update(updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	replacement, err := manager.Get(cfg.CID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if replacement == original {
		t.Fatal("update must install a new connector, not mutate the running one")
	}
	if replacement.Config().ReceiptDelay != 9*time.Second {
		t.Fatalf("receipt delay = %s, want the updated value", replacement.Config().ReceiptDelay)
	}
	waitForStatus(t, replacement, StatusConsuming)
	if !manager.Available(cfg.CID) {
		t.Fatal("a connector that was running before the update must be running after it")
	}
	if err := manager.Update(newManagerTestConfig("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing error = %v, want ErrNotFound", err)
	}
	if err := manager.StopAll(); err != nil {
		t.Fatalf("stop all: %v", err)
	}
}

func TestManagerStartAllStopAll(t *testing.T) {
	provider := newManagerStubProvider()
	manager := newTestManager(t, provider)

	for _, cid := range []string{"a-term", "b-term"} {
		if err := manager.Add(newManagerTestConfig(cid)); err != nil {
			t.Fatalf("add %s: %v", cid, err)
		}
	}
	if err := manager.StartAll(); err != nil {
		t.Fatalf("start all: %v", err)
	}
	for _, cid := range []string{"a-term", "b-term"} {
		connector, err := manager.Get(cid)
		if err != nil {
			t.Fatalf("get %s: %v", cid, err)
		}
		waitForStatus(t, connector, StatusConsuming)
	}
	stats := manager.Stats()
	if stats.Total != 2 || stats.Desired != 2 || stats.Consuming != 2 {
		t.Fatalf("stats = %+v, want two desired and consuming", stats)
	}
	if err := manager.StopAll(); err != nil {
		t.Fatalf("stop all: %v", err)
	}
	for _, cid := range []string{"a-term", "b-term"} {
		if manager.Available(cid) {
			t.Fatalf("%s must not be routable after StopAll", cid)
		}
	}
}

// TestConnectorRetriesAFailedConsumer proves an unreachable broker leaves the
// connector CONNECTING and out of routing rather than silently idle.
func TestConnectorRetriesAFailedConsumer(t *testing.T) {
	provider := newManagerStubProvider()
	provider.err = errors.New("broker unreachable")
	failures := make(chan error, 4)
	connector := newManagerTestConnector(t, newManagerTestConfig("partner-a-term"), provider, &managerStubSpool{}, failures)

	if err := connector.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = connector.Stop() }()

	select {
	case <-failures:
	case <-time.After(2 * time.Second):
		t.Fatal("the consumer failure was never reported")
	}
	if connector.Status() == StatusConsuming {
		t.Fatal("a connector whose consumer failed must not report as consuming")
	}

	// It keeps retrying rather than giving up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		consumes := provider.consumes
		provider.mu.Unlock()
		if consumes > 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the connector never retried its consumer")
}

func TestNewManagerValidation(t *testing.T) {
	if _, err := NewManager("", func(ConnectorConfig, string) (*Connector, error) { return nil, nil }); err == nil {
		t.Fatal("an empty AMQP URL must be refused")
	}
	if _, err := NewManager("amqp://stub", nil); err == nil {
		t.Fatal("a nil factory must be refused")
	}
}

func TestNewConnectorValidation(t *testing.T) {
	cases := map[string]struct {
		cfg     ConnectorConfig
		amqpURL string
		worker  *Worker
	}{
		"invalid config": {cfg: ConnectorConfig{}, amqpURL: "amqp://stub"},
		"empty url":      {cfg: newManagerTestConfig("a-term"), amqpURL: ""},
		"nil worker":     {cfg: newManagerTestConfig("a-term"), amqpURL: "amqp://stub"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewConnector(testCase.cfg, testCase.amqpURL, testCase.worker, ConnectorOptions{}); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func newTestManager(t *testing.T, provider AMQPProvider) *Manager {
	t.Helper()
	manager, err := NewManager("amqp://stub", func(cfg ConnectorConfig, _ string) (*Connector, error) {
		return newManagerTestConnector(t, cfg, provider, &managerStubSpool{}, nil), nil
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.StopAll() })
	return manager
}
