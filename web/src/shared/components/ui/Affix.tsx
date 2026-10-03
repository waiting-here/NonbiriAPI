import { useId, type InputHTMLAttributes, type ReactNode } from 'react';

export function Affix({
  unit,
  className,
  'aria-describedby': describedBy,
  ...input
}: InputHTMLAttributes<HTMLInputElement> & { unit: ReactNode }) {
  const unitId = useId();
  return (
    <span className="nb-affix">
      <input
        {...input}
        className={`nb-input${className ? ` ${className}` : ''}`}
        aria-describedby={[describedBy, unitId].filter(Boolean).join(' ')}
      />
      <span id={unitId} className="nb-affix__unit">
        {unit}
      </span>
    </span>
  );
}
