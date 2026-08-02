package stats

import (
	"sort"
	"time"
)

// This file is the in-process read path over PrometheusRegistry.
//
// RenderPrometheus already exposes every series, but only as exposition text.
// Surfaces inside this process — the console's topology map above all — need the
// same numbers as values, and parsing our own text back would make the console's
// correctness depend on a format whose whole point is that scrapers own it.
//
// Snapshot therefore reads the same fields under the same lock. The invariant
// that keeps the two honest is asserted in snapshot_test.go: every value a
// Snapshot reports must appear in the text RenderPrometheus produces from the
// same registry state.

// SubmitKey identifies one submit lifecycle series.
type SubmitKey struct {
	Connector string
	Outcome   string
	Status    string
}

// DLRKey identifies one delivery-receipt series. Level is the string form the
// exposition uses, so an unknown level stays distinguishable from level 0.
type DLRKey struct {
	Level      string
	FinalState string
	Outcome    string
}

// MOKey identifies one mobile-originated series.
type MOKey struct {
	Connector string
	Outcome   string
}

// BillingChargeKey identifies charged units for one user in one currency.
type BillingChargeKey struct {
	User     string
	Currency string
}

// BillingRefusalKey identifies refusals for one user under one reason.
type BillingRefusalKey struct {
	User   string
	Reason string
}

// RouteMatchKey identifies one routing decision series.
type RouteMatchKey struct {
	Route     string
	Connector string
}

// TerminationKey identifies a termination connector series by outcome. It
// serves both the verdict and the delivery families, which share their shape.
type TerminationKey struct {
	Connector string
	Outcome   string
}

// TerminationBypassKey identifies gate bypasses by connector and source.
type TerminationBypassKey struct {
	Connector string
	Source    string
}

// ConnectorObservation is one SMPP client connector's link state at the instant
// of the snapshot.
type ConnectorObservation struct {
	// State is the one-hot state name ("BOUND", "CONNECTING", ...).
	State string
	// Bound is State == "BOUND", precomputed so callers do not re-encode the
	// comparison the exposition already makes.
	Bound bool
	// UptimeSeconds is time since the connector most recently entered BOUND,
	// and is zero whenever it is not bound.
	UptimeSeconds float64
}

// HistogramBucket is one cumulative latency bucket: every observation at or
// below UpperBound seconds.
type HistogramBucket struct {
	UpperBound float64
	Count      uint64
}

// LatencyObservation is one connector's submit round-trip histogram.
type LatencyObservation struct {
	// Buckets are cumulative and ordered by ascending UpperBound, matching the
	// _bucket{le=...} samples.
	Buckets []HistogramBucket
	Count   uint64
	Sum     float64
}

// Quantile estimates the q-th quantile (0 < q < 1) in seconds by linear
// interpolation within the containing bucket, the way histogram_quantile does.
//
// The second result is false when the connector has no observations yet, so a
// caller can render "no data" instead of a zero that reads as "instant". The
// buckets are coarse by design (5 ms to 10 s in eleven steps), so treat the
// result as the bucket-resolution estimate it is, never as a measured latency.
func (observation LatencyObservation) Quantile(q float64) (float64, bool) {
	if observation.Count == 0 || len(observation.Buckets) == 0 {
		return 0, false
	}
	if q <= 0 {
		return 0, true
	}
	if q >= 1 {
		return observation.Buckets[len(observation.Buckets)-1].UpperBound, true
	}

	rank := q * float64(observation.Count)
	previousBound := 0.0
	var previousCount uint64
	for _, bucket := range observation.Buckets {
		if float64(bucket.Count) >= rank {
			span := bucket.UpperBound - previousBound
			within := float64(bucket.Count - previousCount)
			if span <= 0 || within <= 0 {
				return bucket.UpperBound, true
			}
			return previousBound + span*(rank-float64(previousCount))/within, true
		}
		previousBound = bucket.UpperBound
		previousCount = bucket.Count
	}
	// Observations above the last finite bucket exist (Count exceeds it); the
	// largest bound is the most that can be claimed without inventing a tail.
	return observation.Buckets[len(observation.Buckets)-1].UpperBound, true
}

