// Package restcompat implements the authenticated JSON facade historically
// served by jasmin.protocols.rest. It deliberately delegates message
// validation, routing, billing and submission to the legacy-compatible HTTP
// handler: maintaining a second submit pipeline here would let the two public
// APIs disagree on security and charging rules.
package restcompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	jsonContentType = "application/json"
	maxJSONBody     = 4 << 20
	documentation   = "http://docs.jasminsms.com/en/latest/apis/rest/index.html"
	legacyRelease   = "0.11.1"
)

// NewHandler mounts the implemented /secure JSON resources in front of the
// supplied legacy-compatible HTTP handler. Non-REST paths are passed through
// unchanged, so adding the facade cannot alter /send, /balance, /rate, /ping,
// or /metrics.
//
// /secure/sendbatch is registered only when WithBatchContext is supplied,
// because its asynchronous work must have an explicit process lifetime.
func NewHandler(legacy http.Handler, options ...Option) http.Handler {
	handlers, err := NewHandlers(legacy, options...)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeAPIError(w, http.StatusServiceUnavailable, "REST API unavailable", err.Error(), "")
		})
	}
	return handlers.Combined
}

// Handlers exposes two views over one durable dispatcher: Combined preserves
// the legacy /ping while mounting /secure/* on the public HTTP listener;
// Daemon implements the historical standalone REST listener, including its
// JSON-wrapped /ping contract.
type Handlers struct {
	Combined   http.Handler
	Daemon     http.Handler
	dispatcher *batchDispatcher
}

// Close cancels in-flight task/callback work and waits until both workers have
// stopped. Pending or leased work remains durable for the next process.
func (handlers *Handlers) Close() {
	if handlers != nil && handlers.dispatcher != nil {
		handlers.dispatcher.close()
	}
}

// NewHandlers builds the combined and standalone REST views. Production uses
// the error-returning constructor so durable recovery failures stop startup.
func NewHandlers(legacy http.Handler, options ...Option) (Handlers, error) {
	if legacy == nil {
		legacy = http.NotFoundHandler()
	}
	settings := handlerOptions{
		callbackClient:      &http.Client{Timeout: 30 * time.Second},
		batchThroughput:     8,
		smartQoS:            true,
		batchStore:          NewMemoryBatchStore(),
		maxPending:          10000,
		maxAttempts:         3,
		retryDelay:          time.Second,
		callbackMaxAttempts: 5,
		callbackRetryDelay:  time.Second,
	}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	handler := &handler{legacy: legacy}
	if settings.batchContext != nil {
		var err error
		handler.batch, err = newBatchDispatcher(
			settings.batchContext,
			legacy,
			settings.callbackClient,
			settings.batchStore,
			settings.batchThroughput,
			settings.smartQoS,
			settings.maxPending,
			settings.maxAttempts,
			settings.retryDelay,
			settings.callbackMaxAttempts,
			settings.callbackRetryDelay,
		)
		if err != nil {
			return Handlers{}, err
		}
	}
	combined := http.NewServeMux()
	daemon := http.NewServeMux()
	for _, mux := range []*http.ServeMux{combined, daemon} {
		mux.HandleFunc("/secure/send", handler.send)
		if handler.batch != nil {
			mux.HandleFunc("/secure/sendbatch", handler.sendBatch)
		}
		mux.HandleFunc("/secure/balance", handler.balance)
		mux.HandleFunc("/secure/rate", handler.rate)
		mux.HandleFunc("/secure/", handler.notFound)
	}
	combined.Handle("/", legacy)
	daemon.HandleFunc("/ping", handler.ping)
	daemon.HandleFunc("/", handler.publicNotFound)
	return Handlers{Combined: combined, Daemon: daemon, dispatcher: handler.batch}, nil
}

/*
	The secure route list is intentionally shared by Combined and Daemon above;
	the only differing resource is /ping. Keeping two dispatchers would double
	the configured per-worker throughput and race callback recovery.
*/

type handler struct {
	legacy http.Handler
	batch  *batchDispatcher
}

type handlerOptions struct {
	batchContext        context.Context
	callbackClient      *http.Client
	batchStore          BatchStore
	batchThroughput     float64
	smartQoS            bool
	maxPending          int
	maxAttempts         int
	retryDelay          time.Duration
	callbackMaxAttempts int
	callbackRetryDelay  time.Duration
}

// Option customises the REST facade.
type Option func(*handlerOptions)

// WithBatchContext enables /secure/sendbatch and ties its asynchronous
// submissions and callbacks to the runtime lifetime. A nil context leaves the
// endpoint unregistered.
func WithBatchContext(ctx context.Context) Option {
	return func(options *handlerOptions) {
		options.batchContext = ctx
	}
}

