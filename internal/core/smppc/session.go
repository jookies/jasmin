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

	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

var ErrSessionClosed = errors.New("SMPP session closed")
var ErrAMQPConsumerLost = errors.New("AMQP consumer generation lost")
var ErrInvalidOutboundSubmitSM = errors.New("invalid outbound submit_sm")
var ErrSubmitChainTerminated = errors.New("multipart submit terminated before all parts were sent")

const maxSequenceNumber uint32 = 0x7fffffff
const MaxSubmitSMShortMessageLength = 254

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
	esmClass           byte
	optional           smppwire.OptionalParameters
	customTLVs         []tlv.TLV
	// sentAt is when this part was handed to the write path, the start of the
	// round trip the submit-latency histogram measures.
	sentAt time.Time
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
	conn net.Conn
	cfg  Config
	// counters is the per-connector stats registry, shared with the owning
	// connector. Nil disables counting (focused tests construct sessions
	// directly).
	counters             *stats.SMPPcRegistry
	nextSeq              uint32
	pending              map[uint32]*pendingRequest
	pendingControls      map[uint32]*pendingControl
	mu                   sync.Mutex
	writeMu              sync.Mutex
	cleanup              sync.Once
	closed               chan struct{}
	window               chan struct{}
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

	// deliverPublisher/deliverEncoder wire deliver_sm ingestion (MO + receipt
	// publications). Nil mirrors the legacy RouterPB-not-set drop branch.
	deliverPublisher DeliverPublisher
	deliverEncoder   DeliverEncoder
	// multipartStore accumulates inbound long-message parts for reassembly.
	// Nil reproduces the legacy redis-less drop.
	multipartStore MultipartStore
	// moInterceptor optionally rewrites or drops an inbound MO before publish.
	// Nil disables MO-direction interception.
	moInterceptor MOInterceptor
}

// SetSubmitAuditLogger enables the legacy SMS-MT audit line for correlated
// submit_sm_resp events. A nil logger (the default) disables it. Must be called
// before the session handles responses; the connector sets it at session creation.
func (s *Session) SetSubmitAuditLogger(logger *slog.Logger, privacy bool) {
	s.auditLogger = logger
	s.auditPrivacy = privacy
}

// SetStats attaches the per-connector counter registry. The owning connector
// calls this immediately after construction.
func (s *Session) SetStats(registry *stats.SMPPcRegistry) { s.counters = registry }

