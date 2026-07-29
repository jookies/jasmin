import { useState } from "react";
import { useCustom } from "@refinedev/core";
import {
  App,
  Button,
  Descriptions,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Table,
} from "antd";
import {
  DollarOutlined,
  ReloadOutlined,
  SearchOutlined,
  SendOutlined,
} from "@ant-design/icons";

import { PageTitle, StatusBadge } from "../components/OperatorUI";
import { API_URL, httpClient } from "../httpClient";

type StatsPayload = {
  started_at?: string;
  http: Record<string, number>;
  smpps: Record<string, number>;
  smppc: Array<{ cid: string; counters: Record<string, number> }>;
};

type MessageStatus = {
  message_id?: string;
  total_parts?: number;
  pending?: number;
  attempting?: number;
  unknown_after_send?: number;
  result_committed?: number;
  state?: string;
};

const count = (counters: Record<string, number> | undefined, name: string) => counters?.[name] ?? 0;

const apiErrorMessage = (error: unknown, fallback: string) =>
  typeof error === "object" &&
  error !== null &&
  "response" in error &&
  typeof error.response === "object" &&
  error.response !== null &&
  "data" in error.response
    ? String((error.response.data as { message?: string }).message ?? fallback)
    : fallback;

type AccountToolValues = {
  username: string;
  destination?: string;
};

type AccountResult = {
  kind: "balance" | "rate";
  username: string;
  destination?: string;
  balance?: string | null;
  unit_rate?: number;
  submit_sm_count?: string | number | null;
};

type SendToolValues = {
  username: string;
  password: string;
  destination: string;
  content: string;
  from?: string;
  coding?: number;
  dlr?: boolean;
  dlr_url?: string;
};

