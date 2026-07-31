package stats

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrometheusRenderOperationalSurface(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	registry := newPrometheusRegistry(func() time.Time { return now })

	registry.RecordSubmit("smsc-primary", SubmitAttempt, "pending", 0)
	registry.RecordSubmit("smsc-primary", SubmitSuccess, "ESME_ROK", 30*time.Millisecond)
	registry.RecordSubmit("smsc-backup", SubmitFailure, "ESME_RTHROTTLED", 2*time.Second)
	registry.RecordDLR(3, "DELIVRD", DLROutcomeDelivered)
	registry.RecordDLR(2, "UNKNOWN", DLROutcomeCorrelationFailure)
	registry.RecordMO("smsc-primary", MOReceived)
	registry.RecordMO("smsc-primary", MORouted)
	registry.RecordMO("smsc-backup", MODropped)
	registry.SetConnectorState("smsc-primary", "BOUND")
	registry.SetConnectorState("smsc-backup", "DISCONNECTED")
	registry.SetQueueDepth("submit.sm.smsc-primary", 42)
	registry.RecordThroughputRejection("alice")
	registry.RecordInterceptorError("mt")
	registry.RecordBillingCharge("alice", "USD", 0.25)
	registry.RecordBillingRefusal("bob", "insufficient_balance")
	registry.RecordBillingMismatch("late_charge")
	registry.SetGatewayHealth("degraded")

	now = now.Add(90 * time.Second)
	out := string(registry.RenderPrometheus())
	for _, want := range []string{
		"# HELP synevyr_submit_total Submit lifecycle events by connector, outcome, and SMPP command status.",
		"# TYPE synevyr_submit_total counter",
		`synevyr_submit_total{connector="smsc-primary",outcome="attempt",status="pending"} 1`,
		`synevyr_submit_total{connector="smsc-primary",outcome="success",status="ESME_ROK"} 1`,
		`synevyr_submit_total{connector="smsc-backup",outcome="failure",status="ESME_RTHROTTLED"} 1`,
		`synevyr_submit_round_trip_seconds_bucket{connector="smsc-primary",le="0.05"} 1`,
		`synevyr_submit_round_trip_seconds_bucket{connector="smsc-primary",le="+Inf"} 1`,
		`synevyr_submit_round_trip_seconds_sum{connector="smsc-primary"} 0.03`,
		`synevyr_submit_round_trip_seconds_count{connector="smsc-primary"} 1`,
		`synevyr_dlr_total{final_state="DELIVRD",level="3",outcome="delivered"} 1`,
		`synevyr_dlr_total{final_state="UNKNOWN",level="2",outcome="correlation_failure"} 1`,
		`synevyr_mo_total{connector="smsc-primary",outcome="received"} 1`,
		`synevyr_mo_total{connector="smsc-primary",outcome="routed"} 1`,
		`synevyr_mo_total{connector="smsc-backup",outcome="dropped"} 1`,
		`synevyr_connector_bound{connector="smsc-primary"} 1`,
		`synevyr_connector_state{connector="smsc-primary",state="BOUND"} 1`,
		`synevyr_connector_uptime_seconds{connector="smsc-primary"} 90`,
		`synevyr_connector_bound{connector="smsc-backup"} 0`,
		`synevyr_queue_depth{queue="submit.sm.smsc-primary"} 42`,
		`synevyr_throughput_rejections_total{user="alice"} 1`,
		`synevyr_interceptor_errors_total{direction="mt"} 1`,
		`synevyr_billing_charges_total{currency="USD",user="alice"} 0.25`,
		`synevyr_billing_refusals_total{reason="insufficient_balance",user="bob"} 1`,
		`synevyr_billing_mismatches_total{kind="late_charge"} 1`,
		`synevyr_gateway_ready 0`,
		`synevyr_gateway_health{status="degraded"} 1`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("modern metrics missing %q:\n%s", want, out)
		}
	}
}

func TestPrometheusRenderEscapesLabelsAndSortsSeries(t *testing.T) {
	registry := newPrometheusRegistry(time.Now)
	registry.RecordMO("z", MOReceived)
	registry.RecordMO("a\"\\\n", MOReceived)

	out := string(registry.RenderPrometheus())
	escaped := `synevyr_mo_total{connector="a\"\\\n",outcome="received"} 1`
	if !strings.Contains(out, escaped+"\n") {
		t.Fatalf("escaped label missing:\n%s", out)
	}
	if strings.Index(out, escaped) > strings.Index(out, `synevyr_mo_total{connector="z",outcome="received"} 1`) {
		t.Fatalf("series are not sorted:\n%s", out)
	}
}

