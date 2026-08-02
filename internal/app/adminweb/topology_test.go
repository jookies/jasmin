package adminweb

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/modispatch"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/app/smppsserver"
	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// Secrets planted in the fixture. Every one of them reaches BuildGraph on an
// input struct, and none may appear in the serialized document.
const (
	secretConnectorPassword = "sup3r-secret-bind-password"
	secretInterceptorCode   = "import os; os.system('curl evil.example')"
	secretSMPPsPassword     = "smpps-bind-secret"
	secretUserPassword      = "user-plaintext-secret"
)

func topologyFixture() GraphInputs {
	started := time.Date(2026, 7, 31, 3, 25, 5, 0, time.UTC)
	observed := time.Date(2026, 7, 31, 15, 43, 27, 0, time.UTC)

	maxBindings := 4
	liveBalance := 142.36
	liveMessages := 8400

	return GraphInputs{
		Connectors: []connectorResource{
			{
				ID: "smsc-primary",
				Config: smppc.Config{
					CID: "smsc-primary", Host: "smsc.example.net", Port: 2775,
					SystemID: "synevyr", Password: secretConnectorPassword, Bind: "transceiver",
				},
				DesiredStarted: true, Observed: "BOUND", ManagedBy: "admin",
			},
			{
				ID: "promo-unbound",
				Config: smppc.Config{
					CID: "promo-unbound", Host: "promo.example.net", Port: 2775,
					SystemID: "promo", Password: secretConnectorPassword, Bind: "transmitter",
				},
				DesiredStarted: true, Observed: "DISCONNECTED", ManagedBy: "config",
			},
		},
		Termination: []terminationConnectorResource{{
			ID: "local-app",
			ConnectorConfig: termination.ConnectorConfig{
				CID: "local-app",
				Delivery: termination.DeliveryConfig{
					Endpoint: "https://app.partner.example/inbound", MaxAttempts: 5,
				},
			},
			DesiredStarted: true, Observed: "CONSUMING", ManagedBy: "admin", HasSecret: true,
		}},
		PullTokens: []messageConsumerResource{
			// Pulling normally.
			{
				ID: "tok-live", Label: "acme-prod",
				Scope:      msgspool.Scope{Connectors: []string{"local-app"}, IncludeText: true},
				LastReadAt: stamp(observed.Add(-20 * time.Second)),
				Reads:      counter(41_000), Rows: counter(120_000), Denied: counter(0),
			},
			// Silent for three hours: an outage, not a slow poller.
			{
				ID: "tok-lost", Label: "globex-prod",
				Scope:      msgspool.Scope{Connectors: []string{"local-app"}},
				LastReadAt: stamp(observed.Add(-3 * time.Hour)),
				Reads:      counter(900), Rows: counter(2_400), Denied: counter(0),
			},
			// Issued and never wired up.
			{
				ID: "tok-unused", Label: "initech-staging",
				Scope: msgspool.Scope{Connectors: []string{"local-app"}},
			},
		},
		SpoolRetention: 24 * time.Hour,
		Routes: []routeResource{
			{
				ID: 100, ManagedBy: "admin",
				RouteConfig: outbound.RouteConfig{
					Order: 100, Rate: 0.02, ConnectorID: "smsc-primary",
					Filters: []outbound.FilterConfig{{Type: "destination_addr", Pattern: "^33"}},
				},
			},
			{
				ID: 10, ManagedBy: "admin",
				RouteConfig: outbound.RouteConfig{
					Order: 10, Rate: 0.01, ConnectorID: "promo-unbound",
				},
			},
			{
				ID: 5, ManagedBy: "config",
				RouteConfig: outbound.RouteConfig{
					Order: 5, Rate: 0.03, ConnectorID: "ghost-connector",
				},
			},
			// A termination route. The wire value is "term", the routing
			// engine's own constant — spelling it "termination" here would
			// resolve it against the SMPP connectors and report a healthy path
			// as broken, which is exactly what shipped before this case existed.
			{
				ID: 7, ManagedBy: "admin",
				RouteConfig: outbound.RouteConfig{
					Order: 7, Rate: 0.04, ConnectorID: "local-app", ConnectorType: "term",
				},
			},
		},
		MORoutes: []moRouteResource{{
			ID: 20, ManagedBy: "admin",
			RouteConfig: modispatch.RouteConfig{Order: 20, FilterConnectorID: "smsc-primary"},
		}},
		Users: []userResource{{
			ID: "partner-a", Username: "partner-a", ManagedBy: "admin", GroupID: "eu",
			Password: secretUserPassword, LiveBalance: &liveBalance, LiveSubmitSMCount: &liveMessages,
		}},
		Groups: []groupResource{{ID: "eu", GID: "eu", ManagedBy: "admin"}},
		SMPPsUsers: []smppsUserResource{{
			ID: "partner-a", ManagedBy: "admin",
			UserConfig: smppsserver.UserConfig{
				SystemID: "partner-a", Password: secretSMPPsPassword, MaxBindings: &maxBindings,
			},
		}},
		Filters:          []filterResource{{ID: "geo-eu-allow", FID: "geo-eu-allow", Type: "DestinationAddrFilter"}},
		HTTPDestinations: []httpConnectorResource{{ID: "partner-callback", CID: "partner-callback", BaseURL: "https://partner.example/mo", Method: "POST"}},
		Interceptors: []interceptorResource{{
			ID: "mt:1", Direction: "mt",
			InterceptorConfig: outbound.InterceptorConfig{Order: 1, PyCode: secretInterceptorCode},
		}},

		// A literal snapshot rather than one taken from a live registry: the
		// registry stamps connector uptime from the wall clock, so two fixture
		// builds microseconds apart would differ by a fraction of a second and
		// TestBuildGraphIsDeterministic would fail for a reason the product
		// does not have.
		Metrics: stats.Snapshot{
			ObservedAt: observed,
			Submits: map[stats.SubmitKey]uint64{
				{Connector: "smsc-primary", Outcome: stats.SubmitSuccess, Status: "ESME_ROK"}: 5090,
				{Connector: "smsc-primary", Outcome: stats.SubmitAttempt, Status: "pending"}:  5100,
			},
			SubmitLatency: map[string]stats.LatencyObservation{
				"smsc-primary": {
					Buckets: []stats.HistogramBucket{
						{UpperBound: 0.05, Count: 4000},
						{UpperBound: 0.1, Count: 5090},
					},
					Count: 5090, Sum: 402.1,
				},
			},
			MOs: map[stats.MOKey]uint64{{Connector: "smsc-primary", Outcome: stats.MORouted}: 1841},
			RouteMatches: map[stats.RouteMatchKey]uint64{
				{Route: "mt:100", Connector: "smsc-primary"}: 4820,
				{Route: "mt:100", Connector: "smsc-backup"}:  270,
			},
			RouteLastMatch: map[string]time.Time{"mt:100": observed.Add(-2 * time.Second)},
			DLRs:           map[stats.DLRKey]uint64{{Level: "2", FinalState: "DELIVRD", Outcome: stats.DLROutcomeDelivered}: 4980},
			QueueDepths: map[string]int64{
				"submit.sm.smsc-primary":  428,
				"submit.sm.promo-unbound": 12480,
			},
			Connectors: map[string]stats.ConnectorObservation{
				"smsc-primary": {State: "BOUND", Bound: true, UptimeSeconds: 44302},
			},
			TerminationSpool: map[string]stats.TerminationSpoolCensus{"local-app": {Rows: 91}},
			GatewayHealth:    "ok",
			GatewayReady:     true,
		},
		MetricsWired:  true,
		HTTPCounters:  map[string]int64{"request_count": 5100, "success_count": 5090},
		SMPPsCounters: map[string]int64{"connected_count": 38, "bound_trx_count": 30},

		Health:       map[string]string{"amqp": "ok", "postgres": "ok", "connector:smsc-primary": "bound"},
		HealthStatus: "ok",

		Ingress: IngressSnapshot{
			HTTPBindAddress: "0.0.0.0:1401", SMPPSBindAddress: "0.0.0.0:2775",
			DLRThrowerRunning: true, MOThrowerRunning: false,
			CallbackTimeoutSeconds: 30, CallbackMaxRetries: 3,
		},
		HasIngress: true,

		StartedAt:  started,
		ObservedAt: observed,
	}
}

