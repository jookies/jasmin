package smppc_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
)

// TestSessionPoisonRejectLogsTerminalDrop pins the regression that made the
// empty-bytes poison bug invisible: a poison decode settles reject-no-requeue
// AND leaves an ERROR line naming the message id — never a silent drop.
func TestSessionPoisonRejectLogsTerminalDrop(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	poison := fmt.Errorf("decode: %w", picklecompat.ErrSubmitSMPoison)
	session := smppc.NewSessionWithDecoder(client, smppc.Config{}, nil, nil, failingSubmitDecoder{err: poison}, nil)

	var logged bytes.Buffer
	session.SetSubmitAuditLogger(slog.New(slog.NewTextHandler(&logged, nil)), false)

	properties, _ := amqpcompat.NewProperties("poison-msgid", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.poison", properties, []byte("poison-pickle"))
	settled := make(chan bool, 1)
	delivery := injectDelivery(envelope, func(requeue bool) { settled <- requeue })

	if err := session.Submit(context.Background(), delivery); !errors.Is(err, picklecompat.ErrSubmitSMPoison) {
		t.Fatalf("Submit error = %v, want poison", err)
	}
	select {
	case requeue := <-settled:
		if requeue {
			t.Fatal("poison submit was requeued, want terminal reject")
		}
	case <-time.After(time.Second):
		t.Fatal("poison submit was not settled")
	}
	line := logged.String()
	if !strings.Contains(line, "Rejecting submit_sm message[poison-msgid] without requeue") {
		t.Fatalf("terminal reject left no trace; log output: %q", line)
	}
}
