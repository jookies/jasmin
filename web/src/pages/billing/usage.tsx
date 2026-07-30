import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  App,
  Button,
  Card,
  DatePicker,
  Descriptions,
  Drawer,
  Form,
  Input,
  Select,
  Space,
  Table,
  Timeline,
  Typography,
} from "antd";
import { DownloadOutlined, SearchOutlined } from "@ant-design/icons";
import dayjs, { type Dayjs } from "dayjs";
import { useSearchParams } from "react-router-dom";

import { PageTitle, StatusBadge } from "../../components/OperatorUI";
import { API_URL, httpClient } from "../../httpClient";

type CDRRecord = {
  id: string;
  message_id: string;
  part_number: number;
  part_count: number;
  user_id: string;
  group_id?: string;
  route_id: string;
  connector_id: string;
  ingress?: string;
  bill_id?: string;
  rate: number;
  currency: string;
  early_amount: number;
  late_amount: number;
  actual_late_amount: number;
  charged_total: number;
  billing_mode: string;
  billing_outcome: string;
  state: string;
  smpp_status?: string;
  smsc_message_id?: string;
  delivery_state?: string;
  delivery_status?: string;
  delivery_error?: string;
  admitted_at: string;
  updated_at: string;
  terminal_at?: string;
  delivery_done_at?: string;
  delivery_received_at?: string;
  late_billing_at?: string;
};

type CDREvent = {
  key: string;
  kind: string;
  state: string;
  attempt_id?: number;
  smpp_status?: string;
  smsc_message_id?: string;
  delivery_state?: string;
  delivery_status?: string;
  delivery_error?: string;
  billing_outcome?: string;
  actual_late_amount?: number;
  occurred_at: string;
};

type Filters = {
  user?: string;
  message?: string;
  window?: [Dayjs, Dayjs];
  state?: string;
  connector?: string;
};

const stateTone = (state: string) => {
  switch (state) {
    case "SMSC_ACCEPTED":
      return "positive" as const;
    case "SMSC_REJECTED":
    case "TERMINAL_TIMEOUT":
      return "negative" as const;
    case "UNKNOWN_AFTER_SEND":
    case "RETRY_PENDING":
      return "warning" as const;
    default:
      return "progress" as const;
  }
};

