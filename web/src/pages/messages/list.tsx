import { useCallback, useEffect, useState } from "react";
import {
  App,
  Button,
  Descriptions,
  Empty,
  Form,
  Drawer,
  Input,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import { EyeOutlined, ReloadOutlined, SearchOutlined } from "@ant-design/icons";

import { PageTitle, StatusBadge, TableScrollHint, type StatusTone } from "../../components/OperatorUI";
import { API_URL, httpClient } from "../../httpClient";

// The spool browser.
//
// This page does NOT use useTable/the refine data provider, deliberately. The
// spool pages by an opaque cursor (a store-allocated sequence, so a row
// re-spooled after a redelivery is handed back rather than skipped), and there
// is no total count to page against — refine's offset pagination would have to
// invent both. So this fetches directly and appends, which is also what the
// cursor is actually for.
//
// Content is not in the listing. The API returns text only for
// ?include_content=true, and every such read is audited as a reveal, so a
// listing that always asked for it would make "who read message text"
// unanswerable. Opening one message is the deliberate act; that is the Reveal
// button.

type MessageRow = {
  message_id: string;
  connector_id: string;
  user_id?: string;
  source_addr: string;
  dest_addr: string;
  received_at: string;
  encoding?: string;
  parts?: number;
  verdict_stat?: string;
  delivery_state?: string;
  delivery_attempts: number;
  text?: string;
  raw_hex?: string;
};

type MessagePage = {
  messages: MessageRow[];
  next_cursor?: string;
};

type ConnectorOption = { cid: string };
type UserOption = { username: string };

type Filters = {
  connector?: string;
  to?: string;
  user?: string;
  delivery_state?: string;
};

const deliveryTone = (state?: string): StatusTone => {
  switch (state) {
    case "delivered":
      return "positive";
    case "pending":
      return "progress";
    case "failed":
    case "dead":
      return "negative";
    default:
      return "neutral";
  }
};

const verdictTone = (stat?: string): StatusTone => {
  if (!stat) return "neutral";
  return stat.toUpperCase() === "DELIVRD" ? "positive" : "negative";
};

const apiErrorMessage = (error: unknown, fallback: string) => {
  if (typeof error === "object" && error !== null && "response" in error) {
    const response = (error as { response?: { data?: { message?: string }; status?: number } }).response;
    if (response?.data?.message) return response.data.message;
    if (response?.status === 404) {
      return "This gateway does not spool messages — no termination connector is configured.";
    }
  }
  return fallback;
};

export const MessageList = () => {
  const { message: toast } = App.useApp();
  const [rows, setRows] = useState<MessageRow[]>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [filters, setFilters] = useState<Filters>({});
  const [revealed, setRevealed] = useState<MessageRow | undefined>();
  const [revealing, setRevealing] = useState(false);
  // Connector and partner are chosen from what actually exists rather than
  // typed: both are exact-match filters server-side, so a typo silently returns
  // an empty list that reads as "no traffic" instead of "no such connector".
  const [connectors, setConnectors] = useState<string[]>([]);
  const [partners, setPartners] = useState<string[]>([]);

  const load = useCallback(
    async (after: string | undefined, applied: Filters, append: boolean) => {
      setLoading(true);
      setError(undefined);
      try {
        const { data } = await httpClient.get<MessagePage>(`${API_URL}/messages`, {
          params: {
            after,
            limit: 50,
            connector: applied.connector || undefined,
            to: applied.to || undefined,
            user: applied.user || undefined,
            delivery_state: applied.delivery_state || undefined,
          },
        });
        setRows((previous) => (append ? [...previous, ...data.messages] : data.messages));
        setCursor(data.next_cursor);
      } catch (caught) {
        setError(apiErrorMessage(caught, "Could not read the message spool."));
        if (!append) setRows([]);
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const loadOptions = useCallback(async () => {
    try {
      const { data } = await httpClient.get<ConnectorOption[] | { data: ConnectorOption[] }>(
        `${API_URL}/termination-connectors`,
      );
      const list = Array.isArray(data) ? data : (data.data ?? []);
      setConnectors(list.map((row) => row.cid).filter(Boolean));
    } catch {
      setConnectors([]);
    }
    try {
      const { data } = await httpClient.get<UserOption[] | { data: UserOption[] }>(
        `${API_URL}/users`,
      );
      const list = Array.isArray(data) ? data : (data.data ?? []);
      setPartners(list.map((row) => row.username).filter(Boolean));
    } catch {
      setPartners([]);
    }
  }, []);

  useEffect(() => {
    void load(undefined, {}, false);
    void loadOptions();
  }, [load, loadOptions]);

  // Reveal is a single-message read on purpose: it is the audited act, and the
  // row already in hand carries no content to fall back on.
  const reveal = async (row: MessageRow) => {
    setRevealing(true);
    try {
      const { data } = await httpClient.get<MessageRow>(
        `${API_URL}/messages/${encodeURIComponent(row.message_id)}`,
      );
      setRevealed(data);
    } catch (caught) {
      toast.error(apiErrorMessage(caught, "Could not reveal this message."));
    } finally {
      setRevealing(false);
    }
  };

  return (
    <>
      <PageTitle
        eyebrow="Traffic"
        title="Messages"
        description="Messages terminated locally and held in the spool for its retention window (24h by default), newest first. Message text is not shown in this list — revealing it is recorded against your operator name."
      />

      <Form<Filters>
        layout="inline"
        style={{ marginBottom: 16, rowGap: 8 }}
        onFinish={(values) => {
          setFilters(values);
          void load(undefined, values, false);
        }}
      >
        <Form.Item name="to">
          <Input allowClear placeholder="Destination number" style={{ width: 190 }} />
        </Form.Item>
        <Form.Item name="connector">
          <Select
            allowClear
            showSearch
            placeholder="Connector"
            style={{ width: 200 }}
            options={connectors.map((cid) => ({ value: cid, label: cid }))}
            notFoundContent="no termination connectors"
          />
        </Form.Item>
        <Form.Item name="user">
          <Select
            allowClear
            showSearch
            placeholder="Partner"
            style={{ width: 200 }}
            options={partners.map((u) => ({ value: u, label: u }))}
            notFoundContent="no users"
          />
        </Form.Item>
        <Form.Item name="delivery_state">
          <Select
            allowClear
            placeholder="Delivery state"
            style={{ width: 170 }}
            options={[
              { value: "pending", label: "pending" },
              { value: "delivered", label: "delivered" },
              { value: "failed", label: "failed" },
              { value: "dead", label: "dead" },
            ]}
          />
        </Form.Item>
        <Form.Item>
          <Space>
            <Button htmlType="submit" icon={<SearchOutlined />} loading={loading}>
              Search
            </Button>
            <Button
              icon={<ReloadOutlined />}
              onClick={() => void load(undefined, filters, false)}
              loading={loading}
            >
              Refresh
            </Button>
          </Space>
        </Form.Item>
      </Form>

      {error ? (
        <Empty description={error} style={{ padding: 32 }} />
      ) : (
        <>
          <TableScrollHint />
          <Table<MessageRow>
            dataSource={rows}
            rowKey="message_id"
            loading={loading}
            pagination={false}
            scroll={{ x: "max-content" }}
          >
            <Table.Column<MessageRow>
              dataIndex="received_at"
              title="Received"
              render={(value: string) => (
                <Typography.Text style={{ whiteSpace: "nowrap" }}>
                  {new Date(value).toLocaleString()}
                </Typography.Text>
              )}
            />
            <Table.Column dataIndex="source_addr" title="From" />
            <Table.Column dataIndex="dest_addr" title="To" />
            <Table.Column
              dataIndex="user_id"
              title="Partner"
              render={(value?: string) => value || <Typography.Text type="secondary">—</Typography.Text>}
            />
            <Table.Column dataIndex="connector_id" title="Connector" />
            <Table.Column<MessageRow>
              dataIndex="verdict_stat"
              title="Verdict"
              render={(value?: string) => <StatusBadge tone={verdictTone(value)}>{value || "—"}</StatusBadge>}
            />
            <Table.Column<MessageRow>
              dataIndex="delivery_state"
              title="Delivery"
              render={(value: string | undefined, row) => (
                <Space size={4}>
                  <StatusBadge tone={deliveryTone(value)}>{value || "—"}</StatusBadge>
                  {row.delivery_attempts > 0 && <Tag>{row.delivery_attempts} attempts</Tag>}
                </Space>
              )}
            />
            <Table.Column<MessageRow>
              dataIndex="parts"
              title="Parts"
              render={(value: number | undefined, row) => (
                <Space size={4}>
                  <span>{value ?? 1}</span>
                  {row.encoding && <Tag>{row.encoding}</Tag>}
                </Space>
              )}
            />
            <Table.Column<MessageRow>
              title="Content"
              fixed="right"
              render={(_, row) => (
                <Button
                  size="small"
                  icon={<EyeOutlined />}
                  loading={revealing}
                  onClick={() => void reveal(row)}
                >
                  Reveal
                </Button>
              )}
            />
          </Table>

          {cursor && (
            <div style={{ marginTop: 16, textAlign: "center" }}>
              <Button loading={loading} onClick={() => void load(cursor, filters, true)}>
                Load more
              </Button>
            </div>
          )}
        </>
      )}

      {/* A Drawer, not a Modal. This console shows information in a right-hand
          panel so the table behind it stays readable and the operator keeps
          their place in the list — revealing a message is usually one step in
          scanning several, not a detour out of the page. */}
      <Drawer
        open={Boolean(revealed)}
        onClose={() => setRevealed(undefined)}
        width={560}
        title="Message content"
        destroyOnClose
      >
        {revealed && (
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label="Message ID">
              <Typography.Text copyable>{revealed.message_id}</Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label="From → To">
              {revealed.source_addr} → {revealed.dest_addr}
            </Descriptions.Item>
            <Descriptions.Item label="Received">
              {new Date(revealed.received_at).toLocaleString()}
            </Descriptions.Item>
            <Descriptions.Item label="Encoding">
              {revealed.encoding || "—"} · {revealed.parts ?? 1} part(s)
            </Descriptions.Item>
            <Descriptions.Item label="Text">
              <Typography.Paragraph copyable style={{ marginBottom: 0, whiteSpace: "pre-wrap" }}>
                {revealed.text || "(empty)"}
              </Typography.Paragraph>
            </Descriptions.Item>
            {revealed.raw_hex && (
              <Descriptions.Item label="Raw bytes">
                <Typography.Text code style={{ wordBreak: "break-all" }}>
                  {revealed.raw_hex}
                </Typography.Text>
              </Descriptions.Item>
            )}
          </Descriptions>
        )}
      </Drawer>
    </>
  );
};
