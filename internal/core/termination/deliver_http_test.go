package termination

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// deliveryCapture records what the downstream application actually received, so
// assertions are made against the bytes on the wire rather than against what the
// sink intended to send.
type deliveryCapture struct {
	mu        sync.Mutex
	requests  []deliveryCapturedRequest
	responses func(attempt int) (int, string)
}

type deliveryCapturedRequest struct {
	method    string
	body      []byte
	messageID string
	attempt   string
	timestamp string
	signature string
	signed    bool
	mediaType string
}

func (c *deliveryCapture) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		signature, signed := r.Header[http.CanonicalHeaderKey(HeaderDeliverySignature)]
		captured := deliveryCapturedRequest{
			method:    r.Method,
			body:      body,
			messageID: r.Header.Get(HeaderDeliveryMessageID),
			attempt:   r.Header.Get(HeaderDeliveryAttempt),
			timestamp: r.Header.Get(HeaderDeliveryTimestamp),
			signed:    signed,
			mediaType: r.Header.Get("Content-Type"),
		}
		if signed && len(signature) > 0 {
			captured.signature = signature[0]
		}

		c.mu.Lock()
		c.requests = append(c.requests, captured)
		attempt := len(c.requests)
		respond := c.responses
		c.mu.Unlock()

		status, respBody := http.StatusOK, ""
		if respond != nil {
			status, respBody = respond(attempt)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}
}

func (c *deliveryCapture) all() []deliveryCapturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]deliveryCapturedRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func deliveryTestMessage() Message {
	raw, _ := hex.DecodeString("041f0420043e")
	return Message{
		MessageID:  "019428c1-7f3a-7b21-9c04-8d1e2f6a5b40",
		Connector:  "partner-a-term",
		Partner:    "partner-a",
		From:       "NETFLIX",
		To:         "380671234567",
		Text:       "ПРОВЕРОЧНЫЙ КОД 63125",
		Raw:        raw,
		DataCoding: 8,
		Encoding:   "ucs2",
		Parts:      2,
		ReceivedAt: time.Date(2026, 7, 30, 18, 22, 41, 113_000_000, time.UTC),
	}
}

func deliveryTestSink(t *testing.T, cfg HTTPSinkConfig) *HTTPSink {
	t.Helper()
	sink, err := NewHTTPSink(cfg)
	if err != nil {
		t.Fatalf("NewHTTPSink: %v", err)
	}
	return sink
}

// The signature must verify against the bytes the server received, recomputed
// the way a downstream application would — not by calling the gateway's own
// helper, which would pass even if both sides shared the same bug.
func TestHTTPSinkSignsTheBytesItSends(t *testing.T) {
	capture := &deliveryCapture{}
	srv := httptest.NewServer(capture.handler(t))
	defer srv.Close()

	secret := []byte("per-connector-secret")
	sink := deliveryTestSink(t, HTTPSinkConfig{
		Endpoint: srv.URL,
		Secret:   secret,
		Client:   srv.Client(),
		Verdicts: PayloadVerdictFunc(func(context.Context, Message) (Verdict, bool) {
			return Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}, true
		}),
	})

	msg := deliveryTestMessage()
	result, err := sink.Deliver(context.Background(), msg, 1)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result.StatusCode != http.StatusOK || result.Attempt != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.HasVerdict {
		t.Fatal("HasVerdict is set outside http-inline mode")
	}

	requests := capture.all()
	if len(requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(requests))
	}
	got := requests[0]

	if got.method != http.MethodPost {
		t.Fatalf("method = %s, want POST", got.method)
	}
	if got.mediaType != "application/json" {
		t.Fatalf("Content-Type = %q", got.mediaType)
	}
	if got.messageID != msg.MessageID {
		t.Fatalf("%s = %q, want %q", HeaderDeliveryMessageID, got.messageID, msg.MessageID)
	}
	if got.attempt != "1" {
		t.Fatalf("%s = %q, want 1", HeaderDeliveryAttempt, got.attempt)
	}

	unix, err := strconv.ParseInt(got.timestamp, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q, not unix seconds: %v", HeaderDeliveryTimestamp, got.timestamp, err)
	}
	if delta := time.Since(time.Unix(unix, 0)); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("%s = %q, not close to now", HeaderDeliveryTimestamp, got.timestamp)
	}

	// Independent recomputation over the received bytes.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(got.timestamp + "." + string(got.body)))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got.signature != want {
		t.Fatalf("signature = %q, want %q", got.signature, want)
	}

	var payload DeliveryPayload
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("received body is not the delivery payload: %v", err)
	}
	if payload.MessageID != msg.MessageID || payload.RawHex != "041f0420043e" || payload.Verdict != "delivrd" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestHTTPSinkOmitsSignatureWithoutSecret(t *testing.T) {
	capture := &deliveryCapture{}
	srv := httptest.NewServer(capture.handler(t))
	defer srv.Close()

	sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: srv.URL, Client: srv.Client()})
	if _, err := sink.Deliver(context.Background(), deliveryTestMessage(), 1); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// Absent, not empty: an application must be able to tell "this gateway does
	// not sign" from "this signature does not verify".
	if got := capture.all()[0]; got.signed {
		t.Fatalf("%s sent with no configured secret: %q", HeaderDeliverySignature, got.signature)
	}
}

