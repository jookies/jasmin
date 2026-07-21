package smppc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

var ErrSessionClosed = errors.New("SMPP session closed")

const maxSequenceNumber uint32 = 0x7fffffff

type pendingRequest struct {
	delivery *amqpcompat.Delivery
	timer    *time.Timer
}

type Session struct {
	conn                 net.Conn
	cfg                  Config
	nextSeq              uint32
	pending              map[uint32]*pendingRequest
	pendingControls      map[uint32]*time.Timer
	mu                   sync.Mutex
	writeMu              sync.Mutex
	cleanup              sync.Once
	closed               chan struct{}
	inactivityTimer      *time.Timer
	inactivityGeneration uint64

	retry     *ErrorRetryPolicy
	readiness *ReadinessPolicy
	onClose   func(error)
}

func NewSession(conn net.Conn, cfg Config, retry *ErrorRetryPolicy, readiness *ReadinessPolicy, onClose func(error)) *Session {
	return &Session{
		conn:            conn,
		cfg:             cfg.Clone(),
		nextSeq:         1, // bind_transceiver uses sequence 1 before session ownership transfers.
		pending:         make(map[uint32]*pendingRequest),
		pendingControls: make(map[uint32]*time.Timer),
		retry:           retry,
		readiness:       readiness,
		onClose:         onClose,
		closed:          make(chan struct{}),
	}
}

func (s *Session) nextSequenceLocked() (uint32, error) {
	for attempts := uint32(0); attempts < maxSequenceNumber; attempts++ {
		if s.nextSeq >= maxSequenceNumber {
			s.nextSeq = 1
		} else {
			s.nextSeq++
		}
		sequence := s.nextSeq
		if _, exists := s.pending[sequence]; exists {
			continue
		}
		if _, exists := s.pendingControls[sequence]; exists {
			continue
		}
		return sequence, nil
	}
	return 0, errors.New("SMPP sequence number space exhausted")
}

// Run owns all reads from the established connection until the session ends.
func (s *Session) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	errCh := make(chan error, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			pdu, err := smppwire.Read(s.conn, smppwire.DefaultMaxSize)
			if err == nil {
				s.resetInactivityTimer()
				err = s.handlePDU(pdu)
			}
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	var enquireTicker *time.Ticker
	var enquireC <-chan time.Time
	if s.cfg.PDUTimeout > 0 {
		enquireTicker = time.NewTicker(seconds(s.cfg.PDUTimeout))
		enquireC = enquireTicker.C
		defer enquireTicker.Stop()
	}
	s.resetInactivityTimer()

	var terminalErr error
selectLoop:
	for {
		select {
		case <-ctx.Done():
			terminalErr = ctx.Err()
			break selectLoop
		case terminalErr = <-errCh:
			break selectLoop
		case <-enquireC:
			terminalErr = s.sendEnquireLink()
			if terminalErr != nil {
				break selectLoop
			}
		}
	}
	_ = s.conn.Close()
	<-readerDone
	s.cleanupSession(terminalErr)
	return terminalErr
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

func (s *Session) resetInactivityTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inactivityGeneration++
	generation := s.inactivityGeneration
	if s.inactivityTimer != nil {
		s.inactivityTimer.Stop()
		s.inactivityTimer = nil
	}
	if s.cfg.PDUTimeout > 0 {
		s.inactivityTimer = time.AfterFunc(seconds(s.cfg.PDUTimeout*2), func() {
			s.expireInactivity(generation)
		})
	}
}

func (s *Session) expireInactivity(generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.inactivityGeneration {
		return
	}
	s.inactivityTimer = nil
	_ = s.conn.Close()
}

func (s *Session) writePDU(pdu smppwire.PDU) error {
	wire, err := smppwire.Encode(pdu)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := writeFrame(s.conn, wire); err != nil {
		return err
	}
	s.resetInactivityTimer()
	return nil
}

func (s *Session) sendEnquireLink() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	select {
	case <-s.closed:
		s.mu.Unlock()
		return ErrSessionClosed
	default:
	}
	seq, err := s.nextSequenceLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.pendingControls[seq] = nil
	s.mu.Unlock()

	wire, err := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
		CommandID:      smppwire.CommandEnquireLink,
		SequenceNumber: seq,
	}})
	if err == nil {
		err = writeFrame(s.conn, wire)
	}
	if err != nil {
		if ownedTimer, owned := s.takePendingControl(seq); owned {
			stopTimer(ownedTimer)
		}
		_ = s.conn.Close()
		return err
	}

	s.mu.Lock()
	if _, pending := s.pendingControls[seq]; pending && s.cfg.ResTimeout > 0 {
		s.pendingControls[seq] = time.AfterFunc(seconds(s.cfg.ResTimeout), func() { s.handleControlTimeout(seq) })
	}
	s.mu.Unlock()
	s.resetInactivityTimer()
	return nil
}