func stamp(at time.Time) *string {
	value := at.UTC().Format(time.RFC3339Nano)
	return &value
}

func counter(value int64) *int64 { return &value }

func nodeByID(t *testing.T, graph Graph, id string) Node {
	t.Helper()
	for _, node := range graph.Nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("graph has no node %q", id)
	return Node{}
}

func edgeByID(t *testing.T, graph Graph, id string) Edge {
	t.Helper()
	for _, edge := range graph.Edges {
		if edge.ID == id {
			return edge
		}
	}
	t.Fatalf("graph has no edge %q", id)
	return Edge{}
}

func problemByKind(t *testing.T, graph Graph, kind string) Problem {
	t.Helper()
	for _, problem := range graph.Problems {
		if problem.Kind == kind {
			return problem
		}
	}
	t.Fatalf("graph has no %q problem entry", kind)
	return Problem{}
}

// A gauge that has never been observed is not a gauge reading zero. The spool
// census is a periodic measurement, so for the first moments after a restart
// every value is absent — and "0 rows held" would tell an operator the spool
// had drained when nothing had looked at it.
func TestUnmeasuredSpoolIsNotZero(t *testing.T) {
	in := topologyFixture()
	in.Metrics.TerminationSpool = map[string]stats.TerminationSpoolCensus{}

	graph := BuildGraph(in)
	spool := nodeByID(t, graph, nodeSpool)
	if spool.Status != StatusUnknown {
		t.Errorf("unobserved spool status = %q, want %q", spool.Status, StatusUnknown)
	}
	if spool.Sublabel != "not yet measured" {
		t.Errorf("unobserved spool sublabel = %q", spool.Sublabel)
	}
	for _, field := range spool.Detail {
		if field.Label == "Rows held" && field.Value != "—" {
			t.Errorf("unobserved row count rendered as %q, want an em dash", field.Value)
		}
	}
	// The analytics matrix reads Metrics, so an unobserved gauge must be absent
	// there too — otherwise the card says "not yet measured" while the table
	// beneath it says 0.
	if spool.Metrics != nil {
		t.Errorf("unobserved spool published metrics %v", spool.Metrics)
	}

	// An observed empty spool is a different, and truthful, zero.
	in.Metrics.TerminationSpool = map[string]stats.TerminationSpoolCensus{"local-app": {}}
	observed := nodeByID(t, BuildGraph(in), nodeSpool)
	if observed.Status != StatusOK || observed.Sublabel != "0 rows" {
		t.Errorf("observed empty spool: status %q sublabel %q", observed.Status, observed.Sublabel)
	}
}

