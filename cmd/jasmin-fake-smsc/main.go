// jasmin-fake-smsc is a development/test SMSC simulator for the gateway
// compose stack (docs/plans/007). It accepts any bind, answers enquire_link,
// and acknowledges every submit_sm with ESME_ROK and a generated message_id —
// the same contract as the integration test's in-process fake SMSC.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func main() {
	if err := run(); err != nil {
		log.Printf("jasmin-fake-smsc: %v", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddress := flag.String("listen", ":2775", "SMPP listen address")
	flag.Parse()

	lifetime, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	go func() {
		<-lifetime.Done()
		listener.Close()
	}()
	log.Printf("fake SMSC listening on %s", listener.Addr())

	var messageCounter atomic.Uint64
	for {
		conn, err := listener.Accept()
		if err != nil {
			if lifetime.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		go serve(conn, &messageCounter)
	}
}

func serve(conn net.Conn, messageCounter *atomic.Uint64) {
	defer conn.Close()
	remote := conn.RemoteAddr()
	for {
		request, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("%s: read: %v", remote, err)
			}
			return
		}
		response, done := respond(request, remote, messageCounter)
		if response != nil {
			wire, err := smppwire.Encode(*response)
			if err != nil {
				log.Printf("%s: encode response to %#x: %v", remote, request.Header.CommandID, err)
				return
			}
			if _, err := conn.Write(wire); err != nil {
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
func respond(request smppwire.PDU, remote net.Addr, messageCounter *atomic.Uint64) (*smppwire.PDU, bool) {
	header := func(commandID uint32) smppwire.Header {
		return smppwire.Header{CommandID: commandID, SequenceNumber: request.Header.SequenceNumber}
	}
	switch request.Header.CommandID {
	case smppwire.CommandBindTransceiver, smppwire.CommandBindTransmitter, smppwire.CommandBindReceiver:
		systemID := ""
		if request.Bind != nil {
			systemID = string(request.Bind.SystemID)
		}
		log.Printf("%s: bind %#x system_id=%q accepted", remote, request.Header.CommandID, systemID)
		return &smppwire.PDU{
			Header:       header(request.Header.CommandID | 0x80000000),
			BindResponse: &smppwire.BindResponseBody{SystemID: []byte("fake-smsc")},
		}, false
	case smppwire.CommandEnquireLink:
		return &smppwire.PDU{Header: header(smppwire.CommandEnquireLinkResp)}, false
	case smppwire.CommandSubmitSM:
		messageID := "fake-" + strconv.FormatUint(messageCounter.Add(1), 10)
		log.Printf("%s: submit_sm seq=%d -> ESME_ROK message_id=%s", remote, request.Header.SequenceNumber, messageID)
		return &smppwire.PDU{
			Header:         header(smppwire.CommandSubmitSMResp),
			SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte(messageID)},
		}, false
	case smppwire.CommandUnbind:
		log.Printf("%s: unbind", remote)
		return &smppwire.PDU{Header: header(smppwire.CommandUnbindResp)}, true
	case smppwire.CommandDeliverSMResp, smppwire.CommandEnquireLinkResp:
		return nil, false
	default:
		log.Printf("%s: ignoring unsupported command %#x", remote, request.Header.CommandID)
		return nil, false
	}
}
