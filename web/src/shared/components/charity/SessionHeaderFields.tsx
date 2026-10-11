import { useTranslation } from 'react-i18next';
import { Field } from '@shared/components/ui/Field';
import { Fold } from '@shared/components/ui/Fold';
import { Button } from '@shared/components/ui/Button';
import type { SessionHeader } from '@shared/operations/charity';

export function SessionHeaderFields({
  value,
  onChange,
}: {
  value: SessionHeader[];
  onChange: (headers: SessionHeader[]) => void;
}) {
  const { t } = useTranslation();
  const update = (index: number, patch: Partial<SessionHeader>) =>
    onChange(value.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  return (
    <Fold
      title={t('common.operations.charity.sessionHeaders.title')}
      defaultOpen={value.length > 0}
    >
      <div className="nb-form">
        <p className="nb-sub">{t('common.operations.charity.sessionHeaders.help')}</p>
        {value.map((row, i) => (
          <div className="nb-form" key={i}>
            <Field label={t('common.operations.charity.sessionHeaders.name', { n: i + 1 })}>
              {(props) => (
                <input
                  {...props}
                  value={row.name}
                  placeholder="X-Session-ID"
                  maxLength={128}
                  onChange={(e) => update(i, { name: e.target.value })}
                />
              )}
            </Field>
            <div className="nb-grid nb-grid--2">
              <Field label={t('common.operations.charity.sessionHeaders.idle')}>
                {(props) => (
                  <input
                    {...props}
                    type="number"
                    min={1}
                    max={86400}
                    value={row.idle_seconds || ''}
                    onChange={(e) => update(i, { idle_seconds: Number(e.target.value) })}
                  />
                )}
              </Field>
              <Field label={t('common.operations.charity.sessionHeaders.format')}>
                {(props) => (
                  <select
                    {...props}
                    value={row.format}
                    onChange={(e) =>
                      update(i, { format: e.target.value as SessionHeader['format'] })
                    }
                  >
                    <option value="uuid_v4">UUID v4</option>
                    <option value="hex_32">
                      {t('common.operations.charity.sessionHeaders.hex')}
                    </option>
                  </select>
                )}
              </Field>
            </div>
            <div>
              <Button
                type="button"
                variant="ghost"
                onClick={() => onChange(value.filter((_, n) => i !== n))}
              >
                {t('common.operations.charity.sessionHeaders.remove', { n: i + 1 })}
              </Button>
            </div>
          </div>
        ))}
        <div>
          <Button
            type="button"
            disabled={value.length >= 16}
            onClick={() => onChange([...value, { name: '', idle_seconds: 300, format: 'uuid_v4' }])}
          >
            {t('common.operations.charity.sessionHeaders.add')}
          </Button>
        </div>
      </div>
    </Fold>
  );
}
