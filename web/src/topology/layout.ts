import dagre from "@dagrejs/dagre";

import type { Column, Lane, TopologyEdge, TopologyNode } from "./types";

/**
 * Layout is a hybrid, on purpose.
 *
 * dagre solves the part that is genuinely hard — ordering nodes within a rank
 * so edges cross as little as possible — and we solve the part that is trivial
 * and that dagre would get "right" in a way operators would find wrong: which
 * column a node belongs to. The server already labels every node with its stage
 * in the pipeline, and that reading order (who is talking to us → what we decide
 * → the broker → what we hand it to → where it lands) is the whole point of the
 * picture. So we take dagre's cross-axis ordering and impose our own x.
 *
 * Only top-level nodes are laid out. Children live inside their group's card,
 * so an edge that names a child is resolved to that child's group for layout.
 */

/** Left-to-right column order. Anything unrecognised sorts to the end. */
const COLUMN_ORDER: Column[] = [
  "ingress",
  "policy",
  "core",
  "queues",
  "egress",
  "carriers",
  "delivery",
];

/**
 * Lane order, top to bottom.
 *
 * Infrastructure sits above the traffic map: it is supporting context rather
 * than another step in the message path, and the core column already has useful
 * room above it. MT then reads left to right, with the MO/DLR return path below
 * so the two traffic directions never interleave.
 */
const LANE_ORDER: Lane[] = ["infra", "mt", "return"];

/** Vertical space between lanes — larger than ROW_GAP so the bands read apart. */
const LANE_GAP = 64;

export const CARD_WIDTH = 208;
const COLUMN_GAP = 64;
const ROW_GAP = 24;
/** Supporting configuration sits close enough to read as context, not traffic. */
const SUPPORT_GAP = 48;

/** Keep layout geometry in lockstep with the fixed card rows in topology.css. */
const CARD_BASE_HEIGHT = 76; // 54px header + 20px padding + 2px border
const CARD_MIN_HEIGHT = 92;
const CHILD_LIST_TOP = 8;
const CHILD_ROW_HEIGHT = 26;
const CHILD_ROW_GAP = 4;
const MORE_ROW_HEIGHT = 20;

/**
 * How many children a group card shows inline.
 *
 * Three, and the rest go behind a "+N more" that opens the inspector rather
 * than growing the card. A card that grew on expand would change the graph's
 * geometry, force a relayout, and move every other node on the canvas — the
 * exact jitter the structure hash exists to prevent.
 */
export const INLINE_CHILDREN = 3;

export type Position = { x: number; y: number };
export type LayoutResult = {
  positions: Record<string, Position>;
  /** Card heights, so the renderer and the layout agree on geometry. */
  heights: Record<string, number>;
};

export const cardHeight = (node: TopologyNode, childCount: number): number => {
  if (node.kind !== "group") return CARD_MIN_HEIGHT;
  const inline = Math.min(childCount, INLINE_CHILDREN);
  const children = inline > 0
    ? CHILD_LIST_TOP + inline * CHILD_ROW_HEIGHT + (inline - 1) * CHILD_ROW_GAP
    : 0;
  const more = childCount > INLINE_CHILDREN ? MORE_ROW_HEIGHT : 0;
  return Math.max(CARD_MIN_HEIGHT, CARD_BASE_HEIGHT + children + more);
};

const columnIndex = (column: Column): number => {
  // Infrastructure is supporting context for the gateway and belongs above
  // the core stage, not in a distant eighth column of its own.
  if (column === "infra") return COLUMN_ORDER.indexOf("core");
  const index = COLUMN_ORDER.indexOf(column);
  return index < 0 ? COLUMN_ORDER.length : index;
};

const byId = (left: TopologyNode, right: TopologyNode): number =>
  left.id < right.id ? -1 : left.id > right.id ? 1 : 0;

type ResolvedEdge = {
  edge: TopologyEdge;
  from: string;
  to: string;
};

/**
 * layoutGraph positions the top-level cards.
 *
 * It is a pure function of the graph's *shape*, never of its numbers, so the
 * page can memoize it on structure_hash and repaint metrics in place. See the
 * note on StructureHash in internal/app/adminweb/topology.go.
 */
