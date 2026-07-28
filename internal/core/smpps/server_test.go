package smpps

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/stats"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type mapResolver map[string]UserAuth

func (m mapResolver) ResolveUser(systemID string) (UserAuth, bool) {
	u, ok := m[systemID]
	return u, ok
}

func testUser(password string, opts ...func(*UserAuth)) UserAuth {
	u := UserAuth{
		PasswordDigest: PasswordHash(password),
		UserEnabled:    true,
		GroupEnabled:   true,
		BindAuthorized: true,
		IPWhitelist:    DefaultIPWhitelist,
	}
	for _, opt := range opts {
		opt(&u)
	}
	return u
}

// startServer launches a Server on a loopback listener and returns its address.
func startServer(t *testing.T, resolver UserResolver, cfg ServerConfig) (*Server, string) {
	t.Helper()
	server, err := NewServer(resolver, cfg)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return server, listener.Addr().String()
}

func dial(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func writePDU(t *testing.T, conn net.Conn, pdu smppwire.PDU) {
	t.Helper()
	frame, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(conn, frame); err != nil {
		t.Fatal(err)
	}
}

func readPDU(t *testing.T, conn net.Conn) smppwire.PDU {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	pdu, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatalf("read PDU: %v", err)
	}
	return pdu
}

func bindPDU(command uint32, systemID, password string, sequence uint32) smppwire.PDU {
	return smppwire.PDU{
		Header: smppwire.Header{CommandID: command, SequenceNumber: sequence},
		Bind: &smppwire.BindBody{
			SystemID: []byte(systemID), Password: []byte(password), SystemType: []byte(""),
			InterfaceVersion: 0x34,
		},
	}
}

func TestBindTransceiverSucceeds(t *testing.T) {
	_, addr := startServer(t, mapResolver{"alice": testUser("secret")}, ServerConfig{})
	conn := dial(t, addr)

	writePDU(t, conn, bindPDU(CommandBindTransceiver, "alice", "secret", 7))
	resp := readPDU(t, conn)
	if resp.Header.CommandID != smppwire.CommandBindTransceiverResp {
		t.Fatalf("command = %#x", resp.Header.CommandID)
	}
	if resp.Header.CommandStatus != StatusROK || resp.Header.SequenceNumber != 7 {
		t.Fatalf("status/seq = %#x/%d", resp.Header.CommandStatus, resp.Header.SequenceNumber)
	}
	if resp.BindResponse == nil || string(resp.BindResponse.SystemID) != "alice" {
		t.Fatalf("bind response = %+v", resp.BindResponse)
	}
}

func TestBindEachTypeSucceeds(t *testing.T) {
	cases := []struct {
		command  uint32
		respWant uint32
	}{
		{CommandBindReceiver, smppwire.CommandBindReceiverResp},
		{CommandBindTransmitter, smppwire.CommandBindTransmitterResp},
		{CommandBindTransceiver, smppwire.CommandBindTransceiverResp},
	}
	for _, testCase := range cases {
		_, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
		conn := dial(t, addr)
		writePDU(t, conn, bindPDU(testCase.command, "u", "p", 1))
		resp := readPDU(t, conn)
		if resp.Header.CommandID != testCase.respWant || resp.Header.CommandStatus != StatusROK {
			t.Fatalf("command %#x -> resp %#x status %#x", testCase.command, resp.Header.CommandID, resp.Header.CommandStatus)
		}
	}
}

func TestBindWrongPasswordRejects(t *testing.T) {
	_, addr := startServer(t, mapResolver{"alice": testUser("secret")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "alice", "WRONG", 1))
	resp := readPDU(t, conn)
	if resp.Header.CommandStatus != StatusInvalidPassword {
		t.Fatalf("status = %#x, want ESME_RINVPASWD", resp.Header.CommandStatus)
	}
	// The session stays open — the ESME may retry.
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "alice", "secret", 2))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("retry after wrong password should succeed")
	}
}

