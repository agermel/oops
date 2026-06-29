export function StatusPill({
  status,
  labelMap,
}: {
  status: string;
  labelMap: Record<string, string>;
}) {
  const normalized = status || "unknown";
  return (
    <span className={`status-pill ${normalized}`}>
      {labelMap[normalized] || "未知"}
    </span>
  );
}

export function StatusDot({
  alive,
  loading = false,
  unknown = false,
  title,
}: {
  alive: boolean;
  loading?: boolean;
  unknown?: boolean;
  title?: string;
}) {
  if (loading) {
    return <span className="status-dot loading" title={title || "checking"} />;
  }
  if (unknown) {
    return <span className="status-dot unknown" title={title || "unknown"} />;
  }
  return (
    <span className={`status-dot ${alive ? "alive" : "dead"}`} title={title || (alive ? "reachable" : "unreachable")} />
  );
}

export function TypePill({ label }: { label: string }) {
  return <span className="type-pill">{label}</span>;
}
