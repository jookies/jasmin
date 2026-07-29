package smppsserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
	"github.com/pumpitspace/jasmin/internal/core/stats"
)

// Config is the gateway's smpps-server section.
type Config struct {
	// BindAddr is the listen address (host:port), e.g. "0.0.0.0:2775".
	BindAddr string `json:"bind_addr"`
	// EnquireLinkTimeoutSeconds is the quiet period after which the server
	// sends an enquire_link keepalive (legacy enquireLinkTimerSecs, 30).
	// It does NOT end the session; 0 disables the keepalive.
	EnquireLinkTimeoutSeconds float64 `json:"enquire_link_timeout,omitempty"`
	// InactivityTimeoutSeconds is how long a session may receive nothing
	// before it is dropped (legacy inactivityTimerSecs, 300). 0 disables it.
	InactivityTimeoutSeconds float64 `json:"inactivity_timeout,omitempty"`
	// Users are the SMPPS accounts allowed to bind.
	Users []UserConfig `json:"users"`
	// TLSCertFile/TLSKeyFile, when both set, terminate SMPPS-over-TLS on this
	// listener. File paths are opened at service construction, not validation.
	TLSCertFile string `json:"tls_cert_file,omitempty"`
	TLSKeyFile  string `json:"tls_key_file,omitempty"`
}

func ValidateConfig(config Config) error {
	if config.BindAddr == "" {
		return fmt.Errorf("%w: empty bind_addr", ErrInvalidConfig)
	}
	if config.EnquireLinkTimeoutSeconds < 0 {
		return fmt.Errorf("%w: negative enquire_link_timeout", ErrInvalidConfig)
	}
	if config.InactivityTimeoutSeconds < 0 {
		return fmt.Errorf("%w: negative inactivity_timeout", ErrInvalidConfig)
	}
	if (config.TLSCertFile == "") != (config.TLSKeyFile == "") {
		return fmt.Errorf("%w: tls_cert_file and tls_key_file must be set together", ErrInvalidConfig)
	}
	if _, err := NewDirectory(config.Users); err != nil {
		return err
	}
	return nil
}

// Service owns the SMPPS listener and server. Deliver exposes the server's
// push path so the DLR/MO throwers' receipt sink can reach bound sessions.
type Service struct {
	server    *smpps.Server
	listener  net.Listener
	directory *Directory
}

// NewService builds the SMPPS server from config, wiring bind auth over the
// directory and submit ingestion over the shared MT submitter. It binds the
// listen socket eagerly so a bad address fails fast at construction.
// Option configures optional service dependencies.
type Option func(*options)

type options struct {
	stats  *stats.SMPPsStats
	logger *slog.Logger
}

// WithStats attaches the smppsapi counter registry (O-004).
func WithStats(registry *stats.SMPPsStats) Option {
	return func(o *options) { o.stats = registry }
}

// WithLogger attaches the smpp.server.<id> logger for the bind/unbind audit lines.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

func NewService(config Config, submitter core.Submitter, opts ...Option) (*Service, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	if submitter == nil {
		return nil, fmt.Errorf("%w: nil submitter", ErrInvalidConfig)
	}
	var settings options
	for _, opt := range opts {
		opt(&settings)
	}
	directory, err := NewDirectory(config.Users)
	if err != nil {
		return nil, err
	}
	handler, err := newSubmitHandler(directory, submitter)
	if err != nil {
		return nil, err
	}
	serverConfig := smpps.ServerConfig{
		EnquireLinkTimeout: time.Duration(config.EnquireLinkTimeoutSeconds * float64(time.Second)),
		InactivityTimeout:  time.Duration(config.InactivityTimeoutSeconds * float64(time.Second)),
	}
	serverOpts := []smpps.ServerOption{smpps.WithSubmitHandler(handler)}
	if settings.stats != nil {
		serverOpts = append(serverOpts, smpps.WithStats(settings.stats))
	}
	if settings.logger != nil {
		serverOpts = append(serverOpts, smpps.WithLogger(settings.logger))
	}
	server, err := smpps.NewServer(directory, serverConfig, serverOpts...)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", config.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("smppsserver: listen on %s: %w", config.BindAddr, err)
	}
	if config.TLSCertFile != "" {
		certificate, certErr := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if certErr != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("smppsserver: load TLS keypair: %w", certErr)
		}
		listener = tls.NewListener(listener, &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		})
	}
	return &Service{server: server, listener: listener, directory: directory}, nil
}

// Directory exposes the live user directory so the admin plane can provision
// SMPPs bind users at runtime.
func (s *Service) Directory() *Directory {
	if s == nil {
		return nil
	}
	return s.directory
}

// Addr returns the bound listen address (useful when the config used :0).
func (s *Service) Addr() net.Addr {
	if s == nil || s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Server exposes the underlying SMPPS server for delivery wiring.
func (s *Service) Server() *smpps.Server {
	if s == nil {
		return nil
	}
	return s.server
}

// Run serves until ctx is cancelled or the listener closes.
func (s *Service) Run(ctx context.Context) error {
	err := s.server.Serve(ctx, s.listener)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Close stops the server and releases the listener.
func (s *Service) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Close()
}
