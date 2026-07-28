import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { Table, Space, Tag, Drawer, Tooltip } from "antd";
import { RouteFields } from "./form";

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

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<RouteRow>({ action: "create", syncWithLocation: true });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
  } = useDrawerForm<RouteRow>({ action: "edit", syncWithLocation: true });

  return (
    <>
      <List createButtonProps={{ onClick: () => showCreate() }}>
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
                  <Tooltip key={i} title="Route Filter">
                    <Tag>{filterSummary(f)}</Tag>
                  </Tooltip>
                ))}
              </Space>
            )}
          />
          <Table.Column<RouteRow>
            title="Actions"
            render={(_, r) => (
              <Space>
                <Tooltip title="Edit Route">
                  <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                </Tooltip>
                <Tooltip title="Delete Route">
                  <DeleteButton hideText size="small" recordItemId={r.id} />
                </Tooltip>
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={600}>
        <Create saveButtonProps={createSaveButtonProps}>
          <RouteFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={600}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <RouteFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </>
  );
};
