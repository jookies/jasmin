package smpps

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// Everything else in this package tests our server against our own client
// helpers, which proves the two agree with each other. This drives it with
// `smpp.twisted` instead -- the independent library the frozen Jasmin itself
// runs on -- so a shared misreading of SMPP 3.4 cannot pass on both ends.
//
// Requires PYTHON_PATH to name an interpreter with the smpp package installed
// (the repo's .venv-oracle). Skipped otherwise, because a missing interpreter is
// an environment gap and not a product defect.

const interopProbe = "../../../scripts/interop/esme_probe.py"

type probeResult struct {
	Bound           bool            `json:"bound"`
	BindError       *string         `json:"bind_error"`
	SubmitSent      bool            `json:"submit_sent"`
	SubmitStatus    *string         `json:"submit_status"`
	SubmitMessageID *string         `json:"submit_message_id"`
	Delivered       []probeDelivery `json:"delivered"`
	Unbound         bool            `json:"unbound"`
	Errors          []string        `json:"errors"`
}

type probeDelivery struct {
	Sequence        int    `json:"sequence"`
	Source          string `json:"source"`
	Destination     string `json:"destination"`
	ShortMessageHex string `json:"short_message_hex"`
	ESMClass        string `json:"esm_class"`
}

func runProbe(t *testing.T, addr string, args ...string) probeResult {
	t.Helper()
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH not set; skipping third-party SMPP interop")
	}
	probe, err := filepath.Abs(interopProbe)
	if err != nil {
		t.Fatalf("resolve probe: %v", err)
	}
	if _, statErr := os.Stat(probe); statErr != nil {
		t.Fatalf("probe missing: %v", statErr)
	}
	host, port, err := splitHostPort(addr)
	if err != nil {
		t.Fatalf("addr %q: %v", addr, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	full := append([]string{probe, "--host", host, "--port", port}, args...)
	command := exec.CommandContext(ctx, python, full...)
	output, runErr := command.Output()
	if runErr != nil {
		if len(output) == 0 {
			t.Skipf("third-party SMPP client unusable (%v); skipping interop", runErr)
		}
		t.Fatalf("probe failed: %v\noutput: %s", runErr, output)
	}
	// The library logs to stderr, so stdout should be the JSON line alone;
	// take the last non-empty line defensively.
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var result probeResult
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
		t.Fatalf("probe output not JSON: %v\nraw: %s", err, output)
	}
	return result
}

func splitHostPort(addr string) (string, string, error) {
	index := strings.LastIndex(addr, ":")
	if index < 0 {
		return "", "", os.ErrInvalid
	}
	host := addr[:index]
	if host == "" || host == "[::]" {
		host = "127.0.0.1"
	}
	if _, err := strconv.Atoi(addr[index+1:]); err != nil {
		return "", "", err
	}
	return host, addr[index+1:], nil
}

// A real ESME must be able to bind, submit, and unbind cleanly.
func TestThirdPartyESMEBindsSubmitsAndUnbinds(t *testing.T) {
	handler := &scriptedSubmitHandler{messageID: "interop-1", status: StatusROK}
	server, addr := startServerWithHandler(t, ServerConfig{
		EnquireLinkTimeout: 2 * time.Second,
		InactivityTimeout:  30 * time.Second,
	}, handler)
	defer func() { _ = server.Close() }()

	result := runProbe(t, addr, "--system-id", "u", "--password", "p",
		"--submit-text", "interop", "--timeout", "15")

	if result.BindError != nil {
		t.Fatalf("third-party client could not bind: %s", *result.BindError)
	}
	if !result.Bound {
		t.Fatal("third-party client did not reach a bound state")
	}
	if !result.SubmitSent {
		t.Fatalf("submit never completed; errors=%v", result.Errors)
	}
	if result.SubmitStatus == nil || !strings.Contains(*result.SubmitStatus, "ESME_ROK") {
		t.Errorf("submit_sm_resp status = %v, want ESME_ROK", result.SubmitStatus)
	}
	if result.SubmitMessageID == nil || *result.SubmitMessageID != "interop-1" {
		t.Errorf("submit_sm_resp message_id = %v, want interop-1", result.SubmitMessageID)
	}
	if !result.Unbound {
		t.Errorf("unbind did not complete cleanly; errors=%v", result.Errors)
	}
	if handler.gotSystem != "u" || handler.gotSM == nil {
		t.Errorf("server did not ingest the submit: system=%q sm=%v", handler.gotSystem, handler.gotSM)
	} else if string(handler.gotSM.ShortMessage) != "interop" {
		t.Errorf("server ingested short_message %q, want interop", handler.gotSM.ShortMessage)
	}
}