func TestBindUnknownSystemIDRejects(t *testing.T) {
	_, addr := startServer(t, mapResolver{}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindReceiver, "ghost", "x", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusInvalidPassword {
		t.Fatal("unknown system_id must reject as ESME_RINVPASWD")
	}
}

func TestBindDisallowedIPRejects(t *testing.T) {
	user := testUser("p", func(u *UserAuth) { u.IPWhitelist = "10.0.0.0/8" })
	_, addr := startServer(t, mapResolver{"u": user}, ServerConfig{})
	conn := dial(t, addr) // loopback is not in 10.0.0.0/8
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusBindFailed {
		t.Fatal("bind from a non-whitelisted IP must reject as ESME_RBINDFAIL")
	}
}

func TestMaxBindingsQuotaEnforced(t *testing.T) {
	one := 1
	user := testUser("p", func(u *UserAuth) { u.MaxBindings = &one })
	_, addr := startServer(t, mapResolver{"u": user}, ServerConfig{})

	conn1 := dial(t, addr)
	writePDU(t, conn1, bindPDU(CommandBindTransceiver, "u", "p", 1))
	if readPDU(t, conn1).Header.CommandStatus != StatusROK {
		t.Fatal("first bind should succeed")
	}
	conn2 := dial(t, addr)
	writePDU(t, conn2, bindPDU(CommandBindTransceiver, "u", "p", 1))
	if readPDU(t, conn2).Header.CommandStatus != StatusBindFailed {
		t.Fatal("second bind past max_bindings must reject")
	}
}

func TestSecondBindOnSameSessionRejectsAlreadyBound(t *testing.T) {
	_, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("first bind should succeed")
	}
	writePDU(t, conn, bindPDU(CommandBindReceiver, "u", "p", 2))
	if readPDU(t, conn).Header.CommandStatus != StatusAlreadyBound {
		t.Fatal("re-bind on a bound session must reject as ESME_RALYBND")
	}
}

func TestSubmitFromReceiverRejectsInvalidBindStatus(t *testing.T) {
	_, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindReceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("bind should succeed")
	}
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: CommandSubmitSM, SequenceNumber: 2},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("2222"), ShortMessage: []byte("hi")},
	})
	resp := readPDU(t, conn)
	if resp.Header.CommandStatus != StatusInvalidBindStatus {
		t.Fatalf("submit from RX must reject ESME_RINVBNDSTS, got %#x", resp.Header.CommandStatus)
	}
}

func TestSubmitBeforeBindRejects(t *testing.T) {
	_, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: CommandSubmitSM, SequenceNumber: 1},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("2222"), ShortMessage: []byte("hi")},
	})
	if readPDU(t, conn).Header.CommandStatus != StatusInvalidBindStatus {
		t.Fatal("submit before bind must reject ESME_RINVBNDSTS")
	}
}

func TestEnquireLinkAnyState(t *testing.T) {
	_, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, smppwire.PDU{Header: smppwire.Header{CommandID: CommandEnquireLink, SequenceNumber: 9}})
	resp := readPDU(t, conn)
	if resp.Header.CommandID != smppwire.CommandEnquireLinkResp || resp.Header.SequenceNumber != 9 {
		t.Fatalf("enquire_link_resp = %#x/%d", resp.Header.CommandID, resp.Header.SequenceNumber)
	}
}

func TestUnbindClosesSession(t *testing.T) {
	_, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	readPDU(t, conn)
	writePDU(t, conn, smppwire.PDU{Header: smppwire.Header{CommandID: CommandUnbind, SequenceNumber: 2}})
	resp := readPDU(t, conn)
	if resp.Header.CommandID != smppwire.CommandUnbindResp || resp.Header.CommandStatus != StatusROK {
		t.Fatalf("unbind_resp = %#x/%#x", resp.Header.CommandID, resp.Header.CommandStatus)
	}
	// The server closes after unbind_resp: the next read hits EOF.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := smppwire.Read(conn, smppwire.DefaultMaxSize); err == nil {
		t.Fatal("server should close the connection after unbind")
	}
}

func TestDeliverToBoundTransceiver(t *testing.T) {
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("bind should succeed")
	}

	deliver := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 100},
		SM:     &smppwire.SMBody{SourceAddress: []byte("111"), DestinationAddress: []byte("222"), ShortMessage: []byte("mo")},
	}
	// Retry Deliver until the bind has registered (the server processes the
	// bind asynchronously after writing the resp).
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := server.Deliver(context.Background(), "u", deliver)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Deliver never succeeded: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := readPDU(t, conn)
	if got.Header.CommandID != smppwire.CommandDeliverSM || string(got.SM.ShortMessage) != "mo" {
		t.Fatalf("delivered PDU = %#x %q", got.Header.CommandID, got.SM.ShortMessage)
	}
}