// TestLanesAndColumnsAreBalanced guards the layout's readability, which is a
// product property here rather than a cosmetic one: the first cut put nine
// cards in the egress column while policy, core and queues held one each, and
// the result was a tall sprawl of crossing edges that could not be read as a
// flow at all.
func TestLanesAndColumnsAreBalanced(t *testing.T) {
	graph := BuildGraph(withAnalytics(topologyFixture()))

	perColumn := map[string]int{}
	perLane := map[string]int{}
	for _, node := range graph.Nodes {
		if node.Parent != "" {
			continue
		}
		perColumn[node.Column]++
		lane := node.Lane
		if lane == "" {
			lane = LaneMT
		}
		perLane[lane]++
	}

	// No column may dominate. Eight is generous — the point is to fail loudly
	// if a new node kind is dropped into egress because it was handy.
	for column, count := range perColumn {
		if count > 8 {
			t.Errorf("column %q holds %d cards; split it across a stage or a lane", column, count)
		}
	}

	// The return path must be its own lane, or MT and MO interleave and neither
	// direction can be followed.
	if perLane[LaneReturn] == 0 {
		t.Error("no node is in the return lane; MO and DLR would interleave with MT")
	}
	for _, id := range []string{nodeMORoutes, nodeHTTPDests, nodeDLRThrower, nodeMOThrower} {
		if got := nodeByID(t, graph, id).Lane; got != LaneReturn {
			t.Errorf("%s lane = %q, want %q", id, got, LaneReturn)
		}
	}

	// Infrastructure carries no traffic and belongs under the core, not in a
	// far-right column reached by an edge crossing the whole diagram.
	infra := nodeByID(t, graph, nodeInfra)
	if infra.Lane != LaneInfra || infra.Column != ColumnCore {
		t.Errorf("infrastructure at lane %q column %q, want %q/%q",
			infra.Lane, infra.Column, LaneInfra, ColumnCore)
	}

	// What the customer receives is its own stage, after the carriers.
	for _, id := range []string{nodeSpool, nodePullTokens} {
		if got := nodeByID(t, graph, id).Column; got != ColumnDelivery {
			t.Errorf("%s column = %q, want %q", id, got, ColumnDelivery)
		}
	}
}