// incStat records one event against this session's connector.
func (s *Session) incStat(name string) {
	if s.counters != nil {
		s.counters.Inc(s.cfg.CID, name)
	}
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
	windowSize := cfg.WindowSize
	if windowSize <= 0 {
		windowSize = cfg.PrefetchCount
	}
	if windowSize <= 0 {
		windowSize = 1
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
		window:          make(chan struct{}, windowSize),
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
		s.releaseWindow()
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

// logTerminalReject makes a no-requeue drop visible on the sm-listener logger.
// Not a legacy byte-parity line — the legacy reject lines carry Python error
// strings and stay deferred (O-007) — but silence here is what hid the
// empty-bytes poison drop, so every terminal rejection must leave a trace.
func (s *Session) logTerminalReject(delivery *amqpcompat.Delivery, err error) {
	logger := s.auditLogger
	if logger == nil {
		return
	}
	messageID := ""
	if delivery != nil {
		messageID = delivery.Envelope().Properties().MessageID()
	}
	logger.Error(fmt.Sprintf("Rejecting submit_sm message[%s] without requeue: %v", messageID, err))
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
	// elink_interval, not pdu_red_to: legacy keeps the enquire_link cadence and
	// the PDU read timer as separate settings, and this ticker used to run off
	// the latter. Both default to 30 so the observable cadence is unchanged.
	if s.cfg.EnquireLinkInterval > 0 {
		enquireTicker = time.NewTicker(seconds(s.cfg.EnquireLinkInterval))
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
	if errors.Is(terminalErr, io.EOF) {
		// Let the peer finish the write that supplied a complete unbind PDU
		// before closing transports, such as net.Pipe, with synchronous writes.
		time.Sleep(time.Millisecond)
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
	if err := writeFrameWithTimeout(s.conn, wire, s.pduOperationTimeout()); err != nil {
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
		err = writeFrameWithTimeout(s.conn, wire, s.pduOperationTimeout())
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
	decodeCtx, cancelDecode := context.WithTimeout(ctx, s.pduOperationTimeout())
	parts, err := s.decodeSubmitParts(decodeCtx, d.Envelope().Body())
	cancelDecode()
	if err != nil {
		if errors.Is(err, picklecompat.ErrSubmitSMPoison) {
			s.logTerminalReject(d, err)
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
		if validateErr := validateOutboundSubmitSM(body); validateErr != nil {
			s.logTerminalReject(d, validateErr)
			s.settleDeliveryReject(d, false)
			return validateErr
		}
		if len(parts[i].CustomTLVs) > 0 || len(s.cfg.CustomTLVs) > 0 {
			vendorSection, tlvErr := prepareVendorTLVs(parts[i].CustomTLVs, s.cfg.ConnectorTLVRules())
			if tlvErr != nil {
				rejectErr := fmt.Errorf("%w: %w", ErrCustomTLVRejected, tlvErr)
				s.logTerminalReject(d, rejectErr)
				s.settleDeliveryReject(d, false)
				return rejectErr
			}
			body.VendorTLVs = vendorSection
		}
		bodies[i] = body
	}
	envelope := d.Envelope()
	var attempt submittransaction.SendAttempt
	partKey, err := durablePartKey(envelope)
	if err != nil {
		s.logTerminalReject(d, err)
		s.settleDeliveryReject(d, false)
		return err
	}
	if s.transactions != nil {
		var committed bool
		var beginErr error
		beginCtx, cancelBegin := context.WithTimeout(ctx, 10*time.Second)
		attempt, committed, beginErr = s.transactions.BeginAttempt(beginCtx, partKey)
		cancelBegin()
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

	// Preflight every frame body before any part hits the wire. Sequence numbers
	// are assigned later, when each PDU has acquired a window slot.
	for i := range bodies {
		if _, encodeErr := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: 1},
			SM:     &bodies[i],
		}); encodeErr != nil {
			s.settleDeliveryFailure(d)
			return encodeErr
		}
	}

	var firstPending *pendingRequest
	sent := 0
	for i := range bodies {
		if err := s.acquireWindow(ctx); err != nil {
			s.failPartiallySentSubmit(firstPending, d, sent)
			return err
		}
		if chain != nil && chain.done() {
			s.releaseWindow()
			return ErrSubmitChainTerminated
		}

		s.mu.Lock()
		if s.consumerLost {
			s.mu.Unlock()
			s.releaseWindow()
			s.failPartiallySentSubmit(firstPending, d, sent)
			return ErrAMQPConsumerLost
		}
		select {
		case <-s.closed:
			s.mu.Unlock()
			s.releaseWindow()
			s.failPartiallySentSubmit(firstPending, d, sent)
			return ErrSessionClosed
		default:
		}
		seq, seqErr := s.nextSequenceLocked()
		var wire []byte
		if seqErr == nil {
			wire, seqErr = smppwire.Encode(smppwire.PDU{
				Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: seq},
				SM:     &bodies[i],
			})
		}
		if seqErr != nil {
			s.mu.Unlock()
			s.releaseWindow()
			s.failPartiallySentSubmit(firstPending, d, sent)
			return seqErr
		}
		pending := &pendingRequest{
			delivery: d, attempt: attempt, partKey: partKey, envelope: envelope, chain: chain,
			sourceAddr: bodies[i].SourceAddress, destAddr: bodies[i].DestinationAddress,
			shortMessage: bodies[i].ShortMessage, registeredDelivery: bodies[i].RegisteredDelivery,
			esmClass: bodies[i].ESMClass, optional: bodies[i].Optional, customTLVs: parts[i].CustomTLVs,
			// Stamped before the write, not after: the pending is already in the
			// map, so a fast SMSC can answer and the reader can take it while the
			// writer is still on the next line. Setting it afterwards would race
			// into a zero round trip on exactly the fastest responses.
			sentAt: time.Now(),
		}
		s.pending[seq] = pending
		if firstPending == nil {
			firstPending = pending
		}
		if chain != nil {
			if i == 0 {
				chain.audit.first = pending
			}
			chain.audit.last = pending
			chain.audit.partContents = append(chain.audit.partContents, bodies[i].ShortMessage)
		}
		s.mu.Unlock()

		s.writeMu.Lock()
		writeErr := writeFrameWithTimeout(s.conn, wire, s.pduOperationTimeout())
		s.writeMu.Unlock()
		if writeErr != nil {
			if owned := s.takePending(seq); owned != nil {
				stopTimer(owned.timer)
				s.settlePendingFailure(owned)
			}
			_ = s.conn.Close()
			if s.consumerGenerationLost() {
				return ErrAMQPConsumerLost
			}
			return writeErr
		}
		sent++
		// One submit_sm reached the wire. Counted here rather than at enqueue so
		// the number reflects PDUs actually sent, and each part of a multipart
		// message counts, which is what the SMSC sees and bills.
		s.incStat("submit_sm_request_count")
		// Same event on the modern registry. The two counters deliberately
		// agree: synevyr_submit_total{outcome="attempt"} must equal
		// smppc_submit_sm_request_count for the same connector, so a
		// disagreement is a wiring bug rather than a judgement call about what
		// counts as an attempt.
		stats.DefaultPrometheus().RecordSubmit(s.cfg.CID, stats.SubmitAttempt, "pending", 0)
		if sent == 1 && s.transactions != nil {
			// The first successful write makes the durable attempt externally
			// ambiguous; do not wait until a whole multipart chain is written.
			markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.transactions.MarkSent(markCtx, attempt.ID)
			cancel()
		}
		s.mu.Lock()
		if s.pending[seq] == pending && s.cfg.ResTimeout > 0 {
			responseSequence := seq
			pending.timer = time.AfterFunc(seconds(s.cfg.ResTimeout), func() { s.handleTimeout(responseSequence) })
		}
		s.mu.Unlock()
		s.resetInactivityTimer()
	}
	return nil
}

func (s *Session) acquireWindow(ctx context.Context) error {
	select {
	case s.window <- struct{}{}:
		return nil
	case <-s.closed:
		return ErrSessionClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) releaseWindow() {
	select {
	case <-s.window:
	default:
	}
}

func (s *Session) failPartiallySentSubmit(firstPending *pendingRequest, delivery *amqpcompat.Delivery, sent int) {
	if sent == 0 || firstPending == nil {
		s.settleDeliveryFailure(delivery)
		return
	}
	if claimPendingFailure(firstPending) {
		s.completePendingFailure(firstPending)
	}
	_ = s.conn.Close()
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

func validateOutboundSubmitSM(body smppwire.SubmitSMBody) error {
	for _, field := range []struct {
		name string
		data []byte
		max  int
	}{
		{"service_type", body.ServiceType, 5},
		{"source_addr", body.SourceAddress, 20},
		{"destination_addr", body.DestinationAddress, 20},
		{"schedule_delivery_time", body.ScheduleDeliveryTime, 16},
		{"validity_period", body.ValidityPeriod, 16},
	} {
		if len(field.data) > field.max {
			return fmt.Errorf("%w: %s length %d exceeds %d", ErrInvalidOutboundSubmitSM, field.name, len(field.data), field.max)
		}
		if bytesContainNUL(field.data) {
			return fmt.Errorf("%w: %s contains NUL", ErrInvalidOutboundSubmitSM, field.name)
		}
	}
	if len(body.ShortMessage) > MaxSubmitSMShortMessageLength {
		return fmt.Errorf("%w: short_message length %d exceeds %d",
			ErrInvalidOutboundSubmitSM, len(body.ShortMessage), MaxSubmitSMShortMessageLength)
	}
	if body.Optional.MessagePayload != nil && len(body.ShortMessage) != 0 {
		return fmt.Errorf("%w: message_payload requires sm_length zero", ErrInvalidOutboundSubmitSM)
	}
	if !validAddrTON(int(body.SourceAddressTON)) || !validAddrTON(int(body.DestinationAddressTON)) {
		return fmt.Errorf("%w: invalid address TON", ErrInvalidOutboundSubmitSM)
	}
	if !validAddrNPI(int(body.SourceAddressNPI)) || !validAddrNPI(int(body.DestinationAddressNPI)) {
		return fmt.Errorf("%w: invalid address NPI", ErrInvalidOutboundSubmitSM)
	}
	if body.PriorityFlag > 3 {
		return fmt.Errorf("%w: priority_flag %d exceeds 3", ErrInvalidOutboundSubmitSM, body.PriorityFlag)
	}
	if body.ReplaceIfPresentFlag > 1 {
		return fmt.Errorf("%w: replace_if_present_flag %d exceeds 1", ErrInvalidOutboundSubmitSM, body.ReplaceIfPresentFlag)
	}
	if body.RegisteredDelivery&0xe0 != 0 || body.RegisteredDelivery&0x03 == 0x03 {
		return fmt.Errorf("%w: registered_delivery %#x uses reserved bits", ErrInvalidOutboundSubmitSM, body.RegisteredDelivery)
	}
	return nil
}

func bytesContainNUL(value []byte) bool {
	for _, item := range value {
		if item == 0 {
			return true
		}
	}
	return false
}

// settlePendingFailure marks the attempt unknown and settles the delivery as
// failed. For a chained part only the first caller (per the chain guard) settles
// the shared delivery; later parts are no-ops. It returns true to the caller that
// actually settled (single-part, or the chain's finalizing part), so a per-message
// line is logged exactly once.
func (s *Session) settlePendingFailure(pending *pendingRequest) bool {
	if !claimPendingFailure(pending) {
		return false
	}
	s.completePendingFailure(pending)
	return true
}

func claimPendingFailure(pending *pendingRequest) bool {
	if pending.chain != nil && !pending.chain.finalize() {
		return false
	}
	return true
}

func (s *Session) completePendingFailure(pending *pendingRequest) {
	_ = s.markPendingUnknown(pending)
	s.settleDeliveryFailure(pending.delivery)
}

func (s *Session) takePending(seq uint32) *pendingRequest {
	s.mu.Lock()
	pending := s.pending[seq]
	if pending != nil {
		delete(s.pending, seq)
	}
	s.mu.Unlock()
	if pending != nil {
		s.releaseWindow()
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
	case smppwire.CommandDeliverSM, smppwire.CommandDataSM:
		// The legacy deliver_sm_event catches data_sm as well — same
		// receipt-vs-MO classification and publication; the response command
		// derives from the request id (data_sm -> data_sm_resp).
		if pdu.Header.CommandID == smppwire.CommandDataSM {
			s.incStat("data_sm_count")
		} else {
			s.incStat("deliver_sm_count")
		}
		return s.handleDeliver(pdu)
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
		s.incStat("elink_count")
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

// submitStatusTimeout labels a submit that expired without a response. It is
// deliberately not an ESME_* name: no SMPP status was returned, and borrowing
// one would make an unanswered submit indistinguishable in a metric from an SMSC
// that actually said something.
const submitStatusTimeout = "RESPONSE_TIMEOUT"

// submitOutcome maps a command status to the modern registry's outcome label.
// Throttling is a failure here even though the legacy registry gives it its own
// counter: it is a submit that did not succeed, and the SMPP status label keeps
// it separable.
func submitOutcome(commandStatus uint32) string {
	if commandStatus == 0 {
		return stats.SubmitSuccess
	}
	return stats.SubmitFailure
}

// roundTripSince measures a submit's round trip, returning zero when the send
// time was never recorded so the histogram is not fed a duration measured from
// the zero time.
func roundTripSince(sentAt time.Time) time.Duration {
	if sentAt.IsZero() {
		return 0
	}
	elapsed := time.Since(sentAt)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func (s *Session) handleResponse(pdu smppwire.PDU) {
	// Classify the outcome before settlement, so the counters reflect every
	// submit_sm_resp the SMSC returned regardless of what the durability layer
	// then decides to do with it.
	switch {
	case pdu.Header.CommandStatus == 0:
		s.incStat("submit_sm_count")
	case pdu.Header.CommandStatus == statusThrottled:
		s.incStat("throttling_error_count")
	default:
		s.incStat("other_submit_error_count")
	}
	pending := s.takePending(pdu.Header.SequenceNumber)
	if pending == nil {
		// A response for a sequence this session no longer holds: a duplicate,
		// or one that arrived after its own timeout already failed the part.
		// Counted on the legacy registry above (it counts PDUs), but not here:
		// synevyr_submit_total's outcomes must sum to at most its attempts, or
		// the failure-rate alert's denominator is a number of responses rather
		// than of submits and the ratio it computes is not a rate.
		return
	}
	stopTimer(pending.timer)
	stats.DefaultPrometheus().RecordSubmit(
		s.cfg.CID, submitOutcome(pdu.Header.CommandStatus),
		smppStatusName(pdu.Header.CommandStatus), roundTripSince(pending.sentAt))
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
	if s.auditLogger == nil {
		return
	}
	// One line per queue message. For a multipart submit the from/to/dlr/tlvs come
	// from the last part and the content is every part reassembled (SAR-concat or
	// UDH-header-stripped), mirroring the legacy walking the nextPdu chain; a
	// single-part submit reads the one pending directly.
	source := pending
	content := pending.shortMessage
	if pending.chain != nil {
		if pending.chain.audit.last == nil {
			return
		}
		source = pending.chain.audit.last
		content = reassembleMultipart(&pending.chain.audit)
	}
	properties := source.envelope.Properties()
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
		RegisteredDelivery: source.registeredDelivery,
		Validity:           validity,
		SourceAddr:         source.sourceAddr,
		DestAddr:           source.destAddr,
		ShortMessage:       content,
		Privacy:            s.auditPrivacy,
		TLVs:               formatTLVsForLog(source.optional, source.customTLVs, s.auditPrivacy),
	}
	if pdu.Header.CommandStatus == 0 {
		if pdu.SubmitResponse != nil {
			fields.SMPPMsgID = pdu.SubmitResponse.MessageID
		}
		s.auditLogger.Info(submitAuditLineSuccess(fields))
		return
	}
	if s.retry != nil {
		decision, err := s.retry.Decide(smppStatusName(pdu.Header.CommandStatus), source.attempt.Number)
		fields.WillRetry = err == nil && decision.Action == ErrorRetryRequeue
	}
	s.auditLogger.Info(submitAuditLineError(fields))
}

// reassembleMultipart concatenates a multipart submit's parts the way the legacy
// listener does: SAR (sar_msg_ref_num on the first part) concatenates the full
// short_message of every part; a concatenation UDH (esm_class UDHI bit set and
// the first part starting with the 6-byte 05 00 03 concat header) strips that
// 6-byte header from each part. Anything else falls back to a plain concat.
func reassembleMultipart(audit *chainAudit) []byte {
	first := audit.first
	sar := first != nil && first.optional.SARMessageReference != nil
	udh := !sar && first != nil && first.esmClass&0x40 != 0 &&
		len(first.shortMessage) >= 3 &&
		first.shortMessage[0] == 0x05 && first.shortMessage[1] == 0x00 && first.shortMessage[2] == 0x03
	if !sar && !udh {
		// No detectable split (legacy splitMethod None, e.g. a 16-bit-ref UDH):
		// the legacy logs only the last part's short_message, not a concat.
		if audit.last != nil {
			return audit.last.shortMessage
		}
		return nil
	}
	var content []byte
	for _, part := range audit.partContents {
		if udh && len(part) >= 6 {
			content = append(content, part[6:]...)
		} else {
			content = append(content, part...)
		}
	}
	return content
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
	// the connection; cleanupSession then skips the remaining parts. Log the
	// timeout line only for the part that actually settles, so a multipart submit
	// logs once.
	if !claimPendingFailure(pending) {
		return
	}
	// A submit that never got an answer is a failure, not an absence. Left
	// uncounted, an SMSC that stops responding altogether would show a falling
	// submit rate and a zero failure rate — the shape of a quiet night rather
	// than of an outage. The status label is the sentinel below rather than an
	// SMPP command status, because the SMSC returned none.
	stats.DefaultPrometheus().RecordSubmit(
		s.cfg.CID, stats.SubmitFailure, submitStatusTimeout, roundTripSince(pending.sentAt))
	s.logSubmitTimeout(pending)
	settle := func() {
		s.completePendingFailure(pending)
		_ = s.conn.Close()
	}
	if delay := seconds(s.cfg.RequeueDelay); delay > 0 {
		time.AfterFunc(delay, settle)
		return
	}
	settle()
}

// logSubmitTimeout emits the legacy SM listener's submit_sm timeout line at ERROR
// (SMPPRequestTimoutError → message requeued). The queue msgid and connector id
// are the only fields, so it is byte-exact with the legacy line.
func (s *Session) logSubmitTimeout(pending *pendingRequest) {
	if s.auditLogger == nil {
		return
	}
	s.auditLogger.Error(fmt.Sprintf(
		"SubmitSmPDU[%s] request timed out through [cid:%s], message requeued.",
		pending.envelope.Properties().MessageID(), s.cfg.CID))
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

func writeFrameWithTimeout(conn net.Conn, frame []byte, timeout time.Duration) error {
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if err := writeFrame(conn, frame); err != nil {
		_ = conn.SetWriteDeadline(time.Time{})
		return err
	}
	return conn.SetWriteDeadline(time.Time{})
}

func (s *Session) pduOperationTimeout() time.Duration {
	if timeout := seconds(s.cfg.PDUTimeout); timeout > 0 {
		return timeout
	}
	return 10 * time.Second
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
			s.releaseWindow()
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
