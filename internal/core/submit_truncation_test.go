package core_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/billing"
	"github.com/pumpitspace/synevyr/internal/core/segmentation"
)

// submitOverLongMessage submits content that cannot fit maxParts and reports
// what the service did with it. 1800 GSM-7 characters is twelve 153-byte parts,
// so a five-part maximum keeps 765 bytes and drops 1035.
func submitOverLongMessage(t *testing.T, maxParts int, reject bool) (string, error, []byte) {
	t.Helper()
	user := billing.NewUser(7)
	if err := user.SetBalance(1000); err != nil {
		t.Fatal(err)
	}
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	routes := routeTable(t, true)
	builder := &partCapturingBuilder{}
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:         emptyInterceptors(),
		InterceptorRunner:        fixedRunner{},
		RoutingTable:             &routes,
		BillingUsers:             users,
		EnvelopeBuilder:          builder,
		Publisher:                &recordingPublisher{},
		LongContentSplit:         segmentation.SplitUDH,
		LongContentMaxParts:      maxParts,
		LongContentRejectOverMax: reject,
		Logger:                   slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		NewMessageID:             func() (string, error) { return "11111111-1111-4111-8111-111111111111", nil },
		NewReference:             func() (uint16, error) { return 41, nil },
		Now:                      func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	id, submitErr := service.Submit(context.Background(), core.SubmitRequest{
		Username:    "alice",
		Destination: "15551230000",
		Content:     strings.Repeat("a", 1800),
	})
	return id, submitErr, logs.Bytes()
}

// TestOverLongContentIsReportedNotSilentlyDropped is the regression for content
// that exceeds long_content_max_parts.
//
// segmentation.Segment clamps the part count and discards every byte past the
// limit, recording the loss on Result.Truncated(). Nothing read that flag, so a
// customer received a success message-id, was billed for the parts that were
// sent, and was told nothing about the ~1000 characters that were not.
func TestOverLongContentIsReportedNotSilentlyDropped(t *testing.T) {
	t.Run("default keeps legacy truncation but says so", func(t *testing.T) {
		id, err, logs := submitOverLongMessage(t, 5, false)
		if err != nil {
			t.Fatalf("submit refused with rejection disabled: %v", err)
		}
		if id == "" {
			t.Fatal("no message id returned")
		}
		if !bytes.Contains(logs, []byte("long_content_max_parts")) {
			t.Errorf("truncation was not logged; logs = %s", logs)
		}
		// The exact byte accounting matters: an operator reading the line has to
		// be able to tell the customer how much of their message was dropped.
		if !bytes.Contains(logs, []byte("1035 of 1800")) {
			t.Errorf("dropped-byte count missing or wrong; logs = %s", logs)
		}
	})

	t.Run("operators can opt into refusing it", func(t *testing.T) {
		_, err, _ := submitOverLongMessage(t, 5, true)
		if !errors.Is(err, core.ErrContentTruncated) {
			t.Fatalf("submit error = %v, want ErrContentTruncated", err)
		}
	})

	t.Run("content that fits is untouched", func(t *testing.T) {
		_, err, logs := submitOverLongMessage(t, 20, true)
		if err != nil {
			t.Fatalf("submit refused content that fits: %v", err)
		}
		if bytes.Contains(logs, []byte("long_content_max_parts")) {
			t.Errorf("non-truncated submit logged a truncation; logs = %s", logs)
		}
	})
}
