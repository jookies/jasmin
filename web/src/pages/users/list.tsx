import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { Table, Space, Drawer, Tooltip } from "antd";
import { UserFields } from "./form";

type UserRow = {
  id: string;
  username: string;
  uid: number;
  external_id?: string;
  balance: number | null;
  submit_sm_count: number | null;
  early_decrement_balance_percent: number | null;
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
    <>
      <List createButtonProps={{ onClick: () => showCreate() }}>
        <Table {...tableProps} rowKey="id" size="small">
          <Table.Column dataIndex="username" title="Username" />
          <Table.Column dataIndex="uid" title="UID" />
          <Table.Column dataIndex="external_id" title="External ID" />
          <Table.Column dataIndex="balance" title="Balance" render={orUnlimited} />
          <Table.Column dataIndex="submit_sm_count" title="Submit quota" render={orUnlimited} />
          <Table.Column<UserRow>
            title="Actions"
            render={(_, r) => (
              <Space>
                <Tooltip title="Edit User">
                  <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                </Tooltip>
                <Tooltip title="Delete User">
                  <DeleteButton hideText size="small" recordItemId={r.id} />
                </Tooltip>
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={500}>
        <Create saveButtonProps={createSaveButtonProps}>
          <UserFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={500}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <UserFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </>
  );
};
