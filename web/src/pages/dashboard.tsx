import { useEffect, useMemo, useState } from "react";
import { useCustom } from "@refinedev/core";
import { Button } from "antd";
import {
  ApiOutlined,
  ArrowRightOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  DatabaseOutlined,
  ExclamationCircleFilled,
  ReloadOutlined,
  ShareAltOutlined,
  UserAddOutlined,
  UsergroupAddOutlined,
} from "@ant-design/icons";
import { Link } from "react-router-dom";

import { PageTitle, StatusBadge, type StatusTone } from "../components/OperatorUI";

type Health = { status: string; checks: Record<string, string> };

const isHealthy = (detail: string) => {
  const normalized = detail.toLowerCase();
  return normalized === "ok" || normalized === "bound" || normalized === "healthy";
};

const checkTone = (detail: string): StatusTone => {
  const normalized = detail.toLowerCase();
  if (isHealthy(detail)) return "positive";
  if (normalized.includes("connect") || normalized.includes("start")) return "progress";
  if (normalized.includes("degrad") || normalized.includes("warn")) return "warning";
  return "negative";
};

const readableName = (name: string) => {
  if (name === "amqp") return "AMQP broker";
  if (name === "postgres") return "PostgreSQL";
  if (name === "bridge") return "Compatibility codec";
  if (name.startsWith("connector:")) return `Connector · ${name.replace("connector:", "")}`;
  return name.replace(/_/g, " ");
};

export const DashboardPage = () => {
  const { data, isFetching, refetch } = useCustom<Health>({
    url: "/api/health",
    method: "get",
  });
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const health = data?.data;

  useEffect(() => {
    if (health) setUpdatedAt(new Date());
  }, [health]);

  const rows = useMemo(
    () =>
      Object.entries(health?.checks ?? {}).map(([name, detail]) => ({
        key: name,
        name,
        detail,
        healthy: isHealthy(detail),
      })),
    [health],
  );

  const connectorRows = rows.filter((row) => row.name.startsWith("connector:"));
  const healthyChecks = rows.filter((row) => row.healthy).length;
  const boundConnectors = connectorRows.filter((row) => row.healthy).length;
  const overallHealthy = health?.status === "ok";
  const overallTone: StatusTone = health ? (overallHealthy ? "positive" : "negative") : "progress";

  const refresh = () => {
    void refetch().then(() => setUpdatedAt(new Date()));
  };

  return (
    <div className="page-container">
      <section className="dashboard-hero">
        <PageTitle
          eyebrow="Live operations"
          title="Control room"
          description="Monitor the gateway’s critical dependencies and move directly into the configuration that needs attention."
        />
        <div className={`hero-status is-${overallTone}`} aria-live="polite">
          {overallHealthy ? (
            <CheckCircleFilled className="hero-status-icon" />
          ) : (
            <ExclamationCircleFilled className="hero-status-icon" />
          )}
          <span>
            <strong>
              {health
                ? overallHealthy
                  ? "All critical checks passing"
                  : "Gateway needs attention"
                : "Reading gateway status"}
            </strong>
            <small>{updatedAt ? `Updated ${updatedAt.toLocaleTimeString()}` : "Connecting…"}</small>
          </span>
        </div>
      </section>

      <section className="dashboard-grid" aria-label="Gateway overview">
        <article className="metric-card">
          <div className="metric-card-header">
            <span>Gateway status</span>
            <CloudServerOutlined className="metric-icon" />
          </div>
          <div className="metric-value">
            <StatusBadge tone={overallTone}>
              {health?.status?.toUpperCase() ?? "LOADING"}
            </StatusBadge>
          </div>
          <div className="metric-caption">Public and admin planes</div>
        </article>

        <article className="metric-card">
          <div className="metric-card-header">
            <span>Healthy checks</span>
            <DatabaseOutlined className="metric-icon" />
          </div>
          <div className="metric-value">
            {healthyChecks}
            <span className="metric-denominator"> / {rows.length || "—"}</span>
          </div>
          <div className="metric-caption">Runtime dependencies ready</div>
        </article>

        <article className="metric-card">
          <div className="metric-card-header">
            <span>Bound connectors</span>
            <ApiOutlined className="metric-icon" />
          </div>
          <div className="metric-value">
            {boundConnectors}
            <span className="metric-denominator"> / {connectorRows.length || "—"}</span>
          </div>
          <div className="metric-caption">Required SMPP links online</div>
        </article>

        <article className="metric-card">
          <div className="metric-card-header">
            <span>Last refresh</span>
            <ReloadOutlined className="metric-icon" />
          </div>
          <div className="metric-value metric-time">
            {updatedAt?.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) ?? "—"}
          </div>
          <div className="metric-caption">Live readiness probe</div>
        </article>

        <article className="dashboard-panel is-wide">
          <div className="panel-heading">
            <div>
              <h2>Runtime services</h2>
              <p>Every check comes from the same readiness probe used by the gateway.</p>
            </div>
            <Button icon={<ReloadOutlined />} loading={isFetching} onClick={refresh}>
              Refresh
            </Button>
          </div>
          <div className="health-list" aria-live="polite">
            {rows.map((row) => (
              <div className="health-row" key={row.key}>
                <span className="health-row-name" title={row.name}>
                  {readableName(row.name)}
                </span>
                <StatusBadge tone={checkTone(row.detail)}>{row.detail}</StatusBadge>
              </div>
            ))}
          </div>
        </article>

        <aside className="dashboard-panel is-narrow">
          <div className="panel-heading">
            <div>
              <h2>Quick actions</h2>
              <p>Jump into the most common operator tasks.</p>
            </div>
          </div>
          <div className="quick-actions">
            <Link className="quick-action" to="/connectors">
              <span className="quick-action-icon">
                <ApiOutlined />
              </span>
              <span>
                <strong>Manage connectors</strong>
                <small>Bind, stop, or inspect SMPP links</small>
              </span>
              <ArrowRightOutlined className="quick-action-arrow" />
            </Link>
            <Link className="quick-action" to="/routes">
              <span className="quick-action-icon">
                <ShareAltOutlined />
              </span>
              <span>
                <strong>Review MT routes</strong>
                <small>Control outbound routing priority</small>
              </span>
              <ArrowRightOutlined className="quick-action-arrow" />
            </Link>
            <Link className="quick-action" to="/users">
              <span className="quick-action-icon">
                <UserAddOutlined />
              </span>
              <span>
                <strong>Provision a user</strong>
                <small>Create credentials and quota limits</small>
              </span>
              <ArrowRightOutlined className="quick-action-arrow" />
            </Link>
            <Link className="quick-action is-prototype" to="/partners/onboarding">
              <span className="quick-action-icon">
                <UsergroupAddOutlined />
              </span>
              <span>
                <strong>Onboard a partner</strong>
                <small>Prototype · Plan an SMPP connection</small>
              </span>
              <ArrowRightOutlined className="quick-action-arrow" />
            </Link>
          </div>
        </aside>
      </section>
    </div>
  );
};
