import { Edit3 } from "lucide-react";
import type { StatusItem } from "../types";
import { statusIcon } from "../types";

export function ConnectionRow({ item }: { item: StatusItem }) {
  const status = item.result?.status ?? "unknown";
  const Icon = statusIcon[status];

  return (
    <tr>
      <td className="check-cell">
        <span className="checkbox" />
      </td>
      <td>
        <div className="service-name">{item.connection.name || item.connection.id}</div>
        <div className="service-id">{item.connection.id}</div>
      </td>
      <td>
        <span className="type-pill">{item.connection.type}</span>
      </td>
      <td className="address">{item.connection.address}</td>
      <td>
        <span className={`status ${status}`}>
          <Icon size={15} />
          {status}
        </span>
      </td>
      <td>{item.result?.latency ?? 0}ms</td>
      <td className="message">{item.error || item.result?.message || "-"}</td>
      <td className="action-cell">
        <button title="编辑连接">
          <Edit3 size={18} />
        </button>
      </td>
    </tr>
  );
}
