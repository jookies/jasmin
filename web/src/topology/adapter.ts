import { MarkerType, type Edge as FlowEdge, type Node as FlowNode } from "@xyflow/react";

import { CARD_WIDTH, INLINE_CHILDREN, type LayoutResult } from "./layout";
import type { TopologyFlowEdgeData } from "./TopologyFlowEdge";
import {
  TOPOLOGY_PORT_POSITIONS,
  distributedTopologyPortIndices,
  topologyPortId,
  type TopologyEdgeSide,
} from "./ports";
import type { RateTable } from "./useTopology";
import type {
  FlowFilter,
  Lens,
  TopologyEdge,
  TopologyGraph,
  TopologyNode,
} from "./types";

/**
 * This module is the only place that knows React Flow's shape exists.
 *
 * Everything upstream — the server document, the layout, the node components'
 * own props — is ours. If the rendering library is ever replaced, this file is
 * the rewrite, not the feature.
 */

/** Colour roles, matching the education diagrams so the console reads as one system. */
const OUTBOUND = "#0f766e"; // MT: the submit side
const INBOUND = "#5b5bd6"; // MO and delivery receipts: the return side
const CONFIG = "#808d89"; // configuration references, which carry no traffic
const FAULT = "#d13415";

const CARD_MIN_HEIGHT = 92;
const DETOUR_CLEARANCE = 24;
const DETOUR_TRACK_GAP = 20;
const DETOUR_CARD_GAP = 10;

export type NodeData = {
  node: TopologyNode;
  children: TopologyNode[];
  hiddenChildren: number;
  rates: Record<string, number>;
  lens: Lens;
  selected: boolean;
  highlighted: boolean;
  dimmed: boolean;
  metricsStale: boolean;
  /** Queue holding work that is not going down across consecutive polls. */
  stalled: boolean;
};

export type AdapterOptions = {
  graph: TopologyGraph;
  layout: LayoutResult;
  rates: RateTable;
  lens: Lens;
  flow: FlowFilter;
  selectedId: string | null;
  stalledQueues: Set<string>;
  /** Node ids to emphasise, e.g. the members of a selected problem bucket. */
  highlighted: Set<string>;
};

const edgeVisible = (edge: TopologyEdge, flow: FlowFilter): boolean => {
  if (flow === "all") return true;
  // Configuration edges are structure, not traffic: they stay drawn under every
  // flow filter, because hiding them would make a filtered view look like the
  // routing table had disappeared.
  if (edge.kind === "config") return true;
  return edge.kind === flow;
};

/**
 * The flow switch is a focus control, not only an edge-style control. Keeping
 * every card on screen after choosing "MO" or "DLR" was the main reason those
 * views still looked like the complete topology and fitted at an unreadable
 * zoom. This resolves child endpoints to their group and returns only the
 * systems that participate in the chosen traffic path. Configuration
 * relationships are pulled in when they touch that path, so an MT view still
 * explains which accounts and policy library feed its routes.
 */
export const visibleTopologyNodeIds = (
  graph: TopologyGraph,
  flow: FlowFilter,
): Set<string> => {
  const parentOf = new Map<string, string>();
  const topLevel = new Set<string>();
  for (const node of graph.nodes) {
    if (node.parent) parentOf.set(node.id, node.parent);
    else topLevel.add(node.id);
  }
  if (flow === "all") return topLevel;

  const resolve = (id: string): string => parentOf.get(id) ?? id;
  const visible = new Set<string>();
  for (const edge of [...graph.edges].sort((left, right) => left.id.localeCompare(right.id))) {
    if (edge.kind !== flow) continue;
    visible.add(resolve(edge.from));
    visible.add(resolve(edge.to));
  }

  // A configuration chain can be more than one hop (for example an account
  // group feeding a route group), so expand to a fixed point rather than only
  // checking the first neighbour.
  let changed = true;
  while (changed) {
    changed = false;
    for (const edge of graph.edges) {
      if (edge.kind !== "config") continue;
      const from = resolve(edge.from);
      const to = resolve(edge.to);
      if (!visible.has(from) && !visible.has(to)) continue;
      if (!visible.has(from)) {
        visible.add(from);
        changed = true;
      }
      if (!visible.has(to)) {
        visible.add(to);
        changed = true;
      }
    }
  }

  // Supporting services are useful in the complete system view, but they do
  // not carry a message. Pulling the core's configuration edge into MT would
  // otherwise recreate the distant third lane that the journey view is meant
  // to remove.
  for (const node of graph.nodes) {
    if (!node.parent && node.lane === "infra") visible.delete(node.id);
  }

  return visible;
};

