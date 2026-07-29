package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
)

func TestSubmitServiceBillingFeatureIsIngressSpecific(t *testing.T) {
	user := billing.NewUser(7)
	if err := user.SetBalance(10); err != nil {
		t.Fatal(err)
	}
	user.SetSubmitSmCountQuota(5)
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	routes := routeTable(t, true)
	builder := &recordingBuilder{}
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable: emptyInterceptors(),
		RoutingTable:     &routes,
		BillingUsers:     users,
		EnvelopeBuilder:  builder,
		Publisher:        &recordingPublisher{},
		BillingEnabled: func(ingress string) bool {
			return ingress != "smppsapi"
		},
		NewMessageID: func() (string, error) {
			return "11111111-1111-4111-8111-111111111111", nil
		},
		NewBillID:    func() (string, error) { return "bill-1", nil },
		NewReference: func() (uint16, error) { return 41, nil },
		Now:          func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: "hello",
		SourceConnector: "smppsapi",
	}); err != nil {
		t.Fatal(err)
	}
	state := user.GetState()
	if state.Balance == nil || *state.Balance != 10 ||
		state.SubmitSmCountQuota == nil || *state.SubmitSmCountQuota != 5 {
		t.Fatalf("billing-disabled SMPPs submit changed quotas: %+v", state)
	}
	if builder.request.BillingEnabled || builder.request.Bill != (billing.Bill{}) {
		t.Fatalf("billing-disabled SMPPs envelope retained bill: enabled=%v bill=%+v",
			builder.request.BillingEnabled, builder.request.Bill)
	}
}
