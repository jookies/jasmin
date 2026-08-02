package adminweb

import (
	"context"
	"net/http"
	"time"
)

// handleTopology serves the whole running gateway as one graph document.
//
// It is a read-only aggregate over surfaces the console already exposes
// individually, gathered through the same collectors the list handlers use so
// the map and the tables cannot disagree about what exists. Everything on the
// page is derived from this one response: the canvas, the problem rail and the
// inspector.
//
// A failure to read one entity type fails the request rather than returning a
// partial graph. A map that silently omits the connectors is worse than no map:
// an operator would read "no connectors configured" from what is actually "the
// connector store was unreachable".
func (h *Handler) handleTopology(w http.ResponseWriter, r *http.Request) {
	inputs, err := h.topologyInputs(r.Context(), r.URL.Query().Get("analytics") == "1")
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, BuildGraph(inputs))
}

// topologyInputs gathers the document's inputs. withAnalytics adds the windowed
// read history behind the matrix panel, which costs one audit aggregate per
// window and is therefore opt-in rather than paid for on every five-second poll.
func (h *Handler) topologyInputs(ctx context.Context, withAnalytics bool) (GraphInputs, error) {
	inputs := GraphInputs{ObservedAt: time.Now().UTC()}

	connectors, err := h.collectConnectors(ctx)
	if err != nil {
		return GraphInputs{}, err
	}
	inputs.Connectors = connectors

	if inputs.Termination, err = h.collectTerminationConnectors(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.Routes, err = h.collectRoutes(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.MORoutes, err = h.collectMORoutes(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.Users, err = h.collectUsers(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.Groups, err = h.groupResources(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.SMPPsUsers, err = h.collectSMPPsUsers(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.Filters, err = h.collectFilters(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.HTTPDestinations, err = h.collectHTTPConnectors(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.Interceptors, err = h.collectInterceptors(ctx); err != nil {
		return GraphInputs{}, err
	}
	if inputs.PullTokens, err = h.collectMessageConsumers(ctx); err != nil {
		return GraphInputs{}, err
	}
	if h.deps.Messages != nil {
		inputs.SpoolRetention = h.deps.Messages.Retention().Window
	}
	if withAnalytics {
		if inputs.PullWindows, err = h.collectPullWindows(ctx, inputs.ObservedAt); err != nil {
			return GraphInputs{}, err
		}
	}

	// The live balance is what makes the money lens honest — the provisioned
	// grant stops being the answer after the first message. It is a per-user
	// read, so it is attached here rather than inside the collector, exactly as
	// listUsers does it.
	for index := range inputs.Users {
		h.attachLiveQuota(ctx, &inputs.Users[index])
	}

	if h.deps.Metrics != nil {
		inputs.Metrics = h.deps.Metrics.Snapshot()
		inputs.MetricsWired = true
	}
	inputs.HTTPCounters = h.deps.HTTPStats.Snapshot()
	inputs.SMPPsCounters = h.deps.SMPPsStats.Snapshot()
	// Per-connector legacy counters, for every connector the map draws rather
	// than only the ones the runtime happens to list.
	if h.deps.SMPPcStats != nil {
		inputs.SMPPcCounters = make(map[string]map[string]int64, len(connectors))
		for _, connector := range connectors {
			inputs.SMPPcCounters[connector.CID] = h.deps.SMPPcStats.Snapshot(connector.CID)
		}
	}

	if h.deps.Health != nil {
		inputs.HealthStatus, inputs.Health = h.deps.Health(ctx)
	}
	if h.deps.Ingress != nil {
		inputs.Ingress = h.deps.Ingress()
		inputs.HasIngress = true
	}
	if h.deps.StartedAt != nil {
		inputs.StartedAt = h.deps.StartedAt()
	}
	return inputs, nil
}
