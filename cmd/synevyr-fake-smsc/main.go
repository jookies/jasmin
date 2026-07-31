// synevyr-fake-smsc is a development/test SMSC simulator for the gateway
// compose stack (docs/plans/007/008). It accepts any bind, answers
// enquire_link, acknowledges every submit_sm with ESME_ROK and a generated
// message_id — the integration test's fake-SMSC contract — and exposes an
// HTTP trigger to inject mobile-originated deliver_sm and delivery receipts
// toward the bound client (the Macro-2 inbound drills).
//
// Two behaviours are configurable, both defaulting to the contract above so an
// existing compose file or test keeps working unchanged:
//
//   - -dlr-auto sends a receipt after every accepted submit_sm that asked for
//     one, instead of requiring a curl to /inject/dlr. Without it a receipt is
//     something a human remembers to trigger, so the whole DLR correlation path
//     is exercised only when someone thinks of it.
//   - -submit-status answers submit_sm with a command status other than
//     ESME_ROK, which is the only way to drive the gateway's retry and failure
//     accounting from this emulator.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

func main() {
	if err := run(); err != nil {
		log.Printf("synevyr-fake-smsc: %v", err)
		os.Exit(1)
	}
}

// session is one accepted SMPP connection; SMSC-originated PDUs share the
// write path with request responses under writeMu and draw their sequence
// numbers from sequence.
type session struct {
	conn     net.Conn
	writeMu  sync.Mutex
	sequence atomic.Uint32
	bound    atomic.Bool
}

func (s *session) writePDU(pdu smppwire.PDU) error {
	wire, err := smppwire.Encode(pdu)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.conn.Write(wire)
	return err
}

// registry tracks live sessions so the HTTP injector can pick a bound one.
type registry struct {
	mu       sync.Mutex
	sessions map[*session]struct{}
}

func (r *registry) add(s *session)    { r.mu.Lock(); r.sessions[s] = struct{}{}; r.mu.Unlock() }
func (r *registry) remove(s *session) { r.mu.Lock(); delete(r.sessions, s); r.mu.Unlock() }

func (r *registry) firstBound() *session {
	r.mu.Lock()
	defer r.mu.Unlock()
	for s := range r.sessions {
		if s.bound.Load() {
			return s
		}
	}
	return nil
}

// options is the emulator's configurable behaviour. Its zero value is the
// historical behaviour: accept everything with ESME_ROK, send no receipt unless
// asked over HTTP.
type options struct {
	// submitStatus is the command_status returned for submit_sm. Zero is
	// ESME_ROK.
	submitStatus uint32
	// autoDLR sends a receipt after each accepted submit_sm.
	autoDLR bool
	// dlrStat is the delivery state an automatic receipt reports. The counters
	// are derived from it, never configured beside it.
	dlrStat string
	// dlrDelay and dlrJitter hold an automatic receipt back so it resembles a
	// carrier delivering to a handset, matching the Python emulator's defaults.
	dlrDelay  time.Duration
	dlrJitter time.Duration
}

