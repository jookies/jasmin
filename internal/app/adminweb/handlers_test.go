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
	"time"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/modispatch"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/stats"
)

// fakeManager records live-apply calls and reports every connector unbound.
type fakeManager struct {
	added   map[string]smppc.Config
	started map[string]bool
}

type balanceReaderStub struct {
	snapshot core.BalanceSnapshot
	err      error
}

func (s balanceReaderStub) Balance(context.Context, string) (core.BalanceSnapshot, error) {
	return s.snapshot, s.err
}

type rateReaderStub struct {
	quote core.RateQuote
	err   error
}

func (s rateReaderStub) Rate(context.Context, string, string) (core.RateQuote, error) {
	return s.quote, s.err
}

type submitterStub struct {
	messageID string
	request   core.SubmitRequest
	err       error
}

func (s *submitterStub) Submit(_ context.Context, request core.SubmitRequest) (string, error) {
	s.request = request
	return s.messageID, s.err
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

type fakeGroupProvisioner struct {
	installed map[string]string
	numbers   map[string]int64
	// users backs the cascade delete so the fixture models the real wiring,
	// where both provisioners front the same outbound runtime.
	users *fakeUserProvisioner
}

func (p *fakeGroupProvisioner) RemoveUser(username string) error {
	if p.users == nil {
		return nil
	}
	return p.users.RemoveUser(username)
}

func newFakeGroupProvisioner() *fakeGroupProvisioner {
	return &fakeGroupProvisioner{installed: map[string]string{}, numbers: map[string]int64{}}
}

func (p *fakeGroupProvisioner) AddGroup(gid, specJSON string, number int64) error {
	var config outbound.GroupConfig
	if err := json.Unmarshal([]byte(specJSON), &config); err != nil {
		return err
	}
	if config.GID != gid {
		return fmt.Errorf("group spec gid %q does not match %q", config.GID, gid)
	}
	if _, exists := p.installed[gid]; exists {
		return fmt.Errorf("duplicate group %q", gid)
	}
	p.installed[gid] = specJSON
	p.numbers[gid] = number
	return nil
}

func (p *fakeGroupProvisioner) RemoveGroup(gid string) error {
	if _, exists := p.installed[gid]; !exists {
		return fmt.Errorf("unknown group %q", gid)
	}
	delete(p.installed, gid)
	delete(p.numbers, gid)
	return nil
}

func (p *fakeGroupProvisioner) ConfigGroupFloor() int64 { return 1 }

// webFixture is a fully wired Handler over an in-memory store plus an
// authenticated client state (session cookie + CSRF token).
type webFixture struct {
	handler    *Handler
	deps       Deps
	store      *admin.Store
	manager    *fakeManager
	users      *fakeUserProvisioner
	groups     *fakeGroupProvisioner
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
	groupProv := newFakeGroupProvisioner()
	groupProv.users = userProv
	groupService, err := admin.NewGroupService(store, groupProv, now)
	if err != nil {
		t.Fatalf("new group service: %v", err)
	}
	filterService, err := admin.NewFilterService(store, now)
	if err != nil {
		t.Fatalf("new filter service: %v", err)
	}
	httpConnectorService, err := admin.NewHTTPConnectorService(store, now)
	if err != nil {
		t.Fatalf("new HTTP connector service: %v", err)
	}
	profileService, err := admin.NewProfileService(store, now)
	if err != nil {
		t.Fatalf("new profile service: %v", err)
	}
	httpStats := &stats.HTTPStats{}
	smppcStats := stats.NewSMPPcRegistry()
	smppsStats := &stats.SMPPsStats{}
	deps := Deps{
		Connectors:     service,
		Routes:         routeService,
		MORoutes:       moRouteService,
		Users:          userService,
		Groups:         groupService,
		SMPPsUsers:     smppsUserService,
		Filters:        filterService,
		HTTPConnectors: httpConnectorService,
		Profiles:       profileService,
		HTTPStats:      httpStats,
		SMPPcStats:     smppcStats,
		SMPPsStats:     smppsStats,
		StartedAt:      func() time.Time { return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC) },
		ConnectorIDs: func() []string {
			ids := make([]string, 0, len(manager.added))
			for cid := range manager.added {
				ids = append(ids, cid)
			}
			return ids
		},
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
	f := &webFixture{handler: handler, deps: deps, store: store, manager: manager, users: userProv, groups: groupProv, smppsUsers: smppsUsers, t: t}
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

func TestRouteFlushesOnlyAdminManagedRows(t *testing.T) {
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) {
		deps.ConfigRoutes = func() []outbound.RouteConfig {
			return []outbound.RouteConfig{{Order: 100, ConnectorID: "config-smsc"}}
		}
		deps.ConfigMORoutes = func() []modispatch.RouteConfig {
			return []modispatch.RouteConfig{{
				Order:     100,
				Connector: modispatch.ConnectorConfig{Type: "smpps", SystemID: "config-app"},
			}}
		}
	})
	f.do("POST", "/api/routes",
		`{"order":10,"connector_id":"admin-smsc"}`,
		http.StatusCreated, nil)
	f.do("POST", "/api/mo-routes",
		`{"order":10,"connector":{"type":"smpps","system_id":"admin-app"}}`,
		http.StatusCreated, nil)

	var result map[string]int
	f.do("POST", "/api/routes/flush", "", http.StatusOK, &result)
	if result["deleted"] != 1 {
		t.Fatalf("MT flush: %+v", result)
	}
	f.do("POST", "/api/mo-routes/flush", "", http.StatusOK, &result)
	if result["deleted"] != 1 {
		t.Fatalf("MO flush: %+v", result)
	}

	var routes []routeResource
	f.do("GET", "/api/routes", "", http.StatusOK, &routes)
	if len(routes) != 1 || routes[0].ManagedBy != "config" {
		t.Fatalf("MT rows after flush: %+v", routes)
	}
	var moRoutes []moRouteResource
	f.do("GET", "/api/mo-routes", "", http.StatusOK, &moRoutes)
	if len(moRoutes) != 1 || moRoutes[0].ManagedBy != "config" {
		t.Fatalf("MO rows after flush: %+v", moRoutes)
	}
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

func TestGroupAndAdvancedUserCRUD(t *testing.T) {
	f := newWebFixture(t)

	var group groupResource
	f.do("POST", "/api/groups",
		`{"gid":"customers","balance":500,"submit_sm_count":1000}`,
		http.StatusCreated, &group)
	if group.GID != "customers" || group.Number != 2 {
		t.Fatalf("group: %+v", group)
	}

	var user userResource
	f.do("POST", "/api/users", `{
		"username":"advanced",
		"password":"bind-password",
		"group_id":"customers",
		"http_send":false,
		"http_bulk":true,
		"smpps_send":true,
		"set_priority":false,
		"filter_destination_address":"^1202",
		"default_source_address":"JASMIN",
		"http_throughput":12.5,
		"smpps_bind":true,
		"smpps_ip":"10.0.0.0/8",
		"smpps_max_bindings":3
	}`, http.StatusCreated, &user)
	if user.GroupID != "customers" || user.HTTPSend == nil || *user.HTTPSend ||
		user.HTTPBulk == nil || !*user.HTTPBulk || user.DefaultSourceAddress == nil ||
		*user.DefaultSourceAddress != "JASMIN" {
		t.Fatalf("advanced user fields were not preserved: %+v", user)
	}
	var stored outbound.UserConfig
	if err := json.Unmarshal([]byte(f.users.installed["advanced"]), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.MTCredential == nil || stored.SMPPSCredential == nil ||
		stored.MTCredential.FilterDestinationAddress != "^1202" ||
		stored.SMPPSCredential.MaxBindings == nil || *stored.SMPPSCredential.MaxBindings != 3 {
		t.Fatalf("advanced credentials were not provisioned: %+v", stored)
	}
	if len(f.smppsUsers.applied) != 1 ||
		f.smppsUsers.applied[0].SystemID != "advanced" ||
		f.smppsUsers.applied[0].Password != "bind-password" {
		t.Fatalf("SMPPs mirror was not provisioned: %+v", f.smppsUsers.applied)
	}

	f.do("PATCH", "/api/groups/customers", `{"disabled":true}`, http.StatusOK, &group)
	if !group.Disabled {
		t.Fatalf("group was not disabled: %+v", group)
	}
	f.do("DELETE", "/api/users/advanced", "", http.StatusOK, nil)
	f.do("DELETE", "/api/groups/customers", "", http.StatusOK, nil)
}

func TestSavedFilterAndHTTPConnectorCRUD(t *testing.T) {
	f := newWebFixture(t)

	var filter filterResource
	f.do("POST", "/api/filters",
		`{"fid":"german-msisdn","type":"DestinationAddrFilter","args":{"destination_addr":"^49"}}`,
		http.StatusCreated, &filter)
	if filter.FID != "german-msisdn" || filter.Args["destination_addr"] != "^49" {
		t.Fatalf("filter: %+v", filter)
	}
	f.do("POST", "/api/filters",
		`{"fid":"broken","type":"DestinationAddrFilter"}`,
		http.StatusBadRequest, nil)
	f.do("PATCH", "/api/filters/german-msisdn",
		`{"type":"DestinationAddrFilter","args":{"destination_addr":"^4917"}}`,
		http.StatusOK, &filter)
	if filter.Args["destination_addr"] != "^4917" {
		t.Fatalf("filter update: %+v", filter)
	}

	var destination httpConnectorResource
	f.do("POST", "/api/http-connectors",
		`{"cid":"support-api","baseurl":"https://app.example.com/mo","method":"POST"}`,
		http.StatusCreated, &destination)
	if destination.CID != "support-api" || destination.Method != "POST" {
		t.Fatalf("HTTP destination: %+v", destination)
	}
	f.do("POST", "/api/http-connectors",
		`{"cid":"bad","baseurl":"ftp://example.com/mo","method":"GET"}`,
		http.StatusBadRequest, nil)

	var filters []filterResource
	f.do("GET", "/api/filters", "", http.StatusOK, &filters)
	if len(filters) != 1 {
		t.Fatalf("filters: %+v", filters)
	}
	var destinations []httpConnectorResource
	f.do("GET", "/api/http-connectors", "", http.StatusOK, &destinations)
	if len(destinations) != 1 {
		t.Fatalf("HTTP destinations: %+v", destinations)
	}

	f.do("DELETE", "/api/filters/german-msisdn", "", http.StatusOK, nil)
	f.do("DELETE", "/api/http-connectors/support-api", "", http.StatusOK, nil)
}

func TestConfigOwnedInventoryIsVisibleAndReadOnly(t *testing.T) {
	f := newWebFixture(t)
	f.rebuildHandler(func(deps *Deps) {
		deps.ConfigConnectors = func() []smppc.Config {
			return []smppc.Config{{
				CID: "config-smsc", Host: "smsc.example", Port: 2775,
				SystemID: "system", Password: "never-return-this",
			}}
		}
		deps.ConfigRoutes = func() []outbound.RouteConfig {
			return []outbound.RouteConfig{{Order: 100, ConnectorID: "config-smsc"}}
		}
		deps.ConfigMORoutes = func() []modispatch.RouteConfig {
			return []modispatch.RouteConfig{{
				Order: 100,
				Connector: modispatch.ConnectorConfig{
					Type: "http", CID: "config-http", URL: "https://example.com/mo",
				},
			}}
		}
		deps.ConfigGroups = func() []outbound.GroupConfig {
			return []outbound.GroupConfig{{GID: "config-group"}}
		}
		deps.ConfigUsers = func() []outbound.UserConfig {
			return []outbound.UserConfig{{
				Username: "config-user", ExternalID: "config-user",
				PasswordSHA256: strings.Repeat("a", 64),
			}}
		}
		deps.ConfigSMPPsUsers = func() []smppsserver.UserConfig {
			return []smppsserver.UserConfig{{SystemID: "config-bind", Password: "hidden"}}
		}
		deps.ConnectorStatus = func(cid string) (smppc.ManagedStatus, error) {
			return smppc.ManagedStatus{CID: cid, Desired: true, Observed: smppc.StatusBound}, nil
		}
	})

	var connectors []connectorResource
	f.do("GET", "/api/connectors", "", http.StatusOK, &connectors)
	if len(connectors) != 1 || connectors[0].ManagedBy != "config" ||
		connectors[0].Password != "" || connectors[0].Observed != string(smppc.StatusBound) {
		t.Fatalf("config connector: %+v", connectors)
	}
	var routes []routeResource
	f.do("GET", "/api/routes", "", http.StatusOK, &routes)
	if len(routes) != 1 || routes[0].ManagedBy != "config" {
		t.Fatalf("config routes: %+v", routes)
	}
	var moRoutes []moRouteResource
	f.do("GET", "/api/mo-routes", "", http.StatusOK, &moRoutes)
	if len(moRoutes) != 1 || moRoutes[0].ManagedBy != "config" {
		t.Fatalf("config MO routes: %+v", moRoutes)
	}
	var groups []groupResource
	f.do("GET", "/api/groups", "", http.StatusOK, &groups)
	if len(groups) != 1 || groups[0].ManagedBy != "config" {
		t.Fatalf("config groups: %+v", groups)
	}
	var users []userResource
	f.do("GET", "/api/users", "", http.StatusOK, &users)
	if len(users) != 1 || users[0].ManagedBy != "config" || users[0].Password != "" {
		t.Fatalf("config users: %+v", users)
	}
	var binds []smppsUserResource
	f.do("GET", "/api/smpps-users", "", http.StatusOK, &binds)
	if len(binds) != 1 || binds[0].ManagedBy != "config" || binds[0].Password != "" {
		t.Fatalf("config binds: %+v", binds)
	}

	f.do("PATCH", "/api/users/config-user", `{"disabled":true}`, http.StatusNotFound, nil)
	f.do("DELETE", "/api/groups/config-group", "", http.StatusNotFound, nil)
	f.do("POST", "/api/smpps-users/config-bind/ban", "", http.StatusConflict, nil)
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
	var flushed map[string]int
	f.do("POST", "/api/interceptors/mo/flush", "", http.StatusOK, &flushed)
	if flushed["deleted"] != 1 {
		t.Fatalf("MO interceptor flush: %+v", flushed)
	}

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

func TestSMPPsSessionActions(t *testing.T) {
	f := newWebFixture(t)
	unbound := ""
	f.rebuildHandler(func(deps *Deps) {
		deps.UnbindSMPPsUser = func(systemID string) int {
			unbound = systemID
			return 2
		}
	})
	f.do("POST", "/api/smpps-users",
		`{"system_id":"esme-actions","password":"bindpw"}`,
		http.StatusCreated, nil)

	var action smppsSessionAction
	f.do("POST", "/api/smpps-users/esme-actions/unbind", "", http.StatusOK, &action)
	if unbound != "esme-actions" || action.Sessions != 2 || action.Banned {
		t.Fatalf("unbind: %+v system=%q", action, unbound)
	}
	f.do("POST", "/api/smpps-users/esme-actions/ban", "", http.StatusOK, &action)
	if !action.Banned || action.Sessions != 2 {
		t.Fatalf("ban: %+v", action)
	}
	var account smppsUserResource
	f.do("GET", "/api/smpps-users/esme-actions", "", http.StatusOK, &account)
	if !account.Disabled {
		t.Fatalf("ban did not disable the account: %+v", account)
	}
}

func TestStatsAndUnavailableMessageStatus(t *testing.T) {
	f := newWebFixture(t)

	var payload statsResource
	f.do("GET", "/api/stats", "", http.StatusOK, &payload)
	if payload.StartedAt != "2026-07-27T00:00:00Z" ||
		payload.HTTP == nil || payload.SMPPs == nil || payload.SMPPc == nil {
		t.Fatalf("stats payload: %+v", payload)
	}
	f.do("GET", "/api/message-status/not-known", "", http.StatusServiceUnavailable, nil)
}

func TestOperationalAccountAndSendTools(t *testing.T) {
	f := newWebFixture(t)
	balance := "42.5"
	count := "90"
	submitter := &submitterStub{messageID: "message-123"}
	f.rebuildHandler(func(deps *Deps) {
		deps.BalanceReader = balanceReaderStub{snapshot: core.BalanceSnapshot{
			Balance:  &balance,
			SMSCount: &count,
		}}
		deps.RateReader = rateReaderStub{quote: core.RateQuote{
			UnitRate:      0.035,
			SubmitSMCount: 2,
		}}
		deps.Submitter = submitter
	})

	var balanceResult map[string]any
	f.do("POST", "/api/tools/balance", `{"username":"alice"}`, http.StatusOK, &balanceResult)
	if balanceResult["balance"] != balance || balanceResult["submit_sm_count"] != count {
		t.Fatalf("balance tool: %+v", balanceResult)
	}
	var rateResult map[string]any
	f.do("POST", "/api/tools/rate",
		`{"username":"alice","destination":"+12025550123"}`,
		http.StatusOK, &rateResult)
	if rateResult["unit_rate"] != 0.035 || rateResult["submit_sm_count"] != float64(2) {
		t.Fatalf("rate tool: %+v", rateResult)
	}
	var sendResult map[string]any
	f.do("POST", "/api/tools/send", `{
		"username":"alice",
		"password":"secret",
		"destination":"+12025550123",
		"from":"JASMIN",
		"content":"test message",
		"coding":8,
		"dlr":true
	}`, http.StatusOK, &sendResult)
	if sendResult["message_id"] != "message-123" ||
		submitter.request.Password != "secret" ||
		submitter.request.SourceConnector != "httpapi" ||
		!submitter.request.DLR || submitter.request.DLRLevel != 1 ||
		submitter.request.DLRMethod != "POST" {
		t.Fatalf("send tool result=%+v request=%+v", sendResult, submitter.request)
	}

	f.do("POST", "/api/tools/rate", `{"username":"alice"}`, http.StatusBadRequest, nil)
	f.do("POST", "/api/tools/send", `{"username":"alice"}`, http.StatusBadRequest, nil)
}

func TestProfileSaveAndLiveRestore(t *testing.T) {
	f := newWebFixture(t)

	f.do("POST", "/api/groups",
		`{"gid":"checkpoint","balance":100}`,
		http.StatusCreated, nil)
	f.do("POST", "/api/connectors",
		`{"cid":"checkpoint-smsc","host":"smsc.example","port":2775,"system_id":"sys","password":"pw","bind":"transceiver"}`,
		http.StatusCreated, nil)
	f.do("POST", "/api/profiles/before-change/save", "", http.StatusOK, nil)

	f.do("PATCH", "/api/groups/checkpoint", `{"balance":900}`, http.StatusOK, nil)
	f.do("POST", "/api/groups", `{"gid":"temporary"}`, http.StatusCreated, nil)
	f.do("DELETE", "/api/connectors/checkpoint-smsc", "", http.StatusOK, nil)
	f.do("POST", "/api/connectors",
		`{"cid":"temporary-smsc","host":"smsc.example","port":2775,"system_id":"sys","password":"pw","bind":"transceiver"}`,
		http.StatusCreated, nil)

	f.do("POST", "/api/profiles/before-change/load", "", http.StatusOK, nil)

	var groups []groupResource
	f.do("GET", "/api/groups", "", http.StatusOK, &groups)
	if len(groups) != 1 || groups[0].GID != "checkpoint" ||
		groups[0].Balance == nil || *groups[0].Balance != 100 {
		t.Fatalf("groups after restore: %+v", groups)
	}
	var connectors []connectorResource
	f.do("GET", "/api/connectors", "", http.StatusOK, &connectors)
	if len(connectors) != 1 || connectors[0].CID != "checkpoint-smsc" {
		t.Fatalf("connectors after restore: %+v", connectors)
	}
	if _, ok := f.manager.added["temporary-smsc"]; ok {
		t.Fatal("temporary connector remained live after profile restore")
	}
	f.do("POST", "/api/profiles/missing/load", "", http.StatusNotFound, nil)
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
