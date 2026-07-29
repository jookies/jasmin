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
	calls        int
	msgID        string
	request      dlr.HTTPDLRRequest
	smppsCalls   int
	smppsMsgID   string
	smppsRequest dlr.SMPPSDLRRequest
}

func (s *recordingDLRStore) StoreHTTPDLRRequest(_ context.Context, msgID string, request dlr.HTTPDLRRequest) error {
	s.calls++
	s.msgID = msgID
	s.request = request
	return nil
}

func (s *recordingDLRStore) StoreSMPPSDLRRequest(_ context.Context, msgID string, request dlr.SMPPSDLRRequest) error {
	s.smppsCalls++
	s.smppsMsgID = msgID
	s.smppsRequest = request
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

// TestSubmitRegistersSMPPSDLRRecord covers the gap that silently cost every
// SMPP bind customer 100% of their delivery receipts. The egress plumbing was
// complete, but nothing ever wrote the sc=smppsapi dlr:<msgid> record, so the
// correlation legs had nothing to resolve and every receipt was dropped as
// DLRMapNotFound. An ESME reads that as total delivery failure and re-sends.
func TestSubmitRegistersSMPPSDLRRecord(t *testing.T) {
	store := &recordingDLRStore{}
	service, _ := newSubmitServiceWithDLR(t, store, nil)

	request := core.SubmitRequest{
		Username:        "alice",
		Destination:     "447700900000",
		From:            "ACME",
		Content:         "hi",
		DLR:             true,
		SourceConnector: "smppsapi",
		SMPPSOrigin: &core.SMPPSOrigin{
			SystemID:           "client-a",
			SourceAddrTON:      "AddrTon.ALPHANUMERIC",
			SourceAddrNPI:      "AddrNpi.UNKNOWN",
			DestinationAddrTON: "AddrTon.INTERNATIONAL",
			DestinationAddrNPI: "AddrNpi.ISDN",
			RegisteredDelivery: "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED",
		},
	}
	messageID, err := service.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if store.smppsCalls != 1 {
		t.Fatalf("smpps DLR registrations = %d, want 1", store.smppsCalls)
	}
	if store.calls != 0 {
		t.Fatalf("an SMPPs submit wrote the httpapi DLR record %d time(s)", store.calls)
	}
	if store.smppsMsgID != messageID {
		t.Fatalf("record keyed on %q, want the returned message id %q", store.smppsMsgID, messageID)
	}
	got := store.smppsRequest
	if got.SystemID != "client-a" {
		t.Fatalf("system_id = %q — a receipt would not find the bind that submitted", got.SystemID)
	}
	// The addressing must be the ESME's own, not the outbound connector's: the
	// receipt has to be addressed the way the original submit was.
	if got.SourceAddress != "ACME" || got.DestinationAddress != "447700900000" {
		t.Fatalf("addresses = %q -> %q, want ACME -> 447700900000", got.SourceAddress, got.DestinationAddress)
	}
	if got.SourceAddrTON != "AddrTon.ALPHANUMERIC" || got.DestinationAddrNPI != "AddrNpi.ISDN" {
		t.Fatalf("TON/NPI not carried through: %+v", got)
	}
	if got.ExpirySeconds != core.DefaultDLRExpirySeconds {
		t.Fatalf("expiry = %d, want the legacy default %d", got.ExpirySeconds, core.DefaultDLRExpirySeconds)
	}
	if got.SubmissionDate == "" {
		t.Fatal("sub_date is empty; the receipt renders it verbatim")
	}
}

// TestSubmitSkipsSMPPSDLRRecordWhenNoReceiptRequested pins the legacy gate: the
// record is written only when the ESME actually asked for a receipt
// (managers/clients.py:618), not on every SMPP submit.
func TestSubmitSkipsSMPPSDLRRecordWhenNoReceiptRequested(t *testing.T) {
	store := &recordingDLRStore{}
	service, _ := newSubmitServiceWithDLR(t, store, nil)

	if _, err := service.Submit(context.Background(), core.SubmitRequest{
		Username: "alice", Destination: "447700900000", From: "ACME", Content: "hi",
		SourceConnector: "smppsapi",
	}); err != nil {
		t.Fatal(err)
	}
	if store.smppsCalls != 0 {
		t.Fatalf("wrote a DLR record for a submit that requested no receipt (%d)", store.smppsCalls)
	}
}
