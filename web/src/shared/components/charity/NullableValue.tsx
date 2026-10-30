import { useState } from 'react';
import { Field } from '../ui';
import { useTranslation } from 'react-i18next';

export function NullableValue({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string | null;
  onChange: (value: string | null) => void;
}) {
  const { t } = useTranslation();
  const [unlimited, setUnlimited] = useState(value === null);
  return (
    <div className="nb-stack nb-stack--sm nb-field--short">
      <Field label={label}>
        {(field) => (
          <input
            {...field}
            value={value ?? ''}
            disabled={unlimited}
            onChange={(event) => onChange(event.target.value)}
          />
        )}
      </Field>
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={unlimited}
          onChange={(event) => {
            setUnlimited(event.target.checked);
            onChange(event.target.checked ? null : '0');
          }}
        />
        <span>{t('common.operations.charity.noLimit')}</span>
      </label>
    </div>
  );
}
