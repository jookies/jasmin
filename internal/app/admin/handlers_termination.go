package admin

import (
	"net/http"
	"strings"

	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// Termination connectors on the admin REST API.
//
// It is registered through an Option, like the billing surface, so NewHandler's
// signature is unchanged and a deployment without termination connectors
// answers 404 on these paths rather than serving an empty collection — "this
// gateway does not terminate traffic" and "it terminates none right now" are
// different answers.

// WithTerminationConnectors registers the termination connector CRUD surface on
// the admin API.
func WithTerminationConnectors(service *TerminationService) Option {
	return func(handler *Handler) {
		handler.termination = service
	}
}

func (h *Handler) registerTerminationRoutes(mux *http.ServeMux) {
	if h.termination == nil {
		return
	}
	mux.HandleFunc("/admin/termination-connectors", h.auth(h.terminationConnectors))
	mux.HandleFunc("/admin/termination-connectors/", h.auth(h.terminationConnectorByID))
}

// terminationConnectors handles /admin/termination-connectors (GET list, POST
// create).
func (h *Handler) terminationConnectors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := h.termination.ListConnectors(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, terminationViewsPayload(views))
	case http.MethodPost:
		var body terminationConnectorBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		start := true
		if body.Start != nil {
			start = *body.Start
		}
		if err := h.termination.CreateConnector(r.Context(), body.Config, start); err != nil {
			writeServiceError(w, err)
			return
		}
		h.writeTerminationConnector(w, r, body.Config.CID, http.StatusCreated)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// terminationConnectorByID handles /admin/termination-connectors/{cid} and
// .../{cid}/start|stop, matching the SMPP connector resource exactly.
func (h *Handler) terminationConnectorByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/admin/termination-connectors/")
	if rest == "" {
		writeError(w, http.StatusNotFound, "termination connector id required")
		return
	}
	cid, action, _ := strings.Cut(rest, "/")
	switch {
	case action == "start" && r.Method == http.MethodPost:
		h.setTerminationStarted(w, r, cid, true)
	case action == "stop" && r.Method == http.MethodPost:
		h.setTerminationStarted(w, r, cid, false)
	case action != "":
		writeError(w, http.StatusNotFound, "unknown sub-resource")
	case r.Method == http.MethodGet:
		h.writeTerminationConnector(w, r, cid, http.StatusOK)
	case r.Method == http.MethodPut:
		var body terminationConnectorBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		body.Config.CID = cid // the path is authoritative for the cid
		if err := h.termination.UpdateConnector(r.Context(), body.Config, body.ClearDeliverySecret); err != nil {
			writeServiceError(w, err)
			return
		}
		h.writeTerminationConnector(w, r, cid, http.StatusOK)
	case r.Method == http.MethodDelete:
		if err := h.termination.DeleteConnector(r.Context(), cid); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) setTerminationStarted(w http.ResponseWriter, r *http.Request, cid string, start bool) {
	if err := h.termination.SetStarted(r.Context(), cid, start); err != nil {
		writeServiceError(w, err)
		return
	}
	h.writeTerminationConnector(w, r, cid, http.StatusOK)
}

func (h *Handler) writeTerminationConnector(w http.ResponseWriter, r *http.Request, cid string, status int) {
	view, err := h.termination.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, status, terminationViewPayload(view))
}

// terminationConnectorBody is the create/update body. The config is nested so
// the update can carry clear_delivery_secret beside it: removing a signing
// secret has to be asked for explicitly, because an omitted secret means "keep
// the stored one" on every other path.
type terminationConnectorBody struct {
	Config              termination.ConnectorConfig `json:"config"`
	Start               *bool                       `json:"start,omitempty"`
	ClearDeliverySecret bool                        `json:"clear_delivery_secret,omitempty"`
}

// terminationViewPayload projects a view. Config is already redacted by the
// service; has_secret is the fact the marker stands for.
func terminationViewPayload(view TerminationConnectorView) map[string]any {
	return map[string]any{
		"config":          view.Config,
		"desired_started": view.DesiredStarted,
		"observed":        view.Observed,
		"has_secret":      view.HasSecret,
	}
}

func terminationViewsPayload(views []TerminationConnectorView) map[string]any {
	items := make([]map[string]any, 0, len(views))
	for _, view := range views {
		items = append(items, terminationViewPayload(view))
	}
	return map[string]any{"termination_connectors": items}
}
