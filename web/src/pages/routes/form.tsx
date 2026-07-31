import type { FormProps } from "antd";
import { Form, InputNumber, Select, Switch } from "antd";
import { useSelect } from "@refinedev/antd";

import { FilterList } from "../../components/FilterList";
import { FormIntroduction } from "../../components/OperatorUI";

// Mirrors internal/core/routingtable.ConnectorType's MT-eligible values
// (table.go: connectorAllowed permits SMPPC or TERM for MT). Empty on the wire
// means "smppc" (outbound/config.go's routeConnectorType), but the form always
// writes one explicitly so a saved route's target is never ambiguous.
const connectorTypeOptions = [
  { value: "smppc", label: "Outbound SMPP connector" },
  { value: "term", label: "Termination connector (local delivery)" },
];

// ConnectorTypeField owns the connector_type select and, on a genuine user
// change, clears whatever connector(s) were picked under the previous type —
// a connector id valid for smppc is essentially never valid for term and vice
// versa, and the backend has no concept of "reinterpret this id under the new
// type", so a stale id would just fail to route silently until someone sends
// a test message. Resetting only fires from onChange (a real edit), never from
// the initial value load, so opening an existing route does not blank its
// selection.
const ConnectorTypeField = () => {
  const form = Form.useFormInstance();
  return (
    <Form.Item
      label="Connector type"
      name="connector_type"
      initialValue="smppc"
      rules={[{ required: true }]}
      tooltip="A route may target an outbound SMPP connector or a termination connector, never a mix of both"
    >
      <Select
        options={connectorTypeOptions}
        onChange={() => form.resetFields(["connector_id", "connector_ids"])}
      />
    </Form.Item>
  );
};

// ConnectorPicker lists connectors of whichever type is currently selected —
// resource "connectors" for smppc, "termination-connectors" for term — so the
// dropdown itself makes mixing types impossible instead of letting the backend
// reject it after the fact.
const ConnectorPicker = () => {
  const connectorType = (Form.useWatch("connector_type") as string | undefined) ?? "smppc";
  const isTerm = connectorType === "term";
  const { selectProps: connectorSelect } = useSelect({
    resource: isTerm ? "termination-connectors" : "connectors",
    optionLabel: "id",
    optionValue: "id",
  });
  return (
    <>
      <Form.Item
        label="Connector"
        name="connector_id"
        tooltip={isTerm ? "Target termination connector" : "Target SMPP client connector"}
      >
        <Select {...connectorSelect} allowClear placeholder="select a connector" />
      </Form.Item>
      <Form.Item
        label="Additional connectors (failover/random pick)"
        name="connector_ids"
        tooltip="When set, this list overrides the single connector above"
      >
        <Select {...connectorSelect} mode="multiple" allowClear placeholder="optional" />
      </Form.Item>
    </>
  );
};

// RouteFields is the shared create/edit form body. `editing` locks the route
// order (a route's order is its identity — recreate to renumber).
export const RouteFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => {
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
      <ConnectorTypeField />
      <ConnectorPicker />
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
