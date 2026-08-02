package stats

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	SubmitAttempt = "attempt"
	SubmitSuccess = "success"
	SubmitFailure = "failure"

	DLROutcomeDelivered          = "delivered"
	DLROutcomeFailed             = "failed"
	DLROutcomeCorrelationFailure = "correlation_failure"

	MOReceived = "received"
	MORouted   = "routed"
	MODropped  = "dropped"

	// Termination verdict outcomes. The label is the lowercased SMPP receipt
	// status the partner was told, so a status this build does not yet produce
	// still lands under its own name instead of an "other" bucket.
	TerminationVerdictDelivered = "delivrd"
	TerminationVerdictRejected  = "rejectd"

	// Termination delivery lifecycle events, mirroring the submit outcomes
	// above: an attempt precedes exactly one of the three terminal outcomes.
	TerminationDeliveryAttempt    = "attempt"
	TerminationDeliverySuccess    = "success"
	TerminationDeliveryFailure    = "failure"
	TerminationDeliveryDeadLetter = "dead_letter"
)

var submitLatencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type submitLabels struct {
	connector string
	outcome   string
	status    string
}

type dlrLabels struct {
	level      string
	finalState string
	outcome    string
}

type moLabels struct {
	connector string
	outcome   string
}

type billingChargeLabels struct {
	user     string
	currency string
}

type billingRefusalLabels struct {
	user   string
	reason string
}

type submitHistogram struct {
	buckets []uint64
	count   uint64
	sum     float64
}

type connectorObservation struct {
	state      string
	boundSince time.Time
}

// routeMatchLabels identifies one routing decision: the route that won and the
// connector it chose. Both are needed — a pool route sends to different
// connectors over time, and "which route fired" and "where did it send" are
// separate operational questions.
type routeMatchLabels struct {
	route     string
	connector string
}

type terminationVerdictLabels struct {
	connector string
	outcome   string
}

type terminationBypassLabels struct {
	connector string
	source    string
}

type terminationDeliveryLabels struct {
	connector string
	outcome   string
}

// TerminationSpoolCensus is one termination connector's spool population at a
// single observation.
//
// The three numbers are written together, from one query, because they are read
// together during an incident and a dashboard that mixes two scrape generations
// would report a dead-letter depth larger than the spool it lives in.
type TerminationSpoolCensus struct {
	// Rows is every spool row for the connector, whatever its delivery state.
	// It is the bound on how much a downstream outage can accumulate.
	Rows int64
	// DeadLettered is the rows that exhausted their delivery budget. It should
	// be zero: anything else is messages the downstream application never took,
	// with the retention clock already running against them.
	DeadLettered int64
	// ReceiptsOverdue is the rows whose receipt was due in the past and has not
	// been sent. It should be zero: anything else is partners waiting.
	ReceiptsOverdue int64
}

// PrometheusRegistry holds the production-oriented metrics separately from
// the byte-exact legacy registries in stats.go. Its zero value is not used;
// construct one with NewPrometheusRegistry.
type PrometheusRegistry struct {
	mu  sync.RWMutex
	now func() time.Time

	submits              map[submitLabels]uint64
	submitLatency        map[string]*submitHistogram
	dlrs                 map[dlrLabels]uint64
	mos                  map[moLabels]uint64
	connectors           map[string]connectorObservation
	queueDepths          map[string]int64
	throughputRejections map[string]uint64
	interceptorErrors    map[string]uint64
	billingCharges       map[billingChargeLabels]float64
	billingRefusals      map[billingRefusalLabels]uint64
	billingMismatches    map[string]uint64
	gatewayHealth        string

	routeMatches   map[routeMatchLabels]uint64
	routeLastMatch map[string]time.Time

	terminationVerdicts   map[terminationVerdictLabels]uint64
	terminationBypasses   map[terminationBypassLabels]uint64
	terminationDeliveries map[terminationDeliveryLabels]uint64
	terminationSpool      map[string]TerminationSpoolCensus
}

var defaultPrometheus = NewPrometheusRegistry()

// DefaultPrometheus returns the process-wide registry used by runtime
// instrumentation and the admin metrics handler.
func DefaultPrometheus() *PrometheusRegistry {
	return defaultPrometheus
}

func NewPrometheusRegistry() *PrometheusRegistry {
	return newPrometheusRegistry(time.Now)
}

