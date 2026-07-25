package smppssubmit

import (
	"context"
	"errors"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/mtcredential"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type mapCredentials map[string]*mtcredential.Credential

func (m mapCredentials) ResolveCredential(systemID string) (*mtcredential.Credential, bool) {
	c, ok := m[systemID]
	return c, ok
}

type recordingSubmitter struct {
	request core.SubmitRequest
	id      string
	err     error
	calls   int
}

func (r *recordingSubmitter) Submit(_ context.Context, request core.SubmitRequest) (string, error) {
	r.calls++
	r.request = request
	return r.id, r.err
}

func allowingCredential() *mtcredential.Credential {
	return mtcredential.New(true) // every authorization granted, filters default-open
}

func newTestHandler(t *testing.T, credential *mtcredential.Credential, submitter core.Submitter) *Handler {
	t.Helper()
	handler, err := NewHandler(mapCredentials{"alice": credential}, submitter)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHandleSubmitIngestsWithSMPPSSource(t *testing.T) {
	submitter := &recordingSubmitter{id: "msg-7"}
	handler := newTestHandler(t, allowingCredential(), submitter)

	sm := &smppwire.SMBody{
		SourceAddress: []byte("111"), DestinationAddress: []byte("222"),
		ShortMessage: []byte("hi"), DataCoding: 8, PriorityFlag: 1, RegisteredDelivery: 0x01,
	}
	id, status := handler.HandleSubmit(context.Background(), "alice", sm)
	if status != statusROK || id != "msg-7" {
		t.Fatalf("status/id = %#x/%q", status, id)
	}
	got := submitter.request
	if got.Username != "alice" || got.SourceConnector != "smppsapi" {
		t.Fatalf("request identity = %+v", got)
	}
	if got.Destination != "222" || got.From != "111" || got.Content != "hi" ||
		got.Coding != 8 || got.Priority != 1 || !got.DLR {
		t.Fatalf("request fields = %+v", got)
	}
}

func TestHandleSubmitEmptyDestinationRejects(t *testing.T) {
	submitter := &recordingSubmitter{id: "x"}
	handler := newTestHandler(t, allowingCredential(), submitter)
	_, status := handler.HandleSubmit(context.Background(), "alice", &smppwire.SMBody{ShortMessage: []byte("hi")})
	if status != statusInvalidDestAddr {
		t.Fatalf("status = %#x, want ESME_RINVDSTADR", status)
	}
	if submitter.calls != 0 {
		t.Fatal("submitter must not be called for an empty destination")
	}
}

func TestHandleSubmitUnauthorizedRejects(t *testing.T) {
	credential := allowingCredential()
	credential.SetAuthorization(mtcredential.AuthSMPPSSend, false)
	submitter := &recordingSubmitter{id: "x"}
	handler := newTestHandler(t, credential, submitter)

	_, status := handler.HandleSubmit(context.Background(), "alice",
		&smppwire.SMBody{DestinationAddress: []byte("222"), ShortMessage: []byte("hi")})
	if status != statusSubmitFailed {
		t.Fatalf("status = %#x, want ESME_RSUBMITFAIL", status)
	}
	if submitter.calls != 0 {
		t.Fatal("submitter must not be called when the credential rejects")
	}
}

func TestHandleSubmitSourceAddressNeedsAuthorization(t *testing.T) {
	credential := allowingCredential()
	credential.SetAuthorization(mtcredential.AuthSetSourceAddress, false)
	handler := newTestHandler(t, credential, &recordingSubmitter{id: "x"})

	// A non-empty source_addr without set_source_address is rejected.
	_, status := handler.HandleSubmit(context.Background(), "alice",
		&smppwire.SMBody{SourceAddress: []byte("111"), DestinationAddress: []byte("222"), ShortMessage: []byte("hi")})
	if status != statusSubmitFailed {
		t.Fatalf("status = %#x, want ESME_RSUBMITFAIL", status)
	}
}

func TestHandleSubmitUnknownSystemIDIsSystemError(t *testing.T) {
	handler := newTestHandler(t, allowingCredential(), &recordingSubmitter{id: "x"})
	_, status := handler.HandleSubmit(context.Background(), "ghost",
		&smppwire.SMBody{DestinationAddress: []byte("222"), ShortMessage: []byte("hi")})
	if status != statusSystemError {
		t.Fatalf("status = %#x, want ESME_RSYSERR", status)
	}
}

func TestHandleSubmitErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want uint32
	}{
		{"no route", core.ErrNoRouteMatched, statusSubmitFailed},
		{"no connector", core.ErrNoLiveConnector, statusSubmitFailed},
		{"quota", core.ErrQuotaExceeded, statusThrottled},
		{"filter", core.ErrFilterRejected, statusSubmitFailed},
		{"invalid", core.ErrInvalidParameter, statusSubmitFailed},
		{"auth", core.ErrAuthentication, statusSystemError},
		{"unknown", errors.New("boom"), statusSystemError},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := newTestHandler(t, allowingCredential(), &recordingSubmitter{err: testCase.err})
			_, status := handler.HandleSubmit(context.Background(), "alice",
				&smppwire.SMBody{DestinationAddress: []byte("222"), ShortMessage: []byte("hi")})
			if status != testCase.want {
				t.Fatalf("status = %#x, want %#x", status, testCase.want)
			}
		})
	}
}

func TestNewHandlerValidatesDependencies(t *testing.T) {
	if _, err := NewHandler(nil, &recordingSubmitter{}); err == nil {
		t.Error("nil credential resolver must error")
	}
	if _, err := NewHandler(mapCredentials{}, nil); err == nil {
		t.Error("nil submitter must error")
	}
}
