import { useState } from 'react';
import { Field } from '@shared/components/ui';

export function NumberField({
  label,
  value,
  min,
  max,
  step = 1,
  onChange,
}: {
  label: string;
  value: number;
  min?: number;
  max?: number;
  step?: number;
  onChange(value: number): void;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  const finish = () => {
    if (draft === null) return;
    const next = Number(draft);
    setDraft(null);
    if (
      draft.trim() &&
      Number.isFinite(next) &&
      Math.abs(next / step - Math.round(next / step)) < 0.00001 &&
      next !== value
    )
      onChange(next);
  };
  return (
    <Field size="short" label={label}>
      {(props) => (
        <input
          {...props}
          type="number"
          value={draft ?? value}
          min={min}
          max={max}
          step={step}
          onChange={(event) => setDraft(event.target.value)}
          onBlur={finish}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              event.currentTarget.blur();
            } else if (event.key === 'Escape') setDraft(null);
          }}
        />
      )}
    </Field>
  );
}
