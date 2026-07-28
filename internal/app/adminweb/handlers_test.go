package adminweb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/modispatch"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// fakeManager records live-apply calls and reports every connector unbound.
type fakeManager struct {
	added   map[string]smppc.Config
	started map[string]bool
}

func newFakeManager() *fakeManager {
	return &fakeManager{added: map[string]smppc.Config{}, started: map[string]bool{}}
}

func (m *fakeManager) Add(cfg smppc.Config) error {
	if _, ok := m.added[cfg.CID]; ok {
		return fmt.Errorf("connector %q already managed", cfg.CID)
	}
	m.added[cfg.CID] = cfg
	return nil
}

func (m *fakeManager) Update(cfg smppc.Config) error {
	if _, ok := m.added[cfg.CID]; !ok {
		return fmt.Errorf("connector %q not managed", cfg.CID)
	}
	m.added[cfg.CID] = cfg
	return nil
}

func (m *fakeManager) Remove(cid string) error {
	delete(m.added, cid)
	delete(m.started, cid)
	return nil
}

func (m *fakeManager) Start(cid string) error { m.started[cid] = true; return nil }
func (m *fakeManager) Stop(cid string) error  { m.started[cid] = false; return nil }
func (m *fakeManager) Status(cid string) (smppc.ManagedStatus, error) {
	cfg, ok := m.added[cid]
	if !ok {
		return smppc.ManagedStatus{}, fmt.Errorf("connector %q not managed", cid)
	}
	return smppc.ManagedStatus{CID: cid, Desired: m.started[cid], Observed: smppc.StatusDisconnected, Config: cfg}, nil
}

// fakeRouteProvisioner accepts every route spec that is valid RouteConfig JSON
// with at least one connector candidate, mimicking the outbound validation
// surface the UI must map to inline errors.
type fakeRouteProvisioner struct{ applied [][]string }

func (p *fakeRouteProvisioner) ApplyRoutes(_ context.Context, specs []string) error {
	for _, spec := range specs {
		var cfg outbound.RouteConfig
		if err := json.Unmarshal([]byte(spec), &cfg); err != nil {
			return err
		}
		if len(cfg.ConnectorCandidates()) == 0 {
			return fmt.Errorf("route %d: a connector is required", cfg.Order)
		}
	}
	p.applied = append(p.applied, specs)
	return nil
}

// fakeMORouteProvisioner accepts every MO route spec that is valid
// modispatch.RouteConfig JSON with a usable destination connector.
type fakeMORouteProvisioner struct{ applied [][]string }

func (p *fakeMORouteProvisioner) ApplyMORoutes(_ context.Context, specs []string) error {
	for _, spec := range specs {
		var cfg modispatch.RouteConfig
		if err := json.Unmarshal([]byte(spec), &cfg); err != nil {
			return err
		}
		switch cfg.Connector.Type {
		case "http":
			if cfg.Connector.CID == "" || cfg.Connector.URL == "" {
				return fmt.Errorf("MO route %d: http connector requires cid and url", cfg.Order)
			}
		case "smpps":
			if cfg.Connector.SystemID == "" {
				return fmt.Errorf("MO route %d: smpps connector requires system_id", cfg.Order)
			}
		default:
			return fmt.Errorf("MO route %d: connector type %q", cfg.Order, cfg.Connector.Type)
		}
	}
	p.applied = append(p.applied, specs)
	return nil
}

// fakeSMPPsUserProvisioner validates like the real directory: a system_id and
// password are required and system_ids must be unique.
type fakeSMPPsUserProvisioner struct{ applied []smppsserver.UserConfig }

func (p *fakeSMPPsUserProvisioner) ApplySMPPsUsers(_ context.Context, specs []string) error {
	seen := map[string]struct{}{}
	users := make([]smppsserver.UserConfig, 0, len(specs))
	for _, spec := range specs {
		var cfg smppsserver.UserConfig
		if err := json.Unmarshal([]byte(spec), &cfg); err != nil {
			return err
		}
		if cfg.SystemID == "" || cfg.Password == "" {
			return fmt.Errorf("SMPPs user %q: system_id and password are required", cfg.SystemID)
		}
		if _, dup := seen[cfg.SystemID]; dup {
			return fmt.Errorf("duplicate system_id %q", cfg.SystemID)
		}
		seen[cfg.SystemID] = struct{}{}
		users = append(users, cfg)
	}
	p.applied = users
	return nil
}

