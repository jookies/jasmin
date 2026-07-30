import { useMemo, useState } from "react";
import { useCustom } from "@refinedev/core";
import {
  Alert,
  App as AntdApp,
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Space,
  Spin,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { CopyOutlined, DownloadOutlined, ApiOutlined } from "@ant-design/icons";

import { StatusBadge } from "./OperatorUI";
import { API_URL } from "../httpClient";

/**
 * The integration pack: everything a partner needs in order to send us traffic,
 * for one account — or, for a connector, the inverse checklist of what has to be
 * requested from the carrier.
 *
 * Two things this component must never do. It must not present the gateway's
 * bind address as somewhere a partner can connect (listeners bind 0.0.0.0 and
 * deployments port-map), which is why the payload carries a placeholder and this
 * screen makes the operator substitute a real address before the text is worth
 * sending. And it must not display a password: the backend has none to give, and
 * every example carries a placeholder token instead.
 */

const HOST_PLACEHOLDER = "YOUR-GATEWAY-HOST";

type Endpoint = {
  id: string;
  protocol: string;
  enabled: boolean;
  scheme?: string;
  authority?: string;
  authority_is_placeholder: boolean;
  bind_address?: string;
  port?: number;
  tls: boolean;
  detail?: string;
};

type Parameter = {
  name: string;
  required?: boolean;
  constraint?: string;
  detail?: string;
};

type Example = { id: string; title: string; command: string };

type Channel = {
  id: string;
  title: string;
  protocol: string;
  endpoint_id: string;
  available: boolean;
  blocked_by?: string[];
  summary: string;
  parameters?: Parameter[];
  examples?: Example[];
  notes?: string[];
};

type Authorization = { key: string; label: string; allowed: boolean; effect?: string };

type ValueFilter = {
  key: string;
  applies: string;
  pattern: string;
  constrains: boolean;
  effect?: string;
};

type Position = {
  billing_mode: string;
  granted_balance: number | null;
  remaining_balance: number | null;
  granted_submit_sm_count: number | null;
  remaining_submit_sm_count: number | null;
  live_error?: string;
  http_throughput: number | null;
  smpps_throughput: number | null;
  group_id?: string;
  group_remaining_balance?: number | null;
  group_disabled?: boolean;
};

type CallbackLevel = { requested: number; delivered: string; detail: string };

type Callbacks = {
  available: boolean;
  ack_body: string;
  success_status: string;
  timeout_seconds: number;
  retry_delay_seconds: number;
  max_retries: number;
  total_attempts: number;
  example_response: string;
  levels: CallbackLevel[];
  dlr_fields: Parameter[];
  mo_fields: Parameter[];
  notes?: string[];
};

type MORoute = { order: number; default: boolean; from_connector?: string; destination: string };

type Inbound = {
  bind_account: boolean;
  system_id?: string;
  managed_by?: string;
  disabled?: boolean;
  ip_whitelist?: string;
  max_bindings?: number | null;
  mo_routes: MORoute[];
  mo_thrower_running: boolean;
  notes?: string[];
  receive_by_smpp: boolean;
};

type AccountGuide = {
  kind: "account";
  generated_at: string;
  username: string;
  external_id?: string;
  managed_by: string;
  disabled: boolean;
  position: Position;
  endpoints: Endpoint[];
  channels: Channel[];
  authorizations: Authorization[];
  filters: ValueFilter[];
  default_source_address?: string;
  callbacks: Callbacks;
  inbound: Inbound;
  placeholders: Record<string, string>;
  warnings?: string[];
};

type Setting = { name: string; value: string; is_default: boolean; detail?: string };
type CarrierRequest = { item: string; detail: string; known?: string };

type ConnectorBrief = {
  kind: "connector";
  generated_at: string;
  connector_id: string;
  managed_by: string;
  link: Setting[];
  we_will_send: Setting[];
  request_from_carrier: CarrierRequest[];
  warnings?: string[];
  placeholders: Record<string, string>;
};

const UNLIMITED = "unlimited";

const amount = (value: number | null | undefined) =>
  value === null || value === undefined
    ? UNLIMITED
    : value.toLocaleString(undefined, { minimumFractionDigits: 0, maximumFractionDigits: 4 });

/**
 * copyText prefers the async clipboard API and falls back to the legacy
 * selection copy. The console is normally served over plain HTTP on a loopback
 * listener, and outside a secure context navigator.clipboard does not exist —
 * without the fallback the copy button would silently do nothing exactly where
 * it is used most.
 */
const copyText = async (text: string): Promise<boolean> => {
  try {
    if (window.isSecureContext && navigator.clipboard) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through to the selection copy below.
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.top = "-1000px";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch {
    copied = false;
  }
  document.body.removeChild(area);
  return copied;
};

const downloadText = (filename: string, text: string) => {
  const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
  URL.revokeObjectURL(url);
};

type Substitution = { host: string; httpPort: string };

/**
 * substituteAddress rewrites the placeholder authority the backend emitted.
 * The published port is applied only to the HTTP endpoints, because a single
 * port override cannot be true for both an HTTP listener and an SMPP one; the
 * SMPP block keeps its configured port and says so on screen.
 */
const substituteAddress = (
  text: string,
  endpoints: Endpoint[],
  { host, httpPort }: Substitution,
): string => {
  const target = host.trim();
  if (!target) return text;
  let output = text;
  endpoints.forEach((endpoint) => {
    if (!endpoint.authority) return;
    const port =
      endpoint.id === "smpps" ? endpoint.port : Number(httpPort.trim()) || endpoint.port;
    output = output.split(endpoint.authority).join(port ? `${target}:${port}` : target);
  });
  return output.split(HOST_PLACEHOLDER).join(target);
};

const CopyButton = ({ text, label = "Copy" }: { text: string; label?: string }) => {
  // App.useApp() rather than the static antd message export, so the feedback
  // inherits the console's ConfigProvider theme instead of rendering unstyled.
  const { message } = AntdApp.useApp();
  return (
    <Button
      size="small"
      icon={<CopyOutlined />}
      onClick={async () => {
        const copied = await copyText(text);
        if (copied) {
          message.success("Copied");
        } else {
          message.warning("Could not copy automatically — select the text and copy it manually");
        }
      }}
    >
      {label}
    </Button>
  );
};

const Snippet = ({ command }: { command: string }) => (
  <pre className="integration-snippet">{command}</pre>
);

// ---------------------------------------------------------------------------
// Plain-text rendering. The download and the screen are produced from the same
// payload by these functions, so the file a partner receives cannot drift from
// what the operator reviewed.

const line = (label: string, value: string) => `${label}: ${value}`;

const renderAccountText = (guide: AccountGuide, substitution: Substitution): string => {
  const apply = (text: string) => substituteAddress(text, guide.endpoints, substitution);
  const out: string[] = [];
  out.push(`INTEGRATION INSTRUCTIONS — ${guide.username}`);
  out.push(`Generated ${guide.generated_at}`);
  out.push("");
  if (guide.warnings?.length) {
    out.push("BEFORE YOU SEND THIS");
    guide.warnings.forEach((warning) => out.push(`  - ${warning}`));
    out.push("");
  }
  out.push("ACCOUNT");
  out.push(`  ${line("Username", guide.username)}`);
  if (guide.external_id) out.push(`  ${line("Account reference", guide.external_id)}`);
  out.push(`  ${line("Password", "handed over separately; it cannot be shown again")}`);
  out.push(`  ${line("Charging", guide.position.billing_mode)}`);
  out.push(
    `  ${line(
      "Balance",
      guide.position.live_error
        ? "could not be read"
        : `${amount(guide.position.remaining_balance)} remaining of ${amount(
            guide.position.granted_balance,
          )}`,
    )}`,
  );
  out.push(
    `  ${line(
      "Message quota",
      guide.position.live_error
        ? "could not be read"
        : `${amount(guide.position.remaining_submit_sm_count)} remaining of ${amount(
            guide.position.granted_submit_sm_count,
          )}`,
    )}`,
  );
  out.push(
    `  ${line(
      "Throughput",
      `HTTP ${guide.position.http_throughput ?? "unlimited"} / SMPP ${
        guide.position.smpps_throughput ?? "unlimited"
      } submits per second`,
    )}`,
  );
  out.push("");

  out.push("ENDPOINTS");
  guide.endpoints.forEach((endpoint) => {
    if (!endpoint.enabled) {
      out.push(`  ${endpoint.protocol}: not available — ${endpoint.detail ?? "not configured"}`);
      return;
    }
    out.push(`  ${endpoint.protocol}: ${apply(endpoint.authority ?? "")}${endpoint.tls ? " (TLS)" : ""}`);
    if (endpoint.detail) out.push(`    ${endpoint.detail}`);
  });
  out.push("");

  guide.channels.forEach((channel) => {
    out.push(channel.title.toUpperCase());
    out.push(`  ${channel.summary}`);
    if (!channel.available) {
      out.push("  NOT AVAILABLE FOR THIS ACCOUNT:");
      (channel.blocked_by ?? []).forEach((reason) => out.push(`    - ${reason}`));
    }
    if (channel.parameters?.length) {
      out.push("  Parameters:");
      channel.parameters.forEach((parameter) => {
        const bits = [parameter.required ? "required" : "optional", parameter.constraint, parameter.detail]
          .filter(Boolean)
          .join("; ");
        out.push(`    ${parameter.name} — ${bits}`);
      });
    }
    channel.examples?.forEach((example) => {
      out.push(`  ${example.title}:`);
      apply(example.command)
        .split("\n")
        .forEach((commandLine) => out.push(`    ${commandLine}`));
    });
    channel.notes?.forEach((note) => out.push(`  Note: ${note}`));
    out.push("");
  });

  out.push("WHAT THIS ACCOUNT MAY SET");
  guide.authorizations.forEach((entry) => {
    out.push(`  ${entry.allowed ? "yes" : "no "} ${entry.label}${entry.effect ? ` — ${entry.effect}` : ""}`);
  });
  out.push("");

  const constraining = guide.filters.filter((filter) => filter.constrains);
  out.push("VALUE RESTRICTIONS");
  if (!constraining.length) {
    out.push("  None: this account is not restricted by value filters.");
  } else {
    constraining.forEach((filter) => {
      out.push(`  ${filter.applies} must match ${filter.pattern} from the start of the value`);
      if (filter.effect) out.push(`    Rejection: ${filter.effect}`);
    });
  }
  if (guide.default_source_address) {
    out.push(`  Default sender when none is supplied: ${guide.default_source_address}`);
  }
  out.push("");

  out.push("DELIVERY RECEIPTS AND INBOUND CALLBACKS");
  out.push(
    `  A callback counts as delivered only when the response status is ${guide.callbacks.success_status} AND the body,`,
  );
  out.push(`  after trimming whitespace, is exactly: ${guide.callbacks.ack_body}`);
  out.push("  Minimal successful response:");
  guide.callbacks.example_response.split("\n").forEach((responseLine) => out.push(`    ${responseLine}`));
  out.push(
    `  Retry policy: ${guide.callbacks.max_retries} retries after the first attempt (${guide.callbacks.total_attempts} attempts in total),`,
  );
  out.push(
    `  ${guide.callbacks.retry_delay_seconds}s apart, each with a ${guide.callbacks.timeout_seconds}s timeout, then the delivery is dropped.`,
  );
  out.push("  Receipt levels:");
  guide.callbacks.levels.forEach((level) => {
    out.push(`    dlr-level=${level.requested} -> ${level.delivered}: ${level.detail}`);
  });
  out.push("  Delivery-receipt fields:");
  guide.callbacks.dlr_fields.forEach((field) => out.push(`    ${field.name} — ${field.detail ?? ""}`));
  out.push("  Inbound-message fields:");
  guide.callbacks.mo_fields.forEach((field) => out.push(`    ${field.name} — ${field.detail ?? ""}`));
  guide.callbacks.notes?.forEach((note) => out.push(`  Note: ${note}`));
  out.push("");

  out.push("RECEIVING MESSAGES");
  if (guide.inbound.bind_account) {
    out.push(`  SMPP bind identity: ${guide.inbound.system_id}`);
    out.push(`  Permitted source IPs: ${guide.inbound.ip_whitelist || "any IPv4 address (not restricted)"}`);
    if (guide.inbound.max_bindings != null) {
      out.push(`  Concurrent sessions: ${guide.inbound.max_bindings}`);
    }
  }
  if (guide.inbound.mo_routes.length) {
    out.push("  Inbound routes delivering to this account:");
    guide.inbound.mo_routes.forEach((route) =>
      out.push(
        `    order ${route.order}${route.default ? " (default)" : ""}${
          route.from_connector ? ` from ${route.from_connector}` : ""
        } -> ${route.destination}`,
      ),
    );
  }
  guide.inbound.notes?.forEach((note) => out.push(`  Note: ${note}`));
  out.push("");
  out.push("PLACEHOLDERS TO REPLACE");
  Object.entries(guide.placeholders).forEach(([token, meaning]) => {
    out.push(`  ${token} — ${meaning}`);
  });
  return out.join("\n");
};

const renderConnectorText = (brief: ConnectorBrief): string => {
  const out: string[] = [];
  out.push(`CARRIER LINK BRIEF — ${brief.connector_id}`);
  out.push(`Generated ${brief.generated_at}`);
  out.push("");
  if (brief.warnings?.length) {
    out.push("CHECK BEFORE SENDING");
    brief.warnings.forEach((warning) => out.push(`  - ${warning}`));
    out.push("");
  }
  out.push("THE LINK AS WE HAVE IT CONFIGURED");
  brief.link.forEach((setting) =>
    out.push(`  ${setting.name}: ${setting.value || "(blank)"}${setting.detail ? ` — ${setting.detail}` : ""}`),
  );
  out.push("");
  out.push("WHAT WE WILL SEND YOU");
  brief.we_will_send.forEach((setting) =>
    out.push(
      `  ${setting.name}: ${setting.value || "(blank)"}${setting.is_default ? "  [never configured — confirm]" : ""}`,
    ),
  );
  out.push("");
  out.push("WHAT WE NEED FROM YOU");
  brief.request_from_carrier.forEach((request, index) => {
    out.push(`  ${index + 1}. ${request.item}`);
    out.push(`     ${request.detail}`);
    out.push(`     We currently have: ${request.known || "(nothing)"}`);
  });
  return out.join("\n");
};

// ---------------------------------------------------------------------------

const AddressBanner = ({
  endpoints,
  substitution,
  onChange,
}: {
  endpoints: Endpoint[];
  substitution: Substitution;
  onChange: (next: Substitution) => void;
}) => {
  const placeholder = endpoints.some((endpoint) => endpoint.enabled && endpoint.authority_is_placeholder);
  const httpEndpoint = endpoints.find((endpoint) => endpoint.id === "http");
  return (
    <Alert
      type={placeholder && !substitution.host.trim() ? "warning" : "info"}
      showIcon
      message={
        placeholder
          ? "This gateway does not know its own public address"
          : "Confirm the public address before sending this out"
      }
      description={
        <Space direction="vertical" size={8} style={{ width: "100%" }}>
          <span>
            Every listener binds <code>0.0.0.0</code>, so the process cannot know how a partner
            reaches it. Enter the address partners actually use and every example below is rewritten
            — nothing is sent anywhere, the substitution happens in this browser.
          </span>
          <Space wrap align="end">
            <label className="integration-field">
              <span>Public host</span>
              <Input
                style={{ width: 260 }}
                placeholder={HOST_PLACEHOLDER}
                value={substitution.host}
                onChange={(event) => onChange({ ...substitution, host: event.target.value })}
              />
            </label>
            <label className="integration-field">
              <span>Published HTTP port</span>
              <Input
                style={{ width: 180 }}
                placeholder={httpEndpoint?.port ? String(httpEndpoint.port) : "1401"}
                value={substitution.httpPort}
                onChange={(event) => onChange({ ...substitution, httpPort: event.target.value })}
              />
            </label>
          </Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            The HTTP port applies to the HTTP examples only. An SMPP listener published on a
            different port has to be corrected by hand — one port override cannot be true for both.
          </Typography.Text>
        </Space>
      }
    />
  );
};

const ChannelCard = ({
  channel,
  endpoints,
  substitution,
}: {
  channel: Channel;
  endpoints: Endpoint[];
  substitution: Substitution;
}) => (
  <Card
    size="small"
    className="integration-channel"
    title={
      <Space>
        <span>{channel.title}</span>
        <Tag>{channel.protocol}</Tag>
      </Space>
    }
    extra={
      channel.available ? (
        <StatusBadge tone="positive">Available</StatusBadge>
      ) : (
        <StatusBadge tone="negative">Not available</StatusBadge>
      )
    }
  >
    <Space direction="vertical" size={10} style={{ width: "100%" }}>
      <Typography.Text type="secondary">{channel.summary}</Typography.Text>

      {!channel.available && channel.blocked_by?.length ? (
        <Alert
          type="error"
          showIcon
          message="This account cannot use this channel yet"
          description={
            <ul className="integration-list">
              {channel.blocked_by.map((reason) => (
                <li key={reason}>{reason}</li>
              ))}
            </ul>
          }
        />
      ) : null}

      {channel.parameters?.length ? (
        <Table<Parameter>
          size="small"
          rowKey="name"
          pagination={false}
          dataSource={channel.parameters}
          columns={[
            {
              title: "Parameter",
              dataIndex: "name",
              width: 170,
              render: (name: string, parameter: Parameter) => (
                <Space size={4}>
                  <code>{name}</code>
                  {parameter.required ? <Tag color="red">required</Tag> : null}
                </Space>
              ),
            },
            { title: "Accepted values", dataIndex: "constraint", render: (value?: string) => value || "—" },
            { title: "Notes", dataIndex: "detail", render: (value?: string) => value || "—" },
          ]}
        />
      ) : null}

      {channel.examples?.map((example) => {
        const command = substituteAddress(example.command, endpoints, substitution);
        return (
          <div key={example.id} className="integration-example">
            <Space style={{ justifyContent: "space-between", width: "100%" }}>
              <Typography.Text strong>{example.title}</Typography.Text>
              <CopyButton text={command} />
            </Space>
            <Snippet command={command} />
          </div>
        );
      })}

      {channel.notes?.length ? (
        <ul className="integration-list">
          {channel.notes.map((note) => (
            <li key={note}>{note}</li>
          ))}
        </ul>
      ) : null}
    </Space>
  </Card>
);

const AccountGuideBody = ({ guide }: { guide: AccountGuide }) => {
  const [substitution, setSubstitution] = useState<Substitution>({ host: "", httpPort: "" });
  const text = useMemo(() => renderAccountText(guide, substitution), [guide, substitution]);

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Space wrap>
        <CopyButton text={text} label="Copy the whole pack" />
        <Button
          icon={<DownloadOutlined />}
          size="small"
          onClick={() => downloadText(`integration-${guide.username}.txt`, text)}
        >
          Download as text
        </Button>
      </Space>

      {guide.warnings?.length ? (
        <Alert
          type="warning"
          showIcon
          message="Read these before handing the pack over"
          description={
            <ul className="integration-list">
              {guide.warnings.map((warning) => (
                <li key={warning}>{warning}</li>
              ))}
            </ul>
          }
        />
      ) : null}

      <AddressBanner endpoints={guide.endpoints} substitution={substitution} onChange={setSubstitution} />

      <Card size="small" title="The account">
        <Descriptions column={1} size="small" bordered>
          <Descriptions.Item label="Username">
            <code>{guide.username}</code>
          </Descriptions.Item>
          <Descriptions.Item label="Password">
            Handed over at onboarding. It is stored one way and cannot be shown again — reset it on
            the Gateway users page if it was lost.
          </Descriptions.Item>
          <Descriptions.Item label="Charging">{guide.position.billing_mode}</Descriptions.Item>
          <Descriptions.Item label="Balance">
            {guide.position.live_error
              ? "could not be read"
              : `${amount(guide.position.remaining_balance)} remaining of ${amount(
                  guide.position.granted_balance,
                )} granted`}
          </Descriptions.Item>
          <Descriptions.Item label="Message quota">
            {guide.position.live_error
              ? "could not be read"
              : `${amount(guide.position.remaining_submit_sm_count)} remaining of ${amount(
                  guide.position.granted_submit_sm_count,
                )} granted`}
          </Descriptions.Item>
          <Descriptions.Item label="Throughput">
            HTTP {guide.position.http_throughput ?? "unlimited"} · SMPP{" "}
            {guide.position.smpps_throughput ?? "unlimited"} submits per second
          </Descriptions.Item>
          {guide.position.group_id ? (
            <Descriptions.Item label="Group ceiling">
              {guide.position.group_id} · {amount(guide.position.group_remaining_balance)} remaining
            </Descriptions.Item>
          ) : null}
        </Descriptions>
      </Card>

      {guide.channels.map((channel) => (
        <ChannelCard
          key={channel.id}
          channel={channel}
          endpoints={guide.endpoints}
          substitution={substitution}
        />
      ))}

      <Card size="small" title="What this account may set">
        <Table<Authorization>
          size="small"
          rowKey="key"
          pagination={false}
          dataSource={guide.authorizations}
          columns={[
            { title: "Capability", dataIndex: "label" },
            {
              title: "Allowed",
              dataIndex: "allowed",
              width: 110,
              render: (allowed: boolean) => (
                <StatusBadge tone={allowed ? "positive" : "neutral"}>{allowed ? "Yes" : "No"}</StatusBadge>
              ),
            },
            {
              title: "If used anyway",
              dataIndex: "effect",
              render: (effect?: string) => (effect ? <code>{effect}</code> : "—"),
            },
          ]}
        />
      </Card>

      <Card size="small" title="Value restrictions">
        {guide.filters.some((filter) => filter.constrains) ? (
          <Table<ValueFilter>
            size="small"
            rowKey="key"
            pagination={false}
            dataSource={guide.filters.filter((filter) => filter.constrains)}
            columns={[
              { title: "Applies to", dataIndex: "applies", render: (value: string) => <code>{value}</code> },
              { title: "Must match", dataIndex: "pattern", render: (value: string) => <code>{value}</code> },
              {
                title: "Rejection",
                dataIndex: "effect",
                render: (effect?: string) => (effect ? <code>{effect}</code> : "—"),
              },
            ]}
          />
        ) : (
          <Typography.Text type="secondary">
            No value filter constrains this account. Any destination, sender, priority or content the
            authorizations above allow is accepted.
          </Typography.Text>
        )}
        <Typography.Paragraph type="secondary" style={{ marginTop: 10, marginBottom: 0 }}>
          A pattern is matched from the <strong>start</strong> of the value and is not anchored at the
          end, so <code>^33</code> means “begins with 33”. To match a substring the pattern has to
          begin <code>.*</code>.
          {guide.default_source_address ? (
            <>
              {" "}
              When no sender is supplied, <code>{guide.default_source_address}</code> is used.
            </>
          ) : null}
        </Typography.Paragraph>
      </Card>

      <Card size="small" title="Delivery receipts and inbound callbacks">
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Alert
            type="warning"
            showIcon
            message="This is the one that catches everybody"
            description={
              <>
                A callback counts as delivered only when the receiver answers{" "}
                <strong>HTTP {guide.callbacks.success_status}</strong> <em>and</em> a body that, after
                trimming whitespace, is exactly <code>{guide.callbacks.ack_body}</code>. A{" "}
                <code>200</code> with an empty body, <code>OK</code>, or JSON is a failure and is
                retried.
              </>
            }
          />
          <div className="integration-example">
            <Space style={{ justifyContent: "space-between", width: "100%" }}>
              <Typography.Text strong>Minimal successful response</Typography.Text>
              <CopyButton text={guide.callbacks.example_response} />
            </Space>
            <Snippet command={guide.callbacks.example_response} />
          </div>
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label="Retries">
              {guide.callbacks.max_retries} after the first attempt ({guide.callbacks.total_attempts}{" "}
              in total), {guide.callbacks.retry_delay_seconds}s apart, then dropped
            </Descriptions.Item>
            <Descriptions.Item label="Timeout">{guide.callbacks.timeout_seconds}s per attempt</Descriptions.Item>
          </Descriptions>
          <Table<CallbackLevel>
            size="small"
            rowKey="requested"
            pagination={false}
            dataSource={guide.callbacks.levels}
            columns={[
              { title: "dlr-level", dataIndex: "requested", width: 90 },
              { title: "You receive", dataIndex: "delivered", width: 170 },
              { title: "Meaning", dataIndex: "detail" },
            ]}
          />
          <Table<Parameter>
            size="small"
            rowKey="name"
            pagination={false}
            title={() => "Delivery-receipt fields"}
            dataSource={guide.callbacks.dlr_fields}
            columns={[
              { title: "Field", dataIndex: "name", width: 220, render: (value: string) => <code>{value}</code> },
              { title: "Meaning", dataIndex: "detail" },
            ]}
          />
          <Table<Parameter>
            size="small"
            rowKey="name"
            pagination={false}
            title={() => "Inbound-message fields"}
            dataSource={guide.callbacks.mo_fields}
            columns={[
              { title: "Field", dataIndex: "name", width: 220, render: (value: string) => <code>{value}</code> },
              { title: "Meaning", dataIndex: "detail" },
            ]}
          />
          {guide.callbacks.notes?.length ? (
            <ul className="integration-list">
              {guide.callbacks.notes.map((note) => (
                <li key={note}>{note}</li>
              ))}
            </ul>
          ) : null}
        </Space>
      </Card>

      <Card size="small" title="Receiving messages">
        <Space direction="vertical" size={10} style={{ width: "100%" }}>
          {guide.inbound.bind_account ? (
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label="SMPP bind identity">
                <code>{guide.inbound.system_id}</code>
              </Descriptions.Item>
              <Descriptions.Item label="Permitted source IPs">
                {guide.inbound.ip_whitelist || "any IPv4 address (not restricted)"}
              </Descriptions.Item>
              <Descriptions.Item label="Concurrent sessions">
                {guide.inbound.max_bindings ?? "unlimited"}
              </Descriptions.Item>
            </Descriptions>
          ) : null}
          {guide.inbound.mo_routes.length ? (
            <Table<MORoute>
              size="small"
              rowKey="order"
              pagination={false}
              dataSource={guide.inbound.mo_routes}
              columns={[
                { title: "Order", dataIndex: "order", width: 90 },
                {
                  title: "From connector",
                  dataIndex: "from_connector",
                  render: (value?: string) => value || "any",
                },
                { title: "Delivered to", dataIndex: "destination" },
              ]}
            />
          ) : null}
          {guide.inbound.notes?.length ? (
            <ul className="integration-list">
              {guide.inbound.notes.map((note) => (
                <li key={note}>{note}</li>
              ))}
            </ul>
          ) : null}
        </Space>
      </Card>
    </Space>
  );
};

const ConnectorBriefBody = ({ brief }: { brief: ConnectorBrief }) => {
  const text = useMemo(() => renderConnectorText(brief), [brief]);
  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Space wrap>
        <CopyButton text={text} label="Copy the whole brief" />
        <Button
          icon={<DownloadOutlined />}
          size="small"
          onClick={() => downloadText(`carrier-link-${brief.connector_id}.txt`, text)}
        >
          Download as text
        </Button>
      </Space>

      <Alert
        type="info"
        showIcon
        message="This direction is inverted"
        description="A connector is us dialling out to them, so there is nothing for them to connect to. What is useful here is the agreement: what we will put on the wire, and what we still have to ask them for."
      />

      {brief.warnings?.length ? (
        <Alert
          type="warning"
          showIcon
          message="Silent defaults on this link"
          description={
            <ul className="integration-list">
              {brief.warnings.map((warning) => (
                <li key={warning}>{warning}</li>
              ))}
            </ul>
          }
        />
      ) : null}

      <Card size="small" title="The link as we have it configured">
        <Table<Setting>
          size="small"
          rowKey="name"
          pagination={false}
          dataSource={brief.link}
          columns={[
            { title: "Setting", dataIndex: "name", width: 200 },
            {
              title: "Value",
              dataIndex: "value",
              render: (value: string, setting: Setting) =>
                value ? <code>{value}</code> : <Typography.Text type="secondary">{setting.detail || "—"}</Typography.Text>,
            },
          ]}
        />
      </Card>

      <Card size="small" title="What we will send you">
        <Table<Setting>
          size="small"
          rowKey="name"
          pagination={false}
          dataSource={brief.we_will_send}
          columns={[
            { title: "Field", dataIndex: "name", width: 220 },
            {
              title: "Value",
              dataIndex: "value",
              render: (value: string) => (value ? <code>{value}</code> : <Typography.Text type="secondary">empty</Typography.Text>),
            },
            {
              title: "",
              dataIndex: "is_default",
              width: 190,
              render: (isDefault: boolean) =>
                isDefault ? (
                  <Tooltip title="Never configured, so this is the zero value. Zero is a real value on the wire.">
                    <span>
                      <StatusBadge tone="warning">Never configured</StatusBadge>
                    </span>
                  </Tooltip>
                ) : null,
            },
          ]}
        />
      </Card>

      <Card size="small" title="What we need from the carrier">
        <Table<CarrierRequest>
          size="small"
          rowKey="item"
          pagination={false}
          dataSource={brief.request_from_carrier}
          columns={[
            { title: "Ask for", dataIndex: "item", width: 280 },
            { title: "Why it matters", dataIndex: "detail" },
            {
              title: "We currently have",
              dataIndex: "known",
              width: 220,
              render: (value?: string) =>
                value ? <code>{value}</code> : <Typography.Text type="secondary">nothing</Typography.Text>,
            },
          ]}
        />
      </Card>
    </Space>
  );
};