// The idempotency key is what lets a receiver recognise the retry of a delivery
// that had actually succeeded. It must not change between attempts; the attempt
// header must.
func TestHTTPSinkRetryReusesMessageIDAndAdvancesAttempt(t *testing.T) {
	capture := &deliveryCapture{
		responses: func(attempt int) (int, string) {
			if attempt < 3 {
				return http.StatusInternalServerError, "boom"
			}
			return http.StatusOK, ""
		},
	}
	srv := httptest.NewServer(capture.handler(t))
	defer srv.Close()

	secret := []byte("per-connector-secret")
	sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: srv.URL, Secret: secret, Client: srv.Client()})
	msg := deliveryTestMessage()

	for attempt := 1; attempt <= 2; attempt++ {
		result, err := sink.Deliver(context.Background(), msg, attempt)
		if err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
		if !DeliveryRetryable(err) {
			t.Fatalf("attempt %d: 500 classified terminal: %v", attempt, err)
		}
		if result.Attempt != attempt || result.StatusCode != http.StatusInternalServerError {
			t.Fatalf("attempt %d result = %+v", attempt, result)
		}
	}

	result, err := sink.Deliver(context.Background(), msg, 3)
	if err != nil {
		t.Fatalf("attempt 3: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("attempt 3 result = %+v", result)
	}

	requests := capture.all()
	if len(requests) != 3 {
		t.Fatalf("got %d requests, want 3", len(requests))
	}
	for i, got := range requests {
		if got.messageID != msg.MessageID {
			t.Fatalf("request %d: message id = %q, want %q — a retry must reuse the idempotency key",
				i+1, got.messageID, msg.MessageID)
		}
		if want := strconv.Itoa(i + 1); got.attempt != want {
			t.Fatalf("request %d: attempt header = %q, want %q", i+1, got.attempt, want)
		}
		// Each attempt is signed over its own timestamp and its own bytes.
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(got.timestamp + "." + string(got.body)))
		if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); got.signature != want {
			t.Fatalf("request %d: signature does not verify over the received bytes", i+1)
		}
	}
}

