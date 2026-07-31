package msgspool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestPullHandler(t *testing.T, repository *fakeRepository, scope Scope) (*PullHandler, string) {
	t.Helper()
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	service := newTestConsumerService(t, repository, newFakeConsumers(), now)
	_, token, err := service.CreateConsumer(context.Background(), "smsget", "downstream", scope)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPullHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	return handler, token
}

func pullRequest(t *testing.T, handler *PullHandler, token, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestPullAPIReturnsMessagesAndACursor(t *testing.T) {
	repository := &fakeRepository{records: []Record{
		{Message: sampleMessage(), Sequence: 11, DeliveryState: DeliveryPending, DeliveryAttempts: 2},
	}}
	handler, token := newTestPullHandler(t, repository,
		Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})

	recorder := pullRequest(t, handler, token, "/messages?limit=10")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q; OTP bodies must not be cached by an intermediary",
			recorder.Header().Get("Cache-Control"))
	}
	var page PullPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.NextCursor != EncodeCursor(11) {
		t.Fatalf("page=%+v", page)
	}
	message := page.Messages[0]
	if message.MessageID != "019428c1" || message.Connector != "partner-a-term" ||
		message.Partner != "partner-a" || message.To != "380671234567" ||
		message.Parts != 2 || message.Encoding != "ucs2" || message.DataCoding != 8 {
		t.Fatalf("message=%+v", message)
	}
	if message.Verdict != "delivrd" || message.DeliveryState != DeliveryPending ||
		message.DeliveryAttempts != 2 {
		t.Fatalf("message=%+v", message)
	}
	if message.Text == nil || *message.Text != secretText {
		t.Fatalf("text=%v", message.Text)
	}
	// The raw bytes are hex, matching the push delivery contract exactly.
	if message.RawHex == nil || *message.RawHex != "7261772d6f74702d6279746573" {
		t.Fatalf("raw_hex=%v", message.RawHex)
	}
	// received_at keeps a fixed three-digit fraction so the same instant does
	// not sometimes serialize with milliseconds and sometimes without.
	if message.ReceivedAt != "2026-07-30T18:22:41.000Z" {
		t.Fatalf("received_at=%q", message.ReceivedAt)
	}
}

// The redaction requirement, checked on the wire rather than on the struct: a
// consumer without include_text must see the fields *absent*, because an empty
// string is a legitimate message body and a client that could not tell the two
// apart would store every redacted OTP as a blank SMS.
func TestPullAPIOmitsContentFieldsEntirelyWithoutIncludeText(t *testing.T) {
	record := Record{Message: sampleMessage(), Sequence: 4}
	record.Text, record.Raw, record.ContentRedacted = "", nil, true
	repository := &fakeRepository{records: []Record{record}}
	handler, token := newTestPullHandler(t, repository, Scope{Connectors: []string{"partner-a-term"}})

	recorder := pullRequest(t, handler, token, "/messages")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var raw struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Messages) != 1 {
		t.Fatalf("messages=%d", len(raw.Messages))
	}
	for _, field := range []string{"text", "raw_hex"} {
		if _, present := raw.Messages[0][field]; present {
			t.Fatalf("%q present in a metadata-only read: %s", field, recorder.Body.String())
		}
	}
	// Not null either — the key must simply not be there.
	if strings.Contains(recorder.Body.String(), `"text"`) {
		t.Fatalf("body mentions text: %s", recorder.Body.String())
	}
	// The metadata a reconciling application actually needs is still there.
	if _, present := raw.Messages[0]["message_id"]; !present {
		t.Fatalf("metadata was dropped with the content: %s", recorder.Body.String())
	}
	// And the read is still audited, as a search rather than a reveal.
	if len(repository.audits) != 1 || repository.audits[0].Action != ActionSearch {
		t.Fatalf("audits=%+v", repository.audits)
	}
}

