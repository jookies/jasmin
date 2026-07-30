package restcompat_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/restcompat"
)

type upstreamCall struct {
	method      string
	path        string
	query       url.Values
	contentType string
	body        []byte
}

type upstreamSpy struct {
	call   upstreamCall
	status int
	body   string
}

func (spy *upstreamSpy) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	spy.call = upstreamCall{
		method: request.Method, path: request.URL.Path, query: request.URL.Query(),
		contentType: request.Header.Get("Content-Type"), body: body,
	}
	status := spy.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, spy.body)
}

func TestSecureSendMapsJSONAndUsesBasicCredentials(t *testing.T) {
	upstream := &upstreamSpy{body: `Success "message-123"`}
	request := httptest.NewRequest(http.MethodPost, "/secure/send", strings.NewReader(`{
		"username": "body-user",
		"password": "body-password",
		"to": 19012233451,
		"hex_content": "00ff",
		"dlr_level": 3,
		"custom_tlvs": {"0x1500": "b", "0x1400": "a"}
	}`))
	request.Header.Set("Authorization", basic("alice", "secret"))
	response := httptest.NewRecorder()

	restcompat.NewHandler(upstream).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if upstream.call.method != http.MethodPost || upstream.call.path != "/send" ||
		upstream.call.contentType != "application/json" {
		t.Fatalf("upstream call = %+v", upstream.call)
	}
	var mapped map[string]json.RawMessage
	if err := json.Unmarshal(upstream.call.body, &mapped); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"username":    `"alice"`,
		"password":    `"secret"`,
		"to":          `19012233451`,
		"hex-content": `"00ff"`,
		"dlr-level":   `3`,
		"custom_tlvs": `{"0x1500":"b","0x1400":"a"}`,
	} {
		if got := string(mapped[key]); got != want {
			t.Errorf("mapped[%q] = %s, want %s", key, got, want)
		}
	}
	if _, found := mapped["hex_content"]; found {
		t.Fatal("underscore alias leaked upstream")
	}
	assertJSON(t, response, map[string]any{"data": `Success "message-123"`})
}

func TestSecureSendPreservesUpstreamError(t *testing.T) {
	upstream := &upstreamSpy{status: http.StatusForbidden, body: `Error "Authentication failure for username:alice"`}
	request := httptest.NewRequest(http.MethodPost, "/secure/send", strings.NewReader(`{"to":"1","content":"hi"}`))
	request.Header.Set("Authorization", basic("alice", "wrong"))
	response := httptest.NewRecorder()

	restcompat.NewHandler(upstream).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d", response.Code)
	}
	assertJSON(t, response, map[string]any{"message": `Error "Authentication failure for username:alice"`})
}

func TestSecureBalanceWrapsParsedObject(t *testing.T) {
	upstream := &upstreamSpy{body: `{"balance": "10.23", "sms_count": "ND"}`}
	request := httptest.NewRequest(http.MethodGet, "/secure/balance", nil)
	request.Header.Set("Authorization", basic("alice", "s:e:c:r:e:t"))
	response := httptest.NewRecorder()

	restcompat.NewHandler(upstream).ServeHTTP(response, request)

	if got := upstream.call.query.Get("username"); got != "alice" {
		t.Fatalf("username = %q", got)
	}
	if got := upstream.call.query.Get("password"); got != "s:e:c:r:e:t" {
		t.Fatalf("password = %q", got)
	}
	assertJSON(t, response, map[string]any{
		"data": map[string]any{"balance": "10.23", "sms_count": "ND"},
	})
}

func TestSecureRateMapsUnderscoredQueryNames(t *testing.T) {
	upstream := &upstreamSpy{body: `{"unit_rate": 0.02, "submit_sm_count": 1}`}
	request := httptest.NewRequest(http.MethodGet, "/secure/rate?to=19012233451&some_name=value", nil)
	request.Header.Set("Authorization", basic("alice", "secret"))
	response := httptest.NewRecorder()

	restcompat.NewHandler(upstream).ServeHTTP(response, request)

	if upstream.call.path != "/rate" || upstream.call.query.Get("some-name") != "value" {
		t.Fatalf("upstream call = %+v", upstream.call)
	}
	assertJSON(t, response, map[string]any{
		"data": map[string]any{"unit_rate": 0.02, "submit_sm_count": float64(1)},
	})
}

