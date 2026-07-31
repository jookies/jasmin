import { useState } from "react";
import type { FormProps } from "antd";
import {
  Alert,
  Button,
  Collapse,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
  Typography,
} from "antd";

import { EffectiveValue, FormIntroduction, StatusBadge } from "../../components/OperatorUI";

// Wire contract: internal/core/termination/config.go's ConnectorConfig.
//
// Duration fields (cache_ttl, lookup_timeout, timeout, backoff, backoff_cap,
// receipt_delay, receipt_jitter) are Go time.Duration on the wire. This is the
// first admin-console field backed by a real time.Duration: every existing
// "timeout"-shaped field in this console (e.g. connectors' trx_to, elink_interval)
// is a plain float64 of seconds on the Go side, not a Duration. ConnectorConfig
// has no custom (Un)MarshalJSON, so encoding/json's default applies: a Duration
// marshals as an int64 count of *nanoseconds*. The getValueProps/normalize pairs
// below are the seconds<->nanoseconds boundary so an operator only ever types
// or reads seconds; the antd Form store (and therefore the request body) holds
// nanoseconds throughout.
const NS_PER_SECOND = 1_000_000_000;

const secondsFromNs = (value: unknown): number | undefined =>
  typeof value === "number" ? value / NS_PER_SECOND : undefined;

const nsFromSeconds = (value: unknown): number | undefined =>
  typeof value === "number" ? Math.round(value * NS_PER_SECOND) : undefined;

// Every duration and max_attempts field below is `omitempty` on the Go struct,
// so a zero value is never sent over the wire — it is simply absent, and the
// gateway substitutes its own default (WithDefaults). Leaving these inputs
// blank is therefore the correct way to say "use the default"; the EffectiveValue
// hint states what that default resolves to rather than pre-filling a 0 that
// would read as a deliberately-chosen zero.
const fullWidth = { width: "100%" };

const verdictSourceOptions = [
  { value: "redis-window", label: "Redis activation window (production parity)" },
  { value: "static", label: "Static — always accept (tests only)" },
];

// http-gate and http-inline are intentionally not offered: they are named in
// verdict.go so the factory can tell "not implemented" apart from "typo", but
// NewVerdictSource returns ErrSourceNotImplemented for both. Offering them here
// would create a connector that cannot start.
const VerdictFields = () => (
  <>
    <Form.Item
      label="Verdict source"
      name={["verdict", "source"]}
      initialValue="redis-window"
      rules={[{ required: true }]}
      tooltip="Decides whether a message is DELIVRD or REJECTD"
    >
      <Select options={verdictSourceOptions} />
    </Form.Item>
    <Form.Item noStyle shouldUpdate>
      {({ getFieldValue }) =>
        getFieldValue(["verdict", "source"]) === "static" ? (
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
            message="Static source accepts every message"
            description="No Redis lookup is made and every message is DELIVRD. Intended for tests, not partner traffic — the key prefix, cache TTL and lookup timeout below do not apply and are hidden."
          />
        ) : (
          <>
            <Form.Item
              label="Activation key prefix"
              name={["verdict", "key_prefix"]}
              extra={<EffectiveValue value="dlr:block:" />}
              tooltip="Redis key namespace; the gateway checks <prefix><digits-only destination>"
            >
              <Input placeholder="dlr:block:" />
            </Form.Item>
            <div className="form-grid-two">
              <Form.Item
                label="Verdict cache TTL"
                name={["verdict", "cache_ttl"]}
                getValueProps={(value) => ({ value: secondsFromNs(value) })}
                normalize={(value) => nsFromSeconds(value)}
                extra={<EffectiveValue value="5 s" />}
                tooltip="How long one destination's verdict is reused. A correctness property — two messages to the same number in one window must agree — not just a cache."
              >
                <InputNumber min={0} step={0.1} style={fullWidth} placeholder="5" />
              </Form.Item>
              <Form.Item
                label="Gate lookup timeout"
                name={["verdict", "lookup_timeout"]}
                getValueProps={(value) => ({ value: secondsFromNs(value) })}
                normalize={(value) => nsFromSeconds(value)}
                extra={<EffectiveValue value="2 s" />}
              >
                <InputNumber min={0} step={0.1} style={fullWidth} placeholder="2" />
              </Form.Item>
            </div>
            <Alert
              type="warning"
              showIcon
              message="Fails open on a Redis error"
              description="If the activation gate is unreachable, this source answers DELIVRD rather than rejecting legitimate traffic on an infrastructure blip. The bypass is counted and recorded in the decision trail — this is inherited legacy behaviour, not a bug."
            />
          </>
        )
      }
    </Form.Item>
  </>
);

type SecretAction = "keep" | "replace" | "clear";

