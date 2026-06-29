export function StatusPill({
  status,
  labelMap,
}: {
  status: string;
  labelMap: Record<string, string>;
}) {
  return (
    <span className={`status-pill ${status}`}>
      {labelMap[status] || status}
    </span>
  );
}

export function StatusDot({ alive }: { alive: boolean }) {
  return (
    <span className={`status-dot ${alive ? "alive" : "dead"}`} />
  );
}

export function TypePill({ label }: { label: string }) {
  return <span className="type-pill">{label}</span>;
}