func newPrometheusRegistry(now func() time.Time) *PrometheusRegistry {
	if now == nil {
		now = time.Now
	}
	return &PrometheusRegistry{
		now:                  now,
		submits:              make(map[submitLabels]uint64),
		submitLatency:        make(map[string]*submitHistogram),
		dlrs:                 make(map[dlrLabels]uint64),
		mos:                  make(map[moLabels]uint64),
		connectors:           make(map[string]connectorObservation),
		queueDepths:          make(map[string]int64),
		throughputRejections: make(map[string]uint64),
		interceptorErrors:    make(map[string]uint64),
		billingCharges:       make(map[billingChargeLabels]float64),
		billingRefusals:      make(map[billingRefusalLabels]uint64),
		billingMismatches:    make(map[string]uint64),
		gatewayHealth:        "starting",

		routeMatches:   make(map[routeMatchLabels]uint64),
		routeLastMatch: make(map[string]time.Time),

		terminationVerdicts:   make(map[terminationVerdictLabels]uint64),
		terminationBypasses:   make(map[terminationBypassLabels]uint64),
		terminationDeliveries: make(map[terminationDeliveryLabels]uint64),
		terminationSpool:      make(map[string]TerminationSpoolCensus),
	}
}

// RecordSubmit records an attempt, success, or failure. A round-trip
// observation is added for success and failure outcomes, never for the
// attempt event that precedes them.
func (registry *PrometheusRegistry) RecordSubmit(connector, outcome, status string, elapsed time.Duration) {
	if registry == nil {
		return
	}
	labels := submitLabels{
		connector: labelValue(connector),
		outcome:   labelValue(outcome),
		status:    labelValue(status),
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.submits[labels]++
	if labels.outcome == SubmitAttempt {
		return
	}
	seconds := elapsed.Seconds()
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	histogram := registry.submitLatency[labels.connector]
	if histogram == nil {
		histogram = &submitHistogram{buckets: make([]uint64, len(submitLatencyBuckets))}
		registry.submitLatency[labels.connector] = histogram
	}
	for index, upperBound := range submitLatencyBuckets {
		if seconds <= upperBound {
			histogram.buckets[index]++
		}
	}
	histogram.count++
	histogram.sum += seconds
}

// RecordRouteMatch counts one routing decision and stamps when it happened.
//
// It is called from the submit path only, never from Table.Select itself: the
// rate-quote endpoint selects a route to price a message that is not being
// sent, and counting those would report traffic on a route nothing traverses.
//
// The counter lives here rather than on the route, so an operator editing a
// route does not zero its history — which matches how the routing table already
// treats an order as a stable identity that survives replacement.
func (registry *PrometheusRegistry) RecordRouteMatch(route, connector string) {
	if registry == nil {
		return
	}
	labels := routeMatchLabels{route: labelValue(route), connector: labelValue(connector)}
	registry.mu.Lock()
	registry.routeMatches[labels]++
	registry.routeLastMatch[labels.route] = registry.now()
	registry.mu.Unlock()
}

func (registry *PrometheusRegistry) RecordDLR(level int, finalState, outcome string) {
	if registry == nil {
		return
	}
	labels := dlrLabels{
		level:      strconv.Itoa(level),
		finalState: labelValue(finalState),
		outcome:    labelValue(outcome),
	}
	registry.mu.Lock()
	registry.dlrs[labels]++
	registry.mu.Unlock()
}

func (registry *PrometheusRegistry) RecordMO(connector, outcome string) {
	if registry == nil {
		return
	}
	labels := moLabels{connector: labelValue(connector), outcome: labelValue(outcome)}
	registry.mu.Lock()
	registry.mos[labels]++
	registry.mu.Unlock()
}

// SetConnectorState updates the one-hot state and resets uptime only when a
// connector enters BOUND from another state.
func (registry *PrometheusRegistry) SetConnectorState(connector, state string) {
	if registry == nil {
		return
	}
	connector = labelValue(connector)
	state = labelValue(state)
	registry.mu.Lock()
	observation := registry.connectors[connector]
	if state == "BOUND" && observation.state != "BOUND" {
		observation.boundSince = registry.now()
	} else if state != "BOUND" {
		observation.boundSince = time.Time{}
	}
	observation.state = state
	registry.connectors[connector] = observation
	registry.mu.Unlock()
}

func (registry *PrometheusRegistry) SetQueueDepth(queue string, depth int64) {
	if registry == nil {
		return
	}
	if depth < 0 {
		depth = 0
	}
	registry.mu.Lock()
	registry.queueDepths[labelValue(queue)] = depth
	registry.mu.Unlock()
}

func (registry *PrometheusRegistry) RecordThroughputRejection(user string) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	registry.throughputRejections[labelValue(user)]++
	registry.mu.Unlock()
}

