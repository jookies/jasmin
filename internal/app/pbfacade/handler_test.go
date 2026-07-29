package pbfacade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(Deps{}); err == nil {
		t.Fatal("New accepted an empty authentication token")
	}
}

func TestWireAuthenticationAndCapabilities(t *testing.T) {
	handler := mustHandler(t, Deps{Token: "facade-secret"}).Routes()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}
	if got := unauthorized.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", got)
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer facade-secret")
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d body=%s", authorized.Code, authorized.Body.String())
	}
	var payload map[string]any
	decodeResponse(t, authorized, &payload)
	result := payload["result"].(map[string]any)
	if result["accepts_pb"] != false || result["accepts_pickle"] != false {
		t.Fatalf("unsafe capabilities: %#v", result)
	}
	if got := result["methods"].([]any); len(got) != 1 || got[0] != "version" {
		t.Fatalf("methods = %#v, want version only", got)
	}
}

func TestGroupCallsExistingAdminServiceEndToEnd(t *testing.T) {
	ctx := context.Background()
	store, err := admin.OpenStore(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provisioner := &recordingGroupProvisioner{}
	service, err := admin.NewGroupService(store, provisioner, func() string { return "now" })
	if err != nil {
		t.Fatal(err)
	}
	handler := mustHandler(t, Deps{Groups: service, Token: "secret"}).Routes()

	add := call(t, handler, "secret", request{
		Version: ProtocolVersion,
		ID:      "call-1",
		Method:  "router.group.add",
		Params:  json.RawMessage(`{"id":"customers","spec":{"id":"customers","enabled":true}}`),
	})
	if add.Code != http.StatusOK {
		t.Fatalf("add status = %d body=%s", add.Code, add.Body.String())
	}
	if provisioner.addedID != "customers" ||
		provisioner.addedSpec != `{"id":"customers","enabled":true}` {
		t.Fatalf("live provisioner got id=%q spec=%q", provisioner.addedID, provisioner.addedSpec)
	}
	var payload map[string]any
	decodeResponse(t, add, &payload)
	if payload["version"] != ProtocolVersion || payload["id"] != "call-1" || payload["ok"] != true {
		t.Fatalf("response envelope = %#v", payload)
	}
	result := payload["result"].(map[string]any)
	if result["id"] != "customers" || result["number"] != float64(1) {
		t.Fatalf("group result = %#v", result)
	}
	if !reflect.DeepEqual(result["spec"], map[string]any{"id": "customers", "enabled": true}) {
		t.Fatalf("group spec = %#v", result["spec"])
	}
	if _, err := service.GetGroup(ctx, "customers"); err != nil {
		t.Fatalf("PB seam did not persist through admin service: %v", err)
	}

	list := call(t, handler, "secret", request{
		Version: ProtocolVersion,
		ID:      "call-2",
		Method:  "router.group.list",
		Params:  json.RawMessage(`{}`),
	})
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}
	decodeResponse(t, list, &payload)
	if got := payload["result"].([]any); len(got) != 1 {
		t.Fatalf("list result = %#v", got)
	}
}

func TestConnectorCallsExistingAdminServiceEndToEnd(t *testing.T) {
	ctx := context.Background()
	store, err := admin.OpenStore(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := newRecordingConnectorManager()
	service, err := admin.NewService(store, manager, nil, func() string { return "now" })
	if err != nil {
		t.Fatal(err)
	}
	handler := mustHandler(t, Deps{Connectors: service, Token: "secret"}).Routes()

	add := call(t, handler, "secret", request{
		Version: ProtocolVersion,
		ID:      "add",
		Method:  "client.connector.add",
		Params:  json.RawMessage(`{"config":{"cid":"c1","host":"smsc.example","port":2775},"start":false}`),
	})
	if add.Code != http.StatusOK {
		t.Fatalf("add status = %d body=%s", add.Code, add.Body.String())
	}
	if manager.started["c1"] {
		t.Fatal("connector was started despite start=false")
	}

	start := call(t, handler, "secret", request{
		Version: ProtocolVersion,
		ID:      "start",
		Method:  "client.connector.start",
		Params:  json.RawMessage(`{"id":"c1"}`),
	})
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", start.Code, start.Body.String())
	}
	var payload map[string]any
	decodeResponse(t, start, &payload)
	result := payload["result"].(map[string]any)
	if result["desired_started"] != true || result["observed"] != string(smppc.StatusBound) {
		t.Fatalf("start result = %#v", result)
	}
	config := result["config"].(map[string]any)
	if config["cid"] != "c1" || config["host"] != "smsc.example" {
		t.Fatalf("connector config = %#v", config)
	}
}