func run() error {
	listenAddress := flag.String("listen", ":2775", "SMPP listen address")
	httpAddress := flag.String("http", ":8080", "HTTP inject-trigger listen address")
	submitStatus := flag.String("submit-status", "0",
		"command_status returned for submit_sm, decimal or 0x-prefixed hex (0 = ESME_ROK)")
	autoDLR := flag.Bool("dlr-auto", false,
		"send a delivery receipt automatically after each accepted submit_sm that requested one")
	dlrStat := flag.String("dlr-stat", "DELIVRD",
		"delivery state for automatic receipts; dlvrd and err are derived from it")
	dlrDelay := flag.Duration("dlr-delay", 0, "delay before an automatic receipt")
	dlrJitter := flag.Duration("dlr-jitter", 0, "extra random delay added to -dlr-delay")
	flag.Parse()

	status, err := parseCommandStatus(*submitStatus)
	if err != nil {
		return err
	}
	if *dlrDelay < 0 || *dlrJitter < 0 {
		return fmt.Errorf("-dlr-delay and -dlr-jitter must not be negative")
	}
	opts := options{
		submitStatus: status,
		autoDLR:      *autoDLR,
		dlrStat:      *dlrStat,
		dlrDelay:     *dlrDelay,
		dlrJitter:    *dlrJitter,
	}
	if opts.autoDLR {
		log.Printf("automatic receipts enabled: stat=%s delay=%s jitter=%s",
			opts.dlrStat, opts.dlrDelay, opts.dlrJitter)
	}
	if opts.submitStatus != 0 {
		log.Printf("submit_sm will be answered with command_status %#08x", opts.submitStatus)
	}

	lifetime, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, listenErr := net.Listen("tcp", *listenAddress)
	if listenErr != nil {
		return fmt.Errorf("listen: %w", listenErr)
	}
	go func() {
		<-lifetime.Done()
		listener.Close()
	}()
	log.Printf("fake SMSC listening on %s", listener.Addr())

	sessions := &registry{sessions: make(map[*session]struct{})}
	var messageCounter atomic.Uint64

	injector := &http.Server{Addr: *httpAddress, Handler: injectHandler(sessions), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("inject trigger listening on %s", *httpAddress)
		if err := injector.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("inject trigger: %v", err)
		}
	}()
	go func() {
		<-lifetime.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = injector.Shutdown(shutdownContext)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if lifetime.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		go serve(conn, sessions, &messageCounter, opts)
	}
}

// injectHandler pushes SMSC-originated deliver_sm PDUs down the first bound
// session: /inject/mo (a mobile-originated message) and /inject/dlr (a
// text-form delivery receipt).
func injectHandler(sessions *registry) http.Handler {
	mux := http.NewServeMux()
	send := func(w http.ResponseWriter, body *smppwire.SMBody) {
		target := sessions.firstBound()
		if target == nil {
			http.Error(w, "no bound session", http.StatusServiceUnavailable)
			return
		}
		sequence := target.sequence.Add(1)
		pdu := smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: sequence},
			SM:     body,
		}
		if err := target.writePDU(pdu); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		log.Printf("injected deliver_sm seq=%d to %s", sequence, target.conn.RemoteAddr())
		fmt.Fprintf(w, "deliver_sm seq=%d sent\n", sequence)
	}
	mux.HandleFunc("/inject/mo", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		body := &smppwire.SMBody{
			SourceAddress:      []byte(query.Get("from")),
			DestinationAddress: []byte(query.Get("to")),
			ShortMessage:       []byte(query.Get("text")),
		}
		send(w, body)
	})
	// /inject/mo-long sends a 2-part UDH-concatenated MO ("<text> [part 1of2]"
	// / "<text> [part 2of2]") to exercise long-message reassembly. The gateway
	// should publish exactly one reassembled MO.
	mux.HandleFunc("/inject/mo-long", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		// Both segments must reach the SAME bound session for reassembly, so the
		// target is chosen once (not per part).
		target := sessions.firstBound()
		if target == nil {
			http.Error(w, "no bound session", http.StatusServiceUnavailable)
			return
		}
		from, to := []byte(query.Get("from")), []byte(query.Get("to"))
		ref := byte(0x2a)
		for i, segment := range []string{query.Get("part1"), query.Get("part2")} {
			udh := []byte{0x05, 0x00, 0x03, ref, 0x02, byte(i + 1)}
			sequence := target.sequence.Add(1)
			pdu := smppwire.PDU{
				Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: sequence},
				SM: &smppwire.SMBody{
					SourceAddress: from, DestinationAddress: to, ESMClass: 0x40,
					ShortMessage: append(udh, []byte(segment)...),
				},
			}
			if err := target.writePDU(pdu); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}
		fmt.Fprintf(w, "2-part long MO sent to %s\n", target.conn.RemoteAddr())
	})
	mux.HandleFunc("/inject/dlr", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		stat := query.Get("stat")
		if stat == "" {
			stat = "DELIVRD"
		}
		// The dates stay fixed, as they always were — this endpoint is driven by
		// tests that pin the receipt bytes. Only dlvrd and err change, and only
		// for a stat that is not DELIVRD: a DELIVRD receipt from here is
		// byte-identical to what it was before, while stat:UNDELIV no longer
		// claims a message was delivered.
		receipt := buildReceiptText(query.Get("id"), stat, "2107261200", "2107261201")
		body := &smppwire.SMBody{
			SourceAddress:      []byte(query.Get("to")),
			DestinationAddress: []byte(query.Get("from")),
			ESMClass:           0x04, // MC delivery receipt
			ShortMessage:       []byte(receipt),
		}
		send(w, body)
	})
	return mux
}

