import { useInvalidate } from "@refinedev/core";
import { ClearOutlined } from "@ant-design/icons";
import { App, Button, Modal } from "antd";

import { API_URL, httpClient } from "../httpClient";

export const FlushButton = ({
  endpoint,
  resource,
  label,
  description,
}: {
  endpoint: string;
  resource: string;
  label: string;
  description: string;
}) => {
  const { message } = App.useApp();
  const invalidate = useInvalidate();

  const confirmFlush = () => {
    Modal.confirm({
      title: `${label}?`,
      content: description,
      okText: label,
      okButtonProps: { danger: true },
      async onOk() {
        const response = await httpClient.post(`${API_URL}${endpoint}`);
        await invalidate({ resource, invalidates: ["list", "many", "detail"] });
        const deleted = Number(response.data?.deleted ?? 0);
        void message.success(`Removed ${deleted} admin-managed ${deleted === 1 ? "rule" : "rules"}`);
      },
    });
  };

  return (
    <Button danger icon={<ClearOutlined />} onClick={confirmFlush}>
      {label}
    </Button>
  );
};
