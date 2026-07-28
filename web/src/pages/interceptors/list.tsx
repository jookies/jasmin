import { List, useTable, EditButton, DeleteButton } from "@refinedev/antd";
import { Table, Space, Tag, Typography } from "antd";

type FilterRow = { type: string; pattern?: string; value?: string; start?: string; end?: string };

type InterceptorRow = {
  id: string;
  direction: string;
  order: number;
  py_code: string;
  filters?: FilterRow[];
};

const filterSummary = (f: FilterRow) =>
  `${f.type}=${f.pattern ?? f.value ?? `${f.start ?? ""}..${f.end ?? ""}`}`;

// firstLine keeps the table readable: the full script lives in the edit form.
const firstLine = (code: string) => {
  const line = (code ?? "").split("\n").find((l) => l.trim().length > 0) ?? "";
  return line.length > 60 ? `${line.slice(0, 60)}…` : line;
};

export const InterceptorList = () => {
  const { tableProps } = useTable<InterceptorRow>({ syncWithLocation: true });
  return (
    <List>
      <Table {...tableProps} rowKey="id" size="small">
        <Table.Column
          dataIndex="direction"
          title="Direction"
          render={(v: string) => <Tag color={v === "mt" ? "geekblue" : "purple"}>{v.toUpperCase()}</Tag>}
        />
        <Table.Column
          dataIndex="order"
          title="Order"
          sorter={(a: InterceptorRow, b: InterceptorRow) => a.order - b.order}
        />
        <Table.Column<InterceptorRow>
          title="Filters"
          render={(_, r) => (
            <Space wrap>
              {(r.filters ?? []).map((f, i) => (
                <Tag key={i}>{filterSummary(f)}</Tag>
              ))}
            </Space>
          )}
        />
        <Table.Column<InterceptorRow>
          title="Script"
          render={(_, r) => <Typography.Text code>{firstLine(r.py_code)}</Typography.Text>}
        />
        <Table.Column<InterceptorRow>
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
