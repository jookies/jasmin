package amqpcompat

import (
	"context"
	"testing"
)

// TestTopologyDurability drives every declaration path in both modes and
// asserts the durable bit lands on each exchange_declare/queue_declare.
// durable=false is the legacy txamqp default (the golden fixture pins it);
// durable=true is the Go-native hardening so queued submits survive a broker
// restart. Bindings and consumes carry no durability and are ignored here.
func TestTopologyDurability(t *testing.T) {
	paths := []struct {
		name string
		open func(topology *Topology, channel *recordingTopologyChannel) error
	}{
		{"router_subscriptions", func(topology *Topology, _ *recordingTopologyChannel) error {
			_, err := topology.OpenRouterSubscriptions(context.Background())
			return err
		}},
		{"dlr_thrower_subscription", func(topology *Topology, _ *recordingTopologyChannel) error {
			_, err := topology.OpenDLRThrowerSubscription(context.Background())
			return err
		}},
		{"mo_thrower_subscription", func(topology *Topology, _ *recordingTopologyChannel) error {
			_, err := topology.OpenMOThrowerSubscription(context.Background())
			return err
		}},
		{"dlr_lookup_subscription", func(topology *Topology, _ *recordingTopologyChannel) error {
			_, err := topology.OpenDLRLookupSubscription(context.Background(), "main")
			return err
		}},
		{"declare_exchanges", func(topology *Topology, _ *recordingTopologyChannel) error {
			return topology.Declare(context.Background())
		}},
		{"declare_connector_queue", func(topology *Topology, _ *recordingTopologyChannel) error {
			return topology.DeclareQueue(context.Background(), ConnectorSubmitQueue("cid"), "messaging", ConnectorSubmitRoutingKey("cid"))
		}},
	}
	for _, durable := range []bool{false, true} {
		for _, path := range paths {
			name := path.name + "/durable=false"
			if durable {
				name = path.name + "/durable=true"
			}
			t.Run(name, func(t *testing.T) {
				channel := newRecordingTopologyChannel()
				topology := newTopology(func() (topologyChannel, error) { return channel, nil }, durable)
				if err := path.open(topology, channel); err != nil {
					t.Fatal(err)
				}
				declares := 0
				for _, operation := range channel.operations {
					kind, _ := operation["operation"].(string)
					if kind != "exchange_declare" && kind != "queue_declare" {
						continue
					}
					declares++
					if got, _ := operation["durable"].(bool); got != durable {
						t.Fatalf("%s %v: durable=%v want %v", kind, operation["exchange"], got, durable)
					}
				}
				if declares == 0 {
					t.Fatal("path recorded no declarations")
				}
			})
		}
	}
}
