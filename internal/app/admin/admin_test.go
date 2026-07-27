package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

// fakeManager records live-apply calls and simulates bind status.
type fakeManager struct {
	added   map[string]smppc.Config
	started map[string]bool
	addErr  error
}

func newFakeManager() *fakeManager {
	return &fakeManager{added: map[string]smppc.Config{}, started: map[string]bool{}}
}

func (m *fakeManager) Add(cfg smppc.Config) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.added[cfg.CID] = cfg
	return nil
}
func (m *fakeManager) Update(cfg smppc.Config) error {
	if _, ok := m.added[cfg.CID]; !ok {
		return smppc.ErrNotFound
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
	observed := smppc.StatusDisconnected
	if m.started[cid] {
		observed = smppc.StatusBound
	}
	return smppc.ManagedStatus{CID: cid, Observed: observed}, nil
}

func newTestService(t *testing.T, reserved ...string) (*Service, *fakeManager, *Store) {
	t.Helper()
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := newFakeManager()
	clock := 0
	service, err := NewService(store, manager, reserved, func() string { clock++; return "t" + string(rune('0'+clock)) })
	if err != nil {
		t.Fatal(err)
	}
	return service, manager, store
}

func sampleConnector(cid string) smppc.Config {
	return smppc.Config{CID: cid, Host: "smsc.example", Port: 2775, SystemID: "sys", Password: "pw", Bind: smppc.BindTransceiver}
}

func TestServiceCreateAppliesLiveAndPersists(t *testing.T) {
	service, manager, store := newTestService(t)
	if err := service.CreateConnector(context.Background(), sampleConnector("c1"), true); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.added["c1"]; !ok || !manager.started["c1"] {
		t.Fatalf("connector not applied live: added=%v started=%v", manager.added, manager.started)
	}
	stored, err := store.GetConnector(context.Background(), "c1")
	if err != nil || !stored.DesiredStarted {
		t.Fatalf("connector not persisted: %+v err=%v", stored, err)
	}
}

func TestServiceCreateRollsBackOnPersistIndependence(t *testing.T) {
	service, manager, _ := newTestService(t)
	// A duplicate create must be rejected as a conflict, leaving the first intact.
	if err := service.CreateConnector(context.Background(), sampleConnector("c1"), true); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateConnector(context.Background(), sampleConnector("c1"), true); err == nil {
		t.Fatal("duplicate create accepted")
	}
	if _, ok := manager.added["c1"]; !ok {
		t.Fatal("original connector lost after duplicate attempt")
	}
}

func TestServiceReservedCIDRejected(t *testing.T) {
	service, _, _ := newTestService(t, "config-c")
	if err := service.CreateConnector(context.Background(), sampleConnector("config-c"), true); err == nil {
		t.Fatal("reserved cid create accepted")
	}
}

func TestServiceLoadAndApplyRestoresPersisted(t *testing.T) {
	// Persist through one service, then re-apply through a fresh manager —
	// the restart-survival path.
	service, _, store := newTestService(t)
	if err := service.CreateConnector(context.Background(), sampleConnector("c1"), true); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateConnector(context.Background(), sampleConnector("c2"), false); err != nil {
		t.Fatal(err)
	}

	freshManager := newFakeManager()
	reloaded, err := NewService(store, freshManager, nil, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.LoadAndApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := freshManager.added["c1"]; !ok || !freshManager.started["c1"] {
		t.Fatal("c1 not restored started")
	}
	if _, ok := freshManager.added["c2"]; !ok || freshManager.started["c2"] {
		t.Fatal("c2 restored but should be stopped")
	}
}

func newTestHandler(t *testing.T, token string, reserved ...string) (*Handler, *fakeManager) {
	t.Helper()
	service, manager, _ := newTestService(t, reserved...)
	handler, err := NewHandler(service, nil, token)
	if err != nil {
		t.Fatal(err)
	}
	return handler, manager
}

func doAdmin(t *testing.T, handler *Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	return rec
}

func TestHandlerRequiresToken(t *testing.T) {
	handler, _ := newTestHandler(t, "secret")
	if rec := doAdmin(t, handler, http.MethodGet, "/admin/connectors", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status=%d want 401", rec.Code)
	}
	if rec := doAdmin(t, handler, http.MethodGet, "/admin/connectors", "wrong", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status=%d want 401", rec.Code)
	}
}

func TestHandlerConnectorLifecycle(t *testing.T) {
	handler, manager := newTestHandler(t, "secret")
	create := doAdmin(t, handler, http.MethodPost, "/admin/connectors", "secret",
		connectorCreateBody{Config: sampleConnector("c1")})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	if !strings.Contains(create.Body.String(), "\"observed\":\"BOUND\"") {
		t.Fatalf("create body missing bound status: %s", create.Body.String())
	}
	list := doAdmin(t, handler, http.MethodGet, "/admin/connectors", "secret", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "c1") {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	stop := doAdmin(t, handler, http.MethodPost, "/admin/connectors/c1/stop", "secret", nil)
	if stop.Code != http.StatusOK || manager.started["c1"] {
		t.Fatalf("stop status=%d started=%v", stop.Code, manager.started["c1"])
	}
	del := doAdmin(t, handler, http.MethodDelete, "/admin/connectors/c1", "secret", nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", del.Code)
	}
	if _, ok := manager.added["c1"]; ok {
		t.Fatal("connector not removed from manager")
	}
	missing := doAdmin(t, handler, http.MethodGet, "/admin/connectors/c1", "secret", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("get deleted status=%d want 404", missing.Code)
	}
}