const edgeColor = (edge: TopologyEdge): string => {
  if (edge.degraded) return FAULT;
  switch (edge.kind) {
    case "mt":
      return OUTBOUND;
    case "mo":
    case "dlr":
      return INBOUND;
    default:
      return CONFIG;
  }
};

/**
 * edgeWidth scales with observed throughput under the traffic lens only. Under
 * the other lenses every edge is drawn at its base weight, so thickness always
 * means one thing.
 */
const edgeWidth = (edge: TopologyEdge, lens: Lens, rate: number | undefined): number => {
  if (edge.kind === "config") return 1.25;
  if (lens !== "traffic" || !rate || rate <= 0) return 2.2;
  return Math.min(6, 2.2 + Math.log10(1 + rate));
};

/**
 * Attach an edge to the side that matches its actual spatial direction.
 * MT usually reads left-to-right, while MO/DLR return paths often move left or
 * down. Choosing handles from the relative card positions keeps the final
 * segment—and therefore the arrowhead—pointed toward the destination instead
 * of curling around and appearing to point back out of it.
 */
type CardRect = { x: number; y: number; width: number; height: number };

const cardRect = (layout: LayoutResult, id: string): CardRect => {
  const position = layout.positions[id] ?? { x: 0, y: 0 };
  return {
    ...position,
    width: CARD_WIDTH,
    height: layout.heights[id] ?? CARD_MIN_HEIGHT,
  };
};

const rectCenter = (rect: CardRect) => ({
  x: rect.x + rect.width / 2,
  y: rect.y + rect.height / 2,
});

/**
 * Choose sides from rectangle separation, not top-left-point deltas. A tall
 * group next to a short status card can have very different top-left y values
 * while still being an unambiguously horizontal relationship.
 */
export const topologyEdgeSides = (
  source: CardRect,
  target: CardRect,
): { sourceSide: TopologyEdgeSide; targetSide: TopologyEdgeSide } => {
  const sourceCenter = rectCenter(source);
  const targetCenter = rectCenter(target);
  const rightGap = target.x - (source.x + source.width);
  const leftGap = source.x - (target.x + target.width);
  const downGap = target.y - (source.y + source.height);
  const upGap = source.y - (target.y + target.height);
  const horizontalGap = Math.max(0, rightGap, leftGap);
  const verticalGap = Math.max(0, downGap, upGap);

  if (horizontalGap > 0 && horizontalGap >= verticalGap) {
    return targetCenter.x >= sourceCenter.x
      ? { sourceSide: "right", targetSide: "left" }
      : { sourceSide: "left", targetSide: "right" };
  }
  if (verticalGap > 0) {
    return targetCenter.y >= sourceCenter.y
      ? { sourceSide: "bottom", targetSide: "top" }
      : { sourceSide: "top", targetSide: "bottom" };
  }

  const normalisedX = Math.abs(targetCenter.x - sourceCenter.x) / Math.max(source.width, target.width);
  const normalisedY = Math.abs(targetCenter.y - sourceCenter.y) / Math.max(source.height, target.height);
  if (normalisedY > normalisedX) {
    return targetCenter.y >= sourceCenter.y
      ? { sourceSide: "bottom", targetSide: "top" }
      : { sourceSide: "top", targetSide: "bottom" };
  }
  return targetCenter.x >= sourceCenter.x
    ? { sourceSide: "right", targetSide: "left" }
    : { sourceSide: "left", targetSide: "right" };
};

const edgeHandles = (
  layout: LayoutResult,
  from: string,
  to: string,
): { sourceSide: TopologyEdgeSide; targetSide: TopologyEdgeSide } =>
  topologyEdgeSides(cardRect(layout, from), cardRect(layout, to));

type DetourCandidate = {
  side: "above" | "below";
  coordinate: number;
  spanStart: number;
  spanEnd: number;
};

/**
 * Detect a card sitting in the horizontal corridor between two endpoints.
 * Skip-column links (notably HTTP/SMPP -> core and a broken route -> missing
 * connector) then get a real bypass track instead of disappearing under that
 * card and looking as though they started from it.
 */
