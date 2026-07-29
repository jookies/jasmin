package pbfacade

import (
	"context"
	"errors"
	"sync"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

var errSMPPServerNotReady = errors.New("pb facade: SMPP server is not ready")

// SMPPServerSlot breaks the gateway construction cycle: the PB handler is
// composed with admin services before the SMPP listener is built, then the live
// server is installed before any management listener begins accepting calls.
type SMPPServerSlot struct {
	mu     sync.RWMutex
	server SMPPServerService
}

func (s *SMPPServerSlot) Set(server SMPPServerService) error {
	if server == nil {
		return errors.New("pb facade: nil SMPP server")
	}
	s.mu.Lock()
	s.server = server
	s.mu.Unlock()
	return nil
}

func (s *SMPPServerSlot) current() (SMPPServerService, error) {
	s.mu.RLock()
	server := s.server
	s.mu.RUnlock()
	if server == nil {
		return nil, errSMPPServerNotReady
	}
	return server, nil
}

func (s *SMPPServerSlot) BoundSystemIDs() []string {
	server, err := s.current()
	if err != nil {
		return []string{}
	}
	return server.BoundSystemIDs()
}

func (s *SMPPServerSlot) UnbindUser(systemID string) int {
	server, err := s.current()
	if err != nil {
		return 0
	}
	return server.UnbindUser(systemID)
}

func (s *SMPPServerSlot) Deliver(ctx context.Context, systemID string, pdu smppwire.PDU) error {
	server, err := s.current()
	if err != nil {
		return err
	}
	return server.Deliver(ctx, systemID, pdu)
}
