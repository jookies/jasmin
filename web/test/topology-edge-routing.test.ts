import assert from "node:assert/strict";
import test from "node:test";

import { Position } from "@xyflow/react";

import { horizontalDetourGeometry, type TopologyFlowEdgeData } from "../src/topology/TopologyFlowEdge";
import { toFlowEdges, topologyEdgeSides } from "../src/topology/adapter";
import { CARD_WIDTH, type LayoutResult } from "../src/topology/layout";
import {
  TOPOLOGY_PORT_CLEARANCE,
  TOPOLOGY_PORT_POSITIONS,
  distributedTopologyPortIndices,
  topologyPortCoordinates,
} from "../src/topology/ports";
import type { TopologyGraph, TopologyNode } from "../src/topology/types";

test("short cards retain marker clearance between every port", () => {
  const coordinates = topologyPortCoordinates(92);
  assert.equal(coordinates.length, TOPOLOGY_PORT_POSITIONS.length);
  assert.equal(coordinates[0], TOPOLOGY_PORT_CLEARANCE);
  assert.equal(coordinates.at(-1), 92 - TOPOLOGY_PORT_CLEARANCE);
  for (let index = 1; index < coordinates.length; index += 1) {
    assert.ok(
      coordinates[index] - coordinates[index - 1] >= TOPOLOGY_PORT_CLEARANCE,
      `${coordinates[index - 1]} and ${coordinates[index]} are crowded`,
    );
  }
  assert.deepEqual(distributedTopologyPortIndices(1), [1]);
  assert.deepEqual(distributedTopologyPortIndices(2), [1, 2]);
  assert.deepEqual(distributedTopologyPortIndices(3), [0, 1, 3]);
  assert.deepEqual(distributedTopologyPortIndices(4), [0, 1, 2, 3]);
});

test("side selection uses rectangle gaps instead of misleading top-left deltas", () => {
  assert.deepEqual(
    topologyEdgeSides(
      { x: 0, y: 0, width: 208, height: 194 },
      { x: 272, y: 96, width: 208, height: 92 },
    ),
    { sourceSide: "right", targetSide: "left" },
  );
  assert.deepEqual(
    topologyEdgeSides(
      { x: 0, y: 0, width: 208, height: 92 },
      { x: 24, y: 140, width: 208, height: 92 },
    ),
    { sourceSide: "bottom", targetSide: "top" },
  );
});

test("HTTP and SMPP skip-column paths bypass opposite sides of MT routes", () => {
  const node = (id: string, column: TopologyNode["column"]): TopologyNode => ({
    id,
    kind: "core",
    label: id,
    column,
    status: "ok",
  });
  const graph: TopologyGraph = {
    nodes: [
      node("http", "ingress"),
      node("smpp", "ingress"),
      node("routes", "policy"),
      node("core", "core"),
    ],
    edges: [
      { id: "http-core", from: "http", to: "core", kind: "mt" },
      { id: "smpp-core", from: "smpp", to: "core", kind: "mt" },
      { id: "routes-core", from: "routes", to: "core", kind: "mt" },
    ],
    problems: [],
    structure_hash: "test",
    observed_at: "2026-08-01T00:00:00Z",
  };
  const layout: LayoutResult = {
    positions: {
      http: { x: 0, y: 0 },
      smpp: { x: 0, y: 120 },
      routes: { x: 300, y: 0 },
      core: { x: 600, y: 0 },
    },
    heights: { http: 92, smpp: 92, routes: 194, core: 92 },
  };
  const edges = toFlowEdges({
    graph,
    layout,
    rates: {},
    lens: "health",
    flow: "mt",
    selectedId: null,
    stalledQueues: new Set(),
    highlighted: new Set(),
  });
  const byId = new Map(edges.map((edge) => [edge.id, edge]));
  const http = byId.get("http->core:mt");
  const smpp = byId.get("smpp->core:mt");
  const routes = byId.get("routes->core:mt");
  assert.ok(http && smpp && routes);

  assert.ok((http.data as TopologyFlowEdgeData).detourY! < 0);
  assert.ok((smpp.data as TopologyFlowEdgeData).detourY! > 194);
  assert.equal((routes.data as TopologyFlowEdgeData).detourY, undefined);
  assert.equal(http.sourceHandle?.startsWith("source-right-"), true);
  assert.equal(http.targetHandle?.startsWith("target-left-"), true);
  assert.equal(new Set(edges.map((edge) => edge.targetHandle)).size, 3);
  assert.equal(http.style?.stroke, "#0f766e");
  assert.equal((http.markerEnd as { width?: number }).width, 14);
});

test("detour paths preserve a straight final approach and label the long track", () => {
  const geometry = horizontalDetourGeometry({
    sourceX: 208,
    sourceY: 46,
    targetX: 600,
    targetY: 60,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    detourY: -24,
  });
  const beforeTarget = geometry.points.at(-2)!;
  const target = geometry.points.at(-1)!;
  assert.equal(target.x - beforeTarget.x, 22);
  assert.equal(target.y, beforeTarget.y);
  assert.equal(geometry.label.y, -24);
  assert.ok(geometry.label.x > 230 && geometry.label.x < 578);
  assert.match(geometry.path, /Q/);
});

test("routing stays identical when the server reorders equal relationships", () => {
  const node = (id: string, column: TopologyNode["column"]): TopologyNode => ({
    id,
    kind: "core",
    label: id,
    column,
    status: "ok",
  });
  const graph: TopologyGraph = {
    nodes: [node("routes", "policy"), node("core", "core")],
    edges: [
      { id: "route-b", from: "routes", to: "core", kind: "mt", degraded: true, label: "B" },
      { id: "route-a", from: "routes", to: "core", kind: "mt", degraded: true, label: "A" },
    ],
    problems: [],
    structure_hash: "test",
    observed_at: "2026-08-01T00:00:00Z",
  };
  const layout: LayoutResult = {
    positions: { routes: { x: 0, y: 0 }, core: { x: 272, y: 0 } },
    heights: { routes: 92, core: 92 },
  };
  const render = (candidate: TopologyGraph) =>
    toFlowEdges({
      graph: candidate,
      layout,
      rates: {},
      lens: "health",
      flow: "mt",
      selectedId: null,
      stalledQueues: new Set(),
      highlighted: new Set(),
    }).map((edge) => ({
      id: edge.id,
      sourceHandle: edge.sourceHandle,
      targetHandle: edge.targetHandle,
      label: edge.label,
      data: edge.data,
    }));

  assert.deepEqual(render(graph), render({ ...graph, edges: [...graph.edges].reverse() }));
  assert.equal(render(graph)[0].label, "A");
});
