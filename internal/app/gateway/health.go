package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/stats"
)

// healthDependencies decouples the /health report from live infrastructure.
type healthDependencies struct {
	pingStore       func(context.Context) error
	amqpHealthy     func() bool
	pingBridge      func(context.Context) error
	connectorStatus func(cid string) (smppc.ManagedStatus, error)
	required        []string
}

type healthReport struct {
	Status string            `json:"status"`
	Ready  bool              `json:"ready"`
	Checks map[string]string `json:"checks"`
}

const (
	healthTimeout     = 5 * time.Second
	storeProbeBudget  = 2 * time.Second
	bridgeProbeBudget = 2 * time.Second
)

// buildHealthReport runs every readiness check and never panics on a partially
// constructed runtime. Missing dependencies are still starting, a lost
// connector is degraded, and infrastructure probe failures are broken.
func buildHealthReport(ctx context.Context, deps healthDependencies) healthReport {
	report := healthReport{Status: "ok", Checks: make(map[string]string)}
	setStatus := func(status string) {
		severity := map[string]int{"ok": 0, "starting": 1, "degraded": 2, "broken": 3}
		if severity[status] > severity[report.Status] {
			report.Status = status
		}
	}
	fail := func(name, detail, status string) {
		report.Checks[name] = detail
		setStatus(status)
	}

	switch {
	case deps.pingStore == nil:
		fail("postgres", "unavailable", "starting")
	default:
		err, timedOut := runHealthProbe(ctx, storeProbeBudget, deps.pingStore)
		if timedOut {
			fail("postgres", "probe timeout", "broken")
		} else if err != nil {
			fail("postgres", "failed: "+err.Error(), "broken")
		} else {
			report.Checks["postgres"] = "ok"
		}
	}

	switch {
	case deps.amqpHealthy == nil:
		fail("amqp", "unavailable", "starting")
	case !deps.amqpHealthy():
		fail("amqp", "connection closed", "broken")
	default:
		report.Checks["amqp"] = "ok"
	}

	switch {
	case deps.pingBridge == nil:
		fail("bridge", "unavailable", "starting")
	default:
		err, timedOut := runHealthProbe(ctx, bridgeProbeBudget, deps.pingBridge)
		if timedOut {
			fail("bridge", "probe timeout", "broken")
		} else if err != nil {
			fail("bridge", "failed: "+err.Error(), "broken")
		} else {
			report.Checks["bridge"] = "ok"
		}
	}

	for _, cid := range deps.required {
		name := "connector:" + cid
		switch {
		case deps.connectorStatus == nil:
			fail(name, "unavailable", "starting")
			continue
		default:
		}
		status, err := deps.connectorStatus(cid)
		if err != nil {
			fail(name, "failed: "+err.Error(), "broken")
			continue
		}
		stats.DefaultPrometheus().SetConnectorState(cid, string(status.Observed))
		switch status.Observed {
		case smppc.StatusBound:
			report.Checks[name] = "bound"
		case smppc.StatusConnecting:
			fail(name, string(status.Observed), "starting")
		default:
			fail(name, string(status.Observed), "degraded")
		}
	}

	report.Ready = report.Status == "ok"
	stats.DefaultPrometheus().SetGatewayHealth(report.Status)
	return report
}

// runHealthProbe enforces a wall-clock budget even when a dependency ignores
// context cancellation. The buffered result channel lets a late probe return
// without blocking after the health request has completed.
func runHealthProbe(parent context.Context, budget time.Duration, probe func(context.Context) error) (error, bool) {
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- probe(ctx) }()
	select {
	case err := <-done:
		if errors.Is(err, context.DeadlineExceeded) {
			return err, true
		}
		return err, false
	case <-ctx.Done():
		return ctx.Err(), true
	}
}

// healthDeps snapshots the runtime's readiness-check functions, tolerating a
// partially constructed runtime.
func (runtime *Runtime) healthDeps() healthDependencies {
	dependencies := healthDependencies{required: runtime.requiredConnectors}
	if runtime.store != nil {
		dependencies.pingStore = runtime.store.Ping
	}
	if runtime.outbound != nil {
		dependencies.amqpHealthy = runtime.outbound.AMQPHealthy
	}
	if runtime.bridge != nil {
		dependencies.pingBridge = runtime.bridge.Ping
	}
	if runtime.manager != nil {
		dependencies.connectorStatus = runtime.manager.Status
	}
	return dependencies
}

// healthProbe exposes the same checks as /health to the admin web UI's
// dashboard: overall status plus per-check detail.
func (runtime *Runtime) healthProbe() func(ctx context.Context) (string, map[string]string) {
	dependencies := runtime.healthDeps()
	return func(ctx context.Context) (string, map[string]string) {
		report := buildHealthReport(ctx, dependencies)
		return report.Status, report.Checks
	}
}

// healthHandler serves both dependency health and readiness. This deliberately
// adds production orchestrator semantics beyond Jasmin's unconditional /ping:
// /health stays 200 while degraded so an orchestrator does not restart a
// process that can recover its connector, while /ready is 200 only when the
// node may take traffic. Both return 503 while starting or broken.
func (runtime *Runtime) healthHandler() http.Handler {
	return readinessHandler(runtime.healthDeps())
}

func readinessHandler(dependencies healthDependencies) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), healthTimeout)
		defer cancel()
		report := buildHealthReport(ctx, dependencies)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		if !report.Ready && (request.URL.Path != "/health" || report.Status != "degraded") {
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(writer).Encode(report)
	})
}

// livenessHandler reports only process/admission identity. Readiness is kept
// separate because a live active node may temporarily lose a dependency and
// must leave the load-balancer pool without being restart-looped.
func livenessHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"active","live":true}`))
	})
}
