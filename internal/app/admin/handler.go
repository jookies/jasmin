package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// Handler is the authenticated /admin HTTP surface. Every route requires a
// bearer token equal to the configured admin token (constant-time compared).
type Handler struct {
	service *Service
	token   string
}

// NewHandler builds the admin HTTP handler. token must be non-empty — the
// admin plane is never exposed unauthenticated.
func NewHandler(service *Service, token string) (*Handler, error) {
	if service == nil {
		return nil, errors.New("admin: nil service")
	}
	if token == "" {
		return nil, errors.New("admin: empty token; the admin API must be authenticated")
	}
	return &Handler{service: service, token: token}, nil
}

// Routes returns the admin mux, to be mounted under /admin/ by the gateway.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/connectors", h.auth(h.connectors))
	mux.HandleFunc("/admin/connectors/", h.auth(h.connectorByID))
	return mux
}

// auth enforces the bearer token before dispatching.
func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		presented := strings.TrimPrefix(header, "Bearer ")
		if presented == header || subtle.ConstantTimeCompare([]byte(presented), []byte(h.token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// connectors handles /admin/connectors (GET list, POST create).
func (h *Handler) connectors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := h.service.ListConnectors(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, connectorViewsPayload(views))
	case http.MethodPost:
		var body connectorCreateBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		start := true
		if body.Start != nil {
			start = *body.Start
		}
		if err := h.service.CreateConnector(r.Context(), body.Config, start); err != nil {
			writeServiceError(w, err)
			return
		}
		view, err := h.service.GetConnector(r.Context(), body.Config.CID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, connectorViewPayload(view))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// connectorByID handles /admin/connectors/{cid} and .../{cid}/start|stop.
func (h *Handler) connectorByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/admin/connectors/")
	if rest == "" {
		writeError(w, http.StatusNotFound, "connector id required")
		return
	}
	cid, action, _ := strings.Cut(rest, "/")
	switch {
	case action == "start" && r.Method == http.MethodPost:
		h.setStarted(w, r, cid, true)
	case action == "stop" && r.Method == http.MethodPost:
		h.setStarted(w, r, cid, false)
	case action != "":
		writeError(w, http.StatusNotFound, "unknown sub-resource")
	case r.Method == http.MethodGet:
		view, err := h.service.GetConnector(r.Context(), cid)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, connectorViewPayload(view))
	case r.Method == http.MethodPut:
		var config smppc.Config
		if err := decodeJSON(r, &config); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		config.CID = cid // the path is authoritative for the cid
		if err := h.service.UpdateConnector(r.Context(), config); err != nil {
			writeServiceError(w, err)
			return
		}
		view, err := h.service.GetConnector(r.Context(), cid)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, connectorViewPayload(view))
	case r.Method == http.MethodDelete:
		if err := h.service.DeleteConnector(r.Context(), cid); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) setStarted(w http.ResponseWriter, r *http.Request, cid string, start bool) {
	if err := h.service.SetStarted(r.Context(), cid, start); err != nil {
		writeServiceError(w, err)
		return
	}
	view, err := h.service.GetConnector(r.Context(), cid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, connectorViewPayload(view))
}

type connectorCreateBody struct {
	Config smppc.Config `json:"config"`
	Start  *bool        `json:"start,omitempty"`
}

func connectorViewPayload(view ConnectorView) map[string]any {
	return map[string]any{
		"config":          view.Config,
		"desired_started": view.DesiredStarted,
		"observed":        view.Observed,
	}
}

func connectorViewsPayload(views []ConnectorView) map[string]any {
	items := make([]map[string]any, 0, len(views))
	for _, view := range views {
		items = append(items, connectorViewPayload(view))
	}
	return map[string]any{"connectors": items}
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeServiceError maps a service error to an HTTP status.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrConnectorNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
