import type { FormProps } from "antd";
import { Form, Input, InputNumber, Switch } from "antd";
import { EffectiveValue, FormIntroduction } from "../../components/OperatorUI";

// SMPPsUserFields is the shared create/edit body for an SMPPs bind account.
// The password is write-only: never returned by the API, and left empty on edit
// it keeps the stored one. All authorizations default to true when omitted.
export const SMPPsUserFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical" className="operator-form">
    <FormIntroduction title={editing ? "Update bind account" : "Create a bind account"}>
      Define who may bind to the SMPP server, where they may connect from and which message
      attributes they can control.
    </FormIntroduction>
    <Form.Item
      label="System ID"
      name="system_id"
      rules={[{ required: true }]}
      tooltip="The ESME's bind system_id"
    >
      <Input disabled={editing} placeholder="shortcode-app" />
    </Form.Item>
    <Form.Item
      label="Password"
      name="password"
      rules={editing ? [] : [{ required: true }]}
      tooltip="Bind password; stored as-is and md5-digested for bind auth"
    >
      <Input.Password placeholder={editing ? "unchanged" : ""} autoComplete="new-password" />
    </Form.Item>
    <Form.Item
      label="Disabled"
      name="disabled"
      valuePropName="checked"
      initialValue={false}
      tooltip="A disabled account cannot bind; existing sessions are unaffected"
    >
      <Switch />
    </Form.Item>
    <Form.Item
      label="IP whitelist"
      name="ip_whitelist"
      tooltip="Regular expression matched against the peer IP; empty allows any IPv4"
      extra={<EffectiveValue value="0.0.0.0/0" detail="any IPv4 address" />}
    >
      <Input placeholder="0.0.0.0/0" />
    </Form.Item>
    <Form.Item
      label="Max bindings"
      name="max_bindings"
      tooltip="Concurrent binds allowed; empty = unlimited"
      extra={<EffectiveValue value="Unlimited" detail="no concurrent-bind ceiling" />}
    >
      <InputNumber min={0} style={{ width: "100%" }} placeholder="unlimited" />
    </Form.Item>
    <Form.Item
      label="Allow submit (smpps_send)"
      name="smpps_send"
      valuePropName="checked"
      initialValue={true}
    >
      <Switch />
    </Form.Item>
    <Form.Item
      label="May set DLR level"
      name="set_dlr_level"
      valuePropName="checked"
      initialValue={true}
    >
      <Switch />
    </Form.Item>
    <Form.Item
      label="May set source address"
      name="set_source_address"
      valuePropName="checked"
      initialValue={true}
    >
      <Switch />
    </Form.Item>
    <Form.Item
      label="May set priority"
      name="set_priority"
      valuePropName="checked"
      initialValue={true}
    >
      <Switch />
    </Form.Item>
  </Form>
);
