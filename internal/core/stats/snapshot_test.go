package stats

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sample is one parsed exposition line, keyed by metric name and its labels.
type sampleKey struct {
	name   string
	labels string // "k=v,k2=v2" with keys sorted, so label order never matters
}

// parseExposition turns RenderPrometheus output back into addressable samples.
// It is deliberately strict: an unparseable line fails the test rather than
// being skipped, because a silently ignored line is a silently missed drift.
func parseExposition(t *testing.T, payload []byte) map[sampleKey]string {
	t.Helper()
	samples := make(map[sampleKey]string)
	for _, line := range strings.Split(string(payload), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		space := strings.LastIndexByte(line, ' ')
		if space < 0 {
			t.Fatalf("exposition line has no value: %q", line)
		}
		identity, value := line[:space], line[space+1:]

		key := sampleKey{name: identity}
		if brace := strings.IndexByte(identity, '{'); brace >= 0 {
			if !strings.HasSuffix(identity, "}") {
				t.Fatalf("exposition line has an unterminated label set: %q", line)
			}
			key.name = identity[:brace]
			pairs := strings.Split(identity[brace+1:len(identity)-1], ",")
			sort.Strings(pairs)
			key.labels = strings.Join(pairs, ",")
		}
		samples[key] = value
	}
	return samples
}

// requireSample asserts one snapshot value is present in the rendered text with
// the identical formatting. Values are compared as the strings the exposition
// uses, so a formatting divergence is caught as well as a numeric one.
func requireSample(t *testing.T, samples map[sampleKey]string, name, want string, labels ...string) {
	t.Helper()
	sorted := append([]string(nil), labels...)
	sort.Strings(sorted)
	key := sampleKey{name: name, labels: strings.Join(sorted, ",")}
	got, ok := samples[key]
	if !ok {
		t.Errorf("snapshot reports %s{%s}=%s but RenderPrometheus emitted no such sample",
			name, key.labels, want)
		return
	}
	if got != want {
		t.Errorf("%s{%s}: snapshot says %s, exposition says %s", name, key.labels, want, got)
	}
}

func label(name, value string) string { return name + `="` + escapeLabelValue(value) + `"` }

// seededRegistry records one of every event so no metric family is left
// unexercised by the agreement test below.
func seededRegistry(clock *time.Time) *PrometheusRegistry {
	registry := newPrometheusRegistry(func() time.Time { return *clock })

	registry.RecordSubmit("smsc-primary", SubmitAttempt, "pending", 0)
	registry.RecordSubmit("smsc-primary", SubmitSuccess, "ESME_ROK", 120*time.Millisecond)
	registry.RecordSubmit("smsc-primary", SubmitFailure, "ESME_RTHROTTLED", 3*time.Second)
	registry.RecordSubmit("smsc-secondary", SubmitSuccess, "ESME_ROK", 8*time.Millisecond)

	registry.RecordRouteMatch("mt:10", "smsc-primary")
	registry.RecordRouteMatch("mt:10", "smsc-secondary")
	registry.RecordRouteMatch("mt:0", "smsc-primary")

	registry.RecordDLR(2, "DELIVRD", DLROutcomeDelivered)
	registry.RecordDLR(1, "UNDELIV", DLROutcomeFailed)
	registry.RecordDLR(3, "UNKNOWN", DLROutcomeCorrelationFailure)

	registry.RecordMO("smsc-primary", MOReceived)
	registry.RecordMO("smsc-primary", MORouted)
	registry.RecordMO("smsc-secondary", MODropped)

	registry.SetConnectorState("smsc-primary", "BOUND")
	registry.SetConnectorState("smsc-secondary", "DISCONNECTED")

	registry.SetQueueDepth("submit.sm.smsc-primary", 4821)
	registry.SetQueueDepth("dlr.thrower", 0)

	registry.RecordThroughputRejection("partner-a")
	registry.RecordInterceptorError("mt")

	registry.RecordBillingCharge("partner-a", "EUR", 12.5)
	registry.RecordBillingRefusal("partner-a", "insufficient_balance")
	registry.RecordBillingMismatch("orphan_cdr")

	registry.RecordTerminationVerdict("local-app", TerminationVerdictDelivered)
	registry.RecordTerminationGateBypass("local-app", "gate_unreachable")
	registry.RecordTerminationDelivery("local-app", TerminationDeliverySuccess)
	registry.SetTerminationSpool("local-app", TerminationSpoolCensus{
		Rows: 91, DeadLettered: 2, ReceiptsOverdue: 1,
	})

	registry.SetGatewayHealth("ok")
	return registry
}

