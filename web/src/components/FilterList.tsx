import { Button, Form, Input, Select, Space } from "antd";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";

// The filter types the routing engine accepts and which extra field each one
// uses (mirrors outbound.buildRouteFilter / modispatch.buildMORouteFilters).
// "user" is MT-only; "connector" is MO-only and expressed as the route's
// filter_connector_id rather than a filter row.
const allFilterTypes = [
  { value: "destination_addr", label: "destination_addr (regex)" },
  { value: "source_addr", label: "source_addr (regex)" },
  { value: "short_message", label: "short_message (regex, match is anchored — use .*X for substring)" },
  { value: "tag", label: "tag (exact value)" },
  { value: "user", label: "user (username)" },
  { value: "date_interval", label: "date_interval (YYYY-MM-DD)" },
  { value: "time_interval", label: "time_interval (HH:MM:SS)" },
];

const patternTypes = new Set(["destination_addr", "source_addr", "short_message"]);
const intervalTypes = new Set(["date_interval", "time_interval"]);

// FilterFields renders the type-dependent inputs of one filter row.
const FilterFields = ({ name }: { name: number }) => (
  <Form.Item noStyle shouldUpdate>
    {({ getFieldValue }) => {
      const type = getFieldValue(["filters", name, "type"]);
      if (patternTypes.has(type)) {
        return (
          <Form.Item name={[name, "pattern"]} rules={[{ required: true }]} noStyle>
            <Input placeholder="regular expression" style={{ width: 260 }} />
          </Form.Item>
        );
      }
      if (type === "tag") {
        return (
          <Form.Item name={[name, "value"]} rules={[{ required: true }]} noStyle>
            <Input placeholder="tag value" style={{ width: 260 }} />
          </Form.Item>
        );
      }
      if (type === "user") {
        return (
          <Form.Item name={[name, "username"]} rules={[{ required: true }]} noStyle>
            <Input placeholder="username" style={{ width: 260 }} />
          </Form.Item>
        );
      }
      if (intervalTypes.has(type)) {
        const placeholder = type === "date_interval" ? "YYYY-MM-DD" : "HH:MM:SS";
        return (
          <Space>
            <Form.Item name={[name, "start"]} rules={[{ required: true }]} noStyle>
              <Input placeholder={`start ${placeholder}`} style={{ width: 130 }} />
            </Form.Item>
            <Form.Item name={[name, "end"]} rules={[{ required: true }]} noStyle>
              <Input placeholder={`end ${placeholder}`} style={{ width: 130 }} />
            </Form.Item>
          </Space>
        );
      }
      return null;
    }}
  </Form.Item>
);

// FilterList is the repeatable filter block shared by the MT route, MO route
// and interceptor forms. All filters must match for the rule to apply.
// `direction` drops the types the engine rejects for that direction, so the
// form cannot offer a filter the server will refuse.
export const FilterList = ({ direction = "mt" }: { direction?: "mt" | "mo" }) => {
  const filterTypes =
    direction === "mo" ? allFilterTypes.filter((t) => t.value !== "user") : allFilterTypes;

  return (
    <Form.List name="filters">
      {(fields, { add, remove }) => (
        <>
          <div style={{ marginBottom: 8, fontWeight: 500 }}>Filters (all must match)</div>
          {fields.map(({ key, name }) => (
            <Space key={key} align="baseline" style={{ display: "flex", marginBottom: 8 }}>
              <Form.Item name={[name, "type"]} rules={[{ required: true }]} noStyle>
                <Select options={filterTypes} placeholder="filter type" style={{ width: 260 }} />
              </Form.Item>
              <FilterFields name={name} />
              <Button icon={<DeleteOutlined />} onClick={() => remove(name)} />
            </Space>
          ))}
          <Form.Item>
            <Button icon={<PlusOutlined />} onClick={() => add({})}>
              Add filter
            </Button>
          </Form.Item>
        </>
      )}
    </Form.List>
  );
};
