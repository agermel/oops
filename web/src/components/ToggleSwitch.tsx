export function ToggleSwitch({
  checked,
  disabled,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="tool-toggle">
      <input
        type="checkbox"
        className="toggle-input"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span className={`toggle-track ${disabled ? "toggle-busy" : ""}`}>
        <span className="toggle-thumb" />
      </span>
    </label>
  );
}
