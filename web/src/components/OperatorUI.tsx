import type { ReactNode } from "react";
import { Spin } from "antd";
import {
  CheckCircleFilled,
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
