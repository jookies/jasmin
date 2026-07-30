package smpps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

const maxSequenceNumber uint32 = 0x7fffffff

// ErrDeliverSMResponseTimeout means the ESME did not acknowledge a deliver_sm
// before the configured response timer expired.
var ErrDeliverSMResponseTimeout = errors.New("smpps: deliver_sm response timeout")

// DeliverSMResponseError reports a negative deliver_sm_resp or generic_nack.
type DeliverSMResponseError struct {
	CommandID      uint32
	CommandStatus  uint32
	SequenceNumber uint32
}

func (e *DeliverSMResponseError) Error() string {
	return fmt.Sprintf("smpps: command %#x rejected deliver_sm sequence_number %d with status %#x",
		e.CommandID, e.SequenceNumber, e.CommandStatus)
}

// Session is one SMPPS connection's FSM. It reads inbound PDUs, applies the
// frozen bind-state gate and bind-auth checks, tracks its bound state, and
// (once bound RX/TRX) accepts pushed deliver_sm messages. It implements Binding
// so the server's BindManager can select it for delivery.
type Session struct {
	server *Server
	conn   net.Conn

	// outSequence numbers server-originated requests (enquire_link and deliver_sm).
	outSequence uint32
	outstanding map[uint32]chan error
	window      chan struct{}
	done        chan struct{}
	cleanupOnce sync.Once

	// Set at bind time, read by the delivery path.
	mu       sync.Mutex
	state    SessionState
	systemID string
	bindType BindType
	manager  *BindManager // the system_id's manager once bound
	closed   bool
}

// BindType reports the session's bound type (valid once bound). It satisfies
// the Binding interface for the BindManager.
func (s *Session) BindType() BindType {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindType
}

func (s *Session) currentState() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// run drives the read loop until the connection closes, the context cancels, or
// a fatal protocol error occurs. On exit it removes any binding from the
// manager, matching the legacy connectionLost cleanup.
func (s *Session) run(ctx context.Context) {
	s.server.incStat("connect_count")
	s.server.incStat("connected_count")
	defer s.cleanup()
	keepalive := s.server.cfg.EnquireLinkTimeout
	inactivity := s.server.cfg.InactivityTimeout
	lastActivity := time.Now()
	for {
		if ctx.Err() != nil {
			return
		}
		s.setReadDeadline(keepalive, inactivity, lastActivity)
		pdu, err := smppwire.Read(s.conn, smppwire.DefaultMaxSize)
		if err != nil {
			var parseErr *smppwire.ParseError
			if errors.As(err, &parseErr) {
				if writeErr := s.writeHeader(smppwire.CommandGenericNACK,
					parseErr.Header.SequenceNumber, parseErr.CommandStatus); writeErr != nil {
					return
				}
				if errors.Is(err, smppwire.ErrInvalidCommandLength) ||
					errors.Is(err, smppwire.ErrFrameTooLarge) {
					return
				}
				lastActivity = time.Now()
				continue
			}
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				return
			}
			// A quiet socket is not a dead one. Legacy drops the session only
			// once inactivityTimerSecs has passed with nothing received;
			// before that, the enquire-link timer fires and we probe the peer.
			if inactivity > 0 && time.Since(lastActivity) >= inactivity {
				return
			}
			if keepalive <= 0 {
				return
			}
			if !s.sendEnquireLink() {
				return
			}
			continue
		}
		lastActivity = time.Now()
		if !s.dispatch(ctx, pdu) {
			return
		}
	}
}

// setReadDeadline arms the next read. The enquire-link interval paces the
// keepalive probes; the inactivity deadline is the only thing that actually
// ends the session, and it is measured from the last PDU genuinely received.
func (s *Session) setReadDeadline(keepalive, inactivity time.Duration, lastActivity time.Time) {
	if s.server.cfg.ReadTimeout > 0 {
		_ = s.conn.SetReadDeadline(time.Now().Add(s.server.cfg.ReadTimeout))
		return
	}
	switch {
	case keepalive > 0:
		_ = s.conn.SetReadDeadline(time.Now().Add(keepalive))
	case inactivity > 0:
		_ = s.conn.SetReadDeadline(lastActivity.Add(inactivity))
	default:
		_ = s.conn.SetReadDeadline(time.Time{})
	}
}

// sendEnquireLink probes a quiet peer, mirroring enquireLinkTimerExpired. The
// peer's enquire_link_resp arrives as a response PDU and is consumed by
// handleResponse, which is what keeps the session open.
func (s *Session) sendEnquireLink() bool {
	return s.writeHeader(smppwire.CommandEnquireLink, s.nextSequence(), StatusROK) == nil
}