func TestDeliverToTransmitterFails(t *testing.T) {
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindTransmitter, "u", "p", 1))
	readPDU(t, conn)
	// Give the bind a moment to register.
	time.Sleep(50 * time.Millisecond)
	deliver := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 1},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("2"), ShortMessage: []byte("x")},
	}
	// A TX-only bind is not a delivery target: the manager selects no session.
	if err := server.Deliver(context.Background(), "u", deliver); err != ErrNoBoundSession {
		t.Fatalf("deliver to TX-only user = %v, want ErrNoBoundSession", err)
	}
}

func TestDeliverUnknownSystemIDFails(t *testing.T) {
	server, _ := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	if err := server.Deliver(context.Background(), "nobody", smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("2"), ShortMessage: []byte("x")},
	}); err != ErrNoBoundSession {
		t.Fatalf("deliver to unknown system_id = %v", err)
	}
}

func TestBindRemovedOnDisconnect(t *testing.T) {
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindReceiver, "u", "p", 1))
	readPDU(t, conn)
	// Wait for registration.
	deadline := time.Now().Add(2 * time.Second)
	deliver := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("2"), ShortMessage: []byte("x")},
	}
	for server.Deliver(context.Background(), "u", deliver) != nil {
		if time.Now().After(deadline) {
			t.Fatal("bind never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = conn.Close()
	// After disconnect the binding is removed, so delivery fails again.
	for {
		if server.Deliver(context.Background(), "u", deliver) == ErrNoBoundSession {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("binding not removed after disconnect")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type scriptedSubmitHandler struct {
	messageID string
	status    uint32
	gotSystem string
	gotSM     *smppwire.SMBody
}

func (h *scriptedSubmitHandler) HandleSubmit(_ context.Context, systemID string, sm *smppwire.SMBody) (string, uint32) {
	h.gotSystem = systemID
	h.gotSM = sm
	return h.messageID, h.status
}

func TestSubmitFromTransmitterIngestsAndResponds(t *testing.T) {
	handler := &scriptedSubmitHandler{messageID: "msg-42", status: StatusROK}
	server, err := NewServer(mapResolver{"u": testUser("p")}, ServerConfig{}, WithSubmitHandler(handler))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx, listener) }()
	t.Cleanup(func() { cancel(); _ = server.Close() })

	conn := dial(t, listener.Addr().String())
	writePDU(t, conn, bindPDU(CommandBindTransmitter, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("bind should succeed")
	}
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: CommandSubmitSM, SequenceNumber: 5},
		SM:     &smppwire.SMBody{SourceAddress: []byte("111"), DestinationAddress: []byte("222"), ShortMessage: []byte("hi")},
	})
	resp := readPDU(t, conn)
	if resp.Header.CommandID != smppwire.CommandSubmitSMResp || resp.Header.CommandStatus != StatusROK {
		t.Fatalf("resp = %#x/%#x", resp.Header.CommandID, resp.Header.CommandStatus)
	}
	if resp.Header.SequenceNumber != 5 {
		t.Fatalf("resp sequence = %d", resp.Header.SequenceNumber)
	}
	if resp.SubmitResponse == nil || string(resp.SubmitResponse.MessageID) != "msg-42" {
		t.Fatalf("submit response = %+v", resp.SubmitResponse)
	}
	if handler.gotSystem != "u" || string(handler.gotSM.ShortMessage) != "hi" {
		t.Fatalf("handler saw system=%q sm=%+v", handler.gotSystem, handler.gotSM)
	}
}

func TestSubmitHandlerErrorStatusPropagates(t *testing.T) {
	const esmeRSubmitFail uint32 = 0x00000045
	handler := &scriptedSubmitHandler{status: esmeRSubmitFail}
	server, _ := NewServer(mapResolver{"u": testUser("p")}, ServerConfig{}, WithSubmitHandler(handler))
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx, listener) }()
	t.Cleanup(func() { cancel(); _ = server.Close() })

	conn := dial(t, listener.Addr().String())
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	readPDU(t, conn)
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: CommandSubmitSM, SequenceNumber: 6},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("222"), ShortMessage: []byte("x")},
	})
	resp := readPDU(t, conn)
	if resp.Header.CommandStatus != esmeRSubmitFail || resp.SubmitResponse != nil {
		t.Fatalf("error resp = %#x, body=%+v", resp.Header.CommandStatus, resp.SubmitResponse)
	}
	// The session stays open after a submit rejection.
	writePDU(t, conn, smppwire.PDU{Header: smppwire.Header{CommandID: CommandEnquireLink, SequenceNumber: 7}})
	if readPDU(t, conn).Header.CommandID != smppwire.CommandEnquireLinkResp {
		t.Fatal("session should stay open after submit rejection")
	}
}