// WithBatchQoS changes the per-worker batch dispatch ceiling. The frozen
// default is 8 messages/second with smart QoS enabled. A zero throughput and
// smart=false disables pacing.
func WithBatchQoS(throughput float64, smart bool) Option {
	return func(options *handlerOptions) {
		if throughput >= 0 {
			options.batchThroughput = throughput
		}
		options.smartQoS = smart
	}
}

// WithBatchStore enables durable admission and restart recovery. The gateway
// supplies PostgreSQL; tests may retain one MemoryBatchStore across handlers.
func WithBatchStore(store BatchStore) Option {
	return func(options *handlerOptions) {
		if store != nil {
			options.batchStore = store
		}
	}
}

// WithBatchLimits configures durable backlog, submit retry, and callback retry
// limits. Values must be positive; invalid values retain safe defaults.
func WithBatchLimits(
	maxPending int,
	maxAttempts int,
	retryDelay time.Duration,
	callbackMaxAttempts int,
	callbackRetryDelay time.Duration,
) Option {
	return func(options *handlerOptions) {
		if maxPending > 0 {
			options.maxPending = maxPending
		}
		if maxAttempts > 0 {
			options.maxAttempts = maxAttempts
		}
		if retryDelay >= 0 {
			options.retryDelay = retryDelay
		}
		if callbackMaxAttempts > 0 {
			options.callbackMaxAttempts = callbackMaxAttempts
		}
		if callbackRetryDelay >= 0 {
			options.callbackRetryDelay = callbackRetryDelay
		}
	}
}

// WithCallbackClient supplies the client used for sendbatch callback_url and
// errback_url requests. It primarily exists for deterministic tests.
func WithCallbackClient(client *http.Client) Option {
	return func(options *handlerOptions) {
		if client != nil {
			options.callbackClient = client
		}
	}
}

type apiError struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Link        *apiLink `json:"link,omitempty"`
}

type apiLink struct {
	Text string `json:"text"`
	Href string `json:"href"`
	Rel  string `json:"rel"`
}

type dataEnvelope struct {
	Data any `json:"data"`
}

type messageEnvelope struct {
	Message string `json:"message"`
}

func (h *handler) send(w http.ResponseWriter, request *http.Request) {
	h.secure(w, request, http.MethodPost, func(username, password string) {
		payload, err := decodeObject(request.Body)
		if err != nil {
			writeAPIError(w, http.StatusPreconditionFailed, "Cannot parse JSON data",
				"Got unparseable json data: "+err.Error(), "")
			return
		}

		translated := translatePayload(payload, username, password)
		body, err := json.Marshal(translated)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "Cannot encode JSON data", err.Error(), "")
			return
		}
		upstream := request.Clone(request.Context())
		upstream.Method = http.MethodPost
		upstream.URL = cloneURL(request.URL)
		upstream.URL.Path = "/send"
		upstream.URL.RawQuery = ""
		upstream.RequestURI = "/send"
		upstream.Body = io.NopCloser(bytes.NewReader(body))
		upstream.ContentLength = int64(len(body))
		upstream.Header = request.Header.Clone()
		upstream.Header.Set("Content-Type", jsonContentType)
		h.proxy(w, upstream)
	})
}

func (h *handler) sendBatch(w http.ResponseWriter, request *http.Request) {
	h.secure(w, request, http.MethodPost, func(username, password string) {
		// The frozen facade authenticates a batch through /balance before it
		// parses or acknowledges any jobs.
		if err := h.authenticateBatch(request, username, password); err != nil {
			writeAPIError(w, http.StatusPreconditionFailed, "Authentication failed",
				fmt.Sprintf("Authentication failed for user: %s", username), "")
			return
		}
		payload, err := decodeObject(request.Body)
		if err != nil {
			writeAPIError(w, http.StatusPreconditionFailed, "Cannot parse JSON data",
				"Got unparseable json data: "+err.Error(), "")
			return
		}
		batch, buildErr := buildBatch(payload, username, password, h.batch.maxPending)
		if buildErr != nil {
			status := buildErr.status
			if status == 0 {
				status = http.StatusPreconditionFailed
			}
			writeAPIError(w, status, buildErr.title, buildErr.description, "")
			return
		}
		if err := h.batch.dispatch(batch); err != nil {
			if errors.Is(err, ErrBatchQueueFull) {
				writeAPIError(w, http.StatusTooManyRequests, "Batch queue is full",
					"The durable sendbatch backlog has reached its configured limit.", "")
				return
			}
			writeAPIError(w, http.StatusServiceUnavailable, "Cannot persist batch", err.Error(), "")
			return
		}
		data := map[string]any{
			"batchId":      batch.id,
			"messageCount": len(batch.tasks),
		}
		if batch.delay > 0 {
			data["scheduled"] = formatDelay(batch.delay)
		}
		writeJSON(w, http.StatusOK, dataEnvelope{Data: data})
	})
}