// The counterpart: an empty message with include_text must render "text":"".
func TestPullAPIRendersAnEmptyMessageAsAnEmptyString(t *testing.T) {
	record := Record{Message: sampleMessage(), Sequence: 4}
	record.Text, record.Raw = "", nil
	repository := &fakeRepository{records: []Record{record}}
	handler, token := newTestPullHandler(t, repository,
		Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})

	recorder := pullRequest(t, handler, token, "/messages")
	var raw struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	text, present := raw.Messages[0]["text"]
	if !present || string(text) != `""` {
		t.Fatalf("text=%s present=%t", text, present)
	}
}

func TestPullAPIRejectsBadCredentials(t *testing.T) {
	repository := &fakeRepository{}
	handler, token := newTestPullHandler(t, repository, Scope{Connectors: []string{"partner-a-term"}})

	for _, testCase := range []struct {
		name   string
		header string
	}{
		{name: "no header", header: ""},
		{name: "wrong scheme", header: "Basic " + token},
		{name: "unknown token", header: "Bearer synmsg_nope"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/messages", nil)
			if testCase.header != "" {
				request.Header.Set("Authorization", testCase.header)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("no WWW-Authenticate challenge")
			}
			// Every rejection reads identically: distinguishing them would turn
			// the endpoint into an oracle for valid consumer ids.
			if !strings.Contains(recorder.Body.String(), `"error":"unauthorized"`) {
				t.Fatalf("body=%s", recorder.Body.String())
			}
		})
	}
	// Three attempts, three audit rows.
	if len(repository.audits) != 3 {
		t.Fatalf("audits=%d want 3", len(repository.audits))
	}
}

func TestPullAPIRejectsARevokedToken(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	repository := &fakeRepository{records: []Record{{Message: sampleMessage(), Sequence: 1}}}
	service := newTestConsumerService(t, repository, newFakeConsumers(), now)
	ctx := context.Background()
	_, token, err := service.CreateConsumer(ctx, "smsget", "",
		Scope{Connectors: []string{"partner-a-term"}, IncludeText: true})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPullHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	if recorder := pullRequest(t, handler, token, "/messages"); recorder.Code != http.StatusOK {
		t.Fatalf("status before revoke=%d", recorder.Code)
	}
	if _, err = service.SetConsumerRevoked(ctx, "smsget", true); err != nil {
		t.Fatal(err)
	}
	repository.audits = nil
	recorder := pullRequest(t, handler, token, "/messages")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), secretText) {
		t.Fatal("a revoked token received content")
	}
	if len(repository.audits) != 1 || repository.audits[0].Allowed {
		t.Fatalf("audits=%+v", repository.audits)
	}
}

func TestPullAPIRejectsAnOutOfScopeConnectorAndBadParameters(t *testing.T) {
	handler, token := newTestPullHandler(t, &fakeRepository{},
		Scope{Connectors: []string{"partner-a-term"}})

	if recorder := pullRequest(t, handler, token,
		"/messages?connector=partner-b-term"); recorder.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, target := range []string{
		"/messages?limit=notanumber",
		"/messages?limit=100000",
		"/messages?received_from=yesterday",
		"/messages?delivery_state=shipped",
		"/messages?after=not-a-cursor",
	} {
		if recorder := pullRequest(t, handler, token, target); recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", target, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPullAPIRefusesNonGET(t *testing.T) {
	handler, token := newTestPullHandler(t, &fakeRepository{},
		Scope{Connectors: []string{"partner-a-term"}})
	request := httptest.NewRequest(http.MethodPost, "/messages", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status=%d allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
}

// The time filters exist, but as a convenience: they narrow, they never page.
func TestPullAPIPassesTimeFiltersThroughWithoutReplacingTheCursor(t *testing.T) {
	repository := &fakeRepository{}
	handler, token := newTestPullHandler(t, repository, Scope{Connectors: []string{"partner-a-term"}})
	cursor := EncodeCursor(42)
	recorder := pullRequest(t, handler, token,
		"/messages?after="+cursor+"&received_from=2026-07-30T00:00:00Z&received_to=2026-07-31T00:00:00Z")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	query := repository.lastQuery
	if query.AfterSequence != 42 {
		t.Fatalf("cursor did not reach the query: %+v", query)
	}
	if query.ReceivedFrom == nil || query.ReceivedTo == nil {
		t.Fatalf("time filters did not reach the query: %+v", query)
	}
}
