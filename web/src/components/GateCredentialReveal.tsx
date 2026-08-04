import { useState } from "react";
import { Alert, Button, Drawer, Input, Space, Typography } from "antd";
import { KeyOutlined } from "@ant-design/icons";

import { CopyButton, Snippet } from "./OperatorUI";

/** IssuedGateCredential is a freshly minted per-user DLR registry credential. */
export type IssuedGateCredential = {
  username: string;
  keyID: string;
  token: string;
  notice?: string;
};

const HOST_PLACEHOLDER = "<gateway-host>";
const EXAMPLE_MSISDN = "380930242105";

/**
 * GateCredentialReveal shows a minted registry credential exactly once, as a
 * request you can run rather than as two strings to assemble.
 *
 * The complete example — real key id, real token — can only be produced here:
 * the gateway stores a SHA-256 proof, so after this drawer closes nothing can
 * reconstruct it. That is also why it closes only on the explicit button, with
 * no mask-click or Escape dismissal to lose it by accident.
 *
 * The host is the one part the console genuinely does not know. It is served on
 * its own listener, usually loopback, so its own origin is the wrong answer and
 * guessing would produce an example that fails in a way nobody would suspect.
 * The operator types it and every snippet updates.
 */
export const GateCredentialReveal = ({
  credential,
  onClose,
}: {
  credential?: IssuedGateCredential;
  onClose: () => void;
}) => {
  const [host, setHost] = useState("");

  const authority = host.trim() || HOST_PLACEHOLDER;
  const url = `https://${authority}/dlr-registry/${credential?.keyID ?? ""}`;
  const token = credential?.token ?? "";

  const openWindow = [
    `curl -X POST ${url} \\`,
    `  -H 'Authorization: Bearer ${token}' \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -d '{"msisdn": "${EXAMPLE_MSISDN}", "ttl_seconds": 900}'`,
  ].join("\n");

  const listWindows = `curl ${url} -H 'Authorization: Bearer ${token}'`;

  const closeWindow = `curl -X DELETE ${url}/${EXAMPLE_MSISDN} -H 'Authorization: Bearer ${token}'`;

  const everything = [
    `# DLR registry credential for ${credential?.username ?? ""}`,
    `# Open a window (the number is then reported as delivered for 15 minutes at most)`,
    openWindow,
    "",
    "# List this user's open windows",
    listWindows,
    "",
    "# Close one early",
    closeWindow,
  ].join("\n");

  return (
    <Drawer
      open={Boolean(credential)}
      onClose={onClose}
      width={640}
      rootClassName="plain-drawer"
      closable={false}
      maskClosable={false}
      keyboard={false}
      title={
        <Space>
          <KeyOutlined /> DLR registry credential for {credential?.username}
        </Space>
      }
      footer={
        <Button type="primary" block onClick={onClose}>
          I have copied it
        </Button>
      }
    >
      <Alert
        type="warning"
        showIcon
        style={{ marginBottom: 16 }}
        message="Shown once"
        description={
          credential?.notice ??
          "This token cannot be retrieved again. Rotate the credential to issue a new one."
        }
      />

      <Typography.Paragraph type="secondary" style={{ marginBottom: 6 }}>
        Gateway host — the public address partners already use to reach this gateway. The registry
        is served on the same listener as <code>/send</code>, not on this console's port.
      </Typography.Paragraph>
      <Input
        value={host}
        onChange={(event) => setHost(event.target.value)}
        placeholder="sms.example.com"
        allowClear
        style={{ marginBottom: 16 }}
      />

      <Typography.Title level={5} style={{ marginTop: 0 }}>
        Open a window
      </Typography.Title>
      <Snippet command={openWindow} />
      <Space size="small" wrap style={{ marginBottom: 16 }}>
        <CopyButton text={openWindow} label="Copy request" />
        <CopyButton text={everything} label="Copy all three" />
        <CopyButton text={token} label="Copy token only" />
      </Space>

      <Typography.Title level={5}>List open windows</Typography.Title>
      <Snippet command={listWindows} />

      <Typography.Title level={5}>Close one early</Typography.Title>
      <Snippet command={closeWindow} />

      <Typography.Paragraph type="secondary" style={{ marginTop: 16, marginBottom: 0 }}>
        <code>ttl_seconds</code> is capped at 900 (15 minutes) — a larger value is clamped, not
        rejected. Windows opened with this token count for <strong>{credential?.username}</strong>{" "}
        only. The token is scoped to this endpoint: it cannot submit traffic, read messages, or
        administer the gateway.
      </Typography.Paragraph>
    </Drawer>
  );
};
