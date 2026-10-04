import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import {
  adminCoreKeys,
  getAdminActivity,
  getAdminSiteTimezoneOffset,
  getAdminUsage,
} from '../features/operations/core';
import { adminPageKeys, getAdminEndpointsPage } from '../features/operations/adminPages';
import { adminReportKeys, getReportBadge } from '../features/operations/reports';
import { useAdminSession } from '../data';
import '@shared/operations/operations.css';
import { Fold } from '@shared/components/ui';
import './dashboard.css';

function formatSiteDay(day: number | undefined, offsetMinutes: number | undefined): string {
  if (day === undefined || offsetMinutes === undefined) return '—';
  const match = new Date((day + offsetMinutes * 60) * 1_000)
    .toISOString()
    .match(/^\d{4}-\d{2}-\d{2}/);
  return match?.[0] ?? '—';
}

export function DashboardPage() {
  const { t } = useTranslation();
  const session = useAdminSession();
  const account = session.data?.admin.username ?? '';
  const usage = useQuery({ queryKey: adminCoreKeys.usage, queryFn: getAdminUsage, retry: false });
  const activity = useQuery({
    queryKey: adminCoreKeys.activity(null),
    queryFn: () => getAdminActivity(null),
    retry: false,
  });
  const siteTimezone = useQuery({
    queryKey: adminCoreKeys.siteTimezone,
    queryFn: getAdminSiteTimezoneOffset,
    retry: false,
  });
  const endpoints = useQuery({
    queryKey: adminPageKeys.endpoints(account, '', '1', 20),
    queryFn: ({ signal }) => getAdminEndpointsPage('', '1', 20, signal),
    enabled: Boolean(account) && !session.error,
    retry: false,
  });
  const reports = useQuery({
    queryKey: adminReportKeys.badge,
    queryFn: ({ signal }) => getReportBadge(signal),
    retry: false,
  });
  const endpointError = session.error ?? endpoints.error;
  const stats = [
    { label: t('admin.dashboard.requests'), value: usage.data?.total_requests },
    { label: t('admin.dashboard.promptTokens'), value: usage.data?.total_prompt_tokens },
    { label: t('common.tokens.output'), value: usage.data?.total_output_tokens },
    {
      label: t('admin.dashboard.endpointsTitle'),
      value: endpoints.data?.pagination.total_items,
      to: '/endpoints',
    },
    { label: t('admin.dashboard.nonTerminalReports'), value: reports.data?.total, to: '/reports' },
  ];
  return (
    <div className="page ops-page admin-overview">
      <PageHeader
        title={t('admin.dashboard.title')}
        description={t('admin.dashboard.description')}
      />
      <section aria-label={t('admin.dashboard.usageTitle')}>
        <div className="nb-stats">
          {stats.map((stat) => (
            <div className="nb-stat" key={stat.label}>
              <div className="nb-stat__label">
                {stat.to ? <Link to={stat.to}>{stat.label}</Link> : stat.label}
              </div>
              <div className="nb-stat__value">{stat.value ?? '—'}</div>
            </div>
          ))}
        </div>
        <p className="overview-usage-note">
          {t('admin.dashboard.usageTitle')} · {t('admin.dashboard.unknownUsage')}:{' '}
          {usage.data?.total_unknown_usage_requests ?? '—'}
        </p>
        {usage.isPending || endpoints.isPending || reports.isPending ? <LoadingState /> : null}
        {usage.error ? (
          <ErrorState error={usage.error} onRetry={() => void usage.refetch()} />
        ) : null}
        {endpointError ? (
          <ErrorState
            error={endpointError}
            onRetry={() => void (session.error ? session.refetch() : endpoints.refetch())}
          />
        ) : null}
        {reports.error ? (
          <ErrorState error={reports.error} onRetry={() => void reports.refetch()} />
        ) : null}
      </section>
      {activity.isPending ? (
        <LoadingState />
      ) : activity.error ? (
        <ErrorState error={activity.error} onRetry={() => void activity.refetch()} />
      ) : activity.data?.enabled === false ? (
        <p className="overview-activity-disabled">{t('admin.dashboard.activityDisabled')}</p>
      ) : (
        <Fold title={t('admin.dashboard.activityTitle')}>
          {activity.data?.data.length ? (
            <dl className="nb-facts nb-facts--inline">
              <dt>{t('admin.dashboard.latestDay')}</dt>
              <dd>{formatSiteDay(activity.data.data[0]?.day, siteTimezone.data)}</dd>
              <dt>{t('admin.dashboard.productActive')}</dt>
              <dd>
                {t(
                  activity.data.data[0]?.product_active
                    ? 'admin.dashboard.activeValue'
                    : 'admin.dashboard.inactiveValue',
                )}
              </dd>
              <dt>{t('admin.dashboard.generalCheckins')}</dt>
              <dd>{activity.data.data[0]?.checkins}</dd>
              <dt>{t('admin.dashboard.gameCheckins')}</dt>
              <dd>{activity.data.data[0]?.game_checkins}</dd>
              <dt>{t('admin.dashboard.gameActive')}</dt>
              <dd>
                {t(
                  activity.data.data[0]?.game_active
                    ? 'admin.dashboard.activeValue'
                    : 'admin.dashboard.inactiveValue',
                )}
              </dd>
            </dl>
          ) : (
            <EmptyState
              title={t('admin.dashboard.noActivity')}
              body={t('admin.dashboard.noActivityBody')}
            />
          )}
        </Fold>
      )}
      {reports.data ? (
        <Fold title={t('admin.dashboard.reportsTitle')}>
          <dl className="nb-facts nb-facts--inline">
            <dt>{t('admin.dashboard.pendingIndexing')}</dt>
            <dd>{reports.data.by_status.pending_indexing}</dd>
            <dt>{t('admin.dashboard.pendingReview')}</dt>
            <dd>{reports.data.by_status.pending_review}</dd>
            <dt>{t('admin.dashboard.approvedProcessing')}</dt>
            <dd>{reports.data.by_status.approved_processing}</dd>
          </dl>
          <Link className="nb-btn nb-btn--secondary" to="/reports">
            {t('admin.dashboard.openReportInbox')}
          </Link>
        </Fold>
      ) : null}
    </div>
  );
}