// fakeUserProvisioner validates specs like the live directory: username and a
// 64-hex password_sha256 are required.
type fakeUserProvisioner struct {
	installed map[string]string // username -> specJSON
	uids      map[string]int64
}

func newFakeUserProvisioner() *fakeUserProvisioner {
	return &fakeUserProvisioner{installed: map[string]string{}, uids: map[string]int64{}}
}

func (p *fakeUserProvisioner) AddUser(username, specJSON string, uid int64) error {
	var cfg outbound.UserConfig
	if err := json.Unmarshal([]byte(specJSON), &cfg); err != nil {
		return err
	}
	if cfg.Username != username || len(cfg.PasswordSHA256) != 64 {
		return fmt.Errorf("user %q: invalid spec", username)
	}
	p.installed[username] = specJSON
	p.uids[username] = uid
	return nil
}

func (p *fakeUserProvisioner) RemoveUser(username string) error {
	delete(p.installed, username)
	return nil
}

func (p *fakeUserProvisioner) ConfigUserFloor() int64 { return 2 }

// webFixture is a fully wired Handler over an in-memory store plus an
// authenticated client state (session cookie + CSRF token).
type webFixture struct {
	handler    *Handler
	deps       Deps
	store      *admin.Store
	manager    *fakeManager
	users      *fakeUserProvisioner
	smppsUsers *fakeSMPPsUserProvisioner
	cookie     *http.Cookie
	csrf       string
	t          *testing.T
}

// rebuildHandler re-creates the handler with mutated deps (used to enable an
// opt-in capability) and re-authenticates, since the signing key is per-handler.
func (f *webFixture) rebuildHandler(mutate func(*Deps)) {
	f.t.Helper()
	deps := f.deps
	deps.Password = "s3cret" // New() clears it on the stored copy
	mutate(&deps)
	handler, err := New(deps)
	if err != nil {
		f.t.Fatalf("rebuild handler: %v", err)
	}
	f.handler = handler
	f.deps = deps
	f.login()
}

func newWebFixture(t *testing.T) *webFixture {
	t.Helper()
	ctx := context.Background()
	store, err := admin.OpenStore(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := func() string { return "2026-07-27T00:00:00Z" }
	manager := newFakeManager()
	service, err := admin.NewService(store, manager, []string{"reserved-cid"}, now)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	routeService, err := admin.NewRouteService(store, &fakeRouteProvisioner{}, now)
	if err != nil {
		t.Fatalf("new route service: %v", err)
	}
	moRouteService, err := admin.NewMORouteService(store, &fakeMORouteProvisioner{}, now)
	if err != nil {
		t.Fatalf("new MO route service: %v", err)
	}
	smppsUsers := &fakeSMPPsUserProvisioner{}
	smppsUserService, err := admin.NewSMPPsUserService(store, smppsUsers, now)
	if err != nil {
		t.Fatalf("new SMPPs user service: %v", err)
	}
	userProv := newFakeUserProvisioner()
	userService, err := admin.NewUserService(store, userProv, now)
	if err != nil {
		t.Fatalf("new user service: %v", err)
	}
	deps := Deps{
		Connectors: service,
		Routes:     routeService,
		MORoutes:   moRouteService,
		Users:      userService,
		SMPPsUsers: smppsUserService,
		Health: func(context.Context) (string, map[string]string) {
			return "ok", map[string]string{"postgres": "ok"}
		},
		Username: "admin",
		Password: "s3cret",
	}
	handler, err := New(deps)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	f := &webFixture{handler: handler, deps: deps, store: store, manager: manager, users: userProv, smppsUsers: smppsUsers, t: t}
	f.login()
	return f
}

func (f *webFixture) login() {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login",
		bytes.NewBufferString(`{"username":"admin","password":"s3cret"}`)))
	if rec.Code != http.StatusOK {
		f.t.Fatalf("login: code %d body %s", rec.Code, rec.Body.String())
	}
	f.cookie = rec.Result().Cookies()[0]
	var payload sessionPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		f.t.Fatalf("login payload: %v", err)
	}
	f.csrf = payload.CSRFToken
}

