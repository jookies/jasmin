package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/billing"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// refusingGate refuses every submit after the first, recording what it was
// asked so the ingress attribution can be checked.
type refusingGate struct {
	calls    int
	username string
	ingress  string
}

func (g *refusingGate) AllowSubmit(username, ingress string, _ time.Time) bool {
	g.calls++
	g.username, g.ingress = username, ingress
	return g.calls == 1
}

// TestSubmitRefusesOverRateWithoutCharging proves the gate is actually wired
// into the submit path, and that a refusal costs the user nothing. Legacy
// checks QoS after routing but before billing, so an over-rate submit is
// rejected without a charge and without consuming a message id — if it were
// charged, a client hitting its own ceiling would silently drain its balance.
func TestSubmitRefusesOverRateWithoutCharging(t *testing.T) {
	user := billing.NewUser(7)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	user.SetSubmitSmCountQuota(5)

	gate := &refusingGate{}
	builder := &recordingBuilder{}
	publisher := &recordingPublisher{}
	service := newSubmitServiceWithGate(t, user, routeTable(t, true), builder, publisher, gate)

	request := core.SubmitRequest{Username: "alice", Destination: "15551230000", Content: "hello"}
	if _, err := service.Submit(context.Background(), request); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	balanceAfterFirst := *user.GetState().Balance

	_, err := service.Submit(context.Background(), request)
	if !errors.Is(err, core.ErrThroughputExceeded) {
		t.Fatalf("second submit err = %v, want ErrThroughputExceeded", err)
	}
	if gate.username != "alice" {
		t.Fatalf("gate saw username %q, want alice", gate.username)
	}
	if gate.ingress != "httpapi" {
		t.Fatalf("gate saw ingress %q, want httpapi", gate.ingress)
	}
	if got := *user.GetState().Balance; got != balanceAfterFirst {
		t.Fatalf("balance moved on a refused submit: %v -> %v", balanceAfterFirst, got)
	}
	if len(publisher.bodies) != 1 {
		t.Fatalf("a refused submit was published: %d bodies", len(publisher.bodies))
	}
}

// TestSubmitAttributesSMPPsIngress pins that a bind's submits are metered
// against the smpps ceiling, not the HTTP one.
func TestSubmitAttributesSMPPsIngress(t *testing.T) {
	user := billing.NewUser(7)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	gate := &refusingGate{}
	service := newSubmitServiceWithGate(t, user, routeTable(t, true), &recordingBuilder{}, &recordingPublisher{}, gate)

	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: "hello",
		SourceConnector: "smppsapi",
	}); err != nil {
		t.Fatal(err)
	}
	if gate.ingress != "smppsapi" {
		t.Fatalf("gate saw ingress %q, want smppsapi", gate.ingress)
	}
}

