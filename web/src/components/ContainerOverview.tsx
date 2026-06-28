import { Wrench } from "lucide-react";
import type { ContainerDetail, MCPPrefill } from "../types";
import { serviceTypeIcons, serviceTypeLabels } from "../types";

export function ContainerOverview({ detail, onConfigureMCP }: { detail: ContainerDetail; onConfigureMCP: (prefill: MCPPrefill) => void }) {
  function buildMCPPrefill(): MCPPrefill {
    const env: string[] = [];
    // 把 DSN 中的 host/port/user/database 提出来作为独立字段，密码留空让用户填。
    // env 里仍然放一个模板，方便高级用户直接修改。
    if (detail.dsn?.raw) {
      const type = detail.serviceType;
      if (type === "mysql") env.push(`MYSQL_DSN=${detail.dsn.raw}`);
      else if (type === "redis") {
        env.push(`REDIS_URL=${detail.dsn.raw}`);
        if (detail.dsn.host) env.push(`REDIS_HOST=${detail.dsn.host}`);
        if (detail.dsn.port) env.push(`REDIS_PORT=${String(detail.dsn.port)}`);
      } else if (type === "postgres") env.push(`DATABASE_URL=${detail.dsn.raw}`);
      else if (type === "mongo") env.push(`MONGO_URI=${detail.dsn.raw}`);
      else env.push(detail.dsn.raw);
    }
    return {
      name: detail.container.name,
      type: detail.serviceType,
      host: detail.dsn?.host,
      port: detail.dsn?.port,
      user: detail.dsn?.user,
      database: detail.dsn?.database,
      env,
    };
  }
  const Icon = serviceTypeIcons[detail.serviceType] || serviceTypeIcons.unknown;
  const label = serviceTypeLabels[detail.serviceType] || detail.serviceType;

  return (
    <div className="container-overview">
      <div className="overview-header">
        <Icon size={28} />
        <div>
          <h2>{detail.container.name}</h2>
          <span className="type-pill">{label}</span>
          <span className={`status-pill ${detail.container.state === "running" ? "alive" : "dead"}`}>
            {detail.container.state}
          </span>
        </div>
      </div>

      <div className="overview-grid">
        <div className="overview-card">
          <h3>容器信息</h3>
          <dl>
            <dt>ID</dt>
            <dd className="mono">{detail.container.id.slice(0, 12)}</dd>
            <dt>镜像</dt>
            <dd className="mono">{detail.container.image}</dd>
            <dt>主机</dt>
            <dd className="mono">{detail.container.hostId}</dd>
            <dt>创建时间</dt>
            <dd>{detail.container.created ? new Date(detail.container.created).toLocaleString() : "-"}</dd>
          </dl>
        </div>

        {detail.dsn && (
          <div className="overview-card">
            <h3>连接信息 (DSN)</h3>
            <button className="primary-button small mcp-quick-btn" onClick={() => onConfigureMCP(buildMCPPrefill())}>
              <Wrench size={14} />
              <span>一键配置 MCP</span>
            </button>
            <dl>
              {detail.dsn.host && (
                <>
                  <dt>主机</dt>
                  <dd className="mono">{detail.dsn.host}</dd>
                </>
              )}
              {detail.dsn.port > 0 && (
                <>
                  <dt>端口</dt>
                  <dd className="mono">{detail.dsn.port}</dd>
                </>
              )}
              {detail.dsn.user && (
                <>
                  <dt>用户</dt>
                  <dd className="mono">{detail.dsn.user}</dd>
                </>
              )}
              {detail.dsn.database && (
                <>
                  <dt>数据库</dt>
                  <dd className="mono">{detail.dsn.database}</dd>
                </>
              )}
              {detail.dsn.raw && (
                <>
                  <dt>完整 DSN</dt>
                  <dd className="mono dsn-raw">{detail.dsn.raw}</dd>
                </>
              )}
            </dl>
          </div>
        )}

        {detail.container.ports && detail.container.ports.length > 0 && (
          <div className="overview-card">
            <h3>端口映射</h3>
            <table>
              <thead>
                <tr>
                  <th>容器端口</th>
                  <th>协议</th>
                  <th>主机端口</th>
                </tr>
              </thead>
              <tbody>
                {detail.container.ports.map((p, i) => (
                  <tr key={i}>
                    <td className="mono">{p.containerPort}</td>
                    <td>{p.protocol || "tcp"}</td>
                    <td className="mono">{p.hostPort || "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
