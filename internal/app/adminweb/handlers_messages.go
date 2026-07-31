package adminweb

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// The operator's view of the message spool.
//
// Until this existed the spool had exactly one reader: the partner-facing
// GET /messages pull API, authenticated by a message-consumer bearer token.
// That is the right surface for an application fetching its own traffic and the
// wrong one for an operator answering "did this OTP arrive" — it needs a
// credential scoped to a connector, it is shown once and cannot be recovered,
// and issuing one to a human turns an audit subject into a shared secret.
//
// So this is a separate surface with separate authority, not a shortcut into
// the same one. It reads through msgspool.Service with a console principal, so
// every read goes through the same authorization and audit path a consumer read
// does, and lands in the same access audit.
//
// On the principal: the console session is already a privilege boundary — it
// mints credentials and starts connectors — so an operator holding it can grant
// themselves any role here. The point of naming the role per request is not to
// stop them; it is that revealing content is recorded as a reveal, by subject,
// separately from listing metadata. A console that always asked for
// RoleRevealer would make "who read message text" unanswerable.

// messageResource is the flat REST shape of one spooled message.
//
// Text and RawHex are omitted unless the request asked to reveal content and
// the record actually carries it, so a listing response cannot leak message
// bodies through a field the UI merely chose not to render.
type messageResource struct {
	MessageID     string `json:"message_id"`
	ConnectorID   string `json:"connector_id"`
	UserID        string `json:"user_id,omitempty"`
	SourceAddr    string `json:"source_addr"`
	DestAddr      string `json:"dest_addr"`
	ReceivedAt    string `json:"received_at"`
	Encoding      string `json:"encoding,omitempty"`
	Parts         int    `json:"parts,omitempty"`
	VerdictStat   string `json:"verdict_stat,omitempty"`
	DeliveryState string `json:"delivery_state,omitempty"`
	Attempts      int    `json:"delivery_attempts"`
	Text          string `json:"text,omitempty"`
	RawHex        string `json:"raw_hex,omitempty"`
}