type GuideTarget = { kind: "account"; id: string } | { kind: "connector"; id: string };

const GuideDrawer = ({
  target,
  open,
  onClose,
}: {
  target: GuideTarget;
  open: boolean;
  onClose: () => void;
}) => {
  const path = target.kind === "account" ? "users" : "connectors";
  const { data, isFetching, isError, error } = useCustom<AccountGuide | ConnectorBrief>({
    url: `${API_URL}/integration/${path}/${encodeURIComponent(target.id)}`,
    method: "get",
    queryOptions: { enabled: open },
  });
  const payload = data?.data;

  return (
    <Drawer
      open={open}
      onClose={onClose}
      width={860}
      destroyOnClose
      title={
        target.kind === "account"
          ? `How to send us traffic · ${target.id}`
          : `Carrier link brief · ${target.id}`
      }
    >
      {isFetching ? (
        <div style={{ padding: 32, textAlign: "center" }}>
          <Spin />
        </div>
      ) : isError ? (
        <Alert
          type="error"
          showIcon
          message="The instructions could not be generated"
          description={error instanceof Error ? error.message : "The gateway did not answer."}
        />
      ) : !payload ? (
        <Empty description="Nothing to show" />
      ) : payload.kind === "connector" ? (
        <ConnectorBriefBody brief={payload} />
      ) : (
        <AccountGuideBody guide={payload} />
      )}
    </Drawer>
  );
};

