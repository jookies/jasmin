// Package pbfacade is the authenticated, versioned control seam behind the
// temporary Twisted Perspective Broker compatibility process.
//
// It intentionally does not implement the PB/jelly protocol and never accepts
// Python pickle. The trusted Python facade terminates PB, decodes the legacy
// Jasmin classes, and projects them into the JSON specs accepted here. This
// keeps arbitrary pickle execution outside the Go process while the facade is
// still needed by legacy clients.
package pbfacade

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

const (
	// ProtocolVersion is sent on every request and response. A facade must fail
	// closed when it does not know this exact projection contract.
	ProtocolVersion = "jasmin.pb-facade.v1"
	maxRequestBytes = 1 << 20
)

// ConnectorService is the existing live SMPP client-management service.
type ConnectorService interface {
	CreateConnector(context.Context, smppc.Config, bool) error
	DeleteConnector(context.Context, string) error
	SetStarted(context.Context, string, bool) error
	ListConnectors(context.Context) ([]admin.ConnectorView, error)
	GetConnector(context.Context, string) (admin.ConnectorView, error)
}

// UserService is the existing live RouterPB user service.
type UserService interface {
	CreateUser(context.Context, string, string) error
	DeleteUser(context.Context, string) error
	ListUsers(context.Context) ([]admin.StoredUser, error)
	GetUser(context.Context, string) (admin.StoredUser, error)
}

// GroupService is the existing live RouterPB group service.
type GroupService interface {
	CreateGroup(context.Context, string, string) error
	DeleteGroup(context.Context, string) error
	ListGroups(context.Context) ([]admin.StoredGroup, error)
	GetGroup(context.Context, string) (admin.StoredGroup, error)
}

// OrderedService is implemented by the MT and MO route services.
type OrderedService interface {
	PutRoute(context.Context, int, string) error
	DeleteRoute(context.Context, int) error
	ListRoutes(context.Context) ([]admin.StoredSpec, error)
	GetRoute(context.Context, int) (admin.StoredSpec, error)
}

// MTOrderedService is the shape of admin.RouteService, whose stored row type
// predates the shared MO/interceptor StoredSpec type.
type MTOrderedService interface {
	PutRoute(context.Context, int, string) error
	DeleteRoute(context.Context, int) error
	ListRoutes(context.Context) ([]admin.StoredRoute, error)
	GetRoute(context.Context, int) (admin.StoredRoute, error)
}

// InterceptorService is the opt-in live interception service.
type InterceptorService interface {
	PutInterceptor(context.Context, admin.InterceptorDirection, int, string) error
	DeleteInterceptor(context.Context, admin.InterceptorDirection, int) error
	ListInterceptors(context.Context, admin.InterceptorDirection) ([]admin.StoredSpec, error)
	GetInterceptor(context.Context, admin.InterceptorDirection, int) (admin.StoredSpec, error)
}

// Deps are deliberately the same services used by jCli and the web admin UI.
// A nil service makes only that method family unavailable.
type Deps struct {
	Connectors   ConnectorService
	Users        UserService
	Groups       GroupService
	MTRoutes     MTOrderedService
	MORoutes     OrderedService
	Interceptors InterceptorService
	Token        string
}

// Handler dispatches the PB facade's normalized calls.
type Handler struct {
	deps Deps
}

var (
	_ ConnectorService   = (*admin.Service)(nil)
	_ UserService        = (*admin.UserService)(nil)
	_ GroupService       = (*admin.GroupService)(nil)
	_ MTOrderedService   = (*admin.RouteService)(nil)
	_ OrderedService     = (*admin.MORouteService)(nil)
	_ InterceptorService = (*admin.InterceptorService)(nil)
)

// New validates the fail-closed authentication boundary.
func New(deps Deps) (*Handler, error) {
	if deps.Token == "" {
		return nil, errors.New("pb facade: empty token")
	}
	return &Handler{deps: deps}, nil
}

// Routes returns a handler for POST /v1/call and GET /v1/capabilities. It is
// normally mounted on a private listener reachable only by the PB facade.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/call", h.auth(h.call))
	mux.HandleFunc("/v1/capabilities", h.auth(h.capabilities))
	return mux
}

