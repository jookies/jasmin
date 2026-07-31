// Command synevyr-partner-sim emulates a partner ESME for local development: it
// binds to the gateway, submits messages the way a partner does, and prints the
// delivery receipts that come back with the latency of each one.
//
// It exists because the termination connector's whole job is invisible from the
// gateway side — a message is accepted, a verdict is decided, a receipt is
// emitted 5-7 s later — and the only place that story is observable end to end is
// at the partner's socket. scripts/interop/esme_probe.py answers a different
// question (does an independent SMPP library agree with us, once); this one keeps
// traffic flowing so you can watch behaviour.
//
// It is a development tool. It is not a load generator and it makes no attempt to
// be one: --rate is paced by a ticker, not by a scheduler that keeps up under
// pressure.
//
//	go run ./cmd/synevyr-partner-sim --addr 127.0.0.1:2775 \
//	    --system-id partner-a --password secret \
//	    --count 3 --text "code 63125" --destination 380671234567
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "partner-sim: %v\n", err)
		os.Exit(1)
	}
}

type options struct {
	addr        string
	systemID    string
	password    string
	systemType  string
	source      string
	destination string
	text        string
	count       int
	rate        time.Duration
	wait        time.Duration
	dataCoding  uint
	noReceipt   bool
	allowRemote bool
}

// assertDevTarget refuses to generate traffic against anything that is not
// obviously a development target.
//
// This tool submits real messages that are really charged, really routed and
// really delivered to whatever the connector points at. Nothing about a copied
// command line stops it being aimed at production, so the address is checked
// instead of trusted; --allow-remote is the deliberate override, and it has to be
// typed.
func assertDevTarget(addr string, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse --addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("--addr %q is a hostname, not a development address; pass --allow-remote if you really mean it", addr)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return nil
	}
	return fmt.Errorf("--addr %q is a public address; this is a development tool that submits billable traffic. Pass --allow-remote if you really mean it", addr)
}

func run() error {
	var opts options
	flag.StringVar(&opts.addr, "addr", "127.0.0.1:2775", "gateway SMPP address")
	flag.StringVar(&opts.systemID, "system-id", "", "bind system_id (required)")
	flag.StringVar(&opts.password, "password", "", "bind password (required)")
	flag.StringVar(&opts.systemType, "system-type", "", "bind system_type")
	flag.StringVar(&opts.source, "source", "NETFLIX", "source address (alphanumeric is normal for a brand)")
	flag.StringVar(&opts.destination, "destination", "380671234567", "destination number")
	flag.StringVar(&opts.text, "text", "code 63125", "message text")
	flag.IntVar(&opts.count, "count", 1, "how many messages to submit")
	flag.DurationVar(&opts.rate, "interval", time.Second, "delay between submits")
	flag.DurationVar(&opts.wait, "wait", 20*time.Second, "how long to wait for receipts after the last submit")
	flag.UintVar(&opts.dataCoding, "data-coding", 0, "data_coding value")
	flag.BoolVar(&opts.noReceipt, "no-receipt", false, "do not request a delivery receipt")
	flag.BoolVar(&opts.allowRemote, "allow-remote", false, "permit a non-private target address (this tool submits billable traffic)")
	flag.Parse()

	if opts.systemID == "" || opts.password == "" {
		return errors.New("--system-id and --password are required")
	}
	if err := assertDevTarget(opts.addr, opts.allowRemote); err != nil {
		return err
	}
	if opts.count < 1 {
		return errors.New("--count must be at least 1")
	}

	conn, err := net.DialTimeout("tcp", opts.addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", opts.addr, err)
	}
	defer conn.Close()

	session := &session{conn: conn, reader: bufio.NewReader(conn), submitted: map[uint32]*submitted{}, byMessageID: map[string]*submitted{}}

	if err := session.bind(opts); err != nil {
		return err
	}
	fmt.Printf("bound as %s to %s\n\n", opts.systemID, opts.addr)

	// Receipts arrive asynchronously and out of band with the submits, so the
	// reader runs for the whole session rather than being polled per message.
	done := make(chan struct{})
	go func() {
		defer close(done)
		session.readLoop()
	}()

	for i := 1; i <= opts.count; i++ {
		if err := session.submit(opts, i); err != nil {
			return err
		}
		if i < opts.count {
			time.Sleep(opts.rate)
		}
	}

	if opts.noReceipt {
		fmt.Println("\nnot waiting for receipts (--no-receipt)")
	} else {
		fmt.Printf("\nwaiting up to %s for receipts...\n", opts.wait)
		session.waitForReceipts(opts.wait)
	}

	_ = session.unbind()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	<-done

	session.report()
	return nil
}

