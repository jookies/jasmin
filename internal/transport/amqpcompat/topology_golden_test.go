package amqpcompat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

var errTopologyFixture = errors.New("topology fixture failure")

type topologyGoldenDocument struct {
	Cases []struct {
		Expected struct {
			Operations   []map[string]any `json:"operations"`
			QueueLookups []string         `json:"queue_lookups"`
		} `json:"expected"`
	} `json:"cases"`
}

type recordingTopologyChannel struct {
	operations []map[string]any
	consumers  map[string]chan amqp.Delivery
	closeCalls int
	closeErr   error
	failAt     int
}

func newRecordingTopologyChannel() *recordingTopologyChannel {
	return &recordingTopologyChannel{consumers: make(map[string]chan amqp.Delivery)}
}

func (channel *recordingTopologyChannel) record(operation map[string]any) error {
	channel.operations = append(channel.operations, operation)
	if channel.failAt > 0 && len(channel.operations) == channel.failAt {
		return errTopologyFixture
	}
	return nil
}

func (channel *recordingTopologyChannel) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, arguments amqp.Table) error {
	return channel.record(map[string]any{
		"operation": "exchange_declare", "exchange": name, "type": kind,
		"passive": false, "durable": durable, "auto_delete": autoDelete,
		"internal": internal, "no_wait": noWait, "arguments": arguments,
	})
}

func (channel *recordingTopologyChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, arguments amqp.Table) (amqp.Queue, error) {
	err := channel.record(map[string]any{
		"operation": "queue_declare", "queue": name, "durable": durable,
		"exclusive": exclusive, "auto_delete": autoDelete, "no_wait": noWait,
		"arguments": arguments,
	})
	return amqp.Queue{Name: name}, err
}

func (channel *recordingTopologyChannel) QueueBind(name, key, exchange string, noWait bool, arguments amqp.Table) error {
	return channel.record(map[string]any{
		"operation": "queue_bind", "queue": name, "exchange": exchange,
		"routing_key": key, "no_wait": noWait, "arguments": arguments,
	})
}

func (channel *recordingTopologyChannel) Qos(prefetchCount, prefetchSize int, global bool) error {
	channel.operations = append(channel.operations, map[string]any{
		"operation": "basic_qos", "prefetch_count": prefetchCount,
		"prefetch_size": prefetchSize, "global": global,
	})
	if channel.failAt > 0 && len(channel.operations) == channel.failAt {
		return errTopologyFixture
	}
	return nil
}

func (channel *recordingTopologyChannel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, arguments amqp.Table) (<-chan amqp.Delivery, error) {
	if err := channel.record(map[string]any{
		"operation": "basic_consume", "queue": queue, "consumer_tag": consumer,
		"auto_ack": autoAck, "exclusive": exclusive, "no_local": noLocal,
		"no_wait": noWait, "arguments": arguments,
	}); err != nil {
		return nil, err
	}
	deliveries := make(chan amqp.Delivery)
	channel.consumers[consumer] = deliveries
	return deliveries, nil
}

func (channel *recordingTopologyChannel) Close() error {
	channel.closeCalls++
	return channel.closeErr
}

func TestRouterSubscriptionsGoldenNoSkip(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "router-amqp-subscriptions", "baseline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document topologyGoldenDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Cases) != 1 {
		t.Fatalf("executed cases=0 total=%d: fixture must contain exactly one case", len(document.Cases))
	}
	for index, testCase := range document.Cases {
		channel := newRecordingTopologyChannel()
		subscriptions, err := declareRouterSubscriptions(context.Background(), channel)
		if err != nil {
			t.Fatalf("case %d: %v", index, err)
		}
		normalizedRaw, err := json.Marshal(channel.operations)
		if err != nil {
			t.Fatalf("case %d normalize operations: %v", index, err)
		}
		var normalized []map[string]any
		if err := json.Unmarshal(normalizedRaw, &normalized); err != nil {
			t.Fatalf("case %d decode normalized operations: %v", index, err)
		}
		if !reflect.DeepEqual(normalized, testCase.Expected.Operations) {
			got, _ := json.MarshalIndent(channel.operations, "", "  ")
			want, _ := json.MarshalIndent(testCase.Expected.Operations, "", "  ")
			t.Fatalf("case %d operations mismatch\ngot: %s\nwant: %s", index, got, want)
		}
		lookups := []string{subscriptions.DeliverSMConsumerTag, subscriptions.BillingConsumerTag}
		if !reflect.DeepEqual(lookups, testCase.Expected.QueueLookups) {
			t.Fatalf("case %d queue lookups=%v want %v", index, lookups, testCase.Expected.QueueLookups)
		}
		if subscriptions.DeliverSM != channel.consumers[RouterDeliverSMConsumerTag] {
			t.Fatalf("case %d deliver stream is not owned by %s", index, RouterDeliverSMConsumerTag)
		}
		if subscriptions.Billing != channel.consumers[RouterBillingConsumerTag] {
			t.Fatalf("case %d billing stream is not owned by %s", index, RouterBillingConsumerTag)
		}
	}
}
