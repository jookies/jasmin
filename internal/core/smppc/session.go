package smppc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

var ErrSessionClosed = errors.New("SMPP session closed")
var ErrAMQPConsumerLost = errors.New("AMQP consumer generation lost")

const maxSequenceNumber uint32 = 0x7fffffff

type pendingRequest struct {
	delivery *amqpcompat.Delivery
	timer    *time.Timer
	attempt  submittransaction.SendAttempt
	partKey  string
	envelope amqpcompat.Envelope
	// chain is non-nil when this is one part of a multipart submit sharing one
	// durable attempt/delivery; it settles the message exactly once.
	chain *submitChain
	// The decoded request fields the SMS-MT audit line renders (captured at send
	// time; the response carries none of them). Slices alias the decoded body,
	// which is not mutated after send.
	sourceAddr         []byte
	destAddr           []byte
	shortMessage       []byte
	registeredDelivery byte
	optional           smppwire.OptionalParameters
	customTLVs         []tlv.TLV
}

type SubmitDecoder interface {
	DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error)
}

// chainDecoder is the optional multipart extension: a decoder that projects the
// full nextPdu chain of a legacy submit. The bridge implements it; a decoder
// without it is treated as always single-part.
type chainDecoder interface {
	DecodeSubmitSMChain(context.Context, []byte) ([]picklecompat.SubmitSMChainPart, error)
}

type SubmitResponseEncoder interface {
	EncodeSubmitSMResponse(context.Context, uint32, uint32, []byte) ([]byte, error)
}

type rawSubmitDecoder struct{}

func (rawSubmitDecoder) DecodeSubmitSM(_ context.Context, body []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	return smppwire.SubmitSMBody{ShortMessage: append([]byte(nil), body...)}, nil, nil
}

// Session owns one SMPP connection and, when driven by Connector.runConsumer,
// exactly one AMQP consumer generation. AbortConsumerGeneration is irreversible;
// a replacement consumer must use a newly connected Session.
type pendingControl struct {
	timer                   *time.Timer
	expectedResponseCommand uint32
}

type Session struct {
	conn                 net.Conn
	cfg                  Config
	nextSeq              uint32
	pending              map[uint32]*pendingRequest
	pendingControls      map[uint32]*pendingControl
	mu                   sync.Mutex
	writeMu              sync.Mutex
	cleanup              sync.Once
	closed               chan struct{}
	consumerLost         bool
	inactivityTimer      *time.Timer
	inactivityGeneration uint64

	retry           *ErrorRetryPolicy
	readiness       *ReadinessPolicy
	decoder         SubmitDecoder
	responseEncoder SubmitResponseEncoder
	transactions    *submittransaction.Service
	responses       *DurableResponseLifecycle
	onClose         func(error)

	// auditLogger renders the legacy SMS-MT audit line on a final submit_sm_resp;
	// nil (the default) disables it. auditPrivacy is the sm-listener log_privacy.
	auditLogger  *slog.Logger
	auditPrivacy bool
}

// SetSubmitAuditLogger enables the legacy SMS-MT audit line for correlated
// submit_sm_resp events. A nil logger (the default) disables it. Must be called
// before the session handles responses; the connector sets it at session creation.
func (s *Session) SetSubmitAuditLogger(logger *slog.Logger, privacy bool) {
	s.auditLogger = logger
	s.auditPrivacy = privacy
}

// NewSession preserves the pre-decoder constructor for focused compatibility
// tests. Production connector composition must use NewSessionWithDecoder.
func NewSession(conn net.Conn, cfg Config, retry *ErrorRetryPolicy, readiness *ReadinessPolicy, onClose func(error)) *Session {
	return NewSessionWithDecoder(conn, cfg, retry, readiness, rawSubmitDecoder{}, onClose)
}

func NewSessionWithDecoder(conn net.Conn, cfg Config, retry *ErrorRetryPolicy, readiness *ReadinessPolicy, decoder SubmitDecoder, onClose func(error)) *Session {
	return NewSessionWithDurability(conn, cfg, retry, readiness, decoder, nil, onClose)
}