// submitted tracks one message from submit to receipt so the report can show
// what the gateway did with it and how long the receipt took.
type submitted struct {
	index      int
	sentAt     time.Time
	messageID  string
	respStatus uint32
	receipt    string
	receiptAt  time.Time
}

type session struct {
	conn     net.Conn
	reader   *bufio.Reader
	sequence uint32

	mu          sync.Mutex
	submitted   map[uint32]*submitted // by request sequence, until submit_sm_resp
	byMessageID map[string]*submitted // by SMSC message id, until the receipt
	// all holds every message in submit order. The report reads this rather than
	// byMessageID: a submit refused with a non-zero command_status carries no
	// message id, so it is in neither map, and summarising from those maps drops
	// exactly the messages you most want to see.
	all     []*submitted
	writeMu sync.Mutex
}

func (s *session) nextSequence() uint32 {
	s.sequence++
	return s.sequence
}

func (s *session) write(pdu smppwire.PDU) error {
	wire, err := smppwire.Encode(pdu)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.conn.Write(wire); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (s *session) bind(opts options) error {
	bind := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandBindTransceiver, SequenceNumber: s.nextSequence()},
		Bind: &smppwire.BindBody{
			SystemID:         []byte(opts.systemID),
			Password:         []byte(opts.password),
			SystemType:       []byte(opts.systemType),
			InterfaceVersion: 0x34,
		},
	}
	if err := s.write(bind); err != nil {
		return err
	}
	if err := s.conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	response, err := smppwire.Read(s.reader, smppwire.DefaultMaxSize)
	if err != nil {
		return fmt.Errorf("read bind response: %w", err)
	}
	if response.Header.CommandStatus != 0 {
		return fmt.Errorf("bind refused: command_status 0x%08X", response.Header.CommandStatus)
	}
	// No deadline for the rest of the session: receipts arrive minutes later.
	return s.conn.SetReadDeadline(time.Time{})
}

func (s *session) submit(opts options, index int) error {
	sequence := s.nextSequence()
	registered := byte(1) // request a receipt for final state
	if opts.noReceipt {
		registered = 0
	}
	pdu := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: sequence},
		SM: &smppwire.SMBody{
			SourceAddress:      []byte(opts.source),
			DestinationAddress: []byte(opts.destination),
			ShortMessage:       []byte(opts.text),
			DataCoding:         byte(opts.dataCoding),
			RegisteredDelivery: registered,
		},
	}
	record := &submitted{index: index, sentAt: time.Now()}
	s.mu.Lock()
	s.submitted[sequence] = record
	s.all = append(s.all, record)
	s.mu.Unlock()

	if err := s.write(pdu); err != nil {
		return err
	}
	fmt.Printf("[%02d] submit_sm      seq=%d %s -> %s %q\n", index, sequence, opts.source, opts.destination, opts.text)
	return nil
}

func (s *session) unbind() error {
	return s.write(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandUnbind, SequenceNumber: s.nextSequence()},
	})
}

func (s *session) readLoop() {
	for {
		pdu, err := smppwire.Read(s.reader, smppwire.DefaultMaxSize)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !isTimeout(err) {
				fmt.Fprintf(os.Stderr, "read: %v\n", err)
			}
			return
		}
		switch pdu.Header.CommandID {
		case smppwire.CommandSubmitSMResp:
			s.onSubmitResp(pdu)
		case smppwire.CommandDeliverSM:
			s.onDeliverSM(pdu)
		case smppwire.CommandEnquireLink:
			_ = s.write(smppwire.PDU{Header: smppwire.Header{
				CommandID:      smppwire.CommandEnquireLinkResp,
				SequenceNumber: pdu.Header.SequenceNumber,
			}})
		case smppwire.CommandUnbindResp:
			return
		}
	}
}