func (registry *PrometheusRegistry) RecordInterceptorError(direction string) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	registry.interceptorErrors[labelValue(direction)]++
	registry.mu.Unlock()
}

// RecordBillingCharge adds charged currency units. Invalid or negative
// amounts are ignored so the Prometheus counter remains monotonic.
func (registry *PrometheusRegistry) RecordBillingCharge(user, currency string, amount float64) {
	if registry == nil || amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return
	}
	labels := billingChargeLabels{user: labelValue(user), currency: labelValue(currency)}
	registry.mu.Lock()
	registry.billingCharges[labels] += amount
	registry.mu.Unlock()
}

func (registry *PrometheusRegistry) RecordBillingRefusal(user, reason string) {
	if registry == nil {
		return
	}
	labels := billingRefusalLabels{user: labelValue(user), reason: labelValue(reason)}
	registry.mu.Lock()
	registry.billingRefusals[labels]++
	registry.mu.Unlock()
}

func (registry *PrometheusRegistry) RecordBillingMismatch(kind string) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	registry.billingMismatches[labelValue(kind)]++
	registry.mu.Unlock()
}

// RecordTerminationVerdict counts one receipt decision on a termination
// connector. outcome is the lowercased SMPP status the partner was told.
func (registry *PrometheusRegistry) RecordTerminationVerdict(connector, outcome string) {
	if registry == nil {
		return
	}
	labels := terminationVerdictLabels{
		connector: labelValue(connector),
		outcome:   labelValue(strings.ToLower(strings.TrimSpace(outcome))),
	}
	registry.mu.Lock()
	registry.terminationVerdicts[labels]++
	registry.mu.Unlock()
}

// RecordTerminationGateBypass counts one message accepted without a usable
// verdict, because the gate could not be reached.
//
// This is the highest-value counter in the termination set and the one an alarm
// should be built on first. The gate fails open on purpose — rejecting real
// traffic during an infrastructure blip is worse — so a Redis outage is silent
// in every other signal: the partner is told DELIVRD, the message is spooled and
// delivered, and nothing looks wrong. It must be zero in normal operation.
func (registry *PrometheusRegistry) RecordTerminationGateBypass(connector, source string) {
	if registry == nil {
		return
	}
	labels := terminationBypassLabels{connector: labelValue(connector), source: labelValue(source)}
	registry.mu.Lock()
	registry.terminationBypasses[labels]++
	registry.mu.Unlock()
}

// RecordTerminationDelivery counts one downstream delivery lifecycle event.
func (registry *PrometheusRegistry) RecordTerminationDelivery(connector, outcome string) {
	if registry == nil {
		return
	}
	labels := terminationDeliveryLabels{connector: labelValue(connector), outcome: labelValue(outcome)}
	registry.mu.Lock()
	registry.terminationDeliveries[labels]++
	registry.mu.Unlock()
}

// SetTerminationSpool replaces one connector's spool census.
//
// It replaces rather than accumulates: these are populations, not events, and
// the observer re-counts them from the database on every pass.
func (registry *PrometheusRegistry) SetTerminationSpool(connector string, census TerminationSpoolCensus) {
	if registry == nil {
		return
	}
	if census.Rows < 0 {
		census.Rows = 0
	}
	if census.DeadLettered < 0 {
		census.DeadLettered = 0
	}
	if census.ReceiptsOverdue < 0 {
		census.ReceiptsOverdue = 0
	}
	registry.mu.Lock()
	registry.terminationSpool[labelValue(connector)] = census
	registry.mu.Unlock()
}

func (registry *PrometheusRegistry) SetGatewayHealth(status string) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	registry.gatewayHealth = labelValue(status)
	registry.mu.Unlock()
}

// Handler serves the modern metrics format. It is intended for the private
// admin listener; the public handler continues to own the legacy /metrics.
func (registry *PrometheusRegistry) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		_, _ = writer.Write(registry.RenderPrometheus())
	})
}

