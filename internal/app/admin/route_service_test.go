package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// recordingProvisioner records the last applied set and can reject to simulate
// a bad spec / order collision.
type recordingProvisioner struct {
	applied   []string
	rejectSub string // reject any apply whose set contains this substring
	calls     int
}

func (p *recordingProvisioner) ApplyRoutes(_ context.Context, specs []string) error {
	p.calls++
	for _, spec := range specs {
		if p.rejectSub != "" && strings.Contains(spec, p.rejectSub) {
			return errors.New("provisioner rejected spec")
		}
	}
	p.applied = append([]string(nil), specs...)
	return nil
}

func newRouteService(t *testing.T) (*RouteService, *recordingProvisioner, *Store) {
	t.Helper()
	store, err := OpenStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	prov := &recordingProvisioner{}
	svc, err := NewRouteService(store, prov, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	return svc, prov, store
}

func TestRouteServicePutAppliesThenPersists(t *testing.T) {
	svc, prov, store := newRouteService(t)
	spec := `{"connector_id":"c","order":10,"rate":0}`
	if err := svc.PutRoute(context.Background(), 10, spec); err != nil {
		t.Fatal(err)
	}
	if len(prov.applied) != 1 || prov.applied[0] != spec {
		t.Fatalf("provisioner applied=%v", prov.applied)
	}
	got, err := store.GetRoute(context.Background(), 10)
	if err != nil || got.SpecJSON != spec {
		t.Fatalf("route not persisted: %+v err=%v", got, err)
	}
}

func TestRouteServicePutRejectedDoesNotPersist(t *testing.T) {
	svc, prov, store := newRouteService(t)
	prov.rejectSub = "order\":10"
	err := svc.PutRoute(context.Background(), 10, `{"connector_id":"c","order":10,"rate":0}`)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err=%v want ErrInvalidRequest", err)
	}
	if _, err := store.GetRoute(context.Background(), 10); !errors.Is(err, ErrRouteNotFound) {
		t.Fatal("rejected route was persisted")
	}
}

func TestRouteServiceReplaceAndDelete(t *testing.T) {
	svc, prov, _ := newRouteService(t)
	if err := svc.PutRoute(context.Background(), 10, `{"connector_id":"a","order":10,"rate":0}`); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutRoute(context.Background(), 20, `{"connector_id":"b","order":20,"rate":0}`); err != nil {
		t.Fatal(err)
	}
	// Replace order 10 — the applied set must still have exactly two entries.
	if err := svc.PutRoute(context.Background(), 10, `{"connector_id":"a2","order":10,"rate":0}`); err != nil {
		t.Fatal(err)
	}
	if len(prov.applied) != 2 {
		t.Fatalf("after replace applied=%v want 2 entries", prov.applied)
	}
	if err := svc.DeleteRoute(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if len(prov.applied) != 1 || !strings.Contains(prov.applied[0], "\"b\"") {
		t.Fatalf("after delete applied=%v want only order-20", prov.applied)
	}
	if err := svc.DeleteRoute(context.Background(), 10); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("second delete err=%v want ErrRouteNotFound", err)
	}
}

func TestRouteServiceLoadAndApplyReplaysAll(t *testing.T) {
	svc, _, store := newRouteService(t)
	if err := svc.PutRoute(context.Background(), 10, `{"connector_id":"a","order":10,"rate":0}`); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutRoute(context.Background(), 20, `{"connector_id":"b","order":20,"rate":0}`); err != nil {
		t.Fatal(err)
	}
	freshProv := &recordingProvisioner{}
	reloaded, err := NewRouteService(store, freshProv, func() string { return "t" })
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.LoadAndApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(freshProv.applied) != 2 {
		t.Fatalf("reload applied=%v want 2", freshProv.applied)
	}
}