// do performs an authenticated request and decodes the JSON response into out
// (skipped when out is nil), asserting the expected status.
func (f *webFixture) do(method, target string, body string, wantStatus int, out any) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader *bytes.Buffer
	if body == "" {
		reader = bytes.NewBufferString("")
	} else {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.AddCookie(f.cookie)
	req.Header.Set(csrfHeader, f.csrf)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		f.t.Fatalf("%s %s: code %d want %d body %s", method, target, rec.Code, wantStatus, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			f.t.Fatalf("%s %s: decode response: %v (%s)", method, target, err, rec.Body.String())
		}
	}
	return rec
}

func TestConnectorCRUD(t *testing.T) {
	f := newWebFixture(t)

	var created connectorResource
	f.do("POST", "/api/connectors",
		`{"cid":"smsc-a","host":"smsc.example.com","port":2775,"system_id":"jasmin","password":"pw1","bind":"transceiver","desired_started":true}`,
		http.StatusCreated, &created)
	if created.ID != "smsc-a" || !created.DesiredStarted {
		t.Fatalf("created: %+v", created)
	}
	if created.Password != "" {
		t.Fatalf("response leaked the bind password: %+v", created)
	}
	if !f.manager.started["smsc-a"] {
		t.Fatal("desired_started did not start the connector")
	}

	var listed []connectorResource
	rec := f.do("GET", "/api/connectors", "", http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].ID != "smsc-a" {
		t.Fatalf("list: %+v", listed)
	}
	if rec.Header().Get("X-Total-Count") != "1" {
		t.Fatalf("X-Total-Count = %q", rec.Header().Get("X-Total-Count"))
	}

	// Update with an empty password keeps the stored one; host changes apply.
	var updated connectorResource
	f.do("PATCH", "/api/connectors/smsc-a",
		`{"cid":"smsc-a","host":"other.example.com","port":2775,"system_id":"jasmin","password":"","bind":"transceiver","desired_started":false}`,
		http.StatusOK, &updated)
	if updated.Host != "other.example.com" {
		t.Fatalf("host not updated: %+v", updated)
	}
	if f.manager.added["smsc-a"].Password != "pw1" {
		t.Fatalf("empty password overwrote the stored one: %q", f.manager.added["smsc-a"].Password)
	}
	if f.manager.started["smsc-a"] {
		t.Fatal("desired_started=false did not stop the connector")
	}

	// Reserved (config-owned) connectors are rejected as conflicts.
	f.do("POST", "/api/connectors",
		`{"cid":"reserved-cid","host":"h","port":2775,"system_id":"s","password":"p"}`,
		http.StatusConflict, nil)

	f.do("DELETE", "/api/connectors/smsc-a", "", http.StatusOK, nil)
	f.do("GET", "/api/connectors/smsc-a", "", http.StatusNotFound, nil)

	// A running connector deletes in one call (the handler stops it first);
	// the service alone would refuse with "must be stopped before removal".
	f.do("POST", "/api/connectors",
		`{"cid":"smsc-live","host":"h","port":2775,"system_id":"jasmin","password":"pw","bind":"transceiver","desired_started":true}`,
		http.StatusCreated, nil)
	f.do("DELETE", "/api/connectors/smsc-live", "", http.StatusOK, nil)
	if _, ok := f.manager.added["smsc-live"]; ok {
		t.Fatal("delete did not remove the live connector")
	}
}