// RenderPrometheus emits deterministic Prometheus text exposition. This
// deliberately improves on Jasmin's minimal process counters with operational
// labels and the standard HELP-before-TYPE convention; the customer-visible
// legacy Render function remains unchanged.
func (registry *PrometheusRegistry) RenderPrometheus() []byte {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	var builder strings.Builder
	writeMetricHeader(&builder, "synevyr_submit_total",
		"Submit lifecycle events by connector, outcome, and SMPP command status.", "counter")
	submitKeys := sortedKeys(registry.submits, func(labels submitLabels) string {
		return labels.connector + "\x00" + labels.outcome + "\x00" + labels.status
	})
	for _, labels := range submitKeys {
		writeSample(&builder, "synevyr_submit_total", []prometheusLabel{
			{"connector", labels.connector}, {"outcome", labels.outcome}, {"status", labels.status},
		}, formatUint(registry.submits[labels]))
	}

	writeMetricHeader(&builder, "synevyr_submit_round_trip_seconds",
		"Time from submit_sm write to submit_sm_resp by connector.", "histogram")
	for _, connector := range sortedStringKeys(registry.submitLatency) {
		histogram := registry.submitLatency[connector]
		for index, upperBound := range submitLatencyBuckets {
			writeSample(&builder, "synevyr_submit_round_trip_seconds_bucket", []prometheusLabel{
				{"connector", connector}, {"le", formatFloat(upperBound)},
			}, formatUint(histogram.buckets[index]))
		}
		writeSample(&builder, "synevyr_submit_round_trip_seconds_bucket", []prometheusLabel{
			{"connector", connector}, {"le", "+Inf"},
		}, formatUint(histogram.count))
		writeSample(&builder, "synevyr_submit_round_trip_seconds_sum",
			[]prometheusLabel{{"connector", connector}}, formatFloat(histogram.sum))
		writeSample(&builder, "synevyr_submit_round_trip_seconds_count",
			[]prometheusLabel{{"connector", connector}}, formatUint(histogram.count))
	}

	writeMetricHeader(&builder, "synevyr_route_matches_total",
		"MT routing decisions by route and the connector it selected.", "counter")
	routeKeys := sortedKeys(registry.routeMatches, func(labels routeMatchLabels) string {
		return labels.connector + "\x00" + labels.route
	})
	for _, labels := range routeKeys {
		writeSample(&builder, "synevyr_route_matches_total", []prometheusLabel{
			{"connector", labels.connector}, {"route", labels.route},
		}, formatUint(registry.routeMatches[labels]))
	}

	writeMetricHeader(&builder, "synevyr_route_last_match_seconds",
		"Unix time of the most recent match for a route; absent until it first matches.", "gauge")
	for _, route := range sortedStringKeys(registry.routeLastMatch) {
		writeSample(&builder, "synevyr_route_last_match_seconds",
			[]prometheusLabel{{"route", route}},
			formatFloat(float64(registry.routeLastMatch[route].Unix())))
	}

	writeMetricHeader(&builder, "synevyr_dlr_total",
		"Delivery receipt outcomes by requested level and final delivery state.", "counter")
	dlrKeys := sortedKeys(registry.dlrs, func(labels dlrLabels) string {
		return labels.finalState + "\x00" + labels.level + "\x00" + labels.outcome
	})
	for _, labels := range dlrKeys {
		writeSample(&builder, "synevyr_dlr_total", []prometheusLabel{
			{"final_state", labels.finalState}, {"level", labels.level}, {"outcome", labels.outcome},
		}, formatUint(registry.dlrs[labels]))
	}

	writeMetricHeader(&builder, "synevyr_mo_total",
		"Mobile-originated message lifecycle events by connector and outcome.", "counter")
	moKeys := sortedKeys(registry.mos, func(labels moLabels) string {
		return labels.connector + "\x00" + labels.outcome
	})
	for _, labels := range moKeys {
		writeSample(&builder, "synevyr_mo_total", []prometheusLabel{
			{"connector", labels.connector}, {"outcome", labels.outcome},
		}, formatUint(registry.mos[labels]))
	}

	writeMetricHeader(&builder, "synevyr_connector_bound",
		"Whether an SMPP client connector is currently bound.", "gauge")
	for _, connector := range sortedStringKeys(registry.connectors) {
		bound := "0"
		if registry.connectors[connector].state == "BOUND" {
			bound = "1"
		}
		writeSample(&builder, "synevyr_connector_bound",
			[]prometheusLabel{{"connector", connector}}, bound)
	}

	writeMetricHeader(&builder, "synevyr_connector_state",
		"Current one-hot SMPP client connector state.", "gauge")
	for _, connector := range sortedStringKeys(registry.connectors) {
		writeSample(&builder, "synevyr_connector_state", []prometheusLabel{
			{"connector", connector}, {"state", registry.connectors[connector].state},
		}, "1")
	}

	writeMetricHeader(&builder, "synevyr_connector_uptime_seconds",
		"Seconds since the SMPP client connector most recently entered BOUND.", "gauge")
	now := registry.now()
	for _, connector := range sortedStringKeys(registry.connectors) {
		observation := registry.connectors[connector]
		uptime := 0.0
		if observation.state == "BOUND" && !observation.boundSince.IsZero() {
			uptime = now.Sub(observation.boundSince).Seconds()
			if uptime < 0 {
				uptime = 0
			}
		}
		writeSample(&builder, "synevyr_connector_uptime_seconds",
			[]prometheusLabel{{"connector", connector}}, formatFloat(uptime))
	}

	// The observer reads the broker's queue.declare-ok message count, which is
	// READY messages only: a delivery already handed to a consumer and not yet
	// acknowledged is not in this number. A connector that is consuming but
	// never acking therefore shows a small depth, not a growing one — check
	// consumer counts as well before concluding a queue is draining.
	writeMetricHeader(&builder, "synevyr_queue_depth",
		"Messages ready in a gateway queue at the last observation; unacknowledged deliveries are not counted.", "gauge")
	for _, queue := range sortedStringKeys(registry.queueDepths) {
		writeSample(&builder, "synevyr_queue_depth",
			[]prometheusLabel{{"queue", queue}}, strconv.FormatInt(registry.queueDepths[queue], 10))
	}

	writeMetricHeader(&builder, "synevyr_throughput_rejections_total",
		"Submits rejected by the per-user throughput limit.", "counter")
	for _, user := range sortedStringKeys(registry.throughputRejections) {
		writeSample(&builder, "synevyr_throughput_rejections_total",
			[]prometheusLabel{{"user", user}}, formatUint(registry.throughputRejections[user]))
	}

	writeMetricHeader(&builder, "synevyr_interceptor_errors_total",
		"MT and MO interceptor execution errors.", "counter")
	for _, direction := range sortedStringKeys(registry.interceptorErrors) {
		writeSample(&builder, "synevyr_interceptor_errors_total",
			[]prometheusLabel{{"direction", direction}}, formatUint(registry.interceptorErrors[direction]))
	}

	writeMetricHeader(&builder, "synevyr_billing_charges_total",
		"Currency units charged by user and settlement currency.", "counter")
	chargeKeys := sortedKeys(registry.billingCharges, func(labels billingChargeLabels) string {
		return labels.currency + "\x00" + labels.user
	})
	for _, labels := range chargeKeys {
		writeSample(&builder, "synevyr_billing_charges_total", []prometheusLabel{
			{"currency", labels.currency}, {"user", labels.user},
		}, formatFloat(registry.billingCharges[labels]))
	}

	writeMetricHeader(&builder, "synevyr_billing_refusals_total",
		"Submits refused by billing controls.", "counter")
	refusalKeys := sortedKeys(registry.billingRefusals, func(labels billingRefusalLabels) string {
		return labels.reason + "\x00" + labels.user
	})
	for _, labels := range refusalKeys {
		writeSample(&builder, "synevyr_billing_refusals_total", []prometheusLabel{
			{"reason", labels.reason}, {"user", labels.user},
		}, formatUint(registry.billingRefusals[labels]))
	}

	writeMetricHeader(&builder, "synevyr_billing_mismatches_total",
		"Commercial ledger reconciliation mismatches by kind.", "counter")
	for _, kind := range sortedStringKeys(registry.billingMismatches) {
		writeSample(&builder, "synevyr_billing_mismatches_total",
			[]prometheusLabel{{"kind", kind}}, formatUint(registry.billingMismatches[kind]))
	}

	writeMetricHeader(&builder, "synevyr_termination_verdicts_total",
		"Receipt decisions made by MT termination connectors, by connector and the status the partner was told.", "counter")
	verdictKeys := sortedKeys(registry.terminationVerdicts, func(labels terminationVerdictLabels) string {
		return labels.connector + "\x00" + labels.outcome
	})
	for _, labels := range verdictKeys {
		writeSample(&builder, "synevyr_termination_verdicts_total", []prometheusLabel{
			{"connector", labels.connector}, {"outcome", labels.outcome},
		}, formatUint(registry.terminationVerdicts[labels]))
	}

	writeMetricHeader(&builder, "synevyr_termination_gate_bypass_total",
		"Messages accepted without a verdict because the activation gate was unreachable. Expected to be zero.", "counter")
	bypassKeys := sortedKeys(registry.terminationBypasses, func(labels terminationBypassLabels) string {
		return labels.connector + "\x00" + labels.source
	})
	for _, labels := range bypassKeys {
		writeSample(&builder, "synevyr_termination_gate_bypass_total", []prometheusLabel{
			{"connector", labels.connector}, {"source", labels.source},
		}, formatUint(registry.terminationBypasses[labels]))
	}

	writeMetricHeader(&builder, "synevyr_termination_delivery_total",
		"Downstream delivery lifecycle events by connector and outcome.", "counter")
	deliveryKeys := sortedKeys(registry.terminationDeliveries, func(labels terminationDeliveryLabels) string {
		return labels.connector + "\x00" + labels.outcome
	})
	for _, labels := range deliveryKeys {
		writeSample(&builder, "synevyr_termination_delivery_total", []prometheusLabel{
			{"connector", labels.connector}, {"outcome", labels.outcome},
		}, formatUint(registry.terminationDeliveries[labels]))
	}

	terminationConnectors := sortedStringKeys(registry.terminationSpool)
	writeMetricHeader(&builder, "synevyr_termination_spool_rows",
		"Message spool rows currently held for a termination connector.", "gauge")
	for _, connector := range terminationConnectors {
		writeSample(&builder, "synevyr_termination_spool_rows",
			[]prometheusLabel{{"connector", connector}},
			strconv.FormatInt(registry.terminationSpool[connector].Rows, 10))
	}

	writeMetricHeader(&builder, "synevyr_termination_dead_letter_depth",
		"Spool rows that exhausted their delivery attempts and are awaiting replay.", "gauge")
	for _, connector := range terminationConnectors {
		writeSample(&builder, "synevyr_termination_dead_letter_depth",
			[]prometheusLabel{{"connector", connector}},
			strconv.FormatInt(registry.terminationSpool[connector].DeadLettered, 10))
	}

	writeMetricHeader(&builder, "synevyr_termination_receipts_overdue",
		"Spool rows whose receipt was due in the past and has not been sent.", "gauge")
	for _, connector := range terminationConnectors {
		writeSample(&builder, "synevyr_termination_receipts_overdue",
			[]prometheusLabel{{"connector", connector}},
			strconv.FormatInt(registry.terminationSpool[connector].ReceiptsOverdue, 10))
	}

	writeMetricHeader(&builder, "synevyr_gateway_ready",
		"Whether every gateway readiness dependency is currently usable.", "gauge")
	ready := "0"
	if registry.gatewayHealth == "ok" {
		ready = "1"
	}
	writeSample(&builder, "synevyr_gateway_ready", nil, ready)

	writeMetricHeader(&builder, "synevyr_gateway_health",
		"Current one-hot gateway health state.", "gauge")
	writeSample(&builder, "synevyr_gateway_health",
		[]prometheusLabel{{"status", registry.gatewayHealth}}, "1")

	return []byte(builder.String())
}

