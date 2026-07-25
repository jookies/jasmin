package smppsserver

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type fakeSubmitter struct {
	request core.SubmitRequest
	id      string
	err     error
}

func (f *fakeSubmitter) Submit(_ context.Context, request core.SubmitRequest) (string, error) {
	f.request = request
	return f.id, f.err
}

func writePDU(t *testing.T, conn net.Conn, pdu smppwire.PDU) {
	t.Helper()
	frame, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
}

func readPDU(t *testing.T, conn net.Conn) smppwire.PDU {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	pdu, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatalf("read PDU: %v", err)
	}
	return pdu
}

func TestValidateConfig(t *testing.T) {
	valid := Config{BindAddr: "127.0.0.1:0", Users: []UserConfig{{SystemID: "u", Password: "p"}}}
	if err := ValidateConfig(valid); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]Config{
		"empty addr":   {Users: []UserConfig{{SystemID: "u", Password: "p"}}},
		"negative enq": {BindAddr: "127.0.0.1:0", EnquireLinkTimeoutSeconds: -1},
		"bad user":     {BindAddr: "127.0.0.1:0", Users: []UserConfig{{SystemID: ""}}},
	} {
		if err := ValidateConfig(config); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

// End to end over a real socket: the composed service binds an ESME and ingests
// its submit_sm into the fake submitter, marking the smppsapi source connector.
func TestServiceBindAndSubmitEndToEnd(t *testing.T) {
	submitter := &fakeSubmitter{id: "msg-1"}
	service, err := NewService(Config{
		BindAddr: "127.0.0.1:0",
		Users:    []UserConfig{{SystemID: "alice", Password: "secret"}},
	}, submitter)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = service.Close()
		<-done
	})

	conn, err := net.Dial("tcp", service.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: smpps.CommandBindTransceiver, SequenceNumber: 1},
		Bind:   &smppwire.BindBody{SystemID: []byte("alice"), Password: []byte("secret"), SystemType: []byte(""), InterfaceVersion: 0x34},
	})
	bindResp := readPDU(t, conn)
	if bindResp.Header.CommandStatus != smpps.StatusROK {
		t.Fatalf("bind status = %#x", bindResp.Header.CommandStatus)
	}

	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: smpps.CommandSubmitSM, SequenceNumber: 2},
		SM:     &smppwire.SMBody{DestinationAddress: []byte("2222"), ShortMessage: []byte("hi"), DataCoding: 0},
	})
	resp := readPDU(t, conn)
	if resp.Header.CommandID != smppwire.CommandSubmitSMResp || resp.Header.CommandStatus != smpps.StatusROK {
		t.Fatalf("submit resp = %#x/%#x", resp.Header.CommandID, resp.Header.CommandStatus)
	}
	if resp.SubmitResponse == nil || string(resp.SubmitResponse.MessageID) != "msg-1" {
		t.Fatalf("submit response = %+v", resp.SubmitResponse)
	}
	if submitter.request.Username != "alice" || submitter.request.SourceConnector != "smppsapi" {
		t.Fatalf("submit request = %+v", submitter.request)
	}
	if submitter.request.Destination != "2222" || submitter.request.Content != "hi" {
		t.Fatalf("submit request fields = %+v", submitter.request)
	}
}

func TestServiceWrongPasswordRejects(t *testing.T) {
	service, err := NewService(Config{
		BindAddr: "127.0.0.1:0",
		Users:    []UserConfig{{SystemID: "alice", Password: "secret"}},
	}, &fakeSubmitter{id: "x"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = service.Run(ctx) }()
	t.Cleanup(func() { cancel(); _ = service.Close() })

	conn, err := net.Dial("tcp", service.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: smpps.CommandBindTransceiver, SequenceNumber: 1},
		Bind:   &smppwire.BindBody{SystemID: []byte("alice"), Password: []byte("WRONG"), SystemType: []byte(""), InterfaceVersion: 0x34},
	})
	if readPDU(t, conn).Header.CommandStatus != smpps.StatusInvalidPassword {
		t.Fatal("wrong password must reject ESME_RINVPASWD")
	}
}

func TestNewServiceFailsOnBadAddress(t *testing.T) {
	if _, err := NewService(Config{
		BindAddr: "999.999.999.999:2775",
		Users:    []UserConfig{{SystemID: "u", Password: "p"}},
	}, &fakeSubmitter{}); err == nil {
		t.Fatal("a bad bind address must fail construction")
	}
}