func (s *Session) nextSequence() uint32 {
	for {
		current := atomic.LoadUint32(&s.outSequence)
		next := current + 1
		if current >= maxSequenceNumber {
			next = 1
		}
		if atomic.CompareAndSwapUint32(&s.outSequence, current, next) {
			return next
		}
	}
}

// dispatch handles one inbound request PDU. It returns false when the session
// must close (fatal gate rejection, unbind, or a write failure).
func (s *Session) dispatch(ctx context.Context, pdu smppwire.PDU) bool {
	command := pdu.Header.CommandID
	sequence := pdu.Header.SequenceNumber
	state := s.currentState()

	// Responses are not requests. SMPP 3.4 routes them to PDUResponseReceived,
	// they are never answered, and they must not reach the request gate: an
	// ESME MUST ack every deliver_sm we send it (§4.6), so treating that ack as
	// an unsupported request tore the bind down on the first MO or delivery
	// receipt the customer received. unbind_resp keeps its existing handling in
	// the gate below, which closes the session deliberately.
	if isResponseCommand(command) && command != CommandUnbindResp {
		s.handleResponse(pdu)
		return true
	}

	allowed, status := CommandAllowed(state, command)
	if !allowed {
		respCommand := responseCommandFor(command)
		// ESME_RSYSERR is fatal (unsupported pdu); ESME_RINVBNDSTS /
		// ESME_RALYBND are answered with an error response for the command and
		// the session stays open — the legacy PDUDataRequestReceived contract.
		if status == StatusSystemError {
			_ = s.writeResponse(respCommand, sequence, status, nil)
			return false
		}
		return s.writeResponse(respCommand, sequence, status, nil) == nil
	}

	switch command {
	case CommandBindReceiver, CommandBindTransmitter, CommandBindTransceiver:
		return s.handleBind(pdu)
	case CommandEnquireLink:
		s.server.incStat("elink_count")
		return s.writeHeader(smppwire.CommandEnquireLinkResp, sequence, StatusROK) == nil
	case CommandUnbind:
		s.server.incStat("unbind_count")
		_ = s.writeHeader(smppwire.CommandUnbindResp, sequence, StatusROK)
		s.transition(StateUnbound)
		return false
	case CommandSubmitSM:
		return s.handleSubmit(ctx, pdu)
	case CommandDataSM:
		// smpp.twisted delegates a bound data_sm to Jasmin's submit handler,
		// which accepts only submit_sm and returns a no-shutdown ESME_RSYSERR.
		return s.writeResponse(smppwire.CommandDataSMResp, sequence, StatusSystemError, nil) == nil
	default:
		return s.writeResponse(responseCommandFor(command), sequence, StatusSystemError, nil) == nil
	}
}

// isResponseCommand reports whether a command_id is a response. SMPP 3.4 sets
// the high bit of the command_id for every response PDU.
func isResponseCommand(command uint32) bool {
	return command&0x80000000 != 0
}

