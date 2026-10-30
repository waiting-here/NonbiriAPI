import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Field } from '@shared/components/ui/Field';
import { Fields, FormActions } from '@shared/components/ui/Fields';
import { Fold } from '@shared/components/ui/Fold';
import { Button } from '@shared/components/ui/Button';
import { DEFAULT_SESSION_HEADER_IDLE, type SessionHeaderDraft } from '@shared/sessionHeaders';

export function SessionHeaderFields({
  value,
  onChange,
  disabled = false,
}: {
  value: SessionHeaderDraft[];
  onChange: (headers: SessionHeaderDraft[]) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const idleHelpID = useId();
  const update = (index: number, patch: Partial<SessionHeaderDraft>) =>
    onChange(value.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  return (
    <Fold
      title={t('common.operations.charity.sessionHeaders.title')}
      defaultOpen={value.length > 0}
      summary={t('common.operations.charity.sessionHeaders.saveHelp')}
    >
      <fieldset className="nb-form ops-unframed" disabled={disabled}>
        <p className="nb-sub">{t('common.operations.charity.sessionHeaders.help')}</p>
        <p className="nb-sub" id={idleHelpID}>
          {t('common.operations.charity.sessionHeaders.idleHelp')}
        </p>
        {value.map((row, i) => (
          <Fields key={i}>
            <Field label={t('common.operations.charity.sessionHeaders.name', { n: i + 1 })}>
              {(props) => (
                <input
                  {...props}
                  value={row.name}
                  placeholder="X-Session-ID"
                  maxLength={128}
                  required
                  onChange={(e) => update(i, { name: e.target.value })}
                />
              )}
            </Field>
            <Field size="short" label={t('common.operations.charity.sessionHeaders.idle')}>
              {(props) => (
                <input
                  {...props}
                  type="number"
                  aria-describedby={idleHelpID}
                  min={0}
                  step={1}
                  max={86400}
                  value={row.idle_seconds}
                  placeholder={String(DEFAULT_SESSION_HEADER_IDLE)}
                  onChange={(e) =>
                    update(i, {
                      idle_seconds: e.target.value === '' ? '' : Number(e.target.value),
                    })
                  }
                />
              )}
            </Field>
            <Field label={t('common.operations.charity.sessionHeaders.format')}>
              {(props) => (
                <select
                  {...props}
                  value={row.format}
                  onChange={(e) =>
                    update(i, { format: e.target.value as SessionHeaderDraft['format'] })
                  }
                >
                  <option value="uuid_v4">UUID v4</option>
                  <option value="hex_32">
                    {t('common.operations.charity.sessionHeaders.hex')}
                  </option>
                  <option value="opencode_session">
                    {t('common.operations.charity.sessionHeaders.opencodeSession')}
                  </option>
                  <option value="opencode_message">
                    {t('common.operations.charity.sessionHeaders.opencodeMessage')}
                  </option>
                  <option value="opencode_part">
                    {t('common.operations.charity.sessionHeaders.opencodePart')}
                  </option>
                </select>
              )}
            </Field>
            <FormActions>
              <Button
                type="button"
                variant="ghost"
                onClick={() => onChange(value.filter((_, n) => i !== n))}
              >
                {t('common.operations.charity.sessionHeaders.remove', { n: i + 1 })}
              </Button>
            </FormActions>
          </Fields>
        ))}
        <FormActions>
          <Button
            type="button"
            disabled={value.length >= 16}
            onClick={() =>
              onChange([
                ...value,
                { name: '', idle_seconds: DEFAULT_SESSION_HEADER_IDLE, format: 'uuid_v4' },
              ])
            }
          >
            {t('common.operations.charity.sessionHeaders.add')}
          </Button>
        </FormActions>
      </fieldset>
    </Fold>
  );
}
