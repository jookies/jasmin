// Package smpps composes the frozen SMPPS helpers (bind auth, bind-state gate,
// bind manager, IP whitelist) into a runnable SMPP server: a TCP listener with
// a per-connection session FSM. This is the composition root the DLR/MO
// throwers' SMPPS delivery seam plugs into — a bound RX/TRX session accepts
// pushed deliver_sm receipts and MO messages.
package smpps

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/stats"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// UserResolver projects a system_id into its smpps auth state (the user store).
// A missing system_id returns ok=false, which the session rejects as
// ESME_RINVPASWD — the legacy authenticateUser returning None.
type UserResolver interface {
	ResolveUser(systemID string) (UserAuth, bool)
}

// SubmitHandler ingests a bound ESME's submit_sm/data_sm: it runs smpps
// credential validation and hands the message to the MT pipeline (routing,
// billing, publication), returning the assigned message id and the SMPP
// command_status to answer with. A nil handler makes the server answer
// ESME_RSYSERR — a deployment that binds ESMEs but ingests no MT.
type SubmitHandler interface {
	HandleSubmit(ctx context.Context, systemID string, sm *smppwire.SMBody) (messageID string, status uint32)
}

// ServerConfig carries the listener settings.
type ServerConfig struct {
	// EnquireLinkTimeout bounds inactivity before the server drops a session
	// (the legacy inactivityTimer). Zero disables it.
	EnquireLinkTimeout time.Duration
	// ReadTimeout bounds a single PDU read. Zero disables it.
	ReadTimeout time.Duration
}

// Server owns the listener and the set of live sessions keyed by system_id, each
// with its own BindManager (the legacy SMPPServerFactory.bound_connections).
type Server struct {
	cfg      ServerConfig
	resolver UserResolver
	submit   SubmitHandler
	stats    *stats.SMPPsStats
	logger   *slog.Logger // smpp.server.<id>; nil disables bind/unbind lines

	mu       sync.Mutex
	managers map[string]*BindManager // system_id -> its bindings
	// managerOrder preserves the insertion order of the legacy
	// bound_connections dict for SMPPServerPB.list_bound_systemids.
	managerOrder []string
	sessions     map[*Session]struct{}
	closed       bool

	listener net.Listener
	wg       sync.WaitGroup
}

func NewServer(resolver UserResolver, cfg ServerConfig, options ...ServerOption) (*Server, error) {
	if resolver == nil {
		return nil, errors.New("smpps: nil user resolver")
	}
	server := &Server{
		cfg:      cfg,
		resolver: resolver,
		managers: make(map[string]*BindManager),
		sessions: make(map[*Session]struct{}),
	}
	for _, option := range options {
		option(server)
	}
	return server, nil
}

// ServerOption configures optional server dependencies.
type ServerOption func(*Server)

// WithSubmitHandler injects the MT-ingestion handler for inbound submit_sm.
func WithSubmitHandler(handler SubmitHandler) ServerOption {
	return func(s *Server) { s.submit = handler }
}

// WithStats injects the smppsapi counter registry incremented on session and
// bind lifecycle events (O-004).
func WithStats(registry *stats.SMPPsStats) ServerOption {
	return func(s *Server) { s.stats = registry }
}

// WithLogger injects the smpp.server.<id> logger for the bind/unbind audit lines.
// A nil logger (the default) disables them.
func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) { s.logger = logger }
}

// incStat increments an smppsapi counter when a registry is attached.
func (s *Server) incStat(name string) {
	if s.stats != nil {
		s.stats.Inc(name)
	}
}

// Serve accepts connections on listener until ctx is cancelled or the listener
// closes. Each connection runs an independent session goroutine.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("smpps: server closed")
	}
	s.listener = listener
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.wg.Wait()
				return ctx.Err()
			}
			return err
		}
		session := s.newSession(conn)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.sessions[session] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			session.run(ctx)
			s.forgetSession(session)
		}()
	}
}

