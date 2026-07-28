// Package adminweb serves the browser management UI for the gateway: an
// embedded single-page app (web/, built into dist/) plus the JSON backend it
// talks to (/api/*), calling the in-process admin services for connectors,
// routes and users. It is a privilege boundary — it can mint credentials and
// start connectors — so it is served on its own listener (separate from the
// public sendsms port), behind a session cookie, with CSRF checks on every
// state-changing request.
package adminweb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"

	"github.com/pumpitspace/jasmin/internal/app/admin"
)

// HealthFunc reports gateway readiness for the dashboard: the overall status
// ("ok"/"degraded") and the per-check detail, mirroring the /health endpoint.
type HealthFunc func(ctx context.Context) (status string, checks map[string]string)

// Deps are the collaborators the UI acts against. Connectors/Routes/Users are
// the in-process admin services built in the gateway runtime; Health is the
// shared readiness probe (optional). Username/Password are the single admin
// login (Password is the resolved plaintext secret; it is hashed at
// construction and not retained). Secure marks the session cookie Secure (set
// when the UI listener terminates TLS).
type Deps struct {
	Connectors *admin.Service
	Routes     *admin.RouteService
	MORoutes   *admin.MORouteService
	Users      *admin.UserService
	SMPPsUsers *admin.SMPPsUserService
	// Interceptors is nil unless admin.allow_interceptor_editing is set. When
	// nil the /api/interceptors endpoints answer 404 and the UI hides the
	// section — interceptor scripts are arbitrary Python on this host.
	Interceptors *admin.InterceptorService
	Health       HealthFunc
	Username     string
	Password     string
	Secure       bool
}

// Handler is the adminweb HTTP handler. It is the whole server on the UI's
// dedicated listener: /api/* is the session-authenticated JSON backend, every
// other GET serves the embedded SPA (with index.html fallback for client-side
// routes).
type Handler struct {
	deps         Deps
	usernameHash [32]byte
	passwordHash [32]byte
	signingKey   []byte // boot-random HMAC key for session + CSRF signing
	mux          http.Handler
}

// New builds the handler: it validates the login credential, generates an
// ephemeral session-signing key, and wires the routes. The signing key is
// per-process, so sessions do not survive a restart (admins re-authenticate) —
// a deliberate v1 simplification.
func New(deps Deps) (*Handler, error) {
	if deps.Username == "" || deps.Password == "" {
		return nil, errors.New("adminweb: web_username and web_password are required")
	}
	if deps.Connectors == nil || deps.Routes == nil || deps.MORoutes == nil ||
		deps.Users == nil || deps.SMPPsUsers == nil {
		return nil, errors.New("adminweb: connector, route, MO route, user and SMPPs user services are required")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("adminweb: generate signing key: %w", err)
	}
	h := &Handler{
		deps:         deps,
		usernameHash: sha256.Sum256([]byte(deps.Username)),
		passwordHash: sha256.Sum256([]byte(deps.Password)),
		signingKey:   key,
	}
	h.deps.Password = "" // do not retain the plaintext secret
	h.mux = h.routes()
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// routes builds the mux: /api/login is the only unauthenticated API endpoint,
// the rest of /api is behind requireSession, and the SPA (static, public — the
// login page itself) answers every other GET.
func (h *Handler) routes() http.Handler {
	mux := http.NewServeMux()
	authed := func(fn http.HandlerFunc) http.Handler { return h.requireSession(fn) }

	mux.HandleFunc("POST /api/login", h.handleLogin)
	mux.Handle("POST /api/logout", authed(h.handleLogout))
	mux.Handle("GET /api/session", authed(h.handleSession))
	mux.Handle("GET /api/health", authed(h.handleHealth))

	mux.Handle("GET /api/connectors", authed(h.listConnectors))
	mux.Handle("POST /api/connectors", authed(h.createConnector))
	mux.Handle("GET /api/connectors/{cid}", authed(h.getConnector))
	mux.Handle("PATCH /api/connectors/{cid}", authed(h.updateConnector))
	mux.Handle("PUT /api/connectors/{cid}", authed(h.updateConnector))
	mux.Handle("DELETE /api/connectors/{cid}", authed(h.deleteConnector))

	mux.Handle("GET /api/routes", authed(h.listRoutes))
	mux.Handle("POST /api/routes", authed(h.createRoute))
	mux.Handle("GET /api/routes/{order}", authed(h.getRoute))
	mux.Handle("PATCH /api/routes/{order}", authed(h.updateRoute))
	mux.Handle("PUT /api/routes/{order}", authed(h.updateRoute))
	mux.Handle("DELETE /api/routes/{order}", authed(h.deleteRoute))

	mux.Handle("GET /api/mo-routes", authed(h.listMORoutes))
	mux.Handle("POST /api/mo-routes", authed(h.createMORoute))
	mux.Handle("GET /api/mo-routes/{order}", authed(h.getMORoute))
	mux.Handle("PATCH /api/mo-routes/{order}", authed(h.updateMORoute))
	mux.Handle("PUT /api/mo-routes/{order}", authed(h.updateMORoute))
	mux.Handle("DELETE /api/mo-routes/{order}", authed(h.deleteMORoute))

	mux.Handle("GET /api/smpps-users", authed(h.listSMPPsUsers))
	mux.Handle("POST /api/smpps-users", authed(h.createSMPPsUser))
	mux.Handle("GET /api/smpps-users/{systemID}", authed(h.getSMPPsUser))
	mux.Handle("PATCH /api/smpps-users/{systemID}", authed(h.updateSMPPsUser))
	mux.Handle("PUT /api/smpps-users/{systemID}", authed(h.updateSMPPsUser))
	mux.Handle("DELETE /api/smpps-users/{systemID}", authed(h.deleteSMPPsUser))

	mux.Handle("GET /api/interceptors", authed(h.listInterceptors))
	mux.Handle("POST /api/interceptors", authed(h.createInterceptor))
	mux.Handle("GET /api/interceptors/{id}", authed(h.getInterceptor))
	mux.Handle("PATCH /api/interceptors/{id}", authed(h.updateInterceptor))
	mux.Handle("PUT /api/interceptors/{id}", authed(h.updateInterceptor))
	mux.Handle("DELETE /api/interceptors/{id}", authed(h.deleteInterceptor))

	mux.Handle("GET /api/users", authed(h.listUsers))
	mux.Handle("POST /api/users", authed(h.createUser))
	mux.Handle("GET /api/users/{username}", authed(h.getUser))
	mux.Handle("PATCH /api/users/{username}", authed(h.updateUser))
	mux.Handle("PUT /api/users/{username}", authed(h.updateUser))
	mux.Handle("DELETE /api/users/{username}", authed(h.deleteUser))

	// Unmatched /api paths must 404 as JSON, never fall through to the SPA.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", spaHandler())
	return mux
}

// currentUser is the authenticated username stored in the request context by
// requireSession.
type ctxKey int

const userKey ctxKey = 0

func currentUser(ctx context.Context) string {
	name, _ := ctx.Value(userKey).(string)
	return name
}
