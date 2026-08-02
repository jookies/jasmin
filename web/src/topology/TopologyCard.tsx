import { Handle, type NodeProps, type Node } from "@xyflow/react";
import {
  ApiOutlined,
  BranchesOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  DeploymentUnitOutlined,
  DisconnectOutlined,
  FilterOutlined,
  GlobalOutlined,
  CloudUploadOutlined,
  InboxOutlined,
  KeyOutlined,
  LinkOutlined,
  SendOutlined,
  ShareAltOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import type { ReactNode } from "react";

import type { NodeData } from "./adapter";
import {
  TOPOLOGY_PORT_POSITIONS,
  topologyPortId,
  topologyPortPosition,
  topologyPortStyle,
  type TopologyEdgeRole,
  type TopologyEdgeSide,
} from "./ports";
import type { NodeKind, NodeStatus, TopologyNode } from "./types";

const edgeSides: TopologyEdgeSide[] = ["left", "right", "top", "bottom"];
const edgeRoles: TopologyEdgeRole[] = ["target", "source"];

const icons: Partial<Record<NodeKind, ReactNode>> = {
  smpp_bind: <LinkOutlined />,
  user: <TeamOutlined />,
  account_group: <TeamOutlined />,
  filter: <FilterOutlined />,
  interceptor: <BranchesOutlined />,
  mt_route: <ShareAltOutlined />,
  mo_route: <InboxOutlined />,
  core: <CloudServerOutlined />,
  queue: <DatabaseOutlined />,
  smpp_connector: <ApiOutlined />,
  missing_connector: <DisconnectOutlined />,
  termination_connector: <DeploymentUnitOutlined />,
  delivery_endpoint: <CloudUploadOutlined />,
  message_spool: <InboxOutlined />,
  pull_token: <KeyOutlined />,
  http_destination: <GlobalOutlined />,
  thrower: <SendOutlined />,
  carrier: <ThunderboltOutlined />,
  infra: <DatabaseOutlined />,
  front_door_http: <GlobalOutlined />,
  front_door_smpps: <ApiOutlined />,
};

/**
 * groupIcon picks the icon for an aggregate card from the children it holds,
 * so "SMPP connectors" and "MT routes" do not both render the generic mark.
 */
const iconFor = (node: TopologyNode, children: TopologyNode[]): ReactNode => {
  if (node.kind !== "group") return icons[node.kind] ?? <DeploymentUnitOutlined />;
  return icons[children[0]?.kind] ?? <DeploymentUnitOutlined />;
};

const statusLabel: Record<NodeStatus, string> = {
  ok: "Healthy",
  warning: "Warning",
  down: "Unhealthy",
  idle: "Idle",
  unknown: "Unknown",
};

/**
 * StatusDot never travels alone: every card also carries its state as text, and
 * a fault carries its reason. Colour is the fast path, not the only path.
 */
const StatusDot = ({ status, decorative = false }: { status: NodeStatus; decorative?: boolean }) => (
  <span
    className={`topology-dot is-${status}`}
    role={decorative ? undefined : "img"}
    aria-hidden={decorative || undefined}
    aria-label={decorative ? undefined : `${statusLabel[status]} status`}
  />
);

const compact = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });

/** The headline number for a card, chosen by the active lens. */
const headline = (data: NodeData): { value: string; caption: string } | null => {
  const { node, rates, lens, metricsStale } = data;
  const metrics = node.metrics ?? {};

  if (metricsStale) return null;

  if (lens === "traffic") {
    const rate = rates.submit_success ?? rates.mo_routed ?? rates.requests;
    if (rate !== undefined && rate > 0) {
      return { value: `${compact.format(Math.round(rate))}/min`, caption: "observed rate" };
    }
  }
  if (lens === "latency" && metrics.latency_p95_seconds !== undefined) {
    return {
      value: `${Math.round(metrics.latency_p95_seconds * 1000)} ms`,
      caption: "submit p95",
    };
  }
  if (lens === "billing") {
    const charged = Object.entries(metrics).find(([key]) => key.startsWith("charged_"));
    if (charged) {
      return { value: compact.format(charged[1]), caption: `charged ${charged[0].slice(8)}` };
    }
  }
  if (node.kind === "pull_token") {
    // Silence is the number that matters for a credential nobody connects to:
    // reads and rows are cumulative and say nothing about right now.
    if (metrics.silent_seconds === undefined) return { value: "never", caption: "pulled" };
    const seconds = Math.round(metrics.silent_seconds);
    const label =
      seconds < 90 ? `${seconds}s` : seconds < 5400 ? `${Math.round(seconds / 60)}m` : `${Math.round(seconds / 3600)}h`;
    return { value: label, caption: "since last pull" };
  }
  if (metrics.depth !== undefined) {
    return { value: compact.format(metrics.depth), caption: "ready" };
  }
  if (metrics.ready_total !== undefined) {
    return { value: compact.format(metrics.ready_total), caption: "ready total" };
  }
  if (metrics.submit_success !== undefined) {
    return { value: compact.format(metrics.submit_success), caption: "submits since boot" };
  }
  return null;
};

