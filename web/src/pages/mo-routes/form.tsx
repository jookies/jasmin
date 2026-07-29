import type { FormProps } from "antd";
import { Form, Input, InputNumber, Select, Switch } from "antd";
import { useList } from "@refinedev/core";

import { FilterList } from "../../components/FilterList";
import { EffectiveValue, FormIntroduction } from "../../components/OperatorUI";

// MO route destinations: an HTTP callback (legacy HttpConnector URL rules —
// dotted host, localhost or IP only) or a bound SMPPs system_id.
const destinationTypes = [
  { value: "http", label: "http (callback URL)" },
  { value: "smpps", label: "smpps (bound ESME)" },
];

type HTTPDestination = {
  id: string;
  cid: string;
  baseurl: string;
  method: "GET" | "POST";
};

const HTTPDestinationSelector = () => {
  const form = Form.useFormInstance();
  const destinations = useList<HTTPDestination>({
    resource: "http-connectors",
    pagination: { mode: "off" },
  });
  const rows = destinations.data?.data ?? [];
  if (rows.length === 0) return null;

  return (
    <Form.Item
      label="Use saved destination"
      tooltip="Copies the current destination into this route; future template edits do not change the route"
    >
      <Select
        allowClear
        placeholder="Select a saved HTTP destination"
        options={rows.map((row) => ({
          value: row.cid,
          label: `${row.cid} · ${row.method} ${row.baseurl}`,
        }))}
        onSelect={(cid) => {
          const row = rows.find((item) => item.cid === cid);
          if (!row) return;
          form.setFieldValue("connector", {
            type: "http",
            cid: row.cid,
            url: row.baseurl,
            method: row.method,
          });
        }}
      />
    </Form.Item>
  );
};

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
  <Form {...formProps} layout="vertical" className="operator-form">
    <FormIntroduction title={editing ? "Update MO route" : "Create an MO route"}>
      Choose where inbound messages should be delivered. Static routes can target an HTTP
      application or an active SMPPs bind.
    </FormIntroduction>
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
              tooltip="Optionally restrict this route to the SMSC connector the MO arrived on"
              extra={<EffectiveValue value="Any inbound connector" />}
            >
              <Input placeholder="any connector" />
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
            <HTTPDestinationSelector />
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
