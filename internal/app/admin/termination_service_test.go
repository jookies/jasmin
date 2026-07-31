package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// fakeTerminationManager records live-apply calls. It answers Status the way the
// real manager does so the service's view projection is exercised, not stubbed
// around.
type fakeTerminationManager struct {
	added   map[string]termination.ConnectorConfig
	started map[string]bool
	addErr  error
}

func newFakeTerminationManager() *fakeTerminationManager {
	return &fakeTerminationManager{
		added:   map[string]termination.ConnectorConfig{},
		started: map[string]bool{},
	}
}

func (m *fakeTerminationManager) Add(cfg termination.ConnectorConfig) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.added[cfg.CID] = cfg
	return nil
}

func (m *fakeTerminationManager) Update(cfg termination.ConnectorConfig) error {
	if _, ok := m.added[cfg.CID]; !ok {
		return errors.New("not managed")
	}
	m.added[cfg.CID] = cfg
	return nil
}

func (m *fakeTerminationManager) Remove(cid string) error {
	delete(m.added, cid)
	delete(m.started, cid)
	return nil
}

func (m *fakeTerminationManager) Start(cid string) error { m.started[cid] = true; return nil }
func (m *fakeTerminationManager) Stop(cid string) error  { m.started[cid] = false; return nil }

func (m *fakeTerminationManager) Status(cid string) (termination.ManagedStatus, error) {
	cfg, ok := m.added[cid]
	if !ok {
		return termination.ManagedStatus{}, errors.New("not managed")
	}
	observed := termination.StatusStopped
	if m.started[cid] {
		observed = termination.StatusConsuming
	}
	return termination.ManagedStatus{
		CID: cid, Desired: m.started[cid], Observed: observed, Config: cfg,
	}, nil
}

func newTestTerminationService(t *testing.T, reserved ...string) (*TerminationService, *fakeTerminationManager, *Store) {
	t.Helper()
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := newFakeTerminationManager()
	clock := 0
	service, err := NewTerminationService(store, manager, reserved, func() string {
		clock++
		return "t" + string(rune('0'+clock))
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, manager, store
}

const testDeliverySecret = "s1gn1ng-k3y"

func sampleTerminationConnector(cid string) termination.ConnectorConfig {
	return termination.ConnectorConfig{
		CID:     cid,
		Verdict: termination.VerdictConfig{Source: termination.SourceRedisWindow},
		Delivery: termination.DeliveryConfig{
			Endpoint: "https://app.example/inbound",
			Secret:   testDeliverySecret,
		},
	}
}

func TestTerminationCreateAppliesLiveAndPersists(t *testing.T) {
	service, manager, store := newTestTerminationService(t)
	if err := service.CreateConnector(context.Background(), sampleTerminationConnector("t1"), true); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.added["t1"]; !ok || !manager.started["t1"] {
		t.Fatalf("connector not applied live: added=%v started=%v", manager.added, manager.started)
	}
	stored, err := store.GetTerminationConnector(context.Background(), "t1")
	if err != nil || !stored.DesiredStarted {
		t.Fatalf("connector not persisted: %+v err=%v", stored, err)
	}
	// WithDefaults is applied on the way in, so a read shows the delay the
	// receipt will actually be held for rather than a zero.
	if stored.Config.ReceiptDelay != termination.DefaultReceiptDelay {
		t.Fatalf("receipt delay = %v, want the default %v", stored.Config.ReceiptDelay, termination.DefaultReceiptDelay)
	}
}

func TestTerminationCreateRejectsInvalidConfigBeforeTouchingTheManager(t *testing.T) {
	service, manager, store := newTestTerminationService(t)
	invalid := sampleTerminationConnector("t1")
	invalid.Verdict.Source = "" // an empty source must never mean "accept everything"
	err := service.CreateConnector(context.Background(), invalid, true)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("create with no verdict source: err=%v want ErrInvalidRequest", err)
	}
	if len(manager.added) != 0 {
		t.Fatalf("an invalid config reached the manager: %v", manager.added)
	}
	if _, err := store.GetTerminationConnector(context.Background(), "t1"); !errors.Is(err, ErrTerminationConnectorNotFound) {
		t.Fatalf("an invalid config was persisted: %v", err)
	}
}

// The delivery secret is a credential: no read projection may carry it.
func TestTerminationReadPathsNeverReturnTheSecret(t *testing.T) {
	service, _, _ := newTestTerminationService(t)
	ctx := context.Background()
	if err := service.CreateConnector(ctx, sampleTerminationConnector("t1"), true); err != nil {
		t.Fatal(err)
	}

	view, err := service.GetConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Config.Delivery.Secret == testDeliverySecret {
		t.Fatal("GetConnector returned the delivery secret in clear")
	}
	if !view.HasSecret {
		t.Fatal("HasSecret is false for a connector that has one")
	}

	views, err := service.ListConnectors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Config.Delivery.Secret == testDeliverySecret {
		t.Fatalf("ListConnectors returned the delivery secret: %+v", views)
	}

	// Serialised, too: a marshalled view is what every HTTP surface writes.
	for _, payload := range []any{terminationViewPayload(view), terminationViewsPayload(views)} {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), testDeliverySecret) {
			t.Fatalf("the delivery secret appears in a serialised payload: %s", raw)
		}
	}
}

