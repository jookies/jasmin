package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

// partCapturingBuilder records the actual segmentation output, which is what
// decides whether the wire carries UDH headers or SAR TLVs.
type partCapturingBuilder struct {
	parts []segmentation.Part
}

func (builder *partCapturingBuilder) BuildSubmitEnvelope(
	_ context.Context, request core.SubmitEnvelopeRequest, part segmentation.Part,
) (amqpcompat.Envelope, error) {
	builder.parts = append(builder.parts, part)
	properties, err := amqpcompat.NewProperties(request.MessageID, nil)
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope(
		"submit.sm."+request.ConnectorID, properties, []byte{part.Sequence()})
}

// long_content_split and long_content_max_parts were parsed with the correct
// legacy defaults (internal/config/sections.go:239-240) and then never
// consumed: the submit path hardcoded SAR and 10 parts. An operator could set
// long_content_split=udh, see it accepted and reported back, and still emit SAR
// on the wire -- and an SMSC that only understands UDH will not concatenate
// SAR, so the handset shows N separate fragments. Legacy's HTTP front door
// defaults to udh/5 (jasmin/protocols/http/configs.py:33-34, passed at
// jasmin/protocols/http/endpoints/send.py:80-81).
func TestLongContentSplitDefaultsToTheLegacyUDH(t *testing.T) {
	builder := submitLongMessage(t, "", 0)
	if len(builder.parts) < 2 {
		t.Fatalf("expected a multipart submit, got %d part(s)", len(builder.parts))
	}
	for index, part := range builder.parts {
		if _, hasSAR := part.SAR(); hasSAR {
			t.Errorf("part %d carries SAR TLVs; the legacy default is UDH", index)
		}
		if _, _, hasUDH := part.UDH(); !hasUDH {
			t.Errorf("part %d has no UDH header; the legacy default is UDH", index)
		}
	}
}

func TestLongContentSplitHonoursAnExplicitSAR(t *testing.T) {
	builder := submitLongMessage(t, segmentation.SplitSAR, 0)
	if len(builder.parts) < 2 {
		t.Fatalf("expected a multipart submit, got %d part(s)", len(builder.parts))
	}
	for index, part := range builder.parts {
		if _, hasSAR := part.SAR(); !hasSAR {
			t.Errorf("part %d has no SAR TLVs despite long_content_split=sar", index)
		}
	}
}

// The legacy ceiling is 5 parts, not the 10 that were hardcoded.
func TestLongContentMaxPartsDefaultsToFive(t *testing.T) {
	builder := submitLongMessage(t, "", 0)
	if len(builder.parts) > 5 {
		t.Errorf("emitted %d parts; the legacy long_content_max_parts default is 5", len(builder.parts))
	}
}

func TestLongContentConfigIsValidated(t *testing.T) {
	if _, err := newLongContentService(t, "gsm", 5); err == nil {
		t.Error("an unknown long_content_split must be rejected, not silently ignored")
	}
	if _, err := newLongContentService(t, "", 999); err == nil {
		t.Error("a long_content_max_parts above the 255 the wire allows must be rejected")
	}
	if _, err := newLongContentService(t, "", 0); err != nil {
		t.Errorf("zero long_content_max_parts must take the legacy default, got %v", err)
	}
}

func newLongContentService(
	t *testing.T, split segmentation.SplitMethod, maxParts int,
) (*partCapturingBuilder, error) {
	t.Helper()
	user := billing.NewUser(7)
	if err := user.SetBalance(1000); err != nil {
		t.Fatal(err)
	}
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	builder := &partCapturingBuilder{}
	routes := routeTable(t, true)
	_, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:    emptyInterceptors(),
		InterceptorRunner:   fixedRunner{},
		RoutingTable:        &routes,
		BillingUsers:        users,
		EnvelopeBuilder:     builder,
		Publisher:           &recordingPublisher{},
		LongContentSplit:    split,
		LongContentMaxParts: maxParts,
		NewMessageID:        func() (string, error) { return "11111111-1111-4111-8111-111111111111", nil },
		NewReference:        func() (uint16, error) { return 41, nil },
		Now:                 func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	return builder, err
}

func submitLongMessage(
	t *testing.T, split segmentation.SplitMethod, maxParts int,
) *partCapturingBuilder {
	t.Helper()
	user := billing.NewUser(7)
	if err := user.SetBalance(1000); err != nil {
		t.Fatal(err)
	}
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	builder := &partCapturingBuilder{}
	routes := routeTable(t, true)
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:    emptyInterceptors(),
		InterceptorRunner:   fixedRunner{},
		RoutingTable:        &routes,
		BillingUsers:        users,
		EnvelopeBuilder:     builder,
		Publisher:           &recordingPublisher{},
		LongContentSplit:    split,
		LongContentMaxParts: maxParts,
		NewMessageID:        func() (string, error) { return "11111111-1111-4111-8111-111111111111", nil },
		NewReference:        func() (uint16, error) { return 41, nil },
		Now:                 func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:    "alice",
		Destination: "15551230000",
		Content:     strings.Repeat("a", 400),
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	return builder
}
