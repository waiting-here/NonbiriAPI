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
import { reportPageKeys } from '../features/operations/reportPages';
import { useAdminAlertPage } from '../features/operations/alertPage';
import { getReportBadge } from '../features/operations/reports';
import { charityKeys, getDonationBadge } from '@shared/operations/charity';
import { useAdminSession } from '../data';
import '@shared/operations/operations.css';
import { Fold, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { formatCount } from '@shared/utils/formatNumber';
import './dashboard.css';

/** One queue that may need an administrator, with its count and where to handle it. */
function AttentionCard({
  label,
  count,
  detail,
  to,
  action,
}: {
  label: string;
  count: string | undefined;
  detail?: string;
  to: string;
  action: string;
}) {
  const { t } = useTranslation();
  const waiting = count !== undefined && count !== '0';
  return (
    <Link className={`overview-attention__card${waiting ? ' is-waiting' : ''}`} to={to}>
      <span className="overview-attention__label">{label}</span>
      <strong className="overview-attention__count">
        {count === undefined ? '—' : formatCount(count).display}
      </strong>
      {detail ? <span className="overview-attention__sub">{detail}</span> : null}
      <span className="overview-attention__action">
        {count === '0' ? t('admin.dashboard.nothingPending') : action} →
      </span>
    </Link>
  );
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
    queryKey: reportPageKeys.badge(account || 'anonymous'),
    enabled: Boolean(account) && !session.error,
    queryFn: ({ signal }) => getReportBadge(signal),
    retry: false,
  });
  // Shares the sidebar badge's cache entry, so the count needs no extra request.
  const donations = useQuery({
    queryKey: charityKeys.badge('admin', account),
    queryFn: ({ signal }) => getDonationBadge('admin', signal),
    enabled: Boolean(account) && !session.error,
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
  ];
  const alerts = useAdminAlertPage(
    account ? 'admin:' + account : undefined,
    'false',
    '1',
    20,
    Boolean(account) && !session.error,
  );
  const reportBreakdown =
    !reports.error && reports.data
      ? `${t('admin.dashboard.pendingIndexing')} ${formatCount(reports.data.by_status.pending_indexing).display} · ${t('admin.dashboard.pendingReview')} ${formatCount(reports.data.by_status.pending_review).display} · ${t('admin.dashboard.approvedProcessing')} ${formatCount(reports.data.by_status.approved_processing).display}`
      : undefined;
  return (
    <div className="page ops-page admin-overview">
      <PageHeader title={t('admin.dashboard.title')} />
      <section className="overview-attention" aria-labelledby="overview-attention-title">
        <h2 id="overview-attention-title">{t('admin.dashboard.attentionTitle')}</h2>
        <div className="overview-attention__grid">
          <AttentionCard
            label={t('admin.dashboard.donationsPending')}
            count={donations.error ? undefined : donations.data?.pending_count}
            to="/charity?handling=pending"
            action={t('admin.dashboard.openDonations')}
          />
          <AttentionCard
            label={t('admin.dashboard.unresolvedAlerts')}
            count={alerts.error ? undefined : alerts.data?.pagination.total_items}
            to="/alerts?resolved=false"
            action={t('admin.dashboard.openAlerts')}
          />
          <AttentionCard
            label={t('admin.dashboard.nonTerminalReports')}
            count={reports.error ? undefined : reports.data?.total}
            detail={reportBreakdown}
            to="/reports"
            action={t('admin.dashboard.openReportInbox')}
          />
        </div>
        {alerts.error && <ErrorState error={alerts.error} onRetry={() => void alerts.refetch()} />}
        {reports.error && (
          <ErrorState error={reports.error} onRetry={() => void reports.refetch()} />
        )}
        {donations.error ? (
          <ErrorState error={donations.error} onRetry={() => void donations.refetch()} />
        ) : null}
      </section>
      <Panel aria-label={t('admin.dashboard.usageTitle')}>
        <PanelHead title={t('admin.dashboard.usageTitle')} />
        <PanelBody>
          <div className="nb-stats">
            {stats.map((stat) => (
              <div className="nb-stat" key={stat.label}>
                <div className="nb-stat__label">
                  {stat.to ? (
                    <Link className="overview-stat-link" to={stat.to}>
                      {stat.label}
                      <span aria-hidden="true">→</span>
                    </Link>
                  ) : (
                    stat.label
                  )}
                </div>
                <div className="nb-stat__value">
                  {stat.value === undefined ? '—' : formatCount(stat.value).display}
                </div>
              </div>
            ))}
          </div>
          <p className="overview-usage-note">
            {t('admin.dashboard.unknownUsage')}：
            {!usage.data ? '—' : formatCount(usage.data.total_unknown_usage_requests).display}
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
        </PanelBody>
      </Panel>
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
              <dd>{formatCount(activity.data.data[0]?.checkins ?? 0).display}</dd>
              <dt>{t('admin.dashboard.gameCheckins')}</dt>
              <dd>{formatCount(activity.data.data[0]?.game_checkins ?? 0).display}</dd>
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
    </div>
  );
}