/**
 * IntegrationGuideButton is the trigger, safe to drop into a table whose rows
 * are themselves clickable.
 *
 * The wrapper's stopPropagation is load-bearing and not defensive coding. An
 * antd Drawer renders through a portal, but React events still bubble along the
 * *component* tree, not the DOM tree — so without this, every click inside the
 * opened drawer (including into its input fields) also fired the surrounding
 * table row's onClick and opened that row's own drawer on top. Found in the
 * browser; no type or unit check can see it.
 */
export const IntegrationGuideButton = ({
  kind,
  id,
  label = "Integration",
  hideText = false,
}: {
  kind: "account" | "connector";
  id: string;
  label?: string;
  hideText?: boolean;
}) => {
  const [open, setOpen] = useState(false);
  return (
    <span onClick={(event) => event.stopPropagation()}>
      <Tooltip
        title={
          kind === "account"
            ? "Generate the instructions this account needs in order to send us traffic"
            : "Generate the checklist of what to request from this carrier"
        }
      >
        <Button
          size="small"
          icon={<ApiOutlined />}
          aria-label={`Integration instructions for ${id}`}
          onClick={() => setOpen(true)}
        >
          {hideText ? null : label}
        </Button>
      </Tooltip>
      {open ? <GuideDrawer target={{ kind, id }} open={open} onClose={() => setOpen(false)} /> : null}
    </span>
  );
};