type request struct {
	Version string          `json:"version"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	Version string         `json:"version"`
	ID      string         `json:"id,omitempty"`
	OK      bool           `json:"ok"`
	Result  any            `json:"result,omitempty"`
	Error   *responseError `json:"error,omitempty"`
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		presented := strings.TrimPrefix(header, "Bearer ")
		if presented == header || subtle.ConstantTimeCompare([]byte(presented), []byte(h.deps.Token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			h.write(w, http.StatusUnauthorized, response{
				Version: ProtocolVersion,
				OK:      false,
				Error:   &responseError{Code: "unauthorized", Message: "unauthorized"},
			})
			return
		}
		next(w, r)
	}
}

func (h *Handler) capabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.failure(w, http.StatusMethodNotAllowed, "", "method_not_allowed", "method not allowed")
		return
	}
	h.write(w, http.StatusOK, response{
		Version: ProtocolVersion,
		OK:      true,
		Result: map[string]any{
			"methods":        h.methods(),
			"accepts":        "normalized-json",
			"accepts_pb":     false,
			"accepts_pickle": false,
		},
	})
}

func (h *Handler) methods() []string {
	methods := []string{"version"}
	if h.deps.Connectors != nil {
		methods = append(methods,
			"client.connector.add", "client.connector.remove",
			"client.connector.list", "client.connector.get",
			"client.connector.start", "client.connector.stop",
			"client.connector.status")
	}
	if h.deps.Groups != nil {
		methods = append(methods,
			"router.group.add", "router.group.remove",
			"router.group.list", "router.group.get")
	}
	if h.deps.Users != nil {
		methods = append(methods,
			"router.user.add", "router.user.remove",
			"router.user.list", "router.user.get")
	}
	if h.deps.MTRoutes != nil {
		methods = append(methods,
			"router.mtroute.add", "router.mtroute.remove",
			"router.mtroute.list", "router.mtroute.get")
	}
	if h.deps.MORoutes != nil {
		methods = append(methods,
			"router.moroute.add", "router.moroute.remove",
			"router.moroute.list", "router.moroute.get")
	}
	if h.deps.Interceptors != nil {
		methods = append(methods,
			"router.mtinterceptor.add", "router.mtinterceptor.remove",
			"router.mtinterceptor.list", "router.mtinterceptor.get",
			"router.mointerceptor.add", "router.mointerceptor.remove",
			"router.mointerceptor.list", "router.mointerceptor.get")
	}
	return methods
}

func (h *Handler) call(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.failure(w, http.StatusMethodNotAllowed, "", "method_not_allowed", "method not allowed")
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes+1))
	decoder.DisallowUnknownFields()
	var call request
	if err := decoder.Decode(&call); err != nil {
		h.failure(w, http.StatusBadRequest, call.ID, "bad_request", "invalid request JSON")
		return
	}
	if err := ensureEOF(decoder); err != nil {
		h.failure(w, http.StatusBadRequest, call.ID, "bad_request", "request must contain one JSON object")
		return
	}
	if call.Version != ProtocolVersion {
		h.failure(w, http.StatusBadRequest, call.ID, "unsupported_version", "unsupported protocol version")
		return
	}
	if call.ID == "" {
		h.failure(w, http.StatusBadRequest, "", "bad_request", "id is required")
		return
	}
	result, err := h.dispatch(r.Context(), call.Method, call.Params)
	if err != nil {
		status, code := classify(err)
		h.failure(w, status, call.ID, code, err.Error())
		return
	}
	h.write(w, http.StatusOK, response{
		Version: ProtocolVersion,
		ID:      call.ID,
		OK:      true,
		Result:  result,
	})
}

var (
	errUnknownMethod     = errors.New("unknown method")
	errUnavailableMethod = errors.New("method is unavailable")
)

func (h *Handler) dispatch(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "version":
		return map[string]any{"protocol": ProtocolVersion}, nil
	case "client.connector.add":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		var params struct {
			Config smppc.Config `json:"config"`
			Start  *bool        `json:"start,omitempty"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		start := true
		if params.Start != nil {
			start = *params.Start
		}
		if err := h.deps.Connectors.CreateConnector(ctx, params.Config, start); err != nil {
			return nil, err
		}
		view, err := h.deps.Connectors.GetConnector(ctx, params.Config.CID)
		return connectorResult(view), err
	case "client.connector.remove":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return true, h.deps.Connectors.DeleteConnector(ctx, params.ID)
	case "client.connector.list":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		views, err := h.deps.Connectors.ListConnectors(ctx)
		if err != nil {
			return nil, err
		}
		return connectorResults(views), nil
	case "client.connector.get", "client.connector.status":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		view, err := h.deps.Connectors.GetConnector(ctx, params.ID)
		return connectorResult(view), err
	case "client.connector.start", "client.connector.stop":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.Connectors.SetStarted(ctx, params.ID, method == "client.connector.start"); err != nil {
			return nil, err
		}
		view, err := h.deps.Connectors.GetConnector(ctx, params.ID)
		return connectorResult(view), err
	case "router.group.add":
		if h.deps.Groups == nil {
			return nil, errUnavailableMethod
		}
		var params namedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.Groups.CreateGroup(ctx, params.ID, string(params.Spec)); err != nil {
			return nil, err
		}
		group, err := h.deps.Groups.GetGroup(ctx, params.ID)
		return groupResult(group), err
	case "router.group.remove":
		if h.deps.Groups == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return true, h.deps.Groups.DeleteGroup(ctx, params.ID)
	case "router.group.list":
		if h.deps.Groups == nil {
			return nil, errUnavailableMethod
		}
		groups, err := h.deps.Groups.ListGroups(ctx)
		if err != nil {
			return nil, err
		}
		return groupResults(groups), nil
	case "router.group.get":
		if h.deps.Groups == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		group, err := h.deps.Groups.GetGroup(ctx, params.ID)
		return groupResult(group), err
	case "router.user.add":
		if h.deps.Users == nil {
			return nil, errUnavailableMethod
		}
		var params namedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.Users.CreateUser(ctx, params.ID, string(params.Spec)); err != nil {
			return nil, err
		}
		user, err := h.deps.Users.GetUser(ctx, params.ID)
		return userResult(user), err
	case "router.user.remove":
		if h.deps.Users == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return true, h.deps.Users.DeleteUser(ctx, params.ID)
	case "router.user.list":
		if h.deps.Users == nil {
			return nil, errUnavailableMethod
		}
		users, err := h.deps.Users.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		return userResults(users), nil
	case "router.user.get":
		if h.deps.Users == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		user, err := h.deps.Users.GetUser(ctx, params.ID)
		return userResult(user), err
	case "router.mtroute.add", "router.mtroute.remove", "router.mtroute.list", "router.mtroute.get":
		return h.mtRoute(ctx, method, raw)
	case "router.moroute.add", "router.moroute.remove", "router.moroute.list", "router.moroute.get":
		return h.moRoute(ctx, method, raw)
	case "router.mtinterceptor.add", "router.mtinterceptor.remove", "router.mtinterceptor.list", "router.mtinterceptor.get":
		return h.interceptor(ctx, admin.InterceptMT, method, raw)
	case "router.mointerceptor.add", "router.mointerceptor.remove", "router.mointerceptor.list", "router.mointerceptor.get":
		return h.interceptor(ctx, admin.InterceptMO, method, raw)
	default:
		return nil, errUnknownMethod
	}
}

