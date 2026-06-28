import { Gauge, RefreshCw } from "lucide-react";
import type { HealthResult } from "../types";
import { Button } from "./ui/Button";

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
      <div className="section-header">
        <Gauge size={18} />
        <h2>健康检查</h2>
        <Button size="sm" onClick={onCheck} disabled={loading}>
          <RefreshCw size={15} className={loading ? "spin" : ""} />
          <span>{loading ? "探测中" : "立即探测"}</span>
        </Button>
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
        <div className="empty-state">点击"立即探测"检查服务连通性</div>
      )}
    </div>
  );
}
