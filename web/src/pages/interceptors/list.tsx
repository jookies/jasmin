import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { Table, Space, Tag, Typography, Drawer, Tooltip } from "antd";
import { InterceptorFields } from "./form";

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

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<InterceptorRow>({ action: "create", syncWithLocation: true });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
  } = useDrawerForm<InterceptorRow>({ action: "edit", syncWithLocation: true });

  return (
    <>
      <List createButtonProps={{ onClick: () => showCreate() }}>
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
                  <Tooltip key={i} title="Interceptor Filter">
                    <Tag>{filterSummary(f)}</Tag>
                  </Tooltip>
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
                <Tooltip title="Edit Interceptor">
                  <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                </Tooltip>
                <Tooltip title="Delete Interceptor">
                  <DeleteButton hideText size="small" recordItemId={r.id} />
                </Tooltip>
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={600}>
        <Create saveButtonProps={createSaveButtonProps}>
          <InterceptorFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={600}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <InterceptorFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </>
  );
};
