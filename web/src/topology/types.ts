// Mirror of the Go topology document (internal/app/adminweb/topology.go).
//
// The server owns the graph model and this console owns the pixels. Keep these
// types in step with the Go structs by hand: they are small, they change
// rarely, and a generator would be more machinery than the contract is worth.

export type NodeStatus = "ok" | "warning" | "down" | "idle" | "unknown";

export type EdgeKind = "mt" | "mo" | "dlr" | "config";

export type NodeKind =
  | "group"
  | "smpp_bind"
  | "user"
  | "account_group"
  | "filter"
  | "interceptor"
  | "mt_route"
  | "mo_route"
  | "core"
  | "queue"
  | "smpp_connector"
  | "missing_connector"
  | "termination_connector"
  | "delivery_endpoint"
  | "message_spool"
  | "pull_token"
  | "http_destination"
  | "thrower"
  | "carrier"
  | "infra"
  | "front_door_http"
  | "front_door_smpps";

export type Column =
  | "ingress"
  | "policy"
  | "core"
  | "queues"
  | "egress"
  | "carriers"
  | "delivery"
  | "infra";

/** Direction of travel. MT reads left to right, the return path right to left. */
export type Lane = "mt" | "return" | "infra";

export type Ref = {
  resource: string;
  id: string;
};

export type DetailField = {
  label: string;
  value?: string;
  items?: string[];
};

export type TopologyNode = {
  id: string;
  kind: NodeKind;
  label: string;
  sublabel?: string;
  column: Column;
  lane?: Lane;
  status: NodeStatus;
  parent?: string;
  child_count?: number;
  managed_by?: string;
  reason?: string;
  metrics?: Record<string, number>;
  detail?: DetailField[];
  ref?: Ref;
};

export type TopologyEdge = {
  id: string;
  from: string;
  to: string;
  kind: EdgeKind;
  label?: string;
  /** A fault worth reporting in its own right; counted by the problem rail. */
  degraded?: boolean;
  /** Carries nothing because of a fault reported elsewhere. Dimmed, not counted. */
  inactive?: boolean;
  reason?: string;
  metrics?: Record<string, number>;
};

export type ProblemKind = "broken_path" | "unbound" | "queue_backlog" | "stale_pull";

export type Problem = {
  kind: ProblemKind;
  label: string;
  count: number;
  node_ids: string[];
};

export type ActivityWindow = {
  window: string;
  reads: number;
  rows: number;
  denied: number;
};

export type AnalyticsRow = {
  node_id: string;
  kind: NodeKind;
  label: string;
  status: NodeStatus;
  detail?: string;
  reason?: string;
  /** Windowed history. Pull credentials only — they are the one item with one. */
  windows?: ActivityWindow[];
  /** Cumulative since process start. Never rendered under a window heading. */
  totals?: Record<string, number>;
};

export type Analytics = {
  retention_note: string;
  pull_tokens: AnalyticsRow[];
  delivery: AnalyticsRow[];
};

export type TopologyGraph = {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  problems: Problem[];
  /** Changes only when the shape changes — the layout memoization key. */
  structure_hash: string;
  observed_at: string;
  /** Process start. Every counter in the document is cumulative from here. */
  counters_since?: string;
  /** No metrics registry was wired: structure is real, numbers are absent. */
  metrics_stale?: boolean;
  /** Present only when the matrix panel asked for it. */
  analytics?: Analytics;
};

/** The lenses in the toolbar. Each re-reads the same graph, never re-fetches. */
export type Lens = "health" | "traffic" | "latency" | "billing";

/** The flow filter: which lanes are drawn. */
export type FlowFilter = "all" | "mt" | "mo" | "dlr";

export const emptyGraph: TopologyGraph = {
  nodes: [],
  edges: [],
  problems: [],
  structure_hash: "",
  observed_at: "",
};
