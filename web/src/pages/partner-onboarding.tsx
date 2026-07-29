import { useMemo, useState } from "react";
import {
  Alert,
  Button,
  Descriptions,
  Divider,
  Form,
  Input,
  InputNumber,
  Radio,
  Result,
  Select,
  Steps,
  Switch,
  Tag,
} from "antd";
import {
  ApartmentOutlined,
  ArrowLeftOutlined,
  ArrowRightOutlined,
  CheckCircleFilled,
  CloudServerOutlined,
  GlobalOutlined,
  LockOutlined,
  SafetyCertificateOutlined,
  SendOutlined,
  SwapOutlined,
  TeamOutlined,
} from "@ant-design/icons";

import { PageTitle } from "../components/OperatorUI";

type ConnectionDirection = "inbound" | "outbound" | "bidirectional";
type ContactChannel =
  | "email"
  | "telegram"
  | "teams"
  | "phone"
  | "other"
  | "not_assigned";

type PartnerPlan = {
  partnerName: string;
  partnerCode: string;
  contactChannel: ContactChannel;
  contactValue?: string;
  direction: ConnectionDirection;
  inboundSystemID: string;
  inboundBindMode: "transceiver" | "transmitter" | "receiver";
  inboundAllowlist: string;
  inboundTLS: boolean;
  outboundHost: string;
  outboundPort: number;
  outboundSystemID: string;
  outboundBindMode: "transceiver" | "transmitter" | "receiver";
  outboundTLS: boolean;
  prefixes: string;
  throughput: number;
  dlrMode: "final" | "all" | "none";
  moURL?: string;
};

const stepItems = [
  { title: "Partner", description: "Identity" },
  { title: "Connection", description: "SMPP direction" },
  { title: "Traffic", description: "Policy" },
  { title: "Review", description: "Mock plan" },
];

const initialValues: PartnerPlan = {
  partnerName: "",
  partnerCode: "",
  contactChannel: "email",
  contactValue: "",
  direction: "inbound",
  inboundSystemID: "",
  inboundBindMode: "transceiver",
  inboundAllowlist: "",
  inboundTLS: true,
  outboundHost: "",
  outboundPort: 2775,
  outboundSystemID: "",
  outboundBindMode: "transceiver",
  outboundTLS: true,
  prefixes: "",
  throughput: 25,
  dlrMode: "final",
  moURL: "",
};

const bindModeOptions = [
  { value: "transceiver", label: "Transceiver (send + receive)" },
  { value: "transmitter", label: "Transmitter only" },
  { value: "receiver", label: "Receiver only" },
];

const contactChannelOptions = [
  { value: "email", label: "Email" },
  { value: "telegram", label: "Telegram" },
  { value: "teams", label: "Microsoft Teams" },
  { value: "phone", label: "Phone" },
  { value: "other", label: "Other" },
  { value: "not_assigned", label: "Not assigned yet" },
];

const contactPlaceholders: Record<Exclude<ContactChannel, "not_assigned">, string> = {
  email: "noc@partner.example",
  telegram: "@partner_noc or t.me/partner_noc",
  teams: "Teams email, chat link or display name",
  phone: "+44 20 7946 0958",
  other: "Contact name, handle or channel",
};

const contactLabels: Record<ContactChannel, string> = {
  email: "Email",
  telegram: "Telegram",
  teams: "Microsoft Teams",
  phone: "Phone",
  other: "Other",
  not_assigned: "Not assigned yet",
};

const includesInbound = (direction: ConnectionDirection) =>
  direction === "inbound" || direction === "bidirectional";

const includesOutbound = (direction: ConnectionDirection) =>
  direction === "outbound" || direction === "bidirectional";

