package httpcompat_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/stats"
	"github.com/pumpitspace/jasmin/internal/transport/httpcompat"
	"math/big"
)

type authSpy struct {
	calls int
	err   error
}

func (s *authSpy) Authenticate(context.Context, string, string) error {
	s.calls++
	return s.err
}

type submitSpy struct {
	calls   int
	request core.SubmitRequest
	id      string
	err     error
}

func (s *submitSpy) Submit(_ context.Context, request core.SubmitRequest) (string, error) {
	s.calls++
	s.request = request
	return s.id, s.err
}

func TestSendValidationStopsBeforePorts(t *testing.T) {
	tests := []struct {
		name string
		form url.Values
		body string
	}{
		{
			name: "no arguments",
			form: url.Values{},
			body: `Error "Mandatory argument [to] is not found."`,
		},
		{
			name: "missing password",
			form: url.Values{"username": {"nathalie"}, "to": {"06155423"}, "content": {"hello"}},
			body: `Error "Mandatory argument [password] is not found."`,
		},
		{
			name: "missing both content forms",
			form: url.Values{"username": {"nathalie"}, "password": {"correct"}, "to": {"06155423"}},
			body: `Error "content or hex-content not present."`,
		},
		{
			name: "both content forms present",
			form: url.Values{"username": {"nathalie"}, "password": {"correct"}, "to": {"06155423"}, "content": {""}, "hex-content": {""}},
			body: `Error "content and hex-content cannot be used both in same request."`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth := &authSpy{}
			submit := &submitSpy{}
			response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", tc.form)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			if response.Body.String() != tc.body {
				t.Fatalf("body = %q, want %q", response.Body.String(), tc.body)
			}
			if auth.calls != 0 || submit.calls != 0 {
				t.Fatalf("port calls: auth=%d submit=%d, want 0/0", auth.calls, submit.calls)
			}
		})
	}
}

func TestSendAuthenticationFailureDoesNotSubmit(t *testing.T) {
	auth := &authSpy{err: core.ErrAuthentication}
	submit := &submitSpy{}
	response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", validSendForm())

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if auth.calls != 1 || submit.calls != 0 {
		t.Fatalf("port calls: auth=%d submit=%d, want 1/0", auth.calls, submit.calls)
	}
}

func TestSendPassesNormalizedRequestToPort(t *testing.T) {
	auth := &authSpy{}
	submit := &submitSpy{id: "message-123"}
	response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", validSendForm())

	if response.Code != http.StatusOK || response.Body.String() != `Success "message-123"` {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	want := core.SubmitRequest{
		Username: "nathalie", Password: "correct", Destination: "06155423", Content: "hello",
		DLRMethod: "POST", Coding: 0,
	}
	if !reflect.DeepEqual(submit.request, want) {
		t.Fatalf("submit request = %#v, want %#v", submit.request, want)
	}
}

func TestEmptyHexContentIsPresent(t *testing.T) {
	auth := &authSpy{}
	submit := &submitSpy{err: core.ErrNoLiveConnector}
	form := url.Values{
		"username":    {"nathalie"},
		"password":    {"correct"},
		"to":          {"06155423"},
		"hex-content": {""},
	}
	response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", form)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if auth.calls != 1 || submit.calls != 1 {
		t.Fatalf("port calls: auth=%d submit=%d, want 1/1", auth.calls, submit.calls)
	}
}

func TestSendMapsNoRouteToLegacyServerError(t *testing.T) {
	auth := &authSpy{}
	submit := &submitSpy{err: core.ErrNoRouteMatched}
	response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", validSendForm())
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if response.Body.String() != `Error "Cannot send submit_sm, check SMPPClientManagerPB log file for details"` {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestPingSuppressesAutomaticContentType(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/ping", nil)
	response := httptest.NewRecorder()
	httpcompat.NewHandler(httpcompat.Dependencies{}).ServeHTTP(response, request)
	result := response.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "Jasmin/PONG" {
		t.Fatalf("body = %q", body)
	}
	if values := result.Header.Values("Content-Type"); len(values) != 0 {
		t.Fatalf("Content-Type = %#v, want absent", values)
	}
}

func TestUnsupportedMethodDoesNotCallPorts(t *testing.T) {
	auth := &authSpy{err: errors.New("must not be called")}
	submit := &submitSpy{err: errors.New("must not be called")}
	request := httptest.NewRequest(http.MethodDelete, "/send", nil)
	response := httptest.NewRecorder()
	httpcompat.NewHandler(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}).ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}
	if auth.calls != 0 || submit.calls != 0 {
		t.Fatalf("port calls: auth=%d submit=%d, want 0/0", auth.calls, submit.calls)
	}
}

func validSendForm() url.Values {
	return url.Values{
		"username": {"nathalie"},
		"password": {"correct"},
		"to":       {"06155423"},
		"content":  {"hello"},
	}
}

func serveForm(dependencies httpcompat.Dependencies, method, path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	httpcompat.NewHandler(dependencies).ServeHTTP(response, request)
	return response
}