export const layoutGraph = (
  nodes: TopologyNode[],
  edges: TopologyEdge[],
): LayoutResult => {
  const parentOf = new Map<string, string>();
  for (const node of nodes) {
    if (node.parent) parentOf.set(node.id, node.parent);
  }
  const resolve = (id: string): string => parentOf.get(id) ?? id;

  const topLevel = nodes.filter((node) => !node.parent);
  const topLevelById = new Map(topLevel.map((node) => [node.id, node]));
  const childCounts = new Map<string, number>();
  for (const node of nodes) {
    if (!node.parent) continue;
    childCounts.set(node.parent, (childCounts.get(node.parent) ?? 0) + 1);
  }

  const heights: Record<string, number> = {};
  for (const node of topLevel) {
    heights[node.id] = cardHeight(node, node.child_count ?? childCounts.get(node.id) ?? 0);
  }

  const graph = new dagre.graphlib.Graph({ multigraph: true });
  graph.setGraph({ rankdir: "LR", nodesep: ROW_GAP, ranksep: COLUMN_GAP, marginx: 24, marginy: 24 });
  graph.setDefaultEdgeLabel(() => ({}));

  // dagre's ordering pass is sensitive to insertion order, so nodes and edges
  // are fed in id order rather than document order. The server's structure hash
  // is computed over a *sorted* set, which means two hash-identical documents
  // may legitimately arrive with their arrays in different orders — without
  // this, the layout memoization would hold while the canvas silently
  // reshuffled underneath it.
  for (const node of [...topLevel].sort((left, right) => (left.id < right.id ? -1 : 1))) {
    graph.setNode(node.id, { width: CARD_WIDTH, height: heights[node.id] });
  }

  // Self-edges (both endpoints inside one group) and duplicates carry no layout
  // information; dagre handles them, but skipping keeps the ranking honest.
  // Keep the resolved relationships too: the composition pass uses them to
  // separate cards that carry traffic from cards that only describe config.
  const resolvedEdges: ResolvedEdge[] = [];
  const relationshipKeys = new Set<string>();
  const pairs = new Set<string>();
  for (const edge of [...edges].sort((left, right) =>
    left.id < right.id ? -1 : left.id > right.id ? 1 : 0,
  )) {
    const from = resolve(edge.from);
    const to = resolve(edge.to);
    if (from === to) continue;
    if (!graph.hasNode(from) || !graph.hasNode(to)) continue;
    const relationshipKey = `${from}\u0000${to}\u0000${edge.kind}`;
    if (!relationshipKeys.has(relationshipKey)) {
      relationshipKeys.add(relationshipKey);
      resolvedEdges.push({ edge, from, to });
    }
    pairs.add(from + "\u0000" + to);
  }
  for (const pair of [...pairs].sort()) {
    const [from, to] = pair.split("\u0000");
    graph.setEdge(from, to);
  }

  dagre.layout(graph);

  const laneOf = (node: TopologyNode): Lane => node.lane ?? "mt";
  const incident = new Map<string, ResolvedEdge[]>();
  for (const relationship of resolvedEdges) {
    incident.set(relationship.from, [
      ...(incident.get(relationship.from) ?? []),
      relationship,
    ]);
    incident.set(relationship.to, [
      ...(incident.get(relationship.to) ?? []),
      relationship,
    ]);
  }

  // Configuration libraries are useful context but are not pipeline stages.
  // Pulling them out of the MT lane prevents a tall ingress stack from setting
  // the zoom level for the entire journey. Infrastructure is deliberately
  // excluded: its own lane already gives it the correct "above traffic" role.
  const support = new Set(
    topLevel
      .filter((node) => {
        if (laneOf(node) !== "mt") return false;
        const relationships = incident.get(node.id) ?? [];
        return (
          relationships.length > 0 &&
          relationships.every(({ edge }) => edge.kind === "config")
        );
      })
      .map((node) => node.id),
  );

  const dagreOrder = (left: TopologyNode, right: TopologyNode): number => {
    const byRank = (graph.node(left.id)?.y ?? 0) - (graph.node(right.id)?.y ?? 0);
    return byRank || byId(left, right);
  };

  const trafficEdges = (node: TopologyNode): ResolvedEdge[] =>
    (incident.get(node.id) ?? []).filter(({ edge }) => edge.kind !== "config");

  /**
   * Pick the card that actually continues through a stage as its spine anchor.
   * A card joined to both the previous and next semantic columns outranks a
   * terminal/exception card joined on only one side. Ties remain deterministic
   * and follow dagre's crossing-minimised order.
   */
  const anchorFor = (members: TopologyNode[]): TopologyNode =>
    [...members].sort((left, right) => {
      const score = (node: TopologyNode) => {
        const relationships = trafficEdges(node);
        const neighbourColumns = new Set<number>();
        for (const relationship of relationships) {
          const remoteId = relationship.from === node.id ? relationship.to : relationship.from;
          const remote = topLevelById.get(remoteId);
          if (remote) neighbourColumns.add(columnIndex(remote.column));
        }
        // Layout depends only on structure. A fault or inactive route may
        // repaint a path, but it must never move cards under the operator.
        return [neighbourColumns.size, relationships.length] as const;
      };
      const leftScore = score(left);
      const rightScore = score(right);
      for (let index = 0; index < leftScore.length; index += 1) {
        if (leftScore[index] !== rightScore[index]) {
          return rightScore[index] - leftScore[index];
        }
      }
      return dagreOrder(left, right);
    })[0];

  // Impose the pipeline's shape ourselves, using dagre only for a sensible
  // order within each semantic column. The card which carries a stage through
  // the pipeline is centred on one shared spine; exception cards retain their
  // dagre order above or below it. This keeps the live journey straight even
  // when cards in neighbouring columns have very different heights.
  const positions: Record<string, Position> = {};

  type LaneGeometry = {
    height: number;
    spineBottom: number;
    bottomBySlot: Map<number, number>;
  };

  const positionLane = (members: TopologyNode[], laneTop: number): LaneGeometry => {
    if (members.length === 0) {
      return { height: 0, spineBottom: laneTop, bottomBySlot: new Map() };
    }

    const columns = COLUMN_ORDER.map((_, index) =>
      members.filter((node) => columnIndex(node.column) === index).sort(dagreOrder),
    );
    const anchors = columns.map((column) => (column.length > 0 ? anchorFor(column) : null));

    let roomAboveSpine = 0;
    for (let index = 0; index < columns.length; index += 1) {
      const column = columns[index];
      const anchor = anchors[index];
      if (!anchor) continue;
      const anchorIndex = column.indexOf(anchor);
      const above = column
        .slice(0, anchorIndex)
        .reduce((height, node) => height + heights[node.id] + ROW_GAP, 0);
      roomAboveSpine = Math.max(roomAboveSpine, above + heights[anchor.id] / 2);
    }
    const spineCenterY = laneTop + roomAboveSpine;

    let laneBottom = laneTop;
    let spineBottom = laneTop;
    const bottomBySlot = new Map<number, number>();
    for (let index = 0; index < columns.length; index += 1) {
      const column = columns[index];
      const anchor = anchors[index];
      if (!anchor) continue;
      const anchorIndex = column.indexOf(anchor);
      const anchorY = spineCenterY - heights[anchor.id] / 2;

      let y = anchorY;
      for (let memberIndex = anchorIndex - 1; memberIndex >= 0; memberIndex -= 1) {
        const node = column[memberIndex];
        y -= ROW_GAP + heights[node.id];
        positions[node.id] = {
          x: index * (CARD_WIDTH + COLUMN_GAP),
          y,
        };
      }

      positions[anchor.id] = {
        x: index * (CARD_WIDTH + COLUMN_GAP),
        y: anchorY,
      };
      spineBottom = Math.max(spineBottom, anchorY + heights[anchor.id]);
      y = anchorY + heights[anchor.id] + ROW_GAP;
      for (let memberIndex = anchorIndex + 1; memberIndex < column.length; memberIndex += 1) {
        const node = column[memberIndex];
        positions[node.id] = {
          x: index * (CARD_WIDTH + COLUMN_GAP),
          y,
        };
        y += heights[node.id] + ROW_GAP;
      }
      const columnBottom = y - ROW_GAP;
      bottomBySlot.set(index, columnBottom);
      laneBottom = Math.max(laneBottom, columnBottom);
    }
    return { height: laneBottom - laneTop, spineBottom, bottomBySlot };
  };

  /** Median traffic stage configured by a support card, with its own stage as fallback. */
  const preferredSupportColumn = (node: TopologyNode): number => {
    const neighbours = (incident.get(node.id) ?? [])
      .filter(({ edge }) => edge.kind === "config")
      .map((relationship) =>
        relationship.from === node.id ? relationship.to : relationship.from,
      )
      .filter((id) => !support.has(id))
      .map((id) => topLevelById.get(id))
      .filter((candidate): candidate is TopologyNode => Boolean(candidate))
      .map((candidate) => columnIndex(candidate.column))
      .sort((left, right) => left - right);
    return neighbours.length > 0
      ? neighbours[Math.floor(neighbours.length / 2)]
      : columnIndex(node.column);
  };

  /**
   * Fill one compact row before opening a second. Slots stay on the same fixed
   * horizontal grid as semantic columns and are chosen nearest the stage the
   * support card configures. The current document has three such cards; the
   * unbounded slot search also keeps unusually large documents overlap-free.
   */
  const positionSupport = (
    members: TopologyNode[],
    minimumTop: number,
    blockedBottomBySlot = new Map<number, number>(),
  ): number => {
    if (members.length === 0) return minimumTop;
    const sorted = [...members].sort((left, right) => {
      const byPreferred = preferredSupportColumn(left) - preferredSupportColumn(right);
      return byPreferred || byId(left, right);
    });
    const maxFirstRow = COLUMN_ORDER.length;
    const used = [new Set<number>(), new Set<number>()];
    const placements: Array<{ node: TopologyNode; row: number; slot: number }> = [];

    for (let index = 0; index < sorted.length; index += 1) {
      const node = sorted[index];
      const row = index < maxFirstRow ? 0 : 1;
      const preferred = preferredSupportColumn(node);
      let slot: number | undefined;
      for (let distance = 0; distance < COLUMN_ORDER.length && slot === undefined; distance += 1) {
        const candidates = distance === 0
          ? [preferred]
          : [preferred - distance, preferred + distance];
        slot = candidates.find(
          (candidate) =>
            candidate >= 0 &&
            candidate < COLUMN_ORDER.length &&
            !used[row].has(candidate),
        );
      }
      // More than seven support cards in one row is unusual, but extending the
      // same grid is safer than overlapping or silently adding a third row.
      for (let distance = 1; slot === undefined; distance += 1) {
        slot = [preferred - distance, preferred + distance].find(
          (candidate) => !used[row].has(candidate),
        );
      }
      used[row].add(slot);
      placements.push({ node, row, slot });
    }

    // Keep deterministic semantic order visible from left to right. Slot
    // selection above finds the best footprint; this pass assigns those same
    // slots monotonically rather than producing centre, left, right order.
    for (const row of [0, 1]) {
      const inRow = placements.filter((placement) => placement.row === row);
      const slots = inRow.map((placement) => placement.slot).sort((left, right) => left - right);
      inRow.forEach((placement, index) => {
        placement.slot = slots[index];
      });
    }

    const rowHeights = [0, 0];
    for (const placement of placements) {
      rowHeights[placement.row] = Math.max(
        rowHeights[placement.row],
        heights[placement.node.id],
      );
    }
    const blockedBottom = Math.max(
      ...placements.map(({ slot }) => blockedBottomBySlot.get(slot) ?? Number.NEGATIVE_INFINITY),
    );
    const supportTop = Math.max(
      minimumTop,
      Number.isFinite(blockedBottom) ? blockedBottom + SUPPORT_GAP : minimumTop,
    );
    const rowTops = [supportTop, supportTop + rowHeights[0] + ROW_GAP];
    for (const { node, row, slot } of placements) {
      positions[node.id] = {
        x: slot * (CARD_WIDTH + COLUMN_GAP),
        y: rowTops[row] + (rowHeights[row] - heights[node.id]) / 2,
      };
    }
    return rowHeights[1] > 0
      ? rowTops[1] + rowHeights[1]
      : rowTops[0] + rowHeights[0];
  };

  let laneTop = 0;
  for (const lane of LANE_ORDER) {
    const members = topLevel.filter(
      (node) => laneOf(node) === lane && !support.has(node.id),
    );
    const geometry = positionLane(members, laneTop);
    let laneBottom = laneTop + geometry.height;

    if (lane === "mt" && support.size > 0) {
      const supportBottom = positionSupport(
        topLevel.filter((node) => support.has(node.id)),
        geometry.spineBottom + (geometry.height > 0 ? SUPPORT_GAP : 0),
        geometry.bottomBySlot,
      );
      laneBottom = Math.max(laneBottom, supportBottom);
    }
    if (laneBottom > laneTop) laneTop = laneBottom + LANE_GAP;
  }

  // A node in an unrecognised column still needs a position rather than
  // silently landing at the origin under everything else.
  for (const node of topLevel) {
    if (!positions[node.id]) positions[node.id] = { x: 0, y: laneTop };
  }

  return { positions, heights };
};