const deliveryTone = (state?: string) => {
  switch (state) {
    case "DELIVERED":
      return "positive" as const;
    case "":
    case undefined:
      return "progress" as const;
    default:
      return "negative" as const;
  }
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

export const BillingUsagePage = () => {
  const { message } = App.useApp();
  const [form] = Form.useForm<Filters>();
  // Arriving from a customer's billing account pre-filters to that customer,
  // so the link means what it says rather than dropping you into every record.
  const [searchParams] = useSearchParams();
  const initialUser = searchParams.get("user") ?? undefined;
  const [filters, setFilters] = useState<Filters>({ user: initialUser });
  const [records, setRecords] = useState<CDRRecord[]>([]);
  const [cursors, setCursors] = useState<string[]>([""]);
  const [nextCursor, setNextCursor] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<CDRRecord | null>(null);
  const [events, setEvents] = useState<CDREvent[]>([]);
  const [eventsError, setEventsError] = useState("");

  const serverQuery = useCallback(
    (cursor: string) => {
      const params = new URLSearchParams();
      if (filters.user) params.set("user", filters.user);
      if (filters.message) params.set("message", filters.message.trim());
      if (filters.window) {
        params.set("from", filters.window[0].toISOString());
        params.set("to", filters.window[1].toISOString());
      }
      if (cursor) params.set("cursor", cursor);
      params.set("limit", "200");
      return params.toString();
    },
    [filters],
  );

  const load = useCallback(
    async (cursor: string) => {
      setLoading(true);
      setError("");
      try {
        const response = await httpClient.get(`${API_URL}/billing/cdrs?${serverQuery(cursor)}`);
        setRecords(response.data.records ?? []);
        setNextCursor(response.data.next_cursor ?? "");
      } catch (requestError) {
        setRecords([]);
        setNextCursor("");
        setError(apiErrorMessage(requestError, "Could not read commercial records"));
      } finally {
        setLoading(false);
      }
    },
    [serverQuery],
  );

  useEffect(() => {
    void load("");
    setCursors([""]);
  }, [load]);

  // State and connector narrow the page already fetched. The durable query
  // filters on customer and time only, so pretending otherwise would show an
  // operator "no results" for a window that has plenty.
  const visible = useMemo(
    () =>
      records.filter(
        (record) =>
          (!filters.state || record.state === filters.state) &&
          (!filters.connector || record.connector_id === filters.connector),
      ),
    [records, filters.state, filters.connector],
  );

  const connectors = useMemo(
    () => Array.from(new Set(records.map((record) => record.connector_id))).sort(),
    [records],
  );

  const openDetail = async (record: CDRRecord) => {
    setSelected(record);
    setEvents([]);
    setEventsError("");
    try {
      const response = await httpClient.get(
        `${API_URL}/billing/cdrs/${encodeURIComponent(record.id)}/events`,
      );
      setEvents(response.data ?? []);
    } catch (requestError) {
      setEventsError(apiErrorMessage(requestError, "Could not read the event history"));
    }
  };

  const download = async (format: "csv" | "jsonl") => {
    try {
      const response = await httpClient.get(
        `${API_URL}/billing/export?${serverQuery("")}&format=${format}`,
        { responseType: "blob" },
      );
      const url = URL.createObjectURL(response.data as Blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `cdr-export.${format}`;
      link.click();
      URL.revokeObjectURL(url);
      const count = response.headers["x-cdr-record-count"];
      const more = response.headers["x-cdr-next-cursor"];
      message.success(
        more
          ? `Exported ${count} records — this window has more, the file is one page`
          : `Exported ${count} records`,
      );
    } catch (requestError) {
      message.error(apiErrorMessage(requestError, "Export failed"));
    }
  };

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <PageTitle
        eyebrow="Billing"
        title="Usage"
        description="Every rated message part the gateway admitted: what it was charged, which route and connector carried it, and how it ended."
      />

      <Alert
        type="info"
        showIcon
        message="Records are content-free by design"
        description="A CDR carries routing and billing metadata only — no destination number, sender or message text is stored. Investigate by customer, gateway message ID, time window, or SMSC message ID."
      />

      <Card title="Find records">
        <Form
          form={form}
          layout="vertical"
          initialValues={{ user: initialUser }}
          onFinish={(values: Filters) => {
            setFilters(values);
            setCursors([""]);
          }}
        >
          <div className="billing-filter-grid">
            <Form.Item name="user" label="Customer">
              <Input allowClear placeholder="username" />
            </Form.Item>
            <Form.Item name="message" label="Gateway message ID">
              <Input allowClear placeholder="the ID returned to the customer" />
            </Form.Item>
            <Form.Item name="window" label="Admitted between" className="billing-filter-wide">
              <DatePicker.RangePicker showTime style={{ width: "100%" }} />
            </Form.Item>
            <Form.Item name="state" label="Submission state">
              <Select
                allowClear
                placeholder="any"
                options={[
                  "ADMITTED",
                  "RETRY_PENDING",
                  "UNKNOWN_AFTER_SEND",
                  "SMSC_ACCEPTED",
                  "SMSC_REJECTED",
                  "TERMINAL_TIMEOUT",
                ].map((value) => ({ value, label: value }))}
              />
            </Form.Item>
            <Form.Item name="connector" label="Connector">
              <Select
                allowClear
                placeholder="any"
                options={connectors.map((value) => ({ value, label: value }))}
              />
            </Form.Item>
          </div>
          <Space wrap>
            <Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={loading}>
              Search
            </Button>
            <Button icon={<DownloadOutlined />} onClick={() => void download("csv")}>
              Export CSV
            </Button>
            <Button icon={<DownloadOutlined />} onClick={() => void download("jsonl")}>
              Export JSONL
            </Button>
          </Space>
        </Form>
        <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
          Customer, message ID and time window are applied by the database — a
          message ID returns every part of that message, which is how to answer
          "what happened to the ID I gave the customer?". Submission state and
          connector narrow the loaded page only; page through the window to see
          the rest.
        </Typography.Paragraph>
      </Card>

      {error && <Alert type="error" showIcon message={error} />}

      <Card
        title={`Records (${visible.length}${visible.length !== records.length ? ` of ${records.length} loaded` : ""})`}
      >
        <Table
          rowKey="id"
          dataSource={visible}
          loading={loading}
          scroll={{ x: true }}
          pagination={false}
          onRow={(record) => ({ onClick: () => void openDetail(record) })}
          columns={[
            {
              title: "Admitted",
              dataIndex: "admitted_at",
              render: (value: string) => dayjs(value).format("YYYY-MM-DD HH:mm:ss"),
            },
            { title: "Customer", dataIndex: "user_id" },
            {
              title: "Message",
              dataIndex: "message_id",
              render: (value: string, record: CDRRecord) => (
                <Space direction="vertical" size={0}>
                  <Typography.Text code>{value}</Typography.Text>
                  {record.part_count > 1 && (
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                      part {record.part_number} of {record.part_count}
                    </Typography.Text>
                  )}
                </Space>
              ),
            },
            { title: "Connector", dataIndex: "connector_id" },
            {
              title: "Submission",
              dataIndex: "state",
              render: (value: string) => <StatusBadge tone={stateTone(value)}>{value}</StatusBadge>,
            },
            {
              title: "Delivery",
              dataIndex: "delivery_state",
              render: (value: string | undefined) => (
                <StatusBadge tone={deliveryTone(value)}>{value || "pending"}</StatusBadge>
              ),
            },
            {
              title: "Charged",
              dataIndex: "charged_total",
              align: "right",
              render: (value: number, record: CDRRecord) => (
                <Space direction="vertical" size={0} style={{ textAlign: "right" }}>
                  <Typography.Text strong>
                    {value} {record.currency}
                  </Typography.Text>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {record.early_amount} early + {record.actual_late_amount} late
                  </Typography.Text>
                </Space>
              ),
            },
            { title: "Billing", dataIndex: "billing_outcome" },
          ]}
        />
        <Space style={{ marginTop: 12 }}>
          <Button
            disabled={cursors.length < 2 || loading}
            onClick={() => {
              const previous = cursors.slice(0, -1);
              setCursors(previous);
              void load(previous[previous.length - 1] ?? "");
            }}
          >
            Previous page
          </Button>
          <Button
            disabled={!nextCursor || loading}
            onClick={() => {
              setCursors([...cursors, nextCursor]);
              void load(nextCursor);
            }}
          >
            Next page
          </Button>
        </Space>
      </Card>

      <Drawer
        open={selected !== null}
        onClose={() => setSelected(null)}
        width={640}
        title={selected ? `${selected.message_id} · part ${selected.part_number}` : ""}
      >
        {selected && (
          <Space direction="vertical" size={16} style={{ width: "100%" }}>
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label="Record ID">
                <Typography.Text code>{selected.id}</Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label="Customer">{selected.user_id}</Descriptions.Item>
              <Descriptions.Item label="Route">{selected.route_id}</Descriptions.Item>
              <Descriptions.Item label="Connector">{selected.connector_id}</Descriptions.Item>
              <Descriptions.Item label="Ingress">{selected.ingress || "—"}</Descriptions.Item>
              <Descriptions.Item label="SMSC message ID">
                {selected.smsc_message_id || "—"}
              </Descriptions.Item>
              <Descriptions.Item label="Rate">
                {selected.rate} {selected.currency} ({selected.billing_mode})
              </Descriptions.Item>
              <Descriptions.Item label="Charged">
                {selected.charged_total} {selected.currency} — {selected.early_amount} at submit,{" "}
                {selected.actual_late_amount} after acceptance
              </Descriptions.Item>
              <Descriptions.Item label="Quoted late">
                {selected.late_amount} ({selected.billing_outcome})
              </Descriptions.Item>
              <Descriptions.Item label="Delivery">
                {selected.delivery_state || "pending"}
                {selected.delivery_status ? ` · ${selected.delivery_status}` : ""}
                {selected.delivery_error ? ` · error ${selected.delivery_error}` : ""}
              </Descriptions.Item>
            </Descriptions>

            {eventsError && <Alert type="error" showIcon message={eventsError} />}
            <Card size="small" title="Event history">
              <Timeline
                items={events.map((event) => ({
                  color:
                    event.kind === "SMSC_REJECTED" || event.kind === "LATE_BILLING_REJECTED"
                      ? "red"
                      : event.kind === "FINAL_DLR" || event.kind === "SMSC_ACCEPTED"
                        ? "green"
                        : "blue",
                  children: (
                    <Space direction="vertical" size={0}>
                      <Typography.Text strong>{event.kind}</Typography.Text>
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                        {dayjs(event.occurred_at).format("YYYY-MM-DD HH:mm:ss.SSS")}
                        {event.attempt_id ? ` · attempt ${event.attempt_id}` : ""}
                        {event.smpp_status ? ` · ${event.smpp_status}` : ""}
                        {event.delivery_status ? ` · ${event.delivery_status}` : ""}
                        {event.billing_outcome ? ` · billing ${event.billing_outcome}` : ""}
                      </Typography.Text>
                    </Space>
                  ),
                }))}
              />
              {events.length === 0 && !eventsError && (
                <Typography.Text type="secondary">No events recorded.</Typography.Text>
              )}
            </Card>
          </Space>
        )}
      </Drawer>
    </Space>
  );
};
