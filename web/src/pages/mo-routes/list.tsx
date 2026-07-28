import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { Table, Space, Tag, Drawer, Tooltip } from "antd";
import { MORouteFields } from "./form";

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

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<MORouteRow>({ action: "create", syncWithLocation: true });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
  } = useDrawerForm<MORouteRow>({ action: "edit", syncWithLocation: true });

  return (
    <>
      <List createButtonProps={{ onClick: () => showCreate() }}>
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
                  <Tooltip key={i} title="Route Filter">
                    <Tag>{filterSummary(f)}</Tag>
                  </Tooltip>
                ))}
              </Space>
            )}
          />
          <Table.Column<MORouteRow>
            title="Actions"
            render={(_, r) => (
              <Space>
                <Tooltip title="Edit MO Route">
                  <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                </Tooltip>
                <Tooltip title="Delete MO Route">
                  <DeleteButton hideText size="small" recordItemId={r.id} />
                </Tooltip>
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={600}>
        <Create saveButtonProps={createSaveButtonProps}>
          <MORouteFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={600}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <MORouteFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </>
  );
};