// Retrying a payload the application has rejected is how one poison message
// becomes an outage, so the 4xx/5xx split is asserted status by status.
func TestHTTPSinkStatusClassification(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantErr   bool
		retryable bool
	}{
		{name: "200 ok", status: http.StatusOK},
		{name: "201 ok", status: http.StatusCreated},
		{name: "202 ok", status: http.StatusAccepted},
		{name: "204 ok", status: http.StatusNoContent},
		{name: "299 ok", status: 299},
		{name: "301 terminal", status: http.StatusMovedPermanently, wantErr: true},
		{name: "400 terminal", status: http.StatusBadRequest, wantErr: true},
		{name: "401 terminal", status: http.StatusUnauthorized, wantErr: true},
		{name: "403 terminal", status: http.StatusForbidden, wantErr: true},
		{name: "404 terminal", status: http.StatusNotFound, wantErr: true},
		{name: "409 terminal", status: http.StatusConflict, wantErr: true},
		{name: "422 terminal", status: http.StatusUnprocessableEntity, wantErr: true},
		{name: "429 retryable", status: http.StatusTooManyRequests, wantErr: true, retryable: true},
		{name: "500 retryable", status: http.StatusInternalServerError, wantErr: true, retryable: true},
		{name: "502 retryable", status: http.StatusBadGateway, wantErr: true, retryable: true},
		{name: "503 retryable", status: http.StatusServiceUnavailable, wantErr: true, retryable: true},
		{name: "504 retryable", status: http.StatusGatewayTimeout, wantErr: true, retryable: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			// A 301 with no Location is not followed, so it reaches the sink.
			client := srv.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

			sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: srv.URL, Client: client})
			result, err := sink.Deliver(context.Background(), deliveryTestMessage(), 4)

			if result.StatusCode != tc.status {
				t.Fatalf("StatusCode = %d, want %d", result.StatusCode, tc.status)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Deliver: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("status %d accepted as success", tc.status)
			}
			if !errors.Is(err, ErrDeliveryStatus) {
				t.Fatalf("error does not wrap ErrDeliveryStatus: %v", err)
			}
			if got := DeliveryRetryable(err); got != tc.retryable {
				t.Fatalf("retryable = %v, want %v (%v)", got, tc.retryable, err)
			}

			var de *DeliveryError
			if !errors.As(err, &de) {
				t.Fatalf("error is not a *DeliveryError: %v", err)
			}
			if de.Attempt != 4 || de.StatusCode != tc.status {
				t.Fatalf("DeliveryError = %+v", de)
			}
		})
	}
}

func TestHTTPSinkTransportFailureIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := srv.Client()
	endpoint := srv.URL
	srv.Close() // nothing is listening any more

	sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: endpoint, Client: client, Timeout: 2 * time.Second})
	result, err := sink.Deliver(context.Background(), deliveryTestMessage(), 2)
	if err == nil {
		t.Fatal("delivery to a dead endpoint succeeded")
	}
	if !errors.Is(err, ErrDeliveryTransport) {
		t.Fatalf("error does not wrap ErrDeliveryTransport: %v", err)
	}
	if !DeliveryRetryable(err) {
		t.Fatalf("transport failure classified terminal: %v", err)
	}
	if result.StatusCode != 0 {
		t.Fatalf("StatusCode = %d, want 0 when no response arrived", result.StatusCode)
	}
	if result.Attempt != 2 {
		t.Fatalf("Attempt = %d, want 2", result.Attempt)
	}
}

// Every attempt is bounded. An application that accepts the connection and never
// answers must not hold the worker.
func TestHTTPSinkTimeoutIsRetryable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	sink := deliveryTestSink(t, HTTPSinkConfig{
		Endpoint: srv.URL,
		Client:   srv.Client(),
		Timeout:  75 * time.Millisecond,
	})

	start := time.Now()
	_, err := sink.Deliver(context.Background(), deliveryTestMessage(), 1)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a hung application produced a successful delivery")
	}
	if !errors.Is(err, ErrDeliveryTransport) {
		t.Fatalf("error does not wrap ErrDeliveryTransport: %v", err)
	}
	if !DeliveryRetryable(err) {
		t.Fatalf("timeout classified terminal: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("attempt took %v, the configured timeout did not bound it", elapsed)
	}
}

func TestHTTPSinkHonoursCallerCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: srv.URL, Client: srv.Client(), Timeout: time.Minute})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := sink.Deliver(ctx, deliveryTestMessage(), 1)
	if err == nil {
		t.Fatal("cancelled delivery reported success")
	}
	// Shutdown must not dead-letter in-flight work.
	if !DeliveryRetryable(err) {
		t.Fatalf("cancellation classified terminal: %v", err)
	}
}

