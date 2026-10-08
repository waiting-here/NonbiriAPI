import { Button } from '@shared/components/ui/Button';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { copyText } from '@shared/utils/clipboard';

export function CopyValue({
  value,
  label,
  showValue = true,
}: {
  value: string;
  label: string;
  showValue?: boolean;
}) {
  const { t } = useTranslation();
  const [result, setResult] = useState<{ value: string; ok: boolean }>();
  return (
    <span className="nb-copy-value">
      {showValue ? <code>{value}</code> : null}
      <Button
        type="button"
        variant="ghost"
        aria-label={t('common.copyValue', { label })}
        onClick={() => {
          void copyText(value).then((ok) => setResult({ value, ok }));
        }}
      >
        {result?.value === value && result.ok ? t('common.copied') : t('common.copy')}
      </Button>
      {result?.value === value && !result.ok ? (
        <span role="status">{t('common.copyFailed')}</span>
      ) : null}
    </span>
  );
}
