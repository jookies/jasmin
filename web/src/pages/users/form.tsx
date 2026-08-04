import type { FormProps } from "antd";
import { Alert, Collapse, Form, Input, InputNumber, Select, Space, Switch } from "antd";
import { useSelect } from "@refinedev/antd";

import { CopyButton, EffectiveValue, FormIntroduction, Snippet } from "../../components/OperatorUI";

const UsernameField = ({ editing }: { editing?: boolean }) => {
  const form = Form.useFormInstance();
  return (
    <Form.Item
      label="Username"
      name="username"
      rules={[
        { required: true },
        { pattern: /^[A-Za-z0-9_-]{1,15}$/, message: "Use 1–15 letters, numbers, _ or -" },
      ]}
    >
      <Input
        disabled={editing}
        autoComplete="username"
        onChange={(event) => {
          if (!editing && !form.isFieldTouched("external_id")) {
            form.setFieldValue("external_id", event.target.value || undefined);
          }
        }}
      />
    </Form.Item>
  );
};

const ExternalIDField = ({ editing }: { editing?: boolean }) => {
  const username = (Form.useWatch("username") as string | undefined)?.trim();
  const externalID = (Form.useWatch("external_id") as string | undefined)?.trim();
  const effectiveExternalID = externalID || username;
  return (
    <Form.Item
      label="External ID"
      name="external_id"
      tooltip="Legacy user id (1–16 chars); an empty value inherits the username"
      extra={
        <EffectiveValue
          label="Effective external ID"
          value={effectiveExternalID || "Waiting for username"}
          detail={!externalID && username ? "inherited from username" : undefined}
        />
      }
      rules={[
        {
          validator: (_, value) =>
            !value || /^[A-Za-z0-9_-]{1,16}$/.test(value)
              ? Promise.resolve()
              : Promise.reject(new Error("Use 1–16 letters, numbers, _ or -")),
        },
      ]}
    >
      <Input disabled={editing} placeholder={editing ? undefined : username || "Enter username first"} />
    </Form.Item>
  );
};

const Permission = ({
  name,
  label,
  defaultValue = true,
}: {
  name: string;
  label: string;
  defaultValue?: boolean;
}) => (
  <Form.Item label={label} name={name} valuePropName="checked" initialValue={defaultValue}>
    <Switch />
  </Form.Item>
);

const Permissions = () => (
  <div className="permission-grid">
    <Permission name="http_send" label="HTTP send" />
    <Permission name="http_balance" label="HTTP balance lookup" />
    <Permission name="http_rate" label="HTTP rate lookup" />
    <Permission name="http_bulk" label="HTTP bulk" defaultValue={false} />
    <Permission name="smpps_send" label="SMPP submit" />
    <Permission name="http_long_content" label="Long HTTP messages" />
    <Permission name="set_dlr_level" label="Set DLR level" />
    <Permission name="http_set_dlr_method" label="Set HTTP DLR method" />
    <Permission name="set_source_address" label="Set source address" />
    <Permission name="set_priority" label="Set priority" />
    <Permission name="set_validity_period" label="Set validity period" />
    <Permission name="set_hex_content" label="Send hexadecimal content" />
    <Permission name="set_schedule_delivery_time" label="Schedule delivery" />
  </div>
);

