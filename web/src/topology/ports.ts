import { Position } from "@xyflow/react";

/**
 * Four ports are enough for the collapsed card relationships in this graph.
 *
 * Their positions are a mix of an absolute corner clearance and a fluid
 * middle interval. The shortest topology card is 92px tall, so even there the
 * centres stay at least 18px apart: a 14px marker plus 4px of breathing room.
 * On taller group cards the middle ports expand with the card instead of
 * bunching around its centre.
 */
export const TOPOLOGY_PORT_POSITIONS = [
  "18px",
  "calc(33.333% + 6px)",
  "calc(66.667% - 6px)",
  "calc(100% - 18px)",
] as const;

export const TOPOLOGY_PORT_CLEARANCE = 18;

export type TopologyEdgeSide = "left" | "right" | "top" | "bottom";
export type TopologyEdgeRole = "source" | "target";

export const topologyPortId = (
  role: TopologyEdgeRole,
  side: TopologyEdgeSide,
  index: number,
) => `${role}-${side}-${index}`;

export const topologyPortPosition: Record<TopologyEdgeSide, Position> = {
  left: Position.Left,
  right: Position.Right,
  top: Position.Top,
  bottom: Position.Bottom,
};

export const topologyPortStyle = (
  side: TopologyEdgeSide,
  position: (typeof TOPOLOGY_PORT_POSITIONS)[number],
) =>
  side === "left" || side === "right"
    ? { top: position }
    : { left: position };

/** Pixel coordinates corresponding to the CSS positions above. */
export const topologyPortCoordinates = (length: number): number[] => {
  const inner = Math.max(0, length - TOPOLOGY_PORT_CLEARANCE * 2);
  return [
    TOPOLOGY_PORT_CLEARANCE,
    TOPOLOGY_PORT_CLEARANCE + inner / 3,
    TOPOLOGY_PORT_CLEARANCE + (inner * 2) / 3,
    Math.max(TOPOLOGY_PORT_CLEARANCE, length - TOPOLOGY_PORT_CLEARANCE),
  ];
};

/**
 * Pick distinct, deterministic slots while keeping small groups near the
 * middle of the card. More than four relationships is a data-model fallback;
 * the adapter normally moves different directions onto different card sides.
 */
export const distributedTopologyPortIndices = (count: number): number[] => {
  if (count <= 0) return [];
  if (count === 1) return [1];
  if (count === 2) return [1, 2];
  if (count === 3) return [0, 1, 3];
  return Array.from(
    { length: count },
    (_, index) => Math.min(index, TOPOLOGY_PORT_POSITIONS.length - 1),
  );
};