// TestRouteMatchCounters answers "is this rule actually carrying traffic",
// which the route table alone cannot: an order and a filter say what *would*
// match, never what has.
func TestRouteMatchCounters(t *testing.T) {
	graph := BuildGraph(topologyFixture())

	busy := nodeByID(t, graph, "mt-route:100")
	// A pool route's matches total across the connectors it selected.
	if got := busy.Metrics["matches"]; got != 5090 {
		t.Errorf("route 100 matches = %v, want 5090 across both connectors", got)
	}
	detail := map[string]string{}
	for _, field := range busy.Detail {
		detail[field.Label] = field.Value
	}
	if detail["Last match"] != "00:00:02 ago" {
		t.Errorf("last match = %q, want the age", detail["Last match"])
	}
	if strings.Contains(busy.Sublabel, "never matched") {
		t.Error("a route that has matched was labelled never matched")
	}

	// A route that never fired says so. On a busy gateway that usually means
	// its filter is wrong or a higher-order route shadows it, and no table view
	// can show it.
	quiet := nodeByID(t, graph, "mt-route:10")
	if !strings.Contains(quiet.Sublabel, "never matched") {
		t.Errorf("unused route sublabel = %q, want a never-matched note", quiet.Sublabel)
	}
	for _, field := range quiet.Detail {
		if field.Label == "Last match" && field.Value != "never" {
			t.Errorf("unused route last match = %q, want %q", field.Value, "never")
		}
	}

	// The default route is exempt: it is the catch-all, and calling it unused
	// on a gateway with no traffic yet would be noise.
	in := topologyFixture()
	in.Routes = append(in.Routes, routeResource{
		ID: 1, ManagedBy: "admin",
		RouteConfig: outbound.RouteConfig{Order: 1, Rate: 0.01, ConnectorID: "smsc-primary", Default: true},
	})
	if got := nodeByID(t, BuildGraph(in), "mt-route:1").Sublabel; strings.Contains(got, "never matched") {
		t.Errorf("default route was labelled never matched: %q", got)
	}
}

// --- the analytics matrix ---------------------------------------------------

func withAnalytics(in GraphInputs) GraphInputs {
	in.PullWindows = []PullWindowActivity{
		{Window: "15m", ByToken: map[string]ActivityWindow{
			"tok-live": {Reads: 180, Rows: 540},
		}},
		{Window: "1h", ByToken: map[string]ActivityWindow{
			"tok-live": {Reads: 720, Rows: 2100},
		}},
		{Window: "24h", ByToken: map[string]ActivityWindow{
			"tok-live": {Reads: 17000, Rows: 48000},
			"tok-lost": {Reads: 410, Rows: 1200},
		}},
	}
	return in
}

// Analytics costs an audit aggregate per window, so it must stay absent unless
// it was asked for — the map polls every five seconds and does not need it.
func TestAnalyticsIsAbsentUnlessRequested(t *testing.T) {
	if graph := BuildGraph(topologyFixture()); graph.Analytics != nil {
		t.Error("analytics was built without being requested")
	}
}

func TestAnalyticsMatrix(t *testing.T) {
	graph := BuildGraph(withAnalytics(topologyFixture()))
	if graph.Analytics == nil {
		t.Fatal("analytics was requested but not built")
	}

	byID := map[string]AnalyticsRow{}
	for _, row := range graph.Analytics.PullTokens {
		byID[row.NodeID] = row
	}
	if len(byID) != 3 {
		t.Fatalf("pull rows = %d, want one per token", len(byID))
	}

	live := byID["pull-token:tok-live"]
	if len(live.Windows) != len(AnalyticsWindows) {
		t.Fatalf("windows = %d, want %d", len(live.Windows), len(AnalyticsWindows))
	}
	// Shortest window first, so the eye reads "now" before "all day".
	if live.Windows[0].Window != "15m" || live.Windows[2].Window != "24h" {
		t.Errorf("window order = %s..%s, want 15m..24h",
			live.Windows[0].Window, live.Windows[2].Window)
	}
	if live.Windows[0].Reads != 180 || live.Windows[2].Rows != 48000 {
		t.Errorf("live token counters = %+v", live.Windows)
	}

	// A token absent from a window read nothing in it. Zero is the true
	// answer; omitting the row would make a silent consumer disappear from the
	// table that exists to show silent consumers.
	unused := byID["pull-token:tok-unused"]
	for _, window := range unused.Windows {
		if window.Reads != 0 || window.Rows != 0 {
			t.Errorf("never-used token reported activity in %s: %+v", window.Window, window)
		}
	}
	if unused.Status != StatusWarning || unused.Detail != "never pulled" {
		t.Errorf("never-used row: status %q detail %q", unused.Status, unused.Detail)
	}

	// The row carries the node id so selecting a row can select its card.
	nodeByID(t, graph, live.NodeID)
}

// TestAnalyticsKeepsWindowedAndSinceBootApart is the honesty constraint: only
// the pull side has a queryable history. Delivery counters are cumulative
// process-local totals, and rendering one under a "last 15m" heading would be a
// lie told by the layout.
func TestAnalyticsKeepsWindowedAndSinceBootApart(t *testing.T) {
	graph := BuildGraph(withAnalytics(topologyFixture()))

	for _, row := range graph.Analytics.PullTokens {
		if row.Totals != nil {
			t.Errorf("pull row %s carries since-boot totals", row.NodeID)
		}
	}
	if len(graph.Analytics.Delivery) == 0 {
		t.Fatal("delivery table is empty; the endpoint and spool rows are missing")
	}
	for _, row := range graph.Analytics.Delivery {
		if row.Windows != nil {
			t.Errorf("delivery row %s claims windowed history it does not have", row.NodeID)
		}
		if row.Totals == nil {
			t.Errorf("delivery row %s has no totals", row.NodeID)
		}
	}

	// The ceiling travels in the response, so any API consumer inherits it.
	if !strings.Contains(graph.Analytics.RetentionNote, "24h") {
		t.Errorf("retention note does not state the ceiling: %q", graph.Analytics.RetentionNote)
	}
}

