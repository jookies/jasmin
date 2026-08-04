import type { ReactNode } from "react";
import { App, Button, Spin } from "antd";
import {
  CheckCircleFilled,
  CopyOutlined,
  ClockCircleFilled,
  ExclamationCircleFilled,
  MinusCircleFilled,
  SwapOutlined,
} from "@ant-design/icons";

type PageTitleProps = {
  eyebrow: string;
  title: string;
  description: string;
};

export const PageTitle = ({ eyebrow, title, description }: PageTitleProps) => (
  <div className="page-title">
    <span className="page-eyebrow">{eyebrow}</span>
    <h1>{title}</h1>
    <p>{description}</p>
  </div>
);

export type StatusTone = "positive" | "warning" | "negative" | "neutral" | "progress";

const statusIcons: Record<StatusTone, ReactNode> = {
  positive: <CheckCircleFilled />,
  warning: <ExclamationCircleFilled />,
  negative: <ExclamationCircleFilled />,
  neutral: <MinusCircleFilled />,
  progress: <ClockCircleFilled />,
};

export const StatusBadge = ({
  tone,
  children,
}: {
  tone: StatusTone;
  children: ReactNode;
}) => (
  <span className={`status-badge is-${tone}`}>
    {statusIcons[tone]}
    <span>{children}</span>
  </span>
);

export const FormIntroduction = ({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) => (
  <div className="form-introduction">
    <strong>{title}</strong>
    <p>{children}</p>
  </div>
);

export const TableScrollHint = () => (
  <div className="mobile-table-hint">
    <SwapOutlined />
    Swipe horizontally to see status and actions
  </div>
);

export const PageLoading = () => (
  <div className="page-loading" role="status" aria-label="Loading page">
    <Spin />
    <span>Loading workspace…</span>
  </div>
);

export const EffectiveValue = ({
  label = "When left empty",
  value,
  detail,
}: {
  label?: string;
  value: ReactNode;
  detail?: ReactNode;
}) => (
  <span className="effective-value">
    <span>{label}</span>
    <strong>{value}</strong>
    {detail ? <span className="effective-value-detail">· {detail}</span> : null}
  </span>
);

/**
 * copyText prefers the async clipboard API and falls back to the legacy
 * selection copy. The console is normally served over plain HTTP on a loopback
 * listener, and outside a secure context navigator.clipboard does not exist —
 * without the fallback the copy button would silently do nothing exactly where
 * it is used most.
 */
export const copyText = async (text: string): Promise<boolean> => {
  try {
    if (window.isSecureContext && navigator.clipboard) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through to the selection copy below.
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.top = "-1000px";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch {
    copied = false;
  }
  document.body.removeChild(area);
  return copied;
};

export const CopyButton = ({ text, label = "Copy" }: { text: string; label?: string }) => {
  // App.useApp() rather than the static antd message export, so the feedback
  // inherits the console's ConfigProvider theme instead of rendering unstyled.
  const { message } = App.useApp();
  return (
    <Button
      size="small"
      icon={<CopyOutlined />}
      onClick={async () => {
        const copied = await copyText(text);
        if (copied) {
          message.success("Copied");
        } else {
          message.warning("Could not copy automatically — select the text and copy it manually");
        }
      }}
    >
      {label}
    </Button>
  );
};

export const Snippet = ({ command }: { command: string }) => (
  <pre className="integration-snippet">{command}</pre>
);
