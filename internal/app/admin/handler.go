package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
)

// Handler is the authenticated /admin HTTP surface. Every route requires a
// bearer token equal to the configured admin token (constant-time compared).
type Handler struct {
	service *Service
	routes  *RouteService // optional; nil disables /admin/routes
	users   *UserService  // optional; nil disables /admin/users
	token   string
	billing billingOptions
	// termination is optional; nil disables /admin/termination-connectors.
	termination *TerminationService
	// messageConsumers is optional; nil disables /admin/message-consumers.
	messageConsumers *MessageConsumerService
}

// Option configures an optional part of the admin surface, following the same
// shape as restcompat's handler options.
type Option func(*Handler)

// NewHandler builds the admin HTTP handler. token must be non-empty — the
// admin plane is never exposed unauthenticated. routeService/userService may
// be nil to disable those resources (connectors-only).
func NewHandler(service *Service, routeService *RouteService, userService *UserService, token string, options ...Option) (*Handler, error) {
	if service == nil {
		return nil, errors.New("admin: nil service")
	}
	if token == "" {
		return nil, errors.New("admin: empty token; the admin API must be authenticated")
	}
	handler := &Handler{service: service, routes: routeService, users: userService, token: token}
	for _, option := range options {
		option(handler)
	}
	return handler, nil
}

// Routes returns the admin mux, to be mounted under /admin/ by the gateway.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/connectors", h.auth(h.connectors))
	mux.HandleFunc("/admin/connectors/", h.auth(h.connectorByID))
	if h.routes != nil {
		mux.HandleFunc("/admin/routes", h.auth(h.routesCollection))
		mux.HandleFunc("/admin/routes/", h.auth(h.routeByOrder))
	}
	if h.users != nil {
		mux.HandleFunc("/admin/users", h.auth(h.usersCollection))
		mux.HandleFunc("/admin/users/", h.auth(h.userByName))
	}
	h.registerBillingRoutes(mux)
	h.registerTerminationRoutes(mux)
	h.registerMessageConsumerRoutes(mux)
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

// routesCollection handles /admin/routes (GET list, POST create-or-replace).
// The POST body is the raw outbound.RouteConfig JSON, kept opaque here (the
// provisioner validates it); the route's "order" is its identity.
func (h *Handler) routesCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		stored, err := h.routes.ListRoutes(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(stored))
		for _, route := range stored {
			items = append(items, map[string]any{"order": route.Order, "spec": json.RawMessage(route.SpecJSON)})
		}
		writeJSON(w, http.StatusOK, map[string]any{"routes": items})
	case http.MethodPost:
		raw, order, err := readRouteSpec(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.routes.PutRoute(r.Context(), order, raw); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"order": order, "spec": json.RawMessage(raw)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeByOrder handles /admin/routes/{order} (GET, PUT, DELETE).
func (h *Handler) routeByOrder(w http.ResponseWriter, r *http.Request) {
	orderText := strings.TrimPrefix(r.URL.Path, "/admin/routes/")
	order, err := strconv.Atoi(orderText)
	if err != nil {
		writeError(w, http.StatusBadRequest, "route order must be an integer")
		return
	}
	switch r.Method {
	case http.MethodGet:
		route, err := h.routes.GetRoute(r.Context(), order)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"order": route.Order, "spec": json.RawMessage(route.SpecJSON)})
	case http.MethodPut:
		raw, bodyOrder, err := readRouteSpec(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if bodyOrder != order {
			writeError(w, http.StatusBadRequest, "path order and body order differ")
			return
		}
		if err := h.routes.PutRoute(r.Context(), order, raw); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"order": order, "spec": json.RawMessage(raw)})
	case http.MethodDelete:
		if err := h.routes.DeleteRoute(r.Context(), order); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// usersCollection handles /admin/users (GET list, POST create). The POST body
// is the raw outbound.UserConfig JSON (opaque here; the provisioner validates
// it); "username" is the identity. The stored spec includes the password hash,
// so the list projection omits it.
func (h *Handler) usersCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		stored, err := h.users.ListUsers(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(stored))
		for _, user := range stored {
			items = append(items, map[string]any{"username": user.Username, "uid": user.UID})
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": items})
	case http.MethodPost:
		raw, username, err := readUserSpec(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.users.CreateUser(r.Context(), username, raw); err != nil {
			writeServiceError(w, err)
			return
		}
		stored, err := h.users.GetUser(r.Context(), username)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"username": stored.Username, "uid": stored.UID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// userByName handles /admin/users/{username} (GET, PUT, DELETE).
func (h *Handler) userByName(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimPrefix(r.URL.Path, "/admin/users/")
	if username == "" {
		writeError(w, http.StatusNotFound, "username required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		stored, err := h.users.GetUser(r.Context(), username)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"username": stored.Username, "uid": stored.UID})
	case http.MethodPut:
		raw, bodyUsername, err := readUserSpec(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if bodyUsername != "" && bodyUsername != username {
			writeError(w, http.StatusBadRequest, "path username and body username differ")
			return
		}
		if err := h.users.CreateUser(r.Context(), username, raw); err != nil {
			writeServiceError(w, err)
			return
		}
		stored, err := h.users.GetUser(r.Context(), username)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"username": stored.Username, "uid": stored.UID})
	case http.MethodDelete:
		if err := h.users.DeleteUser(r.Context(), username); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// readUserSpec reads the raw user JSON body and extracts its "username".
func readUserSpec(r *http.Request) (string, string, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("read body: %w", err)
	}
	var probe struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", "", fmt.Errorf("invalid user JSON: %w", err)
	}
	return string(raw), probe.Username, nil
}

// readRouteSpec reads the raw route JSON body and extracts its "order" field
// (the identity) without otherwise interpreting the spec.
func readRouteSpec(r *http.Request) (string, int, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("read body: %w", err)
	}
	var probe struct {
		Order int `json:"order"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", 0, fmt.Errorf("invalid route JSON: %w", err)
	}
	return string(raw), probe.Order, nil
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
	case errors.Is(err, ErrConnectorNotFound), errors.Is(err, ErrRouteNotFound), errors.Is(err, ErrUserNotFound),
		errors.Is(err, ErrTerminationConnectorNotFound), errors.Is(err, ErrMessageConsumerNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
