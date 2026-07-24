package amqpcompat

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The legacy DLRLookup.subscribe declares the messaging exchange, a
// DLRLookup-<pid> named queue bound to dlr.*, and a named manual-ack consumer.
func TestOpenDLRLookupSubscriptionDeclaresLegacyTopology(t *testing.T) {
	channel := newRecordingTopologyChannel()
	topology := newTopology(func() (topologyChannel, error) { return channel, nil })

	subscription, err := topology.OpenDLRLookupSubscription(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if subscription.ConsumerTag != "DLRLookup-main" {
		t.Fatalf("consumer tag = %q", subscription.ConsumerTag)
	}
	want := []map[string]any{
		{"operation": "exchange_declare", "exchange": "messaging", "type": "topic"},
		{"operation": "queue_declare", "queue": "DLRLookup-main"},
		{"operation": "queue_bind", "queue": "DLRLookup-main", "exchange": "messaging", "routing_key": "dlr.*"},
		{"operation": "basic_consume", "queue": "DLRLookup-main", "consumer_tag": "DLRLookup-main", "auto_ack": false},
	}
	if len(channel.operations) != len(want) {
		t.Fatalf("operations = %d, want %d: %v", len(channel.operations), len(want), channel.operations)
	}
	for index, expected := range want {
		got := channel.operations[index]
		for key, value := range expected {
			if got[key] != value {
				t.Errorf("operation %d %s = %v, want %v (%v)", index, key, got[key], value, got)
			}
		}
	}
	if subscription.Deliveries == nil {
		t.Fatal("nil delivery stream")
	}
	if err := subscription.Close(); err != nil {
		t.Fatal(err)
	}
	if channel.closeCalls != 1 {
		t.Fatalf("close calls = %d", channel.closeCalls)
	}
}

func TestOpenDLRLookupSubscriptionClosesChannelOnFailure(t *testing.T) {
	for operation := 1; operation <= 4; operation++ {
		channel := newRecordingTopologyChannel()
		channel.failAt = operation
		topology := newTopology(func() (topologyChannel, error) { return channel, nil })
		subscription, err := topology.OpenDLRLookupSubscription(context.Background(), "main")
		if subscription != nil {
			t.Fatalf("operation %d: got subscription despite failure", operation)
		}
		if !errors.Is(err, errTopologyFixture) {
			t.Fatalf("operation %d: error = %v", operation, err)
		}
		if channel.closeCalls != 1 {
			t.Fatalf("operation %d: close calls = %d", operation, channel.closeCalls)
		}
	}
}

func TestDLRLookupNamesMatchLegacy(t *testing.T) {
	if DLRLookupQueue("main") != "DLRLookup-main" || DLRLookupConsumerTag("worker2") != "DLRLookup-worker2" {
		t.Fatal("queue/tag naming diverges from the legacy DLRLookup-<pid> form")
	}
	if !strings.EqualFold(DLRLookupRoutingKey, "dlr.*") {
		t.Fatalf("routing key = %q", DLRLookupRoutingKey)
	}
}
