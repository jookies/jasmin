import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  DatePicker,
  Form,
  Input,
  Space,
  Statistic,
  Table,
  Typography,
} from "antd";
import { SearchOutlined } from "@ant-design/icons";
import dayjs, { type Dayjs } from "dayjs";

import { PageTitle } from "../../components/OperatorUI";
import { API_URL, httpClient } from "../../httpClient";

type UsageSummary = {
  id: string;
  user_id: string;
  currency: string;
  parts: number;
  messages: number;
  accepted: number;
  rejected: number;
  delivered: number;
  undelivered: number;
  delivery_pending: number;
  charged_early: number;
  charged_late: number;
  charged_total: number;
  quoted_late_pending: number;
  first_admitted_at: string;
  last_admitted_at: string;
};

type StatementFilters = {
  user?: string;
  window: [Dayjs, Dayjs];
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

const sum = (rows: UsageSummary[], pick: (row: UsageSummary) => number) =>
  rows.reduce((total, row) => total + pick(row), 0);

export const BillingStatementsPage = () => {
  const [form] = Form.useForm<StatementFilters>();
  const defaultWindow: [Dayjs, Dayjs] = [dayjs().startOf("month"), dayjs().add(1, "day").startOf("day")];
  const [summaries, setSummaries] = useState<UsageSummary[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [window, setWindow] = useState<[Dayjs, Dayjs]>(defaultWindow);

  const load = useCallback(async (filters: StatementFilters) => {
    setLoading(true);
    setError("");
    try {
      const params = new URLSearchParams({
        from: filters.window[0].toISOString(),
        to: filters.window[1].toISOString(),
      });
      if (filters.user) params.set("user", filters.user);
      const response = await httpClient.get(`${API_URL}/billing/summary?${params.toString()}`);
      setSummaries(response.data ?? []);
      setWindow(filters.window);
    } catch (requestError) {
      setSummaries([]);
      setError(apiErrorMessage(requestError, "Could not aggregate usage"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load({ window: defaultWindow });
    // The default window is the current month; later loads come from the form.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  // Currencies are summed separately. Today one deployment has one currency, but
  // adding two different units into a single headline would be wrong the day
  // that stops being true.
  const currencies = Array.from(new Set(summaries.map((row) => row.currency)));
  const placeholderCurrency = currencies.includes("XXX");

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <PageTitle
        eyebrow="Billing"
        title="Usage statements"
        description="Rated usage per customer over a window, aggregated from the durable commercial records."
      />

      <Card title="Window">
        <Form
          form={form}
          layout="inline"
          initialValues={{ window: defaultWindow }}
          onFinish={(values: StatementFilters) => void load(values)}
        >
          <Form.Item name="user" label="Customer">
            <Input allowClear placeholder="all customers" style={{ width: 200 }} />
          </Form.Item>
          <Form.Item
            name="window"
            label="Admitted between"
            rules={[{ required: true, message: "A statement needs a time window" }]}
          >
            <DatePicker.RangePicker showTime />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={loading}>
              Build statement
            </Button>
          </Form.Item>
        </Form>
      </Card>

      {error && <Alert type="error" showIcon message={error} />}

      {placeholderCurrency && (
        <Alert
          type="warning"
          showIcon
          message="Amounts are in XXX — the ISO 4217 code for “no currency”"
          description="Route rates are unitless until an operator sets cdr_currency in the gateway configuration. Treat these totals as rate units, not money, until then."
        />
      )}

      {currencies.map((currency) => {
        const rows = summaries.filter((row) => row.currency === currency);
        return (
          <Card key={currency} title={`Totals · ${currency}`}>
            <Space size={48} wrap>
              <Statistic title="Charged" value={sum(rows, (row) => row.charged_total)} precision={4} />
              <Statistic
                title="Unsettled (quoted, pending)"
                value={sum(rows, (row) => row.quoted_late_pending)}
                precision={4}
              />
              <Statistic title="Messages" value={sum(rows, (row) => row.messages)} />
              <Statistic title="Parts" value={sum(rows, (row) => row.parts)} />
              <Statistic title="Delivered" value={sum(rows, (row) => row.delivered)} />
            </Space>
          </Card>
        );
      })}

      <Card
        title={`Per customer · ${window[0].format("YYYY-MM-DD HH:mm")} to ${window[1].format("YYYY-MM-DD HH:mm")}`}
      >
        <Table
          rowKey="id"
          dataSource={summaries}
          loading={loading}
          scroll={{ x: true }}
          pagination={{ pageSize: 25, hideOnSinglePage: true }}
          columns={[
            { title: "Customer", dataIndex: "user_id" },
            { title: "Currency", dataIndex: "currency" },
            { title: "Messages", dataIndex: "messages", align: "right" },
            { title: "Parts", dataIndex: "parts", align: "right" },
            { title: "Accepted", dataIndex: "accepted", align: "right" },
            { title: "Rejected", dataIndex: "rejected", align: "right" },
            { title: "Delivered", dataIndex: "delivered", align: "right" },
            { title: "Undelivered", dataIndex: "undelivered", align: "right" },
            {
              title: "Delivery pending",
              dataIndex: "delivery_pending",
              align: "right",
            },
            {
              title: "Charged at submit",
              dataIndex: "charged_early",
              align: "right",
            },
            {
              title: "Charged after acceptance",
              dataIndex: "charged_late",
              align: "right",
            },
            {
              title: "Charged total",
              dataIndex: "charged_total",
              align: "right",
              render: (value: number) => <Typography.Text strong>{value}</Typography.Text>,
            },
            {
              title: "Quoted, not yet applied",
              dataIndex: "quoted_late_pending",
              align: "right",
            },
          ]}
        />
        <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
          Charged is money actually taken: the submit-time decrement plus what the
          late-billing ledger applied. Quoted-but-unapplied late money is shown
          separately — it is an intent, not revenue, and an SMSC rejection means it
          never lands. Money taken at submit is not refunded when a part is later
          rejected, so charged totals can exceed delivered counts.
        </Typography.Paragraph>
      </Card>
    </Space>
  );
};
