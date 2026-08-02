import { Button } from "antd";
import { CloseOutlined, ExportOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";

import type { TopologyGraph, TopologyNode } from "./types";

/** Console route for a server-supplied resource name. */
const routeFor = (resource: string, id: string): string => {
  const base =
    resource === "interceptors"
      ? "/interceptors"
      : resource === "partner-onboarding"
        ? "/partners/onboarding"
        : `/${resource}`;
  // The console's list pages are the editors; ids are carried as a hash so a
  // deep link lands on the page even where a detail route does not exist.
  return id ? `${base}#${encodeURIComponent(id)}` : base;
};

const singular: Record<string, string> = {
  connectors: "connector",
  "termination-connectors": "termination connector",
  routes: "MT route",
  "mo-routes": "MO route",
  users: "user",
  groups: "group",
  "smpps-users": "SMPP bind",
  filters: "filter",
  "http-connectors": "HTTP destination",
  interceptors: "interceptor",
};

type InspectorProps = {
  node: TopologyNode | null;
  children: TopologyNode[];
  graph: TopologyGraph;
  onClose: () => void;
  onSelect: (id: string) => void;
};

/**
 * Inspector shows the selected entity's real record and the way out to the page
 * that edits it. It is deliberately read-only: the map is dense, a misclick on
 * a canvas is easy, and every mutation this console supports already has a page
 * that asks for confirmation in context.
 */
export const Inspector = ({ node, children, graph, onClose, onSelect }: InspectorProps) => {
  if (!node) return null;

  const kindLabel = node.ref ? (singular[node.ref.resource] ?? node.kind) : node.kind;
  const outgoing = graph.edges.filter((edge) => edge.from === node.id && edge.degraded);

  return (
    <aside className="topology-inspector" aria-label={`${node.label} details`}>
      <header>
        <div>
          <span className="topology-inspector-kind">{kindLabel.replace(/_/g, " ")}</span>
          <h2>{node.label}</h2>
          <span className={`topology-inspector-status is-${node.status}`}>
            {node.status.toUpperCase()}
          </span>
        </div>
        <Button type="text" icon={<CloseOutlined />} onClick={onClose} aria-label="Close inspector" />
      </header>

      {node.reason ? <p className="topology-inspector-reason">{node.reason}</p> : null}

      {outgoing.length > 0 ? (
        <section>
          <h3>Broken paths from here</h3>
          <ul className="topology-inspector-problems">
            {outgoing.map((edge) => (
              <li key={edge.id}>{edge.reason}</li>
            ))}
          </ul>
        </section>
      ) : null}

      {node.detail && node.detail.length > 0 ? (
        <dl className="topology-inspector-fields">
          {node.detail.map((field) => (
            <div key={field.label}>
              <dt>{field.label}</dt>
              <dd>
                {field.items && field.items.length > 0 ? (
                  <ol>
                    {field.items.map((item, index) => (
                      <li key={`${item}-${index}`}>{item}</li>
                    ))}
                  </ol>
                ) : (
                  (field.value ?? "—")
                )}
              </dd>
            </div>
          ))}
        </dl>
      ) : null}

      {children.length > 0 ? (
        <section>
          <h3>
            Contains {children.length} {children.length === 1 ? "entity" : "entities"}
          </h3>
          <ul className="topology-inspector-children">
            {children.map((child) => (
              <li key={child.id}>
                <button type="button" onClick={() => onSelect(child.id)}>
                  <span className={`topology-dot is-${child.status}`} aria-hidden="true" />
                  <span>{child.label}</span>
                  {child.sublabel ? <small>{child.sublabel}</small> : null}
                </button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {node.ref ? (
        <section className="topology-inspector-actions">
          <h3>Actions (read only)</h3>
          <Link to={routeFor(node.ref.resource, node.ref.id)}>
            <Button block icon={<ExportOutlined />}>
              Open {singular[node.ref.resource] ?? node.ref.resource}
            </Button>
          </Link>
        </section>
      ) : null}

      {graph.counters_since ? (
        <footer className="topology-inspector-footer">
          Counters since {new Date(graph.counters_since).toLocaleString()} · they reset when the
          gateway restarts
        </footer>
      ) : null}
    </aside>
  );
};