func newSubmitServiceWithGate(
	t *testing.T,
	user *billing.User,
	routes routingtable.Table,
	builder core.SubmitEnvelopeBuilder,
	publisher core.AMQPPublisher,
	gate core.ThroughputGate,
) *core.SubmitService {
	t.Helper()
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:  emptyInterceptors(),
		InterceptorRunner: fixedRunner{},
		RoutingTable:      &routes,
		BillingUsers:      users,
		EnvelopeBuilder:   builder,
		Publisher:         publisher,
		Throughput:        gate,
		NewMessageID: func() (string, error) {
			return "11111111-1111-4111-8111-111111111111", nil
		},
		NewReference: func() (uint16, error) { return 41, nil },
		Now:          func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// TestSubmitPreservesESMEUserDataHeader covers the corruption an ESME saw on
// every long message it sent over a bind.
//
// A pre-segmented part opens with a binary UDH (05 00 03 ref total seq). The
// submit path treated it as text: coding 0 with no hex content meant the GSM
// 03.38 encoder ran over it and replaced each unmappable header byte with '?',
// and because nothing carried the ESME's esm_class the outbound PDU did not
// even claim to have a UDH. The receiving SMSC got a mangled header followed by
// mangled content, so the customer's multipart messages arrived as garbage.
func TestSubmitPreservesESMEUserDataHeader(t *testing.T) {
	user := billing.NewUser(7)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	builder := &recordingBuilder{}
	service := newSubmitServiceWithGate(t, user, routeTable(t, true), builder, &recordingPublisher{}, nil)

	// 8-bit concatenation header for part 1 of 2, followed by text.
	udh := []byte{0x05, 0x00, 0x03, 0x2A, 0x02, 0x01}
	body := []byte("first half")
	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:        "alice",
		Destination:     "447700900000",
		Content:         string(append(append([]byte{}, udh...), body...)),
		Coding:          0,
		SourceConnector: "smppsapi",
		ESMClass:        0x40, // UDHI
	}); err != nil {
		t.Fatal(err)
	}

	if len(builder.request.Parts) != 1 {
		t.Fatalf("parts = %d, want 1 — an already-segmented part was re-segmented, which would prepend a second UDH",
			len(builder.request.Parts))
	}
	part := builder.request.Parts[0]
	if _, _, ok := part.UDH(); !ok {
		t.Fatal("part is not flagged as carrying a UDH, so the outbound esm_class would drop its UDHI bit")
	}
	wire := part.ShortMessage()
	if !bytes.HasPrefix(wire, udh) {
		t.Fatalf("short_message = % x, want the UDH % x preserved verbatim — the GSM encoder mangled the header",
			wire, udh)
	}
	if !bytes.Contains(wire, body) {
		t.Fatalf("short_message = % x, want it to still contain %q", wire, body)
	}
}

func TestSubmitDoesNotResegmentSMPPsSARPart(t *testing.T) {
	user := billing.NewUser(7)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	builder := &recordingBuilder{}
	service := newSubmitServiceWithGate(t, user, routeTable(t, true), builder, &recordingPublisher{}, nil)
	body := bytes.Repeat([]byte{'x'}, 200)
	sarRef := uint16(0x1234)
	total, sequence := byte(3), byte(2)
	raw := &smppwire.SubmitSMBody{
		DestinationAddress: []byte("447700900000"), ShortMessage: body,
		Optional: smppwire.OptionalParameters{
			SARMessageReference: &sarRef, SARTotalSegments: &total, SARSegmentSequence: &sequence,
		},
	}
	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "447700900000", Content: string(body),
		Coding: 3, SourceConnector: "smppsapi", SMPPSubmit: raw,
	}); err != nil {
		t.Fatal(err)
	}
	if len(builder.request.Parts) != 1 {
		t.Fatalf("pre-segmented SAR PDU became %d parts", len(builder.request.Parts))
	}
	if builder.request.SMPPSubmit == nil ||
		builder.request.SMPPSubmit.Optional.SARMessageReference == nil ||
		*builder.request.SMPPSubmit.Optional.SARMessageReference != sarRef {
		t.Fatalf("SAR metadata lost: %+v", builder.request.SMPPSubmit)
	}
}

// TestSubmitStillEncodesPlainTextWithoutUDHI pins that the passthrough is gated
// on the UDHI bit: an ordinary coding-0 submit must still be GSM 03.38 encoded,
// which is what the legacy front door does.
func TestSubmitStillEncodesPlainTextWithoutUDHI(t *testing.T) {
	user := billing.NewUser(7)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	builder := &recordingBuilder{}
	service := newSubmitServiceWithGate(t, user, routeTable(t, true), builder, &recordingPublisher{}, nil)

	// '€' is a GSM 03.38 extension-table rune: the encoder emits ESC + 'e'.
	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "447700900000", Content: "€", Coding: 0,
		SourceConnector: "smppsapi",
	}); err != nil {
		t.Fatal(err)
	}
	wire := builder.request.Parts[0].ShortMessage()
	if bytes.Equal(wire, []byte("€")) {
		t.Fatal("plain text bypassed the GSM 03.38 encoder")
	}
}
