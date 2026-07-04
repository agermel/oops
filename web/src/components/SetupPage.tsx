import React from "react";
import { Server } from "lucide-react";
import { authPaths } from "../lib/paths";

export function SetupPage() {
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [error, setError] = React.useState("");
  const [loading, setLoading] = React.useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (loading) return;
    if (!username.trim()) { setError("请输入用户名"); return; }
    if (!password.trim()) { setError("请输入密码"); return; }
    setLoading(true);
    setError("");

    try {
      const formData = new FormData();
      formData.set("username", username.trim());
      formData.set("name", username.trim());
      formData.set("password", password);

      const resp = await fetch(authPaths.setup, {
        method: "POST",
        body: formData,
      });

      if (resp.ok) {
        // 设置成功 → 整页跳转到首页
        window.location.href = "/";
      } else if (resp.status === 409) {
        setError("管理员账户已存在，请刷新页面后登录");
      } else {
        const body = await resp.json().catch(() => ({}));
        setError((body as any).error || `服务异常 (HTTP ${resp.status})`);
      }
    } catch {
      setError("网络错误，请确认服务端已启动");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="login-page">
      <div className="login-card">
        <div className="login-header">
          <Server size={40} />
          <h1>oops</h1>
          <p>初次使用，请创建管理员账户</p>
        </div>

        <form onSubmit={handleSubmit}>
          <label htmlFor="setup-username">用户名</label>
          <input
            id="setup-username"
            className="form-input"
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="请输入用户名"
            autoComplete="username"
            autoFocus
          />

          <label htmlFor="setup-password">密码</label>
          <input
            id="setup-password"
            className="form-input"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="请设置密码"
            autoComplete="new-password"
          />

          {error && <div className="error-banner">{error}</div>}

          <button type="submit" className="btn-primary login-btn" disabled={loading}>
            {loading ? "创建中..." : "创建管理员账户"}
          </button>
        </form>
      </div>
    </div>
  );
}