const horizontalDetour = (
  source: CardRect,
  target: CardRect,
  blockers: CardRect[],
): DetourCandidate | undefined => {
  const sourceCenter = rectCenter(source);
  const targetCenter = rectCenter(target);
  const leftToRight = targetCenter.x >= sourceCenter.x;
  const spanStart = leftToRight ? source.x + source.width : target.x + target.width;
  const spanEnd = leftToRight ? target.x : source.x;
  if (spanEnd <= spanStart) return undefined;

  const corridorTop = Math.min(sourceCenter.y, targetCenter.y) - 12;
  const corridorBottom = Math.max(sourceCenter.y, targetCenter.y) + 12;
  const crossed = blockers.filter((rect) => {
    const overlapsX = rect.x < spanEnd - 1 && rect.x + rect.width > spanStart + 1;
    const overlapsY = rect.y < corridorBottom && rect.y + rect.height > corridorTop;
    return overlapsX && overlapsY;
  });
  if (crossed.length === 0) return undefined;

  const obstacleTop = Math.min(...crossed.map((rect) => rect.y));
  const obstacleBottom = Math.max(...crossed.map((rect) => rect.y + rect.height));
  const endpointMidpoint = (sourceCenter.y + targetCenter.y) / 2;
  const obstacleMidpoint = (obstacleTop + obstacleBottom) / 2;
  const side = endpointMidpoint <= obstacleMidpoint ? "above" : "below";
  return {
    side,
    coordinate:
      side === "above"
        ? obstacleTop - DETOUR_CLEARANCE
        : obstacleBottom + DETOUR_CLEARANCE,
    spanStart,
    spanEnd,
  };
};

const edgeTrackOrder = (edge: TopologyEdge): number => {
  if (edge.degraded) return 1;
  if (edge.kind === "config") return 2;
  if (edge.kind === "mt") return 0;
  return edge.kind === "mo" ? 3 : 4;
};

const edgeStepPosition = (edge: TopologyEdge): number => {
  if (edge.degraded) return 0.68;
  switch (edge.kind) {
    case "config":
      return 0.34;
    case "mt":
      return 0.46;
    case "mo":
      return 0.56;
    case "dlr":
      return 0.64;
  }
};

export const toFlowNodes = (options: AdapterOptions): FlowNode<NodeData>[] => {
  const { graph, layout, rates, lens, flow, selectedId, highlighted } = options;
  const visible = visibleTopologyNodeIds(graph, flow);

  const childrenOf = new Map<string, TopologyNode[]>();
  for (const node of graph.nodes) {
    if (!node.parent) continue;
    childrenOf.set(node.parent, [...(childrenOf.get(node.parent) ?? []), node]);
  }
  const hasVisibleHighlight = graph.nodes.some((node) => {
    if (!highlighted.has(node.id)) return false;
    return visible.has(node.parent ?? node.id);
  });

  return graph.nodes
    .filter((node) => !node.parent && visible.has(node.id))
    .map((node) => {
      const children = childrenOf.get(node.id) ?? [];
      const position = layout.positions[node.id] ?? { x: 0, y: 0 };
      const selected = selectedId === node.id || children.some((child) => child.id === selectedId);
      const highlightedHere =
        highlighted.has(node.id) || children.some((child) => highlighted.has(child.id));
      return {
        id: node.id,
        type: "topologyCard",
        position,
        // Cards are positioned by the layout, never by the pointer: this is a
        // generated picture of live state, and a hand-moved node would be a
        // lie the next poll silently corrects.
        draggable: false,
        connectable: false,
        style: { width: CARD_WIDTH },
        data: {
          node,
          children: children.slice(0, INLINE_CHILDREN),
          hiddenChildren: Math.max(0, children.length - INLINE_CHILDREN),
          rates: rates[node.id] ?? {},
          lens,
          selected,
          highlighted: highlightedHere,
          dimmed: hasVisibleHighlight && !highlightedHere && !selected,
          metricsStale: Boolean(graph.metrics_stale),
          stalled: options.stalledQueues.has(node.id),
        },
      };
    });
};