func serve(conn net.Conn, sessions *registry, messageCounter *atomic.Uint64, opts options) {
	current := &session{conn: conn}
	sessions.add(current)
	defer func() {
		sessions.remove(current)
		conn.Close()
	}()
	remote := conn.RemoteAddr()
	for {
		request, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("%s: read: %v", remote, err)
			}
			return
		}
		response, done := respond(request, current, remote, messageCounter, opts)
		if response != nil {
			if err := current.writePDU(*response); err != nil {
				log.Printf("%s: write: %v", remote, err)
				return
			}
		}
		if done {
			return
		}
	}
}

// respond maps one request PDU to its response (nil = stay silent) and
// reports whether the session should close afterwards.
func respond(
	request smppwire.PDU,
	current *session,
	remote net.Addr,
	messageCounter *atomic.Uint64,
	opts options,
) (*smppwire.PDU, bool) {
	header := func(commandID uint32) smppwire.Header {
		return smppwire.Header{CommandID: commandID, SequenceNumber: request.Header.SequenceNumber}
	}
	switch request.Header.CommandID {
	case smppwire.CommandBindTransceiver, smppwire.CommandBindTransmitter, smppwire.CommandBindReceiver:
		systemID := ""
		if request.Bind != nil {
			systemID = string(request.Bind.SystemID)
		}
		current.bound.Store(true)
		log.Printf("%s: bind %#x system_id=%q accepted", remote, request.Header.CommandID, systemID)
		return &smppwire.PDU{
			Header:       header(request.Header.CommandID | 0x80000000),
			BindResponse: &smppwire.BindResponseBody{SystemID: []byte("fake-smsc")},
		}, false
	case smppwire.CommandEnquireLink:
		return &smppwire.PDU{Header: header(smppwire.CommandEnquireLinkResp)}, false
	case smppwire.CommandSubmitSM:
		messageID := "fake-" + strconv.FormatUint(messageCounter.Add(1), 10)
		response := &smppwire.PDU{
			Header:         header(smppwire.CommandSubmitSMResp),
			SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte(messageID)},
		}
		response.Header.CommandStatus = opts.submitStatus
		if opts.submitStatus != 0 {
			// A non-zero status means no message was accepted, so there is no id
			// to report and no receipt to promise. Returning one anyway is how an
			// emulator teaches a client to correlate against an id the SMSC never
			// issued.
			//
			// The body is dropped entirely rather than emptied: SMPP v3.4 §4.4.2
			// says an error response carries no body at all, and the encoder
			// enforces it — an empty SubmitResponseBody is still a body and fails
			// to encode.
			response.SubmitResponse = nil
			log.Printf("%s: submit_sm seq=%d -> command_status %#08x",
				remote, request.Header.SequenceNumber, opts.submitStatus)
			return response, false
		}
		log.Printf("%s: submit_sm seq=%d -> ESME_ROK message_id=%s", remote, request.Header.SequenceNumber, messageID)
		if opts.autoDLR {
			scheduleAutoReceipt(current, request.SM, messageID, opts)
		}
		return response, false
	case smppwire.CommandUnbind:
		log.Printf("%s: unbind", remote)
		current.bound.Store(false)
		return &smppwire.PDU{Header: header(smppwire.CommandUnbindResp)}, true
	case smppwire.CommandDeliverSMResp:
		log.Printf("%s: deliver_sm_resp seq=%d status=%#x", remote, request.Header.SequenceNumber, request.Header.CommandStatus)
		return nil, false
	case smppwire.CommandEnquireLinkResp:
		return nil, false
	default:
		log.Printf("%s: ignoring unsupported command %#x", remote, request.Header.CommandID)
		return nil, false
	}
}
