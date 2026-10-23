import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { apiFetch, isForbidden, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import type { DiagnosticRole } from './api';
import './globalPressure.css';

interface Metric {
  current: number;
  limit: number;
  peak_24h: number;
  peak_all: number;
}

interface Pressure {
  rpm: Metric;
  concurrency: Metric;
  observed_at: number;
  started_at: number;
  sample_seconds: number;
}

function width(value: number, limit: number) {
  return `${limit > 0 ? Math.min(100, Math.max(0, (value / limit) * 100)) : 0}%`;
}

function PressureBar({ label, value }: { label: string; value: Metric }) {
  const { t } = useTranslation();
  const description = t('common.operations.logs.pressure.values', {
    current: value.current,
    limit: value.limit,
    day: value.peak_24h,
    all: value.peak_all,
  });
  return (
    <div className="global-pressure__metric">
      <div className="global-pressure__heading">
        <strong>{label}</strong>
        <span>
          {t('common.operations.logs.pressure.currentLimit', {
            current: value.current,
            limit: value.limit,
          })}
        </span>
      </div>
      <div
        className="global-pressure__track"
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={Math.max(1, value.limit)}
        aria-valuenow={Math.min(value.current, Math.max(1, value.limit))}
        aria-valuetext={description}
      >
        <span
          className="global-pressure__fill global-pressure__fill--all"
          style={{ width: width(value.peak_all, value.limit) }}
        />
        <span
          className="global-pressure__fill global-pressure__fill--day"
          style={{ width: width(value.peak_24h, value.limit) }}
        />
        <span
          className="global-pressure__fill global-pressure__fill--current"
          style={{ width: width(value.current, value.limit) }}
        />
      </div>
      <div className="global-pressure__legend" aria-hidden="true">
        <span>
          <i className="global-pressure__fill--current" />
          {t('common.operations.logs.pressure.current', { value: value.current })}
        </span>
        <span>
          <i className="global-pressure__fill--day" />
          {t('common.operations.logs.pressure.day', { value: value.peak_24h })}
        </span>
        <span>
          <i className="global-pressure__fill--all" />
          {t('common.operations.logs.pressure.all', { value: value.peak_all })}
        </span>
      </div>
    </div>
  );
}

export function GlobalPressure({ role, accountID }: { role: DiagnosticRole; accountID: string }) {
  const { t } = useTranslation();
  const formatTime = useDateTimeFormatter();
  const pressure = useQuery({
    queryKey: [role, 'global-pressure', accountID],
    queryFn: ({ signal }) =>
      apiFetch<Pressure>(`${role === 'admin' ? '/admin/api' : '/api/steward'}/logs/pressure`, {
        signal,
      }),
    enabled: accountID !== '',
    retry: false,
    staleTime: 10_000,
    refetchInterval: (query) =>
      isUnauthorized(query.state.error) || isForbidden(query.state.error) ? false : 15_000,
    refetchIntervalInBackground: false,
  });
  if (isUnauthorized(pressure.error) || isForbidden(pressure.error)) return null;
  const value = pressure.data;
  return (
    <section className="global-pressure" aria-label={t('common.operations.logs.pressure.title')}>
      <div className="global-pressure__title">
        <h3>{t('common.operations.logs.pressure.title')}</h3>
        {value?.observed_at ? (
          <small>
            {t('common.operations.logs.pressure.updated', { time: formatTime(value.observed_at) })}
          </small>
        ) : null}
      </div>
      {value?.observed_at ? (
        <>
          <div className="global-pressure__metrics">
            <PressureBar label={t('common.operations.logs.pressure.rpm')} value={value.rpm} />
            <PressureBar
              label={t('common.operations.logs.pressure.concurrency')}
              value={value.concurrency}
            />
          </div>
          <small>
            {t('common.operations.logs.pressure.help', {
              seconds: value.sample_seconds,
              time: formatTime(value.started_at),
            })}
          </small>
        </>
      ) : (
        <p role="status">
          {t(pressure.error ? 'common.operations.logs.pressure.unavailable' : 'common.loading')}
        </p>
      )}
      {value && pressure.error ? (
        <small role="status">{t('common.operations.logs.pressure.stale')}</small>
      ) : null}
    </section>
  );
}
