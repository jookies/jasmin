import { useCallback, useEffect, useRef, useState } from "react";

import { API_URL, httpClient } from "../httpClient";
import { emptyGraph, type TopologyGraph } from "./types";

/**
 * How often the map re-reads /api/topology.
 *
 * Five seconds, matched to the slowest input rather than to how live the page
 * could feel: broker queue depth is only observed every fifteen seconds by the
 * gateway, so polling faster would redraw the same number three times. It is
 * also a full document each time — cheap, and it means a reconnect after a
 * laptop wakes needs no recovery logic at all.
 */
export const POLL_INTERVAL_MS = 5000;

/** Per-minute rates derived from consecutive polls, keyed by node then metric. */
export type RateTable = Record<string, Record<string, number>>;

export type TopologyState = {
  graph: TopologyGraph;
  rates: RateTable;
  /** Queue node ids holding messages that are not going down. */
  stalledQueues: Set<string>;
  /** True until the first successful response. */
  loading: boolean;
  /** The last error, kept alongside the last good graph rather than replacing it. */
  error: string | null;
  /** Wall-clock time of the last successful poll, for the "updated Ns ago" label. */
  updatedAt: Date | null;
  refresh: () => void;
};

/**
 * deriveRates differences two polls into per-minute rates.
 *
 * The registry stores cumulative counters, so a rate only exists once there are
 * two samples: the first poll after load reports nothing rather than dividing a
 * total by an uptime, which would show a busy gateway as permanently slow.
 *
 * A counter that went backwards means the process restarted (counters are
 * process-local, KNOWN_QUIRKS Q-011). That yields no rate rather than a
 * negative one.
 */
const deriveRates = (
  next: TopologyGraph,
  previous: TopologyGraph | null,
): RateTable => {
  if (!previous || !previous.observed_at || !next.observed_at) return {};

  const elapsedSeconds =
    (Date.parse(next.observed_at) - Date.parse(previous.observed_at)) / 1000;
  if (!Number.isFinite(elapsedSeconds) || elapsedSeconds <= 0) return {};

  const before = new Map(previous.nodes.map((node) => [node.id, node.metrics ?? {}]));
  const rates: RateTable = {};

  for (const node of next.nodes) {
    const priorMetrics = before.get(node.id);
    if (!priorMetrics || !node.metrics) continue;

    const perNode: Record<string, number> = {};
    for (const [key, value] of Object.entries(node.metrics)) {
      const prior = priorMetrics[key];
      if (typeof prior !== "number" || value < prior) continue;
      perNode[key] = ((value - prior) / elapsedSeconds) * 60;
    }
    if (Object.keys(perNode).length > 0) rates[node.id] = perNode;
  }
  return rates;
};

/**
 * useTopology polls the topology document and derives rates from consecutive
 * responses.
 *
 * A failed poll keeps the last good graph on screen and surfaces the error
 * beside it. Blanking the canvas because one request timed out would destroy
 * the operator's context at exactly the moment the gateway is in trouble.
 */
/**
 * How many consecutive observations a queue may hold steady before the map
 * calls it stalled.
 *
 * Absolute depth cannot answer this. A queue whose consumer is retry-looping
 * sits at nineteen messages forever, which no sensible threshold would flag,
 * while a healthy burst can touch thousands and drain in seconds. The question
 * is whether it is going *down*, and that needs consecutive samples — which is
 * the one thing polling gives us for free.
 *
 * Three samples at a five-second poll is fifteen seconds of no progress, which
 * is also the gateway's own queue-depth observation interval, so this cannot
 * fire on a queue that simply has not been re-measured yet.
 */
const STALL_SAMPLES = 3;

export const useTopology = (withAnalytics = false): TopologyState => {
  const [graph, setGraph] = useState<TopologyGraph>(emptyGraph);
  const [rates, setRates] = useState<RateTable>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const [stalledQueues, setStalledQueues] = useState<Set<string>>(new Set());
  // depth history per queue, oldest first, capped at STALL_SAMPLES.
  const depths = useRef(new Map<string, number[]>());

  const previous = useRef<TopologyGraph | null>(null);
  // Read through a ref so toggling the panel does not rebuild the poll loop and
  // reset the interval.
  const analytics = useRef(withAnalytics);
  analytics.current = withAnalytics;
  // Guards against a slow response landing after the component unmounted, and
  // against two in-flight polls racing when a manual refresh overlaps the timer.
  const generation = useRef(0);

  const poll = useCallback(async () => {
    const current = ++generation.current;
    try {
      // The matrix rides on this same request rather than a second poll, so
      // the graph and the grid always describe one instant. Its windows cost an
      // audit aggregate each, so they are only asked for while the panel is open.
      const response = await httpClient.get<TopologyGraph>(
        `${API_URL}/topology${analytics.current ? "?analytics=1" : ""}`,
      );
      if (current !== generation.current) return;

      const next = response.data;
      setRates(deriveRates(next, previous.current));
      setStalledQueues(trackStalls(next, depths.current));
      previous.current = next;
      setGraph(next);
      setError(null);
      setUpdatedAt(new Date());
    } catch (cause) {
      if (current !== generation.current) return;
      setError(cause instanceof Error ? cause.message : "failed to read topology");
    } finally {
      if (current === generation.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void poll();
    const timer = window.setInterval(() => void poll(), POLL_INTERVAL_MS);
    return () => {
      window.clearInterval(timer);
      // Invalidate in-flight responses so they cannot setState after unmount.
      generation.current++;
    };
  }, [poll]);

  return { graph, rates, stalledQueues, loading, error, updatedAt, refresh: () => void poll() };
};

/**
 * trackStalls records each queue's depth across polls and reports the ones
 * holding messages that never go down — the shape of a consumer that is
 * accepting work and failing to complete it, which absolute depth cannot see.
 */
const trackStalls = (
  graph: TopologyGraph,
  history: Map<string, number[]>,
): Set<string> => {
  const stalled = new Set<string>();
  const seen = new Set<string>();

  for (const node of graph.nodes) {
    if (node.kind !== "queue") continue;
    const depth = node.metrics?.depth;
    if (typeof depth !== "number") continue;
    seen.add(node.id);

    const samples = [...(history.get(node.id) ?? []), depth].slice(-STALL_SAMPLES);
    history.set(node.id, samples);

    if (samples.length < STALL_SAMPLES) continue;
    // Holding work, and never lower than where it started.
    if (samples[0] > 0 && samples.every((value) => value >= samples[0])) {
      stalled.add(node.id);
    }
  }

  // Forget queues that no longer exist, so a recreated queue starts clean
  // rather than inheriting a stall from a previous incarnation.
  for (const id of [...history.keys()]) {
    if (!seen.has(id)) history.delete(id);
  }
  return stalled;
};
