import { useState } from "react";
import { useCustom } from "@refinedev/core";
import { Alert, Button, Card, Descriptions, Drawer, Space, Table, Tag, Tooltip, Typography } from "antd";
import { FileSearchOutlined, ReloadOutlined } from "@ant-design/icons";
import { Link } from "react-router-dom";

import { PageTitle, StatusBadge } from "../../components/OperatorUI";
import { IntegrationGuideButton } from "../../components/IntegrationGuide";
import { API_URL } from "../../httpClient";

type BillingAccount = {
  id: string;
  username: string;
  managed_by: string;
  group_id?: string;
  disabled: boolean;
  granted_balance: number | null;
  remaining_balance: number | null;
  granted_submit_sm_count: number | null;
  remaining_submit_sm_count: number | null;
  early_decrement_balance_percent?: number | null;
  billing_mode: string;
  live_error?: string;
  group_granted_balance?: number | null;
  group_remaining_balance?: number | null;
  group_granted_submit_sm_count?: number | null;
  group_remaining_submit_sm_count?: number | null;
  group_live_error?: string;
  group_disabled?: boolean;
};

// UNLIMITED is not "no ceiling configured yet" — it is a balance of null, which
// the billing engine never charges. Showing it as a number would invite an
// operator to think the customer is prepaid.
const UNLIMITED = "unlimited";

const amount = (value: number | null | undefined) =>
  value === null || value === undefined ? UNLIMITED : value.toLocaleString(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 4,
  });

const whole = (value: number | null | undefined) =>
  value === null || value === undefined ? UNLIMITED : value.toLocaleString();

const modeTone = (mode: string) => {
  switch (mode) {
    case "PREPAID":
      return "positive" as const;
    case "SPLIT":
      return "progress" as const;
    case "UNLIMITED":
      return "neutral" as const;
    default:
      return "neutral" as const;
  }
};

// remainingTone flags an account an operator should act on: spent, or close to
// it. It is only applied to a real number — an unlimited or unreadable value
// must never be painted as healthy or as empty.
const remainingTone = (granted: number | null | undefined, remaining: number | null | undefined) => {
  if (remaining === null || remaining === undefined) return "neutral" as const;
  if (remaining <= 0) return "negative" as const;
  if (granted && granted > 0 && remaining / granted < 0.1) return "warning" as const;
  return "positive" as const;
};