// A real ESME must receive a pushed deliver_sm and its ack must not kill the
// bind -- the defect that made SMPPs delivery unusable. Independent confirmation
// that the fix is protocol-correct and not merely self-consistent.
func TestThirdPartyESMEReceivesDeliverAndKeepsTheBind(t *testing.T) {
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{
		EnquireLinkTimeout: 2 * time.Second,
		InactivityTimeout:  30 * time.Second,
	})
	defer func() { _ = server.Close() }()

	delivered := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		deliver := smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM},
			SM: &smppwire.SMBody{
				SourceAddress:      []byte("4444"),
				DestinationAddress: []byte("5555"),
				ShortMessage:       []byte("mo-interop"),
			},
		}
		for time.Now().Before(deadline) {
			if err := server.Deliver(context.Background(), "u", deliver); err == nil {
				delivered <- nil
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		delivered <- context.DeadlineExceeded
	}()

	result := runProbe(t, addr, "--system-id", "u", "--password", "p",
		"--expect-deliver", "--timeout", "15")

	if err := <-delivered; err != nil {
		t.Fatalf("server never delivered to the bound third-party client: %v", err)
	}
	if result.BindError != nil {
		t.Fatalf("bind failed: %s", *result.BindError)
	}
	if len(result.Delivered) == 0 {
		t.Fatalf("third-party client received no deliver_sm; errors=%v", result.Errors)
	}
	got := result.Delivered[0]
	if got.Sequence == 0 {
		t.Error("deliver_sm arrived with sequence_number 0; SMPP 3.4 requires 1..0x7FFFFFFF")
	}
	if got.Source != "4444" || got.Destination != "5555" {
		t.Errorf("deliver_sm addressing = %s -> %s, want 4444 -> 5555", got.Source, got.Destination)
	}
	if want := "6d6f2d696e7465726f70"; got.ShortMessageHex != want {
		t.Errorf("deliver_sm short_message = %s, want %s", got.ShortMessageHex, want)
	}
	// The probe acks every deliver_sm, then unbinds. A clean unbind proves the
	// ack did not tear the session down.
	if !result.Unbound {
		t.Errorf("session did not survive the deliver_sm ack; errors=%v", result.Errors)
	}
}

// Wrong credentials must be refused, not accepted or hung.
func TestThirdPartyESMEBindIsRejectedWithBadCredentials(t *testing.T) {
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	defer func() { _ = server.Close() }()

	result := runProbe(t, addr, "--system-id", "u", "--password", "wrong", "--timeout", "10")
	if result.Bound {
		t.Fatal("server accepted a bind with the wrong password")
	}
	if result.BindError == nil {
		t.Fatalf("bind neither succeeded nor reported an error; errors=%v", result.Errors)
	}
}

// startServerWithHandler mirrors startServer but attaches a submit handler, the
// way the handler-bearing tests in server_test.go construct their servers.
func startServerWithHandler(t *testing.T, cfg ServerConfig, handler SubmitHandler) (*Server, string) {
	t.Helper()
	server, err := NewServer(mapResolver{"u": testUser("p")}, cfg, WithSubmitHandler(handler))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return server, listener.Addr().String()
}
