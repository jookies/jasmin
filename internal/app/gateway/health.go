package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
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
	Checks map[string]string `json:"checks"`
}

const (
	healthTimeout     = 5 * time.Second
	bridgeProbeBudget = 3 * time.Second
)

// buildHealthReport runs every readiness check and never panics on a partially
// constructed runtime: a missing dependency is itself a degraded state.
func buildHealthReport(ctx context.Context, deps healthDependencies) healthReport {
	report := healthReport{Status: "ok", Checks: make(map[string]string)}
	fail := func(name, detail string) {
		report.Checks[name] = detail
		report.Status = "degraded"
	}

	switch {
	case deps.pingStore == nil:
		fail("postgres", "unavailable")
	default:
		if err := deps.pingStore(ctx); err != nil {
			fail("postgres", "failed: "+err.Error())
		} else {
			report.Checks["postgres"] = "ok"
		}
	}

	switch {
	case deps.amqpHealthy == nil:
		fail("amqp", "unavailable")
	case !deps.amqpHealthy():
		fail("amqp", "connection closed")
	default:
		report.Checks["amqp"] = "ok"
	}

	// The bridge Ping waits on the bridge request mutex, which is not
	// context-aware — a hung in-flight request would hang the endpoint. Probe
	// in a goroutine and report a bounded timeout instead; a Ping that starts
	// after its context expired returns early without touching the subprocess.
	switch {
	case deps.pingBridge == nil:
		fail("bridge", "unavailable")
	default:
		probeCtx, cancel := context.WithTimeout(ctx, bridgeProbeBudget)
		done := make(chan error, 1)
		go func() { done <- deps.pingBridge(probeCtx) }()
		select {
		case err := <-done:
			if err != nil {
				fail("bridge", "failed: "+err.Error())
			} else {
				report.Checks["bridge"] = "ok"
			}
		case <-probeCtx.Done():
			fail("bridge", "probe timeout")
		}
		cancel()
	}

	for _, cid := range deps.required {
		name := "connector:" + cid
		switch {
		case deps.connectorStatus == nil:
			fail(name, "unavailable")
			continue
		default:
		}
		status, err := deps.connectorStatus(cid)
		if err != nil {
			fail(name, "failed: "+err.Error())
			continue
		}
		if status.Observed != smppc.StatusBound {
			fail(name, string(status.Observed))
			continue
		}
		report.Checks[name] = "bound"
	}

	return report
}

// healthHandler serves GET /health: 200 when every dependency is ready, 503
// with the per-check detail otherwise. Distinct from the legacy-parity /ping,
// which answers unconditionally.
func (runtime *Runtime) healthHandler() http.Handler {
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
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), healthTimeout)
		defer cancel()
		report := buildHealthReport(ctx, dependencies)
		writer.Header().Set("Content-Type", "application/json")
		if report.Status != "ok" {
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(writer).Encode(report)
	})
}
