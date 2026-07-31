package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

// strictRouteProvisioner parses each spec exactly as the gateway's real
// provisioner does — into outbound.RouteConfig with unknown fields refused —
// so a field that never reaches the routing table fails here rather than in
// production. It is the "restart-reload" half of the round trip: LoadAndApply
// feeds it the persisted specs the way boot does.
type strictRouteProvisioner struct{ applied []outbound.RouteConfig }

func (p *strictRouteProvisioner) ApplyRoutes(_ context.Context, specs []string) error {
	routes := make([]outbound.RouteConfig, 0, len(specs))
	for _, spec := range specs {
		var route outbound.RouteConfig
		decoder := json.NewDecoder(bytes.NewReader([]byte(spec)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&route); err != nil {
			return err
		}
		routes = append(routes, route)
	}
	p.applied = routes
	return nil
}

// A route to a termination connector must keep its type through create, list
// and the boot-time re-apply. Without it the route loads as smppc and looks for
// an outbound carrier connector that does not exist — the failure is silent at
// creation and only visible when a message is dropped.
func TestRouteConnectorTypeSurvivesCreateListAndReload(t *testing.T) {
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provisioner := &strictRouteProvisioner{}
	service, err := NewRouteService(store, provisioner, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	spec := `{"connector_id":"partner-a-term","connector_type":"term","order":10,"rate":0.02,"default":false}`
	if err := service.PutRoute(ctx, 10, spec); err != nil {
		t.Fatal(err)
	}
	if len(provisioner.applied) != 1 || provisioner.applied[0].ConnectorType != "term" {
		t.Fatalf("apply-on-create lost the connector type: %+v", provisioner.applied)
	}

	listed, err := service.ListRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var fromList outbound.RouteConfig
	if err := json.Unmarshal([]byte(listed[0].SpecJSON), &fromList); err != nil {
		t.Fatal(err)
	}
	if fromList.ConnectorType != "term" {
		t.Fatalf("list lost the connector type: %+v", fromList)
	}

	// Restart: a fresh provisioner replays the persisted set.
	reloaded := &strictRouteProvisioner{}
	service, err = NewRouteService(store, reloaded, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	if err := service.LoadAndApply(ctx); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.applied) != 1 || reloaded.applied[0].ConnectorType != "term" {
		t.Fatalf("reload lost the connector type: %+v", reloaded.applied)
	}
	if reloaded.applied[0].ConnectorID != "partner-a-term" {
		t.Fatalf("reload lost the connector id: %+v", reloaded.applied)
	}
}

// The same route through the /admin REST surface, which carries the spec as
// opaque JSON. "Opaque" is only safe if nothing along the way re-serialises it
// through a narrower struct.
func TestAdminRESTRouteCarriesTheConnectorType(t *testing.T) {
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provisioner := &strictRouteProvisioner{}
	routeService, err := NewRouteService(store, provisioner, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	connectorService, err := NewService(store, newFakeManager(), nil, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(connectorService, routeService, nil, "secret")
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"connector_id": "partner-a-term", "connector_type": "term", "order": 10, "rate": 0.02,
	}
	if rec := doAdmin(t, handler, http.MethodPost, "/admin/routes", "secret", body); rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec := doAdmin(t, handler, http.MethodGet, "/admin/routes/10", "secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Spec outbound.RouteConfig `json:"spec"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Spec.ConnectorType != "term" {
		t.Fatalf("REST read lost the connector type: %+v", payload.Spec)
	}
}
