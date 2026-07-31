package adminweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// decodeStrict parses a stored spec the way the gateway's route provisioner
// does — unknown fields refused — so a spec that would be rejected at boot is
// rejected here.
func decodeStrict(specJSON string, target any) error {
	decoder := json.NewDecoder(bytes.NewReader([]byte(specJSON)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// fakeTerminationManager is the console-side twin of the admin fixture: it
// records what was applied and reports status the way the real manager does.
type fakeTerminationManager struct {
	added   map[string]termination.ConnectorConfig
	started map[string]bool
}

func newFakeTerminationManager() *fakeTerminationManager {
	return &fakeTerminationManager{
		added:   map[string]termination.ConnectorConfig{},
		started: map[string]bool{},
	}
}

func (m *fakeTerminationManager) Add(cfg termination.ConnectorConfig) error {
	if _, ok := m.added[cfg.CID]; ok {
		return errors.New("already managed")
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
	return termination.ManagedStatus{CID: cid, Desired: m.started[cid], Observed: observed, Config: cfg}, nil
}

const webDeliverySecret = "s1gn1ng-k3y"

// enableTermination rebuilds the handler with the termination surface wired,
// mirroring how the interceptor capability is switched on in these tests.
func enableTermination(f *webFixture, reserved ...string) *fakeTerminationManager {
	f.t.Helper()
	manager := newFakeTerminationManager()
	service, err := admin.NewTerminationService(f.store, manager, reserved,
		func() string { return "2026-07-30T00:00:00Z" })
	if err != nil {
		f.t.Fatalf("new termination service: %v", err)
	}
	f.rebuildHandler(func(d *Deps) { d.TerminationConnectors = service })
	return manager
}

func terminationBody(cid string, start bool) string {
	body := `{"cid":"` + cid + `","verdict":{"source":"redis-window"},` +
		`"delivery":{"endpoint":"https://app.example/inbound","secret":"` + webDeliverySecret + `"},` +
		`"desired_started":`
	if start {
		return body + `true}`
	}
	return body + `false}`
}

// Without the dependency the resource must not exist at all.
func TestTerminationConnectorsAbsentWhenNotWired(t *testing.T) {
	f := newWebFixture(t)
	f.do("GET", "/api/termination-connectors", "", http.StatusNotFound, nil)
	f.do("POST", "/api/termination-connectors", terminationBody("t1", true), http.StatusNotFound, nil)
}

func TestTerminationConnectorCRUD(t *testing.T) {
	f := newWebFixture(t)
	manager := enableTermination(f)

	var created terminationConnectorResource
	f.do("POST", "/api/termination-connectors", terminationBody("t1", true), http.StatusCreated, &created)
	if created.ID != "t1" || !created.DesiredStarted {
		t.Fatalf("created = %+v", created)
	}
	if !manager.started["t1"] {
		t.Fatal("connector not started live")
	}
	if created.Observed != string(termination.StatusConsuming) {
		t.Fatalf("observed = %q", created.Observed)
	}
	if !created.HasSecret {
		t.Fatal("has_secret is false for a connector configured with one")
	}
	// Defaults are visible in the read, so an operator sees the receipt delay
	// that will actually apply.
	if created.ReceiptDelay != termination.DefaultReceiptDelay {
		t.Fatalf("receipt delay = %v, want %v", created.ReceiptDelay, termination.DefaultReceiptDelay)
	}

	var listed []terminationConnectorResource
	f.do("GET", "/api/termination-connectors", "", http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].ID != "t1" {
		t.Fatalf("listed = %+v", listed)
	}

	// Stop through the action endpoint, start through the desired_started field:
	// both shapes exist and both must persist.
	var stopped terminationConnectorResource
	f.do("POST", "/api/termination-connectors/t1/stop", "", http.StatusOK, &stopped)
	if stopped.DesiredStarted || manager.started["t1"] {
		t.Fatalf("stop did not apply: %+v", stopped)
	}
	var restarted terminationConnectorResource
	f.do("PATCH", "/api/termination-connectors/t1", `{"desired_started":true}`, http.StatusOK, &restarted)
	if !restarted.DesiredStarted || !manager.started["t1"] {
		t.Fatalf("restart did not apply: %+v", restarted)
	}

	f.do("DELETE", "/api/termination-connectors/t1", "", http.StatusOK, nil)
	f.do("GET", "/api/termination-connectors/t1", "", http.StatusNotFound, nil)
	if _, still := manager.added["t1"]; still {
		t.Fatal("connector still managed after delete")
	}
}

// No console read may carry the delivery secret — not the create response, not
// the list, not the detail.
func TestTerminationConnectorSecretIsNeverReturned(t *testing.T) {
	f := newWebFixture(t)
	enableTermination(f)

	rec := f.do("POST", "/api/termination-connectors", terminationBody("t1", false), http.StatusCreated, nil)
	if strings.Contains(rec.Body.String(), webDeliverySecret) {
		t.Fatalf("create response leaked the secret: %s", rec.Body.String())
	}
	for _, path := range []string{"/api/termination-connectors", "/api/termination-connectors/t1"} {
		rec := f.do("GET", path, "", http.StatusOK, nil)
		if strings.Contains(rec.Body.String(), webDeliverySecret) {
			t.Fatalf("GET %s leaked the secret: %s", path, rec.Body.String())
		}
	}
}

// The UI reads a connector, edits one field and PATCHes the whole object back —
// including the "***" it was shown. That must leave the credential alone.
func TestTerminationConnectorRedactedRoundTripKeepsTheSecret(t *testing.T) {
	f := newWebFixture(t)
	manager := enableTermination(f)
	f.do("POST", "/api/termination-connectors", terminationBody("t1", false), http.StatusCreated, nil)

	var read terminationConnectorResource
	f.do("GET", "/api/termination-connectors/t1", "", http.StatusOK, &read)
	if read.Delivery.Secret == webDeliverySecret {
		t.Fatal("the read returned the real secret")
	}
	roundTrip := `{"cid":"t1","verdict":{"source":"redis-window"},` +
		`"delivery":{"endpoint":"https://app.example/v2","secret":"` + read.Delivery.Secret + `"}}`
	f.do("PATCH", "/api/termination-connectors/t1", roundTrip, http.StatusOK, nil)

	live := manager.added["t1"]
	if live.Delivery.Secret != webDeliverySecret {
		t.Fatalf("live secret = %q after a redacted round trip", live.Delivery.Secret)
	}
	if live.Delivery.Endpoint != "https://app.example/v2" {
		t.Fatalf("the rest of the update did not apply: %+v", live.Delivery)
	}
}

// A PATCH that omits the delivery block entirely keeps the stored secret too.
func TestTerminationConnectorUpdateWithoutSecretKeepsIt(t *testing.T) {
	f := newWebFixture(t)
	manager := enableTermination(f)
	f.do("POST", "/api/termination-connectors", terminationBody("t1", false), http.StatusCreated, nil)

	f.do("PATCH", "/api/termination-connectors/t1", `{"synchronous_reject":true}`, http.StatusOK, nil)
	live := manager.added["t1"]
	if live.Delivery.Secret != webDeliverySecret {
		t.Fatalf("live secret = %q, want it preserved", live.Delivery.Secret)
	}
	if !live.SynchronousReject {
		t.Fatal("the update did not apply")
	}
}

func TestTerminationConnectorSecretCanBeClearedExplicitly(t *testing.T) {
	f := newWebFixture(t)
	manager := enableTermination(f)
	f.do("POST", "/api/termination-connectors", terminationBody("t1", false), http.StatusCreated, nil)

	var updated terminationConnectorResource
	f.do("PATCH", "/api/termination-connectors/t1", `{"clear_delivery_secret":true}`, http.StatusOK, &updated)
	if manager.added["t1"].Delivery.Secret != "" {
		t.Fatalf("secret not cleared: %q", manager.added["t1"].Delivery.Secret)
	}
	if updated.HasSecret {
		t.Fatal("has_secret still true after clearing")
	}
}

// A config-declared termination connector is visible and read-only.
func TestTerminationConfigConnectorIsVisibleAndNotDeletable(t *testing.T) {
	f := newWebFixture(t)
	manager := newFakeTerminationManager()
	service, err := admin.NewTerminationService(f.store, manager, []string{"config-term"},
		func() string { return "2026-07-30T00:00:00Z" })
	if err != nil {
		t.Fatalf("new termination service: %v", err)
	}
	configured := termination.ConnectorConfig{
		CID:      "config-term",
		Verdict:  termination.VerdictConfig{Source: termination.SourceStatic},
		Delivery: termination.DeliveryConfig{Endpoint: "https://app.example/x", Secret: webDeliverySecret},
	}
	f.rebuildHandler(func(d *Deps) {
		d.TerminationConnectors = service
		d.ConfigTerminationConnectors = func() []termination.ConnectorConfig {
			return []termination.ConnectorConfig{configured}
		}
	})

	var listed []terminationConnectorResource
	f.do("GET", "/api/termination-connectors", "", http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].ManagedBy != "config" {
		t.Fatalf("config connector not listed read-only: %+v", listed)
	}
	if listed[0].Delivery.Secret == webDeliverySecret {
		t.Fatal("the config connector's secret was returned in clear")
	}
	if !listed[0].HasSecret {
		t.Fatal("has_secret is false for a config connector that has one")
	}

	f.do("DELETE", "/api/termination-connectors/config-term", "", http.StatusNotFound, nil)
	f.do("POST", "/api/termination-connectors", terminationBody("config-term", false), http.StatusConflict, nil)
}

// The console must be able to point an MT route at a termination connector, and
// the type must survive the round trip and the boot-time re-apply. A route that
// silently defaults to smppc looks created and never carries traffic.
func TestRouteToTerminationConnectorRoundTrips(t *testing.T) {
	f := newWebFixture(t)

	var created routeResource
	f.do("POST", "/api/routes",
		`{"order":10,"connector_id":"partner-a-term","connector_type":"term","rate":0.02}`,
		http.StatusCreated, &created)
	if created.ConnectorType != "term" {
		t.Fatalf("create lost the connector type: %+v", created)
	}

	var listed []routeResource
	f.do("GET", "/api/routes", "", http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].ConnectorType != "term" {
		t.Fatalf("list lost the connector type: %+v", listed)
	}

	// An edit that does not mention the type must not drop it.
	var patched routeResource
	f.do("PATCH", "/api/routes/10", `{"rate":0.05}`, http.StatusOK, &patched)
	if patched.ConnectorType != "term" || patched.Rate != 0.05 {
		t.Fatalf("patch lost the connector type: %+v", patched)
	}

	// Restart: replay the persisted specs through a provisioner that parses them
	// exactly as the gateway does.
	stored, err := f.deps.Routes.ListRoutes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored %d routes, want 1", len(stored))
	}
	var reloaded outbound.RouteConfig
	if err := decodeStrict(stored[0].SpecJSON, &reloaded); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ConnectorType != "term" || reloaded.ConnectorID != "partner-a-term" {
		t.Fatalf("reload lost the route: %+v", reloaded)
	}
}
