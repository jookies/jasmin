package adminweb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/routingfilter"
	"github.com/pumpitspace/synevyr/internal/core/routingtable"
	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/core/termination"
)

// This file builds the console's topology document: the running gateway as a
// graph, generated from live state rather than drawn by hand.
//
// The rule that makes it maintainable is that this file owns the *model* and
// the browser owns the pixels. Nothing here knows about a rendering library,
// and BuildGraph is a pure function over a plain inputs struct — no HTTP, no
// services, no globals — so the whole shape of the map is testable from a
// fixture.
//
// Two properties are load-bearing:
//
//   - StructureHash covers node and edge identity ONLY, never a metric. The
//     browser re-runs graph layout when it changes and repaints in place when it
//     does not. Include a counter here and every poll would relayout, nodes
//     would jump under the cursor every few seconds, and the map would be
//     unusable.
//
//   - Detail fields are constructed explicitly, one at a time. This document
//     aggregates the entire configuration into a single response, so it must
//     never embed a resource struct wholesale: that is how a password hash, an
//     interceptor script body or a consumer token would reach the browser. See
//     TestTopologyRedactsSecrets.

// Graph columns. These are layout rank hints, not a layout: the browser seeds
// its layered layout from them so the six stages stay in reading order even
// when the underlying algorithm would rank a node differently.
const (
	ColumnIngress  = "ingress"  // who is talking to us
	ColumnPolicy   = "policy"   // what we decide about their traffic
	ColumnCore     = "core"     // the gateway process itself
	ColumnQueues   = "queues"   // the broker between decision and delivery
	ColumnEgress   = "egress"   // what we hand traffic to
	ColumnCarriers = "carriers" // where it ends up
	ColumnDelivery = "delivery" // what our own customers receive
	ColumnInfra    = "infra"    // dependencies, parked under the core
)

// Lanes separate the two directions of travel.
//
// Columns alone were not enough. With everything in one band the MT chain and
// the MO/DLR return chain interleaved, nine cards piled into the egress column
// while three columns held one card each, and the eye could not follow either
// direction. The MT lane now reads left to right along the top; the return lane
// reads right to left underneath it; and infrastructure sits below both, under
// the core it belongs to, instead of in a far-right column reached by an edge
// crossing the whole diagram.
const (
	LaneMT     = "mt"
	LaneReturn = "return"
	LaneInfra  = "infra"
)

// Node kinds. The browser picks a card component per kind, so these are a
// contract with web/src/topology/nodes.
const (
	KindGroup         = "group" // an aggregate card: "MT routes · 142 routes"
	KindSMPPBind      = "smpp_bind"
	KindUser          = "user"
	KindGroupAccount  = "account_group"
	KindFilter        = "filter"
	KindInterceptor   = "interceptor"
	KindMTRoute       = "mt_route"
	KindMORoute       = "mo_route"
	KindCore          = "core"
	KindQueue         = "queue"
	KindSMPPConnector = "smpp_connector"
	// KindMissingConnector is a connector a route names and nothing defines.
	// It is deliberately NOT KindSMPPConnector: it must not be counted as an
	// unbound connector, because it is not a connector at all, and the broken
	// path into it is already reported.
	KindMissingConnector = "missing_connector"
	KindTermination      = "termination_connector"
	KindDeliveryEndpoint = "delivery_endpoint"
	KindSpool            = "message_spool"
	KindPullToken        = "pull_token"
	KindHTTPDestination  = "http_destination"
	KindThrower          = "thrower"
	KindCarrier          = "carrier"
	KindInfraDependency  = "infra"
	KindFrontDoorHTTP    = "front_door_http"
	KindFrontDoorSMPPs   = "front_door_smpps"
)

// Node and edge status values, shared with the legend.
const (
	StatusOK      = "ok"
	StatusWarning = "warning"
	StatusDown    = "down"
	StatusIdle    = "idle"
	StatusUnknown = "unknown"
)

// Edge kinds drive stroke treatment: MT is the solid outbound lane, MO and DLR
// are the return lanes, and config edges carry no traffic at all (an
// authorization or a reference, drawn thin and grey).
const (
	EdgeMT     = "mt"
	EdgeMO     = "mo"
	EdgeDLR    = "dlr"
	EdgeConfig = "config"
)

// QueueBacklogThreshold is the ready-message depth at which a queue is reported
// as a problem.
//
// It is a fixed number rather than a rate because the map's job here is to
// catch the standing case — a queue accumulating behind a connector that is not
// draining it — and any threshold low enough to catch that early is high enough
// to sit above a healthy burst. Depth is also only observed every fifteen
// seconds (gateway.DefaultQueueDepthInterval), so a rate computed from
// consecutive polls would mostly measure the observation cadence.
const QueueBacklogThreshold = 1000

// FlappingDisconnects is how many drops mark a connector as unstable even while
// it is bound at this instant.
//
// Three, because one is a restart and two can be a network blip, but a link
// that has dropped three times is not going to stay up — and every drop loses
// the in-flight window. The counter is cumulative since process start, so this
// is deliberately a low bar on a long-lived gateway rather than a rate.
const FlappingDisconnects = 3

// PullStaleAfter and PullLostAfter are how long a customer's pull credential may
// stay silent before the map says so.
//
// A pull token is the only delivery path with no connection to observe: the
// customer polls, so "healthy" is indistinguishable from "stopped" except by
// how long it has been. Fifteen minutes is short enough to catch a stuck
// consumer within a coffee break and long enough not to fire on a partner that
// batches its polling; an hour of silence is treated as an outage rather than a
// slow poller. Both are wall-clock against the last successful read, and the
// card shows the raw age in every state — the thresholds only decide the
// colour and whether it joins the problem rail.
const (
	PullStaleAfter = 15 * time.Minute
	PullLostAfter  = time.Hour
)

// Ref is a deep link into the console page that owns an entity, so the
// inspector can offer "Open connector" without the browser re-deriving which
// page that is.
type Ref struct {
	// Resource is the console route segment ("connectors", "routes", "users").
	Resource string `json:"resource"`
	// ID is the entity identity within that page.
	ID string `json:"id"`
}

// DetailField is one row in the inspector. Items carries an ordered list (a
// route's filter chain, a connector pool's candidates) where a single Value
// would flatten information the operator needs to read in order.
type DetailField struct {
	Label string   `json:"label"`
	Value string   `json:"value,omitempty"`
	Items []string `json:"items,omitempty"`
}

