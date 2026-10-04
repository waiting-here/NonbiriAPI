import { useId, type ReactNode } from 'react';

export function Segmented<V extends string>({
  label,
  value,
  options,
  onChange,
  disabled,
  describedBy,
}: {
  label: string;
  value: V;
  options: readonly { value: V; label: ReactNode }[];
  onChange: (next: V) => void;
  disabled?: boolean;
  describedBy?: string;
}) {
  const name = useId();
  return (
    <div className="nb-seg" role="radiogroup" aria-label={label} aria-describedby={describedBy}>
      {options.map((option) => (
        <label key={option.value}>
          <input
            type="radio"
            name={name}
            value={option.value}
            checked={value === option.value}
            disabled={disabled}
            onChange={() => onChange(option.value)}
          />
          <span>{option.label}</span>
        </label>
      ))}
    </div>
  );
}