func TestTerminationUpdateWithoutASecretKeepsTheStoredOne(t *testing.T) {
	service, manager, store := newTestTerminationService(t)
	ctx := context.Background()
	if err := service.CreateConnector(ctx, sampleTerminationConnector("t1"), true); err != nil {
		t.Fatal(err)
	}
	update := sampleTerminationConnector("t1")
	update.Delivery.Secret = "" // the form omitted it
	update.Delivery.Endpoint = "https://app.example/v2"
	if err := service.UpdateConnector(ctx, update, false); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetTerminationConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config.Delivery.Secret != testDeliverySecret {
		t.Fatalf("stored secret = %q, want it preserved", stored.Config.Delivery.Secret)
	}
	if stored.Config.Delivery.Endpoint != "https://app.example/v2" {
		t.Fatalf("the rest of the update did not apply: %+v", stored.Config.Delivery)
	}
	if manager.added["t1"].Delivery.Secret != testDeliverySecret {
		t.Fatal("the live connector lost its signing secret on update")
	}
}

// A console renders the redaction marker and PATCHes the object back. That must
// not become the signing key.
func TestTerminationUpdateWithTheRedactionMarkerKeepsTheStoredSecret(t *testing.T) {
	service, _, store := newTestTerminationService(t)
	ctx := context.Background()
	if err := service.CreateConnector(ctx, sampleTerminationConnector("t1"), true); err != nil {
		t.Fatal(err)
	}
	view, err := service.GetConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	roundTripped := view.Config // exactly what a read handed the browser
	roundTripped.SynchronousReject = true
	if err := service.UpdateConnector(ctx, roundTripped, false); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetTerminationConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config.Delivery.Secret != testDeliverySecret {
		t.Fatalf("stored secret = %q, want the real one preserved", stored.Config.Delivery.Secret)
	}
	if !stored.Config.SynchronousReject {
		t.Fatal("the rest of the round-tripped update did not apply")
	}
}

func TestTerminationCreateNeverStoresTheRedactionMarkerAsASecret(t *testing.T) {
	service, _, store := newTestTerminationService(t)
	ctx := context.Background()
	config := sampleTerminationConnector("t1")
	config.Delivery.Secret = redactedSecretMarker
	if err := service.CreateConnector(ctx, config, false); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetTerminationConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config.Delivery.Secret != "" {
		t.Fatalf("stored secret = %q, want empty", stored.Config.Delivery.Secret)
	}
}

func TestTerminationUpdateCanClearTheSecretExplicitly(t *testing.T) {
	service, _, store := newTestTerminationService(t)
	ctx := context.Background()
	if err := service.CreateConnector(ctx, sampleTerminationConnector("t1"), false); err != nil {
		t.Fatal(err)
	}
	update := sampleTerminationConnector("t1")
	update.Delivery.Secret = ""
	if err := service.UpdateConnector(ctx, update, true); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetTerminationConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config.Delivery.Secret != "" {
		t.Fatalf("stored secret = %q, want it cleared", stored.Config.Delivery.Secret)
	}
}

// A config-declared connector is config's to own: admin may not delete, update
// or restart it, whatever the store says.
func TestTerminationReservedCIDIsNotAdminMutable(t *testing.T) {
	service, _, store := newTestTerminationService(t, "config-term")
	ctx := context.Background()

	if err := service.CreateConnector(ctx, sampleTerminationConnector("config-term"), false); !errors.Is(err, ErrConflict) {
		t.Fatalf("create over a reserved cid: err=%v want ErrConflict", err)
	}
	// Even with a row present — a stale store must not become a deletion path.
	if err := store.UpsertTerminationConnector(ctx,
		StoredTerminationConnector{Config: sampleTerminationConnector("config-term")}, "t0"); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteConnector(ctx, "config-term"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete of a reserved cid: err=%v want ErrConflict", err)
	}
	if err := service.UpdateConnector(ctx, sampleTerminationConnector("config-term"), false); !errors.Is(err, ErrConflict) {
		t.Fatalf("update of a reserved cid: err=%v want ErrConflict", err)
	}
	if err := service.SetStarted(ctx, "config-term", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stop of a reserved cid: err=%v want ErrConflict", err)
	}
	if _, err := store.GetTerminationConnector(ctx, "config-term"); err != nil {
		t.Fatalf("the reserved connector was removed anyway: %v", err)
	}
}

func TestTerminationLoadAndApplyRestoresDesiredState(t *testing.T) {
	service, manager, _ := newTestTerminationService(t)
	ctx := context.Background()
	if err := service.CreateConnector(ctx, sampleTerminationConnector("running"), true); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateConnector(ctx, sampleTerminationConnector("halted"), false); err != nil {
		t.Fatal(err)
	}
	// Simulate a restart: a fresh manager holding nothing.
	fresh := newFakeTerminationManager()
	service.manager = fresh
	service.applied = map[string]struct{}{}
	if err := service.LoadAndApply(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fresh.added) != 2 {
		t.Fatalf("re-added %d connectors, want 2", len(fresh.added))
	}
	if !fresh.started["running"] || fresh.started["halted"] {
		t.Fatalf("desired state not restored: %v", fresh.started)
	}
	_ = manager
}

