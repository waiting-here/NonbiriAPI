import { useState } from 'react';
import { QuotaAmountField } from '../QuotaAmountField';
import { useTranslation } from 'react-i18next';

export function NullableValue({
  label,
  value,
  onChange,
  inputMode,
}: {
  label: string;
  inputMode?: 'numeric' | 'decimal';
  value: string | null;
  onChange: (value: string | null) => void;
}) {
  const { t } = useTranslation();
  const [unlimited, setUnlimited] = useState(value === null);
  return (
    <div className="nb-stack nb-stack--sm nb-field--short">
      <QuotaAmountField
        label={label}
        inputMode={inputMode}
        value={value}
        disabled={unlimited}
        onChange={onChange}
      />
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
