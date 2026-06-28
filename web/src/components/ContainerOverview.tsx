import type { ContainerDetail } from "../types";
import { serviceTypeIcons, serviceLabel } from "../types";
import { DSNInfoCard } from "./DSNInfoCard";

export function ContainerOverview({ detail, onEditDSN }: { detail: ContainerDetail; onEditDSN?: () => void }) {
  const Icon = serviceTypeIcons[detail.serviceType] || serviceTypeIcons.unknown;
  const label = serviceLabel(detail.serviceType);

  return (
    <div className="container-overview">
      <div className="overview-header">
        <Icon size={28} />
        <div>
          <h2>{detail.container.name}</h2>
          {label && <span className="type-pill">{label}</span>}
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

        <DSNInfoCard
          dsn={detail.dsn}
          dsnOverrides={detail.dsnOverrides}
          hasDSNOverrides={detail.hasDSNOverrides}
          onEdit={onEditDSN}
        />

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
