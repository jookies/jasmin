import { List, useTable, EditButton, DeleteButton } from "@refinedev/antd";
import { Table, Space, Tag } from "antd";

type FilterRow = { type: string; pattern?: string; value?: string; username?: string; start?: string; end?: string };

type RouteRow = {
  id: number;
  order: number;
  connector_id: string;
  connector_ids?: string[];
  rate: number;
  default: boolean;
  filters?: FilterRow[];
};

const connectorSummary = (r: RouteRow) =>
  r.connector_ids && r.connector_ids.length > 0 ? r.connector_ids.join(", ") : r.connector_id;

const filterSummary = (f: FilterRow) =>
  `${f.type}=${f.pattern ?? f.value ?? f.username ?? `${f.start ?? ""}..${f.end ?? ""}`}`;

export const RouteList = () => {
  const { tableProps } = useTable<RouteRow>({ syncWithLocation: true });
  return (
    <List>
      <Table {...tableProps} rowKey="id" size="small">
        <Table.Column dataIndex="order" title="Order" sorter={(a: RouteRow, b: RouteRow) => a.order - b.order} />
        <Table.Column<RouteRow> title="Connector(s)" render={(_, r) => connectorSummary(r)} />
        <Table.Column dataIndex="rate" title="Rate" />
        <Table.Column
          dataIndex="default"
          title="Default"
          render={(v: boolean) => (v ? <Tag color="blue">default</Tag> : null)}
        />
        <Table.Column<RouteRow>
          title="Filters"
          render={(_, r) => (
            <Space wrap>
              {(r.filters ?? []).map((f, i) => (
                <Tag key={i}>{filterSummary(f)}</Tag>
              ))}
            </Space>
          )}
        />
        <Table.Column<RouteRow>
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
