import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  App,
  Button,
  Empty,
  Form,
  Drawer,
  Input,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from "antd";
import { DeleteOutlined, KeyOutlined, PlusOutlined, ReloadOutlined } from "@ant-design/icons";

import { PageTitle, StatusBadge, TableScrollHint } from "../../components/OperatorUI";
import { API_URL, httpClient } from "../../httpClient";

// Read tokens — the credentials a downstream application uses to fetch
// terminated messages from the spool over GET /messages on the REST listener.
//
// These are NOT gateway users and NOT the admin token. A gateway user submits
// traffic; the admin token administers the gateway. A read token only reads
// spooled message rows, only for the connectors named in its scope. That
// separation is the point: handing an application the admin token to read its
// own messages would give it the ability to mint users and start connectors.
//
// The secret is returned exactly once, by the create call. There is no endpoint
// that can show it again, because the service stores a SHA-256 proof and not the
// secret — so the create modal is the only chance to copy it, and the UI has to
// say so loudly rather than let it scroll past.

type Scope = {
  connectors: string[];
  include_text: boolean;
};

type ConsumerRow = {
  id: string;
  label: string;
  scope: Scope;
  revoked: boolean;
  created_at: string;
  updated_at: string;
  last_used_at: string | null;
  // Read history, aggregated from the access audit by the list endpoint.
  reads?: number;
  rows?: number;
  denied?: number;
  last_read_at?: string;
  token?: string;
  token_notice?: string;
};

type TerminationConnectorRow = { cid: string };

const apiErrorMessage = (error: unknown, fallback: string) => {
  if (typeof error === "object" && error !== null && "response" in error) {
    const response = (error as { response?: { data?: { message?: string }; status?: number } }).response;
    if (response?.data?.message) return response.data.message;
    if (response?.status === 404) {
      return "This gateway does not spool messages — configure a termination connector first.";
    }
  }
  return fallback;
};