type idParams struct {
	ID string `json:"id"`
}

type namedSpecParams struct {
	ID   string          `json:"id"`
	Spec json.RawMessage `json:"spec"`
}

type orderedSpecParams struct {
	Order int             `json:"order"`
	Spec  json.RawMessage `json:"spec"`
}

func (h *Handler) mtRoute(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if h.deps.MTRoutes == nil {
		return nil, errUnavailableMethod
	}
	switch method {
	case "router.mtroute.add":
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.MTRoutes.PutRoute(ctx, params.Order, string(params.Spec)); err != nil {
			return nil, err
		}
		route, err := h.deps.MTRoutes.GetRoute(ctx, params.Order)
		return mtRouteResult(route), err
	case "router.mtroute.remove":
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return true, h.deps.MTRoutes.DeleteRoute(ctx, params.Order)
	case "router.mtroute.list":
		routes, err := h.deps.MTRoutes.ListRoutes(ctx)
		if err != nil {
			return nil, err
		}
		result := make([]map[string]any, 0, len(routes))
		for _, route := range routes {
			result = append(result, mtRouteResult(route))
		}
		return result, nil
	default:
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		route, err := h.deps.MTRoutes.GetRoute(ctx, params.Order)
		return mtRouteResult(route), err
	}
}

func (h *Handler) moRoute(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if h.deps.MORoutes == nil {
		return nil, errUnavailableMethod
	}
	switch method {
	case "router.moroute.add":
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.MORoutes.PutRoute(ctx, params.Order, string(params.Spec)); err != nil {
			return nil, err
		}
		route, err := h.deps.MORoutes.GetRoute(ctx, params.Order)
		return orderedResult(route), err
	case "router.moroute.remove":
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return true, h.deps.MORoutes.DeleteRoute(ctx, params.Order)
	case "router.moroute.list":
		routes, err := h.deps.MORoutes.ListRoutes(ctx)
		if err != nil {
			return nil, err
		}
		return orderedResults(routes), nil
	default:
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		route, err := h.deps.MORoutes.GetRoute(ctx, params.Order)
		return orderedResult(route), err
	}
}