func TestTerminationSetStartedPersistsDesiredState(t *testing.T) {
	service, manager, store := newTestTerminationService(t)
	ctx := context.Background()
	if err := service.CreateConnector(ctx, sampleTerminationConnector("t1"), false); err != nil {
		t.Fatal(err)
	}
	if err := service.SetStarted(ctx, "t1", true); err != nil {
		t.Fatal(err)
	}
	if !manager.started["t1"] {
		t.Fatal("connector not started live")
	}
	stored, err := store.GetTerminationConnector(ctx, "t1")
	if err != nil || !stored.DesiredStarted {
		t.Fatalf("desired-started not persisted: %+v err=%v", stored, err)
	}
	view, err := service.GetConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Observed != string(termination.StatusConsuming) {
		t.Fatalf("observed = %q, want %q", view.Observed, termination.StatusConsuming)
	}
}

// ------------------------------------------------------------------ REST ---

func newTerminationHandler(t *testing.T) (*Handler, *TerminationService) {
	t.Helper()
	service, _, store := newTestService(t)
	terminationService, err := NewTerminationService(store, newFakeTerminationManager(), []string{"config-term"},
		func() string { return "2026-07-30T00:00:00Z" })
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, nil, nil, "secret", WithTerminationConnectors(terminationService))
	if err != nil {
		t.Fatal(err)
	}
	return handler, terminationService
}

func TestTerminationRESTRoutesAreAbsentWithoutTheOption(t *testing.T) {
	handler, _ := newTestHandler(t, "secret")
	for _, path := range []string{"/admin/termination-connectors", "/admin/termination-connectors/t1"} {
		if rec := doAdmin(t, handler, http.MethodGet, path, "secret", nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d want 404 when termination is not wired", path, rec.Code)
		}
	}
}

func TestTerminationRESTRequiresTheAdminToken(t *testing.T) {
	handler, _ := newTerminationHandler(t)
	if rec := doAdmin(t, handler, http.MethodGet, "/admin/termination-connectors", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestTerminationRESTCRUDAndSecretHandling(t *testing.T) {
	handler, service := newTerminationHandler(t)
	ctx := context.Background()

	create := map[string]any{"config": sampleTerminationConnector("t1"), "start": true}
	rec := doAdmin(t, handler, http.MethodPost, "/admin/termination-connectors", "secret", create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testDeliverySecret) {
		t.Fatalf("create response leaked the secret: %s", rec.Body.String())
	}

	for _, path := range []string{"/admin/termination-connectors", "/admin/termination-connectors/t1"} {
		rec := doAdmin(t, handler, http.MethodGet, path, "secret", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), testDeliverySecret) {
			t.Fatalf("GET %s leaked the secret: %s", path, rec.Body.String())
		}
	}

	// A PUT echoing the redacted read back must not blank or overwrite it.
	var read map[string]any
	rec = doAdmin(t, handler, http.MethodGet, "/admin/termination-connectors/t1", "secret", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	rec = doAdmin(t, handler, http.MethodPut, "/admin/termination-connectors/t1", "secret",
		map[string]any{"config": read["config"]})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := service.store.GetTerminationConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config.Delivery.Secret != testDeliverySecret {
		t.Fatalf("stored secret = %q after a redacted round trip", stored.Config.Delivery.Secret)
	}

	for _, action := range []string{"stop", "start"} {
		rec := doAdmin(t, handler, http.MethodPost, "/admin/termination-connectors/t1/"+action, "secret", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", action, rec.Code, rec.Body.String())
		}
	}

	if rec := doAdmin(t, handler, http.MethodDelete, "/admin/termination-connectors/t1", "secret", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doAdmin(t, handler, http.MethodGet, "/admin/termination-connectors/t1", "secret", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status=%d", rec.Code)
	}
}

func TestTerminationRESTRefusesAReservedCID(t *testing.T) {
	handler, _ := newTerminationHandler(t)
	rec := doAdmin(t, handler, http.MethodDelete, "/admin/termination-connectors/config-term", "secret", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete of a config-owned cid: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// A stored connector must survive the JSON round trip the store performs,
// including the duration fields, which marshal as nanoseconds.
func TestTerminationStoreRoundTripsTheWholeConfig(t *testing.T) {
	_, _, store := newTestTerminationService(t)
	ctx := context.Background()
	config := sampleTerminationConnector("t1").WithDefaults()
	config.ReceiptDelay = 7 * time.Second
	config.SynchronousReject = true
	config.Delivery.Format = termination.DeliveryFormatNameLegacy
	if err := store.UpsertTerminationConnector(ctx,
		StoredTerminationConnector{Config: config, DesiredStarted: true}, "t1"); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetTerminationConnector(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config != config || !stored.DesiredStarted {
		t.Fatalf("round trip changed the config:\n got %+v\nwant %+v", stored.Config, config)
	}
}
