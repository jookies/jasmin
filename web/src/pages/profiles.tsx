import { useState } from "react";
import { App, Button, Form, Input, Modal } from "antd";
import { CloudDownloadOutlined, SaveOutlined } from "@ant-design/icons";

import { PageTitle } from "../components/OperatorUI";
import { API_URL, httpClient } from "../httpClient";

export const ProfilesPage = () => {
  const { message } = App.useApp();
  const [profile, setProfile] = useState("jcli-prod");
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(false);
  const valid = /^[A-Za-z0-9_.-]{1,64}$/.test(profile.trim());

  const save = async () => {
    setSaving(true);
    try {
      await httpClient.post(`${API_URL}/profiles/${encodeURIComponent(profile.trim())}/save`);
      void message.success(`Saved configuration profile “${profile.trim()}”`);
    } catch {
      void message.error("Could not save the configuration profile");
    } finally {
      setSaving(false);
    }
  };

  const load = () => {
    const name = profile.trim();
    Modal.confirm({
      title: `Restore “${name}”?`,
      content:
        "This replaces every admin-managed connector, route, group, user, bind account, interceptor, filter and HTTP destination with the saved snapshot.",
      okText: "Restore profile",
      okButtonProps: { danger: true },
      async onOk() {
        setLoading(true);
        try {
          await httpClient.post(`${API_URL}/profiles/${encodeURIComponent(name)}/load`);
          void message.success(`Restored configuration profile “${name}”`);
        } catch {
          void message.error("The profile could not be fully restored");
          throw new Error("profile restore failed");
        } finally {
          setLoading(false);
        }
      },
    });
  };

  return (
    <div className="page-container">
      <PageTitle
        eyebrow="Configuration safety"
        title="Configuration profiles"
        description="Create a named checkpoint of every admin-managed resource or restore a known-good configuration."
      />
      <section className="dashboard-panel profile-panel">
        <div className="panel-heading">
          <div>
            <h2>Named checkpoint</h2>
            <p>
              Profile names are explicit, matching the Go management console. Saving the same name
              replaces its previous snapshot.
            </p>
          </div>
        </div>
        <Form layout="vertical" onFinish={() => void save()}>
          <Form.Item
            label="Profile name"
            validateStatus={profile && !valid ? "error" : undefined}
            help={profile && !valid ? "Use letters, numbers, dots, underscores or dashes" : undefined}
          >
            <Input
              value={profile}
              onChange={(event) => setProfile(event.target.value)}
              placeholder="jcli-prod"
              maxLength={64}
            />
          </Form.Item>
          <div className="profile-actions">
            <Button
              type="primary"
              htmlType="submit"
              icon={<SaveOutlined />}
              loading={saving}
              disabled={!valid}
            >
              Save current configuration
            </Button>
            <Button
              danger
              icon={<CloudDownloadOutlined />}
              loading={loading}
              disabled={!valid}
              onClick={load}
            >
              Restore saved profile
            </Button>
          </div>
        </Form>
        <div className="profile-scope">
          <strong>Snapshot scope</strong>
          <p>
            Groups, users, connectors, MT routes, MO routes, interceptors, SMPPs bind accounts,
            saved filters and HTTP destinations.
          </p>
        </div>
      </section>
    </div>
  );
};