export const PartnerOnboardingPage = () => {
  const [form] = Form.useForm<PartnerPlan>();
  const [currentStep, setCurrentStep] = useState(0);
  const [completed, setCompleted] = useState(false);
  const [reviewValues, setReviewValues] = useState<PartnerPlan>(initialValues);
  const watchedDirection = Form.useWatch("direction", form);
  const watchedContactChannel = Form.useWatch("contactChannel", form);
  const direction =
    currentStep >= 3
      ? reviewValues.direction
      : (watchedDirection ?? initialValues.direction);
  const contactChannel =
    currentStep >= 3
      ? reviewValues.contactChannel
      : (watchedContactChannel ?? initialValues.contactChannel);

  const futureResources = useMemo(() => {
    const resources = ["Partner record", "Audit-ready onboarding run"];
    if (includesInbound(direction)) {
      resources.push("Group and SMPPs bind account", "Inbound permissions and limits");
    }
    if (includesOutbound(direction)) {
      resources.push("SMPPc connector", "MT/MO routes and filters");
    }
    return resources;
  }, [direction]);

  const fieldsForStep = (): (keyof PartnerPlan)[] => {
    if (currentStep === 0) {
      const fields: (keyof PartnerPlan)[] = [
        "partnerName",
        "partnerCode",
        "contactChannel",
      ];
      if (contactChannel !== "not_assigned") fields.push("contactValue");
      return fields;
    }
    if (currentStep === 1) {
      const fields: (keyof PartnerPlan)[] = ["direction"];
      if (includesInbound(direction)) {
        fields.push("inboundSystemID", "inboundBindMode", "inboundAllowlist", "inboundTLS");
      }
      if (includesOutbound(direction)) {
        fields.push(
          "outboundHost",
          "outboundPort",
          "outboundSystemID",
          "outboundBindMode",
          "outboundTLS",
        );
      }
      return fields;
    }
    if (currentStep === 2) return ["prefixes", "throughput", "dlrMode", "moURL"];
    return [];
  };

  const goForward = async () => {
    try {
      await form.validateFields(fieldsForStep());
      if (currentStep === 2) {
        setReviewValues(form.getFieldsValue(true));
      }
      setCurrentStep((step) => Math.min(step + 1, stepItems.length - 1));
    } catch {
      // Ant Design renders field-level errors and keeps the operator on this step.
    }
  };

  const resetPrototype = () => {
    form.resetFields();
    setReviewValues(initialValues);
    setCurrentStep(0);
    setCompleted(false);
  };

  const editPlan = () => {
    setCompleted(false);
    setCurrentStep(0);
  };

  if (completed) {
    return (
      <div className="page-container partner-onboarding-page">
        <section className="partner-complete-panel">
          <Result
            status="success"
            title="Mock onboarding plan is ready"
            subTitle={`A frontend-only plan for ${reviewValues.partnerName}. No gateway configuration was changed.`}
            extra={[
              <Button key="edit" onClick={editPlan}>
                Edit plan
              </Button>,
              <Button key="reset" type="primary" onClick={resetPrototype}>
                Start another mock
              </Button>,
            ]}
          />
          <div className="prototype-safety-note">
            <LockOutlined aria-hidden="true" />
            <span>
              <strong>Nothing was saved or sent.</strong>
              This prototype has no onboarding API and keeps the plan only in this browser tab.
            </span>
          </div>
        </section>
      </div>
    );
  }

  return (
    <div className="page-container partner-onboarding-page">
      <section className="partner-onboarding-hero">
        <div>
          <PageTitle
            eyebrow="Access · Frontend prototype"
            title="Onboard a partner"
            description="Prepare one clear SMPP connection plan, whether a partner connects to us, we connect to them, or both."
          />
          <div className="partner-hero-tags" aria-label="Prototype properties">
            <Tag color="cyan">4 guided steps</Tag>
            <Tag>In-memory only</Tag>
            <Tag>No API calls</Tag>
          </div>
        </div>
        <div className="partner-hero-mark" aria-hidden="true">
          <TeamOutlined />
        </div>
      </section>

      <Alert
        className="prototype-alert"
        type="warning"
        showIcon
        message="Prototype only — nothing on this screen creates credentials, routes, users or connectors."
        description="Use it to validate the workflow and information architecture while production onboarding remains in the backlog."
      />

      <section className="partner-wizard-shell">
        <aside className="partner-wizard-sidebar" aria-label="Onboarding progress">
          <span className="wizard-sidebar-kicker">Setup progress</span>
          <Steps direction="vertical" current={currentStep} items={stepItems} />
          <div className="wizard-safety-card">
            <SafetyCertificateOutlined aria-hidden="true" />
            <span>
              <strong>Safe to explore</strong>
              Values disappear when this tab is refreshed.
            </span>
          </div>
        </aside>

        <main className="partner-wizard-content">
          <Form
            form={form}
            layout="vertical"
            initialValues={initialValues}
            className="operator-form partner-wizard-form"
            onFinish={() => setCompleted(true)}
          >
            <div className="wizard-step-heading">
              <span>
                Step {currentStep + 1} of {stepItems.length}
              </span>
              <h2>{stepItems[currentStep].title}</h2>
              <p>
                {currentStep === 0 &&
                  "Identify who we are connecting and who owns the technical handoff."}
                {currentStep === 1 &&
                  "Choose the traffic direction and collect only the endpoint details it needs."}
                {currentStep === 2 &&
                  "Describe the guardrails that should be approved before live traffic."}
                {currentStep === 3 &&
                  "Review the future resource bundle. This button still creates only local mock state."}
              </p>
            </div>

            {currentStep === 0 && (
              <div className="wizard-form-section">
                <div className="form-grid-two">
                  <Form.Item
                    label="Partner name"
                    name="partnerName"
                    rules={[{ required: true, message: "Enter the partner’s display name" }]}
                  >
                    <Input placeholder="Acme Messaging" autoComplete="organization" />
                  </Form.Item>
                  <Form.Item
                    label="Stable partner code"
                    name="partnerCode"
                    extra="Lowercase letters, numbers and hyphens. A future backend would use this as an idempotent key."
                    rules={[
                      { required: true, message: "Enter a stable partner code" },
                      {
                        pattern: /^[a-z0-9]+(?:-[a-z0-9]+)*$/,
                        message: "Use lowercase letters, numbers and single hyphens",
                      },
                    ]}
                  >
                    <Input placeholder="acme-messaging" autoComplete="off" />
                  </Form.Item>
                </div>
                <div className="contact-grid">
                  <Form.Item label="Contact channel" name="contactChannel">
                    <Select options={contactChannelOptions} />
                  </Form.Item>
                  {contactChannel !== "not_assigned" && (
                    <Form.Item
                      label="Technical contact"
                      name="contactValue"
                      extra="A shared NOC or support contact is preferable to a personal account."
                      rules={
                        contactChannel === "email"
                          ? [
                              { required: true, message: "Enter a technical contact" },
                              { type: "email", message: "Enter a valid email address" },
                            ]
                          : [{ required: true, message: "Enter a technical contact" }]
                      }
                    >
                      <Input
                        placeholder={contactPlaceholders[contactChannel]}
                        type={contactChannel === "email" ? "email" : "text"}
                        autoComplete={
                          contactChannel === "email"
                            ? "email"
                            : contactChannel === "phone"
                              ? "tel"
                              : "off"
                        }
                      />
                    </Form.Item>
                  )}
                </div>
                <div className="wizard-context-card">
                  <ApartmentOutlined aria-hidden="true" />
                  <span>
                    <strong>One partner, one ownership boundary</strong>
                    The production workflow will link every account, connector and route back to this
                    partner.
                  </span>
                </div>
              </div>
            )}

            {currentStep === 1 && (
              <div className="wizard-form-section">
                <Form.Item
                  label="How will the SMPP connection work?"
                  name="direction"
                  rules={[{ required: true }]}
                >
                  <Radio.Group className="wizard-direction-grid">
                    <Radio.Button value="inbound">
                      <span className="direction-choice-icon">
                        <SendOutlined />
                      </span>
                      <strong>Partner connects to us</strong>
                      <small>We expose SMPPs credentials and approve their source IPs.</small>
                    </Radio.Button>
                    <Radio.Button value="outbound">
                      <span className="direction-choice-icon">
                        <CloudServerOutlined />
                      </span>
                      <strong>We connect to partner</strong>
                      <small>We bind an SMPPc connector to their SMSC endpoint.</small>
                    </Radio.Button>
                    <Radio.Button value="bidirectional">
                      <span className="direction-choice-icon">
                        <SwapOutlined />
                      </span>
                      <strong>Bidirectional</strong>
                      <small>Prepare both connection sides under one partner plan.</small>
                    </Radio.Button>
                  </Radio.Group>
                </Form.Item>

                {includesInbound(direction) && (
                  <section className="connection-block">
                    <div className="connection-block-heading">
                      <span className="connection-block-icon">
                        <SendOutlined />
                      </span>
                      <div>
                        <strong>Partner → our SMPPs server</strong>
                        <small>Inbound bind details we would issue and approve.</small>
                      </div>
                      <Tag color="cyan">Inbound</Tag>
                    </div>
                    <div className="form-grid-two">
                      <Form.Item
                        label="Planned system ID"
                        name="inboundSystemID"
                        rules={[{ required: true, message: "Enter the planned system ID" }]}
                      >
                        <Input placeholder="acme_gateway" autoComplete="off" />
                      </Form.Item>
                      <Form.Item label="Bind mode" name="inboundBindMode">
                        <Select options={bindModeOptions} />
                      </Form.Item>
                    </div>
                    <Form.Item
                      label="Source IP allowlist"
                      name="inboundAllowlist"
                      extra="Comma-separated addresses for planning only. No firewall rule is created."
                      rules={[{ required: true, message: "Add at least one expected source IP" }]}
                    >
                      <Input placeholder="203.0.113.10, 203.0.113.11" autoComplete="off" />
                    </Form.Item>
                    <Form.Item
                      label="Require TLS"
                      name="inboundTLS"
                      valuePropName="checked"
                      className="wizard-switch-item"
                    >
                      <Switch checkedChildren="Required" unCheckedChildren="Optional" />
                    </Form.Item>
                  </section>
                )}

                {includesOutbound(direction) && (
                  <section className="connection-block">
                    <div className="connection-block-heading">
                      <span className="connection-block-icon">
                        <CloudServerOutlined />
                      </span>
                      <div>
                        <strong>Our gateway → partner SMSC</strong>
                        <small>Outbound endpoint and bind details the partner must provide.</small>
                      </div>
                      <Tag color="blue">Outbound</Tag>
                    </div>
                    <div className="form-grid-two">
                      <Form.Item
                        label="SMSC host"
                        name="outboundHost"
                        rules={[{ required: true, message: "Enter the partner SMSC host" }]}
                      >
                        <Input
                          prefix={<GlobalOutlined />}
                          placeholder="smpp.partner.example"
                          autoComplete="off"
                        />
                      </Form.Item>
                      <Form.Item
                        label="Port"
                        name="outboundPort"
                        rules={[{ required: true, message: "Enter the SMPP port" }]}
                      >
                        <InputNumber min={1} max={65535} style={{ width: "100%" }} />
                      </Form.Item>
                      <Form.Item
                        label="System ID"
                        name="outboundSystemID"
                        rules={[{ required: true, message: "Enter the assigned system ID" }]}
                      >
                        <Input placeholder="jasmin_acme" autoComplete="off" />
                      </Form.Item>
                      <Form.Item label="Bind mode" name="outboundBindMode">
                        <Select options={bindModeOptions} />
                      </Form.Item>
                    </div>
                    <div className="secret-placeholder">
                      <LockOutlined aria-hidden="true" />
                      <span>
                        <strong>Password intentionally omitted</strong>
                        Production onboarding must collect it through a secret manager, never this
                        mock form.
                      </span>
                    </div>
                    <Form.Item
                      label="Require TLS verification"
                      name="outboundTLS"
                      valuePropName="checked"
                      className="wizard-switch-item"
                    >
                      <Switch checkedChildren="Required" unCheckedChildren="Optional" />
                    </Form.Item>
                  </section>
                )}
              </div>
            )}

            {currentStep === 2 && (
              <div className="wizard-form-section">
                <div className="form-grid-two">
                  <Form.Item
                    label="Destination prefixes"
                    name="prefixes"
                    extra="Comma-separated E.164 prefixes this partner is expected to handle."
                    rules={[{ required: true, message: "Enter at least one destination prefix" }]}
                  >
                    <Input placeholder="44, 49, 358" autoComplete="off" />
                  </Form.Item>
                  <Form.Item
                    label="Planned throughput"
                    name="throughput"
                    extra="Messages per second. Production enforcement remains a backend prerequisite."
                    rules={[{ required: true, message: "Enter a planned throughput" }]}
                  >
                    <InputNumber
                      min={1}
                      max={10000}
                      suffix="msg/s"
                      style={{ width: "100%" }}
                    />
                  </Form.Item>
                  <Form.Item label="Delivery receipt policy" name="dlrMode">
                    <Select
                      options={[
                        { value: "final", label: "Final DLR only" },
                        { value: "all", label: "All requested receipts" },
                        { value: "none", label: "No DLR forwarding" },
                      ]}
                    />
                  </Form.Item>
                  <Form.Item
                    label="Optional MO delivery URL"
                    name="moURL"
                    rules={[{ type: "url", message: "Enter a complete http:// or https:// URL" }]}
                  >
                    <Input placeholder="https://partner.example/messages/mo" autoComplete="url" />
                  </Form.Item>
                </div>
                <div className="wizard-context-card">
                  <SafetyCertificateOutlined aria-hidden="true" />
                  <span>
                    <strong>Policy is not enforcement</strong>
                    These values document the intended limits. The production gate stays closed until
                    quotas, CDRs and per-partner observability are implemented.
                  </span>
                </div>
              </div>
            )}

            {currentStep === 3 && (
              <div className="wizard-review">
                <div className="review-summary-card">
                  <div className="review-summary-heading">
                    <span className="review-partner-icon">
                      <TeamOutlined />
                    </span>
                    <div>
                      <span>Partner plan</span>
                      <h3>{reviewValues.partnerName}</h3>
                      <p>{reviewValues.partnerCode}</p>
                    </div>
                    <Tag color="gold">Mock only</Tag>
                  </div>
                  <Descriptions column={1} size="small" colon={false}>
                    <Descriptions.Item label="Technical contact">
                      {contactLabels[reviewValues.contactChannel]}
                      {reviewValues.contactChannel !== "not_assigned" &&
                        ` · ${reviewValues.contactValue}`}
                    </Descriptions.Item>
                    <Descriptions.Item label="Direction">
                      {direction === "inbound" && "Partner connects to us"}
                      {direction === "outbound" && "We connect to partner"}
                      {direction === "bidirectional" && "Bidirectional"}
                    </Descriptions.Item>
                    {includesInbound(direction) && (
                      <Descriptions.Item label="Inbound bind">
                        {reviewValues.inboundSystemID} · {reviewValues.inboundBindMode} · TLS{" "}
                        {reviewValues.inboundTLS ? "required" : "optional"}
                      </Descriptions.Item>
                    )}
                    {includesOutbound(direction) && (
                      <Descriptions.Item label="Outbound endpoint">
                        {reviewValues.outboundHost}:{reviewValues.outboundPort} ·{" "}
                        {reviewValues.outboundBindMode} · TLS{" "}
                        {reviewValues.outboundTLS ? "required" : "optional"}
                      </Descriptions.Item>
                    )}
                    <Descriptions.Item label="Traffic policy">
                      {reviewValues.prefixes} · {reviewValues.throughput} msg/s ·{" "}
                      {reviewValues.dlrMode} DLR
                    </Descriptions.Item>
                  </Descriptions>
                </div>

                <section className="future-resource-card">
                  <span className="review-card-kicker">
                    What production onboarding would create
                  </span>
                  <ul>
                    {futureResources.map((resource) => (
                      <li key={resource}>
                        <CheckCircleFilled aria-hidden="true" />
                        {resource}
                      </li>
                    ))}
                  </ul>
                </section>

                <section className="readiness-card">
                  <span className="review-card-kicker">Backend readiness gate</span>
                  <p>
                    Secret storage, idempotent orchestration, network preflight, RBAC, audit and
                    per-partner monitoring must land before this plan can be applied.
                  </p>
                  <div className="readiness-state">
                    <LockOutlined aria-hidden="true" />
                    Apply remains locked
                  </div>
                </section>
              </div>
            )}

            <Divider />
            <div className="wizard-actions">
              <Button
                icon={<ArrowLeftOutlined />}
                disabled={currentStep === 0}
                onClick={() => setCurrentStep((step) => Math.max(step - 1, 0))}
              >
                Back
              </Button>
              {currentStep < stepItems.length - 1 ? (
                <Button
                  key="continue"
                  type="primary"
                  htmlType="button"
                  icon={<ArrowRightOutlined />}
                  iconPosition="end"
                  onClick={() => void goForward()}
                >
                  Continue
                </Button>
              ) : (
                <Button
                  key="submit"
                  type="primary"
                  htmlType="submit"
                  icon={<CheckCircleFilled />}
                >
                  Generate mock plan
                </Button>
              )}
            </div>
          </Form>
        </main>
      </section>
    </div>
  );
};