const Billing = () => (
  <>
    <div className="form-grid-two">
      <Form.Item
        label="Balance"
        name="balance"
        tooltip="Empty = unlimited"
        extra={<EffectiveValue value="Unlimited" detail="no user balance ceiling" />}
      >
        <InputNumber min={0} step={0.01} style={{ width: "100%" }} placeholder="unlimited" />
      </Form.Item>
      <Form.Item
        label="Submit SM quota"
        name="submit_sm_count"
        tooltip="Remaining message credits"
        extra={<EffectiveValue value="Unlimited" detail="no message-count ceiling" />}
      >
        <InputNumber min={0} style={{ width: "100%" }} placeholder="unlimited" />
      </Form.Item>
      <Form.Item
        label="Early decrement balance %"
        name="early_decrement_balance_percent"
        tooltip="Percentage charged at submit time; the rest is charged on delivery"
        extra={<EffectiveValue value="Disabled" detail="the full charge occurs on delivery" />}
      >
        <InputNumber min={0} max={100} style={{ width: "100%" }} placeholder="disabled" />
      </Form.Item>
    </div>
    <Alert
      type="info"
      showIcon
      message="Throughput is enforced independently at the HTTP and SMPP server front doors."
      className="form-note"
    />
    <div className="form-grid-two">
      <Form.Item label="HTTP throughput" name="http_throughput" tooltip="Messages per second">
        <InputNumber min={0} step={0.1} style={{ width: "100%" }} placeholder="not set" />
      </Form.Item>
      <Form.Item label="SMPP throughput" name="smpps_throughput" tooltip="Messages per second">
        <InputNumber min={0} step={0.1} style={{ width: "100%" }} placeholder="not set" />
      </Form.Item>
    </div>
  </>
);

const ValueFilters = () => (
  <>
    <Alert
      type="info"
      showIcon
      message="Regex matching is anchored at the beginning. Use .*TEXT when you need a substring match."
      className="form-note"
    />
    <Form.Item
      label="Destination address regex"
      name="filter_destination_address"
      extra={<EffectiveValue value=".*" detail="allow every destination" />}
    >
      <Input placeholder=".*" />
    </Form.Item>
    <Form.Item
      label="Source address regex"
      name="filter_source_address"
      extra={<EffectiveValue value=".*" detail="allow every source" />}
    >
      <Input placeholder=".*" />
    </Form.Item>
    <div className="form-grid-two">
      <Form.Item
        label="Priority regex"
        name="filter_priority"
        extra={<EffectiveValue value="^[0-3]$" />}
      >
        <Input placeholder="^[0-3]$" />
      </Form.Item>
      <Form.Item
        label="Validity-period regex"
        name="filter_validity_period"
        extra={<EffectiveValue value="^\\d+$" />}
      >
        <Input placeholder="^\\d+$" />
      </Form.Item>
    </div>
    <Form.Item
      label="Message-content regex"
      name="filter_content"
      extra={<EffectiveValue value=".*" detail="allow all content" />}
    >
      <Input placeholder=".*" />
    </Form.Item>
    <Form.Item
      label="Default source address"
      name="default_source_address"
      tooltip="Applied when the request does not provide a source address"
    >
      <Input placeholder="none" />
    </Form.Item>
  </>
);

const SMPPPolicy = () => (
  <>
    <Alert
      type="info"
      showIcon
      message="When a bind policy is configured, webadmin mirrors it into the SMPPs bind account with the same username."
      className="form-note"
    />
    <Form.Item
      label="Allow SMPP bind"
      name="smpps_bind"
      valuePropName="checked"
      initialValue={true}
    >
      <Switch />
    </Form.Item>
    <Form.Item
      label="Allowed peer IPs"
      name="smpps_ip"
      tooltip="SMPP server whitelist expression; empty allows any IPv4"
      extra={<EffectiveValue value="0.0.0.0/0" detail="any IPv4 address" />}
    >
      <Input placeholder="0.0.0.0/0" />
    </Form.Item>
    <Form.Item
      label="Maximum bindings"
      name="smpps_max_bindings"
      extra={<EffectiveValue value="Unlimited" detail="no concurrent-bind ceiling" />}
    >
      <InputNumber min={0} style={{ width: "100%" }} placeholder="unlimited" />
    </Form.Item>
  </>
);

/**
 * Terminal receipt statuses the DLR plane will publish.
 *
 * The gate only ever decides a TERMINAL receipt, so the ESME_* command-status
 * names the level-1 leg carries are deliberately not offered here: choosing one
 * would produce a receipt that is well-formed but means "the SMSC answered
 * this", which the registry has no opinion about.
 */
const RECEIPT_STATUSES = [
  { value: "DELIVRD", label: "DELIVRD — delivered to the handset" },
  { value: "ACCEPTD", label: "ACCEPTD — accepted by the network" },
  { value: "REJECTD", label: "REJECTD — rejected" },
  { value: "UNDELIV", label: "UNDELIV — undeliverable" },
  { value: "EXPIRED", label: "EXPIRED — validity period elapsed" },
  { value: "DELETED", label: "DELETED — cancelled" },
  { value: "UNKNOWN", label: "UNKNOWN — no final state known" },
];

