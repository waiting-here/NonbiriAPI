import { useState } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { adminReportKeys, getReportBadge, getReports, REPORT_STATUSES } from '../features/operations/reports';
import '@shared/operations/operations.css';

export function ReportsPage() {
  const pager = useCursorPager();
  const [status, setStatus] = useState('');
  const badge = useQuery({ queryKey: adminReportKeys.badge, queryFn: getReportBadge, retry: false, refetchInterval: 30_000 });
  const reports = useQuery({ queryKey: adminReportKeys.list(status, pager.cursor), queryFn: () => getReports(status, pager.cursor), retry: false });
  const badgeVisual = badge.data ? (BigInt(badge.data.total) > 99n ? '99+' : badge.data.total) : '—';
  return (
    <div className="page ops-page">
      <PageHeader title="Credential report inbox" description="Indexing, human review, and approved processing are distinct authority states." />
      <div className="ops-grid">
        <Card><h2>Non-terminal badge</h2>{badge.isPending ? <LoadingState /> : badge.error ? <ErrorState error={badge.error} onRetry={() => void badge.refetch()} /> : <><strong className="metric-value" aria-label={`Exact non-terminal count ${badge.data?.total}`}>{badgeVisual}</strong><dl className="ops-kv"><dt>Indexing</dt><dd>{badge.data?.by_status.pending_indexing}</dd><dt>Review</dt><dd>{badge.data?.by_status.pending_review}</dd><dt>Processing</dt><dd>{badge.data?.by_status.approved_processing}</dd></dl></>}</Card>
        <Card><h2>Privacy boundary</h2><p>This inbox receives case metadata, bounded review materials, and protected target snapshots. Submitted credential secrets never appear.</p></Card>
      </div>
      <Card>
        <label className="ops-form-field"><span>Status</span><select value={status} onChange={(event) => { setStatus(event.target.value); pager.reset(); }}><option value="">All</option>{REPORT_STATUSES.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
        {reports.isPending ? <LoadingState /> : reports.error ? <ErrorState error={reports.error} onRetry={() => void reports.refetch()} /> : reports.data.data.length === 0 ? <EmptyState title="No report cases" body="The selected server-side status view is empty." /> : <>
          <div className="ops-table-scroll"><table className="ops-table"><thead><tr><th>Status</th><th>Connector / canonical URL</th><th>Progress</th><th>Counts</th><th>Deadline</th><th>Open</th></tr></thead><tbody>{reports.data.data.map((item) => <tr key={item.id}><td><StatusBadge active={item.status === 'pending_review'} danger={item.status === 'approved_processing'} label={item.status} /></td><td>{item.connector_type}<br /><span className="ops-wrap">{item.canonical_base_url}</span></td><td>{item.progress_state}{item.retry ? <><br />retry {item.retry.attempt_count} · {item.retry.last_error_class}</> : null}</td><td>materials {item.counts.materials}<br />targets {item.counts.targets}<br />processed {item.counts.processed}</td><td>{formatDateTime(item.deadline)}</td><td><Link className="btn btn-secondary" to={`/reports/${encodeURIComponent(item.id)}`}>Review</Link></td></tr>)}</tbody></table></div>
          <CursorPagination page={pager.page} nextCursor={reports.data.next_cursor} onPrevious={pager.previous} onNext={pager.next} />
        </>}
      </Card>
    </div>
  );
}
