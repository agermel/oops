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
}: {
  alive: boolean;
  loading?: boolean;
  unknown?: boolean;
}) {
  if (loading) {
    return <span className="status-dot loading" />;
  }
  if (unknown) {
    return <span className="status-dot unknown" />;
  }
  return (
    <span className={`status-dot ${alive ? "alive" : "dead"}`} />
  );
}

export function TypePill({ label }: { label: string }) {
  return <span className="type-pill">{label}</span>;
}