func TestWireRejectsVersionUnknownAndUnavailableMethods(t *testing.T) {
	handler := mustHandler(t, Deps{Token: "secret"}).Routes()
	tests := []struct {
		name   string
		call   request
		status int
		code   string
	}{
		{
			name:   "version",
			call:   request{Version: "future", ID: "1", Method: "version"},
			status: http.StatusBadRequest,
			code:   "unsupported_version",
		},
		{
			name:   "unknown",
			call:   request{Version: ProtocolVersion, ID: "2", Method: "router.magic"},
			status: http.StatusNotFound,
			code:   "unknown_method",
		},
		{
			name:   "known but unavailable",
			call:   request{Version: ProtocolVersion, ID: "3", Method: "router.user.list"},
			status: http.StatusNotImplemented,
			code:   "unavailable_method",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := call(t, handler, "secret", test.call)
			if recorder.Code != test.status {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var payload struct {
				Error responseError `json:"error"`
			}
			decodeResponse(t, recorder, &payload)
			if payload.Error.Code != test.code {
				t.Fatalf("error code = %q, want %q", payload.Error.Code, test.code)
			}
		})
	}
}

func TestWireRejectsUnknownParamsWithoutCallingService(t *testing.T) {
	connectors := &rejectIfCalledConnectorService{}
	handler := mustHandler(t, Deps{Connectors: connectors, Token: "secret"}).Routes()
	recorder := call(t, handler, "secret", request{
		Version: ProtocolVersion,
		ID:      "bad",
		Method:  "client.connector.get",
		Params:  json.RawMessage(`{"id":"c1","pickle":"forbidden"}`),
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestNormalizedRouterFamiliesDispatch(t *testing.T) {
	users := &recordingUserService{}
	mtRoutes := &recordingMTRouteService{}
	moRoutes := &recordingOrderedService{}
	interceptors := &recordingInterceptorService{}
	handler := mustHandler(t, Deps{
		Users:        users,
		MTRoutes:     mtRoutes,
		MORoutes:     moRoutes,
		Interceptors: interceptors,
		Token:        "secret",
	}).Routes()

	tests := []struct {
		method string
		params string
	}{
		{"router.user.add", `{"id":"alice","spec":{"username":"alice"}}`},
		{"router.mtroute.add", `{"order":10,"spec":{"type":"default"}}`},
		{"router.moroute.add", `{"order":20,"spec":{"type":"static"}}`},
		{"router.mtinterceptor.add", `{"order":30,"spec":{"script":"mt"}}`},
		{"router.mointerceptor.add", `{"order":40,"spec":{"script":"mo"}}`},
	}
	for index, test := range tests {
		recorder := call(t, handler, "secret", request{
			Version: ProtocolVersion,
			ID:      test.method,
			Method:  test.method,
			Params:  json.RawMessage(test.params),
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("call %d %s status=%d body=%s", index, test.method, recorder.Code, recorder.Body.String())
		}
	}

	if users.id != "alice" || users.spec != `{"username":"alice"}` {
		t.Fatalf("user dispatch id=%q spec=%q", users.id, users.spec)
	}
	if mtRoutes.order != 10 || mtRoutes.spec != `{"type":"default"}` {
		t.Fatalf("MT route dispatch order=%d spec=%q", mtRoutes.order, mtRoutes.spec)
	}
	if moRoutes.order != 20 || moRoutes.spec != `{"type":"static"}` {
		t.Fatalf("MO route dispatch order=%d spec=%q", moRoutes.order, moRoutes.spec)
	}
	if interceptors.calls[admin.InterceptMT].order != 30 ||
		interceptors.calls[admin.InterceptMT].spec != `{"script":"mt"}` {
		t.Fatalf("MT interceptor dispatch = %#v", interceptors.calls[admin.InterceptMT])
	}
	if interceptors.calls[admin.InterceptMO].order != 40 ||
		interceptors.calls[admin.InterceptMO].spec != `{"script":"mo"}` {
		t.Fatalf("MO interceptor dispatch = %#v", interceptors.calls[admin.InterceptMO])
	}
}

func mustHandler(t *testing.T, deps Deps) *Handler {
	t.Helper()
	handler, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func call(t *testing.T, handler http.Handler, token string, value request) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/call", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
}

type recordingGroupProvisioner struct {
	addedID   string
	addedSpec string
}

func (p *recordingGroupProvisioner) AddGroup(id, spec string, _ int64) error {
	p.addedID, p.addedSpec = id, spec
	return nil
}

func (*recordingGroupProvisioner) RemoveGroup(string) error { return nil }
func (*recordingGroupProvisioner) RemoveUser(string) error  { return nil }
func (*recordingGroupProvisioner) ConfigGroupFloor() int64  { return 0 }

type recordingConnectorManager struct {
	configs map[string]smppc.Config
	started map[string]bool
}

func newRecordingConnectorManager() *recordingConnectorManager {
	return &recordingConnectorManager{
		configs: make(map[string]smppc.Config),
		started: make(map[string]bool),
	}
}

func (m *recordingConnectorManager) Add(config smppc.Config) error {
	if _, exists := m.configs[config.CID]; exists {
		return errors.New("exists")
	}
	m.configs[config.CID] = config
	return nil
}

func (m *recordingConnectorManager) Update(config smppc.Config) error {
	m.configs[config.CID] = config
	return nil
}

func (m *recordingConnectorManager) Remove(id string) error {
	delete(m.configs, id)
	delete(m.started, id)
	return nil
}

func (m *recordingConnectorManager) Start(id string) error {
	m.started[id] = true
	return nil
}

func (m *recordingConnectorManager) Stop(id string) error {
	m.started[id] = false
	return nil
}

func (m *recordingConnectorManager) Status(id string) (smppc.ManagedStatus, error) {
	config, ok := m.configs[id]
	if !ok {
		return smppc.ManagedStatus{}, smppc.ErrNotFound
	}
	observed := smppc.StatusDisconnected
	if m.started[id] {
		observed = smppc.StatusBound
	}
	return smppc.ManagedStatus{
		CID:      id,
		Desired:  m.started[id],
		Observed: observed,
		Config:   config,
	}, nil
}

type rejectIfCalledConnectorService struct{}

func (*rejectIfCalledConnectorService) CreateConnector(context.Context, smppc.Config, bool) error {
	panic("called")
}
func (*rejectIfCalledConnectorService) DeleteConnector(context.Context, string) error {
	panic("called")
}
func (*rejectIfCalledConnectorService) SetStarted(context.Context, string, bool) error {
	panic("called")
}
func (*rejectIfCalledConnectorService) ListConnectors(context.Context) ([]admin.ConnectorView, error) {
	panic("called")
}
func (*rejectIfCalledConnectorService) GetConnector(context.Context, string) (admin.ConnectorView, error) {
	panic("called")
}

type recordingUserService struct {
	id   string
	spec string
}

func (s *recordingUserService) CreateUser(_ context.Context, id, spec string) error {
	s.id, s.spec = id, spec
	return nil
}
func (*recordingUserService) DeleteUser(context.Context, string) error { return nil }
func (s *recordingUserService) ListUsers(context.Context) ([]admin.StoredUser, error) {
	return []admin.StoredUser{{Username: s.id, UID: 7, SpecJSON: s.spec}}, nil
}
func (s *recordingUserService) GetUser(context.Context, string) (admin.StoredUser, error) {
	return admin.StoredUser{Username: s.id, UID: 7, SpecJSON: s.spec}, nil
}

type recordingMTRouteService struct {
	order int
	spec  string
}

func (s *recordingMTRouteService) PutRoute(_ context.Context, order int, spec string) error {
	s.order, s.spec = order, spec
	return nil
}
func (*recordingMTRouteService) DeleteRoute(context.Context, int) error { return nil }
func (s *recordingMTRouteService) ListRoutes(context.Context) ([]admin.StoredRoute, error) {
	return []admin.StoredRoute{{Order: s.order, SpecJSON: s.spec}}, nil
}
func (s *recordingMTRouteService) GetRoute(context.Context, int) (admin.StoredRoute, error) {
	return admin.StoredRoute{Order: s.order, SpecJSON: s.spec}, nil
}

type recordingOrderedService struct {
	order int
	spec  string
}

func (s *recordingOrderedService) PutRoute(_ context.Context, order int, spec string) error {
	s.order, s.spec = order, spec
	return nil
}
func (*recordingOrderedService) DeleteRoute(context.Context, int) error { return nil }
func (s *recordingOrderedService) ListRoutes(context.Context) ([]admin.StoredSpec, error) {
	return []admin.StoredSpec{{Order: s.order, SpecJSON: s.spec}}, nil
}
func (s *recordingOrderedService) GetRoute(context.Context, int) (admin.StoredSpec, error) {
	return admin.StoredSpec{Order: s.order, SpecJSON: s.spec}, nil
}

type interceptorCall struct {
	order int
	spec  string
}

type recordingInterceptorService struct {
	calls map[admin.InterceptorDirection]interceptorCall
}

func (s *recordingInterceptorService) PutInterceptor(_ context.Context, direction admin.InterceptorDirection, order int, spec string) error {
	if s.calls == nil {
		s.calls = make(map[admin.InterceptorDirection]interceptorCall)
	}
	s.calls[direction] = interceptorCall{order: order, spec: spec}
	return nil
}
func (*recordingInterceptorService) DeleteInterceptor(context.Context, admin.InterceptorDirection, int) error {
	return nil
}
func (s *recordingInterceptorService) ListInterceptors(_ context.Context, direction admin.InterceptorDirection) ([]admin.StoredSpec, error) {
	call := s.calls[direction]
	return []admin.StoredSpec{{Order: call.order, SpecJSON: call.spec}}, nil
}
func (s *recordingInterceptorService) GetInterceptor(_ context.Context, direction admin.InterceptorDirection, _ int) (admin.StoredSpec, error) {
	call := s.calls[direction]
	return admin.StoredSpec{Order: call.order, SpecJSON: call.spec}, nil
}