func TestHTTPSinkLegacyRequiresExactACKBody(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{name: "exact ack", status: http.StatusOK, body: "ACK/Jasmin"},
		{name: "ack with surrounding whitespace", status: http.StatusOK, body: "\n ACK/Jasmin \r\n"},
		{name: "empty body", status: http.StatusOK, body: "", wantErr: true},
		{name: "plain ok", status: http.StatusOK, body: "OK", wantErr: true},
		{name: "wrong case", status: http.StatusOK, body: "ack/jasmin", wantErr: true},
		{name: "embedded ack", status: http.StatusOK, body: "prefix ACK/Jasmin", wantErr: true},
		{name: "json body", status: http.StatusOK, body: `{"accepted":true}`, wantErr: true},
		{name: "ack on 202", status: http.StatusAccepted, body: "ACK/Jasmin"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			sink := deliveryTestSink(t, HTTPSinkConfig{
				Endpoint: srv.URL,
				Format:   DeliveryFormatLegacy,
				Client:   srv.Client(),
			})

			_, err := sink.Deliver(context.Background(), deliveryTestMessage(), 1)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("body %q accepted as an acknowledgement", tc.body)
				}
				if !errors.Is(err, ErrDeliveryNotAcknowledged) {
					t.Fatalf("error does not wrap ErrDeliveryNotAcknowledged: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Deliver: %v", err)
			}
		})
	}
}

func TestHTTPSinkLegacySendsFormBody(t *testing.T) {
	capture := &deliveryCapture{
		responses: func(int) (int, string) { return http.StatusOK, "ACK/Jasmin" },
	}
	srv := httptest.NewServer(capture.handler(t))
	defer srv.Close()

	sink := deliveryTestSink(t, HTTPSinkConfig{
		Endpoint: srv.URL,
		Format:   DeliveryFormatLegacy,
		Secret:   []byte("per-connector-secret"),
		Client:   srv.Client(),
		Verdicts: PayloadVerdictFunc(func(context.Context, Message) (Verdict, bool) {
			return Verdict{Accept: false, Stat: "REJECTD", Err: "008"}, true
		}),
	})

	msg := deliveryTestMessage()
	if _, err := sink.Deliver(context.Background(), msg, 1); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	got := capture.all()[0]
	if got.mediaType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q", got.mediaType)
	}
	form, err := url.ParseQuery(string(got.body))
	if err != nil {
		t.Fatalf("body is not form-encoded: %v", err)
	}
	if form.Get("id") != msg.MessageID || form.Get("origin-connector") != msg.Connector {
		t.Fatalf("form = %v", form)
	}
	if form.Get("binary") != hex.EncodeToString(msg.Raw) {
		t.Fatalf("binary = %q", form.Get("binary"))
	}
	if form.Get("verdict") != "rejectd" {
		t.Fatalf("verdict = %q", form.Get("verdict"))
	}
	// The legacy body is signed exactly like the JSON one.
	mac := hmac.New(sha256.New, []byte("per-connector-secret"))
	mac.Write([]byte(got.timestamp + "." + string(got.body)))
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); got.signature != want {
		t.Fatal("legacy body signature does not verify over the received bytes")
	}
}

