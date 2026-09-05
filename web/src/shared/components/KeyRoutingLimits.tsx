import { useTranslation } from 'react-i18next';

export function KeyLimitFields({
  concurrency,
  rpm,
  onConcurrency,
  onRPM,
  disabled = false,
}: {
  concurrency: string;
  rpm: string;
  onConcurrency: (value: string) => void;
  onRPM: (value: string) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <fieldset className="nb-key-limits" disabled={disabled}>
      <legend>{t('common.keyLimits.title')}</legend>
      <div className="nb-key-limits__fields">
        <label>
          <span>{t('common.keyLimits.concurrency')}</span>
          <input
            type="number"
            min="0"
            max="2147483647"
            step="1"
            required
            value={concurrency}
            onChange={(event) => onConcurrency(event.target.value)}
          />
        </label>
        <label>
          <span>{t('common.keyLimits.rpm')}</span>
          <input
            type="number"
            min="0"
            max="2147483647"
            step="1"
            required
            value={rpm}
            onChange={(event) => onRPM(event.target.value)}
          />
        </label>
      </div>
      <p className="nb-key-limits__hint">{t('common.keyLimits.hint')}</p>
    </fieldset>
  );
}

export function KeyLimitSummary({
  concurrency,
  rpm,
  readOnly = false,
}: {
  concurrency: number | null | undefined;
  rpm: number | null | undefined;
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  const display = (value: number | null | undefined) =>
    value == null
      ? t('common.notAvailable')
      : value === 0
        ? t('common.keyLimits.unlimited')
        : String(value);
  return (
    <span className="nb-key-limits__summary">
      {readOnly ? <span>{t('common.keyLimits.readOnly')}</span> : null}
      <span>
        {t('common.keyLimits.concurrency')}: {display(concurrency)}
      </span>
      <span>
        {t('common.keyLimits.rpm')}: {display(rpm)}
      </span>
    </span>
  );
}
