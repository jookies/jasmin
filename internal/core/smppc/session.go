package smppc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type pendingRequest struct {
	delivery *amqpcompat.Delivery
	timer    *time.Timer
	done     chan struct{}
}

type Session struct {
	conn            net.Conn
	cfg             Config
	nextSeq         uint32
	pending         map[uint32]*pendingRequest
	mu              sync.Mutex
	inactivityTimer *time.Timer
	retry           *ErrorRetryPolicy
	readiness       *ReadinessPolicy

	onClose func(error)
	closed  chan struct{}
}

func NewSession(conn net.Conn, cfg Config, retry *ErrorRetryPolicy, readiness *ReadinessPolicy, onClose func(error)) *Session {
	return &Session{
		conn:      conn,
		cfg:       cfg,
		nextSeq:   0,
		pending:   make(map[uint32]*pendingRequest),
		retry:     retry,
		readiness: readiness,
		onClose:   onClose,
		closed:    make(chan struct{}),
	}
}

func (s *Session) getNextSeq() uint32 {
	return atomic.AddUint32(&s.nextSeq, 1)
}

func (s *Session) Run(ctx context.Context) {
	errChan := make(chan error, 1)

	// PDU Reader loop
	go func() {
		for {
			pdu, err := smppwire.Read(s.conn, smppwire.DefaultMaxSize)
			if err != nil {
				errChan <- err
				return
			}
			s.resetInactivityTimer()
			s.handlePDU(pdu)
		}
	}()

	// Enquire Link loop
	var enquireTicker *time.Ticker
	if s.cfg.PDUTimeout > 0 {
		enquireTicker = time.NewTicker(time.Duration(s.cfg.PDUTimeout) * time.Second)
		defer enquireTicker.Stop()
	}
	enquireC := make(<-chan time.Time)

	s.resetInactivityTimer()
	defer func() {
		s.mu.Lock()
		if s.inactivityTimer != nil {
			s.inactivityTimer.Stop()
		}
		s.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			s.conn.Close()
			return
		case err := <-errChan:
			s.cleanup(err)
			return
		case <-func() <-chan time.Time {
			if enquireTicker != nil {
				return enquireTicker.C
			}
			return enquireC
		}():
			s.sendEnquireLink()
		}
	}
}

func (s *Session) resetInactivityTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.inactivityTimer != nil {
		s.inactivityTimer.Stop()
	}

	if s.cfg.PDUTimeout > 0 {
		s.inactivityTimer = time.AfterFunc(time.Duration(s.cfg.PDUTimeout*2)*time.Second, func() {
			fmt.Printf("DEBUG: [%s] Inactivity timeout\n", s.cfg.CID)
			s.conn.Close()
		})
	}
}

func (s *Session) sendEnquireLink() {
	pdu := smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandEnquireLink,
			SequenceNumber: s.getNextSeq(),
		},
	}
	wire, _ := smppwire.Encode(pdu)
	_, _ = s.conn.Write(wire)
}

func (s *Session) Submit(ctx context.Context, d *amqpcompat.Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.closed:
		return fmt.Errorf("session closed")
	default:
	}

	seq := s.getNextSeq()

	pdu := smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandSubmitSM,
			SequenceNumber: seq,
		},
		SM: &smppwire.SMBody{
			ShortMessage: d.Envelope().Body(),
		},
	}

	wire, err := smppwire.Encode(pdu)
	if err != nil {
		return err
	}

	pending := &pendingRequest{
		delivery: d,
		done:     make(chan struct{}),
	}

	pending.timer = time.AfterFunc(time.Duration(s.cfg.ResTimeout)*time.Second, func() {
		s.handleTimeout(seq)
	})

	s.pending[seq] = pending

	_, err = s.conn.Write(wire)
	return err
}

func (s *Session) handlePDU(pdu smppwire.PDU) {
	switch pdu.Header.CommandID {
	case smppwire.CommandSubmitSMResp:
		s.handleResponse(pdu)
	case smppwire.CommandEnquireLink:
		s.handleEnquireLink(pdu)
	case smppwire.CommandEnquireLinkResp:
		// Reset inactivity already done in Run loop
	}
}

func (s *Session) handleResponse(pdu smppwire.PDU) {
	s.mu.Lock()
	pending, ok := s.pending[pdu.Header.SequenceNumber]
	if ok {
		delete(s.pending, pdu.Header.SequenceNumber)
	}
	s.mu.Unlock()

	if !ok {
		return
	}

	pending.timer.Stop()
	close(pending.done)

	if pdu.Header.CommandStatus == 0 { // ESME_ROK
		_ = pending.delivery.Ack()
	} else {
		// Mapping to ErrorRetryPolicy can be added here
		_ = pending.delivery.Reject(true)
	}
}

func (s *Session) handleTimeout(seq uint32) {
	s.mu.Lock()
	pending, ok := s.pending[seq]
	if ok {
		delete(s.pending, seq)
	}
	s.mu.Unlock()

	if !ok {
		return
	}

	close(pending.done)
	_ = pending.delivery.Reject(true)
	s.conn.Close()
}

func (s *Session) handleEnquireLink(pdu smppwire.PDU) {
	resp := smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandEnquireLinkResp,
			SequenceNumber: pdu.Header.SequenceNumber,
		},
	}
	wire, _ := smppwire.Encode(resp)
	_, _ = s.conn.Write(wire)
}

func (s *Session) cleanup(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.closed:
		return
	default:
		close(s.closed)
	}

	for _, pending := range s.pending {
		pending.timer.Stop()
		_ = pending.delivery.Reject(true)
	}
	s.pending = nil

	if s.onClose != nil {
		s.onClose(err)
	}
}
