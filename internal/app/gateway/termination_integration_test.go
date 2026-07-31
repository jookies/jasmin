package gateway_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
	_ "modernc.org/sqlite"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/termination"
	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/infra/storage"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// terminationStubSubmits stands in for the pickle codec. The codec itself is
// covered by its own differential suite; what this test exercises is the path
// between a broker delivery and a committed spool row.
type terminationStubSubmits struct{ err error }

func (d terminationStubSubmits) DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	if d.err != nil {
		return smppwire.SubmitSMBody{}, nil, d.err
	}
	return smppwire.SubmitSMBody{
		SourceAddress:      []byte("NETFLIX"),
		DestinationAddress: []byte("380671234567"),
		ShortMessage:       []byte("code 63125"),
		DataCoding:         8,
	}, nil, nil
}

// failingSpool wraps a working repository and refuses to write, which is what a
// PostgreSQL blip looks like from the connector's side.
type failingSpool struct {
	termination.SpoolStore
}

func (failingSpool) Put(context.Context, msgspool.Message, time.Time) (msgspool.Record, error) {
	return msgspool.Record{}, errors.New("spool unavailable")
}

func terminationTestCID(t *testing.T) string {
	t.Helper()
	var token [6]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	return "term-" + hex.EncodeToString(token[:])
}

