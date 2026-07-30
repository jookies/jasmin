package modispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
)

type fakeBridge struct {
	repickled []byte
	fields    picklecompat.RoutableFields
	repickErr error
	lists     [][]picklecompat.MOConnectorSpec
	listErr   error
}

func (b *fakeBridge) RepickleRoutablePDU(context.Context, []byte) ([]byte, picklecompat.RoutableFields, error) {
	return b.repickled, b.fields, b.repickErr
}

func (b *fakeBridge) EncodeConnectorList(_ context.Context, connectors []picklecompat.MOConnectorSpec) ([]byte, error) {
	b.lists = append(b.lists, connectors)
	if b.listErr != nil {
		return nil, b.listErr
	}
	return []byte("pickled:" + connectors[0].Type), nil
}

type fakePublisher struct {
	exchange   string
	routingKey string
	envelope   amqpcompat.Envelope
	calls      int
	err        error
}

func (p *fakePublisher) Publish(_ context.Context, exchange, routingKey string, envelope amqpcompat.Envelope) error {
	p.calls++
	p.exchange, p.routingKey, p.envelope = exchange, routingKey, envelope
	return p.err
}

// fakeAcknowledger records the settlement outcome of one delivery.
type fakeAcknowledger struct {
	acked    bool
	rejected bool
	requeue  bool
}

func (a *fakeAcknowledger) Ack(uint64, bool) error { a.acked = true; return nil }
func (a *fakeAcknowledger) Nack(_ uint64, _ bool, requeue bool) error {
	a.rejected, a.requeue = true, requeue
	return nil
}
func (a *fakeAcknowledger) Reject(_ uint64, requeue bool) error {
	a.rejected, a.requeue = true, requeue
	return nil
}

func moDelivery(t *testing.T, sourceCID string) (*amqpcompat.Delivery, *fakeAcknowledger) {
	t.Helper()
	return moDeliveryWithMarkers(t, sourceCID, false, false)
}

