import type { FormProps } from "antd";
import { Alert, Collapse, Form, Input, InputNumber, Select, Switch } from "antd";
import { useSelect } from "@refinedev/antd";

import { EffectiveValue, FormIntroduction } from "../../components/OperatorUI";

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
      <Input placeholder={editing ? undefined : username || "Enter username first"} />
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
      message="Throughput values are stored for compatibility but are not enforced yet."
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
        ]}
      />
    </Form>
  );
};
