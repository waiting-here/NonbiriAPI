import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { adminCoreKeys, getAdminAlerts, setAdminAlertResolved } from '../features/operations/core';
import '@shared/operations/operations.css';

export function AlertsPage() {
  const client = useQueryClient();
  const pager = useCursorPager();
  const [resolved, setResolved] = useState<'' | 'true' | 'false'>('false');
  const result = useQuery({ queryKey: adminCoreKeys.alerts(resolved, pager.cursor), queryFn: () => getAdminAlerts(resolved, pager.cursor), retry: false });
  const mutation = useMutation({ retry: false, mutationFn: ({ id, value }: { id: string; value: boolean }) => setAdminAlertResolved(id, value), onSettled: () => client.invalidateQueries({ queryKey: ['admin', 'operations', 'alerts'] }) });
  return (
    <div className="page ops-page">
      <PageHeader title="Operational alerts" description="Ordinary operational alerts are separate from credential-report review and its badge." />
      <Card>
        <label className="ops-form-field"><span>Resolution</span><select value={resolved} onChange={(event) => { setResolved(event.target.value as typeof resolved); pager.reset(); }}><option value="false">Open</option><option value="true">Resolved</option><option value="">All</option></select></label>
        {mutation.error ? <ErrorState error={mutation.error} /> : null}
        {result.isPending ? <LoadingState /> : result.error ? <ErrorState error={result.error} onRetry={() => void result.refetch()} /> : result.data.data.length === 0 ? <EmptyState title="No alerts" body="The selected authority view is empty." /> : <><div className="ops-table-scroll"><table className="ops-table"><thead><tr><th>Kind</th><th>Safe message</th><th>Reference</th><th>Subject</th><th>Created</th><th>State</th><th>Action</th></tr></thead><tbody>{result.data.data.map((alert) => <tr key={alert.id}><td>{alert.kind}</td><td className="ops-wrap">{alert.message}</td><td>{alert.ref ?? '—'}</td><td>{alert.subject_user_id ?? '—'}</td><td>{formatDateTime(alert.created_at)}</td><td><StatusBadge active={alert.resolved} label={alert.resolved ? `Resolved ${alert.resolved_at ? formatDateTime(alert.resolved_at) : ''}` : 'Open'} /></td><td><button className="btn btn-secondary" type="button" disabled={mutation.isPending} onClick={() => mutation.mutate({ id: alert.id, value: !alert.resolved })}>{alert.resolved ? 'Reopen' : 'Resolve'}</button></td></tr>)}</tbody></table></div><CursorPagination page={pager.page} nextCursor={result.data.next_cursor} onPrevious={pager.previous} onNext={pager.next} /></>}
      </Card>
    </div>
  );
}
