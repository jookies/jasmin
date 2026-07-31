package gateway

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

// DefaultQueueDepthInterval is how often broker queue depths are re-read.
//
// Fifteen seconds is chosen against the shipped backlog alert, which needs
// growth measured over fifteen minutes (deploy/alerts.prometheus.yml): sixty
// samples per window is enough for a rate to mean something, and one passive
// declare per queue per fifteen seconds is negligible next to the traffic on the
// same connection.
const DefaultQueueDepthInterval = 15 * time.Second

// queueDepthObserver periodically publishes broker queue depths to the metrics
// registry.
//
// Depth is a population, not an event, so it cannot be counted at the point it
// changes the way a submit or a receipt can: the queue is the broker's, and the
// only place the number exists is the broker. That makes this a poll, and the
// gauge is therefore "depth at the last observation" — an operator comparing two
// scrapes is comparing two observations, not two instants.
type queueDepthObserver struct {
	depths   func(context.Context, []string) (map[string]int, error)
	queues   func() []string
	interval time.Duration
	logger   *slog.Logger
	// registry is injected so a test can assert against its own registry
	// instead of the process-wide one.
	registry *stats.PrometheusRegistry
	// observed remembers every queue this observer has ever reported, so a
	// queue that becomes unreadable is zeroed rather than freezing at its last
	// value. A gauge that silently stops updating is worse than one that reads
	// zero: the first looks like a healthy steady state.
	observed map[string]struct{}
}

func newQueueDepthObserver(
	depths func(context.Context, []string) (map[string]int, error),
	queues func() []string,
	interval time.Duration,
	logger *slog.Logger,
	registry *stats.PrometheusRegistry,
) *queueDepthObserver {
	if interval <= 0 {
		interval = DefaultQueueDepthInterval
	}
	if registry == nil {
		registry = stats.DefaultPrometheus()
	}
	return &queueDepthObserver{
		depths:   depths,
		queues:   queues,
		interval: interval,
		logger:   logger,
		registry: registry,
		observed: map[string]struct{}{},
	}
}

// runOnce reads every queue once and updates the gauge.
func (o *queueDepthObserver) runOnce(ctx context.Context) error {
	if o == nil || o.depths == nil || o.queues == nil {
		return nil
	}
	names := o.queues()
	if len(names) == 0 {
		return nil
	}
	depths, err := o.depths(ctx, names)
	for queue, depth := range depths {
		o.observed[queue] = struct{}{}
		o.registry.SetQueueDepth(queue, int64(depth))
	}
	// Anything previously reported and missing this pass reads zero rather than
	// keeping a stale value. See the observed field.
	for queue := range o.observed {
		if _, ok := depths[queue]; !ok {
			o.registry.SetQueueDepth(queue, 0)
		}
	}
	return err
}

// run polls until the context ends. A failed pass is logged at debug: a broker
// that is down is already loud in /health and in the connector state gauges, and
// an error line every fifteen seconds during an outage buries them.
func (o *queueDepthObserver) run(ctx context.Context) {
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()
	// One immediate pass, so a scrape taken seconds after boot has real numbers
	// instead of an empty metric that a dashboard renders as zero.
	if err := o.runOnce(ctx); err != nil && ctx.Err() == nil && o.logger != nil {
		o.logger.Debug("Queue depth observation failed: " + err.Error())
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := o.runOnce(ctx); err != nil && ctx.Err() == nil && o.logger != nil {
				o.logger.Debug("Queue depth observation failed: " + err.Error())
			}
		}
	}
}

// observedQueues names the queues worth a depth gauge: every MT connector's
// submit queue — SMPP client and termination alike, since both consume the same
// queue and a backlog on either means the same thing — plus the two shared
// worker queues where a stalled consumer is invisible from the connector side.
func (runtime *Runtime) observedQueues(dlrLookupPID string) func() []string {
	return func() []string {
		names := map[string]struct{}{
			amqpcompat.DLRLookupQueue(dlrLookupPID): {},
			amqpcompat.RouterDeliverSMQueue:         {},
		}
		if runtime.manager != nil {
			for _, connector := range runtime.manager.List() {
				names[amqpcompat.ConnectorSubmitQueue(connector.CID)] = struct{}{}
			}
		}
		if manager := runtime.termination.Manager(); manager != nil {
			for _, connector := range manager.List() {
				names[amqpcompat.ConnectorSubmitQueue(connector.CID)] = struct{}{}
			}
		}
		list := make([]string, 0, len(names))
		for name := range names {
			list = append(list, name)
		}
		sort.Strings(list)
		return list
	}
}

// startQueueDepthObserver launches the poll loop, unless there is no broker to
// poll. It is resolved on every pass rather than captured once, because the
// admin plane adds and removes connectors at runtime.
func (runtime *Runtime) startQueueDepthObserver(ctx context.Context, dlrLookupPID string, logger *slog.Logger) {
	if runtime == nil || runtime.outbound == nil {
		return
	}
	observer := newQueueDepthObserver(
		runtime.outbound.QueueDepths,
		runtime.observedQueues(dlrLookupPID),
		DefaultQueueDepthInterval,
		logger,
		stats.DefaultPrometheus(),
	)
	go observer.run(ctx)
}
