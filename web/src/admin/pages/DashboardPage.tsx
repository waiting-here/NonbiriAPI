import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { adminCoreKeys, getAdminActivity, getAdminEndpoints, getAdminSiteTimezoneOffset, getAdminUsage } from '../features/operations/core';
import { adminReportKeys, getReportBadge } from '../features/operations/reports';
import '@shared/operations/operations.css';

function QueryCard({ title, query, children }: { title: string; query: { isPending: boolean; error: unknown; refetch: () => unknown }; children: ReactNode }) {
  return <Card><h2>{title}</h2>{query.isPending ? <LoadingState /> : query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : children}</Card>;
}

function formatSiteDay(day: number | undefined, offsetMinutes: number | undefined): string {
  if (day === undefined || offsetMinutes === undefined) return '—';
  const match = new Date((day + offsetMinutes * 60) * 1_000)
    .toISOString()
    .match(/^\d{4}-\d{2}-\d{2}/);
  return match?.[0] ?? '—';
}

export function DashboardPage() {
  const { t } = useTranslation();
  const usage = useQuery({ queryKey: adminCoreKeys.usage, queryFn: getAdminUsage, retry: false });
  const activity = useQuery({ queryKey: adminCoreKeys.activity(null), queryFn: () => getAdminActivity(null), retry: false });
  const siteTimezone = useQuery({ queryKey: adminCoreKeys.siteTimezone, queryFn: getAdminSiteTimezoneOffset, retry: false });
  const endpoints = useQuery({ queryKey: adminCoreKeys.endpoints('', null), queryFn: () => getAdminEndpoints('', null), retry: false });
  const reports = useQuery({ queryKey: adminReportKeys.badge, queryFn: getReportBadge, retry: false });
  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.dashboard.title')} description={t('admin.dashboard.description')} />
      <div className="ops-grid">
        <QueryCard title={t('admin.dashboard.usageTitle')} query={usage}>
          {usage.data ? (
            <dl className="ops-kv">
              <dt>{t('admin.dashboard.requests')}</dt><dd>{usage.data.total_requests}</dd>
              <dt>{t('admin.dashboard.promptTokens')}</dt><dd>{usage.data.total_prompt_tokens}</dd>
              <dt>{t('common.tokens.output')}</dt><dd>{usage.data.total_output_tokens}</dd>
              <dt>{t('admin.dashboard.unknownUsage')}</dt><dd>{usage.data.total_unknown_usage_requests}</dd>
            </dl>
          ) : null}
        </QueryCard>
        <QueryCard title={t('admin.dashboard.activityTitle')} query={activity}>
          {activity.data?.enabled === false ? (
            <EmptyState
              title={t('admin.dashboard.activityDisabled')}
              body={t('admin.dashboard.activityDisabledBody')}
            />
          ) : activity.data?.data.length ? (
            <dl className="ops-kv">
              <dt>{t('admin.dashboard.latestDay')}</dt><dd>{formatSiteDay(activity.data.data[0]?.day, siteTimezone.data)}</dd>
              <dt>{t('admin.dashboard.productActive')}</dt><dd>{t(activity.data.data[0]?.product_active ? 'admin.dashboard.activeValue' : 'admin.dashboard.inactiveValue')}</dd>
              <dt>{t('admin.dashboard.gameActive')}</dt><dd>{t(activity.data.data[0]?.game_active ? 'admin.dashboard.activeValue' : 'admin.dashboard.inactiveValue')}</dd>
            </dl>
          ) : (
            <EmptyState
              title={t('admin.dashboard.noActivity')}
              body={t('admin.dashboard.noActivityBody')}
            />
          )}
        </QueryCard>
        <QueryCard title={t('admin.dashboard.endpointsTitle')} query={endpoints}>
          {endpoints.data?.data.length ? (
            <>
              <p>{t('admin.dashboard.endpointGroupsFirstPage', { groupCount: endpoints.data.data.length })}</p>
              <Link className="btn btn-secondary" to="/endpoints">{t('admin.dashboard.viewEndpoints')}</Link>
            </>
          ) : (
            <EmptyState title={t('admin.endpoints.empty')} body={t('admin.endpoints.emptyBody')} />
          )}
        </QueryCard>
        <QueryCard title={t('admin.dashboard.reportsTitle')} query={reports}>
          {reports.data ? (
            <>
              <dl className="ops-kv">
                <dt>{t('admin.dashboard.nonTerminalReports')}</dt><dd>{reports.data.total}</dd>
                <dt>{t('admin.dashboard.pendingIndexing')}</dt><dd>{reports.data.by_status.pending_indexing}</dd>
                <dt>{t('admin.dashboard.pendingReview')}</dt><dd>{reports.data.by_status.pending_review}</dd>
                <dt>{t('admin.dashboard.approvedProcessing')}</dt><dd>{reports.data.by_status.approved_processing}</dd>
              </dl>
              <Link className="btn btn-secondary" to="/reports">
                {t('admin.dashboard.openReportInbox')}
              </Link>
            </>
          ) : null}
        </QueryCard>
      </div>
    </div>
  );
}
