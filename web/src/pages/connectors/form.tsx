import type { FormProps } from "antd";
import { Form, Input, InputNumber, Select, Switch } from "antd";

const bindOptions = [
  { value: "transceiver", label: "transceiver" },
  { value: "transmitter", label: "transmitter" },
  { value: "receiver", label: "receiver" },
];

// ConnectorFields is the shared create/edit form body. `editing` locks the
// immutable connector id.
export const ConnectorFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical">
    <Form.Item label="Connector ID" name="cid" rules={[{ required: true }]}>
      <Input placeholder="my-smsc" disabled={editing} />
    </Form.Item>
    <Form.Item label="Host" name="host" rules={[{ required: true }]}>
      <Input placeholder="smsc.example.com" />
    </Form.Item>
    <Form.Item label="Port" name="port" rules={[{ required: true }]} initialValue={2775}>
      <InputNumber min={1} max={65535} style={{ width: "100%" }} />
    </Form.Item>
    <Form.Item label="System ID" name="system_id" rules={[{ required: true }]}>
      <Input />
    </Form.Item>
    <Form.Item label="Password" name="password">
      <Input.Password placeholder={editing ? "unchanged" : ""} />
    </Form.Item>
    <Form.Item label="Bind type" name="bind" initialValue="transceiver">
      <Select options={bindOptions} />
    </Form.Item>
    <Form.Item label="System type" name="system_type">
      <Input />
    </Form.Item>
    <Form.Item label="Default source address" name="source_addr">
      <Input placeholder="optional" />
    </Form.Item>
    <Form.Item
      label="Started"
      name="desired_started"
      valuePropName="checked"
      initialValue={false}
      tooltip="Bind and keep the connector connected"
    >
      <Switch />
    </Form.Item>
  </Form>
);