func TestRouteCRUD(t *testing.T) {
	f := newWebFixture(t)

	var created routeResource
	f.do("POST", "/api/routes",
		`{"order":10,"connector_id":"smsc-a","rate":0.05,"filters":[{"type":"destination_addr","pattern":"^\\+49"}]}`,
		http.StatusCreated, &created)
	if created.ID != 10 || created.ConnectorID != "smsc-a" || len(created.Filters) != 1 {
		t.Fatalf("created: %+v", created)
	}

	// A route without a connector is rejected by the provisioner → 400 inline.
	f.do("POST", "/api/routes", `{"order":20,"rate":0}`, http.StatusBadRequest, nil)

	var updated routeResource
	f.do("PATCH", "/api/routes/10", `{"order":10,"connector_id":"smsc-b","rate":0.07}`,
		http.StatusOK, &updated)
	if updated.ConnectorID != "smsc-b" || updated.Rate != 0.07 {
		t.Fatalf("updated: %+v", updated)
	}

	var listed []routeResource
	f.do("GET", "/api/routes", "", http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].ID != 10 {
		t.Fatalf("list: %+v", listed)
	}

	f.do("DELETE", "/api/routes/10", "", http.StatusOK, nil)
	f.do("GET", "/api/routes/10", "", http.StatusNotFound, nil)
}

func TestMORouteCRUD(t *testing.T) {
	f := newWebFixture(t)

	var created moRouteResource
	f.do("POST", "/api/mo-routes",
		`{"order":10,"filter_connector_id":"smsc-a","connector":{"type":"http","cid":"mo-sink","url":"http://localhost:9/mo","method":"GET"},"filters":[{"type":"short_message","pattern":".*STOP"}]}`,
		http.StatusCreated, &created)
	if created.ID != 10 || created.Connector.CID != "mo-sink" || len(created.Filters) != 1 {
		t.Fatalf("created: %+v", created)
	}

	// A destination the provisioner rejects surfaces as an inline 400.
	f.do("POST", "/api/mo-routes",
		`{"order":20,"filter_connector_id":"smsc-b","connector":{"type":"carrier-pigeon"}}`,
		http.StatusBadRequest, nil)

	// The default route (order 0) carries no filters.
	var defaultRoute moRouteResource
	f.do("POST", "/api/mo-routes",
		`{"order":0,"default":true,"connector":{"type":"smpps","system_id":"app-1"}}`,
		http.StatusCreated, &defaultRoute)
	if !defaultRoute.Default || defaultRoute.Connector.SystemID != "app-1" {
		t.Fatalf("default route: %+v", defaultRoute)
	}

	var updated moRouteResource
	f.do("PATCH", "/api/mo-routes/10",
		`{"connector":{"type":"smpps","system_id":"app-2"}}`,
		http.StatusOK, &updated)
	if updated.Connector.SystemID != "app-2" {
		t.Fatalf("updated: %+v", updated)
	}
	if updated.FilterConnectorID != "smsc-a" {
		t.Fatalf("partial PATCH dropped filter_connector_id: %+v", updated)
	}

	var listed []moRouteResource
	f.do("GET", "/api/mo-routes", "", http.StatusOK, &listed)
	if len(listed) != 2 {
		t.Fatalf("list: %+v", listed)
	}

	f.do("DELETE", "/api/mo-routes/10", "", http.StatusOK, nil)
	f.do("GET", "/api/mo-routes/10", "", http.StatusNotFound, nil)
}

func TestUserCRUD(t *testing.T) {
	f := newWebFixture(t)

	var created userResource
	f.do("POST", "/api/users",
		`{"username":"alice","password":"pw-alice","balance":100.5}`,
		http.StatusCreated, &created)
	if created.ID != "alice" || created.UID != 3 { // config floor 2 → first admin uid 3
		t.Fatalf("created: %+v", created)
	}
	if created.ExternalID != "alice" {
		t.Fatalf("external_id did not default to the username: %+v", created)
	}
	if created.Password != "" || strings.Contains(f.users.installed["alice"], "pw-alice") {
		t.Fatal("plaintext password leaked into the response or the stored spec")
	}
	var spec outbound.UserConfig
	if err := json.Unmarshal([]byte(f.users.installed["alice"]), &spec); err != nil {
		t.Fatalf("stored spec: %v", err)
	}
	if spec.PasswordSHA256 != hashPassword("pw-alice") {
		t.Fatalf("password_sha256 mismatch: %q", spec.PasswordSHA256)
	}
	hashAfterCreate := spec.PasswordSHA256

	// Update with an empty password keeps the hash; balance change applies.
	var updated userResource
	f.do("PATCH", "/api/users/alice", `{"username":"alice","balance":250}`,
		http.StatusOK, &updated)
	if updated.Balance == nil || *updated.Balance != 250 {
		t.Fatalf("balance not updated: %+v", updated)
	}
	if updated.UID != 3 {
		t.Fatalf("uid changed on update: %+v", updated)
	}
	var updatedSpec outbound.UserConfig
	if err := json.Unmarshal([]byte(f.users.installed["alice"]), &updatedSpec); err != nil {
		t.Fatalf("updated spec: %v", err)
	}
	if updatedSpec.PasswordSHA256 != hashAfterCreate {
		t.Fatal("empty password on update replaced the stored hash")
	}

	// Missing password on create is a 400.
	f.do("POST", "/api/users", `{"username":"bob"}`, http.StatusBadRequest, nil)

	f.do("DELETE", "/api/users/alice", "", http.StatusOK, nil)
	if _, ok := f.users.installed["alice"]; ok {
		t.Fatal("delete did not remove the live user")
	}
}

