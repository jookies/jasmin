package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// Message pull consumers on the admin REST API.
//
// Registered through an Option like the billing and termination surfaces, so
// NewHandler's signature is unchanged and a deployment with no message spool
// answers 404 on these paths rather than serving an empty collection — "this
// gateway does not spool messages" and "it has no consumers configured" are
// different answers, and only the second one invites someone to create one.

// WithMessageConsumers registers the pull-consumer CRUD surface.
func WithMessageConsumers(service *MessageConsumerService) Option {
	return func(handler *Handler) {
		handler.messageConsumers = service
	}
}

func (h *Handler) registerMessageConsumerRoutes(mux *http.ServeMux) {
	if h.messageConsumers == nil {
		return
	}
	mux.HandleFunc("/admin/message-consumers", h.auth(h.messageConsumerCollection))
	mux.HandleFunc("/admin/message-consumers/", h.auth(h.messageConsumerByID))
}

// messageConsumerCollection handles /admin/message-consumers (GET list,
// POST create).
func (h *Handler) messageConsumerCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := h.messageConsumers.ListConsumers(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, messageConsumersPayload(views))
	case http.MethodPost:
		var body messageConsumerBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		view, token, err := h.messageConsumers.CreateConsumer(
			r.Context(), body.ID, body.Label, body.Scope)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		// The only response that carries a token, and the only time it exists.
		payload := messageConsumerPayload(view)
		payload["token"] = token
		payload["token_notice"] = messageConsumerTokenNotice
		writeJSON(w, http.StatusCreated, payload)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// messageConsumerTokenNotice travels with the one response that carries a
// token, so an operator reading a terminal transcript is told the thing the
// API cannot tell them twice.
const messageConsumerTokenNotice = "This token is shown once and is not recoverable. " +
	"Store it now; if it is lost, revoke this consumer and create another."

// messageConsumerByID handles /admin/message-consumers/{id} and
// .../{id}/revoke|unrevoke, matching the termination connector resource.
func (h *Handler) messageConsumerByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/admin/message-consumers/")
	if rest == "" {
		writeError(w, http.StatusNotFound, "message consumer id required")
		return
	}
	id, action, _ := strings.Cut(rest, "/")
	switch {
	case action == "revoke" && r.Method == http.MethodPost:
		h.setMessageConsumerRevoked(w, r, id, true)
	case action == "unrevoke" && r.Method == http.MethodPost:
		h.setMessageConsumerRevoked(w, r, id, false)
	case action != "":
		writeError(w, http.StatusNotFound, "unknown sub-resource")
	case r.Method == http.MethodGet:
		view, err := h.messageConsumers.GetConsumer(r.Context(), id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, messageConsumerPayload(view))
	case r.Method == http.MethodPut:
		var body messageConsumerBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		view, err := h.messageConsumers.UpdateConsumer(r.Context(), id, body.Label, body.Scope)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, messageConsumerPayload(view))
	case r.Method == http.MethodDelete:
		if err := h.messageConsumers.DeleteConsumer(r.Context(), id); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) setMessageConsumerRevoked(w http.ResponseWriter, r *http.Request, id string, revoked bool) {
	view, err := h.messageConsumers.SetRevoked(r.Context(), id, revoked)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, messageConsumerPayload(view))
}

// messageConsumerBody is the create/update body.
//
// There is no token field in either direction: it cannot be supplied (the
// gateway mints it, so its entropy is not a caller's choice) and it cannot be
// read back.
type messageConsumerBody struct {
	ID    string         `json:"id"`
	Label string         `json:"label"`
	Scope msgspool.Scope `json:"scope"`
}

func messageConsumerPayload(view MessageConsumerView) map[string]any {
	consumer := view.Consumer
	payload := map[string]any{
		"id":         consumer.ID,
		"label":      consumer.Label,
		"scope":      consumer.Scope,
		"revoked":    consumer.Revoked,
		"created_at": consumer.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at": consumer.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	// Null rather than an empty string: "never used" is a real and interesting
	// state — a credential that was issued and forgotten — and an empty string
	// would sort and render as if it were a timestamp.
	if consumer.LastUsedAt != nil {
		payload["last_used_at"] = consumer.LastUsedAt.UTC().Format(time.RFC3339Nano)
	} else {
		payload["last_used_at"] = nil
	}
	return payload
}

func messageConsumersPayload(views []MessageConsumerView) map[string]any {
	items := make([]map[string]any, 0, len(views))
	for _, view := range views {
		items = append(items, messageConsumerPayload(view))
	}
	return map[string]any{"message_consumers": items}
}
