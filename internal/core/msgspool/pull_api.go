package msgspool

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The cursor-based pull API: the counterpart to the HTTP delivery sink, for a
// downstream application that fetches decoded messages instead of (or as well
// as) having them pushed.
//
// Two properties are the whole design and neither is negotiable:
//
//   - Paging is by cursor, never by a time window. A "now - N seconds" query
//     loses messages: a retried delivery re-enters with a received_at the
//     consumer has already passed, and one missed poll becomes a permanent
//     hole. received_from/received_to exist as a convenience for a backfill or
//     an investigation; they are not how a consumer advances.
//   - The credential is a scoped, read-only consumer token, never the admin
//     token. Its scope is compiled into SQL by ConsumerService.Page, so a
//     consumer's pages and its cursor arithmetic are dense over its own traffic
//     and carry no signal about anyone else's.

// pullTimeFormat matches the delivery contract's received_at rendering: RFC 3339
// with a fixed three-digit fraction, so the same message serializes identically
// whether it was pushed or pulled.
const pullTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// PullMessage is one spooled message as the pull API renders it.
//
// The shared field names are the same wire contract as the push delivery
// payload (see the "Delivery contract" section of plan 021 and
// termination.DeliveryPayload), so an application already parsing pushed
// deliveries reuses its parser. A test in internal/core/termination asserts the
// two shapes have not drifted; this package cannot import that one.
//
// Text and RawHex are pointers for one reason. A consumer whose scope excludes
// content must see the fields *absent*, not null and not empty: an empty string
// is a legitimate message body, and a client that could not tell the two apart
// would silently treat every redacted OTP as a blank SMS.
type PullMessage struct {
	MessageID string `json:"message_id"`
	Connector string `json:"connector"`
	Partner   string `json:"partner"`
	From      string `json:"from"`
	To        string `json:"to"`
	// Text is the decoded message, parts already joined. Absent unless the
	// consumer's scope sets include_text.
	Text *string `json:"text,omitempty"`
	// RawHex is the pre-decode payload, all parts concatenated, lowercase hex.
	// Absent unless the consumer's scope sets include_text.
	RawHex     *string `json:"raw_hex,omitempty"`
	DataCoding byte    `json:"dcs"`
	Encoding   string  `json:"encoding"`
	Parts      int     `json:"parts"`
	ReceivedAt string  `json:"received_at"`
	// Verdict is what the partner was told, lowercased ("delivrd", "rejectd").
	Verdict string `json:"verdict"`
	// DeliveryState and DeliveryAttempts describe the *push* lifecycle, which a
	// pull-only connector never advances: its rows stay "pending" for their
	// whole retention. They are reported so a mixed deployment can tell a
	// message it already received by push from one it has not.
	DeliveryState    DeliveryState `json:"delivery_state"`
	DeliveryAttempts int           `json:"delivery_attempts"`
}

// PullPage is the response body.
type PullPage struct {
	Messages []PullMessage `json:"messages"`
	// NextCursor is what to pass as ?after= on the next poll. It is empty only
	// when the page was empty, in which case the consumer keeps its previous
	// cursor — the position must never reset to the head of the spool.
	NextCursor string `json:"next_cursor,omitempty"`
}

// NewPullMessage projects a stored record.
//
// Content is carried only when the record actually holds it. Record.
// ContentRedacted is the authority, not the caller's intent: the masking
// happened in the SQL projection, so a redacted row genuinely has no text in
// this process to leak.
func NewPullMessage(record Record) PullMessage {
	message := PullMessage{
		MessageID:        record.MessageID,
		Connector:        record.ConnectorID,
		Partner:          record.UserID,
		From:             record.SourceAddr,
		To:               record.DestAddr,
		DataCoding:       record.DataCoding,
		Encoding:         record.Encoding,
		Parts:            record.Parts,
		ReceivedAt:       record.ReceivedAt.UTC().Format(pullTimeFormat),
		Verdict:          strings.ToLower(strings.TrimSpace(record.Verdict.Stat)),
		DeliveryState:    record.DeliveryState,
		DeliveryAttempts: record.DeliveryAttempts,
	}
	if !record.ContentRedacted {
		text := record.Text
		raw := hex.EncodeToString(record.Raw)
		message.Text = &text
		message.RawHex = &raw
	}
	return message
}

// PullHandler serves GET /messages on the existing REST listener.
//
// It is mounted rather than given a port of its own: a second listener is a
// second thing to firewall, and this endpoint has exactly the audience the REST
// API already has.
type PullHandler struct {
	consumers *ConsumerService
}