// fakeInterceptorProvisioner records applies per direction and rejects a spec
// with no script, mirroring what the real table build refuses.
type fakeInterceptorProvisioner struct {
	applied map[admin.InterceptorDirection][]string
}

func (p *fakeInterceptorProvisioner) ApplyInterceptors(_ context.Context, direction admin.InterceptorDirection, specs []string) error {
	for _, spec := range specs {
		var cfg outbound.InterceptorConfig
		if err := json.Unmarshal([]byte(spec), &cfg); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.PyCode) == "" {
			return fmt.Errorf("interceptor %d: py_code is required", cfg.Order)
		}
	}
	if p.applied == nil {
		p.applied = map[admin.InterceptorDirection][]string{}
	}
	p.applied[direction] = specs
	return nil
}

// Interceptor editing is off by default: the endpoints must not exist at all.
func TestInterceptorsDisabledByDefault(t *testing.T) {
	f := newWebFixture(t)

	f.do("GET", "/api/interceptors", "", http.StatusNotFound, nil)
	f.do("POST", "/api/interceptors", `{"direction":"mt","order":10,"py_code":"smpp_status = 88"}`,
		http.StatusNotFound, nil)

	// The session advertises the capability so the UI can hide the section.
	var session sessionPayload
	f.do("GET", "/api/session", "", http.StatusOK, &session)
	if session.Features.InterceptorEditing {
		t.Fatal("interceptor editing advertised while disabled")
	}
}

func TestInterceptorCRUD(t *testing.T) {
	f := newWebFixture(t)
	// Enable the capability on the same fixture wiring.
	provisioner := &fakeInterceptorProvisioner{}
	service, err := admin.NewInterceptorService(f.store, provisioner, func() string { return "2026-07-27T00:00:00Z" })
	if err != nil {
		t.Fatalf("new interceptor service: %v", err)
	}
	f.rebuildHandler(func(deps *Deps) { deps.Interceptors = service })

	var session sessionPayload
	f.do("GET", "/api/session", "", http.StatusOK, &session)
	if !session.Features.InterceptorEditing {
		t.Fatal("interceptor editing not advertised while enabled")
	}

	var created interceptorResource
	f.do("POST", "/api/interceptors",
		`{"direction":"mt","order":10,"py_code":"smpp_status = 88","filters":[{"type":"short_message","pattern":".*STOP"}]}`,
		http.StatusCreated, &created)
	if created.ID != "mt:10" || created.Direction != "mt" || len(created.Filters) != 1 {
		t.Fatalf("created: %+v", created)
	}
	if len(provisioner.applied[admin.InterceptMT]) != 1 {
		t.Fatalf("MT table not applied: %+v", provisioner.applied)
	}

	// An MO interceptor at the same order is a distinct entity.
	var mo interceptorResource
	f.do("POST", "/api/interceptors", `{"direction":"mo","order":10,"py_code":"smpp_status = 255"}`,
		http.StatusCreated, &mo)
	if mo.ID != "mo:10" {
		t.Fatalf("MO created: %+v", mo)
	}

	// A script-less spec is rejected before it reaches the provisioner.
	f.do("POST", "/api/interceptors", `{"direction":"mt","order":20,"py_code":"   "}`,
		http.StatusBadRequest, nil)
	// An unknown direction is a client error, not a 500.
	f.do("POST", "/api/interceptors", `{"direction":"sideways","order":20,"py_code":"x = 1"}`,
		http.StatusBadRequest, nil)

	var listed []interceptorResource
	f.do("GET", "/api/interceptors", "", http.StatusOK, &listed)
	if len(listed) != 2 {
		t.Fatalf("list: %+v", listed)
	}

	var updated interceptorResource
	f.do("PATCH", "/api/interceptors/mt:10", `{"py_code":"smpp_status = 99"}`, http.StatusOK, &updated)
	if updated.PyCode != "smpp_status = 99" {
		t.Fatalf("updated: %+v", updated)
	}
	if updated.Filters == nil {
		t.Fatalf("partial PATCH dropped filters: %+v", updated)
	}

	f.do("DELETE", "/api/interceptors/mt:10", "", http.StatusOK, nil)
	f.do("GET", "/api/interceptors/mt:10", "", http.StatusNotFound, nil)
	// The MO entry at the same order is untouched.
	f.do("GET", "/api/interceptors/mo:10", "", http.StatusOK, nil)

	// A malformed composite id is a client error.
	f.do("GET", "/api/interceptors/not-an-id", "", http.StatusBadRequest, nil)
}

