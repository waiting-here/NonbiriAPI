import { useId, type ReactNode } from 'react';
import './choice-row.css';

/** One selectable resource. Keep other actions beside the label so they act independently. */
export function ChoiceRow({
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
  onChange: (checked: boolean) => void;
}) {
  const id = useId();
  return (
    <label className="nb-choice-row">
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        aria-labelledby={id}
        aria-describedby={description ? id + '-description' : undefined}
        onChange={(event) => onChange(event.currentTarget.checked)}
      />
      <span className="nb-choice-row__content">
        <strong id={id}>{label}</strong>
        {description ? <span id={id + '-description'}>{description}</span> : null}
      </span>
    </label>
  );
}
