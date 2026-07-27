package amqpcompat

import (
	"context"
	"errors"
	"strings"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

var errTopologyCloseFixture = errors.New("topology close fixture failure")

type cancelOnBillingConsumeChannel struct {
	*recordingTopologyChannel
	cancel context.CancelFunc
}

func (channel *cancelOnBillingConsumeChannel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, arguments amqp.Table) (<-chan amqp.Delivery, error) {
	deliveries, err := channel.recordingTopologyChannel.Consume(queue, consumer, autoAck, exclusive, noLocal, noWait, arguments)
	if err == nil && consumer == RouterBillingConsumerTag {
		channel.cancel()
	}
	return deliveries, err
}

func TestOpenRouterSubscriptionsStopsAndClosesOnEveryOperationFailure(t *testing.T) {
	expectedContext := []struct {
		operation string
		context   string
	}{
		{"declare exchange messaging", "declare exchange messaging"},
		{"declare queue RouterPB_deliver_sm_all", "declare queue RouterPB_deliver_sm_all"},
		{"bind queue RouterPB_deliver_sm_all", "bind queue RouterPB_deliver_sm_all"},
		{"consume queue RouterPB_deliver_sm_all", "consume queue RouterPB_deliver_sm_all"},
		{"declare exchange billing", "declare exchange billing"},
		{"declare queue RouterPB_bill_request_submit_sm_resp_all", "declare queue RouterPB_bill_request_submit_sm_resp_all"},
		{"bind queue RouterPB_bill_request_submit_sm_resp_all", "bind queue RouterPB_bill_request_submit_sm_resp_all"},
		{"consume queue RouterPB_bill_request_submit_sm_resp_all", "consume queue RouterPB_bill_request_submit_sm_resp_all"},
	}
	for operation := 1; operation <= len(expectedContext); operation++ {
		testCase := expectedContext[operation-1]
		t.Run(testCase.operation, func(t *testing.T) {
			channel := newRecordingTopologyChannel()
			channel.failAt = operation
			topology := newTopology(func() (topologyChannel, error) { return channel, nil }, false)
			subscriptions, err := topology.OpenRouterSubscriptions(context.Background())
			if subscriptions != nil {
				t.Fatal("failed declaration returned subscriptions")
			}
			if !errors.Is(err, errTopologyFixture) {
				t.Fatalf("error=%v want wrapped fixture failure", err)
			}
			if !strings.Contains(err.Error(), testCase.context) {
				t.Fatalf("error=%q missing operation context %q", err, testCase.context)
			}
			if got := len(channel.operations); got != operation {
				t.Fatalf("operations=%d want %d", got, operation)
			}
			if channel.closeCalls != 1 {
				t.Fatalf("close calls=%d want 1", channel.closeCalls)
			}
		})
	}
}

func TestOpenRouterSubscriptionsRejectsCancelledContextBeforeOpeningChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	openCalls := 0
	topology := newTopology(func() (topologyChannel, error) {
		openCalls++
		return newRecordingTopologyChannel(), nil
	}, false)
	if _, err := topology.OpenRouterSubscriptions(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context cancellation", err)
	}
	if openCalls != 0 {
		t.Fatalf("open calls=%d want 0", openCalls)
	}
}

func TestOpenRouterSubscriptionsWrapsOpenFailure(t *testing.T) {
	topology := newTopology(func() (topologyChannel, error) { return nil, errTopologyFixture }, false)
	if _, err := topology.OpenRouterSubscriptions(context.Background()); !errors.Is(err, errTopologyFixture) {
		t.Fatalf("error=%v want wrapped open failure", err)
	} else if !strings.Contains(err.Error(), "open RouterPB topology channel") {
		t.Fatalf("error=%q missing open context", err)
	}
}

func TestOpenRouterSubscriptionsObservesCancellationAfterFinalConsume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	recorder := newRecordingTopologyChannel()
	channel := &cancelOnBillingConsumeChannel{recordingTopologyChannel: recorder, cancel: cancel}
	topology := newTopology(func() (topologyChannel, error) { return channel, nil }, false)
	if subscriptions, err := topology.OpenRouterSubscriptions(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("subscriptions=%v error=%v want context cancellation", subscriptions, err)
	}
	if recorder.closeCalls != 1 {
		t.Fatalf("close calls=%d want 1", recorder.closeCalls)
	}
}

func TestOpenRouterSubscriptionsPreservesOperationAndCleanupFailures(t *testing.T) {
	channel := newRecordingTopologyChannel()
	channel.failAt = 3
	channel.closeErr = errTopologyCloseFixture
	topology := newTopology(func() (topologyChannel, error) { return channel, nil }, false)
	_, err := topology.OpenRouterSubscriptions(context.Background())
	if !errors.Is(err, errTopologyFixture) || !errors.Is(err, errTopologyCloseFixture) {
		t.Fatalf("error=%v does not preserve operation and cleanup failures", err)
	}
}

func TestTopologyHelpersReturnCloseFailure(t *testing.T) {
	for _, testCase := range []struct {
		name string
		run  func(*Topology) error
	}{
		{name: "declare", run: func(topology *Topology) error { return topology.Declare(context.Background()) }},
		{name: "declare queue", run: func(topology *Topology) error {
			return topology.DeclareQueue(context.Background(), "queue", "messaging", "route")
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			channel := newRecordingTopologyChannel()
			channel.closeErr = errTopologyCloseFixture
			topology := newTopology(func() (topologyChannel, error) { return channel, nil }, false)
			if err := testCase.run(topology); !errors.Is(err, errTopologyCloseFixture) {
				t.Fatalf("error=%v want close failure", err)
			}
		})
	}
}

func TestRouterSubscriptionsOwnsChannelUntilClose(t *testing.T) {
	channel := newRecordingTopologyChannel()
	topology := newTopology(func() (topologyChannel, error) { return channel, nil }, false)
	subscriptions, err := topology.OpenRouterSubscriptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if channel.closeCalls != 0 {
		t.Fatalf("channel closed before owner: %d", channel.closeCalls)
	}
	if subscriptions.DeliverSM == nil || subscriptions.Billing == nil {
		t.Fatal("missing delivery stream")
	}
	if err := subscriptions.Close(); err != nil {
		t.Fatal(err)
	}
	if channel.closeCalls != 1 {
		t.Fatalf("close calls=%d want 1", channel.closeCalls)
	}
}