export const BillingAccountsPage = () => {
  const [selected, setSelected] = useState<BillingAccount | null>(null);
  const { data, isFetching, refetch } = useCustom<BillingAccount[]>({
    url: `${API_URL}/billing/accounts`,
    method: "get",
  });
  const accounts = data?.data ?? [];
  const unreadable = accounts.filter((account) => account.live_error);

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <PageTitle
        eyebrow="Billing"
        title="Accounts"
        description="What each customer was granted, and what they have left right now. The granted figure is the provisioned spec; the remaining figure is the live billing state and moves with every charge."
      />

      {unreadable.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message="Some live balances could not be read"
          description={`${unreadable
            .map((account) => account.username)
            .join(", ")} — the remaining column is blank rather than zero, because "cannot read" and "no money" call for different actions.`}
        />
      )}

      <Card
        title="Customer balances"
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => refetch()} loading={isFetching}>
            Refresh
          </Button>
        }
      >
        <Table
          rowKey="id"
          dataSource={accounts}
          loading={isFetching}
          pagination={{ pageSize: 25, hideOnSinglePage: true }}
          scroll={{ x: true }}
          rowClassName={() => "is-clickable"}
          onRow={(account) => ({
            onClick: () => setSelected(account),
            style: { cursor: "pointer" },
          })}
          columns={[
            {
              title: "",
              dataIndex: "id",
              width: 44,
              render: () => <FileSearchOutlined aria-hidden="true" style={{ opacity: 0.55 }} />,
            },
            {
              title: "Customer",
              dataIndex: "username",
              render: (username: string, account: BillingAccount) => (
                <Space direction="vertical" size={0}>
                  <Typography.Text strong>{username}</Typography.Text>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {account.managed_by === "config" ? "config-owned" : "admin-managed"}
                    {account.group_id ? ` · group ${account.group_id}` : ""}
                  </Typography.Text>
                </Space>
              ),
            },
            {
              title: "Status",
              dataIndex: "disabled",
              render: (disabled: boolean, account: BillingAccount) =>
                disabled ? (
                  <StatusBadge tone="negative">Disabled</StatusBadge>
                ) : account.group_disabled ? (
                  <StatusBadge tone="warning">Group disabled</StatusBadge>
                ) : (
                  <StatusBadge tone="positive">Active</StatusBadge>
                ),
            },
            {
              title: "Mode",
              dataIndex: "billing_mode",
              render: (mode: string, account: BillingAccount) => (
                <Tooltip
                  title={
                    mode === "SPLIT"
                      ? `${account.early_decrement_balance_percent}% is taken at submit, the rest when the SMSC accepts`
                      : mode === "UNLIMITED"
                        ? "A null balance is never charged"
                        : "The whole rate is taken at submit"
                  }
                >
                  <span>
                    <StatusBadge tone={modeTone(mode)}>{mode}</StatusBadge>
                  </span>
                </Tooltip>
              ),
            },
            {
              title: "Balance granted",
              dataIndex: "granted_balance",
              align: "right",
              render: (value: number | null) => amount(value),
            },
            {
              title: "Balance remaining",
              dataIndex: "remaining_balance",
              align: "right",
              render: (value: number | null, account: BillingAccount) =>
                account.live_error ? (
                  <Tooltip title={account.live_error}>
                    <Typography.Text type="secondary">not readable</Typography.Text>
                  </Tooltip>
                ) : (
                  <StatusBadge tone={remainingTone(account.granted_balance, value)}>
                    {amount(value)}
                  </StatusBadge>
                ),
            },
            {
              title: "Messages granted",
              dataIndex: "granted_submit_sm_count",
              align: "right",
              render: (value: number | null) => whole(value),
            },
            {
              title: "Messages remaining",
              dataIndex: "remaining_submit_sm_count",
              align: "right",
              render: (value: number | null, account: BillingAccount) =>
                account.live_error ? (
                  <Typography.Text type="secondary">not readable</Typography.Text>
                ) : (
                  whole(value)
                ),
            },
            {
              title: "Group ceiling",
              dataIndex: "group_id",
              render: (_: string, account: BillingAccount) => {
                if (!account.group_id) {
                  return <Typography.Text type="secondary">—</Typography.Text>;
                }
                // An unreadable ceiling must never render as "unlimited": the
                // granted side is still known, the remaining side is not.
                const remainingBalance = account.group_live_error ? (
                  <Typography.Text type="secondary">not readable</Typography.Text>
                ) : (
                  amount(account.group_remaining_balance)
                );
                const remainingCount = account.group_live_error
                  ? "not readable"
                  : whole(account.group_remaining_submit_sm_count);
                return (
                  <Tooltip title={account.group_live_error || ""}>
                    <Space direction="vertical" size={0}>
                      <span>
                        {remainingBalance} / {amount(account.group_granted_balance)}
                      </span>
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                        {remainingCount} / {whole(account.group_granted_submit_sm_count)} messages
                      </Typography.Text>
                    </Space>
                  </Tooltip>
                );
              },
            },
            {
              // The row itself opens the billing drawer, so this button stops
              // propagation of its own click.
              title: "Integration",
              dataIndex: "id",
              align: "right",
              width: 150,
              render: (_: string, account: BillingAccount) => (
                <IntegrationGuideButton kind="account" id={account.id} label="Instructions" />
              ),
            },
          ]}
        />
        <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
          Select a customer for their full billing position. A customer is refused
          when either their own ceiling or their group's is
          spent, so a healthy personal balance under an exhausted group still
          cannot send. Grants are edited on the Users and Groups pages; nothing on
          this page changes them.
        </Typography.Paragraph>
      </Card>

      <Drawer
        open={selected !== null}
        onClose={() => setSelected(null)}
        width={560}
        title={selected ? `Billing · ${selected.username}` : ""}
        destroyOnClose
      >
        {selected ? (
          <Space direction="vertical" size={16} style={{ width: "100%" }}>
            {selected.live_error ? (
              <Alert
                type="warning"
                showIcon
                message="Live balance could not be read"
                description={selected.live_error}
              />
            ) : null}
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label="Status">
                {selected.disabled ? (
                  <StatusBadge tone="negative">Disabled</StatusBadge>
                ) : (
                  <StatusBadge tone="positive">Active</StatusBadge>
                )}
              </Descriptions.Item>
              <Descriptions.Item label="Managed by">
                <Tag color={selected.managed_by === "config" ? "blue" : "green"}>
                  {selected.managed_by === "config" ? "Config file" : "Admin plane"}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="Group">{selected.group_id || "—"}</Descriptions.Item>
              <Descriptions.Item label="Charging">
                {selected.billing_mode}
                {selected.early_decrement_balance_percent
                  ? ` · ${selected.early_decrement_balance_percent}% taken at submit`
                  : ""}
              </Descriptions.Item>
              <Descriptions.Item label="Balance">
                {amount(selected.remaining_balance)} remaining of {amount(selected.granted_balance)}{" "}
                granted
              </Descriptions.Item>
              <Descriptions.Item label="Messages">
                {whole(selected.remaining_submit_sm_count)} remaining of{" "}
                {whole(selected.granted_submit_sm_count)} granted
              </Descriptions.Item>
              {selected.group_id ? (
                <Descriptions.Item label="Group ceiling">
                  {selected.group_live_error ? "not readable" : amount(selected.group_remaining_balance)} of{" "}
                  {amount(selected.group_granted_balance)} ·{" "}
                  {selected.group_live_error
                    ? "not readable"
                    : whole(selected.group_remaining_submit_sm_count)}{" "}
                  of {whole(selected.group_granted_submit_sm_count)} messages
                  {selected.group_live_error ? (
                    <Typography.Text type="secondary" style={{ display: "block", fontSize: 12 }}>
                      {selected.group_live_error}
                    </Typography.Text>
                  ) : null}
                </Descriptions.Item>
              ) : null}
            </Descriptions>

            {selected.managed_by === "config" ? (
              <Alert
                type="info"
                showIcon
                message="This account is owned by the configuration file"
                description="Its grant cannot be changed from the console, so the remaining balance can only fall. Edit the gateway config file and restart to top it up — and change the number, because re-granting the same value is a no-op across a restart."
              />
            ) : null}

            <Space wrap>
              <Link to={`/billing/usage?user=${encodeURIComponent(selected.username)}`}>
                <Button type="primary">View this customer's usage</Button>
              </Link>
              {selected.managed_by !== "config" ? (
                <Link to="/users">
                  <Button>Edit the grant</Button>
                </Link>
              ) : null}
            </Space>
          </Space>
        ) : null}
      </Drawer>
    </Space>
  );
};
