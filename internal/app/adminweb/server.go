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
	"time"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/modispatch"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/app/smppsserver"
	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/core/termination"
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
	Groups     *admin.GroupService
	SMPPsUsers *admin.SMPPsUserService
	Filters    *admin.NamedSpecService
	// HTTPConnectors are named HTTP destinations copied into MO routes.
	HTTPConnectors *admin.NamedSpecService
	Profiles       *admin.ProfileService
	Transactions   *submittransaction.Service
	BalanceReader  core.BalanceReader
	RateReader     core.RateReader
	Submitter      core.Submitter
	HTTPStats      *stats.HTTPStats
	SMPPcStats     *stats.SMPPcRegistry
	SMPPsStats     *stats.SMPPsStats
	// Metrics is the production registry the runtime records into. The topology
	// map reads it through Snapshot() for per-connector submit, MO, DLR, queue
	// depth and spool figures. Nil is a supported state — the map then renders
	// structure with metrics_stale set, which is honest, where zeroed counters
	// would read as an idle gateway.
	Metrics      *stats.PrometheusRegistry
	StartedAt    func() time.Time
	ConnectorIDs func() []string
	// Config-owned entities are visible but read-only in the browser.
	ConfigConnectors func() []smppc.Config
	ConfigRoutes     func() []outbound.RouteConfig
	ConfigMORoutes   func() []modispatch.RouteConfig
	ConfigUsers      func() []outbound.UserConfig
	ConfigGroups     func() []outbound.GroupConfig
	ConfigSMPPsUsers func() []smppsserver.UserConfig
	ConnectorStatus  func(string) (smppc.ManagedStatus, error)
	// UnbindSMPPsUser gracefully disconnects every live session for a system_id.
	UnbindSMPPsUser func(string) int
	// Interceptors is nil unless admin.allow_interceptor_editing is set. When
	// nil the /api/interceptors endpoints answer 404 and the UI hides the
	// section — interceptor scripts are arbitrary Python on this host.
	Interceptors *admin.InterceptorService
	// CDR is the authorization-and-audit enforcing commercial record service.
	// Nil leaves every /api/billing/cdrs route answering 503 rather than an
	// empty list — "no records service" and "no records" are different answers
	// to "what did this customer send".
	CDR *cdr.Service
	// Ingress reports which customer-facing listeners this deployment actually
	// publishes, for the generated partner integration instructions. Nil means
	// the gateway did not report them, and every channel is then described as
	// "endpoint unknown" rather than given a fabricated address.
	Ingress func() IngressSnapshot
	// GroupQuota reports a group's live shared ceiling; BillingSettings reports
	// the deployment-wide commercial configuration. Both are optional: without
	// them the billing views omit those fields instead of showing zeros.
	GroupQuota      GroupQuotaFunc
	BillingSettings func() BillingSettings
	// TerminationConnectors is the CRUD surface for connectors that terminate MT
	// traffic locally. Nil answers 404 on /api/termination-connectors: a gateway
	// with no termination manager cannot honour one, and an empty list would
	// read as "none configured".
	TerminationConnectors *admin.TerminationService
	// ConfigTerminationConnectors are the config-declared termination
	// connectors: visible in the console, refused by the admin services.
	ConfigTerminationConnectors func() []termination.ConnectorConfig
	// TerminationStatus reports a config-owned termination connector's live
	// state, so the console's columns are true for the rows the admin service
	// knows nothing about. It mirrors ConnectorStatus.
	TerminationStatus func(string) (termination.ManagedStatus, error)
	// MessageConsumers is the CRUD surface for the scoped, read-only credentials
	// the message pull API authenticates. Nil answers 404 on
	// /api/message-consumers, for the same reason TerminationConnectors does: a
	// gateway with no message spool cannot honour one, and an empty list would
	// read as "none configured" and invite creating one that could never work.
	MessageConsumers *admin.MessageConsumerService
	// Messages is the operator's read path into the message spool. Nil answers
	// 404 on /api/messages, like MessageConsumers above. It is deliberately the
	// spool service itself and not a consumer credential: the console reads as
	// an audited subject of its own, never by borrowing a partner's token.
	Messages *msgspool.Service
	// Settings persists operator overrides for the few gateway settings whose
	// consumers can re-read them at runtime. Nil keeps the settings card
	// read-only, which is the honest state when nothing can apply a change.
	Settings *admin.SettingsService
	Health   HealthFunc
	Username string
	Password string
	Secure   bool
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
		deps.Users == nil || deps.Groups == nil || deps.SMPPsUsers == nil ||
		deps.Filters == nil || deps.HTTPConnectors == nil || deps.Profiles == nil {
		return nil, errors.New("adminweb: connector, route, MO route, user, group, SMPPs user, filter, HTTP connector and profile services are required")
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
	mux.Handle("GET /api/stats", authed(h.handleStats))
	// The whole deployment as one graph, for the console's topology map.
	mux.Handle("GET /api/topology", authed(h.handleTopology))
	mux.Handle("GET /api/message-status/{messageID}", authed(h.handleMessageStatus))
	mux.Handle("POST /api/tools/balance", authed(h.handleBalanceTool))
	mux.Handle("POST /api/tools/rate", authed(h.handleRateTool))
	mux.Handle("POST /api/tools/send", authed(h.handleSendTool))

	mux.Handle("GET /api/connectors", authed(h.listConnectors))
	mux.Handle("POST /api/connectors", authed(h.createConnector))
	mux.Handle("GET /api/connectors/{cid}", authed(h.getConnector))
	mux.Handle("PATCH /api/connectors/{cid}", authed(h.updateConnector))
	mux.Handle("PUT /api/connectors/{cid}", authed(h.updateConnector))
	mux.Handle("DELETE /api/connectors/{cid}", authed(h.deleteConnector))

	mux.Handle("GET /api/termination-connectors", authed(h.listTerminationConnectors))
	mux.Handle("POST /api/termination-connectors", authed(h.createTerminationConnector))
	mux.Handle("GET /api/termination-connectors/{cid}", authed(h.getTerminationConnector))
	mux.Handle("PATCH /api/termination-connectors/{cid}", authed(h.updateTerminationConnector))
	mux.Handle("PUT /api/termination-connectors/{cid}", authed(h.updateTerminationConnector))
	mux.Handle("DELETE /api/termination-connectors/{cid}", authed(h.deleteTerminationConnector))
	mux.Handle("POST /api/termination-connectors/{cid}/start", authed(h.startTerminationConnector))
	mux.Handle("POST /api/termination-connectors/{cid}/stop", authed(h.stopTerminationConnector))

	// Message pull credentials: scoped, read-only tokens a downstream
	// application uses to fetch decoded messages by cursor.
	// The operator's read path into the message spool. Metadata by default;
	// content only with ?include_content=true, which is audited as a reveal.
	mux.Handle("GET /api/messages", authed(h.listMessages))
	mux.Handle("GET /api/messages/{messageID}", authed(h.getMessage))

	mux.Handle("GET /api/message-consumers", authed(h.listMessageConsumers))
	mux.Handle("POST /api/message-consumers", authed(h.createMessageConsumer))
	mux.Handle("GET /api/message-consumers/{id}", authed(h.getMessageConsumer))
	mux.Handle("PATCH /api/message-consumers/{id}", authed(h.updateMessageConsumer))
	mux.Handle("PUT /api/message-consumers/{id}", authed(h.updateMessageConsumer))
	mux.Handle("DELETE /api/message-consumers/{id}", authed(h.deleteMessageConsumer))
	mux.Handle("POST /api/message-consumers/{id}/revoke", authed(h.revokeMessageConsumer))
	mux.Handle("POST /api/message-consumers/{id}/unrevoke", authed(h.unrevokeMessageConsumer))

	mux.Handle("GET /api/routes", authed(h.listRoutes))
	mux.Handle("POST /api/routes", authed(h.createRoute))
	mux.Handle("POST /api/routes/flush", authed(h.flushRoutes))
	mux.Handle("GET /api/routes/{order}", authed(h.getRoute))
	mux.Handle("PATCH /api/routes/{order}", authed(h.updateRoute))
	mux.Handle("PUT /api/routes/{order}", authed(h.updateRoute))
	mux.Handle("DELETE /api/routes/{order}", authed(h.deleteRoute))

	mux.Handle("GET /api/mo-routes", authed(h.listMORoutes))
	mux.Handle("POST /api/mo-routes", authed(h.createMORoute))
	mux.Handle("POST /api/mo-routes/flush", authed(h.flushMORoutes))
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
	mux.Handle("POST /api/smpps-users/{systemID}/unbind", authed(h.unbindSMPPsUser))
	mux.Handle("POST /api/smpps-users/{systemID}/ban", authed(h.banSMPPsUser))

	mux.Handle("GET /api/interceptors", authed(h.listInterceptors))
	mux.Handle("POST /api/interceptors", authed(h.createInterceptor))
	mux.Handle("POST /api/interceptors/{direction}/flush", authed(h.flushInterceptors))
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

	mux.Handle("GET /api/groups", authed(h.listGroups))
	mux.Handle("POST /api/groups", authed(h.createGroup))
	mux.Handle("GET /api/groups/{gid}", authed(h.getGroup))
	mux.Handle("PATCH /api/groups/{gid}", authed(h.updateGroup))
	mux.Handle("PUT /api/groups/{gid}", authed(h.updateGroup))
	mux.Handle("DELETE /api/groups/{gid}", authed(h.deleteGroup))

	mux.Handle("GET /api/filters", authed(h.listFilters))
	mux.Handle("POST /api/filters", authed(h.createFilter))
	mux.Handle("GET /api/filters/{fid}", authed(h.getFilter))
	mux.Handle("PATCH /api/filters/{fid}", authed(h.updateFilter))
	mux.Handle("PUT /api/filters/{fid}", authed(h.updateFilter))
	mux.Handle("DELETE /api/filters/{fid}", authed(h.deleteFilter))

	mux.Handle("GET /api/http-connectors", authed(h.listHTTPConnectors))
	mux.Handle("POST /api/http-connectors", authed(h.createHTTPConnector))
	mux.Handle("GET /api/http-connectors/{cid}", authed(h.getHTTPConnector))
	mux.Handle("PATCH /api/http-connectors/{cid}", authed(h.updateHTTPConnector))
	mux.Handle("PUT /api/http-connectors/{cid}", authed(h.updateHTTPConnector))
	mux.Handle("DELETE /api/http-connectors/{cid}", authed(h.deleteHTTPConnector))

	mux.Handle("POST /api/profiles/{profile}/save", authed(h.saveProfile))
	mux.Handle("POST /api/profiles/{profile}/load", authed(h.loadProfile))

	// Partner onboarding provisions the group, user, bind account, connector and
	// route together, or undoes what it created.
	mux.Handle("POST /api/onboarding/partners", authed(h.onboardPartner))

	// Integration: what a partner needs in order to send us traffic, derived
	// from this account's live authorizations, filters and quota.
	mux.Handle("GET /api/integration/users/{username}", authed(h.userIntegrationGuide))
	mux.Handle("GET /api/integration/connectors/{cid}", authed(h.connectorIntegrationBrief))

	// Billing: provisioned grants live on /api/users and /api/groups; these
	// routes serve what is left now and what was actually charged.
	mux.Handle("GET /api/billing/accounts", authed(h.listBillingAccounts))
	mux.Handle("GET /api/billing/settings", authed(h.getBillingSettings))
	mux.Handle("PUT /api/billing/settings", authed(h.updateBillingSettings))
	mux.Handle("GET /api/billing/summary", authed(h.summarizeUsage))
	mux.Handle("GET /api/billing/export", authed(h.exportCDRs))
	mux.Handle("GET /api/billing/cdrs", authed(h.searchCDRs))
	mux.Handle("GET /api/billing/cdrs/{id}", authed(h.getCDR))
	mux.Handle("GET /api/billing/cdrs/{id}/events", authed(h.listCDREvents))
	mux.Handle("POST /api/billing/reconcile", authed(h.reconcileCDRs))
	mux.Handle("POST /api/billing/prune", authed(h.pruneCDRs))

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
