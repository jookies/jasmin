import { ReadOnlyCell } from "../../components/ConfigDetail";
import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { useInvalidate } from "@refinedev/core";
import { App, Button, Drawer, Modal, Space, Table, Tag, Tooltip } from "antd";
import { DisconnectOutlined, StopOutlined } from "@ant-design/icons";
import { SMPPsUserFields } from "./form";
import { PageTitle, StatusBadge, TableScrollHint } from "../../components/OperatorUI";
import { API_URL, httpClient } from "../../httpClient";

type SMPPsUserRow = {
  id: string;
  system_id: string;
  disabled?: boolean;
  ip_whitelist?: string;
  max_bindings?: number | null;
  managed_by: "admin" | "config";
};

export const SMPPsUserList = () => {
  const { tableProps } = useTable<SMPPsUserRow>({ syncWithLocation: true });
  const { message } = App.useApp();
  const invalidate = useInvalidate();

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<SMPPsUserRow>({ action: "create", syncWithLocation: true });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
  } = useDrawerForm<SMPPsUserRow>({ action: "edit", syncWithLocation: true });

  const refresh = () =>
    invalidate({ resource: "smpps-users", invalidates: ["list", "many", "detail"] });

  const unbind = async (row: SMPPsUserRow) => {
    try {
      const response = await httpClient.post(
        `${API_URL}/smpps-users/${encodeURIComponent(row.system_id)}/unbind`,
      );
      const sessions = Number(response.data?.sessions ?? 0);
      void message.success(
        sessions === 1 ? "Closed 1 active bind" : `Closed ${sessions} active binds`,
      );
    } catch {
      void message.error("Could not close the active binds");
    }
  };

  const ban = (row: SMPPsUserRow) => {
    Modal.confirm({
      title: `Ban ${row.system_id}?`,
      content:
        "This disables the bind account and immediately closes every active session. You can re-enable it by editing the account later.",
      okText: "Ban account",
      okButtonProps: { danger: true },
      async onOk() {
        await httpClient.post(
          `${API_URL}/smpps-users/${encodeURIComponent(row.system_id)}/ban`,
        );
        await refresh();
        void message.success(`Banned ${row.system_id}`);
      },
    });
  };

  return (
    <div className="resource-page">
      <List
        title={
          <PageTitle
            eyebrow="Inbound access"
            title="SMPPs bind accounts"
            description="Control the ESME credentials, network restrictions and permissions accepted by the SMPP server."
          />
        }
        createButtonProps={{ onClick: () => showCreate(), children: "Add bind account" }}
      >
        <TableScrollHint />
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 760 }}>
          <Table.Column dataIndex="system_id" title="System ID" />
          <Table.Column
            dataIndex="disabled"
            title="State"
            render={(v: boolean) => (
              <StatusBadge tone={v ? "neutral" : "positive"}>
                {v ? "Disabled" : "Enabled"}
              </StatusBadge>
            )}
          />
          <Table.Column
            dataIndex="max_bindings"
            title="Max binds"
            render={(v: number | null | undefined) => (v === null || v === undefined ? "∞" : v)}
          />
          <Table.Column
            dataIndex="ip_whitelist"
            title="IP whitelist"
            render={(v: string) => v || "any"}
          />
          <Table.Column
            dataIndex="managed_by"
            title="Source"
            render={(source: SMPPsUserRow["managed_by"]) => (
              <Tag color={source === "config" ? "blue" : "green"}>
                {source === "config" ? "Config managed" : "Admin managed"}
              </Tag>
            )}
          />
          <Table.Column<SMPPsUserRow>
            title="Actions"
            render={(_, r) => (
              <Space>
                <Tooltip title="Close all active binds">
                  <Button
                    size="small"
                    icon={<DisconnectOutlined />}
                    onClick={() => void unbind(r)}
                  >
                    Unbind
                  </Button>
                </Tooltip>
                {r.managed_by === "config" ? (
                  <ReadOnlyCell kind="SMPPs bind account" name={r.system_id} record={r as unknown as Record<string, unknown>} />
                ) : (
                  <>
                    <Tooltip title="Disable the account and close active binds">
                      <Button
                        danger
                        size="small"
                        icon={<StopOutlined />}
                        disabled={r.disabled}
                        onClick={() => ban(r)}
                      >
                        Ban
                      </Button>
                    </Tooltip>
                    <Tooltip title="Edit bind account">
                      <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                    </Tooltip>
                    <Tooltip title="Delete bind account">
                      <DeleteButton hideText size="small" recordItemId={r.id} />
                    </Tooltip>
                  </>
                )}
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={500}>
        <Create title="Create SMPP bind account" saveButtonProps={createSaveButtonProps}>
          <SMPPsUserFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={500}>
        <Edit title="Edit SMPP bind account" saveButtonProps={editSaveButtonProps}>
          <SMPPsUserFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
    </div>
  );
};
