import { useEffect, useState } from "react";
import { useCustom } from "@refinedev/core";
import { Alert, App, Button, Card, Descriptions, Input, InputNumber, Modal, Space, Table, Tag, Tooltip, Typography } from "antd";
import { ReloadOutlined, RollbackOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import dayjs from "dayjs";

import { PageTitle, StatusBadge } from "../../components/OperatorUI";
import { API_URL, httpClient } from "../../httpClient";

type SettingName =
  | "cdr_retention_days"
  | "cdr_retention_batch_size"
  | "cdr_maintenance_interval_seconds"
  | "quota_persist_interval_seconds";

type BillingSettingsPayload = {
  settings: {
    currency: string;
    retention_days: number;
    retention_batch_size: number;
    maintenance_interval_seconds: number;
    quota_persist_interval_seconds: number;
  };
  overrides: Partial<Record<SettingName, number>>;
  editable_settings: SettingName[];
  currency_is_placeholder: boolean;
  retention_enabled: boolean;
  cdr_available: boolean;
  editable: boolean;
};

type ReconciliationReport = {
  checked_at: string;
  healthy: boolean;
  issues: Array<{ code: string; count: number }>;
};

const apiErrorMessage = (error: unknown, fallback: string) =>
  typeof error === "object" &&
  error !== null &&
  "response" in error &&
  typeof error.response === "object" &&
  error.response !== null &&
  "data" in error.response
    ? String((error.response.data as { message?: string }).message ?? fallback)
    : fallback;

const seconds = (value: number, fallback: string) =>
  value > 0 ? `${value}s` : `${fallback} (default)`;

// SettingRow edits one setting in place. It shows whether the value currently
// in force came from the configuration file or from an operator override, and
// offers the way back — an override with no way to revert is a trap.
const SettingRow = ({
  name,
  value,
  overridden,
  editable,
  saving,
  onWrite,
  hint,
}: {
  name: SettingName;
  value: number;
  overridden: boolean;
  editable: boolean;
  saving: boolean;
  onWrite: (name: SettingName, value: number | null) => Promise<void>;
  hint?: string;
}) => {
  const [draft, setDraft] = useState<number | null>(value);
  useEffect(() => setDraft(value), [value]);

  if (!editable) {
    return (
      <Space>
        {value}
        {hint ? <Typography.Text type="secondary">· {hint}</Typography.Text> : null}
      </Space>
    );
  }
  return (
    <Space wrap>
      <InputNumber
        min={0}
        value={draft}
        onChange={setDraft}
        style={{ width: 130 }}
        aria-label={name}
      />
      <Button
        size="small"
        type="primary"
        loading={saving}
        disabled={draft === null || draft === value}
        onClick={() => void onWrite(name, draft)}
      >
        Apply
      </Button>
      {overridden ? (
        <>
          <Tag color="gold">overriding the file</Tag>
          <Tooltip title="Revert to the value in the configuration file">
            <Button
              size="small"
              icon={<RollbackOutlined />}
              loading={saving}
              onClick={() => void onWrite(name, null)}
            />
          </Tooltip>
        </>
      ) : (
        <Tag>from the file</Tag>
      )}
      {hint ? <Typography.Text type="secondary">{hint}</Typography.Text> : null}
    </Space>
  );
};

export const BillingSettingsPage = () => {
  const { message } = App.useApp();
  const { data, isFetching, refetch } = useCustom<BillingSettingsPayload>({
    url: `${API_URL}/billing/settings`,
    method: "get",
  });
  const payload = data?.data;
  const settings = payload?.settings;
  const [report, setReport] = useState<ReconciliationReport | null>(null);
  const [reconciling, setReconciling] = useState(false);
  const [pruneOpen, setPruneOpen] = useState(false);
  const [pruneConfirm, setPruneConfirm] = useState("");
  const [pruning, setPruning] = useState(false);
  const [savingSetting, setSavingSetting] = useState<SettingName | null>(null);

  // A setting is written by applying it to the running process first; the API
  // refuses to store anything the runtime will not take, so a value that comes
  // back is genuinely in force.
  const writeSetting = async (name: SettingName, value: number | null) => {
    setSavingSetting(name);
    try {
      await httpClient.put(`${API_URL}/billing/settings`, { name, value });
      await refetch();
      message.success(value === null ? "Reverted to the configuration file" : "Applied");
    } catch (error) {
      message.error(apiErrorMessage(error, "Could not change the setting"));
    } finally {
      setSavingSetting(null);
    }
  };

  const reconcile = async () => {
    setReconciling(true);
    try {
      const response = await httpClient.post(`${API_URL}/billing/reconcile`);
      setReport(response.data);
      if (response.data.healthy) {
        message.success("Ledger reconciled: no mismatches");
      } else {
        message.warning("Reconciliation found mismatches");
      }
    } catch (error) {
      message.error(apiErrorMessage(error, "Reconciliation failed"));
    } finally {
      setReconciling(false);
    }
  };

  const prune = async () => {
    setPruning(true);
    try {
      const response = await httpClient.post(`${API_URL}/billing/prune`, { confirm: "PRUNE" });
      message.success(
        `Pruned ${response.data.records} records and ${response.data.events} events (one batch)`,
      );
      setPruneOpen(false);
      setPruneConfirm("");
    } catch (error) {
      message.error(apiErrorMessage(error, "Prune failed"));
    } finally {
      setPruning(false);
    }
  };

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <PageTitle
        eyebrow="Billing"
        title="Settings and ledger maintenance"
        description="The deployment-wide commercial configuration, and the two maintenance actions over the durable record ledger."
      />

      {payload && !payload.cdr_available && (
        <Alert
          type="warning"
          showIcon
          message="No commercial record service is configured"
          description="Usage and statements are unavailable in this deployment. Charging still applies; only the durable record surface is absent."
        />
      )}

      {payload?.currency_is_placeholder && (
        <Alert
          type="warning"
          showIcon
          message="Currency is XXX — ISO 4217 for “no currency”"
          description="This is the deliberate default: inherited route rates are unitless, so labelling them with a real currency would make every amount in this console commercially misleading. Set cdr_currency in the gateway configuration once the operator owns a settlement currency."
        />
      )}

      <Card
        title="Configuration"
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => refetch()} loading={isFetching}>
            Refresh
          </Button>
        }
      >
        <Descriptions column={1} bordered size="small">
          <Descriptions.Item label="Settlement currency">
            <Space>
              {settings?.currency ?? "—"}
              <Tooltip title="Currency stamps new records only, so changing it mid-window would split a customer's usage across two units. It stays a configuration-file decision.">
                <Tag>configuration file only</Tag>
              </Tooltip>
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="Record retention (days)">
            <SettingRow
              name="cdr_retention_days"
              value={settings?.retention_days ?? 0}
              overridden={payload?.overrides?.cdr_retention_days !== undefined}
              editable={Boolean(payload?.editable)}
              saving={savingSetting === "cdr_retention_days"}
              onWrite={writeSetting}
              hint={payload?.retention_enabled ? undefined : "0 keeps records indefinitely"}
            />
          </Descriptions.Item>
          <Descriptions.Item label="Prune batch size">
            <SettingRow
              name="cdr_retention_batch_size"
              value={settings?.retention_batch_size ?? 0}
              overridden={payload?.overrides?.cdr_retention_batch_size !== undefined}
              editable={Boolean(payload?.editable)}
              saving={savingSetting === "cdr_retention_batch_size"}
              onWrite={writeSetting}
              hint="rows removed per prune call"
            />
          </Descriptions.Item>
          <Descriptions.Item label="Maintenance interval (seconds)">
            <SettingRow
              name="cdr_maintenance_interval_seconds"
              value={settings?.maintenance_interval_seconds ?? 0}
              overridden={payload?.overrides?.cdr_maintenance_interval_seconds !== undefined}
              editable={Boolean(payload?.editable)}
              saving={savingSetting === "cdr_maintenance_interval_seconds"}
              onWrite={writeSetting}
              hint={seconds(settings?.maintenance_interval_seconds ?? 0, "24h")}
            />
          </Descriptions.Item>
          <Descriptions.Item label="Quota flush interval (seconds)">
            <SettingRow
              name="quota_persist_interval_seconds"
              value={settings?.quota_persist_interval_seconds ?? 0}
              overridden={payload?.overrides?.quota_persist_interval_seconds !== undefined}
              editable={Boolean(payload?.editable)}
              saving={savingSetting === "quota_persist_interval_seconds"}
              onWrite={writeSetting}
              hint={seconds(settings?.quota_persist_interval_seconds ?? 0, "built-in")}
            />
          </Descriptions.Item>
        </Descriptions>
        <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
          These four take effect immediately and survive a restart: the value is
          applied to the running gateway first and only stored if it takes, then
          re-applied at boot. A changed value overrides the configuration file —
          revert it to hand the setting back. Everything else on this page is
          applied once at startup and can only be changed in the file.
        </Typography.Paragraph>
      </Card>

      <Card
        title="Ledger maintenance"
        extra={
          <Space>
            <Button
              icon={<SafetyCertificateOutlined />}
              onClick={() => void reconcile()}
              loading={reconciling}
              disabled={!payload?.cdr_available}
            >
              Reconcile now
            </Button>
            <Button
              danger
              onClick={() => setPruneOpen(true)}
              disabled={!payload?.cdr_available || !payload?.retention_enabled}
            >
              Prune one batch
            </Button>
          </Space>
        }
      >
        {report ? (
          <Space direction="vertical" size={12} style={{ width: "100%" }}>
            <Space>
              <StatusBadge tone={report.healthy ? "positive" : "negative"}>
                {report.healthy ? "No mismatches" : "Mismatches found"}
              </StatusBadge>
              <Typography.Text type="secondary">
                checked {dayjs(report.checked_at).format("YYYY-MM-DD HH:mm:ss")}
              </Typography.Text>
            </Space>
            <Table
              rowKey="code"
              size="small"
              pagination={false}
              dataSource={report.issues}
              columns={[
                { title: "Check", dataIndex: "code" },
                {
                  title: "Rows affected",
                  dataIndex: "count",
                  align: "right",
                  render: (value: number) =>
                    value > 0 ? (
                      <StatusBadge tone="negative">{value}</StatusBadge>
                    ) : (
                      <Typography.Text type="secondary">0</Typography.Text>
                    ),
                },
              ]}
            />
          </Space>
        ) : (
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            Reconciliation cross-checks the commercial records against the submit
            transactions and the billing ledger — a record with no submit part, a
            charge with no record, a projection that disagrees with the ledger. The
            gateway runs it on a schedule; this button runs it now.
          </Typography.Paragraph>
        )}
      </Card>

      <Modal
        open={pruneOpen}
        title="Delete records past the retention window"
        okText="Prune"
        okButtonProps={{ danger: true, disabled: pruneConfirm !== "PRUNE", loading: pruning }}
        onOk={() => void prune()}
        onCancel={() => {
          setPruneOpen(false);
          setPruneConfirm("");
        }}
      >
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Typography.Paragraph style={{ marginBottom: 0 }}>
            This permanently deletes commercial records older than{" "}
            <strong>{settings?.retention_days} days</strong>, and their event
            history. These are the records an invoice dispute would be settled
            from. One click prunes at most{" "}
            <strong>{settings?.retention_batch_size}</strong> records.
          </Typography.Paragraph>
          <Typography.Text>
            Type <Typography.Text code>PRUNE</Typography.Text> to confirm:
          </Typography.Text>
          <Input
            value={pruneConfirm}
            onChange={(event) => setPruneConfirm(event.target.value)}
            placeholder="PRUNE"
          />
        </Space>
      </Modal>
    </Space>
  );
};
