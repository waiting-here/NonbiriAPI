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
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { useAdminSession } from '../data';
import {
  setAdminAlertResolved,
  resolveAdminAlerts,
  ALERT_KINDS,
  type AdminAlert,
} from '../features/operations/core';
import {
  adminAlertPageKeys,
  useAdminAlertPage,
  type AdminAlertResolvedFilter,
  type AlertKindFilter,
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
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const client = useQueryClient();
  const session = useAdminSession();
  const [searchParams, setSearchParams] = useSearchState();
  const resolved = resolvedFilter(searchParams);
  const rawKind = searchParams.get('kind');
  const kind: AlertKindFilter = ALERT_KINDS.includes(rawKind as AdminAlert['kind'])
    ? (rawKind as AdminAlert['kind'])
    : 'all';
  const accountID = session.data ? `admin:${session.data.admin.username}` : undefined;
  const scopeReady = Boolean(accountID) && !session.error;
  const pager = useUrlPagePager({
    station: 'admin',
    listType: ALERTS_LIST_TYPE,
    scopeKey: accountID ?? 'anonymous',
    scopeReady,
    resetKey: resolved + kind,
  });
  const result = useAdminAlertPage(
    accountID,
    resolved,
    pager.page,
    pager.pageSize,
    scopeReady && !session.isPending && !session.isFetching,
    kind,
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

  const selectionScope = [accountID, resolved, kind, pager.page, pager.pageSize].join(':');
  const [selection, setSelection] = useState<{ scope: string; ids: string[] }>({
    scope: '',
    ids: [],
  });
  const selectable =
    result.data?.data.filter((alert) => !alert.resolved).map((alert) => alert.id) ?? [];
  const selected =
    selection.scope === selectionScope ? selection.ids.filter((id) => selectable.includes(id)) : [];
  const bulk = useMutation({
    mutationFn: resolveAdminAlerts,
    retry: false,
    onSuccess: () => setSelection({ scope: '', ids: [] }),
    onError: (error) => {
      if (isAuthorityError(error)) {
        setAuthorityError(error);
        clearStationSession(client, 'admin');
      }
    },
    onSettled: () => client.invalidateQueries({ queryKey: adminAlertPageKeys.root }),
  });
  const selectKind = (value: string) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set('kind', value);
      next.set('page', '1');
      return next;
    });
  };
  const toggleSelection = (id: string) =>
    setSelection({
      scope: selectionScope,
      ids: selected.includes(id) ? selected.filter((value) => value !== id) : [...selected, id],
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
    account_deleted: t('admin.alerts.kindValue.accountDeleted'),
  };
  const pageData = result.data;
  const busy = session.isFetching || result.isFetching;
  const actionDisabled = busy || result.isPlaceholderData || mutation.isPending || bulk.isPending;
  const sessionError = session.error ?? (!session.data ? authorityError : null);

  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.alerts.title')} description={t('admin.alerts.description')} />
      <Card>
        <div className="ops-field-grid">
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
          <label className="ops-form-field">
            <span>{t('admin.alerts.filterKind')}</span>
            <select value={kind} onChange={(event) => selectKind(event.target.value)}>
              <option value="all">{t('common.all')}</option>
              {ALERT_KINDS.map((value) => (
                <option key={value} value={value}>
                  {kindLabels[value]}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="ops-actions">
          <label className="checkbox-label">
            <input
              type="checkbox"
              aria-label={t('admin.alerts.selectPage')}
              disabled={actionDisabled || selectable.length === 0}
              checked={selectable.length > 0 && selected.length === selectable.length}
              onChange={() =>
                setSelection({
                  scope: selectionScope,
                  ids: selected.length === selectable.length ? [] : selectable,
                })
              }
            />
            <span>{t('admin.alerts.selectPage')}</span>
          </label>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={actionDisabled || selected.length === 0}
            onClick={() => bulk.mutate(selected)}
          >
            {t('admin.alerts.resolveSelected', { count: selected.length })}
          </button>
        </div>
        {bulk.error && !isAuthorityError(bulk.error) ? <ErrorState error={bulk.error} /> : null}
        {mutation.error && !isAuthorityError(mutation.error) ? (
          <ErrorState error={mutation.error} />
        ) : null}
        {sessionError ? (
          <ErrorState error={sessionError} onRetry={() => void session.refetch()} />
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
                      <th>{t('admin.alerts.select')}</th>
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
                        <td data-label={t('admin.alerts.select')}>
                          <input
                            type="checkbox"
                            aria-label={t('admin.alerts.selectAlert', { id: alert.id })}
                            checked={selected.includes(alert.id)}
                            disabled={actionDisabled || alert.resolved}
                            onChange={() => toggleSelection(alert.id)}
                          />
                        </td>
                        <td data-label={t('admin.alerts.kind')}>{kindLabels[alert.kind]}</td>
                        <td
                          className="ops-cell-wide ops-wrap"
                          data-label={t('admin.alerts.message')}
                        >
                          {alert.account_deletion ? (
                            <>
                              <p>{t('admin.alerts.deletionSnapshot')}</p>
                              <dl className="ops-kv">
                                <dt>Discord ID</dt>
                                <dd>{alert.account_deletion.discord_id || '—'}</dd>
                                <dt>{t('admin.alerts.deletedUser')}</dt>
                                <dd>{alert.account_deletion.user_id}</dd>
                                <dt>{t('admin.alerts.generalBalance')}</dt>
                                <dd>{alert.account_deletion.general_balance}</dd>
                                <dt>{t('admin.alerts.gameBalance')}</dt>
                                <dd>{alert.account_deletion.game_balance}</dd>
                                <dt>{t('admin.alerts.donationCredit')}</dt>
                                <dd>{alert.account_deletion.donation_credit}</dd>
                                <dt>{t('admin.alerts.sketchAssets')}</dt>
                                <dd>
                                  {alert.account_deletion.sketch_paper} /{' '}
                                  {alert.account_deletion.sketch_brush}
                                </dd>
                              </dl>
                            </>
                          ) : (
                            alert.message
                          )}
                        </td>
                        <td data-label={t('admin.alerts.reference')}>{alert.ref ?? '—'}</td>
                        <td data-label={t('admin.alerts.subject')}>
                          {alert.subject_user_id ?? '—'}
                        </td>
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
                            className="btn btn-secondary ops-action-button"
                            type="button"
                            disabled={actionDisabled}
                            onClick={() =>
                              mutation.mutate({ id: alert.id, value: !alert.resolved })
                            }
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