func TestSecureResourcesRequireValidBasicAuth(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "wrong scheme", header: "Bearer " + base64.StdEncoding.EncodeToString([]byte("alice:secret"))},
		{name: "bad base64", header: "Basic !!!"},
		{name: "no separator", header: "Basic " + base64.StdEncoding.EncodeToString([]byte("alice"))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &upstreamSpy{}
			request := httptest.NewRequest(http.MethodGet, "/secure/balance", nil)
			request.Header.Set("Authorization", tc.header)
			response := httptest.NewRecorder()
			restcompat.NewHandler(upstream).ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if upstream.call.path != "" {
				t.Fatalf("upstream was called: %+v", upstream.call)
			}
			assertContentType(t, response)
		})
	}
}

func TestAcceptNegotiationPrecedesAuthentication(t *testing.T) {
	for _, accept := range []string{"text/plain", "application/json;q=0, text/plain"} {
		upstream := &upstreamSpy{}
		request := httptest.NewRequest(http.MethodGet, "/secure/balance", nil)
		request.Header.Set("Accept", accept)
		response := httptest.NewRecorder()

		restcompat.NewHandler(upstream).ServeHTTP(response, request)

		if response.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("Accept %q: status = %d, body = %s", accept, response.Code, response.Body.String())
		}
		if upstream.call.path != "" {
			t.Fatalf("Accept %q: upstream was called: %+v", accept, upstream.call)
		}
	}
}

func TestInvalidJSONAndMethodsNeverReachUpstream(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
		status int
	}{
		{name: "malformed", method: http.MethodPost, body: `{"to":`, status: http.StatusPreconditionFailed},
		{name: "non object", method: http.MethodPost, body: `[]`, status: http.StatusPreconditionFailed},
		{name: "trailing", method: http.MethodPost, body: `{} {}`, status: http.StatusPreconditionFailed},
		{name: "wrong method", method: http.MethodGet, body: `{}`, status: http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &upstreamSpy{}
			request := httptest.NewRequest(tc.method, "/secure/send", strings.NewReader(tc.body))
			request.Header.Set("Authorization", basic("alice", "secret"))
			response := httptest.NewRecorder()
			restcompat.NewHandler(upstream).ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, tc.status, response.Body.String())
			}
			if upstream.call.path != "" {
				t.Fatalf("upstream was called: %+v", upstream.call)
			}
		})
	}
}

