import { useCustom } from "@refinedev/core";
import { Card, Col, Row, Tag, Typography, Button } from "antd";
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
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col span={24}>
          <Card
            title="Gateway health"
            extra={
              <Button type="primary" icon={<ReloadOutlined />} loading={isFetching} onClick={() => refetch()}>
                Refresh
              </Button>
            }
          >
            <Typography.Paragraph style={{ fontSize: 16 }}>
              Overall status:{" "}
              <Tag color={statusColor(health?.status ?? "")} style={{ fontSize: 16, padding: '4px 8px' }}>
                {health?.status?.toUpperCase() ?? "LOADING…"}
              </Tag>
            </Typography.Paragraph>
          </Card>
        </Col>
      </Row>
      <Row gutter={[16, 16]}>
        {rows.map((row) => (
          <Col xs={24} sm={12} md={8} lg={6} key={row.name}>
            <Card hoverable className="glass-card">
              <Typography.Text type="secondary">{row.name}</Typography.Text>
              <div style={{ marginTop: 8, fontSize: 24, fontWeight: 600, color: checkColor(row.detail) }}>
                {row.detail}
              </div>
            </Card>
          </Col>
        ))}
      </Row>
    </div>
  );
};