export const toFlowEdges = (options: AdapterOptions): FlowEdge[] => {
  const { graph, layout, rates, lens, flow, selectedId, highlighted } = options;
  const visible = visibleTopologyNodeIds(graph, flow);

  const parentOf = new Map<string, string>();
  for (const node of graph.nodes) {
    if (node.parent) parentOf.set(node.id, node.parent);
  }
  const resolve = (id: string) => parentOf.get(id) ?? id;
  const focusActive = [...highlighted].some((id) => visible.has(resolve(id)));

  // Several child-level edges can collapse onto the same pair of cards (every
  // route into one connector group). They are merged, keeping the worst state,
  // so the canvas shows one line per relationship rather than a bundle.
  const merged = new Map<string, { edge: TopologyEdge; from: string; to: string; count: number }>();
  for (const edge of [...graph.edges].sort((left, right) => left.id.localeCompare(right.id))) {
    if (!edgeVisible(edge, flow)) continue;
    const from = resolve(edge.from);
    const to = resolve(edge.to);
    if (from === to) continue;
    if (!visible.has(from) || !visible.has(to)) continue;

    const key = `${from}->${to}:${edge.kind}`;
    const existing = merged.get(key);
    if (!existing) {
      merged.set(key, { edge, from, to, count: 1 });
      continue;
    }
    existing.count += 1;
    // A single broken path in a bundle makes the bundle broken: the operator
    // needs to see that something on this hop cannot carry traffic.
    if (edge.degraded && !existing.edge.degraded) existing.edge = edge;
  }

  const relationships = [...merged.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => ({
      key,
      ...value,
      ...edgeHandles(layout, value.from, value.to),
    }));

  const visibleRects = graph.nodes
    .filter((node) => !node.parent && visible.has(node.id))
    .map((node) => ({ id: node.id, rect: cardRect(layout, node.id) }));
  const detourCandidates = relationships
    .flatMap((relationship) => {
      const horizontal =
        (relationship.sourceSide === "left" || relationship.sourceSide === "right") &&
        (relationship.targetSide === "left" || relationship.targetSide === "right");
      if (!horizontal) return [];
      const candidate = horizontalDetour(
        cardRect(layout, relationship.from),
        cardRect(layout, relationship.to),
        visibleRects
          .filter(({ id }) => id !== relationship.from && id !== relationship.to)
          .map(({ rect }) => rect),
      );
      return candidate ? [{ relationship, candidate }] : [];
    })
    .sort(
      (left, right) =>
        edgeTrackOrder(left.relationship.edge) - edgeTrackOrder(right.relationship.edge) ||
        left.relationship.key.localeCompare(right.relationship.key),
    );

  const detourByKey = new Map<string, number>();
  const placedDetours: Array<DetourCandidate> = [];
  const detourHitsCard = (
    relationship: (typeof relationships)[number],
    candidate: DetourCandidate,
    coordinate: number,
  ) =>
    visibleRects.some(({ id, rect }) => {
      if (id === relationship.from || id === relationship.to) return false;
      const overlapsTrack =
        rect.x - DETOUR_CARD_GAP < candidate.spanEnd &&
        rect.x + rect.width + DETOUR_CARD_GAP > candidate.spanStart;
      const overlapsY =
        coordinate > rect.y - DETOUR_CARD_GAP &&
        coordinate < rect.y + rect.height + DETOUR_CARD_GAP;
      return overlapsTrack && overlapsY;
    });
  for (const { relationship, candidate } of detourCandidates) {
    const direction = candidate.side === "above" ? -1 : 1;
    let coordinate = candidate.coordinate;
    const overlaps = (left: DetourCandidate, right: DetourCandidate) =>
      left.spanStart < right.spanEnd && right.spanStart < left.spanEnd;
    while (
      detourHitsCard(relationship, candidate, coordinate) ||
      placedDetours.some(
        (placed) =>
          placed.side === candidate.side &&
          overlaps(placed, candidate) &&
          Math.abs(placed.coordinate - coordinate) < DETOUR_TRACK_GAP,
      )
    ) {
      coordinate += direction * DETOUR_TRACK_GAP;
    }
    detourByKey.set(relationship.key, coordinate);
    placedDetours.push({ ...candidate, coordinate });
  }

  type Terminal = {
    relationship: (typeof relationships)[number];
    role: "source" | "target";
    node: string;
    remote: string;
    side: TopologyEdgeSide;
  };
  const terminalsByCardSide = new Map<string, Terminal[]>();
  const addTerminal = (terminal: Terminal) => {
    const groupKey = `${terminal.node}:${terminal.side}`;
    terminalsByCardSide.set(groupKey, [
      ...(terminalsByCardSide.get(groupKey) ?? []),
      terminal,
    ]);
  };
  for (const relationship of relationships) {
    addTerminal({
      relationship,
      role: "source",
      node: relationship.from,
      remote: relationship.to,
      side: relationship.sourceSide,
    });
    addTerminal({
      relationship,
      role: "target",
      node: relationship.to,
      remote: relationship.from,
      side: relationship.targetSide,
    });
  }

  const portByTerminal = new Map<string, number>();
  const terminalKey = (terminal: Terminal) => `${terminal.relationship.key}:${terminal.role}`;
  for (const terminals of terminalsByCardSide.values()) {
    terminals.sort((left, right) => {
      const leftRemote = layout.positions[left.remote] ?? { x: 0, y: 0 };
      const rightRemote = layout.positions[right.remote] ?? { x: 0, y: 0 };
      const horizontalSide = left.side === "left" || left.side === "right";
      const byRemotePosition = horizontalSide
        ? leftRemote.y - rightRemote.y
        : leftRemote.x - rightRemote.x;
      return (
        byRemotePosition ||
        edgeTrackOrder(left.relationship.edge) - edgeTrackOrder(right.relationship.edge) ||
        left.relationship.key.localeCompare(right.relationship.key)
      );
    });

    const portIndices = distributedTopologyPortIndices(terminals.length);
    terminals.forEach((terminal, index) => {
      const portIndex = portIndices[index] ?? TOPOLOGY_PORT_POSITIONS.length - 1;
      portByTerminal.set(terminalKey(terminal), portIndex);
    });
  }

  return relationships.map(({ key, edge, from, to, count, sourceSide, targetSide }) => {
    const rate = rates[edge.from]?.submit_success ?? rates[from]?.submit_success;
    const color = edgeColor(edge);
    const endpointsHighlighted = highlighted.has(from) && highlighted.has(to);
    const emphasised = selectedId
      ? endpointsHighlighted
      : highlighted.has(edge.from) ||
        highlighted.has(edge.to) ||
        highlighted.has(from) ||
        highlighted.has(to);
    const label = edge.degraded ? edge.label : count > 1 ? `${count} routes` : undefined;
    const sourcePort = portByTerminal.get(`${key}:source`) ?? 3;
    const targetPort = portByTerminal.get(`${key}:target`) ?? 3;
    const averagePort = (sourcePort + targetPort) / 2;
    const detourY = detourByKey.get(key);
    const edgeData: TopologyFlowEdgeData = {
      labelSide:
        detourY === undefined
          ? averagePort > (TOPOLOGY_PORT_POSITIONS.length - 1) / 2
            ? 1
            : -1
          : detourY < Math.min(cardRect(layout, from).y, cardRect(layout, to).y)
            ? -1
            : 1,
      stepPosition: edgeStepPosition(edge),
      detourY,
    };

    return {
      id: key,
      source: from,
      target: to,
      sourceHandle: topologyPortId("source", sourceSide, sourcePort),
      targetHandle: topologyPortId("target", targetSide, targetPort),
      type: "topologyFlow",
      data: edgeData,
      // DLR is the return lane and is dashed everywhere in this console; a
      // degraded edge is dashed too, and carries a label saying why.
      animated: edge.kind !== "config" && !edge.degraded && lens === "traffic",
      label,
      labelStyle: { color, fontSize: 11, fontWeight: 650 },
      // Configuration references explain ownership and routing policy but do
      // not carry messages. Removing their arrowheads prevents operators from
      // reading them as traffic. Traffic arrows are deliberately oversized so
      // direction survives the fitted, whole-journey zoom.
      markerEnd:
        edge.kind === "config"
          ? undefined
          : { type: MarkerType.ArrowClosed, color, width: 14, height: 14 },
      style: {
        stroke: color,
        strokeWidth: edgeWidth(edge, lens, rate) + (focusActive && emphasised ? 0.8 : 0),
        strokeDasharray:
          edge.kind === "config"
            ? "3 5"
            : edge.kind === "dlr" || edge.degraded
              ? "7 5"
              : undefined,
        opacity: edge.inactive
          ? 0.3
          : focusActive
            ? emphasised
              ? 1
              : 0.12
            : edge.kind === "config"
              ? 1
              : 0.9,
      },
    };
  });
};
