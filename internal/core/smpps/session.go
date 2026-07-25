package smpps

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// Session is one SMPPS connection's FSM. It reads inbound PDUs, applies the
// frozen bind-state gate and bind-auth checks, tracks its bound state, and
// (once bound RX/TRX) accepts pushed deliver_sm messages. It implements Binding
// so the server's BindManager can select it for delivery.
type Session struct {
	server *Server
	conn   net.Conn

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
	for {
		if ctx.Err() != nil {
			return
		}
		if s.server.cfg.ReadTimeout > 0 {
			_ = s.conn.SetReadDeadline(time.Now().Add(s.server.cfg.ReadTimeout))
		} else if s.server.cfg.EnquireLinkTimeout > 0 {
			_ = s.conn.SetReadDeadline(time.Now().Add(s.server.cfg.EnquireLinkTimeout))
		}
		pdu, err := smppwire.Read(s.conn, smppwire.DefaultMaxSize)
		if err != nil {
			return
		}
		if !s.dispatch(ctx, pdu) {
			return
		}
	}
}

// dispatch handles one inbound request PDU. It returns false when the session
// must close (fatal gate rejection, unbind, or a write failure).
func (s *Session) dispatch(ctx context.Context, pdu smppwire.PDU) bool {
	command := pdu.Header.CommandID
	sequence := pdu.Header.SequenceNumber
	state := s.currentState()

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
	case CommandSubmitSM, CommandDataSM:
		return s.handleSubmit(ctx, pdu)
	default:
		return s.writeResponse(responseCommandFor(command), sequence, StatusSystemError, nil) == nil
	}
}

// handleSubmit ingests a gate-allowed submit_sm/data_sm through the server's
// SubmitHandler and answers submit_sm_resp/data_sm_resp with the assigned
// message id on ESME_ROK, or the mapped error status. With no handler it
// answers ESME_RSYSERR, keeping the session alive.
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

// deliver encodes and writes a deliver_sm to this session. It fails when the
// session is not in a receive-capable bound state.
func (s *Session) deliver(ctx context.Context, pdu smppwire.PDU) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	deliverable := (s.state == StateBoundRX || s.state == StateBoundTRX) && !s.closed
	s.mu.Unlock()
	if !deliverable {
		return ErrNoBoundSession
	}
	frame, err := smppwire.Encode(pdu)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrNoBoundSession
	}
	if err := writeFrame(s.conn, frame); err != nil {
		return err
	}
	s.server.incStat("deliver_sm_count")
	return nil
}

func (s *Session) transition(state SessionState) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

// cleanup removes the binding from its manager and closes the connection, the
// legacy connectionLost path.
func (s *Session) cleanup() {
	s.server.incStat("disconnect_count")
	s.mu.Lock()
	manager := s.manager
	s.closed = true
	s.mu.Unlock()
	if manager != nil {
		s.server.mu.Lock()
		manager.Remove(s)
		s.server.mu.Unlock()
	}
	_ = s.conn.Close()
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

// writeSubmitResponse writes a success submit_sm_resp/data_sm_resp carrying the
// assigned message id.
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
		return smppwire.CommandSubmitSMResp
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