// --- the downstream delivery half: how a terminated message reaches a client ---

func TestDeliveryEndpointCard(t *testing.T) {
	graph := BuildGraph(topologyFixture())

	node := nodeByID(t, graph, "delivery:local-app")
	if node.Label != "app.partner.example" {
		t.Errorf("delivery label = %q, want the endpoint host", node.Label)
	}
	if node.Status != StatusOK {
		t.Errorf("a delivery endpoint with no failures reported %q", node.Status)
	}
	edgeByID(t, graph, "edge:termination:local-app->delivery:local-app")
}

// A termination connector with no delivery endpoint is the documented pull-only
// deployment. Drawing a push card for it would claim delivery that never happens.
func TestPullOnlyConnectorHasNoDeliveryCard(t *testing.T) {
	in := topologyFixture()
	in.Termination[0].Delivery.Endpoint = ""

	graph := BuildGraph(in)
	for _, node := range graph.Nodes {
		if node.Kind == KindDeliveryEndpoint {
			t.Fatalf("pull-only connector was given a delivery card: %s", node.ID)
		}
	}
	// The spool and its pull tokens must survive: that IS the delivery path here.
	nodeByID(t, graph, nodeSpool)
	nodeByID(t, graph, "pull-token:tok-live")
}

func TestDeliveryDeadLetterIsADegradedPath(t *testing.T) {
	in := topologyFixture()
	in.Metrics.TerminationDeliveries = map[stats.TerminationKey]uint64{
		{Connector: "local-app", Outcome: "dead_letter"}: 7,
	}

	graph := BuildGraph(in)
	if got := nodeByID(t, graph, "delivery:local-app").Status; got != StatusDown {
		t.Errorf("dead-lettered delivery reported %q, want %q", got, StatusDown)
	}
	edge := edgeByID(t, graph, "edge:termination:local-app->delivery:local-app")
	if !edge.Degraded {
		t.Error("dead-lettered delivery did not mark its edge degraded")
	}
}

// TestPullTokenStates is the answer to "is the customer actually collecting?" —
// the only delivery path with no connection to observe.
func TestPullTokenStates(t *testing.T) {
	graph := BuildGraph(topologyFixture())

	live := nodeByID(t, graph, "pull-token:tok-live")
	if live.Status != StatusOK {
		t.Errorf("a token that pulled 20s ago reported %q: %s", live.Status, live.Reason)
	}
	if live.Sublabel != "00:00:20 ago" {
		t.Errorf("live token sublabel = %q, want the raw age", live.Sublabel)
	}

	lost := nodeByID(t, graph, "pull-token:tok-lost")
	if lost.Status != StatusDown {
		t.Errorf("a token silent for 3h reported %q, want %q", lost.Status, StatusDown)
	}
	if !strings.Contains(lost.Reason, "no pull for") {
		t.Errorf("lost token reason = %q", lost.Reason)
	}

	unused := nodeByID(t, graph, "pull-token:tok-unused")
	if unused.Status != StatusWarning || unused.Reason != "issued but never used" {
		t.Errorf("never-used token: status %q reason %q", unused.Status, unused.Reason)
	}
	if unused.Sublabel != "never pulled" {
		t.Errorf("never-used token sublabel = %q", unused.Sublabel)
	}

	// Scope is the authorization, so it is also who receives whose traffic.
	edgeByID(t, graph, "edge:termination:local-app->pull-token:tok-live")

	stalled := problemByKind(t, graph, ProblemStalePull)
	if stalled.Count != 2 {
		t.Errorf("pull-stalled count = %d, want 2 (silent + never used); ids %v",
			stalled.Count, stalled.NodeIDs)
	}
}

func TestPullTokenStalenessBoundaries(t *testing.T) {
	base := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		silent time.Duration
		want   string
	}{
		{"just read", time.Second, StatusOK},
		{"one second inside the window", PullStaleAfter - time.Second, StatusOK},
		{"exactly stale", PullStaleAfter, StatusWarning},
		{"one second inside lost", PullLostAfter - time.Second, StatusWarning},
		{"exactly lost", PullLostAfter, StatusDown},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			token := messageConsumerResource{
				ID: "tok", LastReadAt: stamp(base.Add(-testCase.silent)),
			}
			got, _ := pullTokenStatus(token, base)
			if got != testCase.want {
				t.Errorf("silent %s = %q, want %q", testCase.silent, got, testCase.want)
			}
		})
	}
}

