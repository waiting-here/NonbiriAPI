import { useRef } from 'react';
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
  const previousAmount = useRef('');
  const unlimited = value === null;
  const noLimit = t('common.operations.charity.noLimit');
  return (
    <div className="nb-stack nb-stack--sm nb-field--short">
      <QuotaAmountField
        label={label}
        inputMode={inputMode}
        value={value}
        disabled={unlimited}
        required={!unlimited}
        onChange={onChange}
      />
      <label className="checkbox-label">
        <input
          type="checkbox"
          aria-label={label + ' · ' + noLimit}
          checked={unlimited}
          onChange={(event) => {
            if (event.currentTarget.checked) {
              previousAmount.current = value ?? '';
              onChange(null);
            } else {
              onChange(previousAmount.current);
            }
          }}
        />
        <span>{noLimit}</span>
      </label>
    </div>
  );
}