// Snapshot is a consistent copy of every production metric series, taken under
// one lock so two numbers read together cannot come from two different states.
//
// Counters are cumulative since process start (KNOWN_QUIRKS Q-011): they are
// not rates, and a caller wanting a rate must difference two snapshots and
// divide by the elapsed time between their ObservedAt stamps.
type Snapshot struct {
	ObservedAt time.Time

	Submits       map[SubmitKey]uint64
	SubmitLatency map[string]LatencyObservation
	DLRs          map[DLRKey]uint64
	MOs           map[MOKey]uint64

	Connectors  map[string]ConnectorObservation
	QueueDepths map[string]int64

	// RouteMatches counts routing decisions; RouteLastMatch is when each route
	// most recently won. A route absent from RouteLastMatch has never matched,
	// which is a different and more interesting state than "matched zero times
	// recently" — it is a rule nothing has ever exercised.
	RouteMatches   map[RouteMatchKey]uint64
	RouteLastMatch map[string]time.Time

	ThroughputRejections map[string]uint64
	InterceptorErrors    map[string]uint64

	BillingCharges    map[BillingChargeKey]float64
	BillingRefusals   map[BillingRefusalKey]uint64
	BillingMismatches map[string]uint64

	TerminationVerdicts   map[TerminationKey]uint64
	TerminationBypasses   map[TerminationBypassKey]uint64
	TerminationDeliveries map[TerminationKey]uint64
	TerminationSpool      map[string]TerminationSpoolCensus

	// GatewayHealth is the one-hot health status; GatewayReady mirrors the
	// synevyr_gateway_ready gauge (status == "ok").
	GatewayHealth string
	GatewayReady  bool
}

