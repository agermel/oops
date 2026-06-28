import { Gauge, RefreshCw } from "lucide-react";
import type { HealthResult } from "../types";

export function ContainerHealth({
  health,
  loading,
  onCheck,
}: {
  health?: HealthResult;
  loading: boolean;
  onCheck: () => void;
}) {
  return (
    <div className="container-health">
      <div className="section-title">
        <Gauge size={18} />
        <h2>健康检查</h2>
        <button className="primary-button small" onClick={onCheck} disabled={loading}>
          <RefreshCw size={15} className={loading ? "spin" : ""} />
          <span>{loading ? "探测中" : "立即探测"}</span>
        </button>
      </div>

      {health ? (
        <div className="health-result">
          <div className={`health-status-card ${health.status}`}>
            <span className={`status-dot large ${health.status}`} />
            <div>
              <strong>{health.status === "alive" ? "可达" : health.status === "dead" ? "不可达" : "未知"}</strong>
              <small>{health.latency > 0 ? `${health.latency}ms` : ""}</small>
            </div>
          </div>
          {health.message && <p className="health-message">{health.message}</p>}
        </div>
      ) : (
        <div className="empty-card">点击"立即探测"检查服务连通性</div>
      )}
    </div>
  );
}