// A revoked token stopped pulling because somebody turned it off. Reporting it
// as a stalled consumer would send an operator chasing a fault they created.
func TestRevokedTokenIsIdleNotStalled(t *testing.T) {
	in := topologyFixture()
	in.PullTokens[1].Revoked = true

	graph := BuildGraph(in)
	node := nodeByID(t, graph, "pull-token:tok-lost")
	if node.Status != StatusIdle {
		t.Errorf("revoked token status = %q, want %q", node.Status, StatusIdle)
	}
	for _, id := range problemByKind(t, graph, ProblemStalePull).NodeIDs {
		if id == "pull-token:tok-lost" {
			t.Error("a revoked token was counted as a stalled pull")
		}
	}
	edge := edgeByID(t, graph, "edge:termination:local-app->pull-token:tok-lost")
	if !edge.Inactive || edge.Degraded {
		t.Errorf("revoked token edge: inactive=%v degraded=%v, want inactive only",
			edge.Inactive, edge.Degraded)
	}
}

// Refusals hide behind a recent read: a token pulling happily on one connector
// while being turned away from another looks healthy by age alone.
func TestDeniedReadsSurfaceEvenWhenPullingRecently(t *testing.T) {
	in := topologyFixture()
	in.PullTokens[0].Denied = counter(12)

	graph := BuildGraph(in)
	node := nodeByID(t, graph, "pull-token:tok-live")
	if node.Status != StatusWarning || !strings.Contains(node.Reason, "refused") {
		t.Errorf("token with refused reads: status %q reason %q", node.Status, node.Reason)
	}
}

func TestBuildGraphShape(t *testing.T) {
	graph := BuildGraph(topologyFixture())

	for _, id := range []string{
		nodeBinds, nodeAccounts, nodeLibrary, nodeMTRoutes, nodeCore,
		nodeQueues, nodeConnectors, nodeTermination, nodeMORoutes,
		nodeHTTPDests, nodeCarriers, nodeInfra, nodeFrontHTTP, nodeFrontSMPPs,
	} {
		nodeByID(t, graph, id)
	}

	routes := nodeByID(t, graph, nodeMTRoutes)
	if routes.ChildCount != 4 {
		t.Errorf("MT routes group child count = %d, want 4", routes.ChildCount)
	}
	if routes.Sublabel != "4 routes" {
		t.Errorf("MT routes sublabel = %q, want %q", routes.Sublabel, "4 routes")
	}

	// Children carry their group as Parent so the browser can collapse them.
	child := nodeByID(t, graph, "mt-route:100")
	if child.Parent != nodeMTRoutes {
		t.Errorf("route child parent = %q, want %q", child.Parent, nodeMTRoutes)
	}

	// Routes are ordered highest-first, the order the submit path evaluates.
	var seen []string
	for _, node := range graph.Nodes {
		if node.Kind == KindMTRoute {
			seen = append(seen, node.ID)
		}
	}
	want := []string{"mt-route:100", "mt-route:10", "mt-route:7", "mt-route:5"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("MT route order = %v, want %v", seen, want)
	}

	// Two connectors share no upstream host, so both carriers appear.
	if carriers := nodeByID(t, graph, nodeCarriers); carriers.ChildCount != 2 {
		t.Errorf("carrier count = %d, want 2 distinct endpoints", carriers.ChildCount)
	}

	// Config-owned entities must stay marked as such: the console refuses to
	// edit them, and the map has to agree or it invites an impossible action.
	if got := nodeByID(t, graph, "connector:promo-unbound").ManagedBy; got != "config" {
		t.Errorf("promo-unbound managed_by = %q, want config", got)
	}
}

