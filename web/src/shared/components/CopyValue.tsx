import { Button } from '@shared/components/ui/Button';
import { useState, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { copyText } from '@shared/utils/clipboard';

export function CopyValue({
  value,
  label,
  showValue = true,
  variant = 'ghost',
}: {
  value: string;
  label: string;
  showValue?: boolean;
  /** `secondary` for a row's main action, such as copying a model name. */
  variant?: 'ghost' | 'secondary';
}) {
  const { t } = useTranslation();
  const [result, setResult] = useState<{ value: string; ok: boolean }>();
  useEffect(() => {
    if (!result) return;
    const timer = window.setTimeout(() => setResult(undefined), 2500);
    return () => window.clearTimeout(timer);
  }, [result]);
  return (
    <span className="nb-copy-value">
      {showValue ? <code>{value}</code> : null}
      <Button
        type="button"
        variant={variant}
        aria-label={t('common.copyValue', { label })}
        onClick={() => {
          void copyText(value).then((ok) => setResult({ value, ok }));
        }}
      >
        {result?.value === value && result.ok ? t('common.copied') : t('common.copy')}
      </Button>
      <span role="status" className={result?.ok ? 'sr-only' : undefined}>
        {result?.value === value ? t(result.ok ? 'common.copied' : 'common.copyFailed') : ''}
      </span>
    </span>
  );
}