// handleResponse consumes a response PDU from the ESME. Nothing is written back
// — answering a response is a protocol error. A deliver_sm_resp or generic_nack
// settles the outstanding request with the same sequence_number.
func (s *Session) handleResponse(pdu smppwire.PDU) {
	command := pdu.Header.CommandID
	if command == smppwire.CommandDeliverSMResp {
		s.server.incStat("deliver_sm_resp_count")
	}
	if command != smppwire.CommandDeliverSMResp && command != smppwire.CommandGenericNACK {
		return
	}

	s.mu.Lock()
	result, ok := s.outstanding[pdu.Header.SequenceNumber]
	if ok {
		delete(s.outstanding, pdu.Header.SequenceNumber)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	if command == smppwire.CommandDeliverSMResp && pdu.Header.CommandStatus == StatusROK {
		result <- nil
		return
	}
	result <- &DeliverSMResponseError{
		CommandID:      command,
		CommandStatus:  pdu.Header.CommandStatus,
		SequenceNumber: pdu.Header.SequenceNumber,
	}
}

// handleSubmit ingests a gate-allowed submit_sm through the server's
// SubmitHandler and answers submit_sm_resp with the assigned message id on
// ESME_ROK, or the mapped error status. With no handler it answers
// ESME_RSYSERR, keeping the session alive.
func (s *Session) handleSubmit(ctx context.Context, pdu smppwire.PDU) bool {
	command := pdu.Header.CommandID
	sequence := pdu.Header.SequenceNumber
	respCommand := responseCommandFor(command)
	s.server.incStat("submit_sm_request_count")
	if s.server.submit == nil || pdu.SM == nil {
		return s.writeResponse(respCommand, sequence, StatusSystemError, nil) == nil
	}
	s.mu.Lock()
	systemID := s.systemID
	s.mu.Unlock()

	messageID, status := s.server.submit.HandleSubmit(ctx, systemID, pdu.SM)
	if status != StatusROK {
		return s.writeResponse(respCommand, sequence, status, nil) == nil
	}
	s.server.incStat("submit_sm_count")
	return s.writeSubmitResponse(respCommand, sequence, messageID) == nil
}

// handleBind runs the frozen bind-auth checks and, on success, registers the
// binding and transitions state. Any outcome sends a bind response with the
// matching command_status; a rejection keeps the session OPEN (the legacy
// server leaves the ESME to retry or disconnect).
func (s *Session) handleBind(pdu smppwire.PDU) bool {
	command := pdu.Header.CommandID
	sequence := pdu.Header.SequenceNumber
	respCommand := bindResponseCommand(command)
	if pdu.Bind == nil {
		_ = s.writeResponse(respCommand, sequence, StatusSystemError, nil)
		return false
	}
	systemID := string(pdu.Bind.SystemID)
	password := string(pdu.Bind.Password)
	s.server.incStat(bindRequestMetric(command))

	user, ok := s.server.resolver.ResolveUser(systemID)
	if !ok {
		_ = s.writeBindResponse(respCommand, sequence, StatusInvalidPassword, systemID)
		return true
	}
	manager := s.server.managerFor(systemID)

	// Serialize the count-check-and-add against concurrent binds for the same
	// system_id so max_bindings is enforced exactly.
	s.server.mu.Lock()
	status := AuthorizeBind(user, password, peerIP(s.conn), manager.Count())
	if status == StatusROK {
		bindType, _ := BindTypeForCommand(command)
		s.mu.Lock()
		s.systemID = systemID
		s.bindType = bindType
		s.manager = manager
		s.mu.Unlock()
		manager.Add(s)
		s.server.logBind("Added", bindType, systemID, manager)
	}
	s.server.mu.Unlock()

	if status != StatusROK {
		return s.writeBindResponse(respCommand, sequence, status, systemID) == nil
	}
	s.server.incStat(boundStateMetric(command))
	newState, _ := StateForBind(command)
	s.transition(newState)
	return s.writeBindResponse(respCommand, sequence, StatusROK, systemID) == nil
}

// bindRequestMetric maps a bind command to its bind_*_count metric.
func bindRequestMetric(command uint32) string {
	switch command {
	case CommandBindReceiver:
		return "bind_rx_count"
	case CommandBindTransmitter:
		return "bind_tx_count"
	default:
		return "bind_trx_count"
	}
}

// boundStateMetric maps a bind command to its bound_*_count metric.
func boundStateMetric(command uint32) string {
	switch command {
	case CommandBindReceiver:
		return "bound_rx_count"
	case CommandBindTransmitter:
		return "bound_tx_count"
	default:
		return "bound_trx_count"
	}
}

// deliver writes a deliver_sm and waits for its correlated response. Unlike
// Jasmin's unbounded fire-and-forget path, each session has a bounded window:
// callers only report success after the ESME acknowledges the request.
func (s *Session) deliver(ctx context.Context, pdu smppwire.PDU) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	select {
	case s.window <- struct{}{}:
		defer func() { <-s.window }()
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrNoBoundSession
	}

	pdu.Header.SequenceNumber = s.nextSequence()
	frame, err := smppwire.Encode(pdu)
	if err != nil {
		return err
	}

	result := make(chan error, 1)
	s.mu.Lock()
	deliverable := (s.state == StateBoundRX || s.state == StateBoundTRX) && !s.closed
	if !deliverable {
		s.mu.Unlock()
		return ErrNoBoundSession
	}
	s.outstanding[pdu.Header.SequenceNumber] = result
	writeDeadline := time.Now().Add(s.server.cfg.DeliverSMResponseTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(writeDeadline) {
		writeDeadline = deadline
	}
	if err := s.conn.SetWriteDeadline(writeDeadline); err != nil {
		delete(s.outstanding, pdu.Header.SequenceNumber)
		s.mu.Unlock()
		return fmt.Errorf("smpps: set deliver_sm write deadline: %w", err)
	}
	writeErr := writeFrame(s.conn, frame)
	clearDeadlineErr := s.conn.SetWriteDeadline(time.Time{})
	if writeErr != nil {
		delete(s.outstanding, pdu.Header.SequenceNumber)
		s.mu.Unlock()
		return writeErr
	}
	if clearDeadlineErr != nil {
		delete(s.outstanding, pdu.Header.SequenceNumber)
		s.mu.Unlock()
		return fmt.Errorf("smpps: clear deliver_sm write deadline: %w", clearDeadlineErr)
	}
	s.mu.Unlock()
	s.server.incStat("deliver_sm_count")

	timer := time.NewTimer(s.server.cfg.DeliverSMResponseTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		s.removeOutstanding(pdu.Header.SequenceNumber)
		return ctx.Err()
	case <-timer.C:
		s.removeOutstanding(pdu.Header.SequenceNumber)
		return fmt.Errorf("%w: sequence_number %d", ErrDeliverSMResponseTimeout, pdu.Header.SequenceNumber)
	case <-s.done:
		s.removeOutstanding(pdu.Header.SequenceNumber)
		return ErrNoBoundSession
	}
}

func (s *Session) removeOutstanding(sequence uint32) {
	s.mu.Lock()
	delete(s.outstanding, sequence)
	s.mu.Unlock()
}

func (s *Session) transition(state SessionState) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

// cleanup removes the binding from its manager and closes the connection, the
// legacy connectionLost path.
func (s *Session) cleanup() {
	s.cleanupOnce.Do(func() {
		s.server.incStat("disconnect_count")
		s.mu.Lock()
		manager := s.manager
		bindType := s.bindType
		systemID := s.systemID
		s.closed = true
		close(s.done)
		s.mu.Unlock()
		if manager != nil {
			s.server.mu.Lock()
			manager.Remove(s)
			s.server.logBind("Dropped", bindType, systemID, manager)
			s.server.mu.Unlock()
		}
		_ = s.conn.Close()
	})
}

func (s *Session) writeHeader(command, sequence uint32, status uint32) error {
	return s.writeResponse(command, sequence, status, nil)
}

func (s *Session) writeBindResponse(command, sequence, status uint32, systemID string) error {
	var body *smppwire.BindResponseBody
	if status == StatusROK {
		body = &smppwire.BindResponseBody{SystemID: []byte(systemID)}
	}
	return s.writeResponse(command, sequence, status, body)
}

// writeSubmitResponse writes a success submit_sm_resp carrying the assigned
// message id.
func (s *Session) writeSubmitResponse(command, sequence uint32, messageID string) error {
	pdu := smppwire.PDU{
		Header:         smppwire.Header{CommandID: command, CommandStatus: StatusROK, SequenceNumber: sequence},
		SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte(messageID)},
	}
	frame, err := smppwire.Encode(pdu)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("smpps: session closed")
	}
	return writeFrame(s.conn, frame)
}