func TestBuildGraphMarksBrokenPaths(t *testing.T) {
	graph := BuildGraph(topologyFixture())

	healthy := edgeByID(t, graph, "edge:mt-route:100->connector:smsc-primary")
	if healthy.Degraded {
		t.Error("route to a bound connector was marked degraded")
	}

	unbound := edgeByID(t, graph, "edge:mt-route:10->connector:promo-unbound")
	if !unbound.Degraded {
		t.Fatal("route to an unbound connector was not marked degraded")
	}
	if unbound.Reason != "no active bind" {
		t.Errorf("degraded reason = %q, want %q", unbound.Reason, "no active bind")
	}

	missing := edgeByID(t, graph, "edge:mt-route:5->connector:ghost-connector")
	if !missing.Degraded || !strings.Contains(missing.Reason, "does not exist") {
		t.Errorf("route to a non-existent connector: degraded=%v reason=%q", missing.Degraded, missing.Reason)
	}

	// A "term" route resolves against the termination connectors. Reading the
	// type as anything else sends it to the SMPP connector list, where it can
	// never be found, and every termination route reads as broken.
	termination := edgeByID(t, graph, "edge:mt-route:7->termination:local-app")
	if termination.Degraded {
		t.Errorf("route to a consuming termination connector was marked degraded: %q", termination.Reason)
	}

	// A dlr-url accepted while the MO thrower is off is the same class of
	// fault, reached through configuration rather than routing.
	thrower := edgeByID(t, graph, "edge:queues->"+nodeMOThrower)
	if !thrower.Degraded {
		t.Error("MO thrower is not running but its edge is not degraded")
	}
}

// TestMissingConnectorGetsACard pins the fix for a fault that had no target on
// the canvas: a route naming a connector nothing defines. The edge existed and
// was marked degraded, but its endpoint was not a node, so the renderer had
// nowhere to draw it — the one fault this map exists for, invisible.
func TestMissingConnectorGetsACard(t *testing.T) {
	graph := BuildGraph(topologyFixture())

	group := nodeByID(t, graph, nodeMissing)
	if group.Status != StatusDown {
		t.Errorf("missing-connector group status = %q, want %q", group.Status, StatusDown)
	}
	if group.ChildCount != 1 {
		t.Errorf("missing-connector child count = %d, want 1", group.ChildCount)
	}

	ghost := nodeByID(t, graph, "connector:ghost-connector")
	if ghost.Kind != KindMissingConnector {
		t.Errorf("ghost kind = %q, want %q — KindSMPPConnector would count it as unbound",
			ghost.Kind, KindMissingConnector)
	}
	if ghost.Parent != nodeMissing {
		t.Errorf("ghost parent = %q, want %q", ghost.Parent, nodeMissing)
	}

	// Every edge endpoint must be a real node, or the renderer drops the edge.
	present := map[string]bool{}
	for _, node := range graph.Nodes {
		present[node.ID] = true
	}
	for _, edge := range graph.Edges {
		if !present[edge.From] || !present[edge.To] {
			t.Errorf("edge %s references a node that does not exist (%s -> %s)",
				edge.ID, edge.From, edge.To)
		}
	}

	// A dangling reference is a broken path, never an unbound connector: it is
	// not a connector at all, and double-reporting teaches operators to
	// distrust the rail.
	unbound := problemByKind(t, graph, ProblemUnbound)
	for _, id := range unbound.NodeIDs {
		if id == "connector:ghost-connector" {
			t.Error("a non-existent connector was counted as unbound")
		}
	}
}

func TestNoMissingCardOnAHealthyGateway(t *testing.T) {
	in := topologyFixture()
	// Drop the route that names a connector nothing defines.
	kept := in.Routes[:0]
	for _, route := range in.Routes {
		if route.ConnectorID != "ghost-connector" {
			kept = append(kept, route)
		}
	}
	in.Routes = kept

	graph := BuildGraph(in)
	for _, node := range graph.Nodes {
		if node.ID == nodeMissing {
			t.Fatal("an empty \"Missing connectors\" card was rendered; it must appear only when something dangles")
		}
	}
}

func TestBuildGraphProblems(t *testing.T) {
	graph := BuildGraph(topologyFixture())

	broken := problemByKind(t, graph, ProblemBrokenPath)
	// Two degraded routes plus the stopped MO thrower edge.
	if broken.Count != 3 {
		t.Errorf("broken paths = %d, want 3; node ids %v", broken.Count, broken.NodeIDs)
	}

	unbound := problemByKind(t, graph, ProblemUnbound)
	if unbound.Count != 1 || unbound.NodeIDs[0] != "connector:promo-unbound" {
		t.Errorf("unbound = %d %v, want 1 [connector:promo-unbound]", unbound.Count, unbound.NodeIDs)
	}

	backlog := problemByKind(t, graph, ProblemQueueBacklog)
	if backlog.Count != 1 || backlog.NodeIDs[0] != "queue:submit.sm.promo-unbound" {
		t.Errorf("backlog = %d %v, want the 12480-deep queue", backlog.Count, backlog.NodeIDs)
	}
	// The healthy queue at 428 must NOT be reported: a threshold that fires on
	// normal depth trains operators to ignore the rail.
	if queue := nodeByID(t, graph, "queue:submit.sm.smsc-primary"); queue.Status != StatusOK {
		t.Errorf("a 428-deep queue reported status %q", queue.Status)
	}
}

