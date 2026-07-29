import type { FormProps } from "antd";
import { Form, InputNumber, Select, Switch } from "antd";
import { useSelect } from "@refinedev/antd";

import { FilterList } from "../../components/FilterList";
import { FormIntroduction } from "../../components/OperatorUI";

// RouteFields is the shared create/edit form body. `editing` locks the route
// order (a route's order is its identity — recreate to renumber).
export const RouteFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => {
  const { selectProps: connectorSelect } = useSelect({
    resource: "connectors",
    optionLabel: "id",
    optionValue: "id",
  });
  return (
    <Form {...formProps} layout="vertical" className="operator-form">
      <FormIntroduction title={editing ? "Update MT route" : "Create an MT route"}>
        Higher orders win. Add filters only when this route should handle a specific subset of
        outbound traffic.
      </FormIntroduction>
      <Form.Item
        label="Order"
        name="order"
        rules={[{ required: true }]}
        tooltip="Higher order wins; the order is the route's identity"
      >
        <InputNumber min={0} style={{ width: "100%" }} disabled={editing} />
      </Form.Item>
      <Form.Item label="Connector" name="connector_id" tooltip="Target SMPP client connector">
        <Select {...connectorSelect} allowClear placeholder="select a connector" />
      </Form.Item>
      <Form.Item
        label="Additional connectors (failover/random pick)"
        name="connector_ids"
        tooltip="When set, this list overrides the single connector above"
      >
        <Select {...connectorSelect} mode="multiple" allowClear placeholder="optional" />
      </Form.Item>
      <Form.Item label="Rate" name="rate" initialValue={0}>
        <InputNumber min={0} step={0.001} style={{ width: "100%" }} />
      </Form.Item>
      <Form.Item label="Default route" name="default" valuePropName="checked" initialValue={false}>
        <Switch />
      </Form.Item>
      <FilterList direction="mt" />
    </Form>
  );
};
