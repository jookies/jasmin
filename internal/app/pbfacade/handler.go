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
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/billing"
	"github.com/pumpitspace/jasmin/internal/core/interceptor"
	"github.com/pumpitspace/jasmin/internal/core/routingfilter"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
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

type connectorUpdater interface {
	UpdateConnector(context.Context, smppc.Config) error
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

// ProfileService implements the named snapshot contract exposed by both
// RouterPB and SMPPClientManagerPB. Load must not return until the restored
// rows have been applied to every live service (or rolled back).
type ProfileService interface {
	Save(context.Context, string) error
	Load(context.Context, string) error
	IsPersisted() bool
	MarkDirty()
}

type scopedProfileService interface {
	SaveScope(context.Context, string, string) error
	LoadScope(context.Context, string, string) error
}

// SMPPServerService is the live SMPPServerPB management surface.
type SMPPServerService interface {
	BoundSystemIDs() []string
	UnbindUser(string) int
	Deliver(context.Context, string, smppwire.PDU) error
}

// Deps are deliberately the same services used by jCli and the web admin UI.
// A nil service makes only that method family unavailable.
type Deps struct {
	Connectors    ConnectorService
	Users         UserService
	Groups        GroupService
	MTRoutes      MTOrderedService
	MORoutes      OrderedService
	Interceptors  InterceptorService
	Profiles      ProfileService
	Authenticator core.Authenticator
	Submitter     core.Submitter
	SMPPServer    SMPPServerService
	ScriptRunner  interceptor.Runner
	Token         string
}

// Handler dispatches the PB facade's normalized calls.
type Handler struct {
	deps             Deps
	connectorStatsMu sync.Mutex
	connectorStats   map[string]connectorStats
}

type connectorStats struct {
	start int
	stop  int
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
	return &Handler{deps: deps, connectorStats: make(map[string]connectorStats)}, nil
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
			"client.connector.update", "client.connector.config",
			"client.connector.start", "client.connector.stop",
			"client.connector.stop_all", "client.connector.status",
			"client.connector.session_state")
	}
	if h.deps.Groups != nil {
		methods = append(methods,
			"router.group.add", "router.group.remove",
			"router.group.remove_all", "router.group.list", "router.group.get",
			"router.group.enable", "router.group.disable")
	}
	if h.deps.Users != nil {
		methods = append(methods,
			"router.user.add", "router.user.remove",
			"router.user.remove_all", "router.user.list", "router.user.get",
			"router.user.enable", "router.user.disable",
			"router.user.set_quota", "router.user.update_quota")
	}
	if h.deps.Authenticator != nil {
		methods = append(methods, "router.user.authenticate")
	}
	if h.deps.MTRoutes != nil {
		methods = append(methods,
			"router.mtroute.add", "router.mtroute.remove",
			"router.mtroute.flush", "router.mtroute.list", "router.mtroute.get")
	}
	if h.deps.MORoutes != nil {
		methods = append(methods,
			"router.moroute.add", "router.moroute.remove",
			"router.moroute.flush", "router.moroute.list", "router.moroute.get")
	}
	if h.deps.Interceptors != nil {
		methods = append(methods,
			"router.mtinterceptor.add", "router.mtinterceptor.remove",
			"router.mtinterceptor.flush", "router.mtinterceptor.list", "router.mtinterceptor.get",
			"router.mointerceptor.add", "router.mointerceptor.remove",
			"router.mointerceptor.flush", "router.mointerceptor.list", "router.mointerceptor.get")
	}
	if h.deps.Profiles != nil {
		methods = append(methods, "profile.persist", "profile.load", "profile.is_persisted")
	}
	if h.deps.Submitter != nil && h.deps.Connectors != nil {
		methods = append(methods, "router.submit_sm")
	}
	if h.deps.SMPPServer != nil {
		methods = append(methods,
			"smpps.list_bound_systemids", "smpps.unbind", "smpps.ban",
			"smpps.deliverer_send_request")
	}
	if h.deps.ScriptRunner != nil {
		methods = append(methods, "interceptor.run_script")
	}
	sort.Strings(methods)
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
		h.markDirty()
		view, err := h.deps.Connectors.GetConnector(ctx, params.Config.CID)
		return h.connectorResult(view), err
	case "client.connector.update":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		updater, ok := h.deps.Connectors.(connectorUpdater)
		if !ok {
			return nil, errUnavailableMethod
		}
		var params struct {
			Config smppc.Config `json:"config"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := updater.UpdateConnector(ctx, params.Config); err != nil {
			return nil, err
		}
		h.markDirty()
		view, err := h.deps.Connectors.GetConnector(ctx, params.Config.CID)
		return h.connectorResult(view), err
	case "client.connector.remove":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		view, err := h.deps.Connectors.GetConnector(ctx, params.ID)
		if err != nil {
			return nil, err
		}
		if view.DesiredStarted {
			if err := h.deps.Connectors.SetStarted(ctx, params.ID, false); err != nil {
				return nil, err
			}
		}
		if err := h.deps.Connectors.DeleteConnector(ctx, params.ID); err != nil {
			return nil, err
		}
		h.connectorStatsMu.Lock()
		delete(h.connectorStats, params.ID)
		h.connectorStatsMu.Unlock()
		h.markDirty()
		return true, nil
	case "client.connector.list":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		views, err := h.deps.Connectors.ListConnectors(ctx)
		if err != nil {
			return nil, err
		}
		return h.connectorResults(views), nil
	case "client.connector.get", "client.connector.status", "client.connector.config", "client.connector.session_state":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		view, err := h.deps.Connectors.GetConnector(ctx, params.ID)
		if err != nil {
			return nil, err
		}
		switch method {
		case "client.connector.config":
			return view.Config, nil
		case "client.connector.session_state":
			return view.Observed, nil
		default:
			return h.connectorResult(view), nil
		}
	case "client.connector.start", "client.connector.stop":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		var params struct {
			ID           string `json:"id"`
			DeleteQueues bool   `json:"delete_queues,omitempty"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.DeleteQueues {
			return nil, fmt.Errorf("%w: delete_queues is not supported by the native durable topology", admin.ErrInvalidRequest)
		}
		if err := h.deps.Connectors.SetStarted(ctx, params.ID, method == "client.connector.start"); err != nil {
			return nil, err
		}
		h.recordConnectorLifecycle(params.ID, method == "client.connector.start")
		h.markDirty()
		view, err := h.deps.Connectors.GetConnector(ctx, params.ID)
		return h.connectorResult(view), err
	case "client.connector.stop_all":
		if h.deps.Connectors == nil {
			return nil, errUnavailableMethod
		}
		var params struct {
			DeleteQueues bool `json:"delete_queues,omitempty"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		views, err := h.deps.Connectors.ListConnectors(ctx)
		if err != nil {
			return nil, err
		}
		// The native manager has no per-connector AMQP queue to delete. Reject
		// the legacy flag instead of pretending the destructive request ran.
		if params.DeleteQueues {
			return nil, fmt.Errorf("%w: delete_queues is not supported by the native durable topology", admin.ErrInvalidRequest)
		}
		for _, view := range views {
			if !view.DesiredStarted {
				continue
			}
			if err := h.deps.Connectors.SetStarted(ctx, view.Config.CID, false); err != nil {
				return nil, err
			}
			h.recordConnectorLifecycle(view.Config.CID, false)
		}
		h.markDirty()
		return true, nil
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
		h.markDirty()
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
		if err := h.deps.Groups.DeleteGroup(ctx, params.ID); err != nil {
			return nil, err
		}
		h.markDirty()
		return true, nil
	case "router.group.remove_all":
		if h.deps.Groups == nil {
			return nil, errUnavailableMethod
		}
		if err := decodeParams(raw, &struct{}{}); err != nil {
			return nil, err
		}
		groups, err := h.deps.Groups.ListGroups(ctx)
		if err != nil {
			return nil, err
		}
		for _, group := range groups {
			if err := h.deps.Groups.DeleteGroup(ctx, group.GID); err != nil {
				return nil, err
			}
		}
		h.markDirty()
		return true, nil
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
	case "router.group.enable", "router.group.disable":
		if h.deps.Groups == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		group, err := h.deps.Groups.GetGroup(ctx, params.ID)
		if err != nil {
			return nil, err
		}
		spec, err := setJSONBoolean(group.SpecJSON, "disabled", method == "router.group.disable")
		if err != nil {
			return nil, err
		}
		if err := h.deps.Groups.CreateGroup(ctx, params.ID, spec); err != nil {
			return nil, err
		}
		h.markDirty()
		return true, nil
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
		h.markDirty()
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
		if err := h.deps.Users.DeleteUser(ctx, params.ID); err != nil {
			return nil, err
		}
		h.markDirty()
		return true, nil
	case "router.user.remove_all":
		if h.deps.Users == nil {
			return nil, errUnavailableMethod
		}
		if err := decodeParams(raw, &struct{}{}); err != nil {
			return nil, err
		}
		users, err := h.deps.Users.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			if err := h.deps.Users.DeleteUser(ctx, user.Username); err != nil {
				return nil, err
			}
		}
		h.markDirty()
		return true, nil
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
	case "router.user.enable", "router.user.disable":
		if h.deps.Users == nil {
			return nil, errUnavailableMethod
		}
		var params idParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.setUserDisabled(ctx, params.ID, method == "router.user.disable"); err != nil {
			return nil, err
		}
		h.markDirty()
		return true, nil
	case "router.user.authenticate":
		if h.deps.Authenticator == nil {
			return nil, errUnavailableMethod
		}
		var params struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.Authenticator.Authenticate(ctx, params.Username, params.Password); err != nil {
			// The historical RouterPB returns false for bad credentials instead
			// of failing the Deferred.
			return false, nil
		}
		return true, nil
	case "router.user.set_quota", "router.user.update_quota":
		if h.deps.Users == nil {
			return nil, errUnavailableMethod
		}
		var params quotaParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.mutateQuota(ctx, params, method == "router.user.update_quota"); err != nil {
			return nil, err
		}
		h.markDirty()
		return true, nil
	case "router.mtroute.add", "router.mtroute.remove", "router.mtroute.list", "router.mtroute.get":
		return h.mtRoute(ctx, method, raw)
	case "router.mtroute.flush":
		return h.flushMTRoutes(ctx, raw)
	case "router.moroute.add", "router.moroute.remove", "router.moroute.list", "router.moroute.get":
		return h.moRoute(ctx, method, raw)
	case "router.moroute.flush":
		return h.flushOrdered(ctx, h.deps.MORoutes, raw)
	case "router.mtinterceptor.add", "router.mtinterceptor.remove", "router.mtinterceptor.list", "router.mtinterceptor.get":
		return h.interceptor(ctx, admin.InterceptMT, method, raw)
	case "router.mtinterceptor.flush":
		return h.flushInterceptors(ctx, admin.InterceptMT, raw)
	case "router.mointerceptor.add", "router.mointerceptor.remove", "router.mointerceptor.list", "router.mointerceptor.get":
		return h.interceptor(ctx, admin.InterceptMO, method, raw)
	case "router.mointerceptor.flush":
		return h.flushInterceptors(ctx, admin.InterceptMO, raw)
	case "profile.persist":
		return h.persist(ctx, raw)
	case "profile.load":
		return h.load(ctx, raw)
	case "profile.is_persisted":
		if h.deps.Profiles == nil {
			return nil, errUnavailableMethod
		}
		if err := decodeParams(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return h.deps.Profiles.IsPersisted(), nil
	case "router.submit_sm":
		return h.submitSM(ctx, raw)
	case "smpps.list_bound_systemids":
		if h.deps.SMPPServer == nil {
			return nil, errUnavailableMethod
		}
		if err := decodeParams(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return h.deps.SMPPServer.BoundSystemIDs(), nil
	case "smpps.unbind", "smpps.ban":
		return h.unbind(ctx, method, raw)
	case "smpps.deliverer_send_request":
		return h.deliver(ctx, raw)
	case "interceptor.run_script":
		return h.runScript(ctx, raw)
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

type quotaParams struct {
	ID    string          `json:"id"`
	Cred  string          `json:"cred"`
	Quota string          `json:"quota"`
	Value json.RawMessage `json:"value"`
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
		h.markDirty()
		route, err := h.deps.MTRoutes.GetRoute(ctx, params.Order)
		return mtRouteResult(route), err
	case "router.mtroute.remove":
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.MTRoutes.DeleteRoute(ctx, params.Order); err != nil {
			return nil, err
		}
		h.markDirty()
		return true, nil
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
		h.markDirty()
		route, err := h.deps.MORoutes.GetRoute(ctx, params.Order)
		return orderedResult(route), err
	case "router.moroute.remove":
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.MORoutes.DeleteRoute(ctx, params.Order); err != nil {
			return nil, err
		}
		h.markDirty()
		return true, nil
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
		h.markDirty()
		interceptor, err := h.deps.Interceptors.GetInterceptor(ctx, direction, params.Order)
		return orderedResult(interceptor), err
	case strings.HasSuffix(method, ".remove"):
		var params orderedSpecParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if err := h.deps.Interceptors.DeleteInterceptor(ctx, direction, params.Order); err != nil {
			return nil, err
		}
		h.markDirty()
		return true, nil
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

func (h *Handler) markDirty() {
	if h.deps.Profiles != nil {
		h.deps.Profiles.MarkDirty()
	}
}

func setJSONBoolean(source, key string, value bool) (string, error) {
	var spec map[string]any
	if err := json.Unmarshal([]byte(source), &spec); err != nil {
		return "", fmt.Errorf("%w: invalid stored spec", admin.ErrInvalidRequest)
	}
	spec[key] = value
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("%w: encode spec", admin.ErrInvalidRequest)
	}
	return string(encoded), nil
}

func (h *Handler) setUserDisabled(ctx context.Context, id string, disabled bool) error {
	user, err := h.deps.Users.GetUser(ctx, id)
	if err != nil {
		return err
	}
	spec, err := setJSONBoolean(user.SpecJSON, "disabled", disabled)
	if err != nil {
		return err
	}
	return h.deps.Users.CreateUser(ctx, user.Username, spec)
}

func (h *Handler) setUserSMPPBind(ctx context.Context, id string, allowed bool) error {
	user, err := h.deps.Users.GetUser(ctx, id)
	if err != nil {
		return err
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(user.SpecJSON), &spec); err != nil {
		return fmt.Errorf("%w: invalid stored user spec", admin.ErrInvalidRequest)
	}
	credential, _ := spec["smpps_credential"].(map[string]any)
	if credential == nil {
		credential = make(map[string]any)
	}
	credential["bind"] = allowed
	spec["smpps_credential"] = credential
	encoded, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("%w: encode user spec", admin.ErrInvalidRequest)
	}
	return h.deps.Users.CreateUser(ctx, user.Username, string(encoded))
}

func (h *Handler) mutateQuota(ctx context.Context, params quotaParams, update bool) error {
	if params.ID == "" || params.Cred == "" || params.Quota == "" || len(params.Value) == 0 {
		return fmt.Errorf("%w: id, cred, quota, and value are required", admin.ErrInvalidRequest)
	}
	user, err := h.deps.Users.GetUser(ctx, params.ID)
	if err != nil {
		return err
	}
	var spec map[string]any
	decoder := json.NewDecoder(strings.NewReader(user.SpecJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&spec); err != nil {
		return fmt.Errorf("%w: invalid stored user spec", admin.ErrInvalidRequest)
	}
	var incoming any
	valueDecoder := json.NewDecoder(strings.NewReader(string(params.Value)))
	valueDecoder.UseNumber()
	if err := valueDecoder.Decode(&incoming); err != nil {
		return fmt.Errorf("%w: invalid quota value", admin.ErrInvalidRequest)
	}

	container := spec
	key := params.Quota
	switch params.Cred {
	case "mt_credential", "mt_messaging_cred":
		switch params.Quota {
		case "balance", "submit_sm_count", "early_decrement_balance_percent":
			// These quota values are top-level in the Go provisioning spec.
		case "http_throughput", "smpps_throughput":
			container = nestedObject(spec, "mt_credential")
		default:
			return fmt.Errorf("%w: unknown mt_credential quota %q", admin.ErrInvalidRequest, params.Quota)
		}
	case "smpps_credential":
		if params.Quota != "max_bindings" {
			return fmt.Errorf("%w: unknown smpps_credential quota %q", admin.ErrInvalidRequest, params.Quota)
		}
		container = nestedObject(spec, "smpps_credential")
	default:
		return fmt.Errorf("%w: invalid credential %q", admin.ErrInvalidRequest, params.Cred)
	}

	if update {
		current, exists := container[key]
		if incoming == nil {
			return fmt.Errorf("%w: quota difference must be numeric", admin.ErrInvalidRequest)
		}
		left := 0.0
		if exists && current != nil {
			left, err = numericValue(current)
			if err != nil {
				return err
			}
		}
		right, err := numericValue(incoming)
		if err != nil {
			return err
		}
		incoming = json.Number(fmt.Sprintf("%.12g", left+right))
	}
	container[key] = incoming
	encoded, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("%w: encode quota update", admin.ErrInvalidRequest)
	}
	return h.deps.Users.CreateUser(ctx, user.Username, string(encoded))
}

func nestedObject(parent map[string]any, key string) map[string]any {
	child, _ := parent[key].(map[string]any)
	if child == nil {
		child = make(map[string]any)
		parent[key] = child
	}
	return child
}

func numericValue(value any) (float64, error) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("%w: quota value must be numeric", admin.ErrInvalidRequest)
		}
		return number, nil
	case float64:
		return typed, nil
	default:
		return 0, fmt.Errorf("%w: quota value must be numeric", admin.ErrInvalidRequest)
	}
}

func (h *Handler) flushMTRoutes(ctx context.Context, raw json.RawMessage) (any, error) {
	if h.deps.MTRoutes == nil {
		return nil, errUnavailableMethod
	}
	if err := decodeParams(raw, &struct{}{}); err != nil {
		return nil, err
	}
	routes, err := h.deps.MTRoutes.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	for _, route := range routes {
		if err := h.deps.MTRoutes.DeleteRoute(ctx, route.Order); err != nil {
			return nil, err
		}
	}
	h.markDirty()
	return true, nil
}

func (h *Handler) flushOrdered(ctx context.Context, service OrderedService, raw json.RawMessage) (any, error) {
	if service == nil {
		return nil, errUnavailableMethod
	}
	if err := decodeParams(raw, &struct{}{}); err != nil {
		return nil, err
	}
	specs, err := service.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		if err := service.DeleteRoute(ctx, spec.Order); err != nil {
			return nil, err
		}
	}
	h.markDirty()
	return true, nil
}

func (h *Handler) flushInterceptors(ctx context.Context, direction admin.InterceptorDirection, raw json.RawMessage) (any, error) {
	if h.deps.Interceptors == nil {
		return nil, errUnavailableMethod
	}
	if err := decodeParams(raw, &struct{}{}); err != nil {
		return nil, err
	}
	specs, err := h.deps.Interceptors.ListInterceptors(ctx, direction)
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		if err := h.deps.Interceptors.DeleteInterceptor(ctx, direction, spec.Order); err != nil {
			return nil, err
		}
	}
	h.markDirty()
	return true, nil
}

func (h *Handler) persist(ctx context.Context, raw json.RawMessage) (any, error) {
	if h.deps.Profiles == nil {
		return nil, errUnavailableMethod
	}
	var params struct {
		Profile string `json:"profile,omitempty"`
		Scope   string `json:"scope,omitempty"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Profile == "" {
		params.Profile = "jcli-prod"
	}
	if err := validateScope(params.Scope); err != nil {
		return nil, err
	}
	var err error
	if scoped, ok := h.deps.Profiles.(scopedProfileService); ok {
		err = scoped.SaveScope(ctx, params.Profile, normalizedScope(params.Scope))
	} else {
		err = h.deps.Profiles.Save(ctx, params.Profile)
	}
	if err != nil {
		return nil, err
	}
	return true, nil
}

func (h *Handler) load(ctx context.Context, raw json.RawMessage) (any, error) {
	if h.deps.Profiles == nil {
		return nil, errUnavailableMethod
	}
	var params struct {
		Profile string `json:"profile,omitempty"`
		Scope   string `json:"scope,omitempty"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Profile == "" {
		params.Profile = "jcli-prod"
	}
	if err := validateScope(params.Scope); err != nil {
		return nil, err
	}
	var err error
	if scoped, ok := h.deps.Profiles.(scopedProfileService); ok {
		err = scoped.LoadScope(ctx, params.Profile, normalizedScope(params.Scope))
	} else {
		err = h.deps.Profiles.Load(ctx, params.Profile)
	}
	if err != nil {
		return nil, err
	}
	return true, nil
}

func validateScope(scope string) error {
	switch scope {
	case "", "all", "groups", "users", "moroutes", "mtroutes", "mointerceptors", "mtinterceptors", "connectors":
		return nil
	default:
		return fmt.Errorf("%w: invalid profile scope %q", admin.ErrInvalidRequest, scope)
	}
}

func normalizedScope(scope string) string {
	if scope == "" {
		return "all"
	}
	return scope
}

func (h *Handler) submitSM(ctx context.Context, raw json.RawMessage) (any, error) {
	if h.deps.Submitter == nil {
		return nil, errUnavailableMethod
	}
	var params struct {
		Username    string           `json:"username,omitempty"`
		UserID      string           `json:"user_id,omitempty"`
		ConnectorID string           `json:"connector_id"`
		PDU         *smppwire.SMBody `json:"pdu"`
		PDUWire     []byte           `json:"pdu_wire,omitempty"`
		PDUWires    [][]byte         `json:"pdu_wires,omitempty"`
		Bill        *struct {
			ID                     string  `json:"id,omitempty"`
			SubmitSMAmount         float64 `json:"submit_sm_amount"`
			SubmitSMRespAmount     float64 `json:"submit_sm_resp_amount"`
			DecrementSubmitSMCount int     `json:"decrement_submit_sm_count"`
		} `json:"bill,omitempty"`
		DLRURL          string `json:"dlr_url,omitempty"`
		DLRLevel        int    `json:"dlr_level,omitempty"`
		DLRMethod       string `json:"dlr_method,omitempty"`
		DLRConnector    string `json:"dlr_connector,omitempty"`
		SourceConnector string `json:"source_connector,omitempty"`
		Priority        *int   `json:"priority,omitempty"`
		ValidityPeriod  string `json:"validity_period,omitempty"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	var submitChain []*smppwire.SubmitSMBody
	if len(params.PDUWires) > 0 {
		if len(params.PDUWire) > 0 || params.PDU != nil {
			return nil, fmt.Errorf("%w: use exactly one of pdu, pdu_wire or pdu_wires", admin.ErrInvalidRequest)
		}
		for index, wire := range params.PDUWires {
			body, err := decodeSubmitSMWire(wire)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid pdu_wires[%d]: %v", admin.ErrInvalidRequest, index, err)
			}
			submitChain = append(submitChain, body)
		}
		params.PDU = submitChain[0]
	} else if len(params.PDUWire) > 0 {
		if params.PDU != nil {
			return nil, fmt.Errorf("%w: use exactly one of pdu or pdu_wire", admin.ErrInvalidRequest)
		}
		body, err := decodeSubmitSMWire(params.PDUWire)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid submit_sm wire: %v", admin.ErrInvalidRequest, err)
		}
		params.PDU = body
		submitChain = []*smppwire.SubmitSMBody{body}
	}
	if params.PDU == nil {
		return nil, fmt.Errorf("%w: pdu is required", admin.ErrInvalidRequest)
	}
	if params.ConnectorID == "" {
		return nil, fmt.Errorf("%w: connector_id is required", admin.ErrInvalidRequest)
	}
	if h.deps.Connectors == nil {
		return nil, errUnavailableMethod
	}
	if _, err := h.deps.Connectors.GetConnector(ctx, params.ConnectorID); err != nil {
		return nil, err
	}
	username := params.Username
	if username == "" && params.UserID != "" {
		var err error
		username, err = h.usernameForExternalID(ctx, params.UserID)
		if err != nil {
			return nil, err
		}
	}
	if username == "" {
		return nil, fmt.Errorf("%w: username or user_id is required", admin.ErrInvalidRequest)
	}
	sourceConnector := params.SourceConnector
	if sourceConnector == "" {
		sourceConnector = "httpapi"
	}
	priority := int(params.PDU.PriorityFlag)
	if params.Priority != nil {
		priority = *params.Priority
	}
	if priority < 0 || priority > 3 {
		return nil, fmt.Errorf("%w: priority must be between 0 and 3", admin.ErrInvalidRequest)
	}
	request := core.SubmitRequest{
		Username:        username,
		Destination:     string(params.PDU.DestinationAddress),
		HexContent:      hex.EncodeToString(params.PDU.ShortMessage),
		From:            string(params.PDU.SourceAddress),
		Coding:          int(params.PDU.DataCoding),
		Priority:        priority,
		DLR:             params.PDU.RegisteredDelivery&0x03 != 0,
		DLRUrl:          params.DLRURL,
		DLRLevel:        params.DLRLevel,
		DLRMethod:       params.DLRMethod,
		SourceConnector: sourceConnector,
		ESMClass:        params.PDU.ESMClass,
		SMPPSubmit:      params.PDU,
		TrustedManagerSubmit: &core.TrustedManagerSubmit{
			ConnectorID:    params.ConnectorID,
			DLRConnector:   params.DLRConnector,
			ValidityPeriod: params.ValidityPeriod,
			SubmitSMChain:  submitChain,
		},
	}
	if params.Bill != nil {
		request.TrustedManagerSubmit.HasBill = true
		request.TrustedManagerSubmit.BillID = params.Bill.ID
		request.TrustedManagerSubmit.Bill = billing.Bill{
			SubmitSmAmount:         params.Bill.SubmitSMAmount,
			SubmitSmRespAmount:     params.Bill.SubmitSMRespAmount,
			DecrementSubmitSmCount: params.Bill.DecrementSubmitSMCount,
			AuthorizationAmount:    params.Bill.SubmitSMAmount + params.Bill.SubmitSMRespAmount,
		}
		if err := billing.ValidateBill(request.TrustedManagerSubmit.Bill); err != nil {
			return nil, fmt.Errorf("%w: invalid bill: %v", admin.ErrInvalidRequest, err)
		}
	}
	for _, item := range params.PDU.CapturedVendorTLVs {
		length := len(item.Value)
		request.CustomTLVs = append(request.CustomTLVs, tlv.TLV{
			Tag:    new(big.Int).SetUint64(uint64(item.Tag)),
			Length: &length,
			Type:   "OctetString",
			Value:  append([]byte(nil), item.Value...),
		})
	}
	messageID, err := h.deps.Submitter.Submit(ctx, request)
	if err != nil {
		return nil, err
	}
	return messageID, nil
}