// TestStructureHashIgnoresMetrics is the layout-stability guarantee. If this
// fails, nodes jump on every poll and the map stops being usable.
func TestStructureHashIgnoresMetrics(t *testing.T) {
	base := BuildGraph(topologyFixture()).StructureHash

	moved := topologyFixture()
	registry := stats.NewPrometheusRegistry()
	for i := 0; i < 500; i++ {
		registry.RecordSubmit("smsc-primary", stats.SubmitSuccess, "ESME_ROK", time.Millisecond)
	}
	registry.SetQueueDepth("submit.sm.smsc-primary", 99999)
	registry.SetQueueDepth("submit.sm.promo-unbound", 12480)
	registry.SetConnectorState("smsc-primary", "BOUND")
	registry.SetGatewayHealth("ok")
	moved.Metrics = registry.Snapshot()
	moved.ObservedAt = moved.ObservedAt.Add(time.Hour)
	moved.HTTPCounters = map[string]int64{"request_count": 99999}

	if got := BuildGraph(moved).StructureHash; got != base {
		t.Errorf("structure hash changed on a metrics-only update: %s -> %s", base, got)
	}
}

func TestStructureHashChangesWhenShapeChanges(t *testing.T) {
	base := BuildGraph(topologyFixture()).StructureHash

	added := topologyFixture()
	added.Routes = append(added.Routes, routeResource{
		ID: 200, ManagedBy: "admin",
		RouteConfig: outbound.RouteConfig{Order: 200, Rate: 0.05, ConnectorID: "smsc-primary"},
	})
	if got := BuildGraph(added).StructureHash; got == base {
		t.Error("adding a route did not change the structure hash; layout would never refresh")
	}

	removed := topologyFixture()
	removed.Connectors = removed.Connectors[:1]
	if got := BuildGraph(removed).StructureHash; got == base {
		t.Error("removing a connector did not change the structure hash")
	}
}

func TestBuildGraphIsDeterministic(t *testing.T) {
	first, err := json.Marshal(BuildGraph(topologyFixture()))
	if err != nil {
		t.Fatal(err)
	}
	// Reordered inputs, as the admin services and config lists legitimately
	// return them, must produce a byte-identical document.
	shuffled := topologyFixture()
	shuffled.Connectors[0], shuffled.Connectors[1] = shuffled.Connectors[1], shuffled.Connectors[0]
	shuffled.Routes[0], shuffled.Routes[2] = shuffled.Routes[2], shuffled.Routes[0]
	second, err := json.Marshal(BuildGraph(shuffled))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("input order changed the document; the browser would relayout for no reason")
	}
}

// TestTopologyRedactsSecrets is the reason Detail fields are built by hand.
// The document aggregates every entity in the deployment, so a struct embedded
// wholesale would ship credentials to the browser.
func TestTopologyRedactsSecrets(t *testing.T) {
	payload, err := json.Marshal(BuildGraph(topologyFixture()))
	if err != nil {
		t.Fatal(err)
	}
	document := string(payload)

	for name, secret := range map[string]string{
		"connector bind password": secretConnectorPassword,
		"interceptor script body": secretInterceptorCode,
		"SMPPs bind password":     secretSMPPsPassword,
		"user password":           secretUserPassword,
	} {
		if strings.Contains(document, secret) {
			t.Errorf("topology document leaked the %s", name)
		}
	}
}

func TestBuildGraphWithoutMetricsSaysSo(t *testing.T) {
	in := topologyFixture()
	in.Metrics = stats.Snapshot{}
	in.MetricsWired = false

	graph := BuildGraph(in)
	if !graph.MetricsStale {
		t.Error("a graph built without a metrics registry must report metrics_stale")
	}
	// Zero counters would read as "idle"; unknown is the honest state.
	if got := nodeByID(t, graph, nodeQueues).Status; got != StatusUnknown {
		t.Errorf("queues status without metrics = %q, want %q", got, StatusUnknown)
	}
}

func TestBuildGraphEmptyDeployment(t *testing.T) {
	graph := BuildGraph(GraphInputs{})

	// A gateway with nothing configured still renders its skeleton rather than
	// an empty canvas, so the page is never a blank rectangle.
	nodeByID(t, graph, nodeMTRoutes)
	nodeByID(t, graph, nodeCore)
	nodeByID(t, graph, nodeQueues)

	if graph.StructureHash == "" {
		t.Error("empty deployment produced no structure hash")
	}
	for _, problem := range graph.Problems {
		if problem.Count != 0 {
			t.Errorf("empty deployment reported %d %s problems", problem.Count, problem.Kind)
		}
	}
}
