import { Fields } from '@shared/components/ui/Fields';
import { Field } from '@shared/components/ui/Field';
import type { KeyQueueDraft, KeyQueueProjection } from '@shared/operations/keyQueue';
import { formatCount } from '@shared/utils/formatNumber';
import { useTranslation } from 'react-i18next';

const dimensionCopy = {
  concurrency: {
    limit: 'common.keyLimits.concurrency',
    overflow: 'common.keyLimits.concurrencyOverflow',
    queueLimit: 'common.keyLimits.concurrencyQueueLimit',
  },
  rpm: {
    limit: 'common.keyLimits.rpm',
    overflow: 'common.keyLimits.rpmOverflow',
    queueLimit: 'common.keyLimits.rpmQueueLimit',
  },
} as const;

export function KeyLimitFields({
  concurrency,
  rpm,
  queue,
  onConcurrency,
  onRPM,
  onQueue,
  form,
  disabled = false,
}: {
  concurrency: string;
  rpm: string;
  queue: KeyQueueDraft;
  onConcurrency: (value: string) => void;
  onRPM: (value: string) => void;
  onQueue: (value: KeyQueueDraft) => void;
  form?: string;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <fieldset className="nb-key-limits" disabled={disabled}>
      <legend>{t('common.keyLimits.title')}</legend>
      <div className="nb-key-limits__dimensions">
        {(['concurrency', 'rpm'] as const).map((dimension) => {
          const overflow = `${dimension}_overflow` as const;
          const queueLimit = `${dimension}_queue_limit` as const;
          return (
            <Fields columns={1} key={dimension}>
              <Field label={t(dimensionCopy[dimension].limit)} size="short">
                {(field) => (
                  <input
                    {...field}
                    form={form}
                    type="number"
                    min="0"
                    max="2147483647"
                    step="1"
                    required
                    value={dimension === 'concurrency' ? concurrency : rpm}
                    onChange={(event) =>
                      (dimension === 'concurrency' ? onConcurrency : onRPM)(event.target.value)
                    }
                  />
                )}
              </Field>
              <Field label={t(dimensionCopy[dimension].overflow)}>
                {(field) => (
                  <select
                    {...field}
                    form={form}
                    value={queue[overflow]}
                    onChange={(event) => onQueue({ ...queue, [overflow]: event.target.value })}
                  >
                    <option value="reject">{t('common.keyLimits.reject')}</option>
                    <option value="queue">{t('common.keyLimits.queue')}</option>
                  </select>
                )}
              </Field>
              {queue[overflow] === 'queue' ? (
                <Field
                  label={t(dimensionCopy[dimension].queueLimit)}
                  help={t('common.keyLimits.queueLimitHint')}
                  size="short"
                >
                  {(field) => (
                    <input
                      {...field}
                      form={form}
                      type="number"
                      min="0"
                      max="2147483647"
                      step="1"
                      value={queue[queueLimit]}
                      onChange={(event) => onQueue({ ...queue, [queueLimit]: event.target.value })}
                    />
                  )}
                </Field>
              ) : null}
            </Fields>
          );
        })}
      </div>
      <p className="nb-key-limits__hint">{t('common.keyLimits.hint')}</p>
      {queue.concurrency_overflow === 'queue' || queue.rpm_overflow === 'queue' ? (
        <p className="nb-key-limits__hint">{t('common.keyLimits.queueHint')}</p>
      ) : null}
    </fieldset>
  );
}

export function KeyLimitSummary({
  concurrency,
  rpm,
  queue = {},
  readOnly = false,
}: {
  concurrency: number | null | undefined;
  rpm: number | null | undefined;
  queue?: KeyQueueProjection;
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  const display = (value: number | null | undefined) =>
    value == null
      ? t('common.notAvailable')
      : value === 0
        ? t('common.keyLimits.unlimited')
        : formatCount(value).display;
  return (
    <span className="nb-key-limits__summary">
      {readOnly ? <span>{t('common.keyLimits.readOnly')}</span> : null}
      {(['concurrency', 'rpm'] as const).map((dimension) => {
        const value = dimension === 'concurrency' ? concurrency : rpm;
        return (
          <span className="nb-key-limits__dimension" key={dimension}>
            <span>
              {t(dimensionCopy[dimension].limit)}: {display(value)}
            </span>
            {value == null ? null : (
              <span>
                {queue[`${dimension}_overflow`] === 'queue'
                  ? t('common.keyLimits.queueSummary', {
                      limit: display(queue[`${dimension}_queue_limit`] ?? 0),
                    })
                  : t('common.keyLimits.reject')}
              </span>
            )}
          </span>
        );
      })}
    </span>
  );
}