type prometheusLabel struct {
	name  string
	value string
}

func writeMetricHeader(builder *strings.Builder, name, help, metricType string) {
	builder.WriteString("# HELP ")
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(help)
	builder.WriteByte('\n')
	builder.WriteString("# TYPE ")
	builder.WriteString(name)
	builder.WriteByte(' ')
	builder.WriteString(metricType)
	builder.WriteByte('\n')
}

func writeSample(builder *strings.Builder, name string, labels []prometheusLabel, value string) {
	builder.WriteString(name)
	if len(labels) > 0 {
		builder.WriteByte('{')
		for index, label := range labels {
			if index > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(label.name)
			builder.WriteString(`="`)
			builder.WriteString(escapeLabelValue(label.value))
			builder.WriteByte('"')
		}
		builder.WriteByte('}')
	}
	builder.WriteByte(' ')
	builder.WriteString(value)
	builder.WriteByte('\n')
}

func escapeLabelValue(value string) string {
	replacer := strings.NewReplacer("\\", `\\`, "\n", `\n`, `"`, `\"`)
	return replacer.Replace(value)
}

func labelValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func formatUint(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func sortedStringKeys[Value any](values map[string]Value) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys[Key comparable, Value any](values map[Key]Value, order func(Key) string) []Key {
	keys := make([]Key, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		return order(keys[left]) < order(keys[right])
	})
	return keys
}
