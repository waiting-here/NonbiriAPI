import { useEffect, useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSearchState } from '@shared/operations/useSearchState';
import { clearStationSession } from '@shared/charityManagement';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { formatDateTime } from '@shared/utils/datetime';
import { useAdminSession } from '../data';
import {
  setAdminAlertResolved,
  type AdminAlert,
} from '../features/operations/core';
import {
  adminAlertPageKeys,
  useAdminAlertPage,
  type AdminAlertResolvedFilter,
} from '../features/operations/alertPage';
import '@shared/operations/operations.css';

const ALERTS_LIST_TYPE = 'alerts';
const RESOLVED_PARAM = 'resolved';

function resolvedFilter(searchParams: URLSearchParams): AdminAlertResolvedFilter {
  const values = searchParams.getAll(RESOLVED_PARAM);
  const value = values.length === 1 ? values[0] : undefined;
  return value === 'all' || value === 'true' || value === 'false' ? value : 'false';
}

function needsResolvedNormalization(searchParams: URLSearchParams): boolean {
  const values = searchParams.getAll(RESOLVED_PARAM);
  return values.length !== 1 || !['all', 'true', 'false'].includes(values[0] ?? '');
}

function isAuthorityError(error: unknown): boolean {
  return isUnauthorized(error) || isForbidden(error);
}

export function AlertsPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const session = useAdminSession();
  const [searchParams, setSearchParams] = useSearchState();
  const resolved = resolvedFilter(searchParams);
  const accountID = session.data ? `admin:${session.data.admin.username}` : undefined;
  const scopeReady = Boolean(accountID) && !session.error;
  const pager = useUrlPagePager({
    station: 'admin',
    listType: ALERTS_LIST_TYPE,
    scopeKey: accountID ?? 'anonymous',
    scopeReady,
    resetKey: resolved,
  });
  const result = useAdminAlertPage(
    accountID,
    resolved,
    pager.page,
    pager.pageSize,
    scopeReady && !session.isPending && !session.isFetching,
  );
  const handledAuthorityError = useRef<unknown>(null);
  const [authorityError, setAuthorityError] = useState<unknown>(null);

  useEffect(() => {
    if (!needsResolvedNormalization(searchParams)) return;
    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.delete(RESOLVED_PARAM);
        next.set(RESOLVED_PARAM, 'false');
        return next;
      },
      { replace: true },
    );
  }, [searchParams, setSearchParams]);

  useEffect(() => {
    if (!isAuthorityError(result.error)) {
      if (!result.error) handledAuthorityError.current = null;
      return;
    }
    if (handledAuthorityError.current === result.error) return;
    handledAuthorityError.current = result.error;
    setAuthorityError(result.error);
    clearStationSession(client, 'admin');
  }, [client, result.error]);

  const mutation = useMutation({
    retry: false,
    mutationFn: ({ id, value }: { id: string; value: boolean }) => setAdminAlertResolved(id, value),
    onError: (error) => {
      if (isAuthorityError(error)) {
        setAuthorityError(error);
        clearStationSession(client, 'admin');
      }
    },
    onSettled: () => client.invalidateQueries({ queryKey: adminAlertPageKeys.root }),
  });

  const selectResolved = (nextResolved: AdminAlertResolvedFilter) => {
    if (nextResolved !== 'all' && nextResolved !== 'true' && nextResolved !== 'false') return;
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete(RESOLVED_PARAM);
      next.set(RESOLVED_PARAM, nextResolved);
      next.delete('page');
      next.set('page', '1');
      next.delete('page_size');
      next.set('page_size', String(pager.pageSize));
      return next;
    });
  };

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
  const pageData = result.data;
  const busy = session.isFetching || result.isFetching;
  const actionDisabled = busy || result.isPlaceholderData || mutation.isPending;
  const sessionError = session.error ?? (!session.data ? authorityError : null);

  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.alerts.title')} description={t('admin.alerts.description')} />
      <Card>
        <label className="ops-form-field">
          <span>{t('admin.alerts.filterResolved')}</span>
          <select
            value={resolved}
            onChange={(event) => selectResolved(event.target.value as AdminAlertResolvedFilter)}
          >
            <option value="false">{t('admin.alerts.open')}</option>
            <option value="true">{t('admin.alerts.resolvedValue')}</option>
            <option value="all">{t('common.all')}</option>
          </select>
        </label>
        {mutation.error && !isAuthorityError(mutation.error) ? (
          <ErrorState error={mutation.error} />
        ) : null}
        {sessionError ? (
          <ErrorState
            error={sessionError}
            onRetry={() => void session.refetch()}
          />
        ) : !session.data || session.isPending ? (
          <LoadingState />
        ) : result.error ? (
          <ErrorState error={result.error} onRetry={() => void result.refetch()} />
        ) : !pageData ? (
          <LoadingState />
        ) : (
          <div aria-busy={busy}>
            {busy ? <LoadingState /> : null}
            {pageData.data.length === 0 ? (
              <EmptyState title={t('admin.alerts.empty')} body={t('admin.alerts.emptyBody')} />
            ) : (
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
                    {pageData.data.map((alert) => (
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
                                ? `${t('admin.alerts.resolvedValue')} ${alert.resolved_at !== null ? formatDateTime(alert.resolved_at) : ''}`
                                : t('admin.alerts.open')
                            }
                          />
                        </td>
                        <td className="ops-cell-wide" data-label={t('admin.alerts.action')}>
                          <button
                            className="btn btn-secondary"
                            type="button"
                            disabled={actionDisabled}
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
            )}
            <PagePagination
              metadata={pageData.pagination}
              requestedPage={pager.page}
              busy={busy}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </div>
        )}
      </Card>
    </div>
  );
}
