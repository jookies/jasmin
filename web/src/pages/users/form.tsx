import type { FormProps } from "antd";
import { Form, Input, InputNumber } from "antd";

// UserFields is the shared create/edit form body. The password is write-only:
// entered plaintext, hashed server-side; empty on edit keeps the stored hash.
// Empty balance / submit count mean unlimited (legacy "ND").
export const UserFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical">
    <Form.Item label="Username" name="username" rules={[{ required: true }]}>
      <Input disabled={editing} />
    </Form.Item>
    <Form.Item
      label="Password"
      name="password"
      rules={editing ? [] : [{ required: true }]}
      tooltip="Hashed server-side; never stored or shown in plaintext"
    >
      <Input.Password placeholder={editing ? "unchanged" : ""} autoComplete="new-password" />
    </Form.Item>
    <Form.Item
      label="External ID"
      name="external_id"
      tooltip="Legacy user id (1–16 chars, [A-Za-z0-9_-]); defaults to the username"
    >
      <Input placeholder="defaults to username" />
    </Form.Item>
    <Form.Item label="Balance" name="balance" tooltip="Empty = unlimited (no balance tracking)">
      <InputNumber min={0} step={0.01} style={{ width: "100%" }} placeholder="unlimited" />
    </Form.Item>
    <Form.Item
      label="Submit SM quota"
      name="submit_sm_count"
      tooltip="Remaining message credits; empty = unlimited"
    >
      <InputNumber min={0} style={{ width: "100%" }} placeholder="unlimited" />
    </Form.Item>
    <Form.Item
      label="Early decrement balance %"
      name="early_decrement_balance_percent"
      tooltip="Percentage charged at submit time (rest on DLR); empty = disabled"
    >
      <InputNumber min={0} max={100} style={{ width: "100%" }} placeholder="disabled" />
    </Form.Item>
  </Form>
);
