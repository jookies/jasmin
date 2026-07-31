package termination

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Delivery request headers. They are part of the wire contract; the receiver
// verifies the signature over the timestamp and body and dedupes on the message
// id, so these spellings cannot change without breaking every integration.
const (
	// HeaderDeliveryMessageID carries the gateway message id. It is the
	// idempotency key: every retry of the same message repeats it unchanged, so
	// a delivery that timed out after the application had already committed it
	// is recognisable as a duplicate rather than a second message.
	HeaderDeliveryMessageID = "X-Synevyr-Message-Id"
	// HeaderDeliveryAttempt is the 1-based attempt number.
	HeaderDeliveryAttempt = "X-Synevyr-Attempt"
	// HeaderDeliveryTimestamp is Unix seconds, signed alongside the body so a
	// captured request cannot be replayed indefinitely.
	HeaderDeliveryTimestamp = "X-Synevyr-Timestamp"
	// HeaderDeliverySignature is "sha256=" plus the hex HMAC.
	HeaderDeliverySignature = "X-Synevyr-Signature"
)

// deliveryUserAgent identifies this sink in a downstream application's access
// log, distinctly from the MO thrower's "Jasmin gateway/1.0
// deliverSmHttpThrower" so the two paths are separable there.
const deliveryUserAgent = "Synevyr gateway/1.0 terminationDeliverySink"

// deliveryDefaultTimeout bounds one delivery attempt when the connector does
// not configure its own. Every attempt is bounded: an application that accepts
// a connection and never answers would otherwise hold a worker slot forever,
// which at the plan's 200 msg/s target stalls the connector rather than the
// message.
const deliveryDefaultTimeout = 10 * time.Second

// deliveryMaxResponseBytes caps how much of a response body is read, matching
// the MO thrower's limit. Only the first 4 KiB can influence the outcome: an
// application answering with a megabyte of HTML is a misconfiguration, not a
// verdict.
const deliveryMaxResponseBytes = 4 << 10

// deliveryACKBody is the exact body the legacy Jasmin contract requires,
// matching the check at internal/core/mo/http_thrower.go:122.
const deliveryACKBody = "ACK/Jasmin"

// Delivery failure classes. Every failure returned by HTTPSink.Deliver wraps
// exactly one of these, so a caller can distinguish "the request never landed"
// from "the application refused the payload" without parsing a message.
var (
	// ErrDeliveryTransport reports a request that never produced a response:
	// DNS, dial, TLS, connection reset, or the attempt timeout expiring.
	ErrDeliveryTransport = errors.New("termination: delivery request failed")
	// ErrDeliveryStatus reports a response whose status is not 2xx.
	ErrDeliveryStatus = errors.New("termination: delivery returned HTTP error status")
	// ErrDeliveryNotAcknowledged reports a legacy-mode 2xx whose body was not
	// exactly "ACK/Jasmin".
	ErrDeliveryNotAcknowledged = errors.New("termination: delivery did not reply ACK/Jasmin")
	// ErrDeliveryResponseBody reports a response that could not be read to
	// completion.
	ErrDeliveryResponseBody = errors.New("termination: delivery response unreadable")
	// ErrDeliveryConfig reports a sink that cannot be constructed.
	ErrDeliveryConfig = errors.New("termination: delivery sink misconfigured")
	// ErrDeliveryNotConfigured reports a row belonging to a connector that has
	// no push endpoint -- the pull-only case. It is NOT a failure: the runner
	// must leave the row pending without counting an attempt, because counting
	// attempts nobody made would dead-letter every row of a pull-only connector
	// once the retry budget ran out.
	ErrDeliveryNotConfigured = errors.New("termination: connector has no delivery endpoint")
)

// DeliveryFormat selects the body shape a connector's application expects.
type DeliveryFormat string

const (
	// DeliveryFormatJSON is the default for a new integration: a JSON body,
	// success is any 2xx, no body requirement.
	DeliveryFormatJSON DeliveryFormat = "json"
	// DeliveryFormatLegacy is the Jasmin MO shape: form-encoded body, and
	// success additionally requires a body of exactly "ACK/Jasmin".
	DeliveryFormatLegacy DeliveryFormat = "legacy"
)

