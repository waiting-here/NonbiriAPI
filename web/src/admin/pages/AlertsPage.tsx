import { useState } from 'react';
import { formatDateTime } from '@shared/utils/datetime';
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

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;

export function AlertsPage() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZE_OPTIONS[1]);
  const [draftResolved, setDraftResolved] = useState<'all' | 'unresolved' | 'resolved'>('all');
  const [resolvedFilter, setResolvedFilter] = useState<boolean | undefined>(undefined);
  const alerts = useAdminAlerts(page, resolvedFilter, pageSize);
  const resolve = useResolveAdminAlert();

  const changePageSize = (size: number) => {
    setPageSize(size);
    setPage(1);
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
        <form
          className="filter-bar"
          onSubmit={(event) => {
            event.preventDefault();
            setResolvedFilter(
              draftResolved === 'all' ? undefined : draftResolved === 'resolved',
            );
            setPage(1);
          }}
        >
          <label>
            <span>{t('admin.alerts.filterResolved')}</span>
            <select
              value={draftResolved}
              onChange={(event) => setDraftResolved(event.target.value as 'all' | 'unresolved' | 'resolved')}
              aria-label={t('common.filterResolvedAria')}
            >
              <option value="all">{t('common.all')}</option>
              <option value="unresolved">{t('common.unresolved')}</option>
              <option value="resolved">{t('common.resolved')}</option>
            </select>
          </label>
          <div className="filter-actions">
            <button type="submit" className="btn btn-quiet">
              {t('common.applyFilter')}
            </button>
            <button
              type="button"
              className="btn btn-link"
              onClick={() => {
                setDraftResolved('all');
                setResolvedFilter(undefined);
                setPage(1);
              }}
            >
              {t('common.resetFilter')}
            </button>
          </div>
        </form>
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
                      <td><ReadOnlyValue value={formatDateTime(alert.created_at)} /></td>
                      <td>
                        <StatusBadge
                          active={alert.resolved}
                          label={alert.resolved ? t('admin.alerts.resolvedValue') : t('admin.alerts.open')}
                        />
                        {alert.resolved_at !== '—' ? <span className="table-note">{formatDateTime(alert.resolved_at)}</span> : null}
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
            <Pagination
              page={page}
              hasNext={alerts.data.hasNext}
              onChange={setPage}
              pageSize={pageSize}
              pageSizeOptions={PAGE_SIZE_OPTIONS}
              onPageSizeChange={changePageSize}
              onJumpToPage={setPage}
            />
          </>
        )}
      </Card>
    </div>
  );
}
