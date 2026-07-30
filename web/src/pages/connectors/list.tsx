import { ReadOnlyCell } from "../../components/ConfigDetail";
import { IntegrationGuideButton } from "../../components/IntegrationGuide";
import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { useUpdate } from "@refinedev/core";
import { Table, Space, Button, Drawer, Tag, Tooltip } from "antd";
import { PlayCircleOutlined, PauseCircleOutlined } from "@ant-design/icons";
import { ConnectorFields } from "./form";
import {
  PageTitle,
  StatusBadge,
  TableScrollHint,
  type StatusTone,
} from "../../components/OperatorUI";

type ConnectorRow = {
  id: string;
  cid: string;
  host: string;
  port: number;
  system_id: string;
  bind: string;
  desired_started: boolean;
  observed: string;
  managed_by: "admin" | "config";
};

const observedTone = (observed: string): StatusTone =>
  observed === "BOUND" ? "positive" : observed === "CONNECTING" ? "progress" : "negative";

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
    <div className="resource-page">
      <List
        title={
          <PageTitle
            eyebrow="Messaging"
            title="SMPP connectors"
            description="Control upstream SMSC sessions and see the desired and observed state side by side."
          />
        }
        createButtonProps={{ onClick: () => showCreate(), children: "Add connector" }}
      >
        <TableScrollHint />
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 1080 }}>
          <Table.Column dataIndex="cid" title="ID" />
          <Table.Column<ConnectorRow> title="Target" render={(_, r) => `${r.host}:${r.port}`} />
          <Table.Column dataIndex="system_id" title="System ID" />
          <Table.Column dataIndex="bind" title="Bind" />
          <Table.Column
            dataIndex="desired_started"
            title="Desired"
            render={(v: boolean) => (
              <StatusBadge tone={v ? "progress" : "neutral"}>
                {v ? "Started" : "Stopped"}
              </StatusBadge>
            )}
          />
          <Table.Column
            dataIndex="observed"
            title="Status"
            render={(v: string) => (
              <StatusBadge tone={observedTone(v)}>{v || "Unknown"}</StatusBadge>
            )}
          />
          <Table.Column
            dataIndex="managed_by"
            title="Source"
            render={(source: ConnectorRow["managed_by"]) => (
              <Tag color={source === "config" ? "blue" : "green"}>
                {source === "config" ? "Config managed" : "Admin managed"}
              </Tag>
            )}
          />
          {/*
            A connector is us dialling out, so the artifact here is the inverse
            of the account pack: what to request from the carrier.
          */}
          <Table.Column<ConnectorRow>
            title="Actions"
            render={(_, r) =>
              r.managed_by === "config" ? (
                <Space>
                  <IntegrationGuideButton kind="connector" id={r.id} label="Carrier brief" />
                  <ReadOnlyCell kind="Connector" name={r.cid} record={r as unknown as Record<string, unknown>} />
                </Space>
              ) : (
                <Space>
                  <IntegrationGuideButton kind="connector" id={r.id} label="Carrier brief" />
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
              )
            }
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={760}>
        <Create saveButtonProps={createSaveButtonProps}>
          <ConnectorFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={760}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <ConnectorFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </div>
  );
};