// Close stops accepting, drops all sessions, and waits for their goroutines.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	listener := s.listener
	sessions := make([]*Session, 0, len(s.sessions))
	for session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	for _, session := range sessions {
		_ = session.conn.Close()
	}
	s.wg.Wait()
	return nil
}

// managerFor returns (creating if needed) the BindManager for a system_id.
func (s *Server) managerFor(systemID string) *BindManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	manager, ok := s.managers[systemID]
	if !ok {
		manager = NewBindManager()
		s.managers[systemID] = manager
		s.managerOrder = append(s.managerOrder, systemID)
	}
	return manager
}

func (s *Server) forgetSession(session *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, session)
}

// Deliver pushes a deliver_sm PDU down a bound receiver/transceiver session of
// system_id, selected round-robin by the system_id's BindManager (the legacy
// SMPPServerFactory delivery selection). It returns ErrNoBoundSession when no
// deliverable session exists — the caller (a thrower) retries per its policy.
func (s *Server) Deliver(ctx context.Context, systemID string, pdu smppwire.PDU) error {
	// The manager is not concurrency-safe; hold server.mu across the selection,
	// which races with bind Add/Remove. Release before the socket write so a
	// slow deliver never blocks binds.
	s.mu.Lock()
	manager, ok := s.managers[systemID]
	if !ok {
		s.mu.Unlock()
		return ErrNoBoundSession
	}
	binding := manager.GetNextBindingForDelivery()
	s.mu.Unlock()
	if binding == nil {
		return ErrNoBoundSession
	}
	session, ok := binding.(*Session)
	if !ok {
		return ErrNoBoundSession
	}
	return session.deliver(ctx, pdu)
}

// ErrNoBoundSession means system_id has no receiver/transceiver session to
// deliver down right now.
var ErrNoBoundSession = errors.New("smpps: no bound session for delivery")

func (s *Server) newSession(conn net.Conn) *Session {
	return &Session{
		server: s,
		conn:   conn,
		state:  StateOpen,
	}
}

// UnbindUser unbinds and disconnects every session bound as system_id, the way
// the frozen SMPPServerFactory.unbindGateway does: each binding is sent an
// unbind PDU and then dropped, rather than having its socket yanked. An ESME
// that is told to unbind can reconnect cleanly; one whose connection simply
// vanishes usually retries against a gateway that thinks it is still bound.
//
// It reports how many sessions were unbound; zero is not an error, because the
// operator's intent ("this user must not be bound") is satisfied either way.
func (s *Server) UnbindUser(systemID string) int {
	s.mu.Lock()
	targets := make([]*Session, 0, len(s.sessions))
	for session := range s.sessions {
		session.mu.Lock()
		bound := session.systemID == systemID && !session.closed
		session.mu.Unlock()
		if bound {
			targets = append(targets, session)
		}
	}
	s.mu.Unlock()

	for _, session := range targets {
		// Sequence 0: this is an unsolicited request from the server, not a
		// response, so it carries no client sequence to echo.
		_ = session.writeHeader(smppwire.CommandUnbind, 0, 0)
		session.cleanup()
	}
	return len(targets)
}

// BoundSystemIDs returns the frozen SMPPServerPB list projection: one unique
// system_id for every principal with at least one live bound session, in first
// bind order like the legacy bound_connections dict.
func (s *Server) BoundSystemIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]struct{})
	for session := range s.sessions {
		session.mu.Lock()
		bound := !session.closed &&
			(session.state == StateBoundRX || session.state == StateBoundTX || session.state == StateBoundTRX)
		systemID := session.systemID
		session.mu.Unlock()
		if bound && systemID != "" {
			seen[systemID] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for _, systemID := range s.managerOrder {
		if _, bound := seen[systemID]; bound {
			result = append(result, systemID)
		}
	}
	return result
}
