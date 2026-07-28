import { List, useTable, EditButton, DeleteButton } from "@refinedev/antd";
import { Table, Space, Tag } from "antd";

type FilterRow = {
  type: string;
  pattern?: string;
  value?: string;
  start?: string;
  end?: string;
};

type MORouteRow = {
  id: number;
  order: number;
  default: boolean;
  filter_connector_id?: string;
  filters?: FilterRow[];
  connector: { type: string; cid?: string; url?: string; method?: string; system_id?: string };
};

const destinationSummary = (r: MORouteRow) =>
  r.connector?.type === "smpps"
    ? `smpps:${r.connector.system_id}`
    : `${r.connector?.method ?? "GET"} ${r.connector?.url ?? ""}`;

const filterSummary = (f: FilterRow) =>
  `${f.type}=${f.pattern ?? f.value ?? `${f.start ?? ""}..${f.end ?? ""}`}`;

export const MORouteList = () => {
  const { tableProps } = useTable<MORouteRow>({ syncWithLocation: true });
  return (
    <List>
      <Table {...tableProps} rowKey="id" size="small">
        <Table.Column
          dataIndex="order"
          title="Order"
          sorter={(a: MORouteRow, b: MORouteRow) => a.order - b.order}
        />
        <Table.Column
          dataIndex="default"
          title="Default"
          render={(v: boolean) => (v ? <Tag color="blue">default</Tag> : null)}
        />
        <Table.Column dataIndex="filter_connector_id" title="Source connector" />
        <Table.Column<MORouteRow> title="Destination" render={(_, r) => destinationSummary(r)} />
        <Table.Column<MORouteRow>
          title="Filters"
          render={(_, r) => (
            <Space wrap>
              {(r.filters ?? []).map((f, i) => (
                <Tag key={i}>{filterSummary(f)}</Tag>
              ))}
            </Space>
          )}
        />
        <Table.Column<MORouteRow>
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