export const OperationsPage = () => {
  const { message } = App.useApp();
  const { data, isFetching, refetch } = useCustom<StatsPayload>({
    url: `${API_URL}/stats`,
    method: "get",
    queryOptions: { refetchInterval: 5000 },
  });
  const stats = data?.data;
  const [messageID, setMessageID] = useState("");
  const [messageStatus, setMessageStatus] = useState<MessageStatus | null>(null);
  const [messageError, setMessageError] = useState("");
  const [lookingUp, setLookingUp] = useState(false);
  const [accountForm] = Form.useForm<AccountToolValues>();
  const [accountResult, setAccountResult] = useState<AccountResult | null>(null);
  const [checkingAccount, setCheckingAccount] = useState<"balance" | "rate" | null>(null);
  const [sendForm] = Form.useForm<SendToolValues>();
  const [sending, setSending] = useState(false);
  const [sentMessageID, setSentMessageID] = useState("");

  const lookupMessage = async (requestedID?: string) => {
    const id = (requestedID ?? messageID).trim();
    if (!id) return;
    setLookingUp(true);
    setMessageError("");
    setMessageStatus(null);
    try {
      const response = await httpClient.get(`${API_URL}/message-status/${encodeURIComponent(id)}`);
      setMessageStatus(response.data);
    } catch (error) {
      setMessageError(apiErrorMessage(error, "Message lookup failed"));
    } finally {
      setLookingUp(false);
    }
  };

  const state = messageStatus?.state;
  const statusValue = (field: keyof MessageStatus) => Number(messageStatus?.[field] ?? 0);

  const checkBalance = async () => {
    const values = await accountForm.validateFields(["username"]);
    setCheckingAccount("balance");
    try {
      const response = await httpClient.post(`${API_URL}/tools/balance`, {
        username: values.username,
      });
      setAccountResult({ kind: "balance", ...response.data });
    } catch (error) {
      void message.error(apiErrorMessage(error, "Balance lookup failed"));
    } finally {
      setCheckingAccount(null);
    }
  };

  const checkRate = async () => {
    const values = await accountForm.validateFields(["username", "destination"]);
    setCheckingAccount("rate");
    try {
      const response = await httpClient.post(`${API_URL}/tools/rate`, values);
      setAccountResult({ kind: "rate", ...response.data });
    } catch (error) {
      void message.error(apiErrorMessage(error, "Rate lookup failed"));
    } finally {
      setCheckingAccount(null);
    }
  };

  const confirmSend = (values: SendToolValues) => {
    Modal.confirm({
      title: `Send a real message to ${values.destination}?`,
      content:
        "This uses the live routing and connector pipeline. It can consume account quota and carrier credit.",
      okText: "Send message",
      async onOk() {
        setSending(true);
        try {
          const response = await httpClient.post(`${API_URL}/tools/send`, values);
          const messageID = String(response.data?.message_id ?? "");
          setSentMessageID(messageID);
          sendForm.setFieldValue("password", "");
          void message.success(`Message submitted${messageID ? ` · ${messageID}` : ""}`);
        } catch (error) {
          void message.error(apiErrorMessage(error, "Message submission failed"));
          throw error;
        } finally {
          setSending(false);
        }
      },
    });
  };

  return (
    <div className="page-container">
      <section className="dashboard-hero">
        <PageTitle
          eyebrow="Live operations"
          title="Traffic and sessions"
          description="Watch process-local HTTP and SMPP counters, then inspect the aggregate state of a known gateway message."
        />
        <Button icon={<ReloadOutlined />} loading={isFetching} onClick={() => void refetch()}>
          Refresh now
        </Button>
      </section>

      <section className="operations-metrics">
        <article className="metric-card">
          <div className="metric-card-header"><span>HTTP requests</span></div>
          <div className="metric-value">{count(stats?.http, "request_count")}</div>
          <div className="metric-caption">{count(stats?.http, "success_count")} successful</div>
        </article>
        <article className="metric-card">
          <div className="metric-card-header"><span>HTTP failures</span></div>
          <div className="metric-value">
            {count(stats?.http, "auth_error_count") +
              count(stats?.http, "route_error_count") +
              count(stats?.http, "charging_error_count") +
              count(stats?.http, "server_error_count")}
          </div>
          <div className="metric-caption">Auth, routing, charging and server errors</div>
        </article>
        <article className="metric-card">
          <div className="metric-card-header"><span>SMPP sessions</span></div>
          <div className="metric-value">{count(stats?.smpps, "connected_count")}</div>
          <div className="metric-caption">
            {count(stats?.smpps, "bound_trx_count") +
              count(stats?.smpps, "bound_rx_count") +
              count(stats?.smpps, "bound_tx_count")}{" "}
            currently bound
          </div>
        </article>
        <article className="metric-card">
          <div className="metric-card-header"><span>Counter window</span></div>
          <div className="metric-value metric-time">
            {stats?.started_at
              ? new Date(stats.started_at).toLocaleDateString([], { month: "short", day: "numeric" })
              : "—"}
          </div>
          <div className="metric-caption">
            Since {stats?.started_at ? new Date(stats.started_at).toLocaleTimeString() : "gateway start"}
          </div>
        </article>
      </section>

      <section className="dashboard-panel operations-panel">
        <div className="panel-heading">
          <div>
            <h2>Connector counters</h2>
            <p>Live process counters reset when the gateway restarts.</p>
          </div>
        </div>
        <Table
          dataSource={stats?.smppc ?? []}
          rowKey="cid"
          size="small"
          pagination={false}
          scroll={{ x: 900 }}
        >
          <Table.Column dataIndex="cid" title="Connector" />
          <Table.Column
            title="Connections"
            render={(_, row: StatsPayload["smppc"][number]) =>
              `${count(row.counters, "connected_count")} / ${count(row.counters, "disconnected_count")}`}
          />
          <Table.Column
            title="Binds"
            render={(_, row: StatsPayload["smppc"][number]) => count(row.counters, "bound_count")}
          />
          <Table.Column
            title="Submits"
            render={(_, row: StatsPayload["smppc"][number]) =>
              `${count(row.counters, "submit_sm_count")} / ${count(row.counters, "submit_sm_request_count")}`}
          />
          <Table.Column
            title="Deliveries"
            render={(_, row: StatsPayload["smppc"][number]) => count(row.counters, "deliver_sm_count")}
          />
          <Table.Column
            title="Throttle errors"
            render={(_, row: StatsPayload["smppc"][number]) => count(row.counters, "throttling_error_count")}
          />
          <Table.Column
            title="Other errors"
            render={(_, row: StatsPayload["smppc"][number]) => count(row.counters, "other_submit_error_count")}
          />
        </Table>
      </section>

      <section className="operations-tools">
        <article className="dashboard-panel operations-panel">
          <div className="panel-heading">
            <div>
              <h2>Account diagnostics</h2>
              <p>Read the effective live balance or quote the current default route rate.</p>
            </div>
          </div>
          <Form form={accountForm} layout="vertical">
            <Form.Item label="Gateway username" name="username" rules={[{ required: true }]}>
              <Input placeholder="customer-a" />
            </Form.Item>
            <Form.Item
              label="Destination"
              name="destination"
              tooltip="Required for a rate quote"
            >
              <Input placeholder="+12025550123" />
            </Form.Item>
            <Space wrap>
              <Button
                icon={<DollarOutlined />}
                loading={checkingAccount === "balance"}
                onClick={() => void checkBalance()}
              >
                Check balance
              </Button>
              <Button
                type="primary"
                loading={checkingAccount === "rate"}
                onClick={() => void checkRate()}
              >
                Quote rate
              </Button>
            </Space>
          </Form>
          {accountResult && (
            <Descriptions bordered size="small" column={1} className="tool-result">
              <Descriptions.Item label="User">{accountResult.username}</Descriptions.Item>
              {accountResult.kind === "balance" ? (
                <>
                  <Descriptions.Item label="Balance">
                    {accountResult.balance ?? "Unlimited"}
                  </Descriptions.Item>
                  <Descriptions.Item label="Submit quota">
                    {accountResult.submit_sm_count ?? "Unlimited"}
                  </Descriptions.Item>
                </>
              ) : (
                <>
                  <Descriptions.Item label="Destination">
                    {accountResult.destination}
                  </Descriptions.Item>
                  <Descriptions.Item label="Unit rate">
                    {accountResult.unit_rate ?? "—"}
                  </Descriptions.Item>
                  <Descriptions.Item label="Submit count">
                    {accountResult.submit_sm_count ?? "—"}
                  </Descriptions.Item>
                </>
              )}
            </Descriptions>
          )}
        </article>

        <article className="dashboard-panel operations-panel">
          <div className="panel-heading">
            <div>
              <h2>Send a test message</h2>
              <p>Exercise the real authentication, routing, billing and SMPP submit path.</p>
            </div>
          </div>
          <Form
            form={sendForm}
            layout="vertical"
            initialValues={{ coding: 0, dlr: false }}
            onFinish={confirmSend}
          >
            <div className="form-grid-two">
              <Form.Item label="Username" name="username" rules={[{ required: true }]}>
                <Input autoComplete="username" />
              </Form.Item>
              <Form.Item label="Password" name="password" rules={[{ required: true }]}>
                <Input.Password autoComplete="current-password" />
              </Form.Item>
            </div>
            <div className="form-grid-two">
              <Form.Item label="Destination" name="destination" rules={[{ required: true }]}>
                <Input placeholder="+12025550123" />
              </Form.Item>
              <Form.Item label="Source address" name="from">
                <Input placeholder="Use user default" />
              </Form.Item>
            </div>
            <Form.Item label="Message" name="content" rules={[{ required: true }]}>
              <Input.TextArea rows={4} maxLength={1600} showCount />
            </Form.Item>
            <div className="form-grid-two">
              <Form.Item label="Data coding" name="coding">
                <Select
                  options={[
                    { value: 0, label: "0 · GSM 7-bit / default" },
                    { value: 8, label: "8 · UCS-2" },
                  ]}
                />
              </Form.Item>
              <Form.Item label="Request delivery receipt" name="dlr" valuePropName="checked">
                <Switch />
              </Form.Item>
              <Form.Item
                noStyle
                shouldUpdate={(prev, next) => prev.dlr !== next.dlr}
              >
                {({ getFieldValue }) =>
                  getFieldValue("dlr") ? (
                    <Form.Item
                      label="Receipt callback URL"
                      name="dlr_url"
                      rules={[{ required: true, message: "A receipt needs somewhere to be delivered" }]}
                      extra="The gateway POSTs the receipt here. Without it nothing is registered and no receipt can arrive."
                    >
                      <Input placeholder="https://example.test/dlr" />
                    </Form.Item>
                  ) : null
                }
              </Form.Item>
            </div>
            <Button type="primary" htmlType="submit" icon={<SendOutlined />} loading={sending}>
              Review and send
            </Button>
          </Form>
          {sentMessageID && (
            <div className="send-result">
              <span>Last submitted message</span>
              <strong>{sentMessageID}</strong>
              <Button
                type="link"
                onClick={() => {
                  setMessageID(sentMessageID);
                  void lookupMessage(sentMessageID);
                }}
              >
                Check status
              </Button>
            </div>
          )}
        </article>
      </section>

      <section className="dashboard-panel operations-panel">
        <div className="panel-heading">
          <div>
            <h2>Message status</h2>
            <p>Look up a known gateway message ID. This reports its aggregate durable submit state.</p>
          </div>
        </div>
        <Form layout="vertical" onFinish={() => void lookupMessage()}>
          <Form.Item label="Gateway message ID" validateStatus={messageError ? "error" : undefined} help={messageError}>
            <Space.Compact style={{ width: "100%" }}>
              <Input
                value={messageID}
                onChange={(event) => setMessageID(event.target.value)}
                placeholder="Paste an exact message ID"
              />
              <Button
                type="primary"
                htmlType="submit"
                icon={<SearchOutlined />}
                loading={lookingUp}
                disabled={!messageID.trim()}
              >
                Look up
              </Button>
            </Space.Compact>
          </Form.Item>
        </Form>
        {messageStatus && (
          <Descriptions bordered size="small" column={{ xs: 1, sm: 2, lg: 3 }}>
            <Descriptions.Item label="State">
              <StatusBadge tone={state === "RESULT_COMMITTED" ? "positive" : state === "UNKNOWN_AFTER_SEND" ? "warning" : "progress"}>
                {state ?? "Unknown"}
              </StatusBadge>
            </Descriptions.Item>
            <Descriptions.Item label="Parts">{statusValue("total_parts")}</Descriptions.Item>
            <Descriptions.Item label="Committed">{statusValue("result_committed")}</Descriptions.Item>
            <Descriptions.Item label="Pending">{statusValue("pending")}</Descriptions.Item>
            <Descriptions.Item label="Attempting">{statusValue("attempting")}</Descriptions.Item>
            <Descriptions.Item label="Unknown after send">
              {statusValue("unknown_after_send")}
            </Descriptions.Item>
          </Descriptions>
        )}
      </section>
    </div>
  );
};
