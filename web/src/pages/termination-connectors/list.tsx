import { ReadOnlyCell } from "../../components/ConfigDetail";
import { List, useTable, EditButton, DeleteButton, useDrawerForm, Create, Edit } from "@refinedev/antd";
import { useUpdate } from "@refinedev/core";
import { Button, Drawer, Space, Table, Tag, Tooltip } from "antd";
import { PlayCircleOutlined, PauseCircleOutlined } from "@ant-design/icons";
import { TerminationConnectorFields } from "./form";
import {
  PageTitle,
  StatusBadge,
  TableScrollHint,
  type StatusTone,
} from "../../components/OperatorUI";

type TerminationConnectorRow = {
  id: string;
  cid: string;
  verdict?: {
    source?: string;
    key_prefix?: string;
    cache_ttl?: number;
    lookup_timeout?: number;
  };
  delivery?: {
    endpoint?: string;
    format?: string;
    secret?: string;
    timeout?: number;
    max_attempts?: number;
    backoff?: number;
    backoff_cap?: number;
  };
  receipt_delay?: number;
  receipt_jitter?: number;
  synchronous_reject?: boolean;
  desired_started: boolean;
  // STOPPED | CONNECTING | CONSUMING (internal/core/termination.Status) — never
  // BOUND: there is no bind here, only whether this process is consuming the
  // connector's submit queue.
  observed?: string;
  has_secret?: boolean;
  managed_by?: "admin" | "config";
};

const verdictTone = (source?: string): StatusTone => {
  if (source === "redis-window") return "positive";
  if (source === "static") return "neutral";
  return "warning";
};

const observedTone = (observed?: string): StatusTone => {
  if (observed === "CONSUMING") return "positive";
  if (observed === "CONNECTING") return "progress";
  if (observed === "STOPPED") return "neutral";
  return "negative";
};

export const TerminationConnectorList = () => {
  const { tableProps } = useTable<TerminationConnectorRow>({ syncWithLocation: true });
  const { mutate: update, isLoading: toggling } = useUpdate();

  const {
    drawerProps: createDrawerProps,
    formProps: createFormProps,
    saveButtonProps: createSaveButtonProps,
    show: showCreate,
  } = useDrawerForm<TerminationConnectorRow>({ action: "create", syncWithLocation: true });

  const {
    drawerProps: editDrawerProps,
    formProps: editFormProps,
    saveButtonProps: editSaveButtonProps,
    show: showEdit,
    query: editQuery,
  } = useDrawerForm<TerminationConnectorRow>({ action: "edit", syncWithLocation: true });

  const editingRecord = editQuery?.data?.data;

  const toggleStarted = (row: TerminationConnectorRow) =>
    update({
      resource: "termination-connectors",
      id: row.id,
      values: { desired_started: !row.desired_started },
    });

  return (
    <div className="resource-page">
      <List
        title={
          <PageTitle
            eyebrow="Messaging"
            title="Termination connectors"
            description="MT traffic routed here stops on this gateway: decoded, verdicted, spooled and delivered to your application instead of an upstream SMSC."
          />
        }
        createButtonProps={{ onClick: () => showCreate(), children: "Add termination connector" }}
      >
        <TableScrollHint />
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 1180 }}>
          <Table.Column dataIndex="cid" title="ID" />
          <Table.Column<TerminationConnectorRow>
            title="Verdict source"
            render={(_, r) => (
              <StatusBadge tone={verdictTone(r.verdict?.source)}>
                {r.verdict?.source || "Unset"}
              </StatusBadge>
            )}
          />
          <Table.Column<TerminationConnectorRow>
            title="Delivery"
            render={(_, r) =>
              r.delivery?.endpoint ? (
                <Tooltip title={r.delivery.endpoint}>
                  <span>{r.delivery.format === "legacy" ? "Legacy push" : "JSON push"}</span>
                </Tooltip>
              ) : (
                <Tag>Pull-only</Tag>
              )
            }
          />
          <Table.Column<TerminationConnectorRow>
            title="Signing secret"
            render={(_, r) =>
              r.has_secret ? (
                <StatusBadge tone="positive">Configured</StatusBadge>
              ) : (
                <StatusBadge tone="neutral">Not configured</StatusBadge>
              )
            }
          />
          <Table.Column
            dataIndex="desired_started"
            title="Desired"
            render={(v: boolean) => (
              <StatusBadge tone={v ? "progress" : "neutral"}>{v ? "Started" : "Stopped"}</StatusBadge>
            )}
          />
          <Table.Column
            dataIndex="observed"
            title="Status"
            render={(v: string) => <StatusBadge tone={observedTone(v)}>{v || "Unknown"}</StatusBadge>}
          />
          <Table.Column
            dataIndex="managed_by"
            title="Source"
            render={(source: TerminationConnectorRow["managed_by"]) => (
              <Tag color={source === "config" ? "blue" : "green"}>
                {source === "config" ? "Config managed" : "Admin managed"}
              </Tag>
            )}
          />
          <Table.Column<TerminationConnectorRow>
            title="Actions"
            render={(_, r) =>
              r.managed_by === "config" ? (
                <ReadOnlyCell
                  kind="Termination connector"
                  name={r.cid}
                  record={r as unknown as Record<string, unknown>}
                />
              ) : (
                <Space>
                  <Tooltip title={r.desired_started ? "Stop connector" : "Start connector"}>
                    <Button
                      size="small"
                      loading={toggling}
                      icon={r.desired_started ? <PauseCircleOutlined /> : <PlayCircleOutlined />}
                      onClick={() => toggleStarted(r)}
                    >
                      {r.desired_started ? "Stop" : "Start"}
                    </Button>
                  </Tooltip>
                  <Tooltip title="Edit termination connector">
                    <EditButton hideText size="small" onClick={() => showEdit(r.id)} />
                  </Tooltip>
                  <Tooltip title="Delete termination connector">
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
          <TerminationConnectorFields formProps={createFormProps} />
        </Create>
      </Drawer>
      <Drawer {...editDrawerProps} width={760}>
        <Edit saveButtonProps={editSaveButtonProps}>
          <TerminationConnectorFields
            key={editingRecord?.cid ?? "loading"}
            formProps={editFormProps}
            editing
            secretConfigured={Boolean(editingRecord?.has_secret)}
          />
        </Edit>
      </Drawer>
    </div>
  );
};
