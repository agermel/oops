export function Metric({ label, value, tone }: { label: string; value: number; tone: "neutral" | "alive" | "dead" | "unknown" }) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