// DeliveryDoer is the minimal HTTP client the sink needs; *http.Client
// satisfies it.
type DeliveryDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// DeliveryError describes one failed delivery attempt and, crucially, whether
// repeating it could ever succeed.
//
// It deliberately carries no part of the message and no part of the response
// body: the content this connector delivers is OTP text, and an error string is
// the one place it would reach a log file unredacted.
type DeliveryError struct {
	// Attempt is the 1-based attempt this failure came from.
	Attempt int
	// StatusCode is the HTTP status observed, 0 when no response arrived.
	StatusCode int
	// Retryable reports whether the caller should schedule another attempt.
	// False means the failure is terminal and the message belongs in the
	// dead-letter queue now: re-POSTing a body the application has rejected
	// wastes the retry budget and is how one poison message becomes an outage.
	Retryable bool
	// Err is the underlying failure, wrapping one of the Err* sentinels.
	Err error
}

// Error implements error.
func (e *DeliveryError) Error() string {
	retry := "terminal"
	if e.Retryable {
		retry = "retryable"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("termination: delivery attempt %d failed (%s, status %d): %v",
			e.Attempt, retry, e.StatusCode, e.Err)
	}
	return fmt.Sprintf("termination: delivery attempt %d failed (%s): %v", e.Attempt, retry, e.Err)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *DeliveryError) Unwrap() error { return e.Err }

// DeliveryRetryable reports whether err describes a failure worth another
// attempt. An error that is not a *DeliveryError is treated as retryable,
// because an unclassified failure is more likely a bug in the caller's plumbing
// than a downstream rejection, and losing a message is worse than delivering it
// twice.
func DeliveryRetryable(err error) bool {
	if err == nil {
		return false
	}
	var de *DeliveryError
	if errors.As(err, &de) {
		return de.Retryable
	}
	return true
}

// HTTPSinkConfig configures one termination connector's downstream endpoint.
type HTTPSinkConfig struct {
	// Endpoint is the absolute http/https URL to POST to. Required.
	Endpoint string
	// Method defaults to POST.
	Method string
	// Format defaults to DeliveryFormatJSON.
	Format DeliveryFormat
	// Secret is the per-connector HMAC key. When empty the signature header is
	// omitted entirely rather than sent keyed by nothing, so an application can
	// tell "this gateway does not sign" from "this signature does not verify".
	Secret []byte
	// Inline enables http-inline mode: the response body is parsed for a
	// verdict that overrides the one already decided. In every other mode the
	// body is never inspected for a verdict, and DeliveryResult.HasVerdict is
	// always false. Mode selection itself belongs to the connector worker; this
	// is only the flag it sets.
	Inline bool
	// Timeout bounds a single attempt. Defaults to deliveryDefaultTimeout.
	Timeout time.Duration
	// Client defaults to an *http.Client bounded by the same timeout.
	Client DeliveryDoer
	// Verdicts supplies the payload's verdict field. Optional: with no lookup
	// the field is sent empty, which is correct for http-inline mode where the
	// verdict does not exist yet.
	Verdicts PayloadVerdictLookup
	// MaxResponseBytes defaults to deliveryMaxResponseBytes.
	MaxResponseBytes int
	// Now defaults to time.Now; it exists so the signed timestamp is
	// deterministic under test.
	Now func() time.Time
	// UserAgent defaults to deliveryUserAgent.
	UserAgent string
}

// HTTPSink delivers a decoded message to a downstream application over HTTP.
//
// It performs exactly one attempt per Deliver call and owns nothing beyond it:
// retry scheduling, backoff, the dead-letter queue and the spool row's delivery
// state are the caller's, which is what lets a failed attempt be retried from a
// durable record instead of from a goroutine that a restart would lose.
//
// It is safe for concurrent use.
type HTTPSink struct {
	endpoint  string
	method    string
	format    DeliveryFormat
	secret    []byte
	inline    bool
	timeout   time.Duration
	client    DeliveryDoer
	verdicts  PayloadVerdictLookup
	maxBody   int
	now       func() time.Time
	userAgent string
}

var _ DeliverySink = (*HTTPSink)(nil)

