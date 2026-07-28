package jcli

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/admin"
)

// consoleFixture is a running console backed by an in-memory admin store.
type consoleFixture struct {
	server *Server
	store  *admin.Store
	t      *testing.T
}

func newConsoleFixture(t *testing.T) *consoleFixture {
	t.Helper()
	return newConsoleFixtureAuth(t, true)
}

// newConsoleFixtureAuth builds a console with or without the login exchange.
// The captured oracle fixtures come in both shapes: authentication is a frozen
// [jcli] config knob, and most manager transcripts were recorded without it.
func newConsoleFixtureAuth(t *testing.T, authentication bool) *consoleFixture {
	t.Helper()
	ctx := context.Background()
	store, err := admin.OpenStore(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := func() string { return "2026-07-27T00:00:00Z" }
	connectors, err := admin.NewService(store, newStubManager(), nil, now)
	if err != nil {
		t.Fatalf("connector service: %v", err)
	}
	routes, err := admin.NewRouteService(store, stubProvisioner{}, now)
	if err != nil {
		t.Fatalf("route service: %v", err)
	}
	moRoutes, err := admin.NewMORouteService(store, stubMOProvisioner{}, now)
	if err != nil {
		t.Fatalf("MO route service: %v", err)
	}
	users, err := admin.NewUserService(store, stubUserProvisioner{}, now)
	if err != nil {
		t.Fatalf("user service: %v", err)
	}

	groups, err := admin.NewGroupService(store, stubGroupProvisioner{}, now)
	if err != nil {
		t.Fatalf("group service: %v", err)
	}

	server, err := NewServer("127.0.0.1:0", Deps{
		Connectors:             connectors,
		Routes:                 routes,
		MORoutes:               moRoutes,
		Users:                  users,
		Groups:                 groups,
		Username:               "jcliadmin",
		Password:               "jclipwd",
		AuthenticationDisabled: !authentication,
	}, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	serveCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(func() { cancel(); _ = server.Close() })
	go func() { _ = server.Serve(serveCtx) }()

	return &consoleFixture{server: server, store: store, t: t}
}

// client is one console connection with helpers to read up to a prompt.
type client struct {
	conn   net.Conn
	reader *bufio.Reader
	t      *testing.T
}

func (f *consoleFixture) dial() *client {
	f.t.Helper()
	conn, err := net.DialTimeout("tcp", f.server.Addr().String(), 3*time.Second)
	if err != nil {
		f.t.Fatalf("dial console: %v", err)
	}
	f.t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return &client{conn: conn, reader: bufio.NewReader(conn), t: f.t}
}

// readUntil consumes bytes until the marker appears, returning everything read.
func (c *client) readUntil(marker string) string {
	c.t.Helper()
	var builder strings.Builder
	for {
		b, err := c.reader.ReadByte()
		if err != nil {
			c.t.Fatalf("read (waiting for %q, got %q): %v", marker, builder.String(), err)
		}
		builder.WriteByte(b)
		if strings.HasSuffix(builder.String(), marker) {
			return builder.String()
		}
	}
}

func (c *client) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.conn, line+"\r\n"); err != nil {
		c.t.Fatalf("write %q: %v", line, err)
	}
}

// login performs the full auth exchange and returns the banner text.
//
// It waits for the auth notice, not for a "Username: " prompt: the oracle's
// initializeScreen() deliberately draws no prompt on connect, so the operator
// types the username blind. Proven by J-001-auth-success.
func (c *client) login(username, password string) string {
	c.t.Helper()
	c.readUntil("Authentication required." + lineBreak + lineBreak)
	c.send(username)
	c.readUntil(promptPassword)
	c.send(password)
	return c.readUntil(promptMain)
}

// The auth exchange, help, unknown commands and completion are asserted
// byte-for-byte by TestOracleTranscripts against recordings of the frozen
// console. The tests below cover what a transcript cannot: that the console and
// the admin services share one state, and that the connection lifecycle holds.

func TestConsoleListsReflectAdminState(t *testing.T) {
	fixture := newConsoleFixture(t)
	client := fixture.dial()
	client.login("jcliadmin", "jclipwd")

	// Empty state still renders the totals footer.
	client.send("smppccm -l")
	if empty := client.readUntil(promptMain); !strings.Contains(empty, "Total connectors: 0") {
		t.Fatalf("empty connector list: %q", empty)
	}

	// A connector created through the admin service appears in the console —
	// the point of sharing one management core.
	ctx := context.Background()
	if err := fixture.server.deps.Connectors.CreateConnector(ctx, stubConfig("smsc-a"), true); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	client.send("smppccm -l")
	listing := client.readUntil(promptMain)
	if !strings.Contains(listing, "smsc-a") || !strings.Contains(listing, "started") {
		t.Fatalf("connector missing from listing: %q", listing)
	}
	if !strings.Contains(listing, "Total connectors: 1") {
		t.Fatalf("connector total wrong: %q", listing)
	}
	if !strings.Contains(listing, "#Connector id") {
		t.Fatalf("header shape changed: %q", listing)
	}

	// show renders the connector without echoing its bind password.
	client.send("smppccm -s smsc-a")
	shown := client.readUntil(promptMain)
	if !strings.Contains(shown, "smsc-a") {
		t.Fatalf("show did not render the connector: %q", shown)
	}
	if strings.Contains(shown, "secret-bind-pw") {
		t.Fatalf("show leaked the bind password: %q", shown)
	}

	// An unimplemented verb says so rather than silently doing nothing.
	client.send("smppccm -r smsc-a")
	if refused := client.readUntil(promptMain); !strings.Contains(refused, "not implemented") {
		t.Fatalf("unimplemented verb should be explicit: %q", refused)
	}
	// ...and it must not have removed anything.
	views, err := fixture.server.deps.Connectors.ListConnectors(ctx)
	if err != nil || len(views) != 1 {
		t.Fatalf("connector was mutated by an unimplemented verb: %v %d", err, len(views))
	}
}

func TestConsoleQuitClosesConnection(t *testing.T) {
	fixture := newConsoleFixture(t)
	client := fixture.dial()
	client.login("jcliadmin", "jclipwd")

	client.send("quit")
	// The oracle echoes the command, breaks the line and writes a terminal
	// reset before hanging up (J-001-auth-success step 3), so drain that first
	// -- the contract is that the connection closes after it, not instead of it.
	client.readUntil(terminalReset)
	if _, err := client.reader.ReadByte(); err == nil {
		t.Fatal("connection stayed open after quit")
	}
}
