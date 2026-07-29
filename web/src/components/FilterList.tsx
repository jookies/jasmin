import { useList } from "@refinedev/core";
import { Button, Form, Input, Select, Space } from "antd";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";

// The filter types the routing engine accepts and which extra field each one
// uses (mirrors outbound.buildRouteFilter / modispatch.buildMORouteFilters).
// "user" and "group" are MT-only; "connector" is MO-only and expressed as the route's
// filter_connector_id rather than a filter row.
const allFilterTypes = [
  { value: "destination_addr", label: "destination_addr (regex)" },
  { value: "source_addr", label: "source_addr (regex)" },
  { value: "short_message", label: "short_message (regex, match is anchored — use .*X for substring)" },
  { value: "tag", label: "tag (exact value)" },
  { value: "user", label: "user (username)" },
  { value: "group", label: "group (gid)" },
  { value: "date_interval", label: "date_interval (YYYY-MM-DD)" },
  { value: "time_interval", label: "time_interval (HH:MM:SS)" },
];

const patternTypes = new Set(["destination_addr", "source_addr", "short_message"]);
const intervalTypes = new Set(["date_interval", "time_interval"]);

type SavedFilter = {
  id: string;
  fid: string;
  type: string;
  args?: Record<string, string>;
};

type InlineFilter = {
  type: string;
  pattern?: string;
  value?: string;
  group_id?: string;
  start?: string;
  end?: string;
};

const splitInterval = (value = "") => {
  const [start = "", end = ""] = value.split(",", 2).map((part) => part.trim());
  return { start, end };
};

const inlineFilter = (saved: SavedFilter): InlineFilter | undefined => {
  const args = saved.args ?? {};
  switch (saved.type) {
    case "SourceAddrFilter":
      return { type: "source_addr", pattern: args.source_addr };
    case "DestinationAddrFilter":
      return { type: "destination_addr", pattern: args.destination_addr };
    case "ShortMessageFilter":
      return { type: "short_message", pattern: args.short_message };
    case "TagFilter":
      return { type: "tag", value: args.tag };
    case "GroupFilter":
      return { type: "group", group_id: args.gid };
    case "DateIntervalFilter":
      return { type: "date_interval", ...splitInterval(args.dateInterval) };
    case "TimeIntervalFilter":
      return { type: "time_interval", ...splitInterval(args.timeInterval) };
    default:
      return undefined;
  }
};

// FilterFields renders the type-dependent inputs of one filter row.
const FilterFields = ({ name }: { name: number }) => (
  <Form.Item noStyle shouldUpdate>
    {({ getFieldValue }) => {
      const type = getFieldValue(["filters", name, "type"]);
      if (patternTypes.has(type)) {
        return (
          <Form.Item name={[name, "pattern"]} rules={[{ required: true }]} noStyle>
            <Input placeholder="regular expression" style={{ width: "100%" }} />
          </Form.Item>
        );
      }
      if (type === "tag") {
        return (
          <Form.Item name={[name, "value"]} rules={[{ required: true }]} noStyle>
            <Input placeholder="tag value" style={{ width: "100%" }} />
          </Form.Item>
        );
      }
      if (type === "user") {
        return (
          <Form.Item name={[name, "username"]} rules={[{ required: true }]} noStyle>
            <Input placeholder="username" style={{ width: "100%" }} />
          </Form.Item>
        );
      }
      if (type === "group") {
        return (
          <Form.Item name={[name, "group_id"]} rules={[{ required: true }]} noStyle>
            <Input placeholder="group id" style={{ width: "100%" }} />
          </Form.Item>
        );
      }
      if (intervalTypes.has(type)) {
        const placeholder = type === "date_interval" ? "YYYY-MM-DD" : "HH:MM:SS";
        return (
          <Space.Compact style={{ width: "100%" }}>
            <Form.Item name={[name, "start"]} rules={[{ required: true }]} noStyle>
              <Input placeholder={`start ${placeholder}`} style={{ width: "50%" }} />
            </Form.Item>
            <Form.Item name={[name, "end"]} rules={[{ required: true }]} noStyle>
              <Input placeholder={`end ${placeholder}`} style={{ width: "50%" }} />
            </Form.Item>
          </Space.Compact>
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
    direction === "mo"
      ? allFilterTypes.filter((t) => t.value !== "user" && t.value !== "group")
      : allFilterTypes;
  const savedFilters = useList<SavedFilter>({
    resource: "filters",
    pagination: { mode: "off" },
  });
  const templates = (savedFilters.data?.data ?? []).filter((saved) => inlineFilter(saved));

  return (
    <Form.List name="filters">
      {(fields, { add, remove }) => (
        <>
          <div className="filter-list-label">Filters · all conditions must match</div>
          {templates.length > 0 && (
            <Select
              className="filter-template-select"
              value={undefined}
              placeholder="Insert a saved filter"
              options={templates.map((saved) => ({
                value: saved.fid,
                label: `${saved.fid} · ${saved.type}`,
              }))}
              onSelect={(fid) => {
                const saved = templates.find((item) => item.fid === fid);
                const filter = saved ? inlineFilter(saved) : undefined;
                if (filter) add(filter);
              }}
            />
          )}
          {fields.map(({ key, name }) => (
            <Space key={key} align="start" className="filter-row">
              <Form.Item name={[name, "type"]} rules={[{ required: true }]} noStyle>
                <Select options={filterTypes} placeholder="Filter type" />
              </Form.Item>
              <FilterFields name={name} />
              <Button
                icon={<DeleteOutlined />}
                aria-label="Remove filter"
                onClick={() => remove(name)}
              />
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