type messagePageResource struct {
	Messages   []messageResource `json:"messages"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// requireMessages answers 404 when this gateway spools nothing, matching
// requireMessageConsumers: a deployment without a termination connector should
// expose no trace of the feature rather than an empty list that reads as "no
// messages yet" and sends an operator hunting for traffic that was never
// stored here.
func (h *Handler) requireMessages(w http.ResponseWriter) bool {
	if h.deps.Messages == nil {
		writeError(w, http.StatusNotFound, "this gateway does not spool messages")
		return false
	}
	return true
}

// consolePrincipal names the session user as the audit subject. reveal decides
// whether content may be returned — see the note at the top of this file.
func consolePrincipal(username string, reveal bool) msgspool.Principal {
	role := msgspool.RoleReader
	if reveal {
		role = msgspool.RoleRevealer
	}
	return msgspool.Principal{
		Subject: "console:" + username,
		Roles:   []msgspool.Role{role},
	}
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) {
	if !h.requireMessages(w) {
		return
	}
	request, reveal, err := parseMessageSearch(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := h.deps.Messages.Search(r.Context(),
		consolePrincipal(currentUser(r.Context()), reveal), request)
	if err != nil {
		writeMessageError(w, err)
		return
	}
	body := messagePageResource{
		Messages:   make([]messageResource, 0, len(page.Records)),
		NextCursor: page.NextCursor,
	}
	for _, record := range page.Records {
		body.Messages = append(body.Messages, toMessageResource(record))
	}
	writeJSON(w, http.StatusOK, body)
}

// getMessage returns one record WITH its content. There is no metadata-only
// variant: opening a single message is the deliberate act the reveal audit
// exists to record, and a caller wanting metadata alone can read it from the
// list.
func (h *Handler) getMessage(w http.ResponseWriter, r *http.Request) {
	if !h.requireMessages(w) {
		return
	}
	record, err := h.deps.Messages.Reveal(r.Context(),
		consolePrincipal(currentUser(r.Context()), true), r.PathValue("messageID"))
	if err != nil {
		writeMessageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMessageResource(record))
}

func toMessageResource(record msgspool.Record) messageResource {
	resource := messageResource{
		MessageID:     record.MessageID,
		ConnectorID:   record.ConnectorID,
		UserID:        record.UserID,
		SourceAddr:    record.SourceAddr,
		DestAddr:      record.DestAddr,
		ReceivedAt:    record.ReceivedAt.UTC().Format(time.RFC3339Nano),
		Encoding:      record.Encoding,
		Parts:         record.Parts,
		VerdictStat:   record.Verdict.Stat,
		DeliveryState: string(record.DeliveryState),
		Attempts:      record.DeliveryAttempts,
	}
	// ContentRedacted is the store telling us the read withheld content. Trust
	// that over the emptiness of the fields: an empty Text on a redacted record
	// means "not shown to you", and emitting it as "" would render in the
	// console as a message with no body.
	if !record.ContentRedacted {
		resource.Text = record.Text
		if len(record.Raw) > 0 {
			resource.RawHex = hex.EncodeToString(record.Raw)
		}
	}
	return resource
}

const (
	messagePageDefault = 50
	messagePageMax     = 200
)

func parseMessageSearch(r *http.Request) (msgspool.SearchRequest, bool, error) {
	query := r.URL.Query()
	reveal := boolParam(query.Get("include_content"))
	request := msgspool.SearchRequest{
		Cursor:        strings.TrimSpace(query.Get("after")),
		ConnectorID:   strings.TrimSpace(query.Get("connector")),
		UserID:        strings.TrimSpace(query.Get("user")),
		DestAddr:      strings.TrimSpace(query.Get("to")),
		DeliveryState: msgspool.DeliveryState(strings.TrimSpace(query.Get("delivery_state"))),
		VerdictStat:   strings.TrimSpace(query.Get("verdict")),
		// Content is returned only when explicitly asked for, so the default
		// listing is metadata and is not audited as a reveal.
		IncludeContent: reveal,
		// Newest first, by default and unlike the partner pull API. An operator
		// opening this page is nearly always asking about the message that just
		// arrived; a consumer walking the pull API wants the opposite, so the
		// two surfaces differ deliberately rather than sharing a default.
		Descending: !boolParam(query.Get("oldest_first")),
		Limit:      messagePageDefault,
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return msgspool.SearchRequest{}, false, errors.New("limit must be an integer")
		}
		if limit < 1 || limit > messagePageMax {
			return msgspool.SearchRequest{}, false,
				errors.New("limit must be between 1 and " + strconv.Itoa(messagePageMax))
		}
		request.Limit = limit
	}
	from, err := parseMessageTime(query.Get("received_from"))
	if err != nil {
		return msgspool.SearchRequest{}, false, err
	}
	to, err := parseMessageTime(query.Get("received_to"))
	if err != nil {
		return msgspool.SearchRequest{}, false, err
	}
	request.ReceivedFrom, request.ReceivedTo = from, to
	return request, reveal, nil
}

func parseMessageTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errors.New("received_from and received_to must be RFC 3339 timestamps")
	}
	utc := parsed.UTC()
	return &utc, nil
}

func boolParam(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// writeMessageError maps the spool's sentinel errors onto status codes. A
// denial is 403 rather than 404: the console operator is authenticated, and
// hiding the record's existence from them would hide a real authorization
// result behind a wrong one.
func writeMessageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, msgspool.ErrNotFound):
		writeError(w, http.StatusNotFound, "message not found or past its retention window")
	case errors.Is(err, msgspool.ErrForbidden):
		writeError(w, http.StatusForbidden, "not permitted to read this message")
	case errors.Is(err, msgspool.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeServiceError(w, err)
	}
}
