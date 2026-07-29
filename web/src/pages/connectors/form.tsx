import type { FormProps } from "antd";
import {
  Button,
  Collapse,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
} from "antd";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";

import { EffectiveValue, FormIntroduction } from "../../components/OperatorUI";

const bindOptions = [
  { value: "transceiver", label: "Transceiver · send and receive" },
  { value: "transmitter", label: "Transmitter · send only" },
  { value: "receiver", label: "Receiver · receive only" },
];

const tonOptions = [
  { value: 0, label: "0 · Unknown" },
  { value: 1, label: "1 · International" },
  { value: 2, label: "2 · National" },
  { value: 3, label: "3 · Network specific" },
  { value: 4, label: "4 · Subscriber number" },
  { value: 5, label: "5 · Alphanumeric" },
  { value: 6, label: "6 · Abbreviated" },
];

const npiOptions = [
  { value: 0, label: "0 · Unknown" },
  { value: 1, label: "1 · ISDN" },
  { value: 3, label: "3 · Data" },
  { value: 4, label: "4 · Telex" },
  { value: 6, label: "6 · Land mobile" },
  { value: 8, label: "8 · National" },
  { value: 9, label: "9 · Private" },
  { value: 10, label: "10 · ERMES" },
  { value: 14, label: "14 · Internet" },
  { value: 18, label: "18 · WAP client ID" },
];

const fullWidth = { width: "100%" };

const Basics = ({ editing }: { editing?: boolean }) => (
  <>
    <Form.Item label="Connector ID" name="cid" rules={[{ required: true }]}>
      <Input placeholder="my-smsc" disabled={editing} />
    </Form.Item>
    <div className="form-grid-two">
      <Form.Item label="Host" name="host" rules={[{ required: true }]}>
        <Input placeholder="smsc.example.com" />
      </Form.Item>
      <Form.Item label="Port" name="port" rules={[{ required: true }]} initialValue={2775}>
        <InputNumber min={1} max={65535} style={fullWidth} />
      </Form.Item>
    </div>
    <Form.Item label="System ID" name="system_id" rules={[{ required: true }]}>
      <Input />
    </Form.Item>
    <Form.Item label="Password" name="password">
      <Input.Password
        placeholder={editing ? "unchanged" : ""}
        autoComplete="new-password"
      />
    </Form.Item>
    <div className="form-grid-two">
      <Form.Item label="Bind type" name="bind" initialValue="transceiver">
        <Select options={bindOptions} />
      </Form.Item>
      <Form.Item label="System type" name="system_type">
        <Input />
      </Form.Item>
    </div>
    <Form.Item
      label="Started"
      name="desired_started"
      valuePropName="checked"
      initialValue={false}
      tooltip="Bind and keep the connector connected"
    >
      <Switch />
    </Form.Item>
  </>
);

const Addressing = () => (
  <>
    <div className="form-grid-two">
      <Form.Item label="Bind address TON" name="addr_ton">
        <Select options={tonOptions} placeholder="0 · Unknown" />
      </Form.Item>
      <Form.Item label="Bind address NPI" name="addr_npi">
        <Select options={npiOptions} placeholder="0 · Unknown" />
      </Form.Item>
    </div>
    <Form.Item label="Bind address range" name="address_range">
      <Input placeholder="optional" />
    </Form.Item>
    <div className="form-grid-two">
      <Form.Item
        label="Default source TON"
        name="src_ton"
        extra={<EffectiveValue value="2 · National" />}
      >
        <Select options={tonOptions} placeholder="2 · National" />
      </Form.Item>
      <Form.Item
        label="Default source NPI"
        name="src_npi"
        extra={<EffectiveValue value="1 · ISDN" />}
      >
        <Select options={npiOptions} placeholder="1 · ISDN" />
      </Form.Item>
      <Form.Item
        label="Default destination TON"
        name="dst_ton"
        extra={<EffectiveValue value="1 · International" />}
      >
        <Select options={tonOptions} placeholder="1 · International" />
      </Form.Item>
      <Form.Item
        label="Default destination NPI"
        name="dst_npi"
        extra={<EffectiveValue value="1 · ISDN" />}
      >
        <Select options={npiOptions} placeholder="1 · ISDN" />
      </Form.Item>
    </div>
    <Form.Item label="Default source address" name="source_addr">
      <Input placeholder="optional" />
    </Form.Item>
  </>
);

