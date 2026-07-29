package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/routingtable"
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