// SecretControl never binds to the antd form store: the API returns the
// delivery secret redacted as "***" (ConnectorConfig.Redacted()) and a plain
// `has_secret` boolean alongside it. An antd Form.Item pre-populated with the
// marker would risk resubmitting "***" as the secret on an untouched save —
// the backend's TerminationService.UpdateConnector actually treats an empty or
// marker-valued secret as "keep the stored one", so that specific mistake is
// harmless, but *removing* a secret needs an explicit signal the service does
// honour: the sibling clear_delivery_secret flag (see handleFinish below), not
// an empty string. Keeping "has the operator actually typed a new value" as
// plain React state, entirely outside the form, keeps the three actions —
// keep / replace / clear — unambiguous regardless of what the form store holds.
const SecretControl = ({
  configured,
  action,
  setAction,
  value,
  setValue,
}: {
  configured: boolean;
  action: SecretAction;
  setAction: (action: SecretAction) => void;
  value: string;
  setValue: (value: string) => void;
}) => {
  if (!configured) {
    return (
      <Input.Password
        placeholder="optional"
        autoComplete="new-password"
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
    );
  }
  if (action === "keep") {
    return (
      <Space>
        <StatusBadge tone="positive">Configured</StatusBadge>
        <Button size="small" onClick={() => setAction("replace")}>
          Replace secret
        </Button>
        <Button size="small" danger onClick={() => setAction("clear")}>
          Remove
        </Button>
      </Space>
    );
  }
  if (action === "clear") {
    return (
      <Space>
        <StatusBadge tone="warning">Will be removed on save</StatusBadge>
        <Button size="small" onClick={() => setAction("keep")}>
          Cancel
        </Button>
      </Space>
    );
  }
  return (
    <Space.Compact style={fullWidth}>
      <Input.Password
        placeholder="new secret"
        autoComplete="new-password"
        value={value}
        onChange={(event) => setValue(event.target.value)}
        autoFocus
      />
      <Button
        onClick={() => {
          setAction("keep");
          setValue("");
        }}
      >
        Cancel
      </Button>
    </Space.Compact>
  );
};

const deliveryFormatOptions = [
  { value: "json", label: "JSON (recommended for a new integration)" },
  { value: "legacy", label: "Legacy — form-encoded, requires an exact ACK/Jasmin body" },
];

type SecretFieldProps = {
  configured: boolean;
  action: SecretAction;
  setAction: (action: SecretAction) => void;
  value: string;
  setValue: (value: string) => void;
};

const DeliveryFields = (secretProps: SecretFieldProps) => (
  <>
    <Form.Item
      label="Delivery endpoint"
      name={["delivery", "endpoint"]}
      rules={[{ type: "url", message: "Enter an absolute http or https URL" }]}
      tooltip="Where the decoded message is POSTed. Leave empty for a pull-only deployment."
    >
      <Input placeholder="https://app.example.com/inbound (optional)" />
    </Form.Item>
    <Form.Item noStyle shouldUpdate>
      {({ getFieldValue }) =>
        getFieldValue(["delivery", "endpoint"]) ? (
          <>
            <div className="form-grid-two">
              <Form.Item label="Payload format" name={["delivery", "format"]} initialValue="json">
                <Select options={deliveryFormatOptions} />
              </Form.Item>
              <Form.Item
                label="Attempt timeout"
                name={["delivery", "timeout"]}
                getValueProps={(value) => ({ value: secondsFromNs(value) })}
                normalize={(value) => nsFromSeconds(value)}
                extra={<EffectiveValue value="10 s" />}
              >
                <InputNumber min={0} step={0.1} style={fullWidth} placeholder="10" />
              </Form.Item>
            </div>
            <Form.Item
              label="Delivery signing secret"
              tooltip="Signs every delivery POST with HMAC-SHA256 (X-Synevyr-Signature). The gateway never re-displays a stored secret."
            >
              <SecretControl {...secretProps} />
            </Form.Item>
            <div className="form-grid-three">
              <Form.Item
                label="Max attempts"
                name={["delivery", "max_attempts"]}
                extra={<EffectiveValue value="5" />}
              >
                <InputNumber min={1} style={fullWidth} placeholder="5" />
              </Form.Item>
              <Form.Item
                label="Retry backoff"
                name={["delivery", "backoff"]}
                getValueProps={(value) => ({ value: secondsFromNs(value) })}
                normalize={(value) => nsFromSeconds(value)}
                extra={<EffectiveValue value="30 s" />}
              >
                <InputNumber min={0} step={1} style={fullWidth} placeholder="30" />
              </Form.Item>
              <Form.Item
                label="Backoff cap"
                name={["delivery", "backoff_cap"]}
                getValueProps={(value) => ({ value: secondsFromNs(value) })}
                normalize={(value) => nsFromSeconds(value)}
                extra={<EffectiveValue value="900 s (15 min)" />}
              >
                <InputNumber min={0} step={1} style={fullWidth} placeholder="900" />
              </Form.Item>
            </div>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              A delivery that exhausts every attempt is dead-lettered, never dropped, and can be
              replayed once the endpoint is fixed.
            </Typography.Text>
          </>
        ) : (
          <Alert
            type="info"
            showIcon
            message="Pull-only deployment"
            description="No delivery endpoint is set: messages are decoded, spooled and receipted, but never pushed. Format, secret, timeout and retry settings only apply once an endpoint is set, so they stay hidden until then."
          />
        )
      }
    </Form.Item>
  </>
);

