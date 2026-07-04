import React from "react";
import { Server } from "lucide-react";
import { authPaths } from "../lib/paths";

export function LoginPage() {
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [error, setError] = React.useState("");
  const [loading, setLoading] = React.useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (loading) return;
    setLoading(true);
    setError("");

    try {
      const formData = new FormData();
      formData.set("username", username);
      formData.set("password", password);

      const resp = await fetch(authPaths.token, {
        method: "POST",
        body: formData,
      });

      if (resp.ok) {
        // 登录成功 → 读取 redirectUrl → 整页跳转
        const params = new URLSearchParams(window.location.search);
        const redirectUrl = params.get("redirectUrl") || "/";
        window.location.href = redirectUrl;
      } else if (resp.status === 401) {
        setError("用户名或密码错误");
      } else {
        setError(`服务异常 (HTTP ${resp.status})，请稍后重试`);
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
          <p>基础设施运维面板</p>
        </div>

        <form onSubmit={handleSubmit}>
          <label htmlFor="login-username">用户名</label>
          <input
            id="login-username"
            className="form-input"
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="请输入用户名"
            autoComplete="username"
            autoFocus
          />

          <label htmlFor="login-password">密码</label>
          <input
            id="login-password"
            className="form-input"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="请输入密码"
            autoComplete="current-password"
          />

          {error && <div className="error-banner">{error}</div>}

          <button type="submit" className="btn-primary login-btn" disabled={loading}>
            {loading ? "登录中..." : "登录"}
          </button>
        </form>
      </div>
    </div>
  );
}
