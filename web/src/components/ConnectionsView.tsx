import { RefreshCw, Search, ChevronDown, Gauge, TerminalSquare } from "lucide-react";
import type { StatusItem } from "../types";
import { Metric } from "./Metric";
import { ConnectionRow } from "./ConnectionRow";

export function ConnectionsView({
  items,
  loading,
  error,
  counters,
  onRefresh,
}: {
  items: StatusItem[];
  loading: boolean;
  error: string;
  counters: { total: number; alive: number; dead: number; unknown: number };
  onRefresh: () => void;
}) {
  return (
    <>
      <section className="metrics" aria-label="连接状态统计">
        <Metric label="TOTAL" value={counters.total} tone="neutral" />
        <Metric label="ALIVE" value={counters.alive} tone="alive" />
        <Metric label="DEAD" value={counters.dead} tone="dead" />
        <Metric label="UNKNOWN" value={counters.unknown} tone="unknown" />
      </section>

      <section className="panel">
        <div>
          <div className="panel-summary">
            <Gauge size={16} />
            <span>{loading ? "探测中" : `${items.length} 个连接`}</span>
          </div>
        </div>

        {error && <div className="error-line">{error}</div>}

        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th className="check-cell">
                  <span className="checkbox" />
                </th>
                <th>标题</th>
                <th>类型</th>
                <th>地址</th>
                <th>状态</th>
                <th>延迟</th>
                <th>诊断</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {loading && items.length === 0 ? (
                <tr>
                  <td colSpan={8} className="empty">
                    <TerminalSquare size={16} />
                    <span>正在读取连接状态</span>
                  </td>
                </tr>
              ) : (
                items.map((item) => <ConnectionRow key={item.connection.id} item={item} />)
              )}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