func TestSubmitWithoutHandlerRejectsSystemError(t *testing.T) {
	// The default server (no handler) answers ESME_RSYSERR, session stays open.
	_, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	readPDU(t, conn)
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: CommandSubmitSM, SequenceNumber: 8},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("222"), ShortMessage: []byte("x")},
	})
	if readPDU(t, conn).Header.CommandStatus != StatusSystemError {
		t.Fatal("no-handler submit must answer ESME_RSYSERR")
	}
}

func TestSMPPsStatsIncrementOnLifecycle(t *testing.T) {
	registry := &stats.SMPPsStats{}
	server, err := NewServer(mapResolver{"u": testUser("p")}, ServerConfig{},
		WithSubmitHandler(&scriptedSubmitHandler{messageID: "m", status: StatusROK}), WithStats(registry))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx, listener) }()
	t.Cleanup(func() { cancel(); _ = server.Close() })

	conn := dial(t, listener.Addr().String())
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	readPDU(t, conn)
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: CommandSubmitSM, SequenceNumber: 2},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("222"), ShortMessage: []byte("x")},
	})
	readPDU(t, conn)
	writePDU(t, conn, smppwire.PDU{Header: smppwire.Header{CommandID: CommandEnquireLink, SequenceNumber: 3}})
	readPDU(t, conn)

	// Poll until the async session has recorded the events.
	deadline := time.Now().Add(2 * time.Second)
	for registry.Get("submit_sm_count") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("submit_sm_count never incremented")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for name, want := range map[string]int64{
		"connect_count": 1, "bind_trx_count": 1, "bound_trx_count": 1,
		"submit_sm_request_count": 1, "submit_sm_count": 1, "elink_count": 1,
	} {
		if got := registry.Get(name); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
}

// TestUnbindUserDropsBoundSessions covers what a transcript cannot: that
// `user --smpp-unbind` actually reaches the wire. The ESME must be *told* to
// unbind, not merely disconnected -- an ESME whose socket vanishes typically
// retries against a gateway that still believes it is bound.
func TestUnbindUserDropsBoundSessions(t *testing.T) {
	server, addr := startServer(t, mapResolver{
		"alice": testUser("secret"),
		"bob":   testUser("secret"),
	}, ServerConfig{})

	alice := dial(t, addr)
	writePDU(t, alice, bindPDU(CommandBindTransceiver, "alice", "secret", 1))
	if resp := readPDU(t, alice); resp.Header.CommandStatus != StatusROK {
		t.Fatalf("alice bind status = %#x", resp.Header.CommandStatus)
	}
	bob := dial(t, addr)
	writePDU(t, bob, bindPDU(CommandBindTransceiver, "bob", "secret", 1))
	if resp := readPDU(t, bob); resp.Header.CommandStatus != StatusROK {
		t.Fatalf("bob bind status = %#x", resp.Header.CommandStatus)
	}

	if unbound := server.UnbindUser("alice"); unbound != 1 {
		t.Fatalf("unbound = %d, want 1", unbound)
	}

	// Alice is told to unbind, then dropped.
	unbind := readPDU(t, alice)
	if unbind.Header.CommandID != smppwire.CommandUnbind {
		t.Fatalf("alice received %#x, want unbind", unbind.Header.CommandID)
	}
	_ = alice.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := smppwire.Read(alice, smppwire.DefaultMaxSize); err == nil {
		t.Fatal("alice's connection stayed open after unbind")
	}

	// Bob is untouched: unbinding one user must not disturb anyone else.
	if unbound := server.UnbindUser("nosuchuser"); unbound != 0 {
		t.Fatalf("unbinding an unknown system_id reported %d sessions", unbound)
	}
	writePDU(t, bob, smppwire.PDU{Header: smppwire.Header{
		CommandID: smppwire.CommandEnquireLink, SequenceNumber: 9,
	}})
	if resp := readPDU(t, bob); resp.Header.CommandID != smppwire.CommandEnquireLinkResp {
		t.Fatalf("bob got %#x after another user was unbound", resp.Header.CommandID)
	}
}