// NewHTTPSink validates a connector's delivery configuration and builds the
// sink.
func NewHTTPSink(cfg HTTPSinkConfig) (*HTTPSink, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("%w: endpoint is required", ErrDeliveryConfig)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: endpoint: %w", ErrDeliveryConfig, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: endpoint scheme %q is not http or https", ErrDeliveryConfig, parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: endpoint has no host", ErrDeliveryConfig)
	}

	format := cfg.Format
	if format == "" {
		format = DeliveryFormatJSON
	}
	if format != DeliveryFormatJSON && format != DeliveryFormatLegacy {
		return nil, fmt.Errorf("%w: unknown delivery format %q", ErrDeliveryConfig, format)
	}
	if cfg.Inline && format == DeliveryFormatLegacy {
		// The legacy contract requires the body to be exactly "ACK/Jasmin",
		// which leaves nowhere for a verdict to travel. Refusing the pair here
		// is the difference between a startup error and a connector that
		// silently never honours the application's rejections.
		return nil, fmt.Errorf("%w: http-inline needs a JSON body, not the legacy ACK/Jasmin contract", ErrDeliveryConfig)
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = deliveryDefaultTimeout
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	maxBody := cfg.MaxResponseBytes
	if maxBody <= 0 {
		maxBody = deliveryMaxResponseBytes
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	agent := cfg.UserAgent
	if agent == "" {
		agent = deliveryUserAgent
	}

	return &HTTPSink{
		endpoint:  endpoint,
		method:    method,
		format:    format,
		secret:    cfg.Secret,
		inline:    cfg.Inline,
		timeout:   timeout,
		client:    client,
		verdicts:  cfg.Verdicts,
		maxBody:   maxBody,
		now:       now,
		userAgent: agent,
	}, nil
}

// Name implements DeliverySink.
func (s *HTTPSink) Name() string { return "http-push" }

// Deliver performs one attempt.
//
// A nil error means the application accepted the message: any 2xx in JSON mode,
// or a 2xx whose body trims to exactly "ACK/Jasmin" in legacy mode. A non-nil
// error is always a *DeliveryError; DeliveryRetryable reports whether the
// caller should schedule another attempt or dead-letter the message.
func (s *HTTPSink) Deliver(ctx context.Context, msg Message, attempt int) (DeliveryResult, error) {
	if attempt < 1 {
		attempt = 1
	}
	result := DeliveryResult{Attempt: attempt}

	body, contentType, accept, err := s.encode(ctx, msg)
	if err != nil {
		// A body that cannot be encoded will not encode on the next attempt
		// either.
		return result, &DeliveryError{Attempt: attempt, Retryable: false, Err: err}
	}

	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, s.method, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return result, &DeliveryError{Attempt: attempt, Retryable: false, Err: fmt.Errorf("%w: build request: %w", ErrDeliveryConfig, err)}
	}

	timestamp := deliveryTimestamp(s.now())
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set(HeaderDeliveryMessageID, msg.MessageID)
	req.Header.Set(HeaderDeliveryAttempt, strconv.Itoa(attempt))
	req.Header.Set(HeaderDeliveryTimestamp, timestamp)
	if len(s.secret) > 0 {
		// Signed over the exact bytes in the request body, never a second
		// marshalling of the payload.
		req.Header.Set(HeaderDeliverySignature, DeliverySignature(s.secret, timestamp, body))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		// Everything that prevents a response — dial, TLS, reset, the attempt
		// timeout, a cancelled parent context during shutdown — is transient
		// from the message's point of view.
		return result, &DeliveryError{Attempt: attempt, Retryable: true, Err: fmt.Errorf("%w: %w", ErrDeliveryTransport, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	result.StatusCode = resp.StatusCode

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(s.maxBody)))
	if readErr != nil {
		return result, &DeliveryError{
			Attempt:    attempt,
			StatusCode: resp.StatusCode,
			Retryable:  true,
			Err:        fmt.Errorf("%w: %w", ErrDeliveryResponseBody, readErr),
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return result, &DeliveryError{
			Attempt:    attempt,
			StatusCode: resp.StatusCode,
			Retryable:  deliveryStatusRetryable(resp.StatusCode),
			Err:        fmt.Errorf("%w: %d", ErrDeliveryStatus, resp.StatusCode),
		}
	}

	if s.format == DeliveryFormatLegacy {
		// Identical rule to internal/core/mo/http_thrower.go:122 — trimmed body
		// equal to "ACK/Jasmin". The body itself is never quoted into the
		// error, unlike the MO thrower: an application that echoes the request
		// would put OTP text in a log line.
		if strings.TrimSpace(string(respBody)) != deliveryACKBody {
			return result, &DeliveryError{
				Attempt:    attempt,
				StatusCode: resp.StatusCode,
				// A 2xx with the wrong body is a half-deployed or proxied
				// handler far more often than a rejection of this payload, and
				// a permanently wrong body still ends in the dead-letter queue
				// once the retry budget is spent. Nothing is dropped either way.
				Retryable: true,
				Err:       fmt.Errorf("%w: %d response bytes", ErrDeliveryNotAcknowledged, len(respBody)),
			}
		}
		return result, nil
	}

	if s.inline {
		if verdict, ok := parseDeliveryInlineVerdict(respBody); ok {
			result.Verdict = verdict
			result.HasVerdict = true
		}
	}
	return result, nil
}

// encode renders the request body and its content negotiation headers.
func (s *HTTPSink) encode(ctx context.Context, msg Message) (body []byte, contentType, accept string, err error) {
	verdict, hasVerdict := s.verdict(ctx, msg)
	if s.format == DeliveryFormatLegacy {
		form := BuildLegacyDeliveryForm(msg, verdict, hasVerdict)
		return []byte(form.Encode()), "application/x-www-form-urlencoded", "text/plain", nil
	}
	encoded, err := MarshalDeliveryPayload(BuildDeliveryPayload(msg, verdict, hasVerdict))
	if err != nil {
		return nil, "", "", fmt.Errorf("termination: encode delivery payload: %w", err)
	}
	return encoded, "application/json", "application/json", nil
}

func (s *HTTPSink) verdict(ctx context.Context, msg Message) (Verdict, bool) {
	if s.verdicts == nil {
		return Verdict{}, false
	}
	return s.verdicts.VerdictFor(ctx, msg)
}

// deliveryStatusRetryable classifies a non-2xx response.
//
// 408, 425, 429 and 5xx say "not now"; every other 4xx says "not this payload",
// and repeating a payload the application has rejected burns the retry budget
// without any path to success — the message belongs in the dead-letter queue
// where it can be inspected and replayed after a fix. 3xx is treated as
// terminal for the same reason: the client already follows redirects, so a 3xx
// reaching here is a misconfigured endpoint, not a transient condition.
//
// 408 and 425 are exceptions to the 4xx rule on purpose. Neither is a statement
// about the payload: a 408 is almost always a load balancer giving up on a slow
// backend, and a 425 explicitly asks the caller to try again. Dead-lettering an
// OTP because a proxy was busy would be an outage dressed as a policy decision.
func deliveryStatusRetryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return status >= 500 && status <= 599
}

// deliveryInlineResponse is the http-inline response shape from the delivery
// contract: {"accept": false, "stat": "UNDELIV", "err": "001"}.
type deliveryInlineResponse struct {
	Accept *bool  `json:"accept"`
	Stat   string `json:"stat"`
	Err    string `json:"err"`
	Reason string `json:"reason"`
}

// parseDeliveryInlineVerdict extracts an http-inline verdict from a delivery
// response.
//
// It reports false — no override — for an empty body, an unparseable body, or a
// body that names no accept decision. That is deliberate: the application
// answered 2xx, so the message was delivered, and turning a malformed response
// into a delivery failure would re-POST a message the application already holds.
// The caller keeps whatever verdict it had.
func parseDeliveryInlineVerdict(body []byte) (Verdict, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return Verdict{}, false
	}
	var parsed deliveryInlineResponse
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return Verdict{}, false
	}
	if parsed.Accept == nil {
		return Verdict{}, false
	}

	verdict := Verdict{
		Accept: *parsed.Accept,
		Stat:   strings.ToUpper(strings.TrimSpace(parsed.Stat)),
		Err:    strings.TrimSpace(parsed.Err),
		Reason: strings.TrimSpace(parsed.Reason),
	}
	// Defaults derived from the decision, never hardcoded independently of it:
	// the receipt bugs this plan exists to fix are all a stat and a dlvrd/err
	// that disagree.
	if verdict.Stat == "" {
		if verdict.Accept {
			verdict.Stat = "DELIVRD"
		} else {
			verdict.Stat = "REJECTD"
		}
	}
	if verdict.Err == "" {
		if verdict.Accept {
			verdict.Err = "000"
		} else {
			verdict.Err = "008"
		}
	}
	if verdict.Reason == "" {
		verdict.Reason = "application verdict (http-inline)"
	}
	return verdict, true
}
