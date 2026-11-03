import type { ReactNode } from 'react';
import { RadioGroup, Radio } from 'react-aria-components';

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
  return (
    <RadioGroup
      className="nb-seg"
      aria-label={label}
      aria-describedby={describedBy}
      value={value}
      isDisabled={disabled}
      onChange={(next) => onChange(next as V)}
      orientation="horizontal"
    >
      {options.map((option) => (
        <Radio key={option.value} value={option.value}>
          <span>{option.label}</span>
        </Radio>
      ))}
    </RadioGroup>
  );
}