func TestSMPPsUserCRUD(t *testing.T) {
	f := newWebFixture(t)

	var created smppsUserResource
	f.do("POST", "/api/smpps-users",
		`{"system_id":"esme-1","password":"bindpw","max_bindings":2}`,
		http.StatusCreated, &created)
	if created.ID != "esme-1" {
		t.Fatalf("created: %+v", created)
	}
	if created.Password != "" {
		t.Fatalf("response leaked the bind password: %+v", created)
	}
	if len(f.smppsUsers.applied) != 1 || f.smppsUsers.applied[0].Password != "bindpw" {
		t.Fatalf("directory did not receive the user: %+v", f.smppsUsers.applied)
	}

	// Missing credentials are rejected before reaching the directory.
	f.do("POST", "/api/smpps-users", `{"system_id":"esme-2"}`, http.StatusBadRequest, nil)
	f.do("POST", "/api/smpps-users", `{"password":"x"}`, http.StatusBadRequest, nil)

	// A partial PATCH keeps the stored password and other fields.
	var updated smppsUserResource
	f.do("PATCH", "/api/smpps-users/esme-1", `{"disabled":true}`, http.StatusOK, &updated)
	if !updated.Disabled {
		t.Fatalf("updated: %+v", updated)
	}
	if f.smppsUsers.applied[0].Password != "bindpw" {
		t.Fatalf("empty password on update overwrote the stored one: %+v", f.smppsUsers.applied[0])
	}
	if updated.MaxBindings == nil || *updated.MaxBindings != 2 {
		t.Fatalf("partial PATCH dropped max_bindings: %+v", updated)
	}

	var listed []smppsUserResource
	f.do("GET", "/api/smpps-users", "", http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].ID != "esme-1" {
		t.Fatalf("list: %+v", listed)
	}

	f.do("DELETE", "/api/smpps-users/esme-1", "", http.StatusOK, nil)
	f.do("GET", "/api/smpps-users/esme-1", "", http.StatusNotFound, nil)
	if len(f.smppsUsers.applied) != 0 {
		t.Fatalf("delete did not remove the user from the directory: %+v", f.smppsUsers.applied)
	}
}

func TestHealthAndSPA(t *testing.T) {
	f := newWebFixture(t)

	var health struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	f.do("GET", "/api/health", "", http.StatusOK, &health)
	if health.Status != "ok" || health.Checks["postgres"] != "ok" {
		t.Fatalf("health: %+v", health)
	}

	// Unknown /api paths are JSON 404s, not SPA fallbacks.
	f.do("GET", "/api/nope", "", http.StatusNotFound, nil)

	// The SPA shell answers / and unknown client-side routes alike, without a
	// session (the login page is part of the bundle).
	for _, path := range []string{"/", "/connectors", "/login"} {
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: code %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("GET %s: content-type %q", path, ct)
		}
	}
}