func TestHTTPSinkInlineVerdict(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantVerdict bool
		want        Verdict
	}{
		{
			name:        "explicit reject",
			body:        `{"accept": false, "stat": "UNDELIV", "err": "001"}`,
			wantVerdict: true,
			want:        Verdict{Accept: false, Stat: "UNDELIV", Err: "001", Reason: "application verdict (http-inline)"},
		},
		{
			name:        "accept with defaults derived from the decision",
			body:        `{"accept": true}`,
			wantVerdict: true,
			want:        Verdict{Accept: true, Stat: "DELIVRD", Err: "000", Reason: "application verdict (http-inline)"},
		},
		{
			name:        "reject with defaults derived from the decision",
			body:        `{"accept": false}`,
			wantVerdict: true,
			want:        Verdict{Accept: false, Stat: "REJECTD", Err: "008", Reason: "application verdict (http-inline)"},
		},
		{
			name:        "reason carried through",
			body:        `{"accept": false, "stat": "REJECTD", "err": "008", "reason": "no rental window"}`,
			wantVerdict: true,
			want:        Verdict{Accept: false, Stat: "REJECTD", Err: "008", Reason: "no rental window"},
		},
		{name: "empty body is no override", body: ""},
		{name: "whitespace body is no override", body: "  \n"},
		{name: "unparseable body is no override", body: "not json at all"},
		{name: "no accept field is no override", body: `{"stat": "UNDELIV"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: srv.URL, Inline: true, Client: srv.Client()})
			result, err := sink.Deliver(context.Background(), deliveryTestMessage(), 1)
			// A 2xx means the application holds the message; a body it could
			// not phrase properly must never re-deliver it.
			if err != nil {
				t.Fatalf("Deliver: %v", err)
			}
			if result.HasVerdict != tc.wantVerdict {
				t.Fatalf("HasVerdict = %v, want %v", result.HasVerdict, tc.wantVerdict)
			}
			if tc.wantVerdict {
				if result.Verdict != tc.want {
					t.Fatalf("Verdict = %+v, want %+v", result.Verdict, tc.want)
				}
				if result.Verdict.Delivered() != map[bool]int{true: 1, false: 0}[tc.want.Accept] {
					t.Fatalf("dlvrd disagrees with stat %q", result.Verdict.Stat)
				}
			}
		})
	}
}

// Outside http-inline mode the body is never a verdict, however it is phrased.
func TestHTTPSinkNonInlineIgnoresResponseVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"accept": false, "stat": "UNDELIV", "err": "001"}`)
	}))
	defer srv.Close()

	sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: srv.URL, Client: srv.Client()})
	result, err := sink.Deliver(context.Background(), deliveryTestMessage(), 1)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result.HasVerdict {
		t.Fatalf("a non-inline sink took a verdict from the response: %+v", result.Verdict)
	}
}

func TestNewHTTPSinkValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  HTTPSinkConfig
	}{
		{name: "no endpoint", cfg: HTTPSinkConfig{}},
		{name: "blank endpoint", cfg: HTTPSinkConfig{Endpoint: "   "}},
		{name: "no scheme", cfg: HTTPSinkConfig{Endpoint: "app.internal/messages"}},
		{name: "wrong scheme", cfg: HTTPSinkConfig{Endpoint: "ftp://app.internal/messages"}},
		{name: "no host", cfg: HTTPSinkConfig{Endpoint: "http:///messages"}},
		{name: "unknown format", cfg: HTTPSinkConfig{Endpoint: "https://app.internal/m", Format: "xml"}},
		{
			// The legacy contract requires the body to be exactly "ACK/Jasmin",
			// so a verdict has nowhere to travel. Failing at construction beats
			// a connector that silently ignores every rejection.
			name: "inline with the legacy contract",
			cfg:  HTTPSinkConfig{Endpoint: "https://app.internal/m", Format: DeliveryFormatLegacy, Inline: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHTTPSink(tc.cfg); err == nil {
				t.Fatal("configuration accepted")
			} else if !errors.Is(err, ErrDeliveryConfig) {
				t.Fatalf("error does not wrap ErrDeliveryConfig: %v", err)
			}
		})
	}
}

func TestNewHTTPSinkDefaults(t *testing.T) {
	sink, err := NewHTTPSink(HTTPSinkConfig{Endpoint: "https://app.internal/messages"})
	if err != nil {
		t.Fatalf("NewHTTPSink: %v", err)
	}
	if sink.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", sink.method)
	}
	if sink.format != DeliveryFormatJSON {
		t.Fatalf("format = %q, want json", sink.format)
	}
	if sink.timeout != deliveryDefaultTimeout {
		t.Fatalf("timeout = %v, want %v", sink.timeout, deliveryDefaultTimeout)
	}
	if sink.maxBody != deliveryMaxResponseBytes {
		t.Fatalf("maxBody = %d", sink.maxBody)
	}
	if sink.client == nil || sink.now == nil {
		t.Fatal("client or clock left nil")
	}
	if sink.Name() != "http-push" {
		t.Fatalf("Name = %q", sink.Name())
	}
}