func TestPrometheusHandlerMethodAndContentType(t *testing.T) {
	registry := NewPrometheusRegistry()

	get := httptest.NewRecorder()
	registry.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/metrics/prometheus", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%q", get.Code, get.Body.String())
	}
	if got := get.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("Content-Type=%q", got)
	}

	post := httptest.NewRecorder()
	registry.Handler().ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/metrics/prometheus", nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST status=%d Allow=%q", post.Code, post.Header().Get("Allow"))
	}
}

func TestPrometheusMetricsDoNotChangeLegacyRender(t *testing.T) {
	httpStats := &HTTPStats{}
	httpStats.Inc("request_count")
	smppcStats := NewSMPPcRegistry()
	smppcStats.Inc("smsc-primary", "submit_sm_count")
	smppsStats := &SMPPsStats{}
	smppsStats.Inc("connect_count")
	before := string(Render(httpStats, smppcStats, []string{"smsc-primary"}, smppsStats))

	registry := NewPrometheusRegistry()
	registry.RecordSubmit("smsc-primary", SubmitSuccess, "ESME_ROK", time.Second)
	registry.SetQueueDepth("submit.sm.smsc-primary", 100)

	after := string(Render(httpStats, smppcStats, []string{"smsc-primary"}, smppsStats))
	if after != before {
		t.Fatalf("legacy render changed after modern metrics update:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestPrometheusRenderTerminationSurface(t *testing.T) {
	registry := newPrometheusRegistry(time.Now)

	registry.RecordTerminationVerdict("partner-a-term", "DELIVRD")
	registry.RecordTerminationVerdict("partner-a-term", "DELIVRD")
	registry.RecordTerminationVerdict("partner-a-term", "REJECTD")
	registry.RecordTerminationGateBypass("partner-a-term", "redis-window")
	registry.RecordTerminationDelivery("partner-a-term", TerminationDeliveryAttempt)
	registry.RecordTerminationDelivery("partner-a-term", TerminationDeliveryFailure)
	registry.RecordTerminationDelivery("partner-a-term", TerminationDeliveryDeadLetter)
	registry.SetTerminationSpool("partner-a-term", TerminationSpoolCensus{
		Rows: 12, DeadLettered: 3, ReceiptsOverdue: 1,
	})

	out := string(registry.RenderPrometheus())
	for _, want := range []string{
		// The status label is lowercased, so a receipt status spelled either way
		// upstream cannot split one connector's traffic across two series.
		`synevyr_termination_verdicts_total{connector="partner-a-term",outcome="delivrd"} 2`,
		`synevyr_termination_verdicts_total{connector="partner-a-term",outcome="rejectd"} 1`,
		`synevyr_termination_gate_bypass_total{connector="partner-a-term",source="redis-window"} 1`,
		`synevyr_termination_delivery_total{connector="partner-a-term",outcome="attempt"} 1`,
		`synevyr_termination_delivery_total{connector="partner-a-term",outcome="dead_letter"} 1`,
		`synevyr_termination_delivery_total{connector="partner-a-term",outcome="failure"} 1`,
		`synevyr_termination_spool_rows{connector="partner-a-term"} 12`,
		`synevyr_termination_dead_letter_depth{connector="partner-a-term"} 3`,
		`synevyr_termination_receipts_overdue{connector="partner-a-term"} 1`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("termination metrics missing %q:\n%s", want, out)
		}
	}
}

// TestTerminationSpoolCensusReplaces proves the gauges fall as well as rise. A
// dead-letter depth that only ever climbed would leave its alarm latched after
// the queue was drained.
func TestTerminationSpoolCensusReplaces(t *testing.T) {
	registry := newPrometheusRegistry(time.Now)
	registry.SetTerminationSpool("term", TerminationSpoolCensus{Rows: 9, DeadLettered: 4, ReceiptsOverdue: 2})
	registry.SetTerminationSpool("term", TerminationSpoolCensus{Rows: 1})

	out := string(registry.RenderPrometheus())
	for _, want := range []string{
		`synevyr_termination_spool_rows{connector="term"} 1`,
		`synevyr_termination_dead_letter_depth{connector="term"} 0`,
		`synevyr_termination_receipts_overdue{connector="term"} 0`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("census did not replace: missing %q:\n%s", want, out)
		}
	}
}
