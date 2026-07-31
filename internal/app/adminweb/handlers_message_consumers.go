package adminweb

import (
	"net/http"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// messageConsumerResource is the flat REST shape of one message pull
// credential.
//
// Token is write-nothing/read-once: it is populated only by the create
// response, and there is no field a later read could accidentally fill, because
// the service holds a SHA-256 proof and not the secret.
type messageConsumerResource struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Scope     msgspool.Scope `json:"scope"`
	Revoked   bool           `json:"revoked"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
	// LastUsedAt is null for a credential that has never authenticated — the
	// state that identifies a token issued and then forgotten.
	LastUsedAt *string `json:"last_used_at"`
	// Reads / Rows / Denied / LastReadAt are this credential's read history,
	// aggregated from the access audit. Present only on the list endpoint,
	// which is the one place an operator compares credentials against each
	// other -- "which of these is actually being used" is a question about the
	// set, not about one row.
	//
	// Reads and Rows are both here because either alone misleads: a consumer
	// polling every second with nothing to fetch is many reads and no rows, and
	// one scripted sweep is a single read and a great many rows.
	Reads      *int64  `json:"reads,omitempty"`
	Rows       *int64  `json:"rows,omitempty"`
	Denied     *int64  `json:"denied,omitempty"`
	LastReadAt *string `json:"last_read_at,omitempty"`
	// Token is present exactly once, in the create response.
	Token string `json:"token,omitempty"`
	// TokenNotice accompanies it, because the API cannot say this twice.
	TokenNotice string `json:"token_notice,omitempty"`
}

func toMessageConsumerResource(view admin.MessageConsumerView) messageConsumerResource {
	consumer := view.Consumer
	resource := messageConsumerResource{
		ID:        consumer.ID,
		Label:     consumer.Label,
		Scope:     consumer.Scope,
		Revoked:   consumer.Revoked,
		CreatedAt: consumer.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: consumer.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if consumer.LastUsedAt != nil {
		used := consumer.LastUsedAt.UTC().Format(time.RFC3339Nano)
		resource.LastUsedAt = &used
	}
	if view.ActivityLoaded {
		reads, rows, denied := view.Activity.Reads, view.Activity.Rows, view.Activity.Denied
		resource.Reads, resource.Rows, resource.Denied = &reads, &rows, &denied
		if view.Activity.LastReadAt != nil {
			at := view.Activity.LastReadAt.UTC().Format(time.RFC3339Nano)
			resource.LastReadAt = &at
		}
	}
	return resource
}

// requireMessageConsumers answers 404 when this gateway spools no messages, so
// a deployment without the feature exposes no trace of it rather than an empty
// list that reads as "none configured" and invites creating one.
func (h *Handler) requireMessageConsumers(w http.ResponseWriter) bool {
	if h.deps.MessageConsumers == nil {
		writeError(w, http.StatusNotFound, "message consumers are not enabled on this gateway")
		return false
	}
	return true
}

func (h *Handler) listMessageConsumers(w http.ResponseWriter, r *http.Request) {
	if !h.requireMessageConsumers(w) {
		return
	}
	// Zero `since` means the whole retained audit history. The audit table is
	// pruned with the spool, so this is bounded by retention, not unbounded.
	views, err := h.deps.MessageConsumers.ListConsumersWithActivity(r.Context(), time.Time{})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resources := make([]messageConsumerResource, 0, len(views))
	for _, view := range views {
		resources = append(resources, toMessageConsumerResource(view))
	}
	writeList(w, r, resources)
}

func (h *Handler) getMessageConsumer(w http.ResponseWriter, r *http.Request) {
	if !h.requireMessageConsumers(w) {
		return
	}
	view, err := h.deps.MessageConsumers.GetConsumer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMessageConsumerResource(view))
}

func (h *Handler) createMessageConsumer(w http.ResponseWriter, r *http.Request) {
	if !h.requireMessageConsumers(w) {
		return
	}
	var res messageConsumerResource
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	view, token, err := h.deps.MessageConsumers.CreateConsumer(r.Context(), res.ID, res.Label, res.Scope)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	created := toMessageConsumerResource(view)
	created.Token = token
	created.TokenNotice = messageConsumerTokenNotice
	writeJSON(w, http.StatusCreated, created)
}

// messageConsumerTokenNotice is the console's copy of the one-time warning. It
// is spelled here rather than imported from admin so the browser payload does
// not depend on an unexported constant in another package.
const messageConsumerTokenNotice = "This token is shown once and is not recoverable. " +
	"Store it now; if it is lost, revoke this consumer and create another."

func (h *Handler) updateMessageConsumer(w http.ResponseWriter, r *http.Request) {
	if !h.requireMessageConsumers(w) {
		return
	}
	id := r.PathValue("id")
	current, err := h.deps.MessageConsumers.GetConsumer(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Start from the stored state so a PATCH that omits a field keeps it. There
	// is no secret in this baseline to protect — unlike the termination
	// connector form — because a token never travels on a read.
	res := toMessageConsumerResource(current)
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	view, err := h.deps.MessageConsumers.UpdateConsumer(r.Context(), id, res.Label, res.Scope)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if res.Revoked != current.Consumer.Revoked {
		if view, err = h.deps.MessageConsumers.SetRevoked(r.Context(), id, res.Revoked); err != nil {
			writeServiceError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, toMessageConsumerResource(view))
}

func (h *Handler) deleteMessageConsumer(w http.ResponseWriter, r *http.Request) {
	if !h.requireMessageConsumers(w) {
		return
	}
	id := r.PathValue("id")
	view, err := h.deps.MessageConsumers.GetConsumer(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if err := h.deps.MessageConsumers.DeleteConsumer(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMessageConsumerResource(view))
}

// revokeMessageConsumer / unrevokeMessageConsumer are the explicit action
// endpoints. Revoking is the fast path an operator reaches for when a token has
// leaked, and it must not require sending the whole resource back.
func (h *Handler) revokeMessageConsumer(w http.ResponseWriter, r *http.Request) {
	h.setMessageConsumerRevoked(w, r, true)
}

func (h *Handler) unrevokeMessageConsumer(w http.ResponseWriter, r *http.Request) {
	h.setMessageConsumerRevoked(w, r, false)
}

func (h *Handler) setMessageConsumerRevoked(w http.ResponseWriter, r *http.Request, revoked bool) {
	if !h.requireMessageConsumers(w) {
		return
	}
	view, err := h.deps.MessageConsumers.SetRevoked(r.Context(), r.PathValue("id"), revoked)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMessageConsumerResource(view))
}