// NewPullHandler builds the endpoint.
func NewPullHandler(consumers *ConsumerService) (*PullHandler, error) {
	if consumers == nil {
		return nil, errors.New("msgspool: pull handler requires a consumer service")
	}
	return &PullHandler{consumers: consumers}, nil
}

func (handler *PullHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// OTP bodies must not be held by an intermediary, and this response is
	// per-credential regardless.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writePullError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	token, ok := bearerToken(r)
	if !ok {
		// Authenticate is still called with an empty token so the attempt is
		// audited: an unauthenticated probe of this endpoint is exactly as
		// interesting as one with a wrong token.
		token = ""
	}
	consumer, err := handler.consumers.Authenticate(r.Context(), token)
	if err != nil {
		writePullFailure(w, err)
		return
	}

	request, err := parsePullRequest(r)
	if err != nil {
		writePullFailure(w, err)
		return
	}

	page, err := handler.consumers.Page(r.Context(), consumer, request)
	if err != nil {
		writePullFailure(w, err)
		return
	}

	body := PullPage{Messages: make([]PullMessage, 0, len(page.Records)), NextCursor: page.NextCursor}
	for _, record := range page.Records {
		body.Messages = append(body.Messages, NewPullMessage(record))
	}
	writePullJSON(w, http.StatusOK, body)
}

// bearerToken extracts an Authorization: Bearer credential.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// parsePullRequest reads the query string.
//
// "after" is the cursor and the only paging mechanism. The remaining parameters
// narrow what is returned within the consumer's scope; none of them can widen
// it, because Scope.Compile is what decides the connector predicate.
func parsePullRequest(r *http.Request) (PullRequest, error) {
	query := r.URL.Query()
	request := PullRequest{
		Cursor:        strings.TrimSpace(query.Get("after")),
		ConnectorID:   strings.TrimSpace(query.Get("connector")),
		DeliveryState: DeliveryState(strings.TrimSpace(query.Get("delivery_state"))),
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return PullRequest{}, wrapInvalid("limit must be an integer")
		}
		request.Limit = limit
	}
	from, err := parsePullTime(query.Get("received_from"))
	if err != nil {
		return PullRequest{}, err
	}
	to, err := parsePullTime(query.Get("received_to"))
	if err != nil {
		return PullRequest{}, err
	}
	request.ReceivedFrom, request.ReceivedTo = from, to
	return request, nil
}

func parsePullTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, wrapInvalid("received_from and received_to must be RFC 3339 timestamps")
	}
	utc := parsed.UTC()
	return &utc, nil
}

func wrapInvalid(message string) error {
	return &pullInvalidError{message: message}
}

// pullInvalidError is a request-shape complaint that wraps ErrInvalidInput, so
// it maps to 400 through the same branch as a store-level validation failure.
type pullInvalidError struct{ message string }

func (e *pullInvalidError) Error() string { return e.message }
func (e *pullInvalidError) Unwrap() error { return ErrInvalidInput }

// pullError is the failure body.
type pullError struct {
	Error string `json:"error"`
	// Message never quotes a stored value. Every error on this path is about the
	// request or the credential, and echoing a row into an error string is how
	// content escapes an audited read path.
	Message string `json:"message,omitempty"`
}

// writePullFailure maps an error to a status.
//
// Unknown, malformed and revoked tokens all produce the same 401 with the same
// body: distinguishing them would turn the endpoint into an oracle for valid
// consumer ids.
func writePullFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", `Bearer realm="synevyr-messages"`)
		writePullError(w, http.StatusUnauthorized, "unauthorized", "a valid consumer token is required")
	case errors.Is(err, ErrForbidden):
		writePullError(w, http.StatusForbidden, "forbidden", "outside this consumer's scope")
	case errors.Is(err, ErrInvalidInput):
		writePullError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		// Deliberately opaque: a storage failure's message can name schemas,
		// hosts and statements, and this endpoint faces a partner's application.
		writePullError(w, http.StatusInternalServerError, "internal", "")
	}
}

func writePullError(w http.ResponseWriter, status int, code, message string) {
	writePullJSON(w, status, pullError{Error: code, Message: message})
}

func writePullJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		// Unreachable for the types above; falling back to a fixed body keeps a
		// half-written response from being read as a page of zero messages.
		body, status = []byte(`{"error":"internal"}`), http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
