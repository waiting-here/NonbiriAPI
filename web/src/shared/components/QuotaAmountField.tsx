import type { ReactNode } from 'react';
import { Field } from './ui/Field';

interface QuotaAmountFieldProps {
  label: string;
  value: string | null;
  onChange: (text: string) => void;
  inputMode?: 'numeric' | 'decimal';
  placeholder?: string;
  disabled?: boolean;
  help?: ReactNode;
  error?: string;
  required?: boolean;
  maxLength?: number;
}

export function QuotaAmountField({
  label,
  value,
  onChange,
  inputMode = 'numeric',
  help,
  error,
  ...input
}: QuotaAmountFieldProps) {
  return (
    <Field label={label} help={help} error={error}>
      {(field) => (
        <input
          {...field}
          {...input}
          inputMode={inputMode}
          type="text"
          value={value ?? ''}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
    </Field>
  );
}
