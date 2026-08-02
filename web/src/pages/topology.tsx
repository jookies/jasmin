import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type FitViewOptions,
  type NodeMouseHandler,
  type OnSelectionChangeFunc,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Alert, Button, Input, Segmented, Spin, Tag } from "antd";
import { ReloadOutlined, RightOutlined, SearchOutlined } from "@ant-design/icons";

import { AnalyticsPanel } from "../topology/AnalyticsPanel";
import { Inspector } from "../topology/Inspector";
import { TopologyCard } from "../topology/TopologyCard";
import { TopologyFlowEdge } from "../topology/TopologyFlowEdge";
import { toFlowEdges, toFlowNodes } from "../topology/adapter";
import { CARD_WIDTH, layoutGraph } from "../topology/layout";
import { POLL_INTERVAL_MS, useTopology } from "../topology/useTopology";
import type { FlowFilter, Lens, NodeStatus, TopologyNode } from "../topology/types";
import "../topology/topology.css";

const nodeTypes = { topologyCard: TopologyCard };
const edgeTypes = { topologyFlow: TopologyFlowEdge };

/**
 * The guide, legend, and collapsed analytics bar sit above the React Flow
 * surface. Fit against their real safe area instead of shrinking the graph by
 * the same percentage on every side. A minimum automatic zoom keeps labels
 * readable; operators can still zoom farther out manually when they want the
 * complete shape at once.
 */
const FIT_VIEW_OPTIONS = {
  padding: {
    top: "88px",
    right: "32px",
    bottom: "104px",
    left: "32px",
  },
  minZoom: 0.8,
  maxZoom: 0.95,
} satisfies FitViewOptions;

const LENSES: { label: string; value: Lens }[] = [
  { label: "Health", value: "health" },
  { label: "Traffic", value: "traffic" },
  { label: "Latency", value: "latency" },
  { label: "Billing", value: "billing" },
];

const FLOWS: {
  label: string;
  value: FlowFilter;
  description: string;
  stages: string[];
}[] = [
  {
    label: "MT outbound",
    value: "mt",
    description: "Follow a submitted message to its destination",
    stages: ["Entry", "Routing", "Gateway", "Queues", "Connector", "Destination"],
  },
  {
    label: "MO inbound",
    value: "mo",
    description: "Follow a carrier message back to the customer",
    stages: ["Broker queue", "MO routing", "Delivery worker", "Callback"],
  },
  {
    label: "DLR receipts",
    value: "dlr",
    description: "Follow delivery status back to the sender",
    stages: ["Carrier receipt", "Connector", "Queues", "DLR worker", "Callback"],
  },
  {
    label: "Everything",
    value: "all",
    description: "Show every traffic and configuration relationship",
    stages: ["Traffic paths", "Supporting services"],
  },
];

const problemTone: Record<string, string> = {
  broken_path: "is-down",
  unbound: "is-warning",
  queue_backlog: "is-warning",
  stale_pull: "is-warning",
};

const STATUS_NAME: Record<NodeStatus, string> = {
  ok: "healthy",
  warning: "warning",
  down: "unhealthy",
  idle: "idle",
  unknown: "unknown",
};

