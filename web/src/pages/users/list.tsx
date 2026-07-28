import { List, useTable, EditButton, DeleteButton } from "@refinedev/antd";
import { Table, Space } from "antd";

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
  return (
    <List>
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
              <EditButton hideText size="small" recordItemId={r.id} />
              <DeleteButton hideText size="small" recordItemId={r.id} />
            </Space>
          )}
        />
      </Table>
    </List>
  );
};
