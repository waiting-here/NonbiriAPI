import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  Pagination,
  ReadOnlyValue,
  StatusBadge,
} from '@shared/components/States';
import { useAdminAlerts, useResolveAdminAlert } from '../data';

export function AlertsPage() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [cursors, setCursors] = useState<Record<number, string>>({});
  const beforeId = page > 1 ? cursors[page - 1] : undefined;
  const alerts = useAdminAlerts(page, beforeId);
  const resolve = useResolveAdminAlert();

  const changePage = (nextPage: number) => {
    if (nextPage > page) {
      const nextCursor = alerts.data?.nextCursor;
      if (!nextCursor) return;
      setCursors((current) => ({ ...current, [page]: nextCursor }));
    }
    setPage(nextPage);
  };

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.alerts.title')}
        description={t('admin.alerts.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.alerts.listTitle')}</h2>
          <span className="muted">{t('common.page', { page })}</span>
        </div>
        {resolve.error ? <ErrorState error={resolve.error} /> : null}
        {alerts.isPending ? <LoadingState /> : alerts.error ? <ErrorState error={alerts.error} onRetry={() => void alerts.refetch()} /> : alerts.data.items.length === 0 ? (
          <EmptyState title={t('admin.alerts.empty')} body={t('admin.alerts.emptyBody')} />
        ) : (
          <>
            <div className="table-wrap">
              <table>
                <caption>{t('admin.alerts.listTitle')}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('admin.alerts.kind')}</th>
                    <th scope="col">{t('admin.alerts.message')}</th>
                    <th scope="col">{t('admin.alerts.reference')}</th>
                    <th scope="col">{t('admin.alerts.subject')}</th>
                    <th scope="col">{t('admin.alerts.created')}</th>
                    <th scope="col">{t('admin.alerts.resolved')}</th>
                  </tr>
                </thead>
                <tbody>
                  {alerts.data.items.map((alert) => (
                    <tr key={alert.id}>
                      <td>{alert.kind}</td>
                      <td>{alert.message}</td>
                      <td><ReadOnlyValue value={alert.ref} /></td>
                      <td><ReadOnlyValue value={alert.subject_user_id} /></td>
                      <td><ReadOnlyValue value={alert.created_at} /></td>
                      <td>
                        <StatusBadge
                          active={alert.resolved}
                          label={alert.resolved ? t('admin.alerts.resolvedValue') : t('admin.alerts.open')}
                        />
                        {alert.resolved_at !== '—' ? <span className="table-note">{alert.resolved_at}</span> : null}
                        <button
                          type="button"
                          className="btn btn-quiet"
                          disabled={resolve.isPending}
                          onClick={() => resolve.mutate({ alertId: alert.id, resolved: !alert.resolved })}
                        >
                          {alert.resolved ? t('admin.alerts.reopen') : t('admin.alerts.resolve')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination page={page} hasNext={alerts.data.hasNext} onChange={changePage} />
          </>
        )}
      </Card>
    </div>
  );
}