// Node is one card on the canvas. A node with a Parent is a child inside its
// group's card; the browser shows the first few and collapses the rest.
type Node struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Sublabel string `json:"sublabel,omitempty"`
	Column   string `json:"column"`
	// Lane is the direction of travel this node belongs to; empty means MT.
	Lane   string `json:"lane,omitempty"`
	Status string `json:"status"`
	// Parent is the enclosing group node's id, empty for a top-level card.
	Parent string `json:"parent,omitempty"`
	// ChildCount is set on group nodes so a collapsed card can say how many
	// entities it stands for without the browser counting children.
	ChildCount int `json:"child_count,omitempty"`
	// ManagedBy is "admin" or "config"; config-owned entities are read-only
	// everywhere in the console and the map marks them the same way.
	ManagedBy string `json:"managed_by,omitempty"`
	// Reason explains a non-ok Status in operator language.
	Reason  string             `json:"reason,omitempty"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
	Detail  []DetailField      `json:"detail,omitempty"`
	Ref     *Ref               `json:"ref,omitempty"`
}

// Edge is one connection. From/To may name child nodes; the browser resolves a
// child endpoint to its parent card while that group is collapsed.
type Edge struct {
	ID    string `json:"id"`
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	// Degraded marks a fault worth reporting in its own right: this path is
	// configured to carry traffic and cannot. Degraded edges are what the
	// Broken paths rail counts.
	Degraded bool `json:"degraded,omitempty"`
	// Inactive marks an edge carrying nothing as a *consequence* of a fault
	// reported elsewhere — the link from an unbound connector to its carrier,
	// for instance. It is drawn dimmed, and deliberately not counted: an
	// operator seeing "3 broken paths" for one dead connector would learn to
	// distrust the number.
	Inactive bool               `json:"inactive,omitempty"`
	Reason   string             `json:"reason,omitempty"`
	Metrics  map[string]float64 `json:"metrics,omitempty"`
}

// Problem is one entry in the console's problem rail. It is the map's entry
// point: an operator opening the page should be told what is wrong before
// having to find it on the canvas.
type Problem struct {
	Kind    string   `json:"kind"`
	Label   string   `json:"label"`
	Count   int      `json:"count"`
	NodeIDs []string `json:"node_ids"`
}

// Problem kinds.
const (
	ProblemBrokenPath   = "broken_path"
	ProblemUnbound      = "unbound"
	ProblemQueueBacklog = "queue_backlog"
	ProblemStalePull    = "stale_pull"
)

// --- delivery analytics -----------------------------------------------------

// AnalyticsWindows are the lookback windows the matrix reports, shortest first.
//
// They stop at 24 hours because that is a hard ceiling, not a preference: the
// pull audit table is pruned together with the message spool, so nothing older
// is answerable from this process at all. A longer trend has to come from a
// scrape of /metrics/prometheus, and the panel says so rather than showing an
// empty column that looks like "no traffic".
var AnalyticsWindows = []struct {
	Name string
	Span time.Duration
}{
	{"15m", 15 * time.Minute},
	{"1h", time.Hour},
	{"24h", 24 * time.Hour},
}

// ActivityWindow is one credential's read counters over one bounded window.
type ActivityWindow struct {
	Window string `json:"window"`
	Reads  int64  `json:"reads"`
	Rows   int64  `json:"rows"`
	Denied int64  `json:"denied"`
}

// AnalyticsRow is one line of the matrix, carrying the node id so selecting a
// row can select the card it describes.
type AnalyticsRow struct {
	NodeID string `json:"node_id"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Status string `json:"status"`
	// Detail is the human answer to "when did this last do anything".
	Detail string `json:"detail,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Windows are populated for pull credentials only — they are the one item
	// with a queryable history. Delivery counters live in Totals instead.
	Windows []ActivityWindow `json:"windows,omitempty"`
	// Totals are cumulative since process start (KNOWN_QUIRKS Q-011). They are
	// deliberately a separate field from Windows so the console cannot render a
	// since-boot number under a "last 15m" heading.
	Totals map[string]int64 `json:"totals,omitempty"`
}

// Analytics is the matrix panel's payload. It is present only when the caller
// asks for it: each window costs an aggregate over the audit table, and the
// map itself does not need them.
type Analytics struct {
	// RetentionNote states the ceiling in the response rather than only in the
	// UI, so any consumer of this API inherits the caveat.
	RetentionNote string         `json:"retention_note"`
	PullTokens    []AnalyticsRow `json:"pull_tokens"`
	Delivery      []AnalyticsRow `json:"delivery"`
}

// PullWindowActivity is one lookback window's counters, keyed by credential id.
type PullWindowActivity struct {
	Window  string
	ByToken map[string]ActivityWindow
}

// Graph is the whole document served by GET /api/topology.
type Graph struct {
	Nodes    []Node    `json:"nodes"`
	Edges    []Edge    `json:"edges"`
	Problems []Problem `json:"problems"`

	// StructureHash changes only when the shape changes. See the note at the
	// top of this file: the browser's layout stability depends on it.
	StructureHash string `json:"structure_hash"`

	ObservedAt string `json:"observed_at"`
	// CountersSince is process start. Every counter in this document is
	// cumulative from that instant and resets on restart (KNOWN_QUIRKS Q-011),
	// so the console labels them rather than implying stored history.
	CountersSince string `json:"counters_since,omitempty"`
	// Analytics is the matrix panel, present only when it was requested.
	Analytics *Analytics `json:"analytics,omitempty"`
	// MetricsStale reports that no metrics registry was wired, so the document
	// carries structure without numbers. Better than zeros, which read as idle.
	MetricsStale bool `json:"metrics_stale,omitempty"`
}

// GraphInputs is everything BuildGraph reads. The handler gathers it; the
// builder stays pure so a fixture can produce an exact expected graph.
type GraphInputs struct {
	Connectors       []connectorResource
	Termination      []terminationConnectorResource
	Routes           []routeResource
	MORoutes         []moRouteResource
	Users            []userResource
	Groups           []groupResource
	SMPPsUsers       []smppsUserResource
	Filters          []filterResource
	HTTPDestinations []httpConnectorResource
	Interceptors     []interceptorResource
	// PullTokens are the scoped credentials a customer uses to fetch decoded
	// messages from the spool, with their read activity attached.
	PullTokens []messageConsumerResource
	// PullWindows are the per-window read counters behind the analytics matrix.
	// Empty means the caller did not ask for analytics, which is the default:
	// each window is an aggregate over the audit table.
	PullWindows []PullWindowActivity
	// SpoolRetention is the window decoded content is kept for, reported on the
	// spool card so an operator can see how long a stalled consumer has before
	// its backlog is pruned out from under it.
	SpoolRetention time.Duration

	Metrics       stats.Snapshot
	MetricsWired  bool
	HTTPCounters  map[string]int64
	SMPPsCounters map[string]int64
	// SMPPcCounters are the per-connector legacy counters, keyed by connector id.
	// They carry what the production registry does not: connect/disconnect
	// churn, enquire_link activity and throttling refusals. A connector that is
	// bound right now but has reconnected forty times is a different animal from
	// one that bound once and stayed, and only these numbers can tell them apart.
	SMPPcCounters map[string]map[string]int64

	Health       map[string]string
	HealthStatus string

	Ingress    IngressSnapshot
	HasIngress bool

	StartedAt  time.Time
	ObservedAt time.Time
}

// graphBuilder accumulates nodes and edges so the section functions below read
// as a description of the topology rather than as slice bookkeeping.
type graphBuilder struct {
	nodes []Node
	edges []Edge
}

func (b *graphBuilder) add(node Node) string {
	b.nodes = append(b.nodes, node)
	return node.ID
}

func (b *graphBuilder) link(id, from, to, kind string) *Edge {
	b.edges = append(b.edges, Edge{ID: id, From: from, To: to, Kind: kind})
	return &b.edges[len(b.edges)-1]
}

// Top-level node ids. They are stable strings because the browser stores
// per-node UI state (expanded, selected) against them across polls.
const (
	nodeBinds       = "group:smpp-binds"
	nodeAccounts    = "group:accounts"
	nodeLibrary     = "group:policy-library"
	nodeMTRoutes    = "group:mt-routes"
	nodeCore        = "core"
	nodeQueues      = "group:queues"
	nodeConnectors  = "group:smpp-connectors"
	nodeTermination = "group:termination"
	nodeMORoutes    = "group:mo-routes"
	nodeHTTPDests   = "group:http-destinations"
	nodeDLRThrower  = "thrower:dlr"
	nodeMOThrower   = "thrower:mo"
	nodeCarriers    = "group:carriers"
	nodeMissing     = "group:missing-connectors"
	nodeSpool       = "message-spool"
	nodePullTokens  = "group:pull-tokens"
	nodeInfra       = "group:infra"
	nodeFrontHTTP   = "front-door:http"
	nodeFrontSMPPs  = "front-door:smpps"
)

// BuildGraph turns a snapshot of the running gateway into the topology
// document. It never fails: a missing input degrades that part of the map to
// "unknown" rather than refusing the whole picture, because an operator opening
// this page during an incident is exactly when a partial answer beats none.
func BuildGraph(in GraphInputs) Graph {
	b := &graphBuilder{}

	buildIngress(b, in)
	buildPolicy(b, in)
	buildCore(b, in)
	buildQueues(b, in)
	buildEgress(b, in)
	buildDownstream(b, in)
	buildCarriers(b, in)
	buildInfrastructure(b, in)
	buildRouteEdges(b, in)

	graph := Graph{
		Nodes:         b.nodes,
		Edges:         b.edges,
		Problems:      buildProblems(b.nodes, b.edges, in),
		ObservedAt:    formatTime(in.ObservedAt),
		CountersSince: formatTime(in.StartedAt),
		MetricsStale:  !in.MetricsWired,
	}
	graph.StructureHash = structureHash(graph.Nodes, graph.Edges)
	// Analytics is derived from the nodes just built, so the matrix and the
	// canvas always describe the same instant — the reason it rides on this
	// document instead of a second endpoint with its own poll.
	graph.Analytics = buildAnalytics(graph.Nodes, in)
	return graph
}

// ---------------------------------------------------------------- ingress ---

func buildIngress(b *graphBuilder, in GraphInputs) {
	// Front doors first: they are the only nodes an outsider can address, so
	// the map is read from them.
	if in.HasIngress && in.Ingress.HTTPBindAddress != "" {
		scheme := "http"
		if in.Ingress.HTTPTLS {
			scheme = "https"
		}
		b.add(Node{
			ID: nodeFrontHTTP, Kind: KindFrontDoorHTTP, Column: ColumnIngress,
			Label:    "HTTP front door",
			Sublabel: in.Ingress.HTTPBindAddress,
			Status:   StatusOK,
			Metrics: map[string]float64{
				"requests":     float64(in.HTTPCounters["request_count"]),
				"success":      float64(in.HTTPCounters["success_count"]),
				"auth_errors":  float64(in.HTTPCounters["auth_error_count"]),
				"route_errors": float64(in.HTTPCounters["route_error_count"]),
			},
			Detail: []DetailField{
				{Label: "Listen address", Value: in.Ingress.HTTPBindAddress},
				{Label: "Scheme", Value: scheme},
				{Label: "Endpoints", Items: []string{"/send", "/balance", "/rate", "/ping", "/metrics"}},
			},
		})
	}
	if in.HasIngress && in.Ingress.SMPPSBindAddress != "" {
		b.add(Node{
			ID: nodeFrontSMPPs, Kind: KindFrontDoorSMPPs, Column: ColumnIngress,
			Label:    "SMPP server",
			Sublabel: in.Ingress.SMPPSBindAddress,
			Status:   StatusOK,
			Metrics: map[string]float64{
				"connected": float64(in.SMPPsCounters["connected_count"]),
				"bound_trx": float64(in.SMPPsCounters["bound_trx_count"]),
				"bound_rx":  float64(in.SMPPsCounters["bound_rx_count"]),
				"bound_tx":  float64(in.SMPPsCounters["bound_tx_count"]),
			},
			Detail: []DetailField{
				{Label: "Bind address", Value: in.Ingress.SMPPSBindAddress},
				{Label: "TLS", Value: yesNo(in.Ingress.SMPPSTLS)},
				{Label: "Enquire-link timeout", Value: seconds(in.Ingress.SMPPSEnquireLinkTimeout)},
			},
		})
	}

	binds := sortedSMPPsUsers(in.SMPPsUsers)
	b.add(Node{
		ID: nodeBinds, Kind: KindGroup, Column: ColumnIngress,
		Label:      "SMPP binds",
		Sublabel:   countLabel(len(binds), "client", "clients"),
		Status:     StatusOK,
		ChildCount: len(binds),
		Ref:        &Ref{Resource: "smpps-users"},
	})
	for _, user := range binds {
		status, reason := StatusOK, ""
		if user.Disabled || user.GroupDisabled {
			status, reason = StatusIdle, "account disabled"
		}
		b.add(Node{
			ID: "bind:" + user.SystemID, Kind: KindSMPPBind, Column: ColumnIngress,
			Label: user.SystemID, Parent: nodeBinds, Status: status, Reason: reason,
			ManagedBy: user.ManagedBy,
			Ref:       &Ref{Resource: "smpps-users", ID: user.SystemID},
			Detail: []DetailField{
				{Label: "System ID", Value: user.SystemID},
				{Label: "Max bindings", Value: intPointer(user.MaxBindings, "unlimited")},
				{Label: "IP whitelist", Value: orDefault(user.IPWhitelist, "any")},
				{Label: "Managed by", Value: user.ManagedBy},
			},
		})
	}

	users := sortedUsers(in.Users)
	groups := sortedGroups(in.Groups)
	b.add(Node{
		ID: nodeAccounts, Kind: KindGroup, Column: ColumnIngress,
		Label:      "Users & groups",
		Sublabel:   fmt.Sprintf("%s · %s", countLabel(len(users), "user", "users"), countLabel(len(groups), "group", "groups")),
		Status:     StatusOK,
		ChildCount: len(users) + len(groups),
		Ref:        &Ref{Resource: "users"},
	})
	for _, group := range groups {
		status, reason := StatusOK, ""
		if group.Disabled {
			status, reason = StatusIdle, "group disabled"
		}
		b.add(Node{
			ID: "account-group:" + group.GID, Kind: KindGroupAccount, Column: ColumnIngress,
			Label: group.GID, Parent: nodeAccounts, Status: status, Reason: reason,
			ManagedBy: group.ManagedBy,
			Ref:       &Ref{Resource: "groups", ID: group.GID},
			Detail: []DetailField{
				{Label: "Group", Value: group.GID},
				{Label: "Managed by", Value: group.ManagedBy},
			},
		})
	}
	for _, user := range users {
		status, reason := StatusOK, ""
		if user.Disabled {
			status, reason = StatusIdle, "user disabled"
		}
		rejections := in.Metrics.ThroughputRejections[user.Username]
		if rejections > 0 && status == StatusOK {
			status, reason = StatusWarning, "throughput limit rejecting submits"
		}
		detail := []DetailField{
			{Label: "Username", Value: user.Username},
			{Label: "Group", Value: orDefault(user.GroupID, "none")},
			{Label: "Managed by", Value: user.ManagedBy},
		}
		if user.LiveBalance != nil {
			detail = append(detail, DetailField{Label: "Balance left", Value: formatFloat(*user.LiveBalance)})
		}
		if user.LiveSubmitSMCount != nil {
			detail = append(detail, DetailField{Label: "Messages left", Value: strconv.Itoa(*user.LiveSubmitSMCount)})
		}
		metrics := map[string]float64{"throughput_rejections": float64(rejections)}
		for key, charged := range in.Metrics.BillingCharges {
			if key.User == user.Username {
				metrics["charged_"+strings.ToLower(key.Currency)] = charged
			}
		}
		b.add(Node{
			ID: "user:" + user.Username, Kind: KindUser, Column: ColumnIngress,
			Label: user.Username, Parent: nodeAccounts, Status: status, Reason: reason,
			ManagedBy: user.ManagedBy, Metrics: metrics, Detail: detail,
			Ref: &Ref{Resource: "users", ID: user.Username},
		})
	}

	filters := sortedFilters(in.Filters)
	interceptors := in.Interceptors
	b.add(Node{
		ID: nodeLibrary, Kind: KindGroup, Column: ColumnIngress,
		Label:      "Filters & interceptors",
		Sublabel:   fmt.Sprintf("%s · %s", countLabel(len(filters), "filter", "filters"), countLabel(len(interceptors), "interceptor", "interceptors")),
		Status:     StatusOK,
		ChildCount: len(filters) + len(interceptors),
		Ref:        &Ref{Resource: "filters"},
	})
	for _, filter := range filters {
		b.add(Node{
			ID: "filter:" + filter.FID, Kind: KindFilter, Column: ColumnIngress,
			Label: filter.FID, Parent: nodeLibrary, Status: StatusOK,
			Sublabel: filter.Type,
			Ref:      &Ref{Resource: "filters", ID: filter.FID},
			Detail: []DetailField{
				{Label: "Filter", Value: filter.FID},
				{Label: "Type", Value: filter.Type},
			},
		})
	}
	for _, interceptor := range interceptors {
		// The script body is deliberately absent: it is arbitrary code running
		// on this host, and the map is a picture of the topology, not a code
		// viewer. The interceptors page remains the only place it is readable.
		errors := in.Metrics.InterceptorErrors[interceptor.Direction]
		status, reason := StatusOK, ""
		if errors > 0 {
			status, reason = StatusWarning, "interceptor execution errors"
		}
		b.add(Node{
			ID: "interceptor:" + interceptor.ID, Kind: KindInterceptor, Column: ColumnIngress,
			Label: interceptor.ID, Parent: nodeLibrary, Status: status, Reason: reason,
			Sublabel: strings.ToUpper(interceptor.Direction),
			Metrics:  map[string]float64{"errors": float64(errors)},
			Ref:      &Ref{Resource: "interceptors", ID: interceptor.ID},
			Detail: []DetailField{
				{Label: "Direction", Value: strings.ToUpper(interceptor.Direction)},
				{Label: "Order", Value: strconv.Itoa(interceptor.Order)},
			},
		})
	}
}

// ----------------------------------------------------------------- policy ---

func buildPolicy(b *graphBuilder, in GraphInputs) {
	routes := sortedRoutes(in.Routes)
	b.add(Node{
		ID: nodeMTRoutes, Kind: KindGroup, Column: ColumnPolicy,
		Label:      "MT routes",
		Sublabel:   countLabel(len(routes), "route", "routes"),
		Status:     StatusOK,
		ChildCount: len(routes),
		Ref:        &Ref{Resource: "routes"},
	})
	for _, route := range routes {
		candidates := route.ConnectorCandidates()
		filters := make([]string, 0, len(route.Filters))
		for _, filter := range route.Filters {
			filters = append(filters, describeFilter(filter.Type, filter.Pattern, filter.Value))
		}
		// The routing table keys a route by direction and order, and that is the
		// same identity durable records use, so the counter survives an operator
		// editing the route in place.
		routeKey := string(routingfilter.MT) + ":" + strconv.Itoa(route.Order)
		matches := in.Metrics.MatchesForRoute(routeKey)
		lastMatch, everMatched := in.Metrics.RouteLastMatch[routeKey]

		detail := []DetailField{
			{Label: "Order", Value: strconv.Itoa(route.Order)},
			{Label: "Rate", Value: formatFloat(route.Rate)},
			{Label: "Matches", Value: strconv.FormatUint(matches, 10)},
			{Label: "Last match", Value: matchAge(lastMatch, everMatched, in.ObservedAt)},
			{Label: "Connector type", Value: connectorTypeLabel(route.ConnectorType)},
			{Label: "Destination connectors", Items: candidates},
			{Label: "Managed by", Value: route.ManagedBy},
		}
		if len(filters) > 0 {
			detail = append(detail, DetailField{Label: "Filter chain", Items: filters})
		}
		if route.Default {
			detail = append(detail, DetailField{Label: "Default route", Value: "yes"})
		}
		// "Never matched" is a finding, not an absence: it is a rule that has
		// never fired, which on a busy gateway usually means its filter is
		// wrong or a higher-order route shadows it. It is left at ok rather
		// than warning, because a freshly added route is legitimately unused.
		routeSublabel := strings.Join(candidates, ", ")
		if !everMatched && !route.Default {
			routeSublabel = routeSublabel + " · never matched"
		}
		b.add(Node{
			ID: mtRouteNodeID(route.Order), Kind: KindMTRoute, Column: ColumnPolicy,
			Label:     routeLabel(route.Order, route.Default),
			Sublabel:  routeSublabel,
			Parent:    nodeMTRoutes,
			Status:    StatusOK,
			ManagedBy: route.ManagedBy,
			Metrics: map[string]float64{
				"rate": route.Rate, "order": float64(route.Order),
				// The console differences this across polls into a match rate,
				// which is what answers "is this rule actually carrying
				// traffic" — a total since boot cannot.
				"matches": float64(matches),
			},
			Detail: detail,
			Ref:    &Ref{Resource: "routes", ID: strconv.Itoa(route.Order)},
		})
	}
}

// ------------------------------------------------------------------- core ---

func buildCore(b *graphBuilder, in GraphInputs) {
	status := StatusUnknown
	switch in.HealthStatus {
	case "ok":
		status = StatusOK
	case "degraded":
		status = StatusWarning
	case "":
	default:
		status = StatusDown
	}

	uptime := 0.0
	if !in.StartedAt.IsZero() && !in.ObservedAt.IsZero() {
		uptime = in.ObservedAt.Sub(in.StartedAt).Seconds()
	}

	var submitted, delivered uint64
	for key, value := range in.Metrics.Submits {
		if key.Outcome == stats.SubmitSuccess {
			submitted += value
		}
	}
	for key, value := range in.Metrics.MOs {
		if key.Outcome == stats.MORouted {
			delivered += value
		}
	}
	var receipts uint64
	for _, value := range in.Metrics.DLRs {
		receipts += value
	}

	b.add(Node{
		ID: nodeCore, Kind: KindCore, Column: ColumnCore,
		Label:    "Gateway core",
		Sublabel: orDefault(in.HealthStatus, "unknown"),
		Status:   status,
		Metrics: map[string]float64{
			"submit_success": float64(submitted),
			"mo_routed":      float64(delivered),
			"dlr_total":      float64(receipts),
			"uptime_seconds": uptime,
			"sessions":       float64(in.SMPPsCounters["connected_count"]),
		},
		Detail: []DetailField{
			{Label: "Health", Value: orDefault(in.HealthStatus, "unknown")},
			{Label: "Active SMPP sessions", Value: strconv.FormatInt(in.SMPPsCounters["connected_count"], 10)},
			{Label: "Uptime", Value: duration(uptime)},
		},
	})

	// Ingress feeds the core; the front doors are what authorize and admit.
	for _, from := range []string{nodeFrontHTTP, nodeFrontSMPPs} {
		if hasNode(b.nodes, from) {
			b.link("edge:"+from+"->core", from, nodeCore, EdgeMT)
		}
	}
	b.link("edge:accounts->routes", nodeAccounts, nodeMTRoutes, EdgeConfig)
	b.link("edge:binds->routes", nodeBinds, nodeMTRoutes, EdgeConfig)
	b.link("edge:library->routes", nodeLibrary, nodeMTRoutes, EdgeConfig)
	b.link("edge:routes->core", nodeMTRoutes, nodeCore, EdgeMT)
	b.link("edge:core->queues", nodeCore, nodeQueues, EdgeMT)
}

// ----------------------------------------------------------------- queues ---

func buildQueues(b *graphBuilder, in GraphInputs) {
	queues := make([]string, 0, len(in.Metrics.QueueDepths))
	for name := range in.Metrics.QueueDepths {
		queues = append(queues, name)
	}
	sort.Strings(queues)

	var total int64
	worst := StatusOK
	for _, name := range queues {
		depth := in.Metrics.QueueDepths[name]
		total += depth
		if depth >= QueueBacklogThreshold {
			worst = StatusWarning
		}
	}
	if !in.MetricsWired {
		worst = StatusUnknown
	}

	b.add(Node{
		ID: nodeQueues, Kind: KindGroup, Column: ColumnQueues,
		Label:      "Broker queues",
		Sublabel:   countLabel(len(queues), "queue", "queues"),
		Status:     worst,
		ChildCount: len(queues),
		Metrics:    map[string]float64{"ready_total": float64(total)},
	})

	for _, name := range queues {
		depth := in.Metrics.QueueDepths[name]
		status, reason := StatusOK, ""
		if depth >= QueueBacklogThreshold {
			status = StatusWarning
			reason = fmt.Sprintf("%d messages ready and not draining", depth)
		}
		b.add(Node{
			ID: queueNodeID(name), Kind: KindQueue, Column: ColumnQueues,
			Label: name, Parent: nodeQueues, Status: status, Reason: reason,
			Sublabel: fmt.Sprintf("ready %d", depth),
			Metrics:  map[string]float64{"depth": float64(depth)},
			Detail: []DetailField{
				{Label: "Queue", Value: name},
				{Label: "Ready messages", Value: strconv.FormatInt(depth, 10)},
				// Worth stating on every queue: the number is a poll, and it
				// counts ready messages only. A connector consuming without
				// acking shows a small depth, not a growing one.
				{Label: "Observation", Value: "polled every 15s; unacknowledged deliveries not counted"},
			},
		})
	}
}

// ----------------------------------------------------------------- egress ---

func buildEgress(b *graphBuilder, in GraphInputs) {
	connectors := sortedConnectors(in.Connectors)
	boundCount := 0
	groupStatus := StatusOK
	for _, connector := range connectors {
		if connectorStatus(connector) == StatusOK {
			boundCount++
		} else if connectorStatus(connector) == StatusDown {
			groupStatus = StatusWarning
		}
	}
	b.add(Node{
		ID: nodeConnectors, Kind: KindGroup, Column: ColumnEgress,
		Label:      "SMPP connectors",
		Sublabel:   fmt.Sprintf("%d of %s bound", boundCount, countLabel(len(connectors), "connector", "connectors")),
		Status:     groupStatus,
		ChildCount: len(connectors),
		Ref:        &Ref{Resource: "connectors"},
	})
	for _, connector := range connectors {
		status := connectorStatus(connector)
		observation := in.Metrics.Connectors[connector.CID]
		legacy := in.SMPPcCounters[connector.CID]
		metrics := map[string]float64{
			"submit_success": float64(in.Metrics.SubmitsByConnector(connector.CID, stats.SubmitSuccess)),
			"submit_failure": float64(in.Metrics.SubmitsByConnector(connector.CID, stats.SubmitFailure)),
			"submit_attempt": float64(in.Metrics.SubmitsByConnector(connector.CID, stats.SubmitAttempt)),
			"bound_seconds":  observation.UptimeSeconds,
			// From the per-connector registry, which the map previously ignored
			// entirely — leaving a connector card blank on a gateway that had
			// simply not sent anything yet, and silent about a link that was
			// reconnecting every few seconds.
			"connects":    float64(legacy["connected_count"]),
			"disconnects": float64(legacy["disconnected_count"]),
			"submit_sm":   float64(legacy["submit_sm_count"]),
			"deliver_sm":  float64(legacy["deliver_sm_count"]),
			"throttled":   float64(legacy["throttling_error_count"]),
			"elinks":      float64(legacy["elink_count"]),
		}
		if latency, ok := in.Metrics.SubmitLatency[connector.CID]; ok {
			if p95, has := latency.Quantile(0.95); has {
				metrics["latency_p95_seconds"] = p95
			}
		}
		reason := ""
		switch {
		case status == StatusDown:
			reason = "started but not bound"
		case status == StatusIdle:
			reason = "stopped"
		case legacy["disconnected_count"] >= FlappingDisconnects:
			// Bound right now, but it has not stayed bound. Without this the
			// card reads healthy while the link drops every few seconds, and
			// the messages it loses on each drop have no visible explanation.
			status = StatusWarning
			reason = fmt.Sprintf("link is flapping — %d disconnects since start", legacy["disconnected_count"])
		}
		b.add(Node{
			ID: connectorNodeID(connector.CID), Kind: KindSMPPConnector, Column: ColumnEgress,
			Label: connector.CID, Parent: nodeConnectors, Status: status, Reason: reason,
			Sublabel:  orDefault(connector.Observed, "UNKNOWN"),
			ManagedBy: connector.ManagedBy, Metrics: metrics,
			Ref: &Ref{Resource: "connectors", ID: connector.CID},
			Detail: []DetailField{
				{Label: "Connector", Value: connector.CID},
				{Label: "State", Value: orDefault(connector.Observed, "UNKNOWN")},
				{Label: "Desired", Value: startedStopped(connector.DesiredStarted)},
				{Label: "Upstream", Value: hostPort(connector.Host, connector.Port)},
				{Label: "System ID", Value: connector.SystemID},
				{Label: "Bind type", Value: string(connector.Bind)},
				{Label: "Bound for", Value: duration(observation.UptimeSeconds)},
				{Label: "Connects / disconnects", Value: fmt.Sprintf("%d / %d",
					legacy["connected_count"], legacy["disconnected_count"])},
				{Label: "submit_sm sent", Value: strconv.FormatInt(legacy["submit_sm_count"], 10)},
				{Label: "deliver_sm received", Value: strconv.FormatInt(legacy["deliver_sm_count"], 10)},
				{Label: "Throttled", Value: strconv.FormatInt(legacy["throttling_error_count"], 10)},
				{Label: "Managed by", Value: connector.ManagedBy},
			},
		})
		b.link("edge:queues->"+connectorNodeID(connector.CID), nodeQueues, connectorNodeID(connector.CID), EdgeMT)
	}

	terminations := sortedTermination(in.Termination)
	if len(terminations) > 0 {
		b.add(Node{
			ID: nodeTermination, Kind: KindGroup, Column: ColumnEgress,
			Label:      "Termination connectors",
			Sublabel:   countLabel(len(terminations), "connector", "connectors"),
			Status:     StatusOK,
			ChildCount: len(terminations),
			Ref:        &Ref{Resource: "termination-connectors"},
		})
		b.link("edge:queues->termination", nodeQueues, nodeTermination, EdgeMT)
		for _, connector := range terminations {
			census, censusSeen := in.Metrics.TerminationSpool[connector.CID]
			status, reason := StatusOK, ""
			switch {
			case connector.DesiredStarted && !strings.EqualFold(connector.Observed, "CONSUMING"):
				status, reason = StatusDown, "started but not consuming"
			case !connector.DesiredStarted:
				status, reason = StatusIdle, "stopped"
			case census.DeadLettered > 0:
				status, reason = StatusWarning, fmt.Sprintf("%d dead-lettered rows", census.DeadLettered)
			case census.ReceiptsOverdue > 0:
				status, reason = StatusWarning, fmt.Sprintf("%d receipts overdue", census.ReceiptsOverdue)
			}
			b.add(Node{
				ID: terminationNodeID(connector.CID), Kind: KindTermination, Column: ColumnEgress,
				Label: connector.CID, Parent: nodeTermination, Status: status, Reason: reason,
				Sublabel:  orDefault(connector.Observed, "UNKNOWN"),
				ManagedBy: connector.ManagedBy,
				Metrics: map[string]float64{
					"spool_rows":       float64(census.Rows),
					"dead_lettered":    float64(census.DeadLettered),
					"receipts_overdue": float64(census.ReceiptsOverdue),
				},
				Ref: &Ref{Resource: "termination-connectors", ID: connector.CID},
				Detail: []DetailField{
					{Label: "Connector", Value: connector.CID},
					{Label: "State", Value: orDefault(connector.Observed, "UNKNOWN")},
					{Label: "Spool rows", Value: measured(census.Rows, censusSeen)},
					{Label: "Dead-lettered", Value: measured(census.DeadLettered, censusSeen)},
					{Label: "Receipts overdue", Value: measured(census.ReceiptsOverdue, censusSeen)},
					{Label: "Managed by", Value: connector.ManagedBy},
				},
			})
		}
	}

	moRoutes := sortedMORoutes(in.MORoutes)
	b.add(Node{
		ID: nodeMORoutes, Kind: KindGroup, Column: ColumnQueues, Lane: LaneReturn,
		Label:      "MO routes",
		Sublabel:   countLabel(len(moRoutes), "route", "routes"),
		Status:     StatusOK,
		ChildCount: len(moRoutes),
		Ref:        &Ref{Resource: "mo-routes"},
	})
	b.link("edge:queues->mo-routes", nodeQueues, nodeMORoutes, EdgeMO)
	for _, route := range moRoutes {
		b.add(Node{
			ID: moRouteNodeID(route.Order), Kind: KindMORoute, Column: ColumnQueues, Lane: LaneReturn,
			Label:     routeLabel(route.Order, route.Default),
			Sublabel:  orDefault(route.FilterConnectorID, "any source"),
			Parent:    nodeMORoutes,
			Status:    StatusOK,
			ManagedBy: route.ManagedBy,
			Ref:       &Ref{Resource: "mo-routes", ID: strconv.Itoa(route.Order)},
			Detail: []DetailField{
				{Label: "Order", Value: strconv.Itoa(route.Order)},
				{Label: "Source connector filter", Value: orDefault(route.FilterConnectorID, "any")},
				{Label: "Managed by", Value: route.ManagedBy},
			},
		})
	}

	destinations := sortedHTTPDestinations(in.HTTPDestinations)
	b.add(Node{
		ID: nodeHTTPDests, Kind: KindGroup, Column: ColumnPolicy, Lane: LaneReturn,
		Label:      "HTTP destinations",
		Sublabel:   countLabel(len(destinations), "destination", "destinations"),
		Status:     StatusOK,
		ChildCount: len(destinations),
		Ref:        &Ref{Resource: "http-connectors"},
	})
	b.link("edge:mo-routes->http", nodeMORoutes, nodeHTTPDests, EdgeMO)
	for _, destination := range destinations {
		b.add(Node{
			ID: "http-destination:" + destination.CID, Kind: KindHTTPDestination, Column: ColumnPolicy, Lane: LaneReturn,
			Label: destination.CID, Parent: nodeHTTPDests, Status: StatusOK,
			Sublabel: destination.Method,
			Ref:      &Ref{Resource: "http-connectors", ID: destination.CID},
			Detail: []DetailField{
				{Label: "Destination", Value: destination.CID},
				{Label: "URL", Value: destination.BaseURL},
				{Label: "Method", Value: destination.Method},
			},
		})
	}

	// The throwers are the workers that actually perform partner callbacks. A
	// configured dlr-url with no thrower running is accepted and never called,
	// which is precisely the class of fault this map exists to show.
	buildThrower(b, in, nodeDLRThrower, "DLR thrower", in.HasIngress && in.Ingress.DLRThrowerRunning, EdgeDLR,
		"delivery receipts are accepted but never delivered")
	buildThrower(b, in, nodeMOThrower, "MO thrower", in.HasIngress && in.Ingress.MOThrowerRunning, EdgeMO,
		"inbound messages are routed but never delivered")
}

func buildThrower(b *graphBuilder, in GraphInputs, id, label string, running bool, kind, downReason string) {
	status, reason, sublabel := StatusOK, "", "enabled"
	if !running {
		status, reason, sublabel = StatusDown, downReason, "not running"
	}
	if !in.HasIngress {
		status, reason, sublabel = StatusUnknown, "", "unknown"
	}
	b.add(Node{
		ID: id, Kind: KindThrower, Column: ColumnCore, Lane: LaneReturn,
		Label: label, Sublabel: sublabel, Status: status, Reason: reason,
		Detail: []DetailField{
			{Label: "Worker", Value: label},
			{Label: "Running", Value: yesNo(running)},
			{Label: "Callback timeout", Value: seconds(in.Ingress.CallbackTimeoutSeconds)},
			{Label: "Max retries", Value: strconv.Itoa(in.Ingress.CallbackMaxRetries)},
		},
	})
	edge := b.link("edge:queues->"+id, nodeQueues, id, kind)
	if status == StatusDown {
		edge.Degraded = true
		edge.Reason = reason
		edge.Label = "not running"
	}
}

// ----------------------------------------------------------- downstream ---

// buildDownstream draws how a terminated message actually reaches the customer.
//
// This is the half of the picture the console never had. A termination
// connector decodes a message and then hands it on two ways, and an operator
// asked "did the customer get it?" has to check both: a signed HTTP push to the
// customer's endpoint, and a spool the customer pulls from with a scoped token.
// Either can be broken while the other looks fine.
//
// The pull side is the interesting one, because there is no connection to
// observe. The customer polls, so a healthy consumer and a consumer whose
// process died look identical except for how long the token has been silent —
// which is exactly why the age is drawn on the card in every state.
func buildDownstream(b *graphBuilder, in GraphInputs) {
	terminations := sortedTermination(in.Termination)

	// The push sink, one card per connector that actually has an endpoint. A
	// connector with none is the documented pull-only deployment, and giving it
	// a delivery card would claim a push that does not exist.
	for _, connector := range terminations {
		endpoint := strings.TrimSpace(connector.Delivery.Endpoint)
		if endpoint == "" {
			continue
		}
		from := terminationNodeID(connector.CID)
		id := "delivery:" + connector.CID

		attempts := in.Metrics.TerminationDeliveries[TerminationKeyOf(connector.CID, "attempt")]
		success := in.Metrics.TerminationDeliveries[TerminationKeyOf(connector.CID, "success")]
		failure := in.Metrics.TerminationDeliveries[TerminationKeyOf(connector.CID, "failure")]
		dead := in.Metrics.TerminationDeliveries[TerminationKeyOf(connector.CID, "dead_letter")]

		status, reason := StatusOK, ""
		switch {
		case dead > 0:
			status = StatusDown
			reason = fmt.Sprintf("%d message(s) gave up after %d attempts", dead, deliveryAttempts(connector))
		case failure > 0:
			status = StatusWarning
			reason = fmt.Sprintf("%d delivery failure(s), retrying", failure)
		}

		b.add(Node{
			ID: id, Kind: KindDeliveryEndpoint, Column: ColumnDelivery,
			Label: hostOf(endpoint), Sublabel: "push · " + connector.CID,
			Status: status, Reason: reason,
			Metrics: map[string]float64{
				"attempts":      float64(attempts),
				"success":       float64(success),
				"failure":       float64(failure),
				"dead_lettered": float64(dead),
			},
			Detail: []DetailField{
				{Label: "Endpoint", Value: endpoint},
				{Label: "For connector", Value: connector.CID},
				{Label: "Signed", Value: yesNo(connector.HasSecret)},
				{Label: "Max attempts", Value: strconv.Itoa(deliveryAttempts(connector))},
				{Label: "Delivered", Value: strconv.FormatUint(success, 10)},
				{Label: "Failed", Value: strconv.FormatUint(failure, 10)},
				{Label: "Dead-lettered", Value: strconv.FormatUint(dead, 10)},
			},
			Ref: &Ref{Resource: "termination-connectors", ID: connector.CID},
		})

		edge := b.link("edge:"+from+"->"+id, from, id, EdgeMT)
		edge.Label = "push"
		if dead > 0 {
			edge.Degraded = true
			edge.Reason = reason
			edge.Label = "delivery dead-lettered"
		}
	}

	// The spool exists whenever a termination connector does: every terminated
	// message lands in it, whether or not anything pushes or pulls.
	if len(terminations) > 0 {
		// The spool census is a periodic observation, not an event counter, so
		// a connector missing from the map has not been measured yet rather
		// than measured as empty. Right after a restart every gauge is absent,
		// and reporting that as "0 rows held" would tell an operator the spool
		// had drained when nothing had looked at it.
		var rows, dead, overdue int64
		observed := false
		for _, connector := range terminations {
			census, seen := in.Metrics.TerminationSpool[connector.CID]
			if !seen {
				continue
			}
			observed = true
			rows += census.Rows
			dead += census.DeadLettered
			overdue += census.ReceiptsOverdue
		}
		status, reason := StatusOK, ""
		switch {
		case !observed:
			status, reason = StatusUnknown, "spool not yet measured since start"
		case dead > 0:
			status, reason = StatusWarning, fmt.Sprintf("%d dead-lettered row(s)", dead)
		}
		retention := "24h"
		if in.SpoolRetention > 0 {
			retention = in.SpoolRetention.String()
		}
		b.add(Node{
			ID: nodeSpool, Kind: KindSpool, Column: ColumnDelivery,
			Label: "Message spool", Sublabel: spoolSublabel(rows, observed),
			Status: status, Reason: reason,
			// Omitted entirely when nothing has measured yet, so the analytics
			// matrix renders an em dash for the same reason the card does
			// rather than a zero the card is careful not to claim.
			Metrics: spoolMetrics(rows, dead, overdue, observed),
			Detail: []DetailField{
				{Label: "Rows held", Value: measured(rows, observed)},
				{Label: "Dead-lettered", Value: measured(dead, observed)},
				{Label: "Receipts overdue", Value: measured(overdue, observed)},
				// Worth stating next to a stalled consumer: the backlog it has
				// not read is on a clock.
				{Label: "Retention", Value: retention},
			},
			Ref: &Ref{Resource: "messages"},
		})
		for _, connector := range terminations {
			from := terminationNodeID(connector.CID)
			b.link("edge:"+from+"->spool", from, nodeSpool, EdgeMT)
		}
	}

	buildPullTokens(b, in)
}

// buildPullTokens draws each customer's pull credential and how recently it
// actually read anything.
func buildPullTokens(b *graphBuilder, in GraphInputs) {
	tokens := sortedPullTokens(in.PullTokens)
	if len(tokens) == 0 {
		return
	}

	worst := StatusOK
	for _, token := range tokens {
		if status, _ := pullTokenStatus(token, in.ObservedAt); status == StatusDown {
			worst = StatusDown
		} else if status == StatusWarning && worst == StatusOK {
			worst = StatusWarning
		}
	}

	b.add(Node{
		ID: nodePullTokens, Kind: KindGroup, Column: ColumnDelivery,
		Label:      "Pull tokens",
		Sublabel:   countLabel(len(tokens), "customer", "customers"),
		Status:     worst,
		ChildCount: len(tokens),
		Ref:        &Ref{Resource: "message-consumers"},
	})

	for _, token := range tokens {
		status, reason := pullTokenStatus(token, in.ObservedAt)
		id := "pull-token:" + token.ID

		detail := []DetailField{
			{Label: "Token", Value: orDefault(token.Label, token.ID)},
			{Label: "Last pull", Value: pullAge(token, in.ObservedAt)},
			{Label: "May read connectors", Items: token.Scope.Connectors},
			{Label: "Message text", Value: yesNo(token.Scope.IncludeText)},
		}
		if token.Reads != nil {
			detail = append(detail, DetailField{Label: "Reads", Value: strconv.FormatInt(*token.Reads, 10)})
		}
		if token.Rows != nil {
			detail = append(detail, DetailField{Label: "Rows fetched", Value: strconv.FormatInt(*token.Rows, 10)})
		}
		if token.Denied != nil && *token.Denied > 0 {
			detail = append(detail, DetailField{Label: "Refused reads", Value: strconv.FormatInt(*token.Denied, 10)})
		}
		if token.Revoked {
			detail = append(detail, DetailField{Label: "Revoked", Value: "yes"})
		}

		metrics := map[string]float64{}
		if token.Reads != nil {
			metrics["reads"] = float64(*token.Reads)
		}
		if token.Rows != nil {
			metrics["rows"] = float64(*token.Rows)
		}
		if token.Denied != nil {
			metrics["denied"] = float64(*token.Denied)
		}
		if last := lastPull(token); last != nil && !in.ObservedAt.IsZero() {
			metrics["silent_seconds"] = in.ObservedAt.Sub(*last).Seconds()
		}

		b.add(Node{
			ID: id, Kind: KindPullToken, Column: ColumnDelivery,
			Label: orDefault(token.Label, token.ID), Parent: nodePullTokens,
			Sublabel: pullAge(token, in.ObservedAt),
			Status:   status, Reason: reason,
			Metrics: metrics, Detail: detail,
			Ref: &Ref{Resource: "message-consumers", ID: token.ID},
		})

		// Scope is the authorization, so it is also the truth about who
		// receives whose traffic — the question no console page answers today.
		for _, cid := range token.Scope.Connectors {
			from := terminationNodeID(cid)
			edge := b.link("edge:"+from+"->"+id, from, id, EdgeMT)
			edge.Label = "pull"
			// A customer that stopped polling is not a broken path: nothing on
			// our side failed, and "Pull stalled" already reports it. Marking
			// it degraded here would count one silent consumer twice, which is
			// how a problem rail stops being believed.
			switch {
			case token.Revoked:
				edge.Inactive = true
				edge.Reason = "token revoked"
			case status == StatusDown || status == StatusWarning:
				edge.Inactive = true
				edge.Reason = reason
				edge.Label = "not pulling"
			}
		}
	}
}

// pullTokenStatus grades a credential by silence, because a polling consumer
// offers nothing else to observe.
func pullTokenStatus(token messageConsumerResource, now time.Time) (string, string) {
	if token.Revoked {
		return StatusIdle, "token revoked"
	}
	// Refusals are their own signal: a token being turned away is a different
	// problem from a token going quiet, and it would otherwise hide behind a
	// healthy-looking recent read.
	if token.Denied != nil && *token.Denied > 0 {
		return StatusWarning, fmt.Sprintf("%d read(s) refused — check the token scope", *token.Denied)
	}
	last := lastPull(token)
	if last == nil {
		return StatusWarning, "issued but never used"
	}
	if now.IsZero() {
		return StatusUnknown, ""
	}
	silent := now.Sub(*last)
	switch {
	case silent >= PullLostAfter:
		return StatusDown, "no pull for " + duration(silent.Seconds())
	case silent >= PullStaleAfter:
		return StatusWarning, "no pull for " + duration(silent.Seconds())
	}
	return StatusOK, ""
}

// lastPull prefers the audited read over the authentication stamp: a token that
// authenticates and reads nothing is still being used, and the read is what the
// customer actually depends on.
func lastPull(token messageConsumerResource) *time.Time {
	if parsed := parseStamp(token.LastReadAt); parsed != nil {
		return parsed
	}
	return parseStamp(token.LastUsedAt)
}

func pullAge(token messageConsumerResource, now time.Time) string {
	last := lastPull(token)
	if last == nil {
		return "never pulled"
	}
	if now.IsZero() {
		return last.UTC().Format(time.RFC3339)
	}
	return duration(now.Sub(*last).Seconds()) + " ago"
}

func parseStamp(value *string) *time.Time {
	if value == nil || *value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return nil
	}
	return &parsed
}

// TerminationKeyOf builds the metrics key for one connector and outcome.
func TerminationKeyOf(connector, outcome string) stats.TerminationKey {
	return stats.TerminationKey{Connector: connector, Outcome: outcome}
}

func deliveryAttempts(connector terminationConnectorResource) int {
	if connector.Delivery.MaxAttempts > 0 {
		return connector.Delivery.MaxAttempts
	}
	return termination.DefaultMaxDeliveryAttempts
}

// hostOf shortens a delivery URL to something that fits a card without losing
// which endpoint it is. The full URL stays in the inspector.
func hostOf(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return endpoint
	}
	return parsed.Host
}

func sortedPullTokens(values []messageConsumerResource) []messageConsumerResource {
	out := append([]messageConsumerResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// measured renders a gauge that may not have been observed yet. "—" and "0" are
// different answers: one means nothing has looked, the other that it looked and
// found nothing.
func measured(value int64, observed bool) string {
	if !observed {
		return "—"
	}
	return strconv.FormatInt(value, 10)
}

func spoolMetrics(rows, dead, overdue int64, observed bool) map[string]float64 {
	if !observed {
		return nil
	}
	return map[string]float64{
		"rows": float64(rows), "dead_lettered": float64(dead), "receipts_overdue": float64(overdue),
	}
}

func spoolSublabel(rows int64, observed bool) string {
	if !observed {
		return "not yet measured"
	}
	return fmt.Sprintf("%d rows", rows)
}

// --------------------------------------------------------------- carriers ---

// buildCarriers draws one node per distinct upstream endpoint rather than one
// per connector: two connectors bound to the same SMSC are two links to one
// carrier, and drawing them as two carriers would overstate the redundancy.
func buildCarriers(b *graphBuilder, in GraphInputs) {
	endpoints := map[string][]connectorResource{}
	for _, connector := range sortedConnectors(in.Connectors) {
		endpoint := hostPort(connector.Host, connector.Port)
		if endpoint == "" {
			continue
		}
		endpoints[endpoint] = append(endpoints[endpoint], connector)
	}
	if len(endpoints) == 0 {
		return
	}

	names := make([]string, 0, len(endpoints))
	for endpoint := range endpoints {
		names = append(names, endpoint)
	}
	sort.Strings(names)

	b.add(Node{
		ID: nodeCarriers, Kind: KindGroup, Column: ColumnCarriers,
		Label:      "Upstream carriers",
		Sublabel:   countLabel(len(names), "endpoint", "endpoints"),
		Status:     StatusOK,
		ChildCount: len(names),
	})

	for _, endpoint := range names {
		connectors := endpoints[endpoint]
		status := StatusIdle
		links := make([]string, 0, len(connectors))
		for _, connector := range connectors {
			links = append(links, connector.CID)
			if connectorStatus(connector) == StatusOK {
				status = StatusOK
			}
		}
		id := "carrier:" + endpoint
		b.add(Node{
			ID: id, Kind: KindCarrier, Column: ColumnCarriers,
			Label: endpoint, Parent: nodeCarriers, Status: status,
			Sublabel: countLabel(len(connectors), "link", "links"),
			Detail: []DetailField{
				{Label: "Endpoint", Value: endpoint},
				{Label: "Connectors", Items: links},
			},
		})
		for _, connector := range connectors {
			from := connectorNodeID(connector.CID)
			edge := b.link("edge:"+from+"->"+id, from, id, EdgeMT)
			if connectorStatus(connector) != StatusOK {
				// Inactive, not degraded: the connector node already reports
				// this fault, and the route edge into it already counts as a
				// broken path. Counting the carrier hop as well would report
				// one dead connector three times.
				edge.Inactive = true
				edge.Reason = "connector is not bound"
				edge.Label = "no active bind"
			}
			// The receipt lane home, drawn separately so the return path is
			// visible even when nothing is flowing outbound.
			b.link("edge:"+id+"->"+from+":dlr", id, from, EdgeDLR)
		}
	}
}

// ----------------------------------------------------------------- infra ---

func buildInfrastructure(b *graphBuilder, in GraphInputs) {
	names := make([]string, 0, len(in.Health))
	for name := range in.Health {
		// Per-connector checks are already drawn on their connector node;
		// repeating them as infrastructure would double-count every fault.
		if strings.HasPrefix(name, "connector:") {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)

	worst := StatusOK
	for _, name := range names {
		if healthStatus(in.Health[name]) != StatusOK {
			worst = StatusWarning
		}
	}

	b.add(Node{
		ID: nodeInfra, Kind: KindGroup, Column: ColumnCore, Lane: LaneInfra,
		Label:      "Infrastructure",
		Sublabel:   countLabel(len(names), "dependency", "dependencies"),
		Status:     worst,
		ChildCount: len(names),
	})
	b.link("edge:core->infra", nodeCore, nodeInfra, EdgeConfig)

	for _, name := range names {
		detail := in.Health[name]
		status := healthStatus(detail)
		reason := ""
		if status != StatusOK {
			reason = detail
		}
		b.add(Node{
			ID: "infra:" + name, Kind: KindInfraDependency, Column: ColumnCore, Lane: LaneInfra,
			Label: infraLabel(name), Parent: nodeInfra, Status: status, Reason: reason,
			Sublabel: detail,
			Detail: []DetailField{
				{Label: "Dependency", Value: name},
				{Label: "Reported", Value: detail},
			},
		})
	}
}

// ------------------------------------------------------------ route edges ---

// buildRouteEdges connects each MT route to the connectors it selects, and
// marks the edge degraded when the destination cannot carry traffic. This is
// the cross-page fault the table views structurally cannot show: the routes
// page knows the connector id, the connectors page knows the bind state, and
// only here do the two meet.
func buildRouteEdges(b *graphBuilder, in GraphInputs) {
	connectors := map[string]connectorResource{}
	for _, connector := range in.Connectors {
		connectors[connector.CID] = connector
	}
	terminations := map[string]terminationConnectorResource{}
	for _, connector := range in.Termination {
		terminations[connector.CID] = connector
	}

	// A route can name a connector that does not exist — the connector was
	// deleted, or the id was mistyped. That dangling reference is the single
	// most invisible fault in the console (no page lists it, because there is
	// nothing to list), so it gets its own card rather than an edge pointing at
	// nothing. Without a node here the edge would have no target and the canvas
	// would silently drop exactly the fault this map is for.
	addMissingConnectorNodes(b, in, connectors, terminations)

	for _, route := range sortedRoutes(in.Routes) {
		from := mtRouteNodeID(route.Order)
		for _, cid := range route.ConnectorCandidates() {
			var to, reason string
			if isTerminationRoute(route.ConnectorType) {
				connector, ok := terminations[cid]
				to = terminationNodeID(cid)
				switch {
				case !ok:
					reason = fmt.Sprintf("termination connector %q does not exist", cid)
				case !connector.DesiredStarted:
					reason = "termination connector is stopped"
				case !strings.EqualFold(connector.Observed, "CONSUMING"):
					reason = "termination connector is not consuming"
				}
			} else {
				connector, ok := connectors[cid]
				to = connectorNodeID(cid)
				switch {
				case !ok:
					reason = fmt.Sprintf("connector %q does not exist", cid)
				case !connector.DesiredStarted:
					reason = "connector is stopped"
				case connectorStatus(connector) != StatusOK:
					reason = "no active bind"
				}
			}

			edge := b.link(fmt.Sprintf("edge:%s->%s", from, to), from, to, EdgeMT)
			if reason != "" {
				edge.Degraded = true
				edge.Reason = reason
				edge.Label = reason
			}
		}
	}
}

// addMissingConnectorNodes gives every connector a route names but that does
// not exist a card of its own, grouped together in the egress column. The group
// appears only when something is actually dangling — an empty "Missing
// connectors" card on a healthy gateway would be noise that teaches operators
// to ignore the column.
func addMissingConnectorNodes(
	b *graphBuilder,
	in GraphInputs,
	connectors map[string]connectorResource,
	terminations map[string]terminationConnectorResource,
) {
	missing := map[string]string{} // node id -> connector id
	for _, route := range sortedRoutes(in.Routes) {
		for _, cid := range route.ConnectorCandidates() {
			if isTerminationRoute(route.ConnectorType) {
				if _, ok := terminations[cid]; !ok {
					missing[terminationNodeID(cid)] = cid
				}
				continue
			}
			if _, ok := connectors[cid]; !ok {
				missing[connectorNodeID(cid)] = cid
			}
		}
	}
	if len(missing) == 0 {
		return
	}

	ids := sortedSet(toSet(missing))
	b.add(Node{
		ID: nodeMissing, Kind: KindGroup, Column: ColumnEgress,
		Label:      "Missing connectors",
		Sublabel:   countLabel(len(ids), "dangling reference", "dangling references"),
		Status:     StatusDown,
		ChildCount: len(ids),
		Reason:     "routed to, but not configured",
	})
	for _, id := range ids {
		cid := missing[id]
		b.add(Node{
			ID: id, Kind: KindMissingConnector, Column: ColumnEgress,
			Label: cid, Parent: nodeMissing, Status: StatusDown,
			Sublabel: "does not exist",
			Reason:   "a route names this connector and nothing defines it",
			Detail: []DetailField{
				{Label: "Connector", Value: cid},
				{Label: "State", Value: "not configured"},
			},
		})
	}
}

func toSet(values map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for key := range values {
		out[key] = struct{}{}
	}
	return out
}

// buildAnalytics assembles the matrix from nodes already built, so a row and
// the card it describes can never disagree.
//
// Pull credentials and delivery endpoints are kept in separate tables on
// purpose. Only the pull side has a queryable history — the audit table is
// windowable, the delivery counters are cumulative process-local totals — and
// putting a since-boot number under a "last 15m" heading would be a lie the
// layout tells for us.
func buildAnalytics(nodes []Node, in GraphInputs) *Analytics {
	if len(in.PullWindows) == 0 {
		return nil
	}

	analytics := &Analytics{
		RetentionNote: analyticsRetentionNote(in.SpoolRetention),
		PullTokens:    []AnalyticsRow{},
		Delivery:      []AnalyticsRow{},
	}

	for _, node := range nodes {
		switch node.Kind {
		case KindPullToken:
			row := AnalyticsRow{
				NodeID: node.ID, Kind: node.Kind, Label: node.Label,
				Status: node.Status, Detail: node.Sublabel, Reason: node.Reason,
			}
			id := strings.TrimPrefix(node.ID, "pull-token:")
			for _, window := range in.PullWindows {
				entry := window.ByToken[id]
				entry.Window = window.Window
				row.Windows = append(row.Windows, entry)
			}
			analytics.PullTokens = append(analytics.PullTokens, row)

		case KindDeliveryEndpoint, KindSpool:
			totals := map[string]int64{}
			for name, value := range node.Metrics {
				totals[name] = int64(value)
			}
			analytics.Delivery = append(analytics.Delivery, AnalyticsRow{
				NodeID: node.ID, Kind: node.Kind, Label: node.Label,
				Status: node.Status, Detail: node.Sublabel, Reason: node.Reason,
				Totals: totals,
			})
		}
	}
	return analytics
}

func analyticsRetentionNote(retention time.Duration) string {
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	return "Read history is bounded by the pull audit, pruned with the message spool at " +
		retention.String() + ". Longer trends need a scrape of /metrics/prometheus."
}

// --------------------------------------------------------------- problems ---

func buildProblems(nodes []Node, edges []Edge, in GraphInputs) []Problem {
	// A broken path is highlighted at the end that cannot carry traffic — the
	// unbound connector, the stopped thrower. When that end is not a node at
	// all (a route naming a connector that does not exist) the fault belongs to
	// the source instead, since there is nothing else to point at.
	broken := map[string]struct{}{}
	brokenEdges := 0
	for _, edge := range edges {
		if !edge.Degraded {
			continue
		}
		brokenEdges++
		target := edge.To
		if !hasNode(nodes, target) {
			target = edge.From
		}
		broken[target] = struct{}{}
	}

	var unbound, backlog, stalled []string
	for _, node := range nodes {
		switch node.Kind {
		case KindSMPPConnector, KindTermination:
			if node.Status == StatusDown {
				unbound = append(unbound, node.ID)
			}
		case KindQueue:
			if node.Status == StatusWarning {
				backlog = append(backlog, node.ID)
			}
		case KindPullToken:
			// A revoked token is idle, not stalled: it stopped pulling because
			// somebody turned it off, which is not a fault to chase.
			if node.Status == StatusWarning || node.Status == StatusDown {
				stalled = append(stalled, node.ID)
			}
		}
	}

	problems := []Problem{
		{Kind: ProblemBrokenPath, Label: "Broken paths", Count: brokenEdges, NodeIDs: sortedSet(broken)},
		{Kind: ProblemUnbound, Label: "Unbound", Count: len(unbound), NodeIDs: sorted(unbound)},
		{Kind: ProblemQueueBacklog, Label: "Queue backlog", Count: len(backlog), NodeIDs: sorted(backlog)},
		{Kind: ProblemStalePull, Label: "Pull stalled", Count: len(stalled), NodeIDs: sorted(stalled)},
	}
	return problems
}

// -------------------------------------------------------- structure hash ---

// structureHash digests identity and shape, never a measurement. Adding a field
// here that moves with traffic would defeat the browser's layout memoization —
// see the note at the top of this file.
func structureHash(nodes []Node, edges []Edge) string {
	lines := make([]string, 0, len(nodes)+len(edges))
	for _, node := range nodes {
		lines = append(lines, "n\x00"+node.ID+"\x00"+node.Kind+"\x00"+node.Parent+"\x00"+node.Column+"\x00"+node.Lane)
	}
	for _, edge := range edges {
		lines = append(lines, "e\x00"+edge.ID+"\x00"+edge.From+"\x00"+edge.To+"\x00"+edge.Kind)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:16])
}

// ---------------------------------------------------------------- helpers ---

func connectorStatus(connector connectorResource) string {
	if strings.EqualFold(connector.Observed, "BOUND") {
		return StatusOK
	}
	if !connector.DesiredStarted {
		return StatusIdle
	}
	if connector.Observed == "" {
		return StatusUnknown
	}
	return StatusDown
}

func healthStatus(detail string) string {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "ok", "bound", "healthy", "ready":
		return StatusOK
	case "":
		return StatusUnknown
	case "connecting", "starting":
		return StatusWarning
	default:
		return StatusDown
	}
}

// isTerminationRoute reads a route's declared connector type the way the
// routing engine does. The wire value is "term" — the engine's own constant —
// and comparing against a friendlier spelling here would silently resolve every
// termination route against the SMPP connector list, marking healthy paths
// broken. Reusing routingtable's constant is what keeps the two in step.
func isTerminationRoute(connectorType string) bool {
	return strings.EqualFold(connectorType, string(routingtable.TERM))
}

func connectorTypeLabel(connectorType string) string {
	if isTerminationRoute(connectorType) {
		return string(routingtable.TERM)
	}
	return string(routingtable.SMPPC)
}

// matchAge renders when a route last fired. A route that has never matched says
// so rather than showing an epoch date or an empty cell.
func matchAge(at time.Time, ever bool, now time.Time) string {
	if !ever {
		return "never"
	}
	if now.IsZero() {
		return at.UTC().Format(time.RFC3339)
	}
	return duration(now.Sub(at).Seconds()) + " ago"
}

func mtRouteNodeID(order int) string      { return "mt-route:" + strconv.Itoa(order) }
func moRouteNodeID(order int) string      { return "mo-route:" + strconv.Itoa(order) }
func connectorNodeID(cid string) string   { return "connector:" + cid }
func terminationNodeID(cid string) string { return "termination:" + cid }
func queueNodeID(name string) string      { return "queue:" + name }

func routeLabel(order int, isDefault bool) string {
	if isDefault {
		return fmt.Sprintf("route %d (default)", order)
	}
	return fmt.Sprintf("route %d", order)
}

func describeFilter(kind, pattern, value string) string {
	detail := pattern
	if detail == "" {
		detail = value
	}
	if detail == "" {
		return kind
	}
	return kind + " " + detail
}

func infraLabel(name string) string {
	switch name {
	case "amqp":
		return "AMQP broker"
	case "postgres":
		return "PostgreSQL"
	case "bridge":
		return "Compatibility codec"
	default:
		return strings.ReplaceAll(name, "_", " ")
	}
}

func hostPort(host string, port int) string {
	if host == "" {
		return ""
	}
	return host + ":" + strconv.Itoa(port)
}

func countLabel(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + plural
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func startedStopped(value bool) string {
	if value {
		return "started"
	}
	return "stopped"
}

func intPointer(value *int, fallback string) string {
	if value == nil {
		return fallback
	}
	return strconv.Itoa(*value)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func seconds(value float64) string {
	if value <= 0 {
		return "unset"
	}
	return formatFloat(value) + "s"
}

func duration(totalSeconds float64) string {
	if totalSeconds <= 0 {
		return "—"
	}
	d := time.Duration(totalSeconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %02d:%02d:%02d", days, hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func hasNode(nodes []Node, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// The sorted* helpers below exist because the admin services return rows in
// storage order while the config lists come from the file: without an explicit
// order the document — and therefore StructureHash — would vary between polls
// for reasons the operator never caused.

func sortedConnectors(values []connectorResource) []connectorResource {
	out := append([]connectorResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].CID < out[j].CID })
	return out
}

func sortedTermination(values []terminationConnectorResource) []terminationConnectorResource {
	out := append([]terminationConnectorResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].CID < out[j].CID })
	return out
}

func sortedRoutes(values []routeResource) []routeResource {
	out := append([]routeResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].Order > out[j].Order })
	return out
}

func sortedMORoutes(values []moRouteResource) []moRouteResource {
	out := append([]moRouteResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].Order > out[j].Order })
	return out
}

func sortedUsers(values []userResource) []userResource {
	out := append([]userResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func sortedGroups(values []groupResource) []groupResource {
	out := append([]groupResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].GID < out[j].GID })
	return out
}

func sortedSMPPsUsers(values []smppsUserResource) []smppsUserResource {
	out := append([]smppsUserResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].SystemID < out[j].SystemID })
	return out
}

func sortedFilters(values []filterResource) []filterResource {
	out := append([]filterResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].FID < out[j].FID })
	return out
}

func sortedHTTPDestinations(values []httpConnectorResource) []httpConnectorResource {
	out := append([]httpConnectorResource(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].CID < out[j].CID })
	return out
}
