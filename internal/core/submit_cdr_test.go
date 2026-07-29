package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/cdr"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type commercialAdmissionRecorder struct {
	legacyCalls     int
	commercialCalls int
	envelopes       []amqpcompat.Envelope
	metadata        cdr.SubmitMetadata
}

func (recorder *commercialAdmissionRecorder) AdmitSubmit(_ context.Context, envelopes []amqpcompat.Envelope) error {
	recorder.legacyCalls++
	recorder.envelopes = append([]amqpcompat.Envelope(nil), envelopes...)
	return nil
}

func (recorder *commercialAdmissionRecorder) AdmitSubmitWithCDR(
	_ context.Context,
	envelopes []amqpcompat.Envelope,
	metadata cdr.SubmitMetadata,
) error {
	recorder.commercialCalls++
	recorder.envelopes = append([]amqpcompat.Envelope(nil), envelopes...)
	recorder.metadata = metadata
	return nil
}

func TestSubmitServiceUsesTypedCommercialAdmissionWithoutAMQPHeaders(t *testing.T) {
	group := billing.NewGroup(4)
	user := billing.NewUser(7)
	user.SetGroup(group)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	if err := user.SetEarlyDecrementPercent(50); err != nil {
		t.Fatal(err)
	}
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	builder := &recordingBuilder{}
	transaction := &commercialAdmissionRecorder{}
	routes := routeTable(t, true)
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable: emptyInterceptors(),
		RoutingTable:     &routes,
		BillingUsers:     users,
		EnvelopeBuilder:  builder,
		Transaction:      transaction,
		GroupIdentity: func(username string) (string, bool) {
			return "customers", username == "alice"
		},
		NewMessageID: func() (string, error) {
			return "11111111-1111-4111-8111-111111111111", nil
		},
		NewReference: func() (uint16, error) { return 41, nil },
		Now: func() time.Time {
			return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	if transaction.commercialCalls != 1 || transaction.legacyCalls != 0 {
		t.Fatalf("commercial calls=%d legacy calls=%d", transaction.commercialCalls, transaction.legacyCalls)
	}
	want := cdr.SubmitMetadata{
		GroupID: "customers", RouteID: "mt:0", Ingress: "httpapi",
		Rate: 1, Currency: "XXX", EarlyAmount: 0.5, LateAmount: 0.5,
	}
	if transaction.metadata != want {
		t.Fatalf("metadata=%+v want=%+v", transaction.metadata, want)
	}
	if len(transaction.envelopes) != 1 {
		t.Fatalf("envelopes=%d", len(transaction.envelopes))
	}
	for name := range transaction.envelopes[0].Properties().Headers() {
		if len(name) >= 4 && name[:4] == "cdr-" {
			t.Fatalf("CDR metadata leaked into compatibility header %q", name)
		}
	}
}