const REGISTRY_HOST_PLACEHOLDER = "<gateway-host>";

/**
 * DLRGateGuidance is the partner's copy/paste instructions, shown once the gate
 * is switched on and a credential exists.
 *
 * The endpoint is per user and lives on the public front door, authenticated by
 * a token scoped to this one account — so unlike the operator-wide admin API it
 * is safe to hand to the partner whose traffic this gates. The key id in the
 * path is not a secret; the bearer token is, and it is shown only in the
 * response that minted it.
 */
const DLRGateGuidance = () => {
  const form = Form.useFormInstance();
  const enabled = Form.useWatch("dlr_gate_enabled", form);
  const keyID = Form.useWatch("dlr_gate_key_id", form);
  const hit = Form.useWatch("dlr_gate_hit_status", form) || "DELIVRD";
  const miss = Form.useWatch("dlr_gate_miss_status", form) || "REJECTD";

  if (!enabled) return null;

  if (!keyID) {
    return (
      <Alert
        type="info"
        style={{ marginTop: 8 }}
        message="A registry credential will be issued when you save"
        description="Saving with the gate enabled mints this user's own endpoint URL and bearer token. The token is shown once, immediately after saving — copy it then, because it cannot be retrieved afterwards."
      />
    );
  }

  const url = `https://${REGISTRY_HOST_PLACEHOLDER}/dlr-registry/${keyID}`;
  const body = `{"msisdn": "380930242105", "ttl_seconds": 900, "note": "otp login"}`;
  const curl = [
    `curl -X POST ${url} \\`,
    `  -H 'Authorization: Bearer <this-user-token>' \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -d '${body}'`,
  ].join("\n");

  return (
    <Alert
      type="info"
      style={{ marginTop: 8 }}
      message="Opening a window for a number"
      description={
        <div className="dlr-gate-guidance">
          <p>
            This user's backend calls their own endpoint when they legitimately expect traffic to a
            number. A number with an open window makes this user's terminal receipt{" "}
            <strong>{hit}</strong>; every other destination gets <strong>{miss}</strong>.
          </p>
          <Snippet command={`POST ${url}\nAuthorization: Bearer <this-user-token>\nContent-Type: application/json\n\n${body}`} />
          <Space size="small" wrap style={{ marginBottom: 8 }}>
            <CopyButton text={curl} label="Copy curl" />
            <CopyButton text={url} label="Copy URL" />
          </Space>
          <ul>
            <li>
              <code>msisdn</code> is required — digits, optionally with a leading <code>+</code>.
              Both forms are the same entry.
            </li>
            <li>
              <code>ttl_seconds</code> is optional and <strong>capped at 900 (15 minutes)</strong>;
              a larger value is clamped, not rejected. Omit it to get the full 15 minutes.
            </li>
            <li>
              <code>GET</code> the same URL to list this user's open windows;{" "}
              <code>DELETE {`${url}/380930242105`}</code> closes one early.
            </li>
            <li>
              Windows opened with this token count for <strong>this user only</strong>. Another
              gated partner cannot see, close, or benefit from them.
            </li>
            <li>
              The token is scoped to this endpoint. It cannot submit traffic, read messages, or
              administer the gateway — so it is safe to hand to the partner.
            </li>
          </ul>
          <p>
            Replace <code>{REGISTRY_HOST_PLACEHOLDER}</code> with the public address partners already
            use to reach this gateway; the endpoint is served on the same listener as{" "}
            <code>/send</code>. Lost the token? Switch on <em>Rotate token</em> below and save.
          </p>
        </div>
      }
    />
  );
};

/**
 * DLRGate is the fork-local registry gate. It is its own section rather than a
 * row under permissions because it changes what a partner is TOLD about
 * delivery, not what they are allowed to do — the copy has to say so plainly.
 */