func TestHTTPSinkCoercesAttemptToOneBased(t *testing.T) {
	capture := &deliveryCapture{}
	srv := httptest.NewServer(capture.handler(t))
	defer srv.Close()

	sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: srv.URL, Client: srv.Client()})
	result, err := sink.Deliver(context.Background(), deliveryTestMessage(), 0)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result.Attempt != 1 {
		t.Fatalf("Attempt = %d, want 1", result.Attempt)
	}
	if got := capture.all()[0].attempt; got != "1" {
		t.Fatalf("%s = %q, want 1", HeaderDeliveryAttempt, got)
	}
}

// This connector carries OTP bodies. An error string is the one place message
// content would reach a log file unredacted, so no failure path may embed the
// text, the raw bytes, or a response body that echoed them.
func TestHTTPSinkErrorsCarryNoMessageContent(t *testing.T) {
	msg := deliveryTestMessage()
	rawHex := hex.EncodeToString(msg.Raw)

	tests := []struct {
		name    string
		status  int
		body    string
		format  DeliveryFormat
		wantErr bool
	}{
		{name: "terminal status echoing the message", status: http.StatusBadRequest, body: msg.Text, wantErr: true},
		{name: "retryable status echoing the message", status: http.StatusInternalServerError, body: rawHex, wantErr: true},
		{name: "legacy non-ack echoing the message", status: http.StatusOK, body: msg.Text, format: DeliveryFormatLegacy, wantErr: true},
		{name: "legacy non-ack echoing the raw hex", status: http.StatusOK, body: rawHex, format: DeliveryFormatLegacy, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			format := tc.format
			if format == "" {
				format = DeliveryFormatJSON
			}
			sink := deliveryTestSink(t, HTTPSinkConfig{Endpoint: srv.URL, Format: format, Client: srv.Client()})

			_, err := sink.Deliver(context.Background(), msg, 1)
			if err == nil {
				t.Fatal("expected a failure")
			}
			rendered := err.Error()
			if strings.Contains(rendered, msg.Text) {
				t.Fatalf("error leaked message text: %s", rendered)
			}
			if strings.Contains(rendered, rawHex) {
				t.Fatalf("error leaked raw message bytes: %s", rendered)
			}
		})
	}
}

// The response body cap is what stops a misconfigured application from turning
// one delivery into an unbounded read.
func TestHTTPSinkCapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, strings.Repeat("x", 1<<20))
	}))
	defer srv.Close()

	sink := deliveryTestSink(t, HTTPSinkConfig{
		Endpoint:         srv.URL,
		Format:           DeliveryFormatLegacy,
		Client:           srv.Client(),
		MaxResponseBytes: 16,
	})

	_, err := sink.Deliver(context.Background(), deliveryTestMessage(), 1)
	if err == nil {
		t.Fatal("a megabyte of junk was accepted as an acknowledgement")
	}
	if !strings.Contains(err.Error(), "16 response bytes") {
		t.Fatalf("response was not capped: %v", err)
	}
}

func TestDeliveryRetryableClassifiesUnknownErrors(t *testing.T) {
	if DeliveryRetryable(nil) {
		t.Fatal("nil classified retryable")
	}
	// An unclassified failure is more likely a plumbing bug than a downstream
	// rejection, and losing a message is worse than delivering it twice.
	if !DeliveryRetryable(errors.New("something else")) {
		t.Fatal("unclassified error classified terminal")
	}
	if DeliveryRetryable(&DeliveryError{Retryable: false, Err: ErrDeliveryStatus}) {
		t.Fatal("terminal DeliveryError classified retryable")
	}
}