// TestSnapshotAgreesWithRenderPrometheus is the anti-drift test: every value a
// Snapshot reports must appear, identically, in the exposition rendered from
// the same registry state. Adding a metric family to one path without the other
// fails here.
func TestSnapshotAgreesWithRenderPrometheus(t *testing.T) {
	clock := time.Date(2026, 7, 31, 15, 43, 27, 0, time.UTC)
	registry := seededRegistry(&clock)
	// Advance after seeding so a bound connector has non-zero uptime; both
	// readers observe the same instant, so they must agree on the number.
	clock = clock.Add(90 * time.Second)

	snapshot := registry.Snapshot()
	samples := parseExposition(t, registry.RenderPrometheus())

	if !snapshot.ObservedAt.Equal(clock) {
		t.Errorf("ObservedAt = %s, want the registry clock %s", snapshot.ObservedAt, clock)
	}

	for key, value := range snapshot.Submits {
		requireSample(t, samples, "synevyr_submit_total", formatUint(value),
			label("connector", key.Connector), label("outcome", key.Outcome), label("status", key.Status))
	}

	for connector, observation := range snapshot.SubmitLatency {
		for _, bucket := range observation.Buckets {
			requireSample(t, samples, "synevyr_submit_round_trip_seconds_bucket", formatUint(bucket.Count),
				label("connector", connector), label("le", formatFloat(bucket.UpperBound)))
		}
		requireSample(t, samples, "synevyr_submit_round_trip_seconds_count", formatUint(observation.Count),
			label("connector", connector))
		requireSample(t, samples, "synevyr_submit_round_trip_seconds_sum", formatFloat(observation.Sum),
			label("connector", connector))
	}

	for key, value := range snapshot.RouteMatches {
		requireSample(t, samples, "synevyr_route_matches_total", formatUint(value),
			label("connector", key.Connector), label("route", key.Route))
	}
	for route, at := range snapshot.RouteLastMatch {
		requireSample(t, samples, "synevyr_route_last_match_seconds",
			formatFloat(float64(at.Unix())), label("route", route))
	}

	for key, value := range snapshot.DLRs {
		requireSample(t, samples, "synevyr_dlr_total", formatUint(value),
			label("final_state", key.FinalState), label("level", key.Level), label("outcome", key.Outcome))
	}

	for key, value := range snapshot.MOs {
		requireSample(t, samples, "synevyr_mo_total", formatUint(value),
			label("connector", key.Connector), label("outcome", key.Outcome))
	}

	for connector, observation := range snapshot.Connectors {
		bound := "0"
		if observation.Bound {
			bound = "1"
		}
		requireSample(t, samples, "synevyr_connector_bound", bound, label("connector", connector))
		requireSample(t, samples, "synevyr_connector_state", "1",
			label("connector", connector), label("state", observation.State))
		requireSample(t, samples, "synevyr_connector_uptime_seconds", formatFloat(observation.UptimeSeconds),
			label("connector", connector))
	}

	for queue, depth := range snapshot.QueueDepths {
		requireSample(t, samples, "synevyr_queue_depth", strconv.FormatInt(depth, 10), label("queue", queue))
	}
	for user, value := range snapshot.ThroughputRejections {
		requireSample(t, samples, "synevyr_throughput_rejections_total", formatUint(value), label("user", user))
	}
	for direction, value := range snapshot.InterceptorErrors {
		requireSample(t, samples, "synevyr_interceptor_errors_total", formatUint(value), label("direction", direction))
	}

	for key, value := range snapshot.BillingCharges {
		requireSample(t, samples, "synevyr_billing_charges_total", formatFloat(value),
			label("currency", key.Currency), label("user", key.User))
	}
	for key, value := range snapshot.BillingRefusals {
		requireSample(t, samples, "synevyr_billing_refusals_total", formatUint(value),
			label("reason", key.Reason), label("user", key.User))
	}
	for kind, value := range snapshot.BillingMismatches {
		requireSample(t, samples, "synevyr_billing_mismatches_total", formatUint(value), label("kind", kind))
	}

	for key, value := range snapshot.TerminationVerdicts {
		requireSample(t, samples, "synevyr_termination_verdicts_total", formatUint(value),
			label("connector", key.Connector), label("outcome", key.Outcome))
	}
	for key, value := range snapshot.TerminationBypasses {
		requireSample(t, samples, "synevyr_termination_gate_bypass_total", formatUint(value),
			label("connector", key.Connector), label("source", key.Source))
	}
	for key, value := range snapshot.TerminationDeliveries {
		requireSample(t, samples, "synevyr_termination_delivery_total", formatUint(value),
			label("connector", key.Connector), label("outcome", key.Outcome))
	}
	for connector, census := range snapshot.TerminationSpool {
		requireSample(t, samples, "synevyr_termination_spool_rows",
			strconv.FormatInt(census.Rows, 10), label("connector", connector))
		requireSample(t, samples, "synevyr_termination_dead_letter_depth",
			strconv.FormatInt(census.DeadLettered, 10), label("connector", connector))
		requireSample(t, samples, "synevyr_termination_receipts_overdue",
			strconv.FormatInt(census.ReceiptsOverdue, 10), label("connector", connector))
	}

	ready := "0"
	if snapshot.GatewayReady {
		ready = "1"
	}
	requireSample(t, samples, "synevyr_gateway_ready", ready)
	requireSample(t, samples, "synevyr_gateway_health", "1", label("status", snapshot.GatewayHealth))
}