// Snapshot copies every series out of the registry. A nil registry yields a
// zero-valued snapshot with non-nil maps, so callers never branch on nil before
// ranging — a gateway built without metrics renders an empty map, not a panic.
func (registry *PrometheusRegistry) Snapshot() Snapshot {
	snapshot := Snapshot{
		Submits:               map[SubmitKey]uint64{},
		SubmitLatency:         map[string]LatencyObservation{},
		DLRs:                  map[DLRKey]uint64{},
		MOs:                   map[MOKey]uint64{},
		Connectors:            map[string]ConnectorObservation{},
		RouteMatches:          map[RouteMatchKey]uint64{},
		RouteLastMatch:        map[string]time.Time{},
		QueueDepths:           map[string]int64{},
		ThroughputRejections:  map[string]uint64{},
		InterceptorErrors:     map[string]uint64{},
		BillingCharges:        map[BillingChargeKey]float64{},
		BillingRefusals:       map[BillingRefusalKey]uint64{},
		BillingMismatches:     map[string]uint64{},
		TerminationVerdicts:   map[TerminationKey]uint64{},
		TerminationBypasses:   map[TerminationBypassKey]uint64{},
		TerminationDeliveries: map[TerminationKey]uint64{},
		TerminationSpool:      map[string]TerminationSpoolCensus{},
	}
	if registry == nil {
		return snapshot
	}

	registry.mu.RLock()
	defer registry.mu.RUnlock()

	now := registry.now()
	snapshot.ObservedAt = now

	for labels, value := range registry.submits {
		snapshot.Submits[SubmitKey{
			Connector: labels.connector,
			Outcome:   labels.outcome,
			Status:    labels.status,
		}] = value
	}

	for connector, histogram := range registry.submitLatency {
		buckets := make([]HistogramBucket, len(submitLatencyBuckets))
		for index, upperBound := range submitLatencyBuckets {
			buckets[index] = HistogramBucket{UpperBound: upperBound, Count: histogram.buckets[index]}
		}
		snapshot.SubmitLatency[connector] = LatencyObservation{
			Buckets: buckets,
			Count:   histogram.count,
			Sum:     histogram.sum,
		}
	}

	for labels, value := range registry.dlrs {
		snapshot.DLRs[DLRKey{
			Level:      labels.level,
			FinalState: labels.finalState,
			Outcome:    labels.outcome,
		}] = value
	}

	for labels, value := range registry.mos {
		snapshot.MOs[MOKey{Connector: labels.connector, Outcome: labels.outcome}] = value
	}

	for connector, observation := range registry.connectors {
		bound := observation.state == "BOUND"
		uptime := 0.0
		if bound && !observation.boundSince.IsZero() {
			uptime = now.Sub(observation.boundSince).Seconds()
			if uptime < 0 {
				uptime = 0
			}
		}
		snapshot.Connectors[connector] = ConnectorObservation{
			State:         observation.state,
			Bound:         bound,
			UptimeSeconds: uptime,
		}
	}

	for labels, value := range registry.routeMatches {
		snapshot.RouteMatches[RouteMatchKey{Route: labels.route, Connector: labels.connector}] = value
	}
	for route, at := range registry.routeLastMatch {
		snapshot.RouteLastMatch[route] = at
	}

	for queue, depth := range registry.queueDepths {
		snapshot.QueueDepths[queue] = depth
	}
	for user, value := range registry.throughputRejections {
		snapshot.ThroughputRejections[user] = value
	}
	for direction, value := range registry.interceptorErrors {
		snapshot.InterceptorErrors[direction] = value
	}

	for labels, value := range registry.billingCharges {
		snapshot.BillingCharges[BillingChargeKey{User: labels.user, Currency: labels.currency}] = value
	}
	for labels, value := range registry.billingRefusals {
		snapshot.BillingRefusals[BillingRefusalKey{User: labels.user, Reason: labels.reason}] = value
	}
	for kind, value := range registry.billingMismatches {
		snapshot.BillingMismatches[kind] = value
	}

	for labels, value := range registry.terminationVerdicts {
		snapshot.TerminationVerdicts[TerminationKey{Connector: labels.connector, Outcome: labels.outcome}] = value
	}
	for labels, value := range registry.terminationBypasses {
		snapshot.TerminationBypasses[TerminationBypassKey{Connector: labels.connector, Source: labels.source}] = value
	}
	for labels, value := range registry.terminationDeliveries {
		snapshot.TerminationDeliveries[TerminationKey{Connector: labels.connector, Outcome: labels.outcome}] = value
	}
	for connector, census := range registry.terminationSpool {
		snapshot.TerminationSpool[connector] = census
	}

	snapshot.GatewayHealth = registry.gatewayHealth
	snapshot.GatewayReady = registry.gatewayHealth == "ok"

	return snapshot
}

// SubmitsByConnector totals every submit series for one connector and outcome,
// summing across SMPP command statuses. The topology map needs "how many
// succeeded", not the per-status breakdown, and every caller doing that fold by
// hand would eventually disagree about whether to include the attempt event.
func (snapshot Snapshot) SubmitsByConnector(connector, outcome string) uint64 {
	var total uint64
	for key, value := range snapshot.Submits {
		if key.Connector == connector && key.Outcome == outcome {
			total += value
		}
	}
	return total
}

// Connectors that reported any state, sorted, so callers iterate deterministically.
func (snapshot Snapshot) ConnectorNames() []string {
	names := make([]string, 0, len(snapshot.Connectors))
	for name := range snapshot.Connectors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MatchesForRoute totals a route's decisions across every connector it has
// selected. A pool route spreads across candidates, and "did this rule fire"
// is a question about the rule, not about any one destination.
func (snapshot Snapshot) MatchesForRoute(route string) uint64 {
	var total uint64
	for key, value := range snapshot.RouteMatches {
		if key.Route == route {
			total += value
		}
	}
	return total
}
