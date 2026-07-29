import {
  Create,
  DeleteButton,
  Edit,
  EditButton,
  List,
  useDrawerForm,
  useTable,
} from "@refinedev/antd";
import { Drawer, Form, Input, Select, Space, Table, Tag, Tooltip } from "antd";
import type { FormProps } from "antd";

import { FormIntroduction, PageTitle } from "../../components/OperatorUI";

type FilterRow = {
  id: string;
  fid: string;
  type: string;
  args?: Record<string, string>;
};

const filterTypes = [
  { value: "TransparentFilter", label: "Transparent · MO / MT", directions: "MO · MT" },
  { value: "ConnectorFilter", label: "Connector · MO", directions: "MO" },
  { value: "UserFilter", label: "User · MT", directions: "MT" },
  { value: "GroupFilter", label: "Group · MT", directions: "MT" },
  { value: "SourceAddrFilter", label: "Source address · MO / MT", directions: "MO · MT" },
  { value: "DestinationAddrFilter", label: "Destination address · MO / MT", directions: "MO · MT" },
  { value: "ShortMessageFilter", label: "Message content · MO / MT", directions: "MO · MT" },
  { value: "DateIntervalFilter", label: "Date interval · MO / MT", directions: "MO · MT" },
  { value: "TimeIntervalFilter", label: "Time interval · MO / MT", directions: "MO · MT" },
  { value: "EvalPyFilter", label: "Expression · MO / MT", directions: "MO · MT" },
  { value: "TagFilter", label: "Tag · MO / MT", directions: "MO · MT" },
];

const argumentFor: Record<string, { name: string; label: string; placeholder?: string }> = {
  ConnectorFilter: { name: "cid", label: "Connector ID", placeholder: "smsc-primary" },
  UserFilter: { name: "uid", label: "User ID", placeholder: "customer-a" },
  GroupFilter: { name: "gid", label: "Group ID", placeholder: "customers" },
  SourceAddrFilter: { name: "source_addr", label: "Source regex", placeholder: "^1202" },
  DestinationAddrFilter: { name: "destination_addr", label: "Destination regex", placeholder: "^\\+49" },
  ShortMessageFilter: { name: "short_message", label: "Message regex", placeholder: ".*STOP" },
  DateIntervalFilter: { name: "dateInterval", label: "Date interval", placeholder: "2026-01-01,2026-12-31" },
  TimeIntervalFilter: { name: "timeInterval", label: "Time interval", placeholder: "08:00:00,18:00:00" },
  EvalPyFilter: { name: "pyCode", label: "Expression", placeholder: "result = routable.user.uid == '1'" },
  TagFilter: { name: "tag", label: "Tag", placeholder: "campaign-a" },
};

const FilterArgumentField = () => {
  const type = Form.useWatch("type");
  const argument = argumentFor[type];
  if (!argument) return null;
  return (
    <Form.Item
      label={argument.label}
      name={["args", argument.name]}
      rules={[{ required: true }]}
      tooltip={
        argument.name.includes("addr") || argument.name === "short_message"
          ? "Regex matching is anchored at the beginning; use .* for substring matching"
          : undefined
      }
    >
      <Input.TextArea
        autoSize={argument.name === "pyCode" ? { minRows: 4, maxRows: 10 } : { minRows: 1, maxRows: 4 }}
        placeholder={argument.placeholder}
      />
    </Form.Item>
  );
};

const FilterFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical" className="operator-form">
    <FormIntroduction title={editing ? "Update saved filter" : "Create a saved filter"}>
      Saved filters are templates. Adding one to a route copies its current definition, so later
      template edits do not silently change live routing.
    </FormIntroduction>
    <Form.Item
      label="Filter ID"
      name="fid"
      rules={[
        { required: true },
        { pattern: /^[A-Za-z0-9_-]{1,16}$/, message: "Use 1–16 letters, numbers, _ or -" },
      ]}
    >
      <Input disabled={editing} placeholder="german-mobile" />
    </Form.Item>
    <Form.Item label="Type" name="type" rules={[{ required: true }]}>
      <Select options={filterTypes} placeholder="Choose a filter type" />
    </Form.Item>
    <FilterArgumentField />
  </Form>
);

const argumentSummary = (row: FilterRow) => {
  const entries = Object.entries(row.args ?? {});
  if (entries.length === 0) return "Matches everything";
  return entries.map(([key, value]) => `${key}: ${value}`).join(" · ");
};

export const FilterListPage = () => {
  const { tableProps } = useTable<FilterRow>({ syncWithLocation: true });
  const create = useDrawerForm<FilterRow>({ action: "create", syncWithLocation: true });
  const edit = useDrawerForm<FilterRow>({ action: "edit", syncWithLocation: true });

  return (
    <div className="resource-page">
      <List
        title={
          <PageTitle
            eyebrow="Routing library"
            title="Saved filters"
            description="Keep common routing conditions consistent. Address, message, tag and interval templates can be copied into MT or MO rules."
          />
        }
        createButtonProps={{ onClick: () => create.show(), children: "Add filter" }}
      >
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 850 }}>
          <Table.Column dataIndex="fid" title="Filter" />
          <Table.Column dataIndex="type" title="Type" />
          <Table.Column<FilterRow>
            title="Direction"
            render={(_, row) => (
              <Tag>{filterTypes.find((item) => item.value === row.type)?.directions ?? "—"}</Tag>
            )}
          />
          <Table.Column<FilterRow>
            title="Definition"
            ellipsis
            render={(_, row) => <span title={argumentSummary(row)}>{argumentSummary(row)}</span>}
          />
          <Table.Column<FilterRow>
            title="Actions"
            render={(_, row) => (
              <Space>
                <Tooltip title="Edit saved filter">
                  <EditButton hideText size="small" onClick={() => edit.show(row.id)} />
                </Tooltip>
                <Tooltip title="Delete saved filter">
                  <DeleteButton hideText size="small" recordItemId={row.id} />
                </Tooltip>
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...create.drawerProps} width={560}>
        <Create saveButtonProps={create.saveButtonProps}>
          <FilterFields formProps={create.formProps} />
        </Create>
      </Drawer>
      <Drawer {...edit.drawerProps} width={560}>
        <Edit saveButtonProps={edit.saveButtonProps}>
          <FilterFields formProps={edit.formProps} editing />
        </Edit>
      </Drawer>
    </div>
  );
};
