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
import { API_URL, httpClient } from "../httpClient";

type CreatedResource = { kind: string; id: string };
type OnboardingResult = {
  created: CreatedResource[];
  http_password?: string;
  bind_password?: string;
  warnings?: string[];
};

const RESOURCE_LABELS: Record<string, string> = {
  group: "Billing group",
  user: "Gateway user",
  smpps_bind_account: "SMPPs bind account",
  connector: "SMPPc connector",
  mt_route: "MT route",
};

const apiErrorMessage = (error: unknown, fallback: string) =>
  typeof error === "object" &&
  error !== null &&
  "response" in error &&
  typeof error.response === "object" &&
  error.response !== null &&
  "data" in error.response
    ? String((error.response.data as { message?: string }).message ?? fallback)
    : fallback;

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
  { title: "Review", description: "Provision" },
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
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const [result, setResult] = useState<OnboardingResult | null>(null);
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

  const startAnother = () => {
    form.resetFields();
    setReviewValues(initialValues);
    setCurrentStep(0);
    setCompleted(false);
    setResult(null);
    setSubmitError("");
  };

  // The wizard provisions for real: one call creates the group, gateway user,
  // bind account, connector and route together, or undoes whatever it managed
  // to create. Credentials are generated server-side and shown exactly once.
  const provision = async () => {
    const values = form.getFieldsValue(true) as PartnerPlan;
    setSubmitting(true);
    setSubmitError("");
    try {
      const response = await httpClient.post(`${API_URL}/onboarding/partners`, {
        partner_name: values.partnerName,
        partner_code: values.partnerCode,
        direction: values.direction,
        create_group: true,
        throughput: values.throughput,
        inbound_system_id: values.inboundSystemID,
        inbound_bind_mode: values.inboundBindMode,
        inbound_allowlist: values.inboundAllowlist,
        outbound_host: values.outboundHost,
        outbound_port: values.outboundPort,
        outbound_system_id: values.outboundSystemID,
        outbound_bind_mode: values.outboundBindMode,
        outbound_tls: values.outboundTLS,
        prefixes: (values.prefixes ?? "")
          .split(/[\s,]+/)
          .map((prefix) => prefix.trim())
          .filter(Boolean),
      });
      setResult(response.data as OnboardingResult);
      setCompleted(true);
    } catch (error) {
      setSubmitError(apiErrorMessage(error, "Provisioning failed"));
    } finally {
      setSubmitting(false);
    }
  };

  if (completed) {
    return (
      <div className="page-container partner-onboarding-page">
        <section className="partner-complete-panel">
          <Result
            status="success"
            title={`${reviewValues.partnerName} is provisioned`}
            subTitle="These resources are live now. Nothing else needs to be created by hand."
            extra={[
              <Button key="reset" type="primary" onClick={startAnother}>
                Onboard another partner
              </Button>,
            ]}
          />

          <Descriptions column={1} bordered size="small" title="What was created">
            {(result?.created ?? []).map((created) => (
              <Descriptions.Item
                key={`${created.kind}:${created.id}`}
                label={RESOURCE_LABELS[created.kind] ?? created.kind}
              >
                {created.id}
              </Descriptions.Item>
            ))}
          </Descriptions>

          {(result?.http_password || result?.bind_password) && (
            <Alert
              type="warning"
              showIcon
              icon={<LockOutlined />}
              message="Copy these credentials now — they are shown once and cannot be retrieved"
              description={
                <div className="credential-handoff">
                  {result?.http_password ? (
                    <p>
                      <strong>HTTP password</strong>
                      <code>{result.http_password}</code>
                    </p>
                  ) : null}
                  {result?.bind_password ? (
                    <p>
                      <strong>SMPP bind password</strong>
                      <code>{result.bind_password}</code>
                      <small>
                        Eight characters, because SMPP 3.4 caps a bind password there — a longer
                        secret cannot bind at all.
                      </small>
                    </p>
                  ) : null}
                  <small>
                    Only the hash is stored. Hand these over through your secret channel, not by
                    email.
                  </small>
                </div>
              }
            />
          )}

          {(result?.warnings ?? []).map((warning) => (
            <Alert key={warning} type="info" showIcon message={warning} />
          ))}
        </section>
      </div>
    );
  }

  return (
    <div className="page-container partner-onboarding-page">
      <section className="partner-onboarding-hero">
        <div>
          <PageTitle
            eyebrow="Access · Partner onboarding"
            title="Onboard a partner"
            description="Provision a partner in one step — group, credentials, bind account, connector and route together, or nothing at all."
          />
          <div className="partner-hero-tags" aria-label="Onboarding properties">
            <Tag color="cyan">4 guided steps</Tag>
            <Tag color="green">Provisions live resources</Tag>
            <Tag>Rolls back on failure</Tag>
          </div>
        </div>
        <div className="partner-hero-mark" aria-hidden="true">
          <TeamOutlined />
        </div>
      </section>

      <Alert
        className="onboarding-alert"
        type="info"
        showIcon
        message="Finishing this wizard creates live resources."
        description="The group, gateway user, bind account, connector and route are created together. If any step fails the others are removed, so a partner is never half-built. Generated credentials are displayed once."
      />

      {submitError ? (
        <Alert type="error" showIcon message="Provisioning failed" description={submitError} />
      ) : null}

      <section className="partner-wizard-shell">
        <aside className="partner-wizard-sidebar" aria-label="Onboarding progress">
          <span className="wizard-sidebar-kicker">Setup progress</span>
          <Steps direction="vertical" current={currentStep} items={stepItems} />
          <div className="wizard-safety-card">
            <SafetyCertificateOutlined aria-hidden="true" />
            <span>
              <strong>Nothing is created until the last step</strong>
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
            onFinish={() => void provision()}
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
                  "Review what will be created. Finishing provisions all of it, or none of it."}
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
                      extra="Enforced on every bind attempt. Leave empty to accept any source IP."
                    >
                      <Input placeholder="any" autoComplete="off" />
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
                        <strong>Passwords are generated, not typed</strong>
                        The gateway mints them when you finish and shows them once. Nothing here
                        collects a password, and only the hash is stored.
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
                    extra="Comma-separated E.164 prefixes. Leave empty to route all destinations from this partner."
                  >
                    <Input placeholder="all" autoComplete="off" />
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
                    <Tag color="green">Ready to provision</Tag>
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
                        {reviewValues.inboundTLS ? "required" : "optional"} · source IP{" "}
                        {reviewValues.inboundAllowlist?.trim()
                          ? reviewValues.inboundAllowlist
                          : "any"}
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
                      {reviewValues.prefixes?.trim() ? reviewValues.prefixes : "all destinations"} ·{" "}
                      {reviewValues.throughput} msg/s · {reviewValues.dlrMode} DLR
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
                  loading={submitting}
                >
                  Provision partner
                </Button>
              )}
            </div>
          </Form>
        </main>
      </section>
    </div>
  );
};
