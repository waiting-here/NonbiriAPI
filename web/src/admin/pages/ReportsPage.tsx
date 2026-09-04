import { useState } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import {
  adminReportKeys,
  getReportBadge,
  getReports,
  REPORT_STATUSES,
  type ReportCaseSummary,
  type ReportStatus,
} from '../features/operations/reports';
import '@shared/operations/operations.css';

export function ReportsPage() {
  const { t } = useTranslation();
  const pager = useCursorPager();
  const [status, setStatus] = useState('');
  const badge = useQuery({
    queryKey: adminReportKeys.badge,
    queryFn: getReportBadge,
    retry: false,
    refetchInterval: 30_000,
  });
  const reports = useQuery({
    queryKey: adminReportKeys.list(status, pager.cursor),
    queryFn: () => getReports(status, pager.cursor),
    retry: false,
  });
  const badgeVisual = badge.data
    ? BigInt(badge.data.total) > 99n
      ? '99+'
      : badge.data.total
    : '—';
  const statusLabels: Record<ReportStatus, string> = {
    pending_indexing: t('admin.reports.status.pendingIndexing'),
    pending_review: t('admin.reports.status.pendingReview'),
    approved_processing: t('admin.reports.status.approvedProcessing'),
    approved: t('admin.reports.status.approved'),
    rejected: t('admin.reports.status.rejected'),
    expired: t('admin.reports.status.expired'),
  };
  const progressLabels: Record<ReportCaseSummary['progress_state'], string> = {
    in_progress: t('admin.reports.progress.inProgress'),
    complete: t('admin.reports.progress.complete'),
  };
  const retryClassLabels: Record<
    NonNullable<ReportCaseSummary['retry']>['last_error_class'],
    string
  > = {
    db_busy: t('admin.reports.retryClass.databaseBusy'),
    internal_retryable: t('admin.reports.retryClass.internalRetryable'),
    invariant_violation: t('admin.reports.retryClass.invariantViolation'),
  };
  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.reports.title')} description={t('admin.reports.description')} />
      <div className="ops-grid">
        <Card>
          <h2>{t('admin.reports.badge.title')}</h2>
          {badge.isPending ? (
            <LoadingState />
          ) : badge.error ? (
            <ErrorState error={badge.error} onRetry={() => void badge.refetch()} />
          ) : (
            <>
              <strong
                className="metric-value"
                aria-label={t('admin.reports.badge.exactCount', { count: badge.data?.total })}
              >
                {badgeVisual}
              </strong>
              <dl className="ops-kv">
                <dt>{t('admin.reports.badge.indexing')}</dt>
                <dd>{badge.data?.by_status.pending_indexing}</dd>
                <dt>{t('admin.reports.badge.review')}</dt>
                <dd>{badge.data?.by_status.pending_review}</dd>
                <dt>{t('admin.reports.badge.processing')}</dt>
                <dd>{badge.data?.by_status.approved_processing}</dd>
              </dl>
            </>
          )}
        </Card>
        <Card>
          <h2>{t('admin.reports.privacy.title')}</h2>
          <p>{t('admin.reports.privacy.body')}</p>
        </Card>
      </div>
      <Card>
        <label className="ops-form-field">
          <span>{t('admin.reports.filter.status')}</span>
          <select
            value={status}
            onChange={(event) => {
              setStatus(event.target.value);
              pager.reset();
            }}
          >
            <option value="">{t('admin.reports.filter.all')}</option>
            {REPORT_STATUSES.map((item) => (
              <option key={item} value={item}>
                {statusLabels[item]}
              </option>
            ))}
          </select>
        </label>
        {reports.isPending ? (
          <LoadingState />
        ) : reports.error ? (
          <ErrorState error={reports.error} onRetry={() => void reports.refetch()} />
        ) : reports.data.data.length === 0 ? (
          <EmptyState title={t('admin.reports.empty.title')} body={t('admin.reports.empty.body')} />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>{t('admin.reports.table.status')}</th>
                    <th>{t('admin.reports.table.connectorUrl')}</th>
                    <th>{t('admin.reports.table.progress')}</th>
                    <th>{t('admin.reports.table.counts')}</th>
                    <th>{t('admin.reports.table.deadline')}</th>
                    <th>{t('admin.reports.table.open')}</th>
                  </tr>
                </thead>
                <tbody>
                  {reports.data.data.map((item) => (
                    <tr key={item.id}>
                      <td>
                        <StatusBadge
                          active={item.status === 'pending_review'}
                          danger={item.status === 'approved_processing'}
                          label={statusLabels[item.status]}
                        />
                      </td>
                      <td>
                        {item.connector_type}
                        <br />
                        <span className="ops-wrap">{item.canonical_base_url}</span>
                      </td>
                      <td>
                        {progressLabels[item.progress_state]}
                        {item.retry ? (
                          <>
                            <br />
                            {t('admin.reports.table.retry', {
                              count: item.retry.attempt_count,
                            })}{' '}
                            · {retryClassLabels[item.retry.last_error_class]}
                          </>
                        ) : null}
                      </td>
                      <td>
                        {t('admin.reports.table.materials', { count: item.counts.materials })}
                        <br />
                        {t('admin.reports.table.targets', { count: item.counts.targets })}
                        <br />
                        {t('admin.reports.table.processed', { count: item.counts.processed })}
                      </td>
                      <td>{formatDateTime(item.deadline)}</td>
                      <td>
                        <Link
                          className="btn btn-secondary"
                          to={`/reports/${encodeURIComponent(item.id)}`}
                        >
                          {t('admin.reports.table.review')}
                        </Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={reports.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
      </Card>
    </div>
  );
}