func TestSecureOptionsMatchesFalconResourceDiscovery(t *testing.T) {
	for _, tc := range []struct {
		path  string
		allow string
	}{
		{path: "/secure/send", allow: http.MethodPost},
		{path: "/secure/sendbatch", allow: http.MethodPost},
		{path: "/secure/balance", allow: http.MethodGet},
		{path: "/secure/rate", allow: http.MethodGet},
	} {
		t.Run(tc.path, func(t *testing.T) {
			upstream := &upstreamSpy{}
			request := httptest.NewRequest(http.MethodOptions, tc.path, nil)
			request.Header.Set("Authorization", basic("alice", "secret"))
			response := httptest.NewRecorder()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			restcompat.NewHandler(upstream, restcompat.WithBatchContext(ctx)).ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "null" {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if response.Header().Get("Allow") != tc.allow {
				t.Fatalf("Allow = %q, want %q", response.Header().Get("Allow"), tc.allow)
			}
			if upstream.call.path != "" {
				t.Fatalf("upstream was called: %+v", upstream.call)
			}
		})
	}
}

func TestUnknownSecureResourceIsAuthenticatedJSON404(t *testing.T) {
	upstream := &upstreamSpy{}
	unauthenticated := httptest.NewRecorder()
	restcompat.NewHandler(upstream).ServeHTTP(
		unauthenticated, httptest.NewRequest(http.MethodPost, "/secure/sendbatch", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/secure/sendbatch", nil)
	request.Header.Set("Authorization", basic("alice", "secret"))
	response := httptest.NewRecorder()
	restcompat.NewHandler(upstream).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertContentType(t, response)
}

func TestSecureSendBatchExpandsGlobalsAndDestinations(t *testing.T) {
	upstream := &batchUpstream{calls: make(chan batchUpstreamCall, 4)}
	request := httptest.NewRequest(http.MethodPost, "/secure/sendbatch", strings.NewReader(`{
		"globals": {"from": "Global", "coding": 8, "custom_tlvs": {"0x1400": "x"}},
		"messages": [
			{"to": ["111", "222"], "content": "first"},
			{"to": "333", "from": "Local", "hex_content": "00ff"},
			{"to": "ignored"}
		]
	}`))
	request.Header.Set("Authorization", basic("alice", "secret"))
	response := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restcompat.NewHandler(upstream, restcompat.WithBatchContext(ctx)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			BatchID      string `json:"batchId"`
			MessageCount int    `json:"messageCount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.MessageCount != 3 || len(envelope.Data.BatchID) != 36 {
		t.Fatalf("batch response = %+v", envelope.Data)
	}
	got := make(map[string]map[string]json.RawMessage, 3)
	for range 3 {
		select {
		case call := <-upstream.calls:
			got[string(call.payload["to"])] = call.payload
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for batch submission")
		}
	}
	if upstream.balanceCalls() != 1 {
		t.Fatalf("balance calls = %d", upstream.balanceCalls())
	}
	if string(got[`"111"`]["from"]) != `"Global"` || string(got[`"222"`]["content"]) != `"first"` {
		t.Fatalf("global projection = %+v", got)
	}
	if string(got[`"333"`]["from"]) != `"Local"` || string(got[`"333"`]["hex-content"]) != `"00ff"` {
		t.Fatalf("local override = %+v", got[`"333"`])
	}
	if string(got[`"333"`]["username"]) != `"alice"` || string(got[`"333"`]["password"]) != `"__batch__"` {
		t.Fatalf("credentials = %+v", got[`"333"`])
	}
	if string(got[`"111"`]["custom_tlvs"]) != `{"0x1400":"x"}` {
		t.Fatalf("custom TLVs = %s", got[`"111"`]["custom_tlvs"])
	}
}

func TestSecureSendBatchSchedulingAndCallbacks(t *testing.T) {
	callbacks := make(chan url.Values, 2)
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		callbacks <- request.URL.Query()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()
	upstream := &batchUpstream{calls: make(chan batchUpstreamCall, 2), failDestination: "222"}
	body := `{
		"batch_config": {
			"schedule_at": "1s",
			"callback_url": %q,
			"errback_url": %q
		},
		"messages": [{"to": ["111", "222"], "content": "hi"}]
	}`
	request := httptest.NewRequest(http.MethodPost, "/secure/sendbatch",
		strings.NewReader(fmt.Sprintf(body, callbackServer.URL, callbackServer.URL)))
	request.Header.Set("Authorization", basic("alice", "secret"))
	response := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restcompat.NewHandler(upstream,
		restcompat.WithBatchContext(ctx),
		restcompat.WithCallbackClient(callbackServer.Client()),
	).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var raw map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &raw)
	data := raw["data"].(map[string]any)
	if data["scheduled"] != "1s" || data["messageCount"] != float64(2) {
		t.Fatalf("batch data = %+v", data)
	}
	select {
	case <-upstream.calls:
		t.Fatal("scheduled submit ran immediately")
	case <-time.After(100 * time.Millisecond):
	}
	seen := make(map[string]url.Values, 2)
	for range 2 {
		select {
		case callback := <-callbacks:
			seen[callback.Get("to")] = callback
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for callbacks")
		}
	}
	if seen["111"].Get("status") != "1" || seen["111"].Get("statusText") != `Success "111"` {
		t.Fatalf("success callback = %v", seen["111"])
	}
	if seen["222"].Get("status") != "0" ||
		seen["222"].Get("statusText") != `HTTPAPI error: Error "failed"` {
		t.Fatalf("error callback = %v", seen["222"])
	}
	if seen["111"].Get("batchId") == "" || seen["111"].Get("batchId") != seen["222"].Get("batchId") {
		t.Fatalf("callback batch ids differ: %v", seen)
	}
}

func TestSecureSendBatchRejectsAuthenticationAndBadScheduleBeforeDispatch(t *testing.T) {
	tests := []struct {
		name          string
		balanceStatus int
		body          string
		title         string
	}{
		{
			name: "authentication", balanceStatus: http.StatusForbidden,
			body: `{not json`, title: "Authentication failed",
		},
		{
			name: "unknown schedule", body: `{
				"batch_config":{"schedule_at":"tomorrow"},
				"messages":[{"to":"1","content":"hi"}]
			}`, title: "Cannot parse scheduled_at value",
		},
		{
			name: "past schedule", body: `{
				"batch_config":{"schedule_at":"2000-01-01 00:00:00"},
				"messages":[{"to":"1","content":"hi"}]
			}`, title: "Cannot schedule batch in past date",
		},
		{
			name: "unsafe callback", body: `{
				"batch_config":{"callback_url":"file:///etc/passwd"},
				"messages":[{"to":"1","content":"hi"}]
			}`, title: "Cannot parse callback_url",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &batchUpstream{calls: make(chan batchUpstreamCall, 1), balanceStatus: tc.balanceStatus}
			request := httptest.NewRequest(http.MethodPost, "/secure/sendbatch", strings.NewReader(tc.body))
			request.Header.Set("Authorization", basic("alice", "secret"))
			response := httptest.NewRecorder()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			restcompat.NewHandler(upstream, restcompat.WithBatchContext(ctx)).ServeHTTP(response, request)
			if response.Code != http.StatusPreconditionFailed {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var apiErr map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &apiErr); err != nil {
				t.Fatal(err)
			}
			if apiErr["title"] != tc.title {
				t.Fatalf("error = %+v", apiErr)
			}
			select {
			case call := <-upstream.calls:
				t.Fatalf("unexpected submit: %+v", call)
			default:
			}
		})
	}
}

func TestNonRESTPathsAreUnchanged(t *testing.T) {
	upstream := &upstreamSpy{status: http.StatusTeapot, body: "legacy"}
	request := httptest.NewRequest(http.MethodGet, "/ping", nil)
	response := httptest.NewRecorder()
	restcompat.NewHandler(upstream).ServeHTTP(response, request)
	if response.Code != http.StatusTeapot || response.Body.String() != "legacy" || upstream.call.path != "/ping" {
		t.Fatalf("response = %d %q, call = %+v", response.Code, response.Body.String(), upstream.call)
	}
}

func TestStandaloneRESTPingIsJSONWithoutChangingCombinedLegacyPing(t *testing.T) {
	upstream := &upstreamSpy{body: "Jasmin/PONG"}
	handlers, err := restcompat.NewHandlers(upstream)
	if err != nil {
		t.Fatal(err)
	}
	daemonResponse := httptest.NewRecorder()
	handlers.Daemon.ServeHTTP(
		daemonResponse, httptest.NewRequest(http.MethodGet, "/ping", nil))
	assertJSON(t, daemonResponse, map[string]any{"data": "Jasmin/PONG"})

	legacyResponse := httptest.NewRecorder()
	handlers.Combined.ServeHTTP(
		legacyResponse, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if legacyResponse.Code != http.StatusOK || legacyResponse.Body.String() != "Jasmin/PONG" {
		t.Fatalf("combined /ping = %d %q", legacyResponse.Code, legacyResponse.Body.String())
	}
}

func TestScheduledBatchSurvivesDispatcherRestart(t *testing.T) {
	store := restcompat.NewMemoryBatchStore()
	upstream := &batchUpstream{calls: make(chan batchUpstreamCall, 2)}
	firstContext, cancelFirst := context.WithCancel(context.Background())
	first := restcompat.NewHandler(upstream,
		restcompat.WithBatchContext(firstContext),
		restcompat.WithBatchStore(store),
		restcompat.WithBatchQoS(0, false),
	)
	request := httptest.NewRequest(http.MethodPost, "/secure/sendbatch", strings.NewReader(`{
		"batch_config":{"schedule_at":"1s"},
		"messages":[{"to":"111","content":"survives"}]
	}`))
	request.Header.Set("Authorization", basic("alice", "secret"))
	response := httptest.NewRecorder()
	first.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admission = %d %s", response.Code, response.Body.String())
	}
	cancelFirst()

	secondContext, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	_, err := restcompat.NewHandlers(upstream,
		restcompat.WithBatchContext(secondContext),
		restcompat.WithBatchStore(store),
		restcompat.WithBatchQoS(0, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-upstream.calls:
		if string(call.payload["to"]) != `"111"` ||
			string(call.payload["password"]) != `"__batch__"` {
			t.Fatalf("recovered payload = %+v", call.payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled durable task was not recovered")
	}
	select {
	case duplicate := <-upstream.calls:
		t.Fatalf("recovered task ran twice: %+v", duplicate)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestBatchBacklogIsRejectedBeforeAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &batchUpstream{calls: make(chan batchUpstreamCall, 2)}
	handler := restcompat.NewHandler(upstream,
		restcompat.WithBatchContext(ctx),
		restcompat.WithBatchStore(restcompat.NewMemoryBatchStore()),
		restcompat.WithBatchLimits(1, 1, 0, 1, 0),
	)
	submit := func(destination string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{
			"batch_config":{"schedule_at":"10s"},
			"messages":[{"to":%q,"content":"queued"}]
		}`, destination)
		request := httptest.NewRequest(http.MethodPost, "/secure/sendbatch", strings.NewReader(body))
		request.Header.Set("Authorization", basic("alice", "secret"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := submit("111"); response.Code != http.StatusOK {
		t.Fatalf("first admission = %d %s", response.Code, response.Body.String())
	}
	if response := submit("222"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("overflow admission = %d %s", response.Code, response.Body.String())
	}
}

func TestBatchCallbackRetriesWithStableTerminalPayload(t *testing.T) {
	var mu sync.Mutex
	var seen []url.Values
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		seen = append(seen, request.URL.Query())
		attempt := len(seen)
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &batchUpstream{calls: make(chan batchUpstreamCall, 1)}
	handler := restcompat.NewHandler(upstream,
		restcompat.WithBatchContext(ctx),
		restcompat.WithCallbackClient(callbackServer.Client()),
		restcompat.WithBatchLimits(10, 1, 0, 3, 10*time.Millisecond),
	)
	body := fmt.Sprintf(`{
		"batch_config":{"callback_url":%q},
		"messages":[{"to":"111","content":"hi"}]
	}`, callbackServer.URL)
	request := httptest.NewRequest(http.MethodPost, "/secure/sendbatch", strings.NewReader(body))
	request.Header.Set("Authorization", basic("alice", "secret"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admission = %d %s", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		count := len(seen)
		mu.Unlock()
		if count >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("callback was not retried")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	first, second := seen[0], seen[1]
	mu.Unlock()
	if first.Encode() != second.Encode() || first.Get("status") != "1" ||
		first.Get("statusText") != `Success "111"` {
		t.Fatalf("callback retries diverged: first=%v second=%v", first, second)
	}
}

func basic(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func assertJSON(t *testing.T, response *httptest.ResponseRecorder, want map[string]any) {
	t.Helper()
	assertContentType(t, response)
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v; body = %q", err, response.Body.String())
	}
	wantBody, _ := json.Marshal(want)
	gotBody, _ := json.Marshal(got)
	if string(gotBody) != string(wantBody) {
		t.Fatalf("JSON = %s, want %s", gotBody, wantBody)
	}
}

func assertContentType(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Powered-By"); got != "Jasmin 0.11.1" {
		t.Fatalf("Powered-By = %q", got)
	}
}

type batchUpstreamCall struct {
	payload map[string]json.RawMessage
}

type batchUpstream struct {
	mu              sync.Mutex
	balances        int
	balanceStatus   int
	failDestination string
	calls           chan batchUpstreamCall
}

func (upstream *batchUpstream) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/balance":
		upstream.mu.Lock()
		upstream.balances++
		upstream.mu.Unlock()
		status := upstream.balanceStatus
		if status == 0 {
			status = http.StatusOK
		}
		if request.Method != http.MethodGet ||
			request.Body != http.NoBody ||
			request.ContentLength != 0 ||
			request.Header.Get("Content-Type") != "" ||
			request.URL.Query().Get("username") == "" ||
			request.URL.Query().Get("password") == "" {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"balance":"ND","sms_count":"ND"}`)
	case "/send":
		var payload map[string]json.RawMessage
		_ = json.NewDecoder(request.Body).Decode(&payload)
		upstream.calls <- batchUpstreamCall{payload: payload}
		var destination string
		_ = json.Unmarshal(payload["to"], &destination)
		if destination == upstream.failDestination {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `Error "failed"`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `Success "`+destination+`"`)
	default:
		http.NotFound(w, request)
	}
}

func (upstream *batchUpstream) balanceCalls() int {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	return upstream.balances
}