const Connection = () => (
  <>
    <div className="form-grid-three">
      <Form.Item label="Transaction timeout" name="trx_to" extra={<EffectiveValue value="300 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="300" />
      </Form.Item>
      <Form.Item label="Response timeout" name="res_to" extra={<EffectiveValue value="120 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="120" />
      </Form.Item>
      <Form.Item label="PDU read timeout" name="pdu_to" extra={<EffectiveValue value="10 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="10" />
      </Form.Item>
      <Form.Item label="Enquire-link interval" name="elink_interval" extra={<EffectiveValue value="30 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="30" />
      </Form.Item>
      <Form.Item label="Bind timeout" name="bind_to" extra={<EffectiveValue value="30 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="30" />
      </Form.Item>
      <Form.Item label="Requeue delay" name="requeue_delay" extra={<EffectiveValue value="120 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="120" />
      </Form.Item>
    </div>
    <div className="form-grid-two">
      <Form.Item
        label="Retry connection failures"
        name="con_fail_retry"
        valuePropName="checked"
        initialValue={true}
      >
        <Switch />
      </Form.Item>
      <Form.Item label="Failure retry delay" name="con_fail_delay" extra={<EffectiveValue value="10 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="10" />
      </Form.Item>
      <Form.Item
        label="Retry connection loss"
        name="con_loss_retry"
        valuePropName="checked"
        initialValue={true}
      >
        <Switch />
      </Form.Item>
      <Form.Item label="Loss retry delay" name="con_loss_delay" extra={<EffectiveValue value="10 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="10" />
      </Form.Item>
    </div>
  </>
);

const Security = () => (
  <>
    <Form.Item label="Use SMPP over TLS" name="tls_enabled" valuePropName="checked" initialValue={false}>
      <Switch />
    </Form.Item>
    <Form.Item noStyle shouldUpdate>
      {({ getFieldValue }) =>
        getFieldValue("tls_enabled") ? (
          <>
            <Form.Item label="TLS server name" name="tls_server_name">
              <Input placeholder="smsc.example.com" />
            </Form.Item>
            <Form.Item
              label="CA certificate file"
              name="tls_ca_file"
              tooltip="Path on the gateway host"
            >
              <Input placeholder="/run/secrets/smsc-ca.pem" />
            </Form.Item>
          </>
        ) : null
      }
    </Form.Item>
  </>
);

const SubmitBehavior = () => (
  <>
    <div className="form-grid-two">
      <Form.Item label="Service type" name="service_type">
        <Input />
      </Form.Item>
      <Form.Item label="Protocol ID" name="protocol_id">
        <InputNumber min={0} max={255} style={fullWidth} placeholder="0" />
      </Form.Item>
      <Form.Item label="Replace-if-present" name="replace_if_present_flag">
        <Select
          allowClear
          options={[
            { value: 0, label: "0 · Do not replace" },
            { value: 1, label: "1 · Replace" },
          ]}
          placeholder="0 · Do not replace"
        />
      </Form.Item>
      <Form.Item label="Default message ID" name="sm_default_msg_id">
        <InputNumber min={0} max={255} style={fullWidth} placeholder="0" />
      </Form.Item>
      <Form.Item label="Default data coding" name="data_coding">
        <InputNumber min={0} max={255} style={fullWidth} placeholder="0" />
      </Form.Item>
      <Form.Item label="Default validity period" name="validity_period">
        <Input placeholder="optional" />
      </Form.Item>
      <Form.Item
        label="Submit throughput"
        name="submit_sm_throughput"
        tooltip="Messages per second; empty uses the connector default"
      >
        <InputNumber min={0} step={0.1} style={fullWidth} placeholder="default" />
      </Form.Item>
      <Form.Item label="AMQP prefetch" name="prefetch_count" extra={<EffectiveValue value="1" />}>
        <InputNumber min={1} max={65535} style={fullWidth} placeholder="1" />
      </Form.Item>
      <Form.Item label="Connector priority" name="priority">
        <InputNumber style={fullWidth} placeholder="0" />
      </Form.Item>
    </div>
  </>
);

const DeliveryAndLogging = () => (
  <>
    <div className="form-grid-two">
      <Form.Item label="DLR message-ID bases" name="dlr_msg_id_bases">
        <Select
          allowClear
          placeholder="0 · Same base"
          options={[
            { value: 0, label: "0 · Same base" },
            { value: 1, label: "1 · Receipt decimal / response hex" },
            { value: 2, label: "2 · Receipt hex / response decimal" },
          ]}
        />
      </Form.Item>
      <Form.Item label="DLR expiry" name="dlr_expiry" extra={<EffectiveValue value="86,400 s" />}>
        <InputNumber min={0} style={fullWidth} placeholder="86400" />
      </Form.Item>
      <Form.Item label="Log level" name="log_level">
        <Select
          allowClear
          options={["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"].map((value) => ({
            value,
            label: value,
          }))}
        />
      </Form.Item>
      <Form.Item label="Log file" name="log_file">
        <Input placeholder="shared gateway logger" />
      </Form.Item>
      <Form.Item label="Log rotation" name="log_rotate">
        <Input placeholder="midnight" />
      </Form.Item>
      <Form.Item label="Privacy logging" name="log_privacy" valuePropName="checked" initialValue={false}>
        <Switch />
      </Form.Item>
      <Form.Item
        label="Durable AMQP topology"
        name="amqp_durable_topology"
        valuePropName="checked"
        initialValue={false}
        tooltip="Must match every producer and consumer on the broker vhost"
      >
        <Switch />
      </Form.Item>
    </div>
  </>
);

const CustomTLVs = () => (
  <Form.List name="custom_tlvs">
    {(fields, { add, remove }) => (
      <>
        {fields.map(({ key, name }) => (
          <Space key={key} align="start" className="tlv-row" wrap>
            <Form.Item label="Tag" name={[name, "tag"]} rules={[{ required: true }]}>
              <InputNumber min={0} max={65535} placeholder="524" />
            </Form.Item>
            <Form.Item label="Type" name={[name, "type"]} rules={[{ required: true }]}>
              <Select
                style={{ minWidth: 160 }}
                options={[
                  "Int1",
                  "Int2",
                  "Int4",
                  "Int8",
                  "OctetString",
                  "COctetString",
                ].map((value) => ({ value, label: value }))}
              />
            </Form.Item>
            <Form.Item label="Max bytes" name={[name, "length"]}>
              <InputNumber min={1} placeholder="unlimited" />
            </Form.Item>
            <Form.Item label="Required" name={[name, "required"]} valuePropName="checked">
              <Switch />
            </Form.Item>
            <Button
              className="tlv-delete"
              icon={<DeleteOutlined />}
              aria-label="Remove TLV rule"
              onClick={() => remove(name)}
            />
          </Space>
        ))}
        <Button icon={<PlusOutlined />} onClick={() => add({ required: false })}>
          Add custom TLV rule
        </Button>
      </>
    )}
  </Form.List>
);

export const ConnectorFields = ({
  formProps,
  editing,
}: {
  formProps: FormProps;
  editing?: boolean;
}) => (
  <Form {...formProps} layout="vertical" className="operator-form operator-form-wide">
    <FormIntroduction title={editing ? "Update connector" : "Provision a connector"}>
      Configure the SMSC endpoint and bind identity first. Advanced sections expose the existing
      Go connector controls without hiding their effective defaults.
    </FormIntroduction>
    <Basics editing={editing} />
    <Collapse
      className="form-collapse"
      items={[
        { key: "addressing", label: "Addressing and TON/NPI", children: <Addressing /> },
        { key: "connection", label: "Timeouts and reconnection", children: <Connection /> },
        { key: "security", label: "Transport security", children: <Security /> },
        { key: "submit", label: "Submit behavior and throughput", children: <SubmitBehavior /> },
        { key: "delivery", label: "DLR, topology and logging", children: <DeliveryAndLogging /> },
        { key: "tlvs", label: "Custom TLV rules", children: <CustomTLVs /> },
      ]}
    />
  </Form>
);