func decodeSubmitSMWire(wire []byte) (*smppwire.SubmitSMBody, error) {
	decoded, err := smppwire.Decode(wire)
	if err != nil {
		return nil, err
	}
	if decoded.Header.CommandID != smppwire.CommandSubmitSM || decoded.SM == nil {
		return nil, errors.New("wire value must contain submit_sm")
	}
	return decoded.SM, nil
}

func (h *Handler) usernameForExternalID(ctx context.Context, externalID string) (string, error) {
	if h.deps.Users == nil {
		return "", errUnavailableMethod
	}
	users, err := h.deps.Users.ListUsers(ctx)
	if err != nil {
		return "", err
	}
	for _, user := range users {
		var spec struct {
			ExternalID string `json:"external_id"`
		}
		if json.Unmarshal([]byte(user.SpecJSON), &spec) == nil && spec.ExternalID == externalID {
			return user.Username, nil
		}
	}
	return "", admin.ErrUserNotFound
}

func (h *Handler) unbind(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if h.deps.SMPPServer == nil {
		return nil, errUnavailableMethod
	}
	var params idParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if method == "smpps.ban" {
		if h.deps.Users == nil {
			return nil, errUnavailableMethod
		}
		if err := h.setUserSMPPBind(ctx, params.ID, false); err != nil {
			return nil, err
		}
		h.markDirty()
	}
	return h.deps.SMPPServer.UnbindUser(params.ID), nil
}

