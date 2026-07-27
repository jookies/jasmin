package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
)

type recordingDLRStore struct {
	calls   int
	msgID   string
	request dlr.HTTPDLRRequest
}

func (s *recordingDLRStore) StoreHTTPDLRRequest(_ context.Context, msgID string, request dlr.HTTPDLRRequest) error {
	s.calls++
	s.msgID = msgID
	s.request = request
	return nil
}

func newSubmitServiceWithDLR(t *testing.T, store core.DLRRequestStore, expiry func(string) int64) (*core.SubmitService, *recordingPublisher) {
	t.Helper()
	user := billing.NewUser(7)
	if err := user.SetBalance(100); err != nil {
		t.Fatal(err)
	}
	users := billing.NewManager()
	if err := users.AddUserWithID("alice", "user-opaque", user); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	routes := routeTable(t, true)
	service, err := core.NewSubmitService(core.SubmitServiceDependencies{
		InterceptorTable:   emptyInterceptors(),
		InterceptorRunner:  fixedRunner{},
		RoutingTable:       &routes,
		BillingUsers:       users,
		EnvelopeBuilder:    &recordingBuilder{},
		Publisher:          publisher,
		DLRRequestStore:    store,
		ConnectorDLRExpiry: expiry,
		NewMessageID:       func() (string, error) { return "11111111-1111-4111-8111-111111111111", nil },
		NewReference:       func() (uint16, error) { return 41, nil },
		Now:                func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, publisher
}

func TestSubmitWritesDLRRequestForLevel2HTTP(t *testing.T) {
	store := &recordingDLRStore{}
	service, _ := newSubmitServiceWithDLR(t, store, func(string) int64 { return 3600 })

	_, err := service.Submit(context.Background(), core.SubmitRequest{
		Username:    "alice",
		Destination: "15551230000",
		Content:     "hi",
		DLR:         true,
		DLRUrl:      "http://sink.example/dlr",
		DLRLevel:    2,
		DLRMethod:   "GET",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want 1", store.calls)
	}
	if store.msgID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("stored msgid=%q", store.msgID)
	}
	want := dlr.HTTPDLRRequest{URL: "http://sink.example/dlr", Level: 2, Method: "GET", Connector: "connector-a", ExpirySeconds: 3600}
	if store.request != want {
		t.Fatalf("stored request=%+v want %+v", store.request, want)
	}
}

func TestSubmitDLRExpiryFallsBackToDefault(t *testing.T) {
	store := &recordingDLRStore{}
	// Nil expiry provider → the legacy 86400 default.
	service, _ := newSubmitServiceWithDLR(t, store, nil)

	_, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: "hi",
		DLR: true, DLRUrl: "http://sink/dlr", DLRLevel: 3, DLRMethod: "POST",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.request.ExpirySeconds != core.DefaultDLRExpirySeconds {
		t.Fatalf("expiry=%d want %d", store.request.ExpirySeconds, core.DefaultDLRExpirySeconds)
	}
}

func TestSubmitSkipsDLRStoreWhenNotRequested(t *testing.T) {
	store := &recordingDLRStore{}
	service, _ := newSubmitServiceWithDLR(t, store, func(string) int64 { return 3600 })

	// No dlr-url → no record (a bare submit).
	_, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "15551230000", Content: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls=%d want 0 (DLR not requested)", store.calls)
	}
}
