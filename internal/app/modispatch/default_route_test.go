package modispatch

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/stats"
)

// An MO matching no route is acknowledged to the carrier and dropped. That is the
// reference behaviour and it stays, but it is the most expensive silence in the
// system: the carrier believes it delivered, and the only trace was a log line. An
// operator asking "are we losing inbound messages?" needs a number, so the drop
// and the success are both counted -- 3 drops means something different against 3
// total than against 3 million.
func TestHandleCountsMOOutcomes(t *testing.T) {
	config := Config{
		AMQPURL: "amqp://guest:guest@localhost:5672/",
		Routes: []RouteConfig{{
			Order: 10, FilterConnectorID: "only-this",
			Connector: ConnectorConfig{Type: "http", CID: "c", URL: "http://localhost:1/", Method: "GET"},
		}},
	}
	service, err := NewService(context.Background(), config, &fakeBridge{repickled: []byte("pdu")},
		WithOnError(func(error) {}))
	if err != nil {
		t.Fatal(err)
	}

	before := renderedMOCount(t, "unknown-src", "dropped")
	delivery, acknowledger := moDelivery(t, "unknown-src")
	if err := service.Handle(context.Background(), delivery, &fakePublisher{}); err == nil {
		t.Fatal("unroutable MO must surface an error")
	}
	if !acknowledger.acked {
		t.Fatal("unroutable MO must still be acked; the drop is the reference behaviour")
	}
	if got := renderedMOCount(t, "unknown-src", "dropped"); got != before+1 {
		t.Errorf("dropped count = %d, want %d", got, before+1)
	}

	routedBefore := renderedMOCount(t, "only-this", "routed")
	delivery, _ = moDelivery(t, "only-this")
	if err := service.Handle(context.Background(), delivery, &fakePublisher{}); err != nil {
		t.Fatalf("routable MO: %v", err)
	}
	if got := renderedMOCount(t, "only-this", "routed"); got != routedBefore+1 {
		t.Errorf("routed count = %d, want %d", got, routedBefore+1)
	}
}

// renderedMOCount reads the value back out of the rendered exposition rather than
// an internal field, because the exposition is what an operator actually sees --
// a counter that increments but never renders is not observability.
func renderedMOCount(t *testing.T, connector, outcome string) int {
	t.Helper()
	needle := `synevyr_mo_total{connector="` + connector + `",outcome="` + outcome + `"} `
	for _, line := range strings.Split(string(stats.DefaultPrometheus().RenderPrometheus()), "\n") {
		if !strings.HasPrefix(line, needle) {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, needle)))
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return value
	}
	return 0
}

// The dispatcher warns when it has no default route, because without one every
// unmatched inbound message is a silent drop. The warning goes to the caller's
// logger so it lands in the router log in the router's format rather than in two
// different timestamp formats in one stream.
func TestWarnIfNoDefaultRoute(t *testing.T) {
	filtered := RouteConfig{
		Order: 10, FilterConnectorID: "only-this",
		Connector: ConnectorConfig{Type: "http", CID: "c", URL: "http://localhost:1/", Method: "GET"},
	}
	fallback := RouteConfig{
		Order: 0, Default: true,
		Connector: ConnectorConfig{Type: "http", CID: "sink", URL: "http://localhost:1/", Method: "GET"},
	}

	cases := []struct {
		name     string
		routes   []RouteConfig
		wantWarn bool
	}{
		{name: "no routes at all", routes: nil, wantWarn: true},
		{name: "filtered routes only", routes: []RouteConfig{filtered}, wantWarn: true},
		{name: "default present", routes: []RouteConfig{filtered, fallback}, wantWarn: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sink bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelWarn}))
			service, err := NewService(context.Background(),
				Config{AMQPURL: "amqp://guest:guest@localhost:5672/", Routes: tc.routes},
				&fakeBridge{repickled: []byte("pdu")}, WithLogger(logger))
			if err != nil {
				t.Fatal(err)
			}
			// Deliberately not emitted by the constructor: admin-persisted routes
			// are applied afterwards, so a constructor-time warning would fire for
			// a deployment whose default route is stored rather than configured.
			if sink.Len() != 0 {
				t.Fatalf("constructor must not warn on its own; got %q", sink.String())
			}

			service.WarnIfNoDefaultRoute()
			warned := strings.Contains(sink.String(), "default MO route")
			if warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v (log: %q)", warned, tc.wantWarn, sink.String())
			}
			if tc.wantWarn && !strings.Contains(sink.String(), "DROPPED") {
				t.Error("the warning must name the consequence, not just the condition")
			}
		})
	}
}

// Removing the last default route at runtime turns every unmatched inbound
// message into a silent drop, so it warns at the moment it happens rather than
// when someone eventually asks why inbound volume fell.
func TestApplyRoutesWarnsWhenTheDefaultIsRemoved(t *testing.T) {
	fallback := RouteConfig{
		Order: 0, Default: true,
		Connector: ConnectorConfig{Type: "http", CID: "sink", URL: "http://localhost:1/", Method: "GET"},
	}
	var sink bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelWarn}))
	service, err := NewService(context.Background(),
		Config{AMQPURL: "amqp://guest:guest@localhost:5672/"},
		&fakeBridge{repickled: []byte("pdu")}, WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyRoutes(context.Background(), []RouteConfig{fallback}); err != nil {
		t.Fatalf("install default: %v", err)
	}
	if !service.HasDefaultRoute() {
		t.Fatal("default route not installed")
	}
	sink.Reset()

	if err := service.ApplyRoutes(context.Background(), nil); err != nil {
		t.Fatalf("remove default: %v", err)
	}
	if service.HasDefaultRoute() {
		t.Fatal("default route still reported after removal")
	}
	if !strings.Contains(sink.String(), "removed") {
		t.Errorf("removing the default route did not warn; log = %q", sink.String())
	}
}