export type TopologyCardNode = Node<NodeData, "topologyCard">;

/**
 * TopologyCard is one card on the canvas: a group with a few of its members
 * inline, or a standalone node. Selecting it, or its "+N more" affordance,
 * opens the inspector — the card itself never grows, because a card that
 * changed size would move every other node on the next layout.
 */
export const TopologyCard = ({ data }: NodeProps<TopologyCardNode>) => {
  const { node, children, hiddenChildren, selected, highlighted, dimmed, stalled } = data;
  const stat = headline(data);
  const effectiveStatus: NodeStatus = stalled ? "warning" : node.status;

  const className = [
    "topology-card",
    `is-${effectiveStatus}`,
    selected ? "is-selected" : "",
    highlighted ? "is-highlighted" : "",
    dimmed ? "is-dimmed" : "",
    node.managed_by === "config" ? "is-readonly" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={className}>
      {/* Multiple hidden ports keep incoming, outgoing, and reverse traffic on
          separate tracks. React Flow still owns the path and arrow geometry;
          the card only gives each relationship a clear place to terminate. */}
      {edgeSides.flatMap((side) =>
        TOPOLOGY_PORT_POSITIONS.flatMap((percentage, index) =>
          edgeRoles.map((role) => (
            <Handle
              key={`${role}-${side}-${index}`}
              id={topologyPortId(role, side, index)}
              type={role}
              position={topologyPortPosition[side]}
              style={topologyPortStyle(side, percentage)}
              isConnectable={false}
            />
          )),
        ),
      )}

      <div className="topology-card-header">
        <div className="topology-card-title-row">
          <span className="topology-card-icon">{iconFor(node, children)}</span>
          <span className="topology-card-title">{node.label}</span>
        </div>

        <div className="topology-card-meta-row">
          <div className="topology-card-context">
            {node.sublabel ? <span className="topology-card-sub">{node.sublabel}</span> : null}
            {node.sublabel && stat ? (
              <span className="topology-card-context-divider" aria-hidden="true">
                ·
              </span>
            ) : null}
            {stat ? (
              <span className="topology-card-stat">
                <strong>{stat.value}</strong>
                <span>{stat.caption}</span>
              </span>
            ) : null}
          </div>

          <span className={`topology-card-state is-${effectiveStatus}`}>
            <StatusDot status={effectiveStatus} decorative />
            <span>{statusLabel[effectiveStatus]}</span>
          </span>
        </div>
      </div>

      {children.length > 0 ? (
        <ul className="topology-card-children">
          {children.map((child) => (
            <li key={child.id} className={`is-${child.status}`}>
              <StatusDot status={child.status} />
              <span title={child.label}>{child.label}</span>
            </li>
          ))}
        </ul>
      ) : null}

      {hiddenChildren > 0 ? (
        <div className="topology-card-more">+{hiddenChildren} more</div>
      ) : null}

      {/* A queue that is not draining is a fault no depth threshold can catch,
          so it is stated on the card rather than left to the operator to spot
          by watching a number fail to change. */}
      {stalled ? (
        <div className="topology-card-reason">holding — depth not going down</div>
      ) : node.reason ? (
        <div className="topology-card-reason">{node.reason}</div>
      ) : null}
    </div>
  );
};