func (s *Session) writeResponse(command, sequence, status uint32, bindResponse *smppwire.BindResponseBody) error {
	pdu := smppwire.PDU{
		Header:       smppwire.Header{CommandID: command, CommandStatus: status, SequenceNumber: sequence},
		BindResponse: bindResponse,
	}
	frame, err := smppwire.Encode(pdu)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("smpps: session closed")
	}
	return writeFrame(s.conn, frame)
}

// responseCommandFor maps an inbound request command to the response command
// used to answer it (including gate rejections).
func responseCommandFor(command uint32) uint32 {
	switch command {
	case CommandBindReceiver, CommandBindTransmitter, CommandBindTransceiver:
		return bindResponseCommand(command)
	case CommandSubmitSM:
		return smppwire.CommandSubmitSMResp
	case CommandDataSM:
		return smppwire.CommandDataSMResp
	case CommandEnquireLink:
		return smppwire.CommandEnquireLinkResp
	case CommandUnbind:
		return smppwire.CommandUnbindResp
	default:
		return smppwire.CommandGenericNACK
	}
}

// bindResponseCommand maps a bind request command to its response command.
func bindResponseCommand(command uint32) uint32 {
	switch command {
	case CommandBindReceiver:
		return smppwire.CommandBindReceiverResp
	case CommandBindTransmitter:
		return smppwire.CommandBindTransmitterResp
	default:
		return smppwire.CommandBindTransceiverResp
	}
}

func peerIP(conn net.Conn) string {
	addr := conn.RemoteAddr()
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
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
