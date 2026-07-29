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
		"# HELP jasmin_submit_total Submit lifecycle events by connector, outcome, and SMPP command status.",
		"# TYPE jasmin_submit_total counter",
		`jasmin_submit_total{connector="smsc-primary",outcome="attempt",status="pending"} 1`,
		`jasmin_submit_total{connector="smsc-primary",outcome="success",status="ESME_ROK"} 1`,
		`jasmin_submit_total{connector="smsc-backup",outcome="failure",status="ESME_RTHROTTLED"} 1`,
		`jasmin_submit_round_trip_seconds_bucket{connector="smsc-primary",le="0.05"} 1`,
		`jasmin_submit_round_trip_seconds_bucket{connector="smsc-primary",le="+Inf"} 1`,
		`jasmin_submit_round_trip_seconds_sum{connector="smsc-primary"} 0.03`,
		`jasmin_submit_round_trip_seconds_count{connector="smsc-primary"} 1`,
		`jasmin_dlr_total{final_state="DELIVRD",level="3",outcome="delivered"} 1`,
		`jasmin_dlr_total{final_state="UNKNOWN",level="2",outcome="correlation_failure"} 1`,
		`jasmin_mo_total{connector="smsc-primary",outcome="received"} 1`,
		`jasmin_mo_total{connector="smsc-primary",outcome="routed"} 1`,
		`jasmin_mo_total{connector="smsc-backup",outcome="dropped"} 1`,
		`jasmin_connector_bound{connector="smsc-primary"} 1`,
		`jasmin_connector_state{connector="smsc-primary",state="BOUND"} 1`,
		`jasmin_connector_uptime_seconds{connector="smsc-primary"} 90`,
		`jasmin_connector_bound{connector="smsc-backup"} 0`,
		`jasmin_queue_depth{queue="submit.sm.smsc-primary"} 42`,
		`jasmin_throughput_rejections_total{user="alice"} 1`,
		`jasmin_interceptor_errors_total{direction="mt"} 1`,
		`jasmin_billing_charges_total{currency="USD",user="alice"} 0.25`,
		`jasmin_billing_refusals_total{reason="insufficient_balance",user="bob"} 1`,
		`jasmin_billing_mismatches_total{kind="late_charge"} 1`,
		`jasmin_gateway_ready 0`,
		`jasmin_gateway_health{status="degraded"} 1`,
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
	escaped := `jasmin_mo_total{connector="a\"\\\n",outcome="received"} 1`
	if !strings.Contains(out, escaped+"\n") {
		t.Fatalf("escaped label missing:\n%s", out)
	}
	if strings.Index(out, escaped) > strings.Index(out, `jasmin_mo_total{connector="z",outcome="received"} 1`) {
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