func (h *handler) ping(w http.ResponseWriter, request *http.Request) {
	if !acceptsJSON(request.Header.Get("Accept")) {
		writeAPIError(w, http.StatusUnsupportedMediaType, "Unsupported media type",
			"This API supports JSON media type only.", documentation)
		return
	}
	if request.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet+", OPTIONS")
		writeAPIError(w, http.StatusMethodNotAllowed, "405 Method Not Allowed", "", "")
		return
	}
	upstream := request.Clone(request.Context())
	upstream.Method = http.MethodGet
	upstream.URL = &url.URL{Path: "/ping"}
	upstream.RequestURI = "/ping"
	h.proxy(w, upstream)
}

func (h *handler) publicNotFound(w http.ResponseWriter, request *http.Request) {
	if !acceptsJSON(request.Header.Get("Accept")) {
		writeAPIError(w, http.StatusUnsupportedMediaType, "Unsupported media type",
			"This API supports JSON media type only.", documentation)
		return
	}
	writeAPIError(w, http.StatusNotFound, "Resource not found",
		fmt.Sprintf("No REST resource is registered for %s.", request.URL.Path), "")
}

func (h *handler) authenticateBatch(request *http.Request, username, password string) error {
	values := make(url.Values, 2)
	values.Set("username", username)
	values.Set("password", password)
	upstream := request.Clone(request.Context())
	upstream.Method = http.MethodGet
	upstream.URL = &url.URL{Path: "/balance", RawQuery: values.Encode()}
	upstream.RequestURI = upstream.URL.RequestURI()
	// Clone retains the sendbatch POST body and Content-Type. The real legacy
	// handler selects JSON parsing from that header, so without clearing both
	// it decodes the batch document instead of the credential query and reports
	// a false authentication failure. This probe is a distinct bodyless GET.
	upstream.Body = http.NoBody
	upstream.GetBody = nil
	upstream.ContentLength = 0
	upstream.Form = nil
	upstream.PostForm = nil
	upstream.MultipartForm = nil
	upstream.Header = request.Header.Clone()
	upstream.Header.Del("Content-Type")
	upstream.Header.Del("Content-Length")
	response := newCapture()
	h.legacy.ServeHTTP(response, upstream)
	if response.statusCode() != http.StatusOK {
		return fmt.Errorf("balance returned %d", response.statusCode())
	}
	return nil
}

func (h *handler) balance(w http.ResponseWriter, request *http.Request) {
	h.secure(w, request, http.MethodGet, func(username, password string) {
		values := make(url.Values, 2)
		values.Set("username", username)
		values.Set("password", password)
		upstream := request.Clone(request.Context())
		upstream.Method = http.MethodGet
		upstream.URL = cloneURL(request.URL)
		upstream.URL.Path = "/balance"
		upstream.URL.RawQuery = values.Encode()
		upstream.RequestURI = upstream.URL.RequestURI()
		h.proxy(w, upstream)
	})
}

func (h *handler) rate(w http.ResponseWriter, request *http.Request) {
	h.secure(w, request, http.MethodGet, func(username, password string) {
		values := make(url.Values, len(request.URL.Query())+2)
		for key, items := range request.URL.Query() {
			key = strings.ReplaceAll(key, "_", "-")
			for _, item := range items {
				values.Add(key, item)
			}
		}
		values.Set("username", username)
		values.Set("password", password)
		upstream := request.Clone(request.Context())
		upstream.Method = http.MethodGet
		upstream.URL = cloneURL(request.URL)
		upstream.URL.Path = "/rate"
		upstream.URL.RawQuery = values.Encode()
		upstream.RequestURI = upstream.URL.RequestURI()
		h.proxy(w, upstream)
	})
}

func (h *handler) notFound(w http.ResponseWriter, request *http.Request) {
	if !acceptsJSON(request.Header.Get("Accept")) {
		writeAPIError(w, http.StatusUnsupportedMediaType, "Unsupported media type",
			"This API supports JSON media type only.", documentation)
		return
	}
	if _, _, ok := basicCredentials(w, request); !ok {
		return
	}
	writeAPIError(w, http.StatusNotFound, "Resource not found",
		fmt.Sprintf("No REST resource is registered for %s.", request.URL.Path), "")
}

