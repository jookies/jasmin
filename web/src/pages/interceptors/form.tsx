import type { FormProps } from "antd";
import { Alert, Form, Input, InputNumber, Select } from "antd";

import { FilterList } from "../../components/FilterList";
import { FormIntroduction } from "../../components/OperatorUI";

const directions = [
  { value: "mt", label: "MT (outbound, pre-routing)" },
  { value: "mo", label: "MO (inbound, deliver path)" },
];

// InterceptorFields is the shared create/edit body. Direction and order form
// the identity, so both lock on edit.
export const InterceptorFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical" className="operator-form">
    <FormIntroduction title={editing ? "Update interceptor" : "Create an interceptor"}>
      Interceptors run before routing. Use the narrowest filters possible and treat every script
      change as a production code change.
    </FormIntroduction>
    <Alert
      type="warning"
      showIcon
      style={{ marginBottom: 16 }}
      message="This script runs as code on the gateway host"
      description="An interceptor is arbitrary Python executed by the gateway process. Only trusted operators should have access to this page."
    />
    <Form.Item label="Direction" name="direction" rules={[{ required: true }]} initialValue="mt">
      <Select options={directions} disabled={editing} />
    </Form.Item>
    <Form.Item
      label="Order"
      name="order"
      rules={[{ required: true }]}
      tooltip="Highest order runs first, until one rejects"
    >
      <InputNumber min={0} style={{ width: "100%" }} disabled={editing} />
    </Form.Item>
    <Form.Item noStyle shouldUpdate>
      {({ getFieldValue }) => (
        <FilterList direction={getFieldValue("direction") === "mo" ? "mo" : "mt"} />
      )}
    </Form.Item>
    <Form.Item
      label="Python script"
      name="py_code"
      rules={[{ required: true, message: "A script is required" }]}
      tooltip="Receives `routable` (pdu.params source_addr / destination_addr / short_message as bytes, and .tags). Set smpp_status / http_status to reject; mutate routable.pdu.params to rewrite the message."
    >
      <Input.TextArea
        rows={12}
        spellCheck={false}
        style={{ fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace" }}
        placeholder={"# reject anything matching the filters above\nsmpp_status = 88\nhttp_status = 400"}
      />
    </Form.Item>
  </Form>
);