func (h *Handler) interceptor(ctx context.Context, direction admin.InterceptorDirection, method string, raw json.RawMessage) (any, error) {
	if h.deps.Interceptors == nil {
		return nil, errUnavailableMethod
	}
	switch {
	case strings.HasSuffix(method, ".add"):
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.Interceptors.PutInterceptor(ctx, direction, params.Order, string(params.Spec)); err != nil {
			return nil, err
		}
		interceptor, err := h.deps.Interceptors.GetInterceptor(ctx, direction, params.Order)
		return orderedResult(interceptor), err
	case strings.HasSuffix(method, ".remove"):
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return true, h.deps.Interceptors.DeleteInterceptor(ctx, direction, params.Order)
	case strings.HasSuffix(method, ".list"):
		interceptors, err := h.deps.Interceptors.ListInterceptors(ctx, direction)
		if err != nil {
			return nil, err
		}
		return orderedResults(interceptors), nil
	default:
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		interceptor, err := h.deps.Interceptors.GetInterceptor(ctx, direction, params.Order)
		return orderedResult(interceptor), err
	}
}

func connectorResult(view admin.ConnectorView) map[string]any {
	return map[string]any{
		"config":          view.Config,
		"desired_started": view.DesiredStarted,
		"observed":        view.Observed,
	}
}

func connectorResults(views []admin.ConnectorView) []map[string]any {
	result := make([]map[string]any, 0, len(views))
	for _, view := range views {
		result = append(result, connectorResult(view))
	}
	return result
}

func groupResult(group admin.StoredGroup) map[string]any {
	return map[string]any{
		"id":     group.GID,
		"number": group.Number,
		"spec":   json.RawMessage(group.SpecJSON),
	}
}

func groupResults(groups []admin.StoredGroup) []map[string]any {
	result := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		result = append(result, groupResult(group))
	}
	return result
}

func userResult(user admin.StoredUser) map[string]any {
	return map[string]any{
		"id":   user.Username,
		"uid":  user.UID,
		"spec": json.RawMessage(user.SpecJSON),
	}
}

func userResults(users []admin.StoredUser) []map[string]any {
	result := make([]map[string]any, 0, len(users))
	for _, user := range users {
		result = append(result, userResult(user))
	}
	return result
}

func mtRouteResult(route admin.StoredRoute) map[string]any {
	return map[string]any{
		"order": route.Order,
		"spec":  json.RawMessage(route.SpecJSON),
	}
}

func orderedResult(spec admin.StoredSpec) map[string]any {
	return map[string]any{
		"order": spec.Order,
		"spec":  json.RawMessage(spec.SpecJSON),
	}
}

func orderedResults(specs []admin.StoredSpec) []map[string]any {
	result := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		result = append(result, orderedResult(spec))
	}
	return result
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid params", admin.ErrInvalidRequest)
	}
	if err := ensureEOF(decoder); err != nil {
		return fmt.Errorf("%w: params must contain one JSON object", admin.ErrInvalidRequest)
	}
	return nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
}

func classify(err error) (int, string) {
	switch {
	case errors.Is(err, errUnknownMethod):
		return http.StatusNotFound, "unknown_method"
	case errors.Is(err, errUnavailableMethod):
		return http.StatusNotImplemented, "unavailable_method"
	case errors.Is(err, admin.ErrInvalidRequest):
		return http.StatusBadRequest, "invalid_params"
	case errors.Is(err, admin.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, admin.ErrConnectorNotFound),
		errors.Is(err, admin.ErrUserNotFound),
		errors.Is(err, admin.ErrGroupNotFound),
		errors.Is(err, admin.ErrRouteNotFound),
		errors.Is(err, admin.ErrMORouteNotFound),
		errors.Is(err, admin.ErrInterceptorNotFound):
		return http.StatusNotFound, "not_found"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

func (h *Handler) failure(w http.ResponseWriter, status int, id, code, message string) {
	h.write(w, status, response{
		Version: ProtocolVersion,
		ID:      id,
		OK:      false,
		Error:   &responseError{Code: code, Message: message},
	})
}

func (h *Handler) write(w http.ResponseWriter, status int, payload response) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