/** "Updated 2s ago", recomputed on a ticker so it stays true between polls. */
const useAgo = (at: Date | null): string => {
  const [, tick] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => tick((value) => value + 1), 1000);
    return () => window.clearInterval(timer);
  }, []);
  if (!at) return "never";
  const seconds = Math.max(0, Math.round((Date.now() - at.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  return `${Math.floor(seconds / 60)}m ago`;
};

const TopologyCanvas = () => {
  const { fitView, getZoom, setCenter } = useReactFlow();
  const [analyticsOpen, setAnalyticsOpen] = useState(false);
  const { graph, rates, stalledQueues, loading, error, updatedAt, refresh } = useTopology(analyticsOpen);
  const [lens, setLens] = useState<Lens>("health");
  const [flow, setFlow] = useState<FlowFilter>("mt");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [activeProblem, setActiveProblem] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const ago = useAgo(updatedAt);

  // Layout runs only when the shape changes. Metrics arriving every five
  // seconds repaint the cards in place; without this the whole canvas would
  // reflow on a timer and nodes would move under the pointer.
  const layout = useMemo(
    () => layoutGraph(graph.nodes, graph.edges),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [graph.structure_hash],
  );

  const byId = useMemo(
    () => new Map(graph.nodes.map((node) => [node.id, node])),
    [graph.nodes],
  );

  const childrenOf = useMemo(() => {
    const map = new Map<string, TopologyNode[]>();
    for (const node of graph.nodes) {
      if (!node.parent) continue;
      map.set(node.parent, [...(map.get(node.parent) ?? []), node]);
    }
    return map;
  }, [graph.nodes]);

  const highlighted = useMemo(() => {
    const ids = new Set<string>();
    if (activeProblem) {
      const problem = graph.problems.find((entry) => entry.kind === activeProblem);
      problem?.node_ids.forEach((id) => ids.add(id));
    }
    const needle = search.trim().toLowerCase();
    if (needle) {
      for (const node of graph.nodes) {
        if (node.label.toLowerCase().includes(needle)) ids.add(node.id);
      }
    }

    // Selecting a card turns the full graph into a local trace: keep the
    // selected system and its immediate upstream/downstream systems strong,
    // while quieting unrelated branches. Child endpoints resolve to the card
    // that contains them so grouped routes and connectors trace correctly.
    if (selectedId) {
      const parentOf = new Map<string, string>();
      for (const node of graph.nodes) {
        if (node.parent) parentOf.set(node.id, node.parent);
      }
      const resolve = (id: string) => parentOf.get(id) ?? id;
      const selectedCard = resolve(selectedId);
      ids.add(selectedCard);
      for (const edge of graph.edges) {
        const from = resolve(edge.from);
        const to = resolve(edge.to);
        if (from === selectedCard || to === selectedCard) {
          ids.add(from);
          ids.add(to);
        }
      }
    }
    return ids;
  }, [activeProblem, graph.edges, graph.problems, graph.nodes, search, selectedId]);

  const options = { graph, layout, rates, lens, flow, selectedId, highlighted, stalledQueues };
  const flowNodes = useMemo(
    () =>
      toFlowNodes(options)
        .map((node) => ({
          ...node,
          ariaRole: "button" as const,
          ariaLabel: `${node.data.node.label}, ${
            node.data.stalled ? "warning" : STATUS_NAME[node.data.node.status]
          }. Select for details.`,
          focusable: true,
          selected: node.data.selected,
        }))
        .sort(
          (left, right) =>
            left.position.x - right.position.x ||
            left.position.y - right.position.y ||
            left.id.localeCompare(right.id),
        ),
    [graph, layout, rates, lens, flow, selectedId, highlighted, stalledQueues],
  );
  const flowEdges = useMemo(
    () => toFlowEdges(options),
    [graph, layout, rates, lens, flow, selectedId, highlighted],
  );

  const onNodeClick: NodeMouseHandler = useCallback((_, node) => setSelectedId(node.id), []);
  const onSelectionChange: OnSelectionChangeFunc = useCallback(({ nodes }) => {
    const focused = nodes[nodes.length - 1];
    setSelectedId(focused?.id ?? null);
  }, []);
  const chooseFlow = useCallback((value: FlowFilter) => {
    setFlow(value);
    setSelectedId(null);
  }, []);

  const selected = selectedId ? (byId.get(selectedId) ?? null) : null;
  const selectedChildren = selectedId ? (childrenOf.get(selectedId) ?? []) : [];
  const inspectorOpen = selected !== null;
  const selectedCardId = selected?.parent ?? selectedId;
  const activeFlow = FLOWS.find((item) => item.value === flow) ?? FLOWS[0];
  const emphasizedSystems = flowNodes.filter((node) => node.data.highlighted).length;
  const focusRequested = Boolean(activeProblem || search.trim() || selectedId);

  // Reframe after a journey switch. React Flow's fitView prop only handles the
  // first render; without this, selecting MO after MT leaves the smaller return
  // path stranded in the old viewport.
  useEffect(() => {
    if (flowNodes.length === 0) return;
    const frame = window.requestAnimationFrame(() => {
      void fitView({ ...FIT_VIEW_OPTIONS, duration: 320 });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [fitView, flow, flowNodes.length, graph.structure_hash]);

  // Opening the inspector removes horizontal room. Preserve the operator's
  // chosen zoom and move only enough to keep the selected card in the remaining
  // canvas; fitting the entire graph here would make every label tiny again.
  useEffect(() => {
    if (!inspectorOpen || !selectedCardId) return;
    const position = layout.positions[selectedCardId];
    const height = layout.heights[selectedCardId];
    if (!position || !height) return;

    const frame = window.requestAnimationFrame(() => {
      void setCenter(position.x + CARD_WIDTH / 2, position.y + height / 2, {
        zoom: getZoom(),
        duration: 240,
      });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [getZoom, inspectorOpen, layout, selectedCardId, setCenter]);

  return (
    <div className="topology-page">
      <header className="topology-topbar">
        <div className="topology-title">
          <span className="page-eyebrow">Overview</span>
          <h1>SMPP topology</h1>
        </div>
        <div className="topology-live">
          <span className={`topology-live-dot ${error ? "is-error" : ""}`} aria-hidden="true" />
          <strong>{error ? "STALE" : "LIVE"}</strong>
          <span>
            Updated {ago} · polls every {POLL_INTERVAL_MS / 1000}s
          </span>
        </div>
        <div className="topology-controls">
          <div className="topology-control-group">
            <span>Color by</span>
            <Segmented
              options={LENSES}
              value={lens}
              onChange={(value) => setLens(value as Lens)}
              aria-label="Color and size by signal"
            />
          </div>
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder="Search entities, routes, connectors…"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="topology-search"
          />
          <Button icon={<ReloadOutlined />} onClick={refresh} loading={loading}>
            Refresh
          </Button>
          <Tag className="topology-readonly">Read only</Tag>
        </div>
      </header>

      {error ? (
        <Alert
          type="warning"
          showIcon
          banner
          message={`Topology is stale: ${error}. Showing the last successful read.`}
        />
      ) : null}
      {graph.metrics_stale ? (
        <Alert
          type="info"
          showIcon
          banner
          message="This gateway reports no metrics registry, so the map shows structure without traffic figures."
        />
      ) : null}

      <div className={`topology-body ${inspectorOpen ? "has-inspector" : ""}`}>
        <nav className="topology-rail" aria-label="Explore topology">
          <div className="topology-rail-section is-problems-first">
            <span className="topology-rail-heading">
              Recent problems
              {activeProblem ? (
                <button type="button" onClick={() => setActiveProblem(null)}>
                  Clear filters
                </button>
              ) : null}
            </span>
            <ul className="topology-problems">
              {graph.problems.map((problem) => (
                <li key={problem.kind}>
                  <button
                    type="button"
                    className={[
                      problem.count > 0 ? problemTone[problem.kind] : "is-ok",
                      activeProblem === problem.kind ? "is-active" : "",
                    ]
                      .filter(Boolean)
                      .join(" ")}
                    onClick={() =>
                      setActiveProblem(activeProblem === problem.kind ? null : problem.kind)
                    }
                    disabled={problem.count === 0}
                  >
                    <span className="topology-dot" aria-hidden="true" />
                    <span>{problem.label}</span>
                    <strong>{problem.count}</strong>
                  </button>
                </li>
              ))}
            </ul>
          </div>

          <h2>Choose a journey</h2>
          <p className="topology-rail-intro">
            Start with one message path. Open the complete map only when you need cross-system context.
          </p>

          <ul className="topology-flows">
            {FLOWS.map((item) => (
              <li key={item.value}>
                <button
                  type="button"
                  className={flow === item.value ? "is-active" : ""}
                  aria-pressed={flow === item.value}
                  onClick={() => chooseFlow(item.value)}
                >
                  <span className={`topology-flow-mark is-${item.value}`}>{item.value.toUpperCase()}</span>
                  <span>
                    <strong>{item.label}</strong>
                    <small>{item.description}</small>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </nav>

        <div className={`topology-canvas ${analyticsOpen ? "has-analytics-open" : ""}`}>
          {loading && graph.nodes.length === 0 ? (
            <div className="topology-loading">
              <Spin />
              <span>Reading the running gateway…</span>
            </div>
          ) : (
            <ReactFlow
              nodes={flowNodes}
              edges={flowEdges}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              onNodeClick={onNodeClick}
              onSelectionChange={onSelectionChange}
              onPaneClick={() => setSelectedId(null)}
              fitView
              fitViewOptions={FIT_VIEW_OPTIONS}
              minZoom={0.2}
              maxZoom={1.6}
              nodesDraggable={false}
              nodesConnectable={false}
              edgesFocusable={false}
              deleteKeyCode={null}
              elementsSelectable
              proOptions={{ hideAttribution: false }}
            >
              <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
              <Controls showInteractive={false} fitViewOptions={FIT_VIEW_OPTIONS} />
            </ReactFlow>
          )}

          <div className={`topology-path-guide is-${flow}`} aria-live="polite">
            <div className="topology-path-summary">
              <span className="topology-path-label">{activeFlow.label}</span>
              <ol className="topology-path-stages" aria-label={`${activeFlow.label} stages`}>
                {activeFlow.stages.map((stage, index) => (
                  <li key={stage}>
                    <span className="topology-stage-number">{index + 1}</span>
                    <strong>{stage}</strong>
                    {index < activeFlow.stages.length - 1 ? (
                      <RightOutlined className="topology-stage-arrow" aria-hidden="true" />
                    ) : null}
                  </li>
                ))}
              </ol>
            </div>
            <small>
              {emphasizedSystems > 0
                ? selectedId
                  ? `${emphasizedSystems} connected ${emphasizedSystems === 1 ? "system" : "systems"} highlighted`
                  : `${emphasizedSystems} matching ${emphasizedSystems === 1 ? "system" : "systems"} emphasized`
                : focusRequested
                  ? "No matches in this journey"
                  : `${flowNodes.length} systems in this view · select a card for details`}
            </small>
          </div>

          <div className="topology-legend">
            <span className="legend-direction">
              <RightOutlined aria-hidden="true" /> Arrowheads show traffic direction
            </span>
            {flow === "all" || flow === "mt" ? (
              <span>
                <i className="legend-line is-mt" /> MT flow
              </span>
            ) : null}
            {flow === "all" || flow === "mo" ? (
              <span>
                <i className="legend-line is-mo" /> MO flow
              </span>
            ) : null}
            {flow === "all" || flow === "dlr" ? (
              <span>
                <i className="legend-line is-dlr" /> DLR flow
              </span>
            ) : null}
            <span>
              <i className="legend-line is-config" /> Configuration reference (no traffic)
            </span>
            <span>
              <i className="topology-dot is-ok" /> Healthy
            </span>
            <span>
              <i className="topology-dot is-warning" /> Warning
            </span>
            <span>
              <i className="topology-dot is-down" /> Unhealthy
            </span>
            <span>
              <i className="topology-dot is-unknown" /> Unknown
            </span>
          </div>
          <AnalyticsPanel
            analytics={graph.analytics}
            open={analyticsOpen}
            onToggle={() => setAnalyticsOpen((value) => !value)}
            onSelect={setSelectedId}
            selectedId={selectedId}
          />
        </div>

        <Inspector
          node={selected}
          children={selectedChildren}
          graph={graph}
          onClose={() => setSelectedId(null)}
          onSelect={setSelectedId}
        />
      </div>
    </div>
  );
};

/**
 * ReactFlowProvider keeps the viewport helpers and canvas in the same store.
 */
export const TopologyPage = () => (
  <ReactFlowProvider>
    <TopologyCanvas />
  </ReactFlowProvider>
);

export default TopologyPage;
