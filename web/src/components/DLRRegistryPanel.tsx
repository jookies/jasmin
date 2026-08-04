import { useCallback, useEffect, useState } from "react";
import { App, Button, Empty, Form, Input, InputNumber, Popconfirm, Space, Table, Tag } from "antd";
import { DeleteOutlined, PlusOutlined, ReloadOutlined } from "@ant-design/icons";

import { API_URL, httpClient } from "../httpClient";
import { StatusBadge, TableScrollHint } from "./OperatorUI";

const POLL_INTERVAL_MS = 5000;

type RegistryEntry = {
  msisdn: string;
  added_by?: string;
  note?: string;
  added_at?: string;
  expires_at: string;
  expires_in_seconds: number;
};

type AddValues = {
  msisdn: string;
  ttl_minutes?: number;
  note?: string;
};

const apiErrorMessage = (error: unknown, fallback: string) => {
  if (typeof error === "object" && error !== null && "response" in error) {
    const response = (error as { response?: { data?: { message?: string }; status?: number } }).response;
    if (response?.data?.message) return response.data.message;
  }
  return fallback;
};

const isNotFound = (error: unknown) =>
  typeof error === "object" &&
  error !== null &&
  "response" in error &&
  (error as { response?: { status?: number } }).response?.status === 404;

const formatRemaining = (seconds: number) => {
  if (seconds <= 0) return "expired";
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return minutes > 0 ? `${minutes}m ${rest}s` : `${rest}s`;
};

/**
 * DLRRegistryPanel shows the activation window the per-user DLR gate reads.
 *
 * The list is polled because entries expire on their own: a number that was
 * here five seconds ago may not be now, and a stale table would suggest traffic
 * will be confirmed when it will in fact be rejected.
 *
 * A gateway with no Redis behind its DLR plane answers 404, and the panel then
 * renders nothing at all rather than an empty table — matching the API, which
 * deliberately leaves no trace of a capability it cannot honour.
 */
export const DLRRegistryPanel = () => {
  const { message: toast } = App.useApp();
  const [entries, setEntries] = useState<RegistryEntry[]>([]);
  const [available, setAvailable] = useState(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [form] = Form.useForm<AddValues>();

  const load = useCallback(async () => {
    try {
      const response = await httpClient.get<RegistryEntry[]>(`${API_URL}/dlr-registry`);
      setEntries(response.data ?? []);
      setAvailable(true);
      setError(null);
    } catch (caught) {
      if (isNotFound(caught)) {
        setAvailable(false);
        return;
      }
      // Keep the last good list on screen: a failed poll is a gap in our view,
      // not evidence that the registry emptied.
      setError(apiErrorMessage(caught, "The registry could not be read"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [load]);

  const submit = async (values: AddValues) => {
    setAdding(true);
    try {
      await httpClient.post(`${API_URL}/dlr-registry`, {
        msisdn: values.msisdn,
        ttl_seconds: values.ttl_minutes ? values.ttl_minutes * 60 : undefined,
        note: values.note,
      });
      form.resetFields();
      void toast.success("Window opened");
      await load();
    } catch (caught) {
      void toast.error(apiErrorMessage(caught, "The number could not be added"));
    } finally {
      setAdding(false);
    }
  };

  const remove = async (msisdn: string) => {
    try {
      await httpClient.delete(`${API_URL}/dlr-registry/${encodeURIComponent(msisdn)}`);
      void toast.success("Window closed");
      await load();
    } catch (caught) {
      void toast.error(apiErrorMessage(caught, "The number could not be removed"));
    }
  };

  if (!available) return null;

  return (
    <section className="dashboard-panel operations-panel">
      <div className="panel-heading">
        <div>
          <h2>DLR registry</h2>
          <p>
            Destinations with an open activation window. Users with the DLR gate enabled are told the
            hit receipt for these numbers and the miss receipt for every other one. Entries expire on
            their own — at most 15 minutes — and this list covers those added through the registry API.
          </p>
        </div>
        <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load()}>
          Refresh now
        </Button>
      </div>

      <Form form={form} layout="inline" onFinish={(values) => void submit(values)} className="dlr-registry-form">
        <Form.Item
          name="msisdn"
          rules={[{ required: true, message: "A destination number is required" }]}
        >
          <Input placeholder="380930242105" allowClear style={{ minWidth: 200 }} />
        </Form.Item>
        <Form.Item name="ttl_minutes" tooltip="Minutes to keep the window open. Blank uses the 15-minute maximum.">
          <InputNumber min={1} max={15} placeholder="15" addonAfter="min" />
        </Form.Item>
        <Form.Item name="note">
          <Input placeholder="Note (optional)" allowClear style={{ minWidth: 180 }} />
        </Form.Item>
        <Form.Item>
          <Button type="primary" htmlType="submit" icon={<PlusOutlined />} loading={adding}>
            Open window
          </Button>
        </Form.Item>
      </Form>

      <TableScrollHint />
      <Table
        dataSource={entries}
        rowKey="msisdn"
        size="small"
        pagination={false}
        loading={loading && entries.length === 0}
        scroll={{ x: "max-content" }}
        locale={{
          emptyText: (
            <Empty
              description={error ?? "No destination currently has an open window."}
              image={Empty.PRESENTED_IMAGE_SIMPLE}
            />
          ),
        }}
      >
        <Table.Column<RegistryEntry> title="Destination" dataIndex="msisdn" />
        <Table.Column<RegistryEntry>
          title="Expires in"
          dataIndex="expires_in_seconds"
          render={(seconds: number) => (
            <StatusBadge tone={seconds > 60 ? "positive" : "warning"}>
              {formatRemaining(seconds)}
            </StatusBadge>
          )}
        />
        <Table.Column<RegistryEntry>
          title="Added by"
          dataIndex="added_by"
          render={(addedBy?: string) => addedBy || <Tag>unknown</Tag>}
        />
        <Table.Column<RegistryEntry>
          title="Note"
          dataIndex="note"
          render={(note?: string) => note || "—"}
        />
        <Table.Column<RegistryEntry>
          title="Actions"
          render={(_, row) => (
            <Space>
              <Popconfirm
                title="Close this window?"
                description="Traffic to this number will get the miss receipt from now on."
                okText="Close"
                onConfirm={() => void remove(row.msisdn)}
              >
                <Button size="small" danger icon={<DeleteOutlined />}>
                  Close
                </Button>
              </Popconfirm>
            </Space>
          )}
        />
      </Table>
    </section>
  );
};
