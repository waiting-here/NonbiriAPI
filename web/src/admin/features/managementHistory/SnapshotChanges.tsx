import { useTranslation } from 'react-i18next';
import { Amount } from '@shared/components/Amount';
import { Fold, Note } from '@shared/components/ui';
import { formatCount } from '@shared/utils/formatNumber';
import { formatDateTime } from '@shared/utils/datetime';
import { FAILURE_RESET_REASON_KEYS } from '@shared/operations/failureReset';
import { NATIVE_SERVICE_LABEL_KEYS, type NativeService } from '@shared/nativeServices';
import type { Change, Item } from './api';

function ChangeValue({ value, change }: { value?: string; change: Change }) {
  const { t } = useTranslation();
  if (value === undefined)
    return <span className="nb-muted">{t('admin.managementHistory.snapshot.notSet')}</span>;
  if (change.field === 'reset_skip_reason')
    return (
      <span>{FAILURE_RESET_REASON_KEYS[value] ? t(FAILURE_RESET_REASON_KEYS[value]) : value}</span>
    );
  if (change.field === 'native_service') {
    const key = NATIVE_SERVICE_LABEL_KEYS[value as NativeService];
    return <span>{key ? t(key) : value}</span>;
  }
  if (change.field === 'reset_result')
    return (
      <span>
        {value === 'recovered'
          ? t('admin.managementHistory.snapshot.resetResults.recovered')
          : value === 'already_clear'
            ? t('admin.managementHistory.snapshot.resetResults.already_clear')
            : value}
      </span>
    );
  if (change.unit === 'milli_credits') return <Amount value={value} />;
  if (change.unit === 'unix_seconds' && /^\d+$/.test(value))
    return <span>{formatDateTime(Number(value))}</span>;
  if (change.field.startsWith('permissions.'))
    return (
      <span>
        {t(value === '1' ? 'management.permissions.allowed' : 'management.permissions.notAllowed')}
      </span>
    );
  return (
    <span className="management-history__value">
      {value || t('admin.managementHistory.snapshot.empty')}
      {change.unit
        ? ' ' +
          t(`admin.managementHistory.snapshot.units.${change.unit}`, { defaultValue: change.unit })
        : ''}
    </span>
  );
}

export function SnapshotChanges({ item }: { item: Item }) {
  const { t } = useTranslation();
  const details = item.details;
  const label = (field: string) => {
    if (field.startsWith('permissions.') || field.startsWith('limits.')) {
      const [group, ...parts] = field.split('.');
      return t(
        `management.permissions.${group === 'permissions' ? 'labels' : 'limits'}.${parts.join('_')}`,
        { defaultValue: field },
      );
    }
    return t(`admin.managementHistory.snapshot.fields.${field.replaceAll('.', '_')}`, {
      defaultValue: field.replaceAll('_', ' '),
    });
  };
  return (
    <div className="nb-stack nb-stack--sm management-history__snapshot">
      {!details?.version ? (
        <p className="nb-small nb-muted">{t('admin.managementHistory.snapshot.legacy')}</p>
      ) : null}
      {details?.version && !details.state && !item.changes.length && !details.related?.length ? (
        <p className="nb-small nb-muted">{t('admin.managementHistory.snapshot.outcomeOnly')}</p>
      ) : null}
      {details?.previous_name ? (
        <p>{t('admin.managementHistory.snapshot.previousName', { name: details.previous_name })}</p>
      ) : null}
      {details?.state ? (
        <p className="nb-small">
          {t(`admin.managementHistory.snapshot.states.${details.state}`, {
            defaultValue: details.state,
          })}
        </p>
      ) : null}
      {item.changes.length ? (
        <dl className="management-history__changes">
          {item.changes.map((change) => (
            <div key={change.field}>
              <dt title={change.field}>{label(change.field)}</dt>
              <dd>
                {change.operation === 'retained' ? (
                  <>
                    <ChangeValue value={change.after} change={change} /> ·{' '}
                    {t('admin.managementHistory.snapshot.operations.retained')}
                  </>
                ) : change.before !== undefined || change.after !== undefined ? (
                  <>
                    <span className="nb-small nb-muted">
                      {t('admin.managementHistory.snapshot.before')}{' '}
                    </span>
                    <ChangeValue value={change.before} change={change} />
                    <span className="nb-small nb-muted">
                      {' '}
                      → {t('admin.managementHistory.snapshot.after')}{' '}
                    </span>
                    <ChangeValue value={change.after} change={change} />
                  </>
                ) : (
                  t(`admin.managementHistory.snapshot.operations.${change.operation}`, {
                    defaultValue: change.operation,
                  })
                )}
                {change.truncated ? (
                  <small className="nb-muted">
                    {' '}
                    · {t('admin.managementHistory.snapshot.shortened')}
                  </small>
                ) : null}
              </dd>
            </div>
          ))}
        </dl>
      ) : details?.state === 'unchanged' ? (
        <p>{t('admin.managementHistory.outcomes.no_change')}</p>
      ) : null}
      {details?.impacts?.length ? (
        <Fold title={t('admin.managementHistory.snapshot.impacts')} defaultOpen>
          <p className="nb-small nb-muted">{t('admin.managementHistory.snapshot.impactHelp')}</p>
          <ul className="management-history__impact-list">
            {details.impacts.map((impact) => (
              <li key={impact.kind}>
                <strong>
                  {t(`admin.managementHistory.snapshot.kinds.${impact.kind}`, {
                    defaultValue: impact.kind,
                  })}
                </strong>
                <span>
                  {t('admin.managementHistory.snapshot.impactCounts', {
                    created: formatCount(impact.created).display,
                    updated: formatCount(impact.updated).display,
                    deleted: formatCount(impact.deleted).display,
                  })}
                </span>
              </li>
            ))}
          </ul>
        </Fold>
      ) : null}
      {details?.related?.length ? (
        <Fold title={t('admin.managementHistory.snapshot.related')}>
          <ul className="management-history__impact-list">
            {details.related.map((related, index) => (
              <li key={`${related.kind}:${related.id}:${index}`}>
                <span title={related.name}>
                  {related.name || t('admin.managementHistory.snapshot.unnamed')}
                  {related.state
                    ? ' · ' +
                      t(`admin.managementHistory.snapshot.states.${related.state}`, {
                        defaultValue: related.state,
                      })
                    : null}
                </span>
                {related.previous_name ? (
                  <small>
                    {t('admin.managementHistory.snapshot.previousName', {
                      name: related.previous_name,
                    })}
                  </small>
                ) : null}
                {related.changes?.map((change) => (
                  <small key={change.field}>
                    {label(change.field)}:{' '}
                    {change.operation === 'retained' ? (
                      <>
                        <ChangeValue value={change.after} change={change} /> ·{' '}
                        {t('admin.managementHistory.snapshot.operations.retained')}
                      </>
                    ) : change.before !== undefined || change.after !== undefined ? (
                      <>
                        <ChangeValue value={change.before} change={change} /> →{' '}
                        <ChangeValue value={change.after} change={change} />
                      </>
                    ) : (
                      t(`admin.managementHistory.snapshot.operations.${change.operation}`, {
                        defaultValue: change.operation,
                      })
                    )}
                    {change.truncated
                      ? ' · ' + t('admin.managementHistory.snapshot.shortened')
                      : ''}
                  </small>
                ))}
                <small className="nb-muted mono">{related.id}</small>
              </li>
            ))}
          </ul>
        </Fold>
      ) : null}
      {details?.truncated ? <Note>{t('admin.managementHistory.snapshot.truncated')}</Note> : null}
    </div>
  );
}
