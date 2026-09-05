import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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
  adminCoreKeys,
  getAdminAlerts,
  setAdminAlertResolved,
  type AdminAlert,
} from '../features/operations/core';
import '@shared/operations/operations.css';

export function AlertsPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const pager = useCursorPager();
  const [resolved, setResolved] = useState<'' | 'true' | 'false'>('false');
  const result = useQuery({
    queryKey: adminCoreKeys.alerts(resolved, pager.cursor),
    queryFn: () => getAdminAlerts(resolved, pager.cursor),
    retry: false,
  });
  const mutation = useMutation({
    retry: false,
    mutationFn: ({ id, value }: { id: string; value: boolean }) => setAdminAlertResolved(id, value),
    onSettled: () => client.invalidateQueries({ queryKey: ['admin', 'operations', 'alerts'] }),
  });
  const kindLabels: Record<AdminAlert['kind'], string> = {
    fetch_failed: t('admin.alerts.kindValue.fetchFailed'),
    forward_error: t('admin.alerts.kindValue.forwardError'),
    registration_rejected: t('admin.alerts.kindValue.registrationRejected'),
    maintenance_enabled: t('admin.alerts.kindValue.maintenanceEnabled'),
    donation_failure_disabled: t('admin.alerts.kindValue.donationFailureDisabled'),
    issue_projection_incomplete: t('admin.alerts.kindValue.issueProjectionIncomplete'),
    report_retry_exhausted: t('admin.alerts.kindValue.reportRetryExhausted'),
    fishing_retry_exhausted: t('admin.alerts.kindValue.fishingRetryExhausted'),
    rps_terminal_retrying: t('admin.alerts.kindValue.rpsTerminalRetrying'),
    worker_checkpoint_failed: t('admin.alerts.kindValue.workerCheckpointFailed'),
    invariant_violation: t('admin.alerts.kindValue.invariantViolation'),
  };
  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.alerts.title')} description={t('admin.alerts.description')} />
      <Card>
        <label className="ops-form-field">
          <span>{t('admin.alerts.filterResolved')}</span>
          <select
            value={resolved}
            onChange={(event) => {
              setResolved(event.target.value as typeof resolved);
              pager.reset();
            }}
          >
            <option value="false">{t('admin.alerts.open')}</option>
            <option value="true">{t('admin.alerts.resolvedValue')}</option>
            <option value="">{t('common.all')}</option>
          </select>
        </label>
        {mutation.error ? <ErrorState error={mutation.error} /> : null}
        {result.isPending ? (
          <LoadingState />
        ) : result.error ? (
          <ErrorState error={result.error} onRetry={() => void result.refetch()} />
        ) : result.data.data.length === 0 ? (
          <EmptyState title={t('admin.alerts.empty')} body={t('admin.alerts.emptyBody')} />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table ops-table--responsive">
                <thead>
                  <tr>
                    <th>{t('admin.alerts.kind')}</th>
                    <th>{t('admin.alerts.message')}</th>
                    <th>{t('admin.alerts.reference')}</th>
                    <th>{t('admin.alerts.subject')}</th>
                    <th>{t('admin.alerts.created')}</th>
                    <th>{t('admin.alerts.resolved')}</th>
                    <th>{t('admin.alerts.action')}</th>
                  </tr>
                </thead>
                <tbody>
                  {result.data.data.map((alert) => (
                    <tr key={alert.id}>
                      <td data-label={t('admin.alerts.kind')}>{kindLabels[alert.kind]}</td>
                      <td className="ops-cell-wide ops-wrap" data-label={t('admin.alerts.message')}>
                        {alert.message}
                      </td>
                      <td data-label={t('admin.alerts.reference')}>{alert.ref ?? '—'}</td>
                      <td data-label={t('admin.alerts.subject')}>{alert.subject_user_id ?? '—'}</td>
                      <td data-label={t('admin.alerts.created')}>
                        {formatDateTime(alert.created_at)}
                      </td>
                      <td data-label={t('admin.alerts.resolved')}>
                        <StatusBadge
                          active={alert.resolved}
                          label={
                            alert.resolved
                              ? `${t('admin.alerts.resolvedValue')} ${alert.resolved_at ? formatDateTime(alert.resolved_at) : ''}`
                              : t('admin.alerts.open')
                          }
                        />
                      </td>
                      <td className="ops-cell-wide" data-label={t('admin.alerts.action')}>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          disabled={mutation.isPending}
                          onClick={() => mutation.mutate({ id: alert.id, value: !alert.resolved })}
                        >
                          {alert.resolved ? t('admin.alerts.reopen') : t('admin.alerts.resolve')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={result.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
      </Card>
    </div>
  );
}