// Submit takes settlement ownership of d on entry. Every return path either
// leaves it pending for a correlated response/timeout or settles it once.
func (s *Session) Submit(ctx context.Context, d *amqpcompat.Delivery) error {
	if d == nil {
		return errors.New("nil AMQP delivery")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		_ = d.Reject(true)
		return err
	}

	pdu := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM},
		SM:     &smppwire.SMBody{ShortMessage: d.Envelope().Body()},
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = d.Reject(true)
		return err
	}

	s.mu.Lock()
	select {
	case <-s.closed:
		s.mu.Unlock()
		_ = d.Reject(true)
		return ErrSessionClosed
	default:
	}
	seq, err := s.nextSequenceLocked()
	if err != nil {
		s.mu.Unlock()
		_ = d.Reject(true)
		return err
	}
	pdu.Header.SequenceNumber = seq
	wire, err := smppwire.Encode(pdu)
	if err != nil {
		s.mu.Unlock()
		_ = d.Reject(true)
		return err
	}
	pending := &pendingRequest{delivery: d}
	s.pending[seq] = pending
	s.mu.Unlock()

	err = writeFrame(s.conn, wire)
	if err != nil {
		if owned := s.takePending(seq); owned != nil {
			stopTimer(owned.timer)
			_ = owned.delivery.Reject(true)
		}
		_ = s.conn.Close()
		return err
	}

	s.mu.Lock()
	if s.pending[seq] == pending && s.cfg.ResTimeout > 0 {
		pending.timer = time.AfterFunc(seconds(s.cfg.ResTimeout), func() { s.handleTimeout(seq) })
	}
	s.mu.Unlock()
	s.resetInactivityTimer()
	return nil
}

func (s *Session) takePending(seq uint32) *pendingRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.pending[seq]
	if pending != nil {
		delete(s.pending, seq)
	}
	return pending
}

func (s *Session) takePendingControl(seq uint32) (*time.Timer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	timer, exists := s.pendingControls[seq]
	if exists {
		delete(s.pendingControls, seq)
	}
	return timer, exists
}

func (s *Session) handleControlTimeout(seq uint32) {
	if _, exists := s.takePendingControl(seq); !exists {
		return
	}
	_ = s.conn.Close()
}

func (s *Session) handlePDU(pdu smppwire.PDU) error {
	switch pdu.Header.CommandID {
	case smppwire.CommandSubmitSMResp:
		s.handleResponse(pdu)
	case smppwire.CommandEnquireLink:
		return s.writePDU(smppwire.PDU{Header: smppwire.Header{
			CommandID:      smppwire.CommandEnquireLinkResp,
			SequenceNumber: pdu.Header.SequenceNumber,
		}})
	case smppwire.CommandEnquireLinkResp:
		if timer, matched := s.takePendingControl(pdu.Header.SequenceNumber); matched {
			stopTimer(timer)
		}
	}
	return nil
}

func (s *Session) handleResponse(pdu smppwire.PDU) {
	pending := s.takePending(pdu.Header.SequenceNumber)
	if pending == nil {
		return
	}
	stopTimer(pending.timer)
	if pdu.Header.CommandStatus == 0 {
		_ = pending.delivery.Ack()
		return
	}
	_ = pending.delivery.Reject(true)
}

func (s *Session) handleTimeout(seq uint32) {
	pending := s.takePending(seq)
	if pending == nil {
		return
	}
	_ = pending.delivery.Reject(true)
	_ = s.conn.Close()
}

func stopTimer(timer *time.Timer) {
	if timer != nil {
		timer.Stop()
	}
}

func writeFrame(conn net.Conn, frame []byte) error {
	for len(frame) > 0 {
		written, err := conn.Write(frame)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		frame = frame[written:]
	}
	return nil
}

func (s *Session) cleanupSession(err error) {
	s.cleanup.Do(func() {
		s.mu.Lock()
		stopTimer(s.inactivityTimer)
		s.inactivityTimer = nil
		pending := s.pending
		pendingControls := s.pendingControls
		s.pending = make(map[uint32]*pendingRequest)
		s.pendingControls = make(map[uint32]*time.Timer)
		close(s.closed)
		s.mu.Unlock()

		for _, request := range pending {
			stopTimer(request.timer)
			_ = request.delivery.Reject(true)
		}
		for _, timer := range pendingControls {
			stopTimer(timer)
		}
		if s.onClose != nil {
			s.onClose(err)
		}
	})
}

func (s *Session) String() string {
	return fmt.Sprintf("SMPP session %s", s.cfg.CID)
}
