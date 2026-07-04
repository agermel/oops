import React from "react";
import { Edit3 } from "lucide-react";
import type { DSNInfo } from "../types";
import { DSN_STANDARD_FIELDS } from "../types";
import { Button } from "./ui/Button";

function monoClass(override: boolean): string {
  return `mono${override ? " dsn-overridden" : ""}`;
}

export function DSNInfoCard({
  dsn,
  dsnOverrides,
  hasDSNOverrides,
  onEdit,
}: {
  dsn?: DSNInfo;
  dsnOverrides?: Record<string, string>;
  hasDSNOverrides: boolean;
  onEdit?: () => void;
}) {
  const overrideKeys = new Set(Object.keys(dsnOverrides || {}));

  // 有自动检测 DSN 或有用户覆盖值
  if (dsn || hasDSNOverrides) {
    return (
      <div className="overview-card">
        <div className="overview-card-head">
          <h3>连接信息 (DSN)</h3>
          <Button variant="ghost" size="sm" onClick={() => onEdit?.()}>
            <Edit3 size={13} />
            <span>编辑</span>
          </Button>
        </div>
        <dl>
          {(dsn?.host || overrideKeys.has("host")) && (
            <>
              <dt>主机</dt>
              <dd className={monoClass(overrideKeys.has("host"))}>{dsn?.host || dsnOverrides?.host || "-"}</dd>
            </>
          )}
          {(dsn?.port && dsn.port > 0 || overrideKeys.has("port")) && (
            <>
              <dt>端口</dt>
              <dd className={monoClass(overrideKeys.has("port"))}>{dsn?.port || dsnOverrides?.port || "-"}</dd>
            </>
          )}
          {(dsn?.user || overrideKeys.has("user")) && (
            <>
              <dt>用户</dt>
              <dd className={monoClass(overrideKeys.has("user"))}>{dsn?.user || dsnOverrides?.user || "-"}</dd>
            </>
          )}
          {(dsn?.database || overrideKeys.has("database")) && (
            <>
              <dt>数据库</dt>
              <dd className={monoClass(overrideKeys.has("database"))}>{dsn?.database || dsnOverrides?.database || "-"}</dd>
            </>
          )}
          {(dsn?.raw || overrideKeys.has("raw")) && (
            <>
              <dt>完整 DSN</dt>
              <dd className={`mono dsn-raw ${overrideKeys.has("raw") ? "dsn-overridden" : ""}`}>
                {dsn?.raw || dsnOverrides?.raw || "-"}
              </dd>
            </>
          )}
          {/* 额外的覆盖键值对（不在标准 DSN 字段内） */}
          {dsnOverrides && Object.entries(dsnOverrides)
            .filter(([k]) => !DSN_STANDARD_FIELDS.includes(k as any))
            .map(([k, v]) => (
              <React.Fragment key={k}>
                <dt>{k}</dt>
                <dd className="mono dsn-overridden">{v}</dd>
              </React.Fragment>
            ))}
        </dl>
      </div>
    );
  }

  // 无 DSN 也无覆盖值：显示配置入口
  return (
    <div className="overview-card">
      <div className="overview-card-head">
        <h3>连接信息</h3>
        <Button variant="ghost" size="sm" onClick={() => onEdit?.()}>
          <Edit3 size={13} />
          <span>配置</span>
        </Button>
      </div>
      <p className="dsn-config-hint">此容器未自动检测到 DSN 信息，您可以手动添加连接参数。</p>
    </div>
  );
}