func moDeliveryWithMarkers(t *testing.T, sourceCID string, concatenated, willBeConcatenated bool) (*amqpcompat.Delivery, *fakeAcknowledger) {
	t.Helper()
	acknowledger := &fakeAcknowledger{}
	headers := amqp.Table{
		"try-count":            int64(0),
		"concatenated":         concatenated,
		"will_be_concatenated": willBeConcatenated,
	}
	routingCID := sourceCID
	if sourceCID != "" {
		headers["connector-id"] = sourceCID
	} else {
		// A malformed producer: valid routing key, missing header.
		routingCID = "some-src"
	}
	delivery, err := amqpcompat.NewDelivery(amqp.Delivery{
		Acknowledger: acknowledger,
		MessageId:    "mo-msgid-1",
		RoutingKey:   "deliver.sm." + routingCID,
		Headers:      headers,
		Body:         []byte("routable-pickle"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return delivery, acknowledger
}

func baseConfig() Config {
	return Config{
		AMQPURL: "amqp://guest:guest@localhost:5672/",
		Routes: []RouteConfig{
			{Order: 0, Default: true, Connector: ConnectorConfig{Type: "http", CID: "mo-any", URL: "http://localhost:1/mo", Method: "GET"}},
			{Order: 10, FilterConnectorID: "smsc-special", Connector: ConnectorConfig{Type: "smpps", SystemID: "alice"}},
		},
	}
}

func TestValidateConfig(t *testing.T) {
	if err := ValidateConfig(baseConfig()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Config){
		"no routes":            func(c *Config) { c.Routes = nil },
		"default with order":   func(c *Config) { c.Routes[0].Order = 5 },
		"default with filter":  func(c *Config) { c.Routes[0].FilterConnectorID = "x" },
		"static without order": func(c *Config) { c.Routes[1].Order = 0; c.Routes[1].Default = false },
		"http missing url":     func(c *Config) { c.Routes[0].Connector.URL = "" },
		"smpps missing system": func(c *Config) { c.Routes[1].Connector.SystemID = "" },
		"bad connector type":   func(c *Config) { c.Routes[0].Connector.Type = "carrier-pigeon" },
		"duplicate order":      func(c *Config) { c.Routes[1].Order = 0 },
		"default with content filter": func(c *Config) {
			c.Routes[0].Filters = []FilterConfig{{Type: "destination_addr", Pattern: "^2255"}}
		},
		"bad filter regex": func(c *Config) {
			c.Routes[1].Filters = []FilterConfig{{Type: "destination_addr", Pattern: "("}}
		},
		"user filter on MO route": func(c *Config) {
			c.Routes[1].Filters = []FilterConfig{{Type: "user"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := baseConfig()
			mutate(&config)
			if err := ValidateConfig(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

// TestStaticRouteWithoutConnectorFilterMatchesAnyConnector pins the relaxation
// that made legacy StaticMORoutes expressible: legacy matches on the filter list
// alone, so demanding a filter_connector_id rejected a legal route. An empty
// value now means "any inbound connector".
func TestStaticRouteWithoutConnectorFilterMatchesAnyConnector(t *testing.T) {
	config := baseConfig()
	config.Routes[1].FilterConnectorID = ""
	if err := ValidateConfig(config); err != nil {
		t.Fatalf("connector-agnostic static route rejected: %v", err)
	}
}

func TestHandleRoutesByConnectorFilterAndDefault(t *testing.T) {
	bridge := &fakeBridge{repickled: []byte("pdu-pickle")}
	service, err := NewService(context.Background(), baseConfig(), bridge)
	if err != nil {
		t.Fatal(err)
	}

	// Filtered source hits the static smpps route.
	delivery, acknowledger := moDelivery(t, "smsc-special")
	publisher := &fakePublisher{}
	if err := service.Handle(context.Background(), delivery, publisher); err != nil {
		t.Fatal(err)
	}
	if publisher.routingKey != "deliver_sm_thrower.smpps" || !acknowledger.acked {
		t.Fatalf("routed to %q acked=%v", publisher.routingKey, acknowledger.acked)
	}

	// Any other source falls back to the default http route with the legacy
	// RoutedDeliverSmContent shape.
	delivery, acknowledger = moDelivery(t, "smsc-primary")
	publisher = &fakePublisher{}
	if err := service.Handle(context.Background(), delivery, publisher); err != nil {
		t.Fatal(err)
	}
	if publisher.exchange != "messaging" || publisher.routingKey != "deliver_sm_thrower.http" {
		t.Fatalf("published to %s/%s", publisher.exchange, publisher.routingKey)
	}
	if !acknowledger.acked {
		t.Fatal("dispatched delivery was not acked")
	}
	envelope := publisher.envelope
	if envelope.Properties().MessageID() != "mo-msgid-1" || string(envelope.Body()) != "pdu-pickle" {
		t.Fatalf("msgid=%q body=%q", envelope.Properties().MessageID(), envelope.Body())
	}
	headers := envelope.Properties().Headers()
	if routeType, _ := headers["route-type"].String(); routeType != "simple" {
		t.Fatalf("route-type=%q", routeType)
	}
	if source, _ := headers["src-connector-id"].String(); source != "smsc-primary" {
		t.Fatalf("src-connector-id=%q", source)
	}
	if pickled, _ := headers["dst-connectors"].Bytes(); string(pickled) != "pickled:http" {
		t.Fatalf("dst-connectors=%q", pickled)
	}
	if tryCount, _ := headers["try-count"].Integer(); tryCount != 0 {
		t.Fatalf("try-count=%d", tryCount)
	}
}

func TestHandleContentFilterRouting(t *testing.T) {
	config := Config{
		AMQPURL: "amqp://guest:guest@localhost:5672/",
		Routes: []RouteConfig{
			{Order: 0, Default: true, Connector: ConnectorConfig{Type: "http", CID: "mo-default", URL: "http://localhost:1/mo", Method: "GET"}},
			{Order: 10, FilterConnectorID: "smsc-in", Filters: []FilterConfig{{Type: "destination_addr", Pattern: "^2255"}},
				Connector: ConnectorConfig{Type: "smpps", SystemID: "shortcode"}},
		},
	}
	bridge := &fakeBridge{repickled: []byte("pdu")}
	service, err := NewService(context.Background(), config, bridge)
	if err != nil {
		t.Fatal(err)
	}

	// A matching destination (from the filtered connector) hits the smpps route.
	bridge.fields = picklecompat.RoutableFields{DestinationAddr: []byte("2255")}
	delivery, acknowledger := moDelivery(t, "smsc-in")
	publisher := &fakePublisher{}
	if err := service.Handle(context.Background(), delivery, publisher); err != nil {
		t.Fatal(err)
	}
	if publisher.routingKey != "deliver_sm_thrower.smpps" || !acknowledger.acked {
		t.Fatalf("matching dest routed to %q acked=%v want smpps", publisher.routingKey, acknowledger.acked)
	}

	// A non-matching destination from the SAME connector falls through the
	// content filter to the default http route.
	bridge.fields = picklecompat.RoutableFields{DestinationAddr: []byte("9000")}
	delivery, acknowledger = moDelivery(t, "smsc-in")
	publisher = &fakePublisher{}
	if err := service.Handle(context.Background(), delivery, publisher); err != nil {
		t.Fatal(err)
	}
	if publisher.routingKey != "deliver_sm_thrower.http" || !acknowledger.acked {
		t.Fatalf("non-matching dest routed to %q, want default http", publisher.routingKey)
	}
}

func TestHandleFiltersMultipartByDestinationType(t *testing.T) {
	service, err := NewService(context.Background(), baseConfig(), &fakeBridge{repickled: []byte("pdu")})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name               string
		sourceCID          string
		concatenated       bool
		willBeConcatenated bool
		wantRoutingKey     string
		wantPublished      bool
	}{
		{"segment to smpps", "smsc-special", false, true, "deliver_sm_thrower.smpps", true},
		{"whole to smpps", "smsc-special", true, false, "", false},
		{"segment to http", "smsc-primary", false, true, "", false},
		{"whole to http", "smsc-primary", true, false, "deliver_sm_thrower.http", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			delivery, acknowledger := moDeliveryWithMarkers(
				t, testCase.sourceCID, testCase.concatenated, testCase.willBeConcatenated,
			)
			publisher := &fakePublisher{}
			if err := service.Handle(context.Background(), delivery, publisher); err != nil {
				t.Fatal(err)
			}
			if testCase.wantPublished {
				if publisher.calls != 1 || publisher.routingKey != testCase.wantRoutingKey || !acknowledger.acked {
					t.Fatalf("calls=%d routingKey=%q acked=%v", publisher.calls, publisher.routingKey, acknowledger.acked)
				}
				return
			}
			if publisher.calls != 0 || !acknowledger.rejected || acknowledger.requeue {
				t.Fatalf("calls=%d rejected=%v requeue=%v want reject without publish", publisher.calls, acknowledger.rejected, acknowledger.requeue)
			}
		})
	}
}

func TestHandleSettlements(t *testing.T) {
	config := Config{
		AMQPURL: "amqp://guest:guest@localhost:5672/",
		Routes: []RouteConfig{{
			Order: 10, FilterConnectorID: "only-this",
			Connector: ConnectorConfig{Type: "http", CID: "c", URL: "http://localhost:1/", Method: "GET"},
		}},
	}
	bridge := &fakeBridge{repickled: []byte("pdu")}
	var observed []string
	service, err := NewService(context.Background(), config, bridge,
		WithOnError(func(err error) { observed = append(observed, err.Error()) }))
	if err != nil {
		t.Fatal(err)
	}

	// No default route + unmatched source: legacy drop (ack) with a trace.
	delivery, acknowledger := moDelivery(t, "unknown-src")
	if err := service.Handle(context.Background(), delivery, &fakePublisher{}); err == nil {
		t.Fatal("unroutable MO must surface an error")
	}
	if !acknowledger.acked || acknowledger.rejected {
		t.Fatalf("unroutable MO settlement acked=%v rejected=%v want ack-drop", acknowledger.acked, acknowledger.rejected)
	}
	if len(observed) == 0 || !strings.Contains(observed[0], "no route matched") {
		t.Fatalf("drop left no trace: %v", observed)
	}

	// Missing connector-id header: poison, reject without requeue.
	delivery, acknowledger = moDelivery(t, "")
	if err := service.Handle(context.Background(), delivery, &fakePublisher{}); err == nil {
		t.Fatal("missing header must surface an error")
	}
	if !acknowledger.rejected || acknowledger.requeue {
		t.Fatalf("poison settlement rejected=%v requeue=%v want reject-no-requeue", acknowledger.rejected, acknowledger.requeue)
	}

	// Repickle failure: poison, reject without requeue.
	bridge.repickErr = errors.New("forbidden global")
	delivery, acknowledger = moDelivery(t, "only-this")
	if err := service.Handle(context.Background(), delivery, &fakePublisher{}); err == nil {
		t.Fatal("repickle failure must surface an error")
	}
	if !acknowledger.rejected || acknowledger.requeue {
		t.Fatalf("repickle settlement rejected=%v requeue=%v", acknowledger.rejected, acknowledger.requeue)
	}
	bridge.repickErr = nil

	// Publish failure: transient, reject WITH requeue.
	delivery, acknowledger = moDelivery(t, "only-this")
	if err := service.Handle(context.Background(), delivery, &fakePublisher{err: errors.New("broker down")}); err == nil {
		t.Fatal("publish failure must surface an error")
	}
	if !acknowledger.rejected || !acknowledger.requeue {
		t.Fatalf("publish settlement rejected=%v requeue=%v want reject-requeue", acknowledger.rejected, acknowledger.requeue)
	}
}