export const MessageConsumerList = () => {
  const { message: toast } = App.useApp();
  const [rows, setRows] = useState<ConsumerRow[]>([]);
  const [connectors, setConnectors] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<ConsumerRow | undefined>();
  const [form] = Form.useForm();

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const { data } = await httpClient.get<ConsumerRow[] | { data: ConsumerRow[] }>(
        `${API_URL}/message-consumers`,
      );
      setRows(Array.isArray(data) ? data : (data.data ?? []));
    } catch (caught) {
      setError(apiErrorMessage(caught, "Could not list read tokens."));
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, []);

  // The scope names termination connectors, so offer the real ones rather than
  // a free-text box an operator can typo into a token that silently matches
  // nothing.
  const loadConnectors = useCallback(async () => {
    try {
      const { data } = await httpClient.get<
        TerminationConnectorRow[] | { data: TerminationConnectorRow[] }
      >(`${API_URL}/termination-connectors`);
      const list = Array.isArray(data) ? data : (data.data ?? []);
      setConnectors(list.map((row) => row.cid).filter(Boolean));
    } catch {
      // Non-fatal: the select falls back to free entry.
      setConnectors([]);
    }
  }, []);

  useEffect(() => {
    void load();
    void loadConnectors();
  }, [load, loadConnectors]);

  const create = async (values: { id: string; label?: string; connectors: string[]; include_text: boolean }) => {
    try {
      const { data } = await httpClient.post<ConsumerRow>(`${API_URL}/message-consumers`, {
        id: values.id,
        label: values.label ?? "",
        scope: {
          connectors: values.connectors,
          include_text: values.include_text ?? true,
        },
      });
      setCreating(false);
      form.resetFields();
      setIssued(data);
      void load();
    } catch (caught) {
      toast.error(apiErrorMessage(caught, "Could not create the read token."));
    }
  };

  const setRevoked = async (row: ConsumerRow, revoked: boolean) => {
    try {
      await httpClient.post(
        `${API_URL}/message-consumers/${encodeURIComponent(row.id)}/${revoked ? "revoke" : "unrevoke"}`,
      );
      toast.success(revoked ? `Revoked ${row.id}.` : `Restored ${row.id}.`);
      void load();
    } catch (caught) {
      toast.error(apiErrorMessage(caught, "Could not change the token's state."));
    }
  };

  const remove = async (row: ConsumerRow) => {
    try {
      await httpClient.delete(`${API_URL}/message-consumers/${encodeURIComponent(row.id)}`);
      toast.success(`Deleted ${row.id}.`);
      void load();
    } catch (caught) {
      toast.error(apiErrorMessage(caught, "Could not delete the token."));
    }
  };

  return (
    <>
      <PageTitle
        eyebrow="Termination"
        title="Read tokens"
        description="Credentials a downstream application uses to fetch terminated messages from the spool. Each token is scoped to the termination connectors you name, and is read-only — it cannot submit traffic or administer the gateway."
      />

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="How an application uses one"
        description={
          <Typography.Paragraph style={{ marginBottom: 0 }}>
            <code>
              curl -H &quot;Authorization: Bearer &lt;token&gt;&quot; http://&lt;gateway&gt;:8080/messages?limit=50
            </code>
            <br />
            Page with <code>?after=&lt;next_cursor&gt;</code> from the previous response. This is the{" "}
            <strong>pull</strong> path; the alternative is <strong>http-push</strong>, configured as the
            delivery endpoint on the termination connector itself.
          </Typography.Paragraph>
        }
      />

      <Space style={{ marginBottom: 16 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
          New read token
        </Button>
        <Button icon={<ReloadOutlined />} onClick={() => void load()} loading={loading}>
          Refresh
        </Button>
      </Space>

      {error ? (
        <Empty description={error} style={{ padding: 32 }} />
      ) : (
        <>
          <TableScrollHint />
          <Table<ConsumerRow>
            dataSource={rows}
            rowKey="id"
            loading={loading}
            pagination={false}
            scroll={{ x: "max-content" }}
          >
            <Table.Column dataIndex="id" title="ID" />
            <Table.Column
              dataIndex="label"
              title="Label"
              render={(value?: string) => value || <Typography.Text type="secondary">—</Typography.Text>}
            />
            <Table.Column<ConsumerRow>
              title="Scope"
              render={(_, row) => (
                <Space size={4} wrap>
                  {(row.scope?.connectors ?? []).map((cid) => (
                    <Tag key={cid}>{cid}</Tag>
                  ))}
                  {row.scope?.include_text ? (
                    <Tag color="orange">text</Tag>
                  ) : (
                    <Tag>metadata only</Tag>
                  )}
                </Space>
              )}
            />
            <Table.Column<ConsumerRow>
              dataIndex="revoked"
              title="State"
              render={(revoked: boolean) => (
                <StatusBadge tone={revoked ? "negative" : "positive"}>
                  {revoked ? "revoked" : "active"}
                </StatusBadge>
              )}
            />
            <Table.Column<ConsumerRow>
              title="Activity"
              render={(_, row) =>
                // reads and rows both, because either alone misleads: a
                // consumer polling with nothing to fetch is many reads and no
                // rows; one scripted sweep is one read and a great many rows.
                (row.reads ?? 0) === 0 ? (
                  <Typography.Text type="secondary">no reads</Typography.Text>
                ) : (
                  <Space size={4} wrap>
                    <Tag>{row.reads} reads</Tag>
                    <Tag color="blue">{row.rows} msgs</Tag>
                    {(row.denied ?? 0) > 0 && <Tag color="red">{row.denied} denied</Tag>}
                  </Space>
                )
              }
            />
            <Table.Column<ConsumerRow>
              dataIndex="last_used_at"
              title="Last used"
              render={(value: string | null) =>
                value ? (
                  new Date(value).toLocaleString()
                ) : (
                  // A token issued and never used is the one worth noticing:
                  // either the integration never shipped, or it is being held
                  // somewhere nobody is watching.
                  <Typography.Text type="warning">never</Typography.Text>
                )
              }
            />
            <Table.Column<ConsumerRow>
              title="Actions"
              fixed="right"
              render={(_, row) => (
                <Space>
                  <Button size="small" onClick={() => void setRevoked(row, !row.revoked)}>
                    {row.revoked ? "Restore" : "Revoke"}
                  </Button>
                  <Popconfirm
                    title="Delete this token?"
                    description="Any application still using it stops receiving messages immediately."
                    onConfirm={() => void remove(row)}
                  >
                    <Button size="small" danger icon={<DeleteOutlined />} />
                  </Popconfirm>
                </Space>
              )}
            />
          </Table>
        </>
      )}

      {/* Drawer, not Modal — the house pattern (see
          web/src/pages/termination-connectors/list.tsx). The list behind stays
          visible, which matters here: the scope you are about to grant is
          judged against the tokens that already exist. */}
      <Drawer
        open={creating}
        onClose={() => setCreating(false)}
        width={480}
        title="New read token"
        destroyOnClose
        extra={
          <Space>
            <Button onClick={() => setCreating(false)}>Cancel</Button>
            <Button type="primary" onClick={() => form.submit()}>
              Create
            </Button>
          </Space>
        }
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={create}
          initialValues={{ include_text: true, connectors: [] }}
        >
          <Form.Item
            name="id"
            label="ID"
            rules={[
              { required: true, message: "An ID is required." },
              {
                pattern: /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$/,
                message: "Letters, digits, dot, dash and underscore; must start alphanumeric.",
              },
            ]}
          >
            <Input placeholder="smsget-api-gateway" />
          </Form.Item>
          <Form.Item name="label" label="Label">
            <Input placeholder="What consumes this" />
          </Form.Item>
          <Form.Item
            name="connectors"
            label="Termination connectors"
            rules={[{ required: true, message: "Name at least one connector." }]}
            extra="The token can read only messages that arrived on these connectors. An empty scope is refused rather than treated as 'everything'."
          >
            <Select
              mode="tags"
              placeholder="terminate-local"
              options={connectors.map((cid) => ({ value: cid, label: cid }))}
            />
          </Form.Item>
          <Form.Item
            name="include_text"
            label="Include message text"
            valuePropName="checked"
            extra="Off returns metadata only — who sent what, when, with which verdict, but not the body."
          >
            <Switch />
          </Form.Item>
        </Form>
      </Drawer>

      {/* The one place a dismissable panel is load-bearing: this secret cannot
          be shown again. It stays a Drawer for consistency, but closes only on
          the explicit button — no mask-click or Escape dismissal to lose it by
          accident. */}
      <Drawer
        open={Boolean(issued)}
        onClose={() => setIssued(undefined)}
        width={520}
        closable={false}
        maskClosable={false}
        keyboard={false}
        title={
          <Space>
            <KeyOutlined /> Token for {issued?.id}
          </Space>
        }
        footer={
          <Button type="primary" block onClick={() => setIssued(undefined)}>
            I have copied it
          </Button>
        }
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="Shown once"
          description={
            issued?.token_notice ??
            "This token is not recoverable. If it is lost, delete this consumer and create another."
          }
        />
        <Typography.Paragraph copyable={{ text: issued?.token }} code style={{ wordBreak: "break-all" }}>
          {issued?.token}
        </Typography.Paragraph>
      </Drawer>
    </>
  );
};
