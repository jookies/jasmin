import { ReadOnlyCell } from "../../components/ConfigDetail";
import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { Table, Space, Tag, Drawer, Tooltip , Alert } from "antd";
import { MORouteFields } from "./form";
import { FlushButton } from "../../components/FlushButton";
import { PageTitle, StatusBadge, TableScrollHint } from "../../components/OperatorUI";

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
  managed_by: "admin" | "config";
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

  // The MO side is the dangerous one: an inbound message matching no route is
  // acknowledged and DROPPED, so the carrier believes it delivered and nobody
  // here is told.
  const moRows = tableProps.dataSource ?? [];
  const hasMODefault = moRows.some((r) => r.default);

  return (
    <div className="resource-page">
      {moRows.length > 0 && !hasMODefault && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message="No default MO route — inbound messages can be dropped silently"
          description={
            "Nothing occupies order 0. An inbound message matching no route below is " +
            "acknowledged to the carrier and then discarded, so it looks delivered to " +
            "them and is lost here. Add a default route unless that is intended."
          }
        />
      )}
      <List
        title={
          <PageTitle
            eyebrow="Inbound routing"
            title="MO routes"
            description="Deliver incoming messages to HTTP applications or currently bound SMPPs clients."
          />
        }
        createButtonProps={{ onClick: () => showCreate(), children: "Add MO route" }}
        headerButtons={({ defaultButtons }) => (
          <>
            {defaultButtons}
            <FlushButton
              endpoint="/mo-routes/flush"
              resource="mo-routes"
              label="Clear admin routes"
              description="This removes every admin-managed MO route. Config-managed routes stay active."
            />
          </>
        )}
      >
        <TableScrollHint />
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 900 }}>
          <Table.Column
            dataIndex="order"
            title="Order"
            sorter={(a: MORouteRow, b: MORouteRow) => a.order - b.order}
          />
          <Table.Column
            dataIndex="default"
            title="Default"
            render={(v: boolean) =>
              v ? <StatusBadge tone="progress">Default</StatusBadge> : <span>—</span>
            }
          />
          <Table.Column
            dataIndex="filter_connector_id"
            title="Source connector"
            render={(value?: string) => value || "Any"}
          />
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
          <Table.Column
            dataIndex="managed_by"
            title="Source"
            render={(source: MORouteRow["managed_by"]) => (
              <Tag color={source === "config" ? "blue" : "green"}>
                {source === "config" ? "Config managed" : "Admin managed"}
              </Tag>
            )}
          />
          <Table.Column<MORouteRow>
            title="Actions"
            render={(_, r) =>
              r.managed_by === "config" ? (
                <ReadOnlyCell kind="MO route" name={`order ${r.order}`} record={r as unknown as Record<string, unknown>} />
              ) : (
                <Space>
                  <Tooltip title="Edit MO route">
                    <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                  </Tooltip>
                  <Tooltip title="Delete MO route">
                    <DeleteButton hideText size="small" recordItemId={r.id} />
                  </Tooltip>
                </Space>
              )
            }
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
    </div>
  );
};
