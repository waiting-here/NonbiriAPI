import { useId, type ReactNode } from 'react';

export function Toggle({
  label,
  description,
  checked,
  disabled,
  onChange,
}: {
  label: ReactNode;
  description?: ReactNode;
  checked: boolean;
  disabled?: boolean;
  onChange: (next: boolean) => void;
}) {
  const id = useId();
  return (
    <label className="nb-toggle" htmlFor={id}>
      <span className="nb-toggle__text">
        <strong id={`${id}-label`}>{label}</strong>
        {description ? <span id={`${id}-description`}>{description}</span> : null}
      </span>
      <input
        id={id}
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        aria-checked={checked}
        aria-labelledby={`${id}-label`}
        aria-describedby={description ? `${id}-description` : undefined}
        onChange={(event) => onChange(event.target.checked)}
      />
    </label>
  );
}
