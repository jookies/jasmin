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

	writeMetricHeader(&builder, "synevyr_queue_depth",
		"Ready and unacknowledged messages currently observed in a gateway queue.", "gauge")
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
