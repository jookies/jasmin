import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { Table, Space, Drawer, Tag, Tooltip } from "antd";
import { UserFields } from "./form";
import { PageTitle, StatusBadge, TableScrollHint } from "../../components/OperatorUI";

type UserRow = {
  id: string;
  username: string;
  uid: number;
  external_id?: string;
  group_id?: string;
  disabled?: boolean;
  balance: number | null;
  submit_sm_count: number | null;
  early_decrement_balance_percent: number | null;
  managed_by: "admin" | "config";
};

const orUnlimited = (v: number | null | undefined) => (v === null || v === undefined ? "∞" : v);

export const UserList = () => {
  const { tableProps } = useTable<UserRow>({ syncWithLocation: true });

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<UserRow>({ action: "create", syncWithLocation: true });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
  } = useDrawerForm<UserRow>({ action: "edit", syncWithLocation: true });

  return (
    <div className="resource-page">
      <List
        title={
          <PageTitle
            eyebrow="Access and billing"
            title="Gateway users"
            description="Manage submit credentials, identifiers and the quotas enforced by the HTTP and SMPP paths."
          />
        }
        createButtonProps={{ onClick: () => showCreate(), children: "Add gateway user" }}
      >
        <TableScrollHint />
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 820 }}>
          <Table.Column dataIndex="username" title="Username" />
          <Table.Column dataIndex="external_id" title="External ID" render={(v?: string) => v || "—"} />
          <Table.Column dataIndex="group_id" title="Group" render={(v?: string) => v || "—"} />
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
            dataIndex="balance"
            title="Balance"
            render={(v: number | null | undefined) =>
              v === null || v === undefined ? (
                <StatusBadge tone="neutral">Unlimited</StatusBadge>
              ) : (
                orUnlimited(v)
              )
            }
          />
          <Table.Column
            dataIndex="submit_sm_count"
            title="Submit quota"
            render={(v: number | null | undefined) =>
              v === null || v === undefined ? (
                <StatusBadge tone="neutral">Unlimited</StatusBadge>
              ) : (
                orUnlimited(v)
              )
            }
          />
          <Table.Column
            dataIndex="managed_by"
            title="Source"
            render={(source: UserRow["managed_by"]) => (
              <Tag color={source === "config" ? "blue" : "green"}>
                {source === "config" ? "Config managed" : "Admin managed"}
              </Tag>
            )}
          />
          <Table.Column<UserRow>
            title="Actions"
            render={(_, r) =>
              r.managed_by === "config" ? (
                <span className="muted-copy">Read only</span>
              ) : (
                <Space>
                  <Tooltip title="Edit user">
                    <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                  </Tooltip>
                  <Tooltip title="Delete user">
                    <DeleteButton hideText size="small" recordItemId={r.id} />
                  </Tooltip>
                </Space>
              )
            }
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={760}>
        <Create saveButtonProps={createSaveButtonProps}>
          <UserFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={760}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <UserFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </div>
  );
};
