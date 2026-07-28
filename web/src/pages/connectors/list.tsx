import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { useUpdate } from "@refinedev/core";
import { Table, Space, Tag, Button, Drawer, Tooltip } from "antd";
import { PlayCircleOutlined, PauseCircleOutlined } from "@ant-design/icons";
import { ConnectorFields } from "./form";

type ConnectorRow = {
  id: string;
  cid: string;
  host: string;
  port: number;
  system_id: string;
  bind: string;
  desired_started: boolean;
  observed: string;
};

const observedColor = (observed: string) =>
  observed === "BOUND" ? "green" : observed === "CONNECTING" ? "orange" : "red";

export const ConnectorList = () => {
  const { tableProps } = useTable<ConnectorRow>({ syncWithLocation: true });
  const { mutate: update, isLoading: toggling } = useUpdate();

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<ConnectorRow>({ action: "create", syncWithLocation: true });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
  } = useDrawerForm<ConnectorRow>({ action: "edit", syncWithLocation: true });

  const toggleStarted = (row: ConnectorRow) =>
    update({
      resource: "connectors",
      id: row.id,
      values: { desired_started: !row.desired_started },
    });

  return (
    <>
      <List
        createButtonProps={{ onClick: () => showCreate() }}
      >
        <Table {...tableProps} rowKey="id" size="small">
          <Table.Column dataIndex="cid" title="ID" />
          <Table.Column<ConnectorRow> title="Target" render={(_, r) => `${r.host}:${r.port}`} />
          <Table.Column dataIndex="system_id" title="System ID" />
          <Table.Column dataIndex="bind" title="Bind" />
          <Table.Column
            dataIndex="desired_started"
            title="Desired"
            render={(v: boolean) => (v ? <Tag color="blue">started</Tag> : <Tag>stopped</Tag>)}
          />
          <Table.Column
            dataIndex="observed"
            title="Status"
            render={(v: string) => <Tag color={observedColor(v)}>{v || "—"}</Tag>}
          />
          <Table.Column<ConnectorRow>
            title="Actions"
            render={(_, r) => (
              <Space>
                <Tooltip title={r.desired_started ? "Stop Connector" : "Start Connector"}>
                  <Button
                    size="small"
                    loading={toggling}
                    icon={r.desired_started ? <PauseCircleOutlined /> : <PlayCircleOutlined />}
                    onClick={() => toggleStarted(r)}
                  >
                    {r.desired_started ? "Stop" : "Start"}
                  </Button>
                </Tooltip>
                <Tooltip title="Edit Connector">
                  <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                </Tooltip>
                <Tooltip title="Delete Connector">
                  <DeleteButton hideText size="small" recordItemId={r.id} />
                </Tooltip>
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={500}>
        <Create saveButtonProps={createSaveButtonProps}>
          <ConnectorFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={500}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <ConnectorFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </>
  );
};
