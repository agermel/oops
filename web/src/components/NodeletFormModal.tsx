import React from "react";
import type { NodeletConfig } from "../types";
import { apiRequest, getErrorMessage } from "../lib/api";
import { Modal } from "./Modal";
import { Button } from "./ui/Button";

type Props = {
  editItem: NodeletConfig | null;
  onSaved: () => void;
  onClose: () => void;
};

// ---- NodeletFormModal ----
export function NodeletFormModal({ editItem, onSaved, onClose }: Props) {
  const isNew = !editItem;

  const [id, setId] = React.useState(editItem?.id ?? "");
  const [name, setName] = React.useState(editItem?.name ?? "");
  const [address, setAddress] = React.useState(editItem?.address ?? "http://:8686");
  const hasExistingToken = editItem?.hasToken === true;
  const [token, setToken] = React.useState("");

  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState("");
  const [saveOk, setSaveOk] = React.useState("");

  const [testing, setTesting] = React.useState(false);
  const [testResult, setTestResult] = React.useState("");

  function buildPayload(): NodeletConfig {
    return { id, name, address, token };
  }

  function canSave() {
    return id.trim() !== "" && address.trim() !== "";
  }

  async function handleSave() {
    setSaving(true);
    setSaveError("");
    setSaveOk("");
    try {
      const method = isNew ? "POST" : "PUT";
      const url = isNew ? "/api/nodelets" : `/api/nodelets/${encodeURIComponent(id)}`;
      await apiRequest(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(buildPayload()),
      });
      setSaveOk("Saved");
      onSaved();
    } catch (err) {
      setSaveError(getErrorMessage(err, "保存失败"));
    } finally {
      setSaving(false);
    }
  }

  async function handleTest() {
    setTesting(true);
    setTestResult("");
    try {
      const resp = await apiRequest<{ status: string; error?: string }>(
        "/api/nodelets/test",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(buildPayload()),
        },
      );
      setTestResult(resp.status === "ok" ? "连接成功 ✓" : `失败: ${resp.error ?? "未知错误"}`);
    } catch (err) {
      setTestResult(`错误: ${getErrorMessage(err, "测试请求失败")}`);
    } finally {
      setTesting(false);
    }
  }

  return (
    <Modal
      title={isNew ? "添加服务器" : "编辑服务器"}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={handleTest} disabled={testing || !canSave()}>
            {testing ? "测试中..." : "测试连接"}
          </Button>
          <Button onClick={handleSave} disabled={saving || !canSave()}>
            {saving ? "保存中..." : "保存"}
          </Button>
        </>
      }
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        {saveError && <div className="error-banner">{saveError}</div>}
        {saveOk && <div className="success-banner">{saveOk}</div>}
        {testResult && (
          <div className={testResult.includes("成功") ? "success-banner" : "error-banner"}>
            {testResult}
          </div>
        )}

        <label className="form-label">
          ID
          <input
            className="form-input"
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="例: prod-api-01"
            disabled={!isNew}
          />
        </label>

        <label className="form-label">
          名称
          <input
            className="form-input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="例: 生产环境 API 服务器"
          />
        </label>

        <label className="form-label">
          地址
          <input
            className="form-input form-monospace"
            value={address}
            onChange={(e) => setAddress(e.target.value)}
            placeholder="http://10.0.0.1:8686"
          />
        </label>

        <label className="form-label">
          Token
          <input
            className="form-input form-monospace"
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder={hasExistingToken ? "•••••••• (留空则不修改)" : "Nodelet 启动时打印的配对 token"}
          />
        </label>
      </div>
    </Modal>
  );
}
