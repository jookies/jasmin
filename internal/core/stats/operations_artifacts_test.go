package stats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrometheusAlertsCoverGatewayFailureModes(t *testing.T) {
	path := filepath.Join("..", "..", "..", "deploy", "alerts.prometheus.yml")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, alert := range []string{
		"JasminConnectorUnbound",
		"JasminConnectorFlapping",
		"JasminSubmitFailureRateHigh",
		"JasminDLRCorrelationFailures",
		"JasminQueueBacklogGrowing",
		"JasminBillingMismatch",
		"JasminInterceptorFailures",
		"JasminThroughputRejectionsSpiking",
		"JasminGatewayUnready",
	} {
		if !strings.Contains(text, "alert: "+alert) {
			t.Errorf("missing alert %s", alert)
		}
	}
	for _, metric := range []string{
		"jasmin_connector_bound",
		"jasmin_submit_total",
		"jasmin_dlr_total",
		"jasmin_queue_depth",
		"jasmin_billing_mismatches_total",
		"jasmin_interceptor_errors_total",
		"jasmin_throughput_rejections_total",
		"jasmin_gateway_ready",
	} {
		if !strings.Contains(text, metric) {
			t.Errorf("alerts do not use emitted metric %s", metric)
		}
	}
}

func TestRunbooksHaveRequiredRecoveryWorkflow(t *testing.T) {
	runbooks := []struct {
		name   string
		tokens []string
	}{
		{"smsc-connection-lost-or-flapping.md", []string{"/ready", "/admin/connectors", "Connection lost. Reason:", "con_loss_delay"}},
		{"rabbitmq-down-or-backed-up.md", []string{"rabbitmq-diagnostics", "rabbitmqctl list_queues", "DLRLookup-main", "RouterPB_deliver_sm_all"}},
		{"postgresql-down-or-slow.md", []string{"pg_isready", "pg_stat_activity", "postgres_dsn", `"postgres"`}},
		{"stuck-or-growing-queue.md", []string{"submit.sm.", "messages_ready", "messages_unacknowledged", "prefetch_count"}},
		{"dlrs-not-arriving.md", []string{"DLRLookup-main", "dlr_lookup.redis_url", "DLR lookup failed:", "dlr_thrower"}},
		{"billing-mismatch-or-balance-drift.md", []string{"billing_quotas", "cdr_records", "CDR reconciliation mismatch", "quota_persist_interval_seconds"}},
		{"credential-compromise-or-rotation.md", []string{"ADMIN_TOKEN", "SMSC_PASSWORD", "SMPPS_USER_PASSWORD", "password_sha256"}},
		{"gateway-will-not-start.md", []string{"--check-config", "bind_timeout_seconds", "oracle_tree=frozen", "docker compose"}},
	}
	for _, runbook := range runbooks {
		t.Run(runbook.name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "docs", "runbooks", runbook.name)
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(payload)
			for _, heading := range []string{"## Symptom", "## Confirm", "## Fix", "## Verify recovery"} {
				if !strings.Contains(text, heading) {
					t.Errorf("missing heading %q", heading)
				}
			}
			for _, token := range runbook.tokens {
				if !strings.Contains(text, token) {
					t.Errorf("missing system-specific detail %q", token)
				}
			}
		})
	}
}