func NewSessionWithDurability(conn net.Conn, cfg Config, retry *ErrorRetryPolicy, readiness *ReadinessPolicy, decoder SubmitDecoder, transactions *submittransaction.Service, onClose func(error)) *Session {
	var responses *DurableResponseLifecycle
	if transactions != nil && retry != nil {
		responses, _ = NewDurableResponseLifecycle(transactions, retry, nil)
	}
	responseEncoder, _ := decoder.(SubmitResponseEncoder)
	return &Session{
		conn:            conn,
		cfg:             cfg.Clone(),
		nextSeq:         1, // bind_transceiver uses sequence 1 before session ownership transfers.
		pending:         make(map[uint32]*pendingRequest),
		pendingControls: make(map[uint32]*pendingControl),
		retry:           retry,
		readiness:       readiness,
		decoder:         decoder,
		responseEncoder: responseEncoder,
		transactions:    transactions,
		responses:       responses,
		onClose:         onClose,
		closed:          make(chan struct{}),
	}
}

// AbortConsumerGeneration permanently fences this SMPP session from the AMQP
// consumer generation that fed it. Pending local delivery handles are abandoned
// without broker settlement, then the socket is closed to interrupt any in-flight
// write before the broker can redeliver the same message to a new generation.
func (s *Session) AbortConsumerGeneration() {
	s.mu.Lock()
	if s.consumerLost {
		s.mu.Unlock()
		_ = s.conn.Close()
		return
	}
	s.consumerLost = true
	pending := s.pending
	s.pending = make(map[uint32]*pendingRequest)
	s.mu.Unlock()
	// Interrupt any in-flight write before waiting on the durable fence. The new
	// generation cannot safely reuse an attempt that may have reached the SMSC.
	_ = s.conn.Close()

	for _, request := range pending {
		stopTimer(request.timer)
		_ = s.markPendingUnknown(request)
		_ = request.delivery.Abandon()
	}
}

func (s *Session) markPendingUnknown(request *pendingRequest) error {
	if request == nil || s.transactions == nil || request.attempt.ID <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.transactions.MarkUnknownAfterSend(ctx, request.attempt.ID)
}

func (s *Session) settleDeliveryReject(delivery *amqpcompat.Delivery, requeue bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumerLost {
		_ = delivery.Abandon()
		return
	}
	_ = delivery.Reject(requeue)
}

func (s *Session) settleDeliveryFailure(delivery *amqpcompat.Delivery) {
	s.settleDeliveryReject(delivery, true)
}

func (s *Session) settleDeliveryResponse(delivery *amqpcompat.Delivery, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumerLost {
		_ = delivery.Abandon()
		return
	}
	if success {
		_ = delivery.Ack()
		return
	}
	_ = delivery.Reject(true)
}