func (h *handler) secure(
	w http.ResponseWriter,
	request *http.Request,
	method string,
	next func(username, password string),
) {
	// Falcon's request middleware checks response negotiation before the auth
	// middleware and resource method.
	if !acceptsJSON(request.Header.Get("Accept")) {
		writeAPIError(w, http.StatusUnsupportedMediaType, "Unsupported media type",
			"This API supports JSON media type only.", documentation)
		return
	}
	username, password, ok := basicCredentials(w, request)
	if !ok {
		return
	}
	if request.Method != method {
		if request.Method == http.MethodOptions {
			w.Header().Set("Allow", method)
			writeJSON(w, http.StatusOK, nil)
			return
		}
		w.Header().Set("Allow", method+", OPTIONS")
		writeAPIError(w, http.StatusMethodNotAllowed, "405 Method Not Allowed", "", "")
		return
	}
	next(username, password)
}

func (h *handler) proxy(w http.ResponseWriter, request *http.Request) {
	response := newCapture()
	h.legacy.ServeHTTP(response, request)

	body := response.body.String()
	if response.statusCode() != http.StatusOK {
		writeJSON(w, response.statusCode(), messageEnvelope{Message: body})
		return
	}

	var data any = body
	if strings.Contains(body, "{") {
		var decoded any
		if err := json.Unmarshal([]byte(body), &decoded); err == nil {
			data = decoded
		}
	}
	writeJSON(w, http.StatusOK, dataEnvelope{Data: data})
}

func basicCredentials(w http.ResponseWriter, request *http.Request) (string, string, bool) {
	header := request.Header.Get("Authorization")
	if header == "" {
		writeAPIError(w, http.StatusUnauthorized, "Authentication required",
			"Please provide a valid Basic auth token", documentation)
		return "", "", false
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Basic") || token == "" || strings.Contains(token, " ") {
		writeAPIError(w, http.StatusUnauthorized, "Invalid token",
			"Please provide a valid Basic auth token", documentation)
		return "", "", false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(token)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "Invalid token: "+err.Error(),
			"Please provide a valid Basic auth token", documentation)
		return "", "", false
	}
	username, password, found := strings.Cut(string(decoded), ":")
	if !found {
		writeAPIError(w, http.StatusUnauthorized, "Invalid token",
			"Please provide a valid Basic auth token", documentation)
		return "", "", false
	}
	return username, password, true
}

func decodeObject(body io.Reader) (map[string]json.RawMessage, error) {
	limited := &io.LimitedReader{R: body, N: maxJSONBody + 1}
	decoder := json.NewDecoder(limited)
	var payload map[string]json.RawMessage
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, fmt.Errorf("JSON body must be an object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON content")
		}
		return nil, err
	}
	if limited.N <= 0 {
		return nil, fmt.Errorf("JSON body exceeds %d bytes", maxJSONBody)
	}
	return payload, nil
}

func acceptsJSON(header string) bool {
	if strings.TrimSpace(header) == "" {
		return true
	}
	for _, item := range strings.Split(header, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err != nil {
			continue
		}
		if quality, found := parameters["q"]; found {
			parsed, err := strconv.ParseFloat(quality, 64)
			if err != nil || parsed <= 0 {
				continue
			}
		}
		if mediaType == "*/*" || mediaType == "application/*" || strings.EqualFold(mediaType, jsonContentType) ||
			strings.HasSuffix(strings.ToLower(mediaType), "+json") {
			return true
		}
	}
	return false
}

func writeAPIError(w http.ResponseWriter, status int, title, description, link string) {
	var errorLink *apiLink
	if link != "" {
		errorLink = &apiLink{
			Text: "Documentation related to this error",
			Href: link,
			Rel:  "help",
		}
	}
	writeJSON(w, status, apiError{Title: title, Description: description, Link: errorLink})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	restHeaders(w)
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"title":"500 Internal Server Error"}`)
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func restHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set("Powered-By", "Jasmin "+legacyRelease)
	w.Header().Add("Vary", "Accept")
}

func translatePayload(payload map[string]json.RawMessage, username, password string) map[string]json.RawMessage {
	translated := make(map[string]json.RawMessage, len(payload)+2)
	for key, value := range payload {
		// custom_tlvs is the one underscored name understood directly by the
		// HTTP JSON front door. Its RawMessage retains object order, which is
		// also TLV wire order.
		if key != "custom_tlvs" {
			key = strings.ReplaceAll(key, "_", "-")
		}
		translated[key] = value
	}
	// Basic Auth is authoritative even if a caller smuggles credentials in the
	// JSON document.
	translated["username"] = mustJSON(username)
	translated["password"] = mustJSON(password)
	return translated
}

func mustJSON(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func cloneURL(source *url.URL) *url.URL {
	if source == nil {
		return &url.URL{}
	}
	result := *source
	return &result
}

type captureWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newCapture() *captureWriter {
	return &captureWriter{header: make(http.Header)}
}

func (writer *captureWriter) Header() http.Header {
	return writer.header
}

func (writer *captureWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *captureWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.body.Write(body)
}

func (writer *captureWriter) statusCode() int {
	if writer.status == 0 {
		return http.StatusOK
	}
	return writer.status
}
