package smppsserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
)

// Config is the gateway's smpps-server section.
type Config struct {
	// BindAddr is the listen address (host:port), e.g. "0.0.0.0:2775".
	BindAddr string `json:"bind_addr"`
	// EnquireLinkTimeoutSeconds bounds session inactivity (0 disables).
	EnquireLinkTimeoutSeconds float64 `json:"enquire_link_timeout,omitempty"`
	// Users are the SMPPS accounts allowed to bind.
	Users []UserConfig `json:"users"`
}

func ValidateConfig(config Config) error {
	if config.BindAddr == "" {
		return fmt.Errorf("%w: empty bind_addr", ErrInvalidConfig)
	}
	if config.EnquireLinkTimeoutSeconds < 0 {
		return fmt.Errorf("%w: negative enquire_link_timeout", ErrInvalidConfig)
	}
	if _, err := NewDirectory(config.Users); err != nil {
		return err
	}
	return nil
}

// Service owns the SMPPS listener and server. Deliver exposes the server's
// push path so the DLR/MO throwers' receipt sink can reach bound sessions.
type Service struct {
	server   *smpps.Server
	listener net.Listener
}

// NewService builds the SMPPS server from config, wiring bind auth over the
// directory and submit ingestion over the shared MT submitter. It binds the
// listen socket eagerly so a bad address fails fast at construction.
func NewService(config Config, submitter core.Submitter) (*Service, error) {
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	if submitter == nil {
		return nil, fmt.Errorf("%w: nil submitter", ErrInvalidConfig)
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
	}
	server, err := smpps.NewServer(directory, serverConfig, smpps.WithSubmitHandler(handler))
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", config.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("smppsserver: listen on %s: %w", config.BindAddr, err)
	}
	return &Service{server: server, listener: listener}, nil
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