func (s *Session) consumerGenerationLost() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumerLost
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
	s.pendingControls[seq] = &pendingControl{expectedResponseCommand: smppwire.CommandEnquireLinkResp}
	s.mu.Unlock()

	wire, err := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
		CommandID:      smppwire.CommandEnquireLink,
		SequenceNumber: seq,
	}})
	if err == nil {
		err = writeFrame(s.conn, wire)
	}
	if err != nil {
		if ownedTimer, owned := s.takePendingControl(seq, smppwire.CommandEnquireLinkResp); owned {
			stopTimer(ownedTimer)
		}
		_ = s.conn.Close()
		return err
	}

	s.mu.Lock()
	if pending := s.pendingControls[seq]; pending != nil && s.cfg.ResTimeout > 0 {
		pending.timer = time.AfterFunc(seconds(s.cfg.ResTimeout), func() { s.handleControlTimeout(seq) })
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
		s.settleDeliveryFailure(d)
		return err
	}

	if s.decoder == nil {
		s.settleDeliveryFailure(d)
		return errors.New("SubmitSM decoder is required")
	}
	parts, err := s.decodeSubmitParts(ctx, d.Envelope().Body())
	if err != nil {
		if errors.Is(err, picklecompat.ErrSubmitSMPoison) {
			s.settleDeliveryReject(d, false)
		} else {
			s.settleDeliveryFailure(d)
		}
		return fmt.Errorf("decode legacy SubmitSM envelope: %w", err)
	}
	// Resolve each part's connector-declared vendor TLVs into its wire body. Any
	// failure is the legacy rejectMessage — settle without requeue.
	bodies := make([]smppwire.SubmitSMBody, len(parts))
	for i := range parts {
		body := parts[i].Body
		if len(parts[i].CustomTLVs) > 0 || len(s.cfg.CustomTLVs) > 0 {
			vendorSection, tlvErr := prepareVendorTLVs(parts[i].CustomTLVs, s.cfg.ConnectorTLVRules())
			if tlvErr != nil {
				s.settleDeliveryReject(d, false)
				return fmt.Errorf("%w: %w", ErrCustomTLVRejected, tlvErr)
			}
			body.VendorTLVs = vendorSection
		}
		bodies[i] = body
	}
	envelope := d.Envelope()
	var attempt submittransaction.SendAttempt
	partKey, err := durablePartKey(envelope)
	if err != nil {
		s.settleDeliveryReject(d, false)
		return err
	}
	if s.transactions != nil {
		var committed bool
		var beginErr error
		attempt, committed, beginErr = s.transactions.BeginAttempt(ctx, partKey)
		if beginErr != nil {
			s.settleDeliveryFailure(d)
			return fmt.Errorf("commit send intent: %w", beginErr)
		}
		if committed {
			s.settleDeliveryResponse(d, true)
			return nil
		}
	}
	// A multipart submit shares one attempt/delivery across N wire PDUs; the
	// chain settles the message exactly once (last response, timeout, or drop).
	var chain *submitChain
	if len(bodies) > 1 {
		chain = &submitChain{remaining: len(bodies)}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		s.settleDeliveryFailure(d)
		return err
	}

	// Phase 1: assign a sequence, encode, and register a pending for every part
	// under one lock, so an encode/sequence failure aborts before any part hits
	// the wire (no half-sent chain).
	type framedPart struct {
		seq     uint32
		wire    []byte
		pending *pendingRequest
	}
	s.mu.Lock()
	if s.consumerLost {
		s.mu.Unlock()
		s.settleDeliveryFailure(d)
		return ErrAMQPConsumerLost
	}
	select {
	case <-s.closed:
		s.mu.Unlock()
		s.settleDeliveryFailure(d)
		return ErrSessionClosed
	default:
	}
	frames := make([]framedPart, 0, len(bodies))
	for i := range bodies {
		seq, seqErr := s.nextSequenceLocked()
		if seqErr == nil {
			pdu := smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: seq}, SM: &bodies[i]}
			var wire []byte
			wire, seqErr = smppwire.Encode(pdu)
			if seqErr == nil {
				pending := &pendingRequest{
					delivery: d, attempt: attempt, partKey: partKey, envelope: envelope, chain: chain,
					sourceAddr: bodies[i].SourceAddress, destAddr: bodies[i].DestinationAddress,
					shortMessage: bodies[i].ShortMessage, registeredDelivery: bodies[i].RegisteredDelivery,
					optional: bodies[i].Optional, customTLVs: parts[i].CustomTLVs,
				}
				s.pending[seq] = pending
				frames = append(frames, framedPart{seq: seq, wire: wire, pending: pending})
				continue
			}
		}
		for _, f := range frames {
			delete(s.pending, f.seq)
		}
		s.mu.Unlock()
		s.settleDeliveryFailure(d)
		return seqErr
	}
	s.mu.Unlock()

	// Phase 2: write every part's frame. A write failure breaks the connection;
	// settle the message once here and let cleanupSession skip the rest via the
	// chain guard.
	for _, f := range frames {
		if writeErr := writeFrame(s.conn, f.wire); writeErr != nil {
			if owned := s.takePending(f.seq); owned != nil {
				stopTimer(owned.timer)
				s.settlePendingFailure(owned)
			}
			_ = s.conn.Close()
			if s.consumerGenerationLost() {
				return ErrAMQPConsumerLost
			}
			return writeErr
		}
	}
	if s.transactions != nil {
		// The write is already externally ambiguous. MarkSent is deliberately
		// best-effort; recovery maps both INTENT and SENT to UNKNOWN_AFTER_SEND.
		_ = s.transactions.MarkSent(context.Background(), attempt.ID)
	}

	// Phase 3: arm a response timer per part.
	if s.cfg.ResTimeout > 0 {
		s.mu.Lock()
		for _, f := range frames {
			if s.pending[f.seq] == f.pending {
				seq := f.seq
				f.pending.timer = time.AfterFunc(seconds(s.cfg.ResTimeout), func() { s.handleTimeout(seq) })
			}
		}
		s.mu.Unlock()
	}
	s.resetInactivityTimer()
	return nil
}

