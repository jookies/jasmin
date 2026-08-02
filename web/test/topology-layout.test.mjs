import assert from "node:assert/strict";
import test from "node:test";

import { CARD_WIDTH, layoutGraph } from "../src/topology/layout.ts";

const node = (id, column, overrides = {}) => ({
  id,
  kind: "core",
  label: id,
  column,
  status: "ok",
  ...overrides,
});

const edge = (id, from, to, kind = "mt", overrides = {}) => ({
  id,
  from,
  to,
  kind,
  ...overrides,
});

const centreY = (layout, id) => layout.positions[id].y + layout.heights[id] / 2;

test("uses compact fixed semantic columns and aligns stage anchors on one spine", () => {
  const nodes = [
    node("routes", "policy", { kind: "group", child_count: 9 }),
    node("core", "core"),
    node("queues", "queues", { kind: "group", child_count: 2 }),
  ];
  const edges = [
    edge("routes-core", "routes", "core"),
    edge("core-queues", "core", "queues"),
  ];

  const layout = layoutGraph(nodes, edges);

  assert.equal(layout.positions.core.x - layout.positions.routes.x, CARD_WIDTH + 64);
  assert.equal(layout.positions.queues.x - layout.positions.core.x, CARD_WIDTH + 64);
  assert.equal(centreY(layout, "routes"), centreY(layout, "core"));
  assert.equal(centreY(layout, "core"), centreY(layout, "queues"));
});

test("keeps the through-stage card on the spine and places exception cards around it", () => {
  const nodes = [
    node("queues", "queues", { kind: "group", child_count: 4 }),
    node("connector", "egress", { kind: "group", child_count: 5 }),
    node("missing", "egress", { kind: "group", child_count: 2 }),
    node("termination", "egress", { kind: "group", child_count: 2 }),
    node("carrier", "carriers", { kind: "group", child_count: 3 }),
  ];
  const edges = [
    edge("queues-connector", "queues", "connector"),
    edge("connector-carrier", "connector", "carrier"),
    edge("queues-missing", "queues", "missing", "mt", { degraded: true }),
    edge("queues-termination", "queues", "termination"),
  ];

  const layout = layoutGraph(nodes, edges);

  assert.equal(centreY(layout, "queues"), centreY(layout, "connector"));
  assert.equal(centreY(layout, "connector"), centreY(layout, "carrier"));
  assert.notEqual(layout.positions.missing.y, layout.positions.connector.y);
  assert.notEqual(layout.positions.termination.y, layout.positions.connector.y);
});

test("places config-only cards in a compact support band near the stage they configure", () => {
  const nodes = [
    node("routes", "policy", { kind: "group", child_count: 9 }),
    node("core", "core"),
    node("accounts", "ingress", { kind: "group", child_count: 5 }),
    node("binds", "ingress", { kind: "group", child_count: 2 }),
    node("library", "ingress", { kind: "group", child_count: 8 }),
  ];
  const edges = [
    edge("routes-core", "routes", "core"),
    edge("accounts-routes", "accounts", "routes", "config"),
    edge("binds-routes", "binds", "routes", "config"),
    edge("library-routes", "library", "routes", "config"),
  ];

  const layout = layoutGraph(nodes, edges);
  const primaryBottom = Math.max(
    layout.positions.routes.y + layout.heights.routes,
    layout.positions.core.y + layout.heights.core,
  );
  const support = ["accounts", "binds", "library"];

  assert.deepEqual(
    support.map((id) => layout.positions[id].x).sort((left, right) => left - right),
    [0, CARD_WIDTH + 64, 2 * (CARD_WIDTH + 64)],
  );
  assert.ok(layout.positions.accounts.x < layout.positions.binds.x);
  assert.ok(layout.positions.binds.x < layout.positions.library.x);
  assert.ok(support.every((id) => layout.positions[id].y >= primaryBottom + 48));
  assert.equal(new Set(support.map((id) => centreY(layout, id))).size, 1);
});

test("lets support use free columns beside a taller exception stack", () => {
  const nodes = [
    node("routes", "policy", { kind: "group", child_count: 3 }),
    node("core", "core"),
    node("queues", "queues"),
    node("connector", "egress", { kind: "group", child_count: 3 }),
    node("missing", "egress", { kind: "group", child_count: 3 }),
    node("termination", "egress", { kind: "group", child_count: 3 }),
    node("accounts", "ingress", { kind: "group", child_count: 3 }),
  ];
  const edges = [
    edge("routes-core", "routes", "core"),
    edge("core-queues", "core", "queues"),
    edge("queues-connector", "queues", "connector"),
    edge("queues-missing", "queues", "missing", "mt", { degraded: true }),
    edge("queues-termination", "queues", "termination"),
    edge("accounts-routes", "accounts", "routes", "config"),
  ];

  const layout = layoutGraph(nodes, edges);
  const exceptionBottom = Math.max(
    layout.positions.missing.y + layout.heights.missing,
    layout.positions.termination.y + layout.heights.termination,
  );

  assert.ok(layout.positions.accounts.y < exceptionBottom);
  assert.ok(
    layout.positions.accounts.y >= layout.positions.routes.y + layout.heights.routes + 48,
  );
});

test("limits a large support band to two rows", () => {
  const support = Array.from({ length: 9 }, (_, index) =>
    node(`support-${String(index).padStart(2, "0")}`, "ingress", {
      kind: "group",
      child_count: 1,
    }),
  );
  const nodes = [node("routes", "policy"), node("core", "core"), ...support];
  const edges = [
    edge("routes-core", "routes", "core"),
    ...support.map((item) => edge(`${item.id}-routes`, item.id, "routes", "config")),
  ];

  const layout = layoutGraph(nodes, edges);

  assert.equal(new Set(support.map((item) => centreY(layout, item.id))).size, 2);
});

test("keeps infrastructure above traffic and aligned with the core stage", () => {
  const nodes = [
    node("infra", "infra", { lane: "infra", kind: "infra" }),
    node("core", "core"),
    node("queues", "queues"),
  ];
  const edges = [
    edge("core-infra", "core", "infra", "config"),
    edge("core-queues", "core", "queues"),
  ];

  const layout = layoutGraph(nodes, edges);

  assert.equal(layout.positions.infra.x, 2 * (CARD_WIDTH + 64));
  assert.ok(layout.positions.infra.y < layout.positions.core.y);
});

test("is deterministic when node and edge document order changes", () => {
  const nodes = [
    node("routes", "policy", { kind: "group", child_count: 4 }),
    node("core", "core"),
    node("queues", "queues"),
    node("accounts", "ingress", { kind: "group", child_count: 3 }),
  ];
  const edges = [
    edge("routes-core", "routes", "core"),
    edge("core-queues", "core", "queues"),
    edge("accounts-routes", "accounts", "routes", "config"),
  ];

  const expected = layoutGraph(nodes, edges);
  const reordered = layoutGraph([...nodes].reverse(), [...edges].reverse());

  assert.deepEqual(reordered, expected);
});
