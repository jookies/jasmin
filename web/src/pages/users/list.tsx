import { ReadOnlyCell } from "../../components/ConfigDetail";
import { IntegrationGuideButton } from "../../components/IntegrationGuide";
import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { useState } from "react";
import { Table, Space, Drawer, Tag, Tooltip } from "antd";
import { UserFields } from "./form";
import { PageTitle, StatusBadge, TableScrollHint } from "../../components/OperatorUI";
import { GateCredentialReveal } from "../../components/GateCredentialReveal";
import type { IssuedGateCredential } from "../../components/GateCredentialReveal";

type UserRow = {
  id: string;
  username: string;
  uid: number;
  external_id?: string;
  group_id?: string;
  disabled?: boolean;
  balance: number | null;
  submit_sm_count: number | null;
  early_decrement_balance_percent: number | null;
  managed_by: "admin" | "config";
};

const orUnlimited = (v: number | null | undefined) => (v === null || v === undefined ? "∞" : v);

/**
 * captureIssuedCredential picks the one-time token out of a save response.
 *
 * The token exists only in the response that created it — nothing can reprint
 * it, because the gateway stores a SHA-256 proof — so it is caught here on both
 * the create and the edit path, since a rotation happens on edit.
 */
const captureIssuedCredential = (payload: unknown): IssuedGateCredential | undefined => {
  const row = (payload as { data?: UserRow } | undefined)?.data as
    | (UserRow & {
        dlr_gate_token?: string;
        dlr_gate_key_id?: string;
        dlr_gate_token_notice?: string;
        username?: string;
      })
    | undefined;
  if (!row?.dlr_gate_token) return undefined;
  return {
    username: row.username ?? "",
    keyID: row.dlr_gate_key_id ?? "",
    token: row.dlr_gate_token,
    notice: row.dlr_gate_token_notice,
  };
};

export const UserList = () => {
  const { tableProps } = useTable<UserRow>({ syncWithLocation: true });
  const [issuedGate, setIssuedGate] = useState<IssuedGateCredential | undefined>();

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<UserRow>({
    action: "create",
    syncWithLocation: true,
    onMutationSuccess: (data) => setIssuedGate(captureIssuedCredential(data)),
  });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
  } = useDrawerForm<UserRow>({
    action: "edit",
    syncWithLocation: true,
    onMutationSuccess: (data) => setIssuedGate(captureIssuedCredential(data)),
  });

  return (
    <div className="resource-page">
      <List
        title={
          <PageTitle
            eyebrow="Access and billing"
            title="Gateway users"
            description="Manage submit credentials, identifiers and the quotas enforced by the HTTP and SMPP paths."
          />
        }
        createButtonProps={{ onClick: () => showCreate(), children: "Add gateway user" }}
      >
        <TableScrollHint />
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 980 }}>
          <Table.Column dataIndex="username" title="Username" />
          <Table.Column dataIndex="external_id" title="External ID" render={(v?: string) => v || "—"} />
          <Table.Column dataIndex="group_id" title="Group" render={(v?: string) => v || "—"} />
          <Table.Column
            dataIndex="disabled"
            title="Status"
            render={(disabled: boolean) => (
              <StatusBadge tone={disabled ? "negative" : "positive"}>
                {disabled ? "Disabled" : "Enabled"}
              </StatusBadge>
            )}
          />
          <Table.Column
            dataIndex="balance"
            title="Balance"
            render={(v: number | null | undefined) =>
              v === null || v === undefined ? (
                <StatusBadge tone="neutral">Unlimited</StatusBadge>
              ) : (
                orUnlimited(v)
              )
            }
          />
          <Table.Column
            dataIndex="submit_sm_count"
            title="Submit quota"
            render={(v: number | null | undefined) =>
              v === null || v === undefined ? (
                <StatusBadge tone="neutral">Unlimited</StatusBadge>
              ) : (
                orUnlimited(v)
              )
            }
          />
          <Table.Column
            dataIndex="managed_by"
            title="Source"
            render={(source: UserRow["managed_by"]) => (
              <Tag color={source === "config" ? "blue" : "green"}>
                {source === "config" ? "Config managed" : "Admin managed"}
              </Tag>
            )}
          />
          {/*
            The integration pack is offered for config-owned users too: they can
            send exactly like an admin-managed user, they just cannot be edited
            here, and "how do I connect" is the same question either way.
          */}
          <Table.Column<UserRow>
            title="Actions"
            render={(_, r) => (
              <Space>
                <IntegrationGuideButton kind="account" id={r.id} label="Integration" />
                {r.managed_by === "config" ? (
                  <ReadOnlyCell
                    kind="User"
                    name={r.username}
                    record={r as unknown as Record<string, unknown>}
                  />
                ) : (
                  <>
                    <Tooltip title="Edit user">
                      <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                    </Tooltip>
                    <Tooltip title="Delete user">
                      <DeleteButton hideText size="small" recordItemId={r.id} />
                    </Tooltip>
                  </>
                )}
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...createDrawerProps} width={760}>
        <Create saveButtonProps={createSaveButtonProps}>
          <UserFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={760}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <UserFields formProps={editFormProps} editing />
        </Edit>
      </Drawer>
      <GateCredentialReveal credential={issuedGate} onClose={() => setIssuedGate(undefined)} />
    </div>
  );
};
