import {
  Create,
  DeleteButton,
  Edit,
  EditButton,
  List,
  useDrawerForm,
  useTable,
} from "@refinedev/antd";
import { Drawer, Form, Input, Select, Space, Table, Tag, Tooltip } from "antd";
import type { FormProps } from "antd";

import { FormIntroduction, PageTitle } from "../../components/OperatorUI";

type HTTPConnectorRow = {
  id: string;
  cid: string;
  baseurl: string;
  method: "GET" | "POST";
};

const HTTPConnectorFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical" className="operator-form">
    <FormIntroduction title={editing ? "Update HTTP destination" : "Create an HTTP destination"}>
      Use this destination when creating MO routes. Routes receive a copy, so existing routes stay
      stable if this template changes later.
    </FormIntroduction>
    <Form.Item
      label="Connector ID"
      name="cid"
      rules={[
        { required: true },
        { pattern: /^[A-Za-z0-9_-]{3,25}$/, message: "Use 3–25 letters, numbers, _ or -" },
      ]}
    >
      <Input disabled={editing} placeholder="support-api" />
    </Form.Item>
    <Form.Item
      label="Base URL"
      name="baseurl"
      rules={[
        { required: true },
        { type: "url", message: "Enter an absolute HTTP or HTTPS URL" },
      ]}
    >
      <Input placeholder="https://app.example.com/messages/mo" />
    </Form.Item>
    <Form.Item label="Method" name="method" initialValue="GET" rules={[{ required: true }]}>
      <Select
        options={[
          { value: "GET", label: "GET" },
          { value: "POST", label: "POST" },
        ]}
      />
    </Form.Item>
  </Form>
);

export const HTTPConnectorList = () => {
  const { tableProps } = useTable<HTTPConnectorRow>({ syncWithLocation: true });
  const create = useDrawerForm<HTTPConnectorRow>({ action: "create", syncWithLocation: true });
  const edit = useDrawerForm<HTTPConnectorRow>({ action: "edit", syncWithLocation: true });

  return (
    <div className="resource-page">
      <List
        title={
          <PageTitle
            eyebrow="Delivery library"
            title="HTTP destinations"
            description="Save reusable MO callback destinations and keep route creation fast and consistent."
          />
        }
        createButtonProps={{ onClick: () => create.show(), children: "Add destination" }}
      >
        <Table {...tableProps} rowKey="id" size="small" scroll={{ x: 760 }}>
          <Table.Column dataIndex="cid" title="Destination" />
          <Table.Column dataIndex="method" title="Method" render={(method: string) => <Tag>{method}</Tag>} />
          <Table.Column dataIndex="baseurl" title="URL" ellipsis />
          <Table.Column<HTTPConnectorRow>
            title="Actions"
            render={(_, row) => (
              <Space>
                <Tooltip title="Edit HTTP destination">
                  <EditButton hideText size="small" onClick={() => edit.show(row.id)} />
                </Tooltip>
                <Tooltip title="Delete HTTP destination">
                  <DeleteButton hideText size="small" recordItemId={row.id} />
                </Tooltip>
              </Space>
            )}
          />
        </Table>
      </List>
      <Drawer {...create.drawerProps} width={540}>
        <Create saveButtonProps={create.saveButtonProps}>
          <HTTPConnectorFields formProps={create.formProps} />
        </Create>
      </Drawer>
      <Drawer {...edit.drawerProps} width={540}>
        <Edit saveButtonProps={edit.saveButtonProps}>
          <HTTPConnectorFields formProps={edit.formProps} editing />
        </Edit>
      </Drawer>
    </div>
  );
};
