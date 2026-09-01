import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { adminCoreKeys, getAdminActivity, getAdminEndpoints, getAdminUsage } from '../features/operations/core';
import { adminReportKeys, getReportBadge } from '../features/operations/reports';
import '@shared/operations/operations.css';

function QueryCard({ title, query, children }: { title: string; query: { isPending: boolean; error: unknown; refetch: () => unknown }; children: ReactNode }) {
  return <Card><h2>{title}</h2>{query.isPending ? <LoadingState /> : query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : children}</Card>;
}

export function DashboardPage() {
  const usage = useQuery({ queryKey: adminCoreKeys.usage, queryFn: getAdminUsage, retry: false });
  const activity = useQuery({ queryKey: adminCoreKeys.activity(null), queryFn: () => getAdminActivity(null), retry: false });
  const endpoints = useQuery({ queryKey: adminCoreKeys.endpoints('', null), queryFn: () => getAdminEndpoints('', null), retry: false });
  const reports = useQuery({ queryKey: adminReportKeys.badge, queryFn: getReportBadge, retry: false });
  return (
    <div className="page ops-page">
      <PageHeader title="Operations overview" description="Independent authority reads keep one failing subsystem from hiding the others." />
      <div className="ops-grid">
        <QueryCard title="API usage" query={usage}>{usage.data ? <dl className="ops-kv"><dt>Requests</dt><dd>{usage.data.total_requests}</dd><dt>Prompt tokens</dt><dd>{usage.data.total_prompt_tokens}</dd><dt>Output tokens</dt><dd>{usage.data.total_output_tokens}</dd><dt>Unknown usage</dt><dd>{usage.data.total_unknown_usage_requests}</dd></dl> : null}</QueryCard>
        <QueryCard title="Product activity" query={activity}>{activity.data?.enabled === false ? <EmptyState title="Activity aggregation disabled" body="No activity rows are retained while aggregation is disabled." /> : activity.data?.data.length ? <dl className="ops-kv"><dt>Latest day</dt><dd>{activity.data.data[0]?.day}</dd><dt>Product active</dt><dd>{activity.data.data[0]?.product_active}</dd><dt>Game active</dt><dd>{activity.data.data[0]?.game_active}</dd></dl> : <EmptyState title="No activity rows" body="The authority read succeeded but no day is available." />}</QueryCard>
        <QueryCard title="Endpoint groups" query={endpoints}>{endpoints.data?.data.length ? <><p>{endpoints.data.data.length} groups in the first authority page.</p><Link className="btn btn-secondary" to="/endpoints">Open endpoint overview</Link></> : <EmptyState title="No endpoint groups" body="No canonical endpoint aggregate is currently visible." />}</QueryCard>
        <QueryCard title="Credential reports" query={reports}>{reports.data ? <><dl className="ops-kv"><dt>Non-terminal total</dt><dd>{reports.data.total}</dd><dt>Indexing</dt><dd>{reports.data.by_status.pending_indexing}</dd><dt>Review</dt><dd>{reports.data.by_status.pending_review}</dd><dt>Processing</dt><dd>{reports.data.by_status.approved_processing}</dd></dl><Link className="btn btn-secondary" to="/reports">Open report inbox</Link></> : null}</QueryCard>
      </div>
    </div>
  );
}
