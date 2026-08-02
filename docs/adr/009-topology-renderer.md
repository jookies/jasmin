# ADR-009 — React Flow and dagre for the live topology map

- **Date:** 2026-07-31
- **Status:** accepted
- **Summary:** The console's topology map renders with `@xyflow/react` over a `@dagrejs/dagre` layout, behind a server-owned graph document, rather than a hand-rolled canvas renderer.
- **Related:** [plan 025](../plans/025-live-topology-map.md), [ADR-002](002-web-ui-stack.md)

## Context

The gateway's configuration is spread across roughly fifteen console pages.
Each is true in isolation, but none answers the operator's actual question —
*what goes where, right now, and is it moving?* Worse, the faults that span two
pages are invisible on both: a route pointing at a connector that is not bound,
a `dlr-url` accepted while the thrower is not running, a `submit.sm.<cid>` queue
growing behind a connector that stopped consuming. The routes page knows the
connector id; the connectors page knows the bind state; nothing joins them.

A generated picture of the running system joins them. That needs a renderer, and
the console has hard constraints on what it may pull in: the SPA is embedded in
the gateway binary with `go:embed`, ships to air-gapped deployments, must make no
outbound request at runtime, and its `dist/` is committed to git.

## Decision

**`@xyflow/react` v12 (MIT core only) for rendering, `@dagrejs/dagre` v3 for
layered layout, on a lazily-imported `/topology` route.** The server computes a
`{nodes, edges}` document; a thin adapter maps it to the library's types.

The architectural half of this decision matters more than the library half:

- `internal/app/adminweb/topology.go` owns the graph model. It is a pure
  function over a plain inputs struct and knows nothing about any renderer.
- `web/src/topology/adapter.ts` is the **only** file that knows React Flow
  exists. Replacing the renderer rewrites that file, not the feature.

Two properties are load-bearing and enforced by tests:

- `StructureHash` digests node and edge identity only, never a metric. The
  browser re-runs layout when it changes and repaints in place when it does not.
  Include a counter and the canvas would relayout every five seconds, moving
  nodes under the pointer. `TestStructureHashIgnoresMetrics` pins this.
- Layout depends on the node *set*, not on array order, because two
  hash-identical documents may legitimately arrive ordered differently.
  `layoutGraph` sorts before feeding dagre.

Layout is deliberately a hybrid: dagre solves the hard part (ordering nodes
within a rank to minimise crossings) and the server's `Column` label imposes the
x axis. dagre would rank by edges alone, which is "correct" and reads wrong —
the pipeline's reading order is the point of the picture.

## Alternatives considered

**Hand-rolled canvas 2D with a custom Sugiyama layout.** Zero dependencies,
which fits the embedded-bundle constraint best, and the education diagrams
already take this route (`web/src/components/EducationDiagrams.tsx`). Rejected
because layered layout — ranking, crossing minimisation, edge routing — is
roughly a thousand lines we would own forever, and it is precisely the code that
breaks whenever a node kind is added. dagre is a pure `(nodes, edges) →
positions` function that has been stable for a decade. The cost of the
dependency is bounded and measurable; the cost of the algorithm is not.

**Cytoscape.js.** More mature still, canvas-based, and faster past roughly five
thousand nodes. Rejected on two grounds: this deployment's ceiling is 150–400
nodes, so the performance advantage never pays; and Cytoscape styles through its
own selector DSL, so node cards could not reuse antd or
`web/src/components/OperatorUI.tsx`. Every visual change would be authored in a
second language, and the map would drift from the rest of the console.

**Inline SVG with no layout library.** Free hit-testing and accessibility, and
consistent with the education diagrams. Rejected for the same reason as the
hand-rolled canvas — it solves rendering, not layout, and layout is the problem.

**elkjs** as the layout engine. Rejected on size: it is GWT-compiled Java and an
order of magnitude larger than dagre, against a bundle that is already committed
to git.

## Consequences

- The bundle grows by a measured **238 KB minified / 79 KB gzip**, isolated in
  the lazily-loaded `topology-*.js` chunk. The console already code-splits per
  page, so no other page pays for it. `dist/` grew 2.4 MB → 2.7 MB, permanently,
  in git history.
- CI enforces bundle freshness (`.github/workflows/ci.yml`), so any `web/src`
  change must be accompanied by `cd web && npm run build` in the same commit.
- We are on React Flow's MIT core. React Flow *Pro* examples and plugins are
  commercially licensed and must not be copied in.
- Upstream `dagre` is in maintenance mode; `@dagrejs/dagre` is the maintained
  fork. Because layout is a pure function, swapping it later costs nothing in
  the render layer.
- The map is read-only by design. Every mutation the console supports already
  has a page that asks for confirmation in context, and a dense canvas is an
  easy place to misclick.
