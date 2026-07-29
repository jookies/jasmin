package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

func healthyDependencies() healthDependencies {
	return healthDependencies{
		pingStore:   func(context.Context) error { return nil },
		amqpHealthy: func() bool { return true },
		pingBridge:  func(context.Context) error { return nil },
		connectorStatus: func(cid string) (smppc.ManagedStatus, error) {
			return smppc.ManagedStatus{CID: cid, Observed: smppc.StatusBound}, nil
		},
		required: []string{"smsc-primary"},
	}
}

func TestActiveLivenessAndReadinessHandlers(t *testing.T) {
	liveResponse := httptest.NewRecorder()
	livenessHandler().ServeHTTP(liveResponse, httptest.NewRequest(http.MethodGet, "/live", nil))
	if liveResponse.Code != http.StatusOK || !strings.Contains(liveResponse.Body.String(), `"status":"active"`) {
		t.Fatalf("liveness=(%d,%q)", liveResponse.Code, liveResponse.Body.String())
	}

	readyResponse := httptest.NewRecorder()
	readinessHandler(healthyDependencies()).ServeHTTP(
		readyResponse,
		httptest.NewRequest(http.MethodGet, "/ready", nil),
	)
	if readyResponse.Code != http.StatusOK || !strings.Contains(readyResponse.Body.String(), `"status":"ok"`) {
		t.Fatalf("readiness=(%d,%q)", readyResponse.Code, readyResponse.Body.String())
	}

	postResponse := httptest.NewRecorder()
	readinessHandler(healthyDependencies()).ServeHTTP(
		postResponse,
		httptest.NewRequest(http.MethodPost, "/ready", nil),
	)
	if postResponse.Code != http.StatusMethodNotAllowed ||
		postResponse.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST readiness=(%d, Allow=%q)", postResponse.Code, postResponse.Header().Get("Allow"))
	}
}

func TestBuildHealthReportAllHealthy(t *testing.T) {
	report := buildHealthReport(context.Background(), healthyDependencies())
	if report.Status != "ok" {
		t.Fatalf("status=%q want ok (checks=%v)", report.Status, report.Checks)
	}
	for name, want := range map[string]string{
		"postgres": "ok", "amqp": "ok", "bridge": "ok", "connector:smsc-primary": "bound",
	} {
		if got := report.Checks[name]; got != want {
			t.Fatalf("check %s=%q want %q", name, got, want)
		}
	}
}

func TestBuildHealthReportEachFailureDegrades(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*healthDependencies)
		check  string
	}{
		{"postgres_down", func(deps *healthDependencies) {
			deps.pingStore = func(context.Context) error { return errors.New("dial refused") }
		}, "postgres"},
		{"amqp_closed", func(deps *healthDependencies) {
			deps.amqpHealthy = func() bool { return false }
		}, "amqp"},
		{"bridge_error", func(deps *healthDependencies) {
			deps.pingBridge = func(context.Context) error { return errors.New("broken pipe") }
		}, "bridge"},
		{"connector_unbound", func(deps *healthDependencies) {
			deps.connectorStatus = func(cid string) (smppc.ManagedStatus, error) {
				return smppc.ManagedStatus{CID: cid, Observed: smppc.StatusDisconnected}, nil
			}
		}, "connector:smsc-primary"},
		{"missing_dependency", func(deps *healthDependencies) {
			deps.pingStore = nil
		}, "postgres"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			deps := healthyDependencies()
			testCase.mutate(&deps)
			report := buildHealthReport(context.Background(), deps)
			if report.Status != "degraded" {
				t.Fatalf("status=%q want degraded", report.Status)
			}
			if got := report.Checks[testCase.check]; got == "ok" || got == "bound" || got == "" {
				t.Fatalf("check %s=%q want failure detail", testCase.check, got)
			}
		})
	}
}

func TestBuildHealthReportBridgeProbeTimeoutDoesNotHang(t *testing.T) {
	deps := healthyDependencies()
	release := make(chan struct{})
	defer close(release)
	deps.pingBridge = func(ctx context.Context) error {
		// Simulates Ping stuck behind the bridge request mutex.
		select {
		case <-release:
		case <-time.After(30 * time.Second):
		}
		return nil
	}
	start := time.Now()
	report := buildHealthReport(context.Background(), deps)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("report took %v, probe budget not enforced", elapsed)
	}
	if report.Status != "degraded" || report.Checks["bridge"] != "probe timeout" {
		t.Fatalf("status=%q bridge=%q want degraded probe timeout", report.Status, report.Checks["bridge"])
	}
}
