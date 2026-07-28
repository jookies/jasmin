import { useLogin } from "@refinedev/core";
import { Button, Card, Form, Input, Layout, Typography } from "antd";
import { LockOutlined, UserOutlined } from "@ant-design/icons";

type LoginForm = { username: string; password: string };

// The gateway's admin credential is a username, not an email, so this replaces
// Refine's stock AuthPage (which renders an email field with email-format
// validation and would post {email, password} to /api/login).
export const LoginPage = () => {
  const { mutate: login, isLoading } = useLogin<LoginForm>();

  return (
    <Layout
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "transparent",
      }}
    >
      <Card 
        className="page-container" 
        style={{ width: 380, padding: '24px 8px', borderRadius: '16px' }}
      >
        <Typography.Title level={2} style={{ textAlign: "center", marginBottom: 32, fontWeight: 700 }}>
          Jasmin Admin
        </Typography.Title>
        <Form<LoginForm> layout="vertical" onFinish={(values) => login(values)} requiredMark={false}>
          <Form.Item
            label="Username"
            name="username"
            rules={[{ required: true, message: "Username is required" }]}
          >
            <Input prefix={<UserOutlined />} autoComplete="username" autoFocus size="large" />
          </Form.Item>
          <Form.Item
            label="Password"
            name="password"
            rules={[{ required: true, message: "Password is required" }]}
          >
            <Input.Password
              prefix={<LockOutlined />}
              autoComplete="current-password"
              size="large"
            />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={isLoading} size="large" block>
            Sign in
          </Button>
        </Form>
      </Card>
    </Layout>
  );
};