func TestSendCustomTLVsFromQueryPreserveDocumentOrder(t *testing.T) {
	auth := &authSpy{}
	submit := &submitSpy{id: "message-123"}
	form := validSendForm()
	form.Set("custom_tlvs", `{"0x1500": "b", "0x1400": "a"}`)
	response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", form)

	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	tlvs := submit.request.CustomTLVs
	if len(tlvs) != 2 || tlvs[0].Tag.Cmp(big.NewInt(0x1500)) != 0 || tlvs[1].Tag.Cmp(big.NewInt(0x1400)) != 0 {
		t.Fatalf("TLVs must keep document order: %+v", tlvs)
	}
	if tlvs[0].Value != "b" || tlvs[1].Value != "a" || tlvs[0].Type != "" {
		t.Fatalf("TLV tuples = %+v", tlvs)
	}
}

func TestSendCustomTLVsFromJSONBodyPreserveDocumentOrder(t *testing.T) {
	auth := &authSpy{}
	submit := &submitSpy{id: "message-123"}
	body := `{"username": "nathalie", "password": "correct", "to": "06155423", "content": "hello",` +
		` "custom_tlvs": {"0x1401:OctetString": "1401778070000018542", "0x1400": "1707167205648943173"}}`
	request := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpcompat.NewHandler(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	tlvs := submit.request.CustomTLVs
	if len(tlvs) != 2 || tlvs[0].Tag.Cmp(big.NewInt(0x1401)) != 0 || tlvs[1].Tag.Cmp(big.NewInt(0x1400)) != 0 {
		t.Fatalf("TLVs must keep body document order: %+v", tlvs)
	}
	if tlvs[0].Type != "OctetString" || tlvs[0].Value != "1401778070000018542" ||
		tlvs[1].Type != "" || tlvs[1].Value != "1707167205648943173" {
		t.Fatalf("TLV tuples = %+v", tlvs)
	}
}

func TestSendCustomTLVsBadTagMapsToLegacyUnknownError(t *testing.T) {
	auth := &authSpy{}
	submit := &submitSpy{}
	form := validSendForm()
	form.Set("custom_tlvs", `{"banana": 1}`)
	response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", form)

	// The legacy endpoint raises out of route_routable and answers with the
	// generic exception envelope: 500 + Error "Unknown error: ...".
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if want := `Error "Unknown error: invalid integer tag \"banana\""`; response.Body.String() != want {
		t.Fatalf("body = %q, want %q", response.Body.String(), want)
	}
	if submit.calls != 0 {
		t.Fatalf("submit calls = %d, want 0", submit.calls)
	}
}

func TestSendCustomTLVsMalformedJSONIsSilentlyEmpty(t *testing.T) {
	auth := &authSpy{}
	submit := &submitSpy{id: "message-123"}
	form := validSendForm()
	form.Set("custom_tlvs", `{"banana": 1, `)
	response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", form)

	// json.loads failure is swallowed by the oracle (empty TLV set), even with
	// a bad tag earlier in the text.
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if submit.request.CustomTLVs != nil {
		t.Fatalf("TLVs = %+v, want none", submit.request.CustomTLVs)
	}
}

func TestSendAuthenticationRunsBeforeCustomTLVs(t *testing.T) {
	auth := &authSpy{err: core.ErrAuthentication}
	submit := &submitSpy{}
	form := validSendForm()
	form.Set("custom_tlvs", `{"banana": 1}`)
	response := serveForm(httpcompat.Dependencies{Authenticator: auth, Submitter: submit}, http.MethodPost, "/send", form)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (auth failure wins over TLV error)", response.Code)
	}
	if submit.calls != 0 {
		t.Fatalf("submit calls = %d, want 0", submit.calls)
	}
}

func TestMetricsEndpointReflectsHTTPCounters(t *testing.T) {
	httpStats := &stats.HTTPStats{}
	auth := &authSpy{}
	submit := &submitSpy{id: "m-1"}
	deps := httpcompat.Dependencies{Authenticator: auth, Submitter: submit, HTTPStats: httpStats}

	// A successful send increments request_count and success_count.
	if resp := serveForm(deps, http.MethodPost, "/send", validSendForm()); resp.Code != http.StatusOK {
		t.Fatalf("send status = %d", resp.Code)
	}
	// A no-route send increments request_count and route_error_count.
	failing := httpcompat.Dependencies{Authenticator: auth, Submitter: &submitSpy{err: core.ErrNoRouteMatched}, HTTPStats: httpStats}
	if resp := serveForm(failing, http.MethodPost, "/send", validSendForm()); resp.Code != http.StatusInternalServerError {
		t.Fatalf("failing send status = %d", resp.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	httpcompat.NewHandler(deps).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d", response.Code)
	}
	if ct := response.Header().Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("/metrics content-type = %q", ct)
	}
	body := response.Body.String()
	for _, want := range []string{
		"httpapi_request_count 2",
		"httpapi_success_count 1",
		"httpapi_route_error_count 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsAuthErrorCounter(t *testing.T) {
	httpStats := &stats.HTTPStats{}
	auth := &authSpy{err: core.ErrAuthentication}
	deps := httpcompat.Dependencies{Authenticator: auth, Submitter: &submitSpy{}, HTTPStats: httpStats}
	serveForm(deps, http.MethodPost, "/send", validSendForm())

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	httpcompat.NewHandler(deps).ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "httpapi_auth_error_count 1") {
		t.Fatalf("auth_error_count not incremented:\n%s", response.Body.String())
	}
}

func TestMetricsRejectsNonGET(t *testing.T) {
	response := serveForm(httpcompat.Dependencies{}, http.MethodPost, "/metrics", url.Values{})
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics status = %d, want 405", response.Code)
	}
}
