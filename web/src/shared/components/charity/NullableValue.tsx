import { useId, useState } from 'react';
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
  const valueId = useId();
  const unlimitedId = useId();
  const [unlimited, setUnlimited] = useState(value === null);
  return (
    <div className="ops-form-field">
      <label htmlFor={valueId}>{label}</label>
      <input
        id={valueId}
        value={value ?? ''}
        disabled={unlimited}
        onChange={(event) => onChange(event.target.value)}
      />
      <label className="checkbox-label" htmlFor={unlimitedId}>
        <input
          id={unlimitedId}
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
