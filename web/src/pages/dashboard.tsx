import { useCustom } from "@refinedev/core";
import { Card, Col, Row, Table, Tag, Typography, Button } from "antd";
import { ReloadOutlined } from "@ant-design/icons";

type Health = { status: string; checks: Record<string, string> };

const statusColor = (status: string) =>
  status === "ok" ? "green" : status === "degraded" ? "red" : "orange";

const checkColor = (detail: string) => (detail === "ok" || detail === "bound" ? "green" : "red");

// Dashboard renders the shared gateway readiness probe (/api/health mirrors
// the public /health checks: postgres, amqp, codec, required connectors).
export const DashboardPage = () => {
  const { data, isFetching, refetch } = useCustom<Health>({
    url: "/api/health",
    method: "get",
  });
  const health = data?.data;
  const rows = Object.entries(health?.checks ?? {}).map(([name, detail]) => ({
    key: name,
    name,
    detail,
  }));
  return (
    <div className="page-container">
      <Row gutter={[16, 16]}>
        <Col span={24}>
          <Card
            title="Gateway health"
            extra={
              <Button type="primary" icon={<ReloadOutlined />} loading={isFetching} onClick={() => refetch()}>
                Refresh
              </Button>
            }
          >
            <Typography.Paragraph>
              Overall status:{" "}
              <Tag color={statusColor(health?.status ?? "")}>{health?.status ?? "loading…"}</Tag>
            </Typography.Paragraph>
            <Table
              size="small"
              pagination={false}
              dataSource={rows}
              columns={[
                { title: "Check", dataIndex: "name" },
                {
                  title: "Detail",
                  dataIndex: "detail",
                  render: (detail: string) => <Tag color={checkColor(detail)}>{detail}</Tag>,
                },
              ]}
            />
          </Card>
        </Col>
      </Row>
    </div>
  );
};
