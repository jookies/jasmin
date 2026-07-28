// Package jcli serves the Jasmin management console (jCli) over a telnet-style
// line protocol, as a third face over the same internal/app/admin services the
// /admin JSON API and the web UI use. No business logic lives here: a command
// parses its arguments, calls a service, and renders the result.
//
// The contract this package owes is the *transcript* — spec/compatibility/
// JCLI_MATRIX.md holds prompt bytes, line ordering and error text at
// transcript strictness, because operators script against them. Every literal
// in this package is taken from the frozen oracle (jasmin/protocols/cli/*) and
// must not be "improved".
package jcli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/admin"
)

// LegacyRelease is what the banner reports. It mirrors jasmin.get_release()
// (MAJOR.MINOR.META+PATCH = 0.11.1) rather than any Go build version, because
// the banner is part of the frozen transcript.
const LegacyRelease = "0.11.1"

// Deps are the management services the commands drive, plus the console
// credential. They are the same instances the JSON API and web UI hold.
type Deps struct {
	Connectors *admin.Service
	Routes     *admin.RouteService
	MORoutes   *admin.MORouteService
	Users      *admin.UserService
	Groups     *admin.GroupService
	SMPPsUsers *admin.SMPPsUserService
	// Filters and HTTPConnectors are the named registries jCli manages. They
	// have no live runtime table: a route embeds a copy of the resolved filter,
	// exactly as the frozen console pickles the filter object into the route.
	Filters        *admin.NamedSpecService
	HTTPConnectors *admin.NamedSpecService
	// Interceptors is nil unless admin.allow_interceptor_editing is on: the
	// scripts are arbitrary Python on the gateway host, so the capability is
	// opt-in and the console says so rather than failing obscurely.
	Interceptors *admin.InterceptorService

	// Username/Password are the console login. Password is the resolved
	// plaintext; it is compared constant-time and not retained in the clear.
	Username string
	Password string

	// IdleTimeout closes a session that has sent nothing for this long. Zero
	// disables it.
	IdleTimeout time.Duration

	// AuthenticationDisabled mirrors the legacy `[jcli] authentication = False`
	// knob, which serves the console with no login at all. It exists for
	// transcript parity (the frozen fixtures are captured both ways) and is
	// never the default: this console can mint credentials and start
	// connectors, so disabling auth hands the gateway to anyone who can reach
	// the port. The gateway logs a warning when it is on.
	AuthenticationDisabled bool
}

// AuthenticationRequired reports whether a session must log in before it can
// run commands.
func (d Deps) AuthenticationRequired() bool { return !d.AuthenticationDisabled }

// Server accepts console connections.
type Server struct {
	deps     Deps
	listener net.Listener
	logger   *slog.Logger

	mu       sync.Mutex
	sessions int64 // monotonic session ref, mirroring the legacy sessionRef
}

// NewServer binds the console listener. It binds eagerly so a bad address fails
// at construction rather than at first connect.
func NewServer(address string, deps Deps, logger *slog.Logger) (*Server, error) {
	if address == "" {
		return nil, errors.New("jcli: empty listen address")
	}
	if deps.Username == "" || deps.Password == "" {
		return nil, errors.New("jcli: username and password are required")
	}
	if deps.Connectors == nil || deps.Routes == nil || deps.MORoutes == nil || deps.Users == nil {
		return nil, errors.New("jcli: connector, route, MO route and user services are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("jcli: listen on %s: %w", address, err)
	}
	return &Server{deps: deps, listener: listener, logger: logger}, nil
}

// Addr reports the bound address (useful when the config used :0).
func (s *Server) Addr() net.Addr {
	if s == nil || s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Serve accepts connections until ctx is cancelled or the listener closes.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("jcli: accept: %w", err)
		}
		go s.handle(ctx, conn)
	}
}

// Close stops accepting connections.
func (s *Server) Close() error {
	if s == nil || s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *Server) nextSessionRef() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions++
	return s.sessions
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	ref := s.nextSessionRef()
	defer func() {
		_ = conn.Close()
		if recovered := recover(); recovered != nil {
			// A panic in one console session must never take the gateway down.
			s.logger.Error("jcli: session panic",
				"session", ref, "peer", conn.RemoteAddr().String(), "panic", fmt.Sprint(recovered))
		}
	}()
	session := newSession(s, conn, ref)
	session.run(ctx)
}