func openTerminationBroker(t *testing.T, amqpURL string) *amqp091.Connection {
	t.Helper()
	conn, err := amqp091.Dial(amqpURL)
	if err != nil {
		t.Fatalf("RabbitMQ unavailable with AMQP_URL set: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// declareTerminationTopology declares the connector's submit queue and a
// DLRLookup queue. The second is not optional: the synthesized submit_sm_resp
// leg is published mandatory, so without a queue bound to dlr.* the publish is
// returned and the message never leaves the queue.
//
// The lookup pid is per test rather than "main" so this never redeclares a
// running deployment's durable queue, which the broker answers with
// PRECONDITION_FAILED.
func declareTerminationTopology(t *testing.T, conn *amqp091.Connection, cid, lookupPID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	topology := amqpcompat.NewTopology(conn, false)
	if err := topology.DeclareQueue(ctx, amqpcompat.ConnectorSubmitQueue(cid), "messaging",
		amqpcompat.ConnectorSubmitRoutingKey(cid)); err != nil {
		t.Fatalf("declare submit queue: %v", err)
	}
	if err := topology.DeclareQueue(ctx, amqpcompat.DLRLookupQueue(lookupPID), "messaging",
		amqpcompat.DLRLookupRoutingKey); err != nil {
		t.Fatalf("declare DLRLookup queue: %v", err)
	}
	t.Cleanup(func() {
		channel, err := conn.Channel()
		if err != nil {
			return
		}
		defer func() { _ = channel.Close() }()
		_, _ = channel.QueueDelete(amqpcompat.ConnectorSubmitQueue(cid), false, false, false)
		_, _ = channel.QueueDelete(amqpcompat.DLRLookupQueue(lookupPID), false, false, false)
	})
}

func openTerminationSpool(t *testing.T) msgspool.Repository {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/spool.db")
	if err != nil {
		t.Fatalf("open spool: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := storage.NewSQLiteMessageSpool(db)
	if err != nil {
		t.Fatalf("build spool: %v", err)
	}
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init spool: %v", err)
	}
	return store
}

func newTerminationTestConnector(
	t *testing.T,
	conn *amqp091.Connection,
	cid string,
	store termination.SpoolStore,
	onError func(error),
) *termination.Connector {
	t.Helper()
	publisher, err := amqpcompat.NewPublisher(conn)
	if err != nil {
		t.Fatalf("new publisher: %v", err)
	}
	t.Cleanup(func() { _ = publisher.Close() })
	leg, err := termination.NewSMSCLeg(publisher, cid, time.Now)
	if err != nil {
		t.Fatalf("new smsc leg: %v", err)
	}
	verdicts, err := termination.NewVerdictSource(
		termination.VerdictConfig{Source: termination.SourceStatic}, termination.Dependencies{})
	if err != nil {
		t.Fatalf("new verdict source: %v", err)
	}
	spool, err := termination.NewSpool(store, time.Now, nil)
	if err != nil {
		t.Fatalf("new spool adapter: %v", err)
	}
	worker, err := termination.NewWorker(
		termination.WorkerConfig{CID: cid, ReceiptDelay: time.Second},
		terminationStubSubmits{},
		termination.NewDefaultMsgContentDecoder(),
		termination.PassThroughAssembler{},
		verdicts,
		spool,
		leg,
		time.Now,
		nil,
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	connector, err := termination.NewConnector(
		termination.ConnectorConfig{CID: cid, Verdict: termination.VerdictConfig{Source: termination.SourceStatic}},
		os.Getenv("AMQP_URL"),
		worker,
		termination.ConnectorOptions{
			RetryDelay:   100 * time.Millisecond,
			FailurePause: 100 * time.Millisecond,
			OnError:      onError,
		},
	)
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	return connector
}

func publishTerminationSubmit(t *testing.T, conn *amqp091.Connection, cid, messageID string) {
	t.Helper()
	publisher, err := amqpcompat.NewPublisher(conn)
	if err != nil {
		t.Fatalf("new publisher: %v", err)
	}
	defer func() { _ = publisher.Close() }()
	properties, err := amqpcompat.NewProperties(messageID, map[string]amqpcompat.Field{
		"user-id": amqpcompat.StringField("partner-a"),
	})
	if err != nil {
		t.Fatalf("build properties: %v", err)
	}
	envelope, err := amqpcompat.NewEnvelope(amqpcompat.ConnectorSubmitRoutingKey(cid), properties, []byte("pickled-submit"))
	if err != nil {
		t.Fatalf("build envelope: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := publisher.Publish(ctx, "messaging", envelope.RoutingKey(), envelope); err != nil {
		t.Fatalf("publish submit: %v", err)
	}
}

// queueDepth reports how many messages are ready on a queue. Unacknowledged
// deliveries are excluded by the broker, which is precisely why this is the
// measurement that separates "acked" from "still held".
func queueDepth(t *testing.T, conn *amqp091.Connection, queue string) int {
	t.Helper()
	channel, err := conn.Channel()
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}
	defer func() { _ = channel.Close() }()
	declared, err := channel.QueueDeclarePassive(queue, false, false, false, false, nil)
	if err != nil {
		t.Fatalf("inspect queue %s: %v", queue, err)
	}
	return declared.Messages
}

// TestTerminationConnectorAcksOnlyAfterSpoolCommitOnRealBroker is the Step 5
// verification against RabbitMQ: one queued submit produces exactly one spool
// row, and only then is the delivery acknowledged.
func TestTerminationConnectorAcksOnlyAfterSpoolCommitOnRealBroker(t *testing.T) {
	amqpURL := os.Getenv("AMQP_URL")
	if amqpURL == "" {
		t.Skip("AMQP_URL is not set; live RabbitMQ integration is opt-in")
	}
	conn := openTerminationBroker(t, amqpURL)
	cid := terminationTestCID(t)
	declareTerminationTopology(t, conn, cid, cid)

	store := openTerminationSpool(t)
	connector := newTerminationTestConnector(t, conn, cid, store, func(err error) {
		t.Errorf("unexpected connector failure: %v", err)
	})
	if err := connector.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = connector.Stop() }()

	publishTerminationSubmit(t, conn, cid, "term-msg-1")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	record := waitForSpoolRow(t, ctx, store, "term-msg-1")
	if record.DestAddr != "380671234567" {
		t.Errorf("destination = %q", record.DestAddr)
	}
	if record.Verdict.Stat != termination.StatDelivered {
		t.Errorf("verdict = %q, want DELIVRD from the static source", record.Verdict.Stat)
	}
	if record.ReceiptDueAt == nil {
		t.Error("the spool row must carry when the receipt is owed")
	}

	// Stop first: an unacknowledged delivery is only requeued once the channel
	// closes, so measuring depth while the connector holds it proves nothing.
	if err := connector.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if depth := queueDepth(t, conn, amqpcompat.ConnectorSubmitQueue(cid)); depth != 0 {
		t.Fatalf("queue depth = %d after a committed spool row, want 0 (the delivery must be acked)", depth)
	}

	// Redelivery is idempotent by construction: the message id is the spool's
	// primary key and the SMSC id derives from it.
	publishTerminationSubmit(t, conn, cid, "term-msg-1")
	if err := connector.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if queueDepth(t, conn, amqpcompat.ConnectorSubmitQueue(cid)) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	page, err := store.Search(ctx, msgspool.Query{Limit: 10})
	if err != nil {
		t.Fatalf("search spool: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("spool rows = %d after a redelivery, want exactly 1", len(page))
	}
}

// TestTerminationConnectorRequeuesOnSpoolFailureOnRealBroker proves the message
// survives a spool outage: it is neither acked nor lost, and the broker still
// holds it once the connector lets go.
func TestTerminationConnectorRequeuesOnSpoolFailureOnRealBroker(t *testing.T) {
	amqpURL := os.Getenv("AMQP_URL")
	if amqpURL == "" {
		t.Skip("AMQP_URL is not set; live RabbitMQ integration is opt-in")
	}
	conn := openTerminationBroker(t, amqpURL)
	cid := terminationTestCID(t)
	declareTerminationTopology(t, conn, cid, cid)

	store := openTerminationSpool(t)
	failures := make(chan error, 8)
	connector := newTerminationTestConnector(t, conn, cid, failingSpool{store}, func(err error) {
		select {
		case failures <- err:
		default:
		}
	})
	if err := connector.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = connector.Stop() }()

	publishTerminationSubmit(t, conn, cid, "term-msg-2")

	select {
	case <-failures:
	case <-time.After(15 * time.Second):
		t.Fatal("the spool failure was never reported")
	}
	if err := connector.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if queueDepth(t, conn, amqpcompat.ConnectorSubmitQueue(cid)) == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("queue depth = %d after a failed spool write, want 1 (the message must not be acked)",
		queueDepth(t, conn, amqpcompat.ConnectorSubmitQueue(cid)))
}

func waitForSpoolRow(t *testing.T, ctx context.Context, store msgspool.Repository, messageID string) msgspool.Record {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		record, err := store.Get(ctx, messageID, true)
		if err == nil {
			return record
		}
		if !errors.Is(err, msgspool.ErrNotFound) {
			t.Fatalf("read spool row: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("spool row %q never appeared", messageID)
	return msgspool.Record{}
}
