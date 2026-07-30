import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { Table, Space, Tag, Drawer, Tooltip, Alert } from "antd";
import { RouteFields } from "./form";
import { FlushButton } from "../../components/FlushButton";
import { PageTitle, StatusBadge, TableScrollHint } from "../../components/OperatorUI";

type FilterRow = { type: string; pattern?: string; value?: string; username?: string; start?: string; end?: string };

type RouteRow = {
  id: number;
  order: number;
  connector_id: string;
  connector_ids?: string[];
  rate: number;
  default: boolean;
  filters?: FilterRow[];
  managed_by: "admin" | "config";
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

  // A table with no default route is legal but rarely intended: every submit
  // matching no filter is refused at the front door. Surface the absence rather
  // than leaving an operator to notice it from a customer's failed send.
  const rows = (tableProps.dataSource ?? []) as RouteRow[];
  const hasDefault = rows.some((r) => r.default);

  return (
    <div className="resource-page">
      {rows.length > 0 && !hasDefault && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message="No default MT route"
          description={
            "Nothing occupies order 0, so any submit that matches no filter below is " +
            "refused with \u201cno route matched\u201d. Add a default route unless every " +
            "sender is deliberately restricted to a filtered route."
          }
        />
      )}
      <List
        title={
          <PageTitle
            eyebrow="Outbound routing"
            title="MT routes"
            description="Prioritize outbound traffic, choose connector pools and apply precise message filters."
          />
        }
        createButtonProps={{ onClick: () => showCreate(), children: "Add MT route" }}
        headerButtons={({ defaultButtons }) => (
          <>
            {defaultButtons}
            <FlushButton
              endpoint="/routes/flush"
              resource="routes"
              label="Clear admin routes"
              description="This removes every admin-managed MT route. Config-managed routes stay active."
            />
          </>
        )}
      >
        <TableScrollHint />
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 860 }}>
          <Table.Column dataIndex="order" title="Order" sorter={(a: RouteRow, b: RouteRow) => a.order - b.order} />
          <Table.Column<RouteRow> title="Connector(s)" render={(_, r) => connectorSummary(r)} />
          <Table.Column dataIndex="rate" title="Rate" />
          <Table.Column
            dataIndex="default"
            title="Default"
            render={(v: boolean) =>
              v ? <StatusBadge tone="progress">Default</StatusBadge> : <span>—</span>
            }
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
          <Table.Column
            dataIndex="managed_by"
            title="Source"
            render={(source: RouteRow["managed_by"]) => (
              <Tag color={source === "config" ? "blue" : "green"}>
                {source === "config" ? "Config managed" : "Admin managed"}
              </Tag>
            )}
          />
          <Table.Column<RouteRow>
            title="Actions"
            render={(_, r) =>
              r.managed_by === "config" ? (
                <span className="muted-copy">Read only</span>
              ) : (
                <Space>
                  <Tooltip title="Edit route">
                    <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                  </Tooltip>
                  <Tooltip title="Delete route">
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
          <RouteFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={600}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <RouteFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </div>
  );
};
