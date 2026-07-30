import { useState } from "react";
import { Alert, Button, Collapse, Descriptions, Drawer, Space, Tooltip, Typography } from "antd";
import { InfoCircleOutlined } from "@ant-design/icons";

/**
 * Config-owned objects are read-only in the console because the gateway's JSON
 * file is re-read at every boot and wins: an edit made here would apply live and
 * then silently revert at the next restart. Marking a row "Read only" without
 * showing what it contains left the operator unable to answer "read-only, but
 * set to what?" — which is the question they actually have. This reveals the
 * whole live definition, still without offering an edit that could not stick.
 *
 * Nothing secret reaches this component: the BFF blanks connector, bind and user
 * passwords before serialising, so the record it renders has no credential in it.
 */

const HIDDEN_KEYS = new Set(["id", "managed_by"]);

const humanize = (key: string) =>
  key
    .replace(/_/g, " ")
    .replace(/\b(id|ip|url|tps|ton|npi|dlr|smpps|http|smsc|pdu|tls|sm)\b/gi, (match) =>
      match.toUpperCase(),
    )
    .replace(/^./, (character) => character.toUpperCase());

const renderValue = (value: unknown) => {
  if (typeof value === "boolean") return value ? "Yes" : "No";
  if (Array.isArray(value)) return value.length ? value.join(", ") : "—";
  if (value !== null && typeof value === "object") return JSON.stringify(value);
  return String(value);
};

// An empty string, null or undefined means "not set on this object" and is
// omitted; false and 0 are real values and are kept, because a throughput of 0
// or a disabled flag of false both mean something specific here.
const isPresent = (value: unknown) =>
  value !== null && value !== undefined && value !== "" && !(Array.isArray(value) && !value.length);

type ConfigDetailProps = {
  /** What kind of object this is, e.g. "connector" — used in the drawer title. */
  kind: string;
  /** The object's identity, shown beside the kind. */
  name: string;
  record: Record<string, unknown>;
  /** Text shown next to the info button. */
  label?: string;
};

/**
 * ReadOnlyCell renders the "Read only" marker plus an info button that opens the
 * full definition. Drop it wherever a config-owned row would otherwise offer
 * actions.
 */
export const ReadOnlyCell = ({ kind, name, record, label = "Read only" }: ConfigDetailProps) => {
  const [open, setOpen] = useState(false);
  const entries = Object.entries(record).filter(
    ([key, value]) => !HIDDEN_KEYS.has(key) && isPresent(value),
  );

  return (
    <Space size={4}>
      <span className="muted-copy">{label}</span>
      <Tooltip title={`Show the ${kind} configuration`}>
        <Button
          size="small"
          type="text"
          icon={<InfoCircleOutlined />}
          aria-label={`Show the configuration of ${kind} ${name}`}
          onClick={() => setOpen(true)}
        />
      </Tooltip>
      <Drawer
        open={open}
        onClose={() => setOpen(false)}
        width={520}
        title={`${kind} · ${name}`}
        destroyOnClose
      >
        <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <Alert
            type="info"
            showIcon
            message="Owned by the gateway configuration file"
            description={
              <>
                This object is declared in the gateway's config file, which is re-read at every
                start and takes precedence over the admin plane. To change it, edit the file and
                restart — or remove it from the file and re-create it here, after which it becomes
                live-editable.
              </>
            }
          />
          <Descriptions column={1} size="small" bordered>
            {entries.map(([key, value]) => (
              <Descriptions.Item key={key} label={humanize(key)}>
                {renderValue(value)}
              </Descriptions.Item>
            ))}
          </Descriptions>
          <Collapse
            ghost
            items={[
              {
                key: "raw",
                label: "Raw values",
                children: (
                  <Typography.Paragraph>
                    <pre style={{ margin: 0, fontSize: 12, overflowX: "auto" }}>
                      {JSON.stringify(record, null, 2)}
                    </pre>
                  </Typography.Paragraph>
                ),
              },
            ]}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Passwords are never sent to the browser, so they do not appear here.
          </Typography.Text>
        </Space>
      </Drawer>
    </Space>
  );
};
