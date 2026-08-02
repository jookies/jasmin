package adminweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// seedTopology provisions a small but complete deployment through the same
// endpoints an operator would use, so the graph is built from real stored state
// rather than from hand-made resource structs.
func seedTopology(f *webFixture) {
	f.do("POST", "/api/connectors",
		`{"cid":"smsc-a","host":"smsc.example.com","port":2775,"system_id":"jasmin","password":"bind-pw","bind":"transceiver","desired_started":true}`,
		http.StatusCreated, nil)
	f.do("POST", "/api/groups", `{"gid":"eu"}`, http.StatusCreated, nil)
	f.do("POST", "/api/users",
		`{"username":"partner-a","password":"partner-pw","group_id":"eu"}`,
		http.StatusCreated, nil)
	f.do("POST", "/api/routes",
		`{"order":10,"rate":0.02,"connector_id":"smsc-a"}`,
		http.StatusCreated, nil)
}

func TestTopologyRequiresSession(t *testing.T) {
	f := newWebFixture(t)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/topology", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/topology: code %d, want 401", rec.Code)
	}
	// The whole configuration is in this document; an unauthenticated caller
	// must not learn even the node kinds from an error body.
	if strings.Contains(rec.Body.String(), "nodes") {
		t.Errorf("unauthenticated response mentioned graph content: %s", rec.Body.String())
	}
}

func TestTopologyServesTheDeployment(t *testing.T) {
	f := newWebFixture(t)
	seedTopology(f)

	var graph Graph
	f.do("GET", "/api/topology", "", http.StatusOK, &graph)

	if graph.StructureHash == "" {
		t.Error("document carries no structure hash; the browser would relayout on every poll")
	}
	if graph.ObservedAt == "" {
		t.Error("document carries no observation time")
	}
	if graph.CountersSince == "" {
		t.Error("document does not say when its counters started; they reset on restart")
	}

	for _, id := range []string{"connector:smsc-a", "mt-route:10", "user:partner-a", "account-group:eu", nodeCore} {
		nodeByID(t, graph, id)
	}
	if got := nodeByID(t, graph, "connector:smsc-a").ManagedBy; got != "admin" {
		t.Errorf("provisioned connector managed_by = %q, want admin", got)
	}

	// The route points at a connector the fake manager reports unbound, so the
	// map must show the broken path rather than a healthy-looking line.
	edge := edgeByID(t, graph, "edge:mt-route:10->connector:smsc-a")
	if !edge.Degraded {
		t.Error("route to an unbound connector was not marked degraded")
	}
	if problem := problemByKind(t, graph, ProblemBrokenPath); problem.Count == 0 {
		t.Error("problem rail reports no broken paths while an edge is degraded")
	}
}

// TestTopologyEndpointRedactsSecrets repeats the model-level redaction check at
// the HTTP boundary, over state that really went through the stores. The two
// are worth having separately: this one would catch a leak introduced by a
// collector or by resource serialization rather than by the graph builder.
func TestTopologyEndpointRedactsSecrets(t *testing.T) {
	f := newWebFixture(t)
	seedTopology(f)

	rec := f.do("GET", "/api/topology", "", http.StatusOK, nil)
	body := rec.Body.String()

	for name, secret := range map[string]string{
		"connector bind password": "bind-pw",
		"user password":           "partner-pw",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("/api/topology leaked the %s", name)
		}
	}
	// A hash is as disclosing as the secret for offline attack purposes.
	if strings.Contains(body, "password_sha256") || strings.Contains(body, "password_md5") {
		t.Error("/api/topology carried a password digest field")
	}
}

func TestTopologyWithoutMetricsRegistry(t *testing.T) {
	f := newWebFixture(t)
	// newWebFixture leaves Deps.Metrics nil, which is the supported "no
	// registry wired" state.
	var graph Graph
	f.do("GET", "/api/topology", "", http.StatusOK, &graph)

	if !graph.MetricsStale {
		t.Error("a gateway with no metrics registry must set metrics_stale rather than report zeros")
	}
}

// identities renders the graph's shape as a stable string: what the browser
// lays out, with none of the values that legitimately move between polls.
func identities(nodes []Node, edges []Edge) string {
	parts := make([]string, 0, len(nodes)+len(edges))
	for _, node := range nodes {
		parts = append(parts, "n:"+node.ID+"/"+node.Kind+"/"+node.Parent)
	}
	for _, edge := range edges {
		parts = append(parts, "e:"+edge.ID+"/"+edge.From+"->"+edge.To)
	}
	return strings.Join(parts, "\n")
}

func TestTopologyIsStableAcrossPolls(t *testing.T) {
	f := newWebFixture(t)
	seedTopology(f)

	var first, second Graph
	f.do("GET", "/api/topology", "", http.StatusOK, &first)
	f.do("GET", "/api/topology", "", http.StatusOK, &second)

	if first.StructureHash != second.StructureHash {
		t.Fatalf("structure hash changed between two idle polls: %s -> %s",
			first.StructureHash, second.StructureHash)
	}

	// Node and edge ordering must be identical poll to poll: the browser keys
	// its layout off this document, and a map iteration leaking into the order
	// would reshuffle the canvas on a timer. Values are deliberately not
	// compared — uptime and counters are supposed to move between polls, and
	// whole-document determinism is pinned at the model level with a fixed
	// clock in TestBuildGraphIsDeterministic.
	if got, want := identities(second.Nodes, second.Edges), identities(first.Nodes, first.Edges); got != want {
		t.Errorf("two idle polls ordered the graph differently:\n%s\n%s", want, got)
	}

	// Adding a connector must move the hash, or a new entity would never appear
	// in the right place.
	f.do("POST", "/api/connectors",
		`{"cid":"smsc-b","host":"smsc-b.example.com","port":2775,"system_id":"jasmin","password":"pw","bind":"transceiver"}`,
		http.StatusCreated, nil)
	var third Graph
	f.do("GET", "/api/topology", "", http.StatusOK, &third)
	if third.StructureHash == first.StructureHash {
		t.Error("adding a connector did not change the structure hash")
	}
}