const DLRGate = () => (
  <>
    <Alert
      type="warning"
      message="Reported receipts stop following the upstream"
      description="With the gate on, this user's terminal delivery receipts are decided by the DLR registry: destinations with an open activation window get the hit receipt, every other destination gets the miss receipt, whatever the upstream reported. Routing is unchanged and the CDR still records the real upstream status."
      style={{ marginBottom: 16 }}
    />
    <Form.Item
      label="Enable DLR registry gate"
      name="dlr_gate_enabled"
      valuePropName="checked"
      initialValue={false}
    >
      <Switch />
    </Form.Item>
    <Form.Item
      label="Receipt when the number is in the registry"
      name="dlr_gate_hit_status"
      tooltip="Terminal SMPP receipt status reported for a destination with an open window"
      extra={<EffectiveValue value="DELIVRD" detail="delivered" />}
    >
      <Select options={RECEIPT_STATUSES} placeholder="DELIVRD" allowClear />
    </Form.Item>
    <Form.Item
      label="Error code on a hit"
      name="dlr_gate_hit_error"
      tooltip="The receipt's err: field; three digits by convention"
      extra={<EffectiveValue value="000" detail="no error" />}
    >
      <Input placeholder="000" />
    </Form.Item>
    <Form.Item
      label="Receipt when the number is not in the registry"
      name="dlr_gate_miss_status"
      tooltip="Terminal SMPP receipt status reported for every other destination"
      extra={<EffectiveValue value="REJECTD" detail="rejected" />}
    >
      <Select options={RECEIPT_STATUSES} placeholder="REJECTD" allowClear />
    </Form.Item>
    <Form.Item
      label="Error code on a miss"
      name="dlr_gate_miss_error"
      tooltip="The receipt's err: field; three digits by convention"
      extra={<EffectiveValue value="008" detail="rejected" />}
    >
      <Input placeholder="008" />
    </Form.Item>
    <Form.Item name="dlr_gate_key_id" hidden>
      <Input />
    </Form.Item>
    <Form.Item
      label="Rotate token"
      name="dlr_gate_rotate_token"
      valuePropName="checked"
      tooltip="Issues a new URL and token on save. The old pair stops working immediately, so update the partner before rotating."
      extra={<EffectiveValue value="Off" detail="the existing credential is kept" />}
    >
      <Switch />
    </Form.Item>
    <DLRGateGuidance />
  </>
);

export const UserFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => {
  const { selectProps: groupSelect } = useSelect({
    resource: "groups",
    optionLabel: "gid",
    optionValue: "gid",
  });

  return (
    <Form {...formProps} layout="vertical" className="operator-form operator-form-wide">
      <FormIntroduction title={editing ? "Update gateway user" : "Provision a gateway user"}>
        Identity and credentials are at the top; billing, permissions and validation rules remain
        available without crowding the main task.
      </FormIntroduction>
      <UsernameField editing={editing} />
      <Form.Item
        label="Password"
        name="password"
        rules={editing ? [] : [{ required: true }]}
        tooltip="Hashed server-side; never stored or shown in plaintext"
      >
        <Input.Password placeholder={editing ? "unchanged" : ""} autoComplete="new-password" />
      </Form.Item>
      <ExternalIDField editing={editing} />
      <Form.Item label="Group" name="group_id" tooltip="Optional shared billing and enable/disable policy">
        <Select {...groupSelect} allowClear placeholder="No group" />
      </Form.Item>
      <Form.Item
        label="Disabled"
        name="disabled"
        valuePropName="checked"
        initialValue={false}
        tooltip="A disabled user is rejected during authentication"
      >
        <Switch />
      </Form.Item>
      <Collapse
        className="form-collapse"
        items={[
          { key: "billing", label: "Billing and quotas", children: <Billing /> },
          { key: "permissions", label: "HTTP and SMPP permissions", children: <Permissions /> },
          { key: "filters", label: "Value restrictions and default sender", children: <ValueFilters /> },
          { key: "smpps", label: "SMPP bind policy", children: <SMPPPolicy /> },
          { key: "dlrgate", label: "DLR registry gate", children: <DLRGate /> },
        ]}
      />
    </Form>
  );
};
