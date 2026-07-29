package smpps

import (
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// Legacy runs two independent timers: enquireLinkTimerSecs (30) sends an
// enquire_link and the session lives on, while inactivityTimerSecs (300) is
// what actually drops it. Using the enquire-link interval as the read deadline
// disconnected every quiet bind after 30s -- normal for a receiver bind that is
// only ever written to.
func TestQuietSessionGetsAnEnquireLinkInsteadOfADisconnect(t *testing.T) {
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{
		EnquireLinkTimeout: 50 * time.Millisecond,
		InactivityTimeout:  10 * time.Second,
	})
	defer func() { _ = server.Close() }()

	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindReceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("bind should succeed")
	}

	// Say nothing. The server must probe rather than hang up.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := readPDU(t, conn)
	if got.Header.CommandID != smppwire.CommandEnquireLink {
		t.Fatalf("quiet session got %#x, want enquire_link", got.Header.CommandID)
	}
	if got.Header.SequenceNumber == 0 {
		t.Error("server-originated enquire_link must carry a non-zero sequence number")
	}

	// Answering it keeps the bind alive, and the server probes again.
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandEnquireLinkResp,
			SequenceNumber: got.Header.SequenceNumber,
			CommandStatus:  StatusROK,
		},
	})
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if second := readPDU(t, conn); second.Header.CommandID != smppwire.CommandEnquireLink {
		t.Fatalf("after enquire_link_resp the session answered %#x, want another enquire_link", second.Header.CommandID)
	}
}

// The inactivity timer must still end a genuinely dead session.
func TestSessionIsDroppedAfterInactivityTimeout(t *testing.T) {
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{
		EnquireLinkTimeout: 20 * time.Millisecond,
		InactivityTimeout:  60 * time.Millisecond,
	})
	defer func() { _ = server.Close() }()

	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindReceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("bind should succeed")
	}

	// Never answer the keepalives; the server must give up and close.
	deadline := time.Now().Add(5 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	for {
		if _, err := smppwire.Read(conn, smppwire.DefaultMaxSize); err != nil {
			return // closed, as required
		}
		if time.Now().After(deadline) {
			t.Fatal("session outlived the inactivity timeout")
		}
	}
}
