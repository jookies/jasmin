import type { FormProps } from "antd";
import { Form, Input, InputNumber, Select, Switch } from "antd";

import { FilterList } from "../../components/FilterList";

// MO route destinations: an HTTP callback (legacy HttpConnector URL rules —
// dotted host, localhost or IP only) or a bound SMPPs system_id.
const destinationTypes = [
  { value: "http", label: "http (callback URL)" },
  { value: "smpps", label: "smpps (bound ESME)" },
];

// MORouteFields is the shared create/edit body. The order is the route's
// identity, so it is locked on edit. The default route (order 0) carries no
// connector filter and no content filters — the server enforces that, and the
// form hides them to match.
export const MORouteFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical">
    <Form.Item
      label="Order"
      name="order"
      rules={[{ required: true }]}
      tooltip="Higher order wins; order 0 is the default route"
    >
      <InputNumber min={0} style={{ width: "100%" }} disabled={editing} />
    </Form.Item>
    <Form.Item
      label="Default route"
      name="default"
      valuePropName="checked"
      initialValue={false}
      tooltip="The fallback when no static route matches; must use order 0 and carry no filters"
    >
      <Switch />
    </Form.Item>

    <Form.Item noStyle shouldUpdate>
      {({ getFieldValue }) =>
        getFieldValue("default") ? null : (
          <>
            <Form.Item
              label="Source connector"
              name="filter_connector_id"
              rules={[{ required: true, message: "A static MO route requires a source connector" }]}
              tooltip="The SMSC connector the MO arrived on"
            >
              <Input placeholder="smsc-primary" />
            </Form.Item>
            <FilterList direction="mo" />
          </>
        )
      }
    </Form.Item>

    <Form.Item
      label="Destination type"
      name={["connector", "type"]}
      rules={[{ required: true }]}
      initialValue="http"
    >
      <Select options={destinationTypes} />
    </Form.Item>
    <Form.Item noStyle shouldUpdate>
      {({ getFieldValue }) =>
        getFieldValue(["connector", "type"]) === "smpps" ? (
          <Form.Item
            label="SMPPs system_id"
            name={["connector", "system_id"]}
            rules={[{ required: true }]}
            tooltip="The bound ESME that receives the MO"
          >
            <Input placeholder="shortcode-app" />
          </Form.Item>
        ) : (
          <>
            <Form.Item
              label="Connector ID"
              name={["connector", "cid"]}
              rules={[{ required: true }]}
              tooltip="Names this HTTP destination in logs and receipts"
            >
              <Input placeholder="mo-sink" />
            </Form.Item>
            <Form.Item label="URL" name={["connector", "url"]} rules={[{ required: true }]}>
              <Input placeholder="http://app.example.com/mo" />
            </Form.Item>
            <Form.Item label="Method" name={["connector", "method"]} initialValue="GET">
              <Select
                options={[
                  { value: "GET", label: "GET" },
                  { value: "POST", label: "POST" },
                ]}
              />
            </Form.Item>
          </>
        )
      }
    </Form.Item>
  </Form>
);
