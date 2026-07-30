import { useState } from "react";
import { useCustom } from "@refinedev/core";
import { Alert, App, Button, Card, Descriptions, Input, Modal, Space, Table, Typography } from "antd";
import { ReloadOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import dayjs from "dayjs";

import { PageTitle, StatusBadge } from "../../components/OperatorUI";
import { API_URL, httpClient } from "../../httpClient";

type BillingSettingsPayload = {
  settings: {
    currency: string;
    retention_days: number;
    retention_batch_size: number;
    maintenance_interval_seconds: number;
    quota_persist_interval_seconds: number;
  };
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
            {settings?.currency ?? "—"}
          </Descriptions.Item>
          <Descriptions.Item label="Record retention">
            {payload?.retention_enabled ? (
              <>
                {settings?.retention_days} days, pruned {settings?.retention_batch_size} rows per
                batch
              </>
            ) : (
              <StatusBadge tone="neutral">Disabled — records are kept indefinitely</StatusBadge>
            )}
          </Descriptions.Item>
          <Descriptions.Item label="Maintenance interval">
            {seconds(settings?.maintenance_interval_seconds ?? 0, "24h")}
          </Descriptions.Item>
          <Descriptions.Item label="Quota flush interval">
            {seconds(settings?.quota_persist_interval_seconds ?? 0, "built-in")}
          </Descriptions.Item>
        </Descriptions>
        <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
          These are gateway configuration fields, not console state. Changing them
          means editing the configuration file and restarting; this page is
          read-only on purpose so it cannot disagree with what the process is
          actually running.
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
