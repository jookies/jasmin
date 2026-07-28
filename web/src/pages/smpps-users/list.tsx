import { List, useTable, EditButton, DeleteButton } from "@refinedev/antd";
import { Table, Space, Tag } from "antd";

type SMPPsUserRow = {
  id: string;
  system_id: string;
  disabled?: boolean;
  ip_whitelist?: string;
  max_bindings?: number | null;
};

export const SMPPsUserList = () => {
  const { tableProps } = useTable<SMPPsUserRow>({ syncWithLocation: true });
  return (
    <List>
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
              <EditButton hideText size="small" recordItemId={r.id} />
              <DeleteButton hideText size="small" recordItemId={r.id} />
            </Space>
          )}
        />
      </Table>
    </List>
  );
};
