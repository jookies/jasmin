import { ReadOnlyCell } from "../../components/ConfigDetail";
import {
  Create,
  DeleteButton,
  Edit,
  EditButton,
  List,
  useDrawerForm,
  useTable,
} from "@refinedev/antd";
import { Drawer, Form, Input, InputNumber, Space, Switch, Table, Tag, Tooltip } from "antd";
import type { FormProps } from "antd";

import { EffectiveValue, FormIntroduction, PageTitle, StatusBadge } from "../../components/OperatorUI";

type GroupRow = {
  id: string;
  gid: string;
  number: number;
  balance: number | null;
  submit_sm_count: number | null;
  disabled: boolean;
  managed_by: "admin" | "config";
};

const GroupFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical" className="operator-form">
    <FormIntroduction title={editing ? "Update billing group" : "Create a billing group"}>
      Group ceilings are shared by every assigned user. Disabling the group immediately blocks
      authentication for its users.
    </FormIntroduction>
    <Form.Item
      label="Group ID"
      name="gid"
      rules={[
        { required: true },
        { pattern: /^[A-Za-z0-9_-]{1,16}$/, message: "Use 1–16 letters, numbers, _ or -" },
      ]}
    >
      <Input disabled={editing} placeholder="customers" />
    </Form.Item>
    <Form.Item
      label="Shared balance"
      name="balance"
      tooltip="Empty means the group has no balance ceiling"
      extra={<EffectiveValue value="Unlimited" detail="no shared balance ceiling" />}
    >
      <InputNumber min={0} step={0.01} style={{ width: "100%" }} placeholder="unlimited" />
    </Form.Item>
    <Form.Item
      label="Shared submit quota"
      name="submit_sm_count"
      tooltip="Remaining message credits shared by users in this group"
      extra={<EffectiveValue value="Unlimited" detail="no shared message ceiling" />}
    >
      <InputNumber min={0} style={{ width: "100%" }} placeholder="unlimited" />
    </Form.Item>
    <Form.Item
      label="Disabled"
      name="disabled"
      valuePropName="checked"
      initialValue={false}
      tooltip="Users in a disabled group cannot authenticate"
    >
      <Switch />
    </Form.Item>
  </Form>
);

const quota = (value: number | null) =>
  value === null || value === undefined ? <StatusBadge tone="neutral">Unlimited</StatusBadge> : value;

export const GroupList = () => {
  const { tableProps } = useTable<GroupRow>({ syncWithLocation: true });
  const create = useDrawerForm<GroupRow>({ action: "create", syncWithLocation: true });
  const edit = useDrawerForm<GroupRow>({ action: "edit", syncWithLocation: true });

  return (
    <div className="resource-page">
      <List
        title={
          <PageTitle
            eyebrow="Access and billing"
            title="Groups"
            description="Apply shared spending ceilings and suspend a customer group without editing every user."
          />
        }
        createButtonProps={{ onClick: () => create.show(), children: "Add group" }}
      >
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 800 }}>
          <Table.Column dataIndex="gid" title="Group" />
          <Table.Column dataIndex="number" title="Internal ID" />
          <Table.Column dataIndex="balance" title="Balance" render={quota} />
          <Table.Column dataIndex="submit_sm_count" title="Submit quota" render={quota} />
          <Table.Column
            dataIndex="disabled"
            title="Status"
            render={(disabled: boolean) => (
              <StatusBadge tone={disabled ? "negative" : "positive"}>
                {disabled ? "Disabled" : "Enabled"}
              </StatusBadge>
            )}
          />
          <Table.Column
            dataIndex="managed_by"
            title="Source"
            render={(source: GroupRow["managed_by"]) => (
              <Tag color={source === "config" ? "blue" : "green"}>
                {source === "config" ? "Config managed" : "Admin managed"}
              </Tag>
            )}
          />
          <Table.Column<GroupRow>
            title="Actions"
            render={(_, row) =>
              row.managed_by === "config" ? (
                <ReadOnlyCell kind="Group" name={row.gid} record={row as unknown as Record<string, unknown>} />
              ) : (
                <Space>
                  <Tooltip title="Edit group">
                    <EditButton hideText size="small" onClick={() => edit.show(row.id)} />
                  </Tooltip>
                  <Tooltip title="Delete group">
                    <DeleteButton hideText size="small" recordItemId={row.id} />
                  </Tooltip>
                </Space>
              )
            }
          />
        </Table>
      </List>

      <Drawer {...create.drawerProps} width={520}>
        <Create saveButtonProps={create.saveButtonProps}>
          <GroupFields formProps={create.formProps} />
        </Create>
      </Drawer>
      <Drawer {...edit.drawerProps} width={520}>
        <Edit saveButtonProps={edit.saveButtonProps}>
          <GroupFields formProps={edit.formProps} editing />
        </Edit>
      </Drawer>
    </div>
  );
};