const ReceiptFields = () => (
  <>
    <div className="form-grid-two">
      <Form.Item
        label="Receipt delay"
        name="receipt_delay"
        getValueProps={(value) => ({ value: secondsFromNs(value) })}
        normalize={(value) => nsFromSeconds(value)}
        extra={<EffectiveValue value="5 s" />}
        tooltip="Held after the verdict is known, matching the legacy fake SMSC so an instantly-returned receipt does not look synthetic to a partner's monitoring"
      >
        <InputNumber min={0} step={0.1} style={fullWidth} placeholder="5" />
      </Form.Item>
      <Form.Item
        label="Receipt jitter"
        name="receipt_jitter"
        getValueProps={(value) => ({ value: secondsFromNs(value) })}
        normalize={(value) => nsFromSeconds(value)}
        extra={<EffectiveValue value="2 s" />}
      >
        <InputNumber min={0} step={0.1} style={fullWidth} placeholder="2" />
      </Form.Item>
    </div>
    <Form.Item
      label="Synchronous reject"
      name="synchronous_reject"
      valuePropName="checked"
      initialValue={false}
      tooltip="Off by default. When on, a rejected verdict answers submit_sm with an SMPP error immediately instead of accepting it and sending a REJECTD receipt later — a visible behaviour change some partner stacks treat as retryable."
    >
      <Switch />
    </Form.Item>
  </>
);

export const TerminationConnectorFields = ({
  formProps,
  editing,
  secretConfigured = false,
}: {
  formProps: FormProps;
  editing?: boolean;
  /** The fetched record's has_secret. Always false on create. */
  secretConfigured?: boolean;
}) => {
  const [secretAction, setSecretAction] = useState<SecretAction>("keep");
  const [secretValue, setSecretValue] = useState("");

  // delivery.secret is only ever *added* to the outgoing payload when the
  // operator typed a real replacement — it is otherwise absent, because no
  // Form.Item registers that field name, so "keep" sends nothing and the
  // stored secret survives untouched. Removal is not "send an empty secret":
  // UpdateConnector maps an empty (or redacted-marker) secret straight back to
  // the stored value, so the only way to actually remove one is the sibling
  // top-level clear_delivery_secret flag the admin API added for exactly this.
  const handleFinish = (values: Record<string, unknown>) => {
    const delivery = { ...(values.delivery as Record<string, unknown> | undefined) };
    let clearDeliverySecret = false;
    if (!secretConfigured) {
      if (secretValue) delivery.secret = secretValue;
    } else if (secretAction === "replace" && secretValue) {
      delivery.secret = secretValue;
    } else if (secretAction === "clear") {
      clearDeliverySecret = true;
    }
    const payload = { ...values, delivery, clear_delivery_secret: clearDeliverySecret };
    return formProps.onFinish?.(payload as never);
  };

  return (
    <Form {...formProps} onFinish={handleFinish} layout="vertical" className="operator-form">
      <FormIntroduction
        title={editing ? "Update termination connector" : "Create a termination connector"}
      >
        MT traffic routed here terminates on this gateway instead of an upstream SMSC: the
        message is decoded, a verdict source decides the receipt, and the content is delivered to
        your application.
      </FormIntroduction>
      <Form.Item
        label="Connector ID"
        name="cid"
        rules={[
          { required: true },
          { pattern: /^[A-Za-z0-9_-]{3,25}$/, message: "Use 3–25 letters, numbers, _ or -" },
        ]}
        tooltip={
          editing
            ? "Immutable — it names the AMQP submit queue and appears on every DLR leg"
            : "Names the AMQP submit queue this connector consumes"
        }
      >
        <Input placeholder="partner-a-term" disabled={editing} />
      </Form.Item>
      <Form.Item
        label="Started"
        name="desired_started"
        valuePropName="checked"
        initialValue={false}
        tooltip="Consume the connector's submit queue and accept traffic. A stopped connector's routes are excluded from selection, exactly like a stopped SMPP connector."
      >
        <Switch />
      </Form.Item>
      <Collapse
        className="form-collapse"
        defaultActiveKey={["verdict", "delivery", "receipt"]}
        items={[
          { key: "verdict", label: "Verdict source", children: <VerdictFields /> },
          {
            key: "delivery",
            label: "Delivery",
            children: (
              <DeliveryFields
                configured={secretConfigured}
                action={secretAction}
                setAction={setSecretAction}
                value={secretValue}
                setValue={setSecretValue}
              />
            ),
          },
          { key: "receipt", label: "Receipt timing", children: <ReceiptFields /> },
        ]}
      />
    </Form>
  );
};