// TestSnapshotCoversEveryRenderedFamily is the other half of the invariant:
// agreement above only checks the families the snapshot knows about, so a
// family added to the exposition alone would pass it unnoticed.
func TestSnapshotCoversEveryRenderedFamily(t *testing.T) {
	clock := time.Date(2026, 7, 31, 15, 43, 27, 0, time.UTC)
	registry := seededRegistry(&clock)

	covered := map[string]bool{
		"synevyr_submit_total":                     true,
		"synevyr_submit_round_trip_seconds_bucket": true,
		"synevyr_submit_round_trip_seconds_sum":    true,
		"synevyr_submit_round_trip_seconds_count":  true,
		"synevyr_route_matches_total":              true,
		"synevyr_route_last_match_seconds":         true,
		"synevyr_dlr_total":                        true,
		"synevyr_mo_total":                         true,
		"synevyr_connector_bound":                  true,
		"synevyr_connector_state":                  true,
		"synevyr_connector_uptime_seconds":         true,
		"synevyr_queue_depth":                      true,
		"synevyr_throughput_rejections_total":      true,
		"synevyr_interceptor_errors_total":         true,
		"synevyr_billing_charges_total":            true,
		"synevyr_billing_refusals_total":           true,
		"synevyr_billing_mismatches_total":         true,
		"synevyr_termination_verdicts_total":       true,
		"synevyr_termination_gate_bypass_total":    true,
		"synevyr_termination_delivery_total":       true,
		"synevyr_termination_spool_rows":           true,
		"synevyr_termination_dead_letter_depth":    true,
		"synevyr_termination_receipts_overdue":     true,
		"synevyr_gateway_ready":                    true,
		"synevyr_gateway_health":                   true,
	}
	for key := range parseExposition(t, registry.RenderPrometheus()) {
		if !covered[key.name] {
			t.Errorf("%s is rendered but Snapshot does not expose it — add it to Snapshot and to this list", key.name)
		}
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	clock := time.Date(2026, 7, 31, 15, 43, 27, 0, time.UTC)
	registry := seededRegistry(&clock)

	snapshot := registry.Snapshot()
	before := snapshot.QueueDepths["submit.sm.smsc-primary"]
	registry.SetQueueDepth("submit.sm.smsc-primary", 99999)
	registry.RecordSubmit("smsc-primary", SubmitSuccess, "ESME_ROK", time.Millisecond)

	if got := snapshot.QueueDepths["submit.sm.smsc-primary"]; got != before {
		t.Errorf("mutating the registry changed an existing snapshot: depth %d became %d", before, got)
	}
	if got := snapshot.SubmitLatency["smsc-primary"].Count; got != 2 {
		t.Errorf("snapshot latency count = %d, want the 2 observations taken before the later submit", got)
	}
}

func TestNilRegistrySnapshotIsEmptyNotNil(t *testing.T) {
	var registry *PrometheusRegistry
	snapshot := registry.Snapshot()

	// Ranging over a nil map is legal, but indexing helpers and JSON encoding
	// both behave differently for nil, so the maps must exist.
	if snapshot.Submits == nil || snapshot.Connectors == nil || snapshot.TerminationSpool == nil {
		t.Fatal("nil registry produced nil maps; callers would encode JSON nulls instead of empty objects")
	}
	if len(snapshot.ConnectorNames()) != 0 {
		t.Error("nil registry reported connectors")
	}
	if snapshot.GatewayReady {
		t.Error("nil registry reported the gateway ready")
	}
}

func TestLatencyQuantile(t *testing.T) {
	observation := LatencyObservation{
		Buckets: []HistogramBucket{
			{UpperBound: 0.005, Count: 0},
			{UpperBound: 0.01, Count: 0},
			{UpperBound: 0.025, Count: 0},
			{UpperBound: 0.05, Count: 5},
			{UpperBound: 0.1, Count: 10},
			{UpperBound: 0.25, Count: 10},
		},
		Count: 10,
		Sum:   0.6,
	}

	value, ok := observation.Quantile(0.95)
	if !ok {
		t.Fatal("Quantile reported no data for a populated histogram")
	}
	// p95 falls in the (0.05, 0.1] bucket, which holds observations 6..10.
	if value <= 0.05 || value > 0.1 {
		t.Errorf("p95 = %v, want a value inside the (0.05, 0.1] bucket", value)
	}

	if _, ok := (LatencyObservation{}).Quantile(0.95); ok {
		t.Error("an empty histogram must report no data rather than a zero latency")
	}
}

func TestSubmitsByConnectorFoldsAcrossStatuses(t *testing.T) {
	clock := time.Date(2026, 7, 31, 15, 43, 27, 0, time.UTC)
	registry := seededRegistry(&clock)
	registry.RecordSubmit("smsc-primary", SubmitFailure, "ESME_RSYSERR", time.Second)

	snapshot := registry.Snapshot()
	if got := snapshot.SubmitsByConnector("smsc-primary", SubmitFailure); got != 2 {
		t.Errorf("failures for smsc-primary = %d, want 2 folded across ESME_RTHROTTLED and ESME_RSYSERR", got)
	}
	if got := snapshot.SubmitsByConnector("smsc-primary", SubmitSuccess); got != 1 {
		t.Errorf("successes for smsc-primary = %d, want 1", got)
	}
	if got := snapshot.SubmitsByConnector("absent", SubmitSuccess); got != 0 {
		t.Errorf("unknown connector reported %d submits", got)
	}
}

func TestRouteMatchAccounting(t *testing.T) {
	clock := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	registry := seededRegistry(&clock)
	snapshot := registry.Snapshot()

	// A pool route's decisions total across the connectors it chose.
	if got := snapshot.MatchesForRoute("mt:10"); got != 2 {
		t.Errorf("mt:10 matches = %d, want 2 across two connectors", got)
	}
	if got := snapshot.MatchesForRoute("mt:0"); got != 1 {
		t.Errorf("mt:0 matches = %d, want 1", got)
	}

	// A route that has never matched is absent, not zero-stamped: "never fired"
	// is a different finding from "fired, but not lately".
	if _, ok := snapshot.RouteLastMatch["mt:999"]; ok {
		t.Error("a route that never matched carries a last-match stamp")
	}
	if got := snapshot.RouteLastMatch["mt:10"]; !got.Equal(clock) {
		t.Errorf("last match = %s, want the registry clock %s", got, clock)
	}
}