func (s *session) onSubmitResp(pdu smppwire.PDU) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.submitted[pdu.Header.SequenceNumber]
	if !ok {
		return
	}
	delete(s.submitted, pdu.Header.SequenceNumber)
	record.respStatus = pdu.Header.CommandStatus
	if pdu.SubmitResponse != nil {
		record.messageID = string(pdu.SubmitResponse.MessageID)
		s.byMessageID[normalizeID(record.messageID)] = record
	}
	fmt.Printf("[%02d] submit_sm_resp status=0x%08X id=%s (%s)\n",
		record.index, record.respStatus, record.messageID, time.Since(record.sentAt).Round(time.Millisecond))
}

func (s *session) onDeliverSM(pdu smppwire.PDU) {
	// Always answer: a receipt the ESME never acknowledges is one the gateway
	// will keep retrying, which makes local behaviour look broken when it is not.
	defer func() {
		_ = s.write(smppwire.PDU{Header: smppwire.Header{
			CommandID:      smppwire.CommandDeliverSMResp,
			SequenceNumber: pdu.Header.SequenceNumber,
		}})
	}()
	if pdu.SM == nil {
		return
	}
	text := string(pdu.SM.ShortMessage)
	id := receiptField(text, "id:")

	s.mu.Lock()
	record := s.byMessageID[normalizeID(id)]
	if record != nil {
		record.receipt = text
		record.receiptAt = time.Now()
	}
	s.mu.Unlock()

	stat := receiptField(text, "stat:")
	dlvrd := receiptField(text, "dlvrd:")
	errField := receiptField(text, "err:")
	switch {
	case record != nil:
		fmt.Printf("[%02d] receipt        stat=%s dlvrd=%s err=%s after %s\n",
			record.index, stat, dlvrd, errField, record.receiptAt.Sub(record.sentAt).Round(time.Millisecond))
	default:
		// A receipt for an id we did not submit: worth showing rather than
		// dropping, because it usually means id correlation is broken.
		fmt.Printf("[??] receipt        stat=%s dlvrd=%s err=%s id=%s (unmatched)\n", stat, dlvrd, errField, id)
	}
	// A receipt that claims delivery counts while saying nothing was delivered is
	// the exact defect this project found in three separate emulators, so the
	// simulator calls it out instead of printing it like any other line.
	if stat != "" && stat != "DELIVRD" && dlvrd != "" && dlvrd != "000" {
		fmt.Printf("     ^ CONTRADICTORY: stat=%s with dlvrd=%s\n", stat, dlvrd)
	}
}

func (s *session) waitForReceipts(limit time.Duration) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		outstanding := 0
		for _, record := range s.byMessageID {
			if record.receipt == "" {
				outstanding++
			}
		}
		pending := len(s.submitted)
		s.mu.Unlock()
		if outstanding == 0 && pending == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *session) report() {
	s.mu.Lock()
	defer s.mu.Unlock()

	accepted, refused, receipted, delivered, rejected := 0, 0, 0, 0, 0
	var slowest time.Duration
	for _, record := range s.all {
		if record.respStatus != 0 {
			refused++
			fmt.Printf("[%02d] refused at submit: command_status 0x%08X\n", record.index, record.respStatus)
			continue
		}
		accepted++
		if record.receipt == "" {
			continue
		}
		receipted++
		if latency := record.receiptAt.Sub(record.sentAt); latency > slowest {
			slowest = latency
		}
		switch receiptField(record.receipt, "stat:") {
		case "DELIVRD":
			delivered++
		default:
			rejected++
		}
	}
	fmt.Printf("\nsubmitted=%d accepted=%d refused=%d receipted=%d delivrd=%d other-stat=%d slowest-receipt=%s\n",
		len(s.all), accepted, refused, receipted, delivered, rejected, slowest.Round(time.Millisecond))
	if missing := accepted - receipted; missing > 0 {
		fmt.Printf("%d message(s) never received a receipt\n", missing)
	}
}

// receiptField pulls one space-delimited field out of a receipt's text body. The
// text field is last and may itself contain spaces, so it is not extractable this
// way — which is fine, the gateway does not put content there.
func receiptField(text, key string) string {
	index := strings.Index(text, key)
	if index < 0 {
		return ""
	}
	rest := text[index+len(key):]
	if end := strings.IndexByte(rest, ' '); end >= 0 {
		return rest[:end]
	}
	return rest
}

// normalizeID matches the legacy correlation rule: upper-case, leading zeros
// stripped. A receipt whose id does not survive this is one the gateway would not
// have correlated either.
func normalizeID(id string) string {
	return strings.TrimLeft(strings.ToUpper(strings.TrimSpace(id)), "0")
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