// decodeSubmitParts projects the submit body into its multipart chain when the
// decoder supports it, else a single part.
func (s *Session) decodeSubmitParts(ctx context.Context, body []byte) ([]picklecompat.SubmitSMChainPart, error) {
	if cd, ok := s.decoder.(chainDecoder); ok {
		return cd.DecodeSubmitSMChain(ctx, body)
	}
	single, tuples, err := s.decoder.DecodeSubmitSM(ctx, body)
	if err != nil {
		return nil, err
	}
	return []picklecompat.SubmitSMChainPart{{Body: single, CustomTLVs: tuples}}, nil
}

// settlePendingFailure marks the attempt unknown and settles the delivery as
// failed. For a chained part only the first caller (per the chain guard) settles
// the shared delivery; later parts are no-ops.
func (s *Session) settlePendingFailure(pending *pendingRequest) {
	if pending.chain != nil && !pending.chain.finalize() {
		return
	}
	_ = s.markPendingUnknown(pending)
	s.settleDeliveryFailure(pending.delivery)
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

func (s *Session) takePendingControl(seq uint32, responseCommand uint32) (*time.Timer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, exists := s.pendingControls[seq]
	if exists && pending.expectedResponseCommand == responseCommand {
		delete(s.pendingControls, seq)
		return pending.timer, true
	}
	return nil, false
}

func (s *Session) handleControlTimeout(seq uint32) {
	s.mu.Lock()
	_, exists := s.pendingControls[seq]
	if exists {
		delete(s.pendingControls, seq)
	}
	s.mu.Unlock()
	if !exists {
		return
	}
	_ = s.conn.Close()
}

func (s *Session) Unbind(ctx context.Context) error {
	s.mu.Lock()
	select {
	case <-s.closed:
		s.mu.Unlock()
		return ErrSessionClosed
	default:
	}
	sequence, err := s.nextSequenceLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.pendingControls[sequence] = &pendingControl{
		expectedResponseCommand: smppwire.CommandUnbindResp,
		timer: time.AfterFunc(seconds(s.cfg.TrxTimeout), func() {
			s.handleControlTimeout(sequence)
		}),
	}
	s.mu.Unlock()
	if err := s.writePDU(smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandUnbind, SequenceNumber: sequence}}); err != nil {
		if timer, matched := s.takePendingControl(sequence, smppwire.CommandUnbindResp); matched {
			stopTimer(timer)
		}
		return err
	}
	select {
	case <-s.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) handlePDU(pdu smppwire.PDU) error {
	switch pdu.Header.CommandID {
	case smppwire.CommandSubmitSMResp:
		s.handleResponse(pdu)
	case smppwire.CommandUnbind:
		_ = s.writePDU(smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandUnbindResp, SequenceNumber: pdu.Header.SequenceNumber}})
		return io.EOF
	case smppwire.CommandUnbindResp:
		timer, matched := s.takePendingControl(pdu.Header.SequenceNumber, smppwire.CommandUnbindResp)
		if !matched {
			return nil
		}
		stopTimer(timer)
		return io.EOF
	case smppwire.CommandEnquireLink:
		return s.writePDU(smppwire.PDU{Header: smppwire.Header{
			CommandID:      smppwire.CommandEnquireLinkResp,
			SequenceNumber: pdu.Header.SequenceNumber,
		}})
	case smppwire.CommandEnquireLinkResp:
		if timer, matched := s.takePendingControl(pdu.Header.SequenceNumber, smppwire.CommandEnquireLinkResp); matched {
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
	if pending.chain != nil {
		// One part of a multipart submit. Settle the message only on the last
		// part's response (this pdu becomes the aggregated response — the legacy
		// last-arriving-wins semantics, KNOWN_QUIRKS). Earlier parts just wait;
		// a response after a timeout/drop already settled the chain is ignored.
		final, dead := pending.chain.arrive()
		if dead || !final {
			return
		}
	}
	if s.transactions == nil || s.responses == nil {
		s.logSubmitAudit(pending, pdu)
		s.settleDeliveryResponse(pending.delivery, pdu.Header.CommandStatus == 0)
		return
	}
	properties := pending.envelope.Properties()
	replyTo, replyEnabled := properties.ReplyTo()
	headers := properties.Headers()
	createdAt, _ := headerString(headers, "created_at")
	userID, _ := headerString(headers, "user-id")
	billID, _ := headerString(headers, "bill-id")
	lateBillAmount, _ := headerString(headers, "late-bill-amount")
	var smscMessageID []byte
	if pdu.SubmitResponse != nil {
		smscMessageID = append([]byte(nil), pdu.SubmitResponse.MessageID...)
	}
	var responseBody []byte
	if replyEnabled {
		if s.responseEncoder == nil {
			s.settleDeliveryFailure(pending.delivery)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var err error
		responseBody, err = s.responseEncoder.EncodeSubmitSMResponse(ctx, pdu.Header.CommandStatus, pdu.Header.SequenceNumber, smscMessageID)
		cancel()
		if err != nil {
			s.settleDeliveryFailure(pending.delivery)
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	committed, err := s.responses.Commit(ctx, DurableResponseInput{
		PartKey: pending.partKey, AttemptID: pending.attempt.ID,
		Status: smppStatusName(pdu.Header.CommandStatus), SMSCMessageID: string(smscMessageID),
		ReplyEnabled: replyEnabled, ReplyTo: replyTo, MessageID: properties.MessageID(),
		CreatedAt: createdAt, Body: responseBody, UserID: userID, BillID: billID,
		LateBillAmount: lateBillAmount, RetryAttempt: pending.attempt.Number,
		RetryEnvelope: &pending.envelope,
	})
	cancel()
	if err != nil {
		s.settleDeliveryFailure(pending.delivery)
		return
	}
	// Log the SMS-MT audit line once, on the fresh commit only — the durable
	// boundary is the exactly-once point, so a duplicate (replayed) commit must
	// not re-log.
	if committed {
		s.logSubmitAudit(pending, pdu)
	}
	// Fresh and duplicate commits both mean the durable boundary owns all local
	// effects. ACK only after that boundary, never directly on socket response.
	s.settleDeliveryResponse(pending.delivery, true)
}

// logSubmitAudit emits the legacy SMS-MT audit line for a correlated, final
// submit_sm_resp. It is single-part only for now — multipart content reassembly
// (SAR concatenation / UDH-header stripping) and the populated tlvs field are
// follow-ups (docs/plans/005) — so a multipart chain is skipped rather than logged
// with just its last part. A nil auditLogger disables it.
func (s *Session) logSubmitAudit(pending *pendingRequest, pdu smppwire.PDU) {
	if s.auditLogger == nil || pending.chain != nil {
		return
	}
	properties := pending.envelope.Properties()
	priority, _ := properties.Priority()
	validity := "none"
	if expiration, ok := headerString(properties.Headers(), "expiration"); ok {
		validity = expiration
	}
	fields := submitAuditFields{
		ConnectorID:        s.cfg.CID,
		QueueMsgID:         properties.MessageID(),
		Status:             pdu.Header.CommandStatus,
		Priority:           priority,
		RegisteredDelivery: pending.registeredDelivery,
		Validity:           validity,
		SourceAddr:         pending.sourceAddr,
		DestAddr:           pending.destAddr,
		ShortMessage:       pending.shortMessage,
		Privacy:            s.auditPrivacy,
		TLVs:               formatTLVsForLog(pending.optional, pending.customTLVs, s.auditPrivacy),
	}
	if pdu.Header.CommandStatus == 0 {
		if pdu.SubmitResponse != nil {
			fields.SMPPMsgID = pdu.SubmitResponse.MessageID
		}
		s.auditLogger.Info(submitAuditLineSuccess(fields))
		return
	}
	if s.retry != nil {
		decision, err := s.retry.Decide(smppStatusName(pdu.Header.CommandStatus), pending.attempt.Number)
		fields.WillRetry = err == nil && decision.Action == ErrorRetryRequeue
	}
	s.auditLogger.Info(submitAuditLineError(fields))
}

func headerString(headers map[string]amqpcompat.Field, name string) (string, bool) {
	field, ok := headers[name]
	if !ok {
		return "", false
	}
	return field.String()
}

func headerInteger(headers map[string]amqpcompat.Field, name string) (int64, bool) {
	field, ok := headers[name]
	if !ok {
		return 0, false
	}
	return field.Integer()
}

func durablePartKey(envelope amqpcompat.Envelope) (string, error) {
	properties := envelope.Properties()
	messageID := properties.MessageID()
	headers := properties.Headers()
	aggregate, aggregateOK := headerString(headers, "aggregate-message-id")
	partNumber, partNumberOK := headerInteger(headers, "part-number")
	partCount, partCountOK := headerInteger(headers, "part-count")
	if !aggregateOK && !partNumberOK && !partCountOK {
		return messageID + "/000001", nil
	}
	if !aggregateOK || aggregate == "" || !partNumberOK || !partCountOK || partNumber < 1 || partCount < 1 || partNumber > partCount {
		return "", errors.New("invalid durable multipart identity")
	}
	expectedMessageID := aggregate
	if partCount > 1 {
		expectedMessageID = fmt.Sprintf("%s/%06d", aggregate, partNumber)
	}
	if messageID != expectedMessageID {
		return "", errors.New("message-id does not match durable multipart identity")
	}
	return fmt.Sprintf("%s/%06d", aggregate, partNumber), nil
}

func (s *Session) handleTimeout(seq uint32) {
	pending := s.takePending(seq)
	if pending == nil {
		return
	}
	// A part timing out fails the whole message once (the chain guard) and drops
	// the connection; cleanupSession then skips the remaining parts.
	s.settlePendingFailure(pending)
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
		s.pendingControls = make(map[uint32]*pendingControl)
		close(s.closed)
		s.mu.Unlock()

		for _, request := range pending {
			stopTimer(request.timer)
			// A chained message's shared delivery is settled once (chain guard).
			s.settlePendingFailure(request)
		}
		for _, control := range pendingControls {
			stopTimer(control.timer)
		}
		if s.onClose != nil {
			s.onClose(err)
		}
	})
}

func (s *Session) String() string {
	return fmt.Sprintf("SMPP session %s", s.cfg.CID)
}

// ErrCustomTLVRejected marks a submit rejected by the connector's custom-TLV
// rules or by a tuple the legacy wire encoder would crash on. The delivery is
// settled without requeue, matching the legacy listener's rejectMessage.
var ErrCustomTLVRejected = errors.New("custom TLV rejected")

// prepareVendorTLVs runs the legacy submit-time TLV sequence and returns the
// encoded vendor section for the outbound PDU.
func prepareVendorTLVs(tuples []tlv.TLV, rules []tlv.ConnectorRule) ([]byte, error) {
	resolved, err := tlv.ResolveTLVTypes(tuples, rules)
	if err != nil {
		return nil, fmt.Errorf("resolve connector TLV types: %w", err)
	}
	if err := tlv.ValidateCustomTLVs(resolved, rules); err != nil {
		return nil, fmt.Errorf("validate connector TLV rules: %w", err)
	}
	section, err := tlv.WireEncodeCustomTLVs(resolved)
	if err != nil {
		return nil, fmt.Errorf("wire-encode custom TLVs: %w", err)
	}
	return section, nil
}
