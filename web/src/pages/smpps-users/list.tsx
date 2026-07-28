import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { Table, Space, Tag, Drawer, Tooltip } from "antd";
import { SMPPsUserFields } from "./form";

type SMPPsUserRow = {
  id: string;
  system_id: string;
  disabled?: boolean;
  ip_whitelist?: string;
  max_bindings?: number | null;
};

export const SMPPsUserList = () => {
  const { tableProps } = useTable<SMPPsUserRow>({ syncWithLocation: true });

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<SMPPsUserRow>({ action: "create", syncWithLocation: true });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
  } = useDrawerForm<SMPPsUserRow>({ action: "edit", syncWithLocation: true });

  return (
    <>
      <List createButtonProps={{ onClick: () => showCreate() }}>
        <Table {...tableProps} rowKey="id" size="small">
          <Table.Column dataIndex="system_id" title="System ID" />
          <Table.Column
            dataIndex="disabled"
            title="State"
            render={(v: boolean) =>
              v ? <Tag>disabled</Tag> : <Tag color="green">enabled</Tag>
            }
          />
          <Table.Column
            dataIndex="max_bindings"
            title="Max binds"
            render={(v: number | null | undefined) => (v === null || v === undefined ? "∞" : v)}
          />
          <Table.Column
            dataIndex="ip_whitelist"
            title="IP whitelist"
            render={(v: string) => v || "any"}
          />
          <Table.Column<SMPPsUserRow>
            title="Actions"
            render={(_, r) => (
              <Space>
                <Tooltip title="Edit SMPPs User">
                  <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                </Tooltip>
                <Tooltip title="Delete SMPPs User">
                  <DeleteButton hideText size="small" recordItemId={r.id} />
                </Tooltip>
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={500}>
        <Create saveButtonProps={createSaveButtonProps}>
          <SMPPsUserFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={500}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <SMPPsUserFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </>
  );
};
