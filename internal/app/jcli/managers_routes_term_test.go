package jcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

// promptInteractive is the sub-prompt the interactive add loop draws.
const promptInteractive = "> "

// addMTRoute drives `mtrouter -a` through the interactive loop and returns the
// console's answer to `ok`.
func addMTRoute(c *client, lines ...string) string {
	c.t.Helper()
	c.send("mtrouter -a")
	c.readUntil(promptInteractive)
	for _, line := range lines {
		c.send(line)
		c.readUntil(promptInteractive)
	}
	c.send("ok")
	return c.readUntil(promptMain)
}

// addMTRouteRejected drives the same loop for an input the console refuses.
//
// A rejected save keeps the interactive session open, so the answer arrives at
// the sub-prompt rather than the main one — and the sub-prompt is not a usable
// read marker here, because the class repr the console echoes for `type`
// ("<class '...MTRoute'> arguments:") contains "> " itself. The expected text is
// the marker instead; a wrong answer shows up as a read timeout carrying
// everything that was received.
func addMTRouteRejected(c *client, want string, lines ...string) string {
	c.t.Helper()
	c.send("mtrouter -a")
	c.readUntil(promptInteractive)
	for _, line := range lines {
		c.send(line)
		c.readUntil(promptInteractive)
	}
	c.send("ok")
	return c.readUntil(want)
}

// An MT route to a termination connector must be creatable from the console and
// must persist its connector type. Without the type the stored route loads as
// smppc and looks for an outbound carrier connector that does not exist — a
// route that reports success and never carries traffic.
func TestConsoleCreatesMTRouteToTerminationConnector(t *testing.T) {
	fixture := newConsoleFixture(t)
	client := fixture.dial()
	client.login("jcliadmin", "jclipwd")

	answer := addMTRoute(client,
		"type StaticMTRoute",
		"order 10",
		"rate 0.02",
		"filters ",
		"connector term(partner-a-term)")
	if !strings.Contains(answer, "Successfully added MTRoute [StaticMTRoute] with order:10") {
		t.Fatalf("add answer: %q", answer)
	}

	stored, err := fixture.server.deps.Routes.GetRoute(context.Background(), 10)
	if err != nil {
		t.Fatalf("route not persisted: %v", err)
	}
	var route outbound.RouteConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &route); err != nil {
		t.Fatal(err)
	}
	if route.ConnectorType != "term" {
		t.Fatalf("stored connector_type = %q, want \"term\": %s", route.ConnectorType, stored.SpecJSON)
	}
	if route.ConnectorID != "partner-a-term" {
		t.Fatalf("stored connector_id = %q", route.ConnectorID)
	}

	// The listing must name the real type; rendering it as smppc would tell an
	// operator the route points at a carrier.
	client.send("mtrouter -l")
	listing := client.readUntil(promptMain)
	if !strings.Contains(listing, "term(partner-a-term)") {
		t.Fatalf("listing does not show the term connector: %q", listing)
	}
	client.send("mtrouter -s 10")
	shown := client.readUntil(promptMain)
	if !strings.Contains(shown, "term(partner-a-term)") {
		t.Fatalf("show does not name the term connector: %q", shown)
	}
}

// An smppc route must still be stored without a connector_type, byte-identical
// to one written before the field existed.
func TestConsoleSMPPcRouteStillOmitsTheConnectorType(t *testing.T) {
	fixture := newConsoleFixture(t)
	client := fixture.dial()
	client.login("jcliadmin", "jclipwd")

	addMTRoute(client, "type StaticMTRoute", "order 20", "rate 0.02", "connector smppc(smsc-a)")
	stored, err := fixture.server.deps.Routes.GetRoute(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.SpecJSON, "connector_type") {
		t.Fatalf("an smppc route grew a connector_type field: %s", stored.SpecJSON)
	}
}

// The two MT connector kinds cannot be mixed in one pool: failover across a
// carrier and a local termination endpoint would mean two different things.
func TestConsoleRefusesMixedMTConnectorTypes(t *testing.T) {
	fixture := newConsoleFixture(t)
	client := fixture.dial()
	client.login("jcliadmin", "jclipwd")

	answer := addMTRouteRejected(client, "Error: an MT route cannot mix connector types",
		"type RandomRoundrobinMTRoute",
		"order 30",
		"rate 0.02",
		"connectors smppc(smsc-a);term(partner-a-term)")
	if !strings.Contains(answer, "cannot mix connector types") {
		t.Fatalf("mixed pool answer: %q", answer)
	}
	if _, err := fixture.server.deps.Routes.GetRoute(context.Background(), 30); err == nil {
		t.Fatal("a mixed-type route was persisted")
	}
}

// An MO route has no termination target: a termination connector consumes the
// MT submit queue and delivers nothing inbound.
func TestConsoleRefusesTerminationConnectorOnMORoute(t *testing.T) {
	fixture := newConsoleFixture(t)
	client := fixture.dial()
	client.login("jcliadmin", "jclipwd")

	client.send("morouter -a")
	client.readUntil(promptInteractive)
	for _, line := range []string{"type StaticMORoute", "order 10", "connector term(partner-a-term)"} {
		client.send(line)
		client.readUntil(promptInteractive)
	}
	client.send("ok")
	answer := client.readUntil("Error: an MO route cannot point at a term connector")
	if !strings.Contains(answer, "an MO route cannot point at a term connector") {
		t.Fatalf("MO answer: %q", answer)
	}
}

// An MT route to an http connector is still refused, and the message still
// names the syntax that is legal.
func TestConsoleRefusesHTTPConnectorOnMTRoute(t *testing.T) {
	fixture := newConsoleFixture(t)
	client := fixture.dial()
	client.login("jcliadmin", "jclipwd")

	answer := addMTRouteRejected(client, "Error: an MT route must point at an smppc or term connector",
		"type StaticMTRoute", "order 40", "rate 0.02", "connector http(app)")
	if !strings.Contains(answer, "must point at an smppc or term connector") {
		t.Fatalf("http-on-MT answer: %q", answer)
	}
}