func (h *Handler) deliver(ctx context.Context, raw json.RawMessage) (any, error) {
	if h.deps.SMPPServer == nil {
		return nil, errUnavailableMethod
	}
	var params struct {
		SystemID string       `json:"system_id"`
		PDU      smppwire.PDU `json:"pdu"`
		PDUWire  []byte       `json:"pdu_wire,omitempty"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if len(params.PDUWire) > 0 {
		decoded, err := smppwire.Decode(params.PDUWire)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid deliver wire: %v", admin.ErrInvalidRequest, err)
		}
		if decoded.SM != nil {
			decoded.SM.VendorTLVs = capturedVendorWire(decoded.SM.CapturedVendorTLVs)
		}
		params.PDU = decoded
	}
	if params.SystemID == "" || params.PDU.SM == nil {
		return nil, fmt.Errorf("%w: system_id and pdu.sm are required", admin.ErrInvalidRequest)
	}
	if params.PDU.Header.CommandID != smppwire.CommandDeliverSM &&
		params.PDU.Header.CommandID != smppwire.CommandDataSM {
		return nil, fmt.Errorf("%w: pdu must be deliver_sm or data_sm", admin.ErrInvalidRequest)
	}
	if err := h.deps.SMPPServer.Deliver(ctx, params.SystemID, params.PDU); err != nil {
		// Frozen SMPPServerPB returns false when no deliverer is available.
		return false, nil
	}
	return true, nil
}

func capturedVendorWire(items []smppwire.CapturedVendorTLV) []byte {
	var result []byte
	for _, item := range items {
		offset := len(result)
		result = append(result, 0, 0, 0, 0)
		binary.BigEndian.PutUint16(result[offset:offset+2], item.Tag)
		binary.BigEndian.PutUint16(result[offset+2:offset+4], uint16(len(item.Value)))
		result = append(result, item.Value...)
	}
	return result
}

func (h *Handler) runScript(ctx context.Context, raw json.RawMessage) (any, error) {
	if h.deps.ScriptRunner == nil {
		return nil, errUnavailableMethod
	}
	var params struct {
		Script      string   `json:"script"`
		Direction   string   `json:"direction,omitempty"`
		ConnectorID string   `json:"connector_id,omitempty"`
		UserID      int64    `json:"user_id,omitempty"`
		GroupID     int64    `json:"group_id,omitempty"`
		Source      []byte   `json:"source_addr"`
		Destination []byte   `json:"destination_addr"`
		Message     []byte   `json:"short_message"`
		Tags        []string `json:"tags,omitempty"`
		Locked      []string `json:"locked,omitempty"`
		SMPPStatus  int      `json:"smpp_status,omitempty"`
		HTTPStatus  int      `json:"http_status,omitempty"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	direction := routingfilter.MT
	if strings.EqualFold(params.Direction, "mo") {
		direction = routingfilter.MO
	}
	routable, err := routingfilter.NewRoutable(routingfilter.RoutableInput{
		Direction:       direction,
		ConnectorID:     params.ConnectorID,
		UserID:          params.UserID,
		GroupID:         params.GroupID,
		SourceAddr:      routingfilter.BytesField{Present: true, Value: params.Source},
		DestinationAddr: routingfilter.BytesField{Present: true, Value: params.Destination},
		ShortMessage:    routingfilter.BytesField{Present: true, Value: params.Message},
		Tags:            params.Tags,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", admin.ErrInvalidRequest, err)
	}
	for _, field := range params.Locked {
		routable.Lock(field)
	}
	result, err := h.deps.ScriptRunner.Run(ctx, interceptor.Script{
		IDValue: "pb-facade",
		PyCode:  params.Script,
	}, interceptor.Context{
		Routable:   routable,
		SMPPStatus: params.SMPPStatus,
		HTTPStatus: params.HTTPStatus,
	})
	if err != nil {
		// InterceptorPB returns False for every execution failure.
		return false, nil
	}
	return map[string]any{
		"source_addr":      result.Routable.SourceAddr().Value,
		"destination_addr": result.Routable.DestinationAddr().Value,
		"short_message":    result.Routable.ShortMessage().Value,
		"tags":             result.Routable.Tags(),
		"smpp_status":      result.SMPPStatus,
		"http_status":      result.HTTPStatus,
		"action":           result.Action,
		"execution_ms":     result.ExecutionMS,
	}, nil
}

func (h *Handler) connectorResult(view admin.ConnectorView) map[string]any {
	h.connectorStatsMu.Lock()
	stats := h.connectorStats[view.Config.CID]
	h.connectorStatsMu.Unlock()
	return map[string]any{
		"config":          view.Config,
		"desired_started": view.DesiredStarted,
		"observed":        view.Observed,
		"start_count":     stats.start,
		"stop_count":      stats.stop,
	}
}

func (h *Handler) connectorResults(views []admin.ConnectorView) []map[string]any {
	result := make([]map[string]any, 0, len(views))
	for _, view := range views {
		result = append(result, h.connectorResult(view))
	}
	return result
}

func (h *Handler) recordConnectorLifecycle(cid string, start bool) {
	h.connectorStatsMu.Lock()
	defer h.connectorStatsMu.Unlock()
	stats := h.connectorStats[cid]
	if start {
		stats.start++
	} else {
		stats.stop++
	}
	h.connectorStats[cid] = stats
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
