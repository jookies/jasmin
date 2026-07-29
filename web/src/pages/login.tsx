import { useLogin } from "@refinedev/core";
import { Button, Form, Input } from "antd";
import {
  ApiOutlined,
  CheckCircleFilled,
  LockOutlined,
  SafetyCertificateOutlined,
  ThunderboltFilled,
  UserOutlined,
} from "@ant-design/icons";

type LoginForm = { username: string; password: string };

export const LoginPage = () => {
  const { mutate: login, isLoading } = useLogin<LoginForm>();

  return (
    <main className="login-shell">
      <section className="login-story" aria-label="Jasmin gateway console">
        <div className="login-brand">
          <span className="brand-mark" aria-hidden="true">
            <ThunderboltFilled />
          </span>
          <span>
            <strong>Jasmin</strong>
            <small>Gateway console</small>
          </span>
        </div>

        <div className="login-story-copy">
          <span className="login-kicker">
            <CheckCircleFilled />
            Operator workspace
          </span>
          <h1>Run messaging infrastructure with confidence.</h1>
          <p>
            One focused workspace for gateway health, SMPP connectivity, routing, access, and
            runtime changes.
          </p>
          <div className="login-capabilities">
            <div className="login-capability">
              <CheckCircleFilled />
              Live gateway and dependency health
            </div>
            <div className="login-capability">
              <ApiOutlined />
              Runtime connector and route management
            </div>
            <div className="login-capability">
              <SafetyCertificateOutlined />
              Session-protected internal access
            </div>
          </div>
        </div>

        <div className="login-story-footer">Jasmin Gateway · Internal operations</div>
      </section>

      <section className="login-form-pane">
        <div className="login-form-wrap">
          <span className="login-access-label">
            <LockOutlined />
            Restricted access
          </span>
          <h2>Welcome back</h2>
          <p>Sign in with your operator credentials to continue to the gateway console.</p>

          <Form<LoginForm>
            layout="vertical"
            onFinish={(values) => login(values)}
            requiredMark={false}
          >
            <Form.Item
              label="Username"
              name="username"
              rules={[{ required: true, message: "Enter your username" }]}
            >
              <Input
                prefix={<UserOutlined />}
                autoComplete="username"
                autoFocus
                placeholder="Operator username"
              />
            </Form.Item>
            <Form.Item
              label="Password"
              name="password"
              rules={[{ required: true, message: "Enter your password" }]}
            >
              <Input.Password
                prefix={<LockOutlined />}
                autoComplete="current-password"
                placeholder="Password"
              />
            </Form.Item>
            <Button
              className="login-submit"
              type="primary"
              htmlType="submit"
              loading={isLoading}
              block
            >
              Sign in to console
            </Button>
          </Form>

          <div className="login-security-note">
            <SafetyCertificateOutlined />
            <span>
              This console changes live gateway configuration. Access should remain restricted to
              trusted operators on an internal network.
            </span>
          </div>
        </div>
      </section>
    </main>
  );
};
