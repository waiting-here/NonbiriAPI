import { useId, type ReactNode } from 'react';

export function OptionCards<V extends string>({
  legend,
  value,
  options,
  onChange,
}: {
  legend: ReactNode;
  value: V;
  options: readonly { value: V; title: ReactNode; body?: ReactNode; disabled?: boolean }[];
  onChange: (next: V) => void;
}) {
  const name = useId();
  return (
    <fieldset className="nb-fieldset">
      <legend>{legend}</legend>
      <div className="nb-options">
        {options.map((option) => (
          <label key={option.value} className="nb-option">
            <input
              type="radio"
              name={name}
              value={option.value}
              checked={value === option.value}
              disabled={option.disabled}
              aria-labelledby={`${name}-${option.value}-title`}
              aria-describedby={option.body ? `${name}-${option.value}-body` : undefined}
              onChange={() => onChange(option.value)}
            />
            <strong id={`${name}-${option.value}-title`}>{option.title}</strong>
            {option.body ? <span id={`${name}-${option.value}-body`}>{option.body}</span> : null}
          </label>
        ))}
      </div>
    </fieldset>
  );
}
