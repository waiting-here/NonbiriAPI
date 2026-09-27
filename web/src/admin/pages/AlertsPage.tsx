import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
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
import { useDisplayTimeContext } from '@shared/components/timeContextValue';
import { fixedOffsetZone } from '@shared/time';
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
import {
  isDiagnosticTarget,
  useAdminAlertDetail,
  useAdminTargetDiagnostic,
  validAlertID,
  type AlertTarget,
  type DiagnosticTargetKind,
} from '../features/operations/alertDetail';
import { alertDetailCopy } from './alertDetailCopy';
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

export function validAlertReturnTo(value: string | null): string | null {
  if (value === null || value.length === 0 || value.length > 2048 || value.startsWith('//'))
    return null;
  if (
    Array.from(value).some(
      (character) =>
        character === '\\' || character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127,
    )
  )
    return null;
  const [path] = value.split('?');
  if (
    ![
      '/users',
      '/alerts',
      '/charity',
      '/reports',
      '/logs',
      '/settings',
      '/risk-audit',
      '/games',
    ].includes(path)
  )
    return null;
  return value;
}

function targetPath(target: AlertTarget, targets: AlertTarget[]): string | null {
  if (!target.available) return null;
  switch (target.kind) {
    case 'deleted_account':
      return `/users?deleted=${target.id}`;
    case 'user':
      return `/users?user=${target.id}`;
    case 'donation':
      return `/charity?donation_id=${target.id}`;
    case 'donation_key': {
      const donation = targets.find((item) => item.kind === 'donation' && item.available);
      return donation ? `/charity?donation_id=${donation.id}&donation_key=${target.id}` : null;
    }
    case 'report_case':
      return `/reports/${target.id}`;
    case 'request_log':
      return `/logs?request_id=${target.id}`;
    case 'maintenance_event':
      return '/settings';
    default:
      return null;
  }
}

export function AlertsPage() {
  const formatDateTime = useDateTimeFormatter();
  const timeContext = useDisplayTimeContext();
  const { t, i18n } = useTranslation();
  const copy = i18n.language.startsWith('zh') ? alertDetailCopy.zh : alertDetailCopy.en;
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
  const rawFocusedID = searchParams.getAll('alert_id');
  const focusedID =
    rawFocusedID.length === 1 && validAlertID(rawFocusedID[0]) ? rawFocusedID[0] : null;
  const returnToValues = searchParams.getAll('return_to');
  const returnTo = returnToValues.length === 1 ? validAlertReturnTo(returnToValues[0]) : null;
  const detail = useAdminAlertDetail(
    accountID,
    focusedID,
    scopeReady && !session.isPending && !session.isFetching,
  );
  const rawTargetKind = searchParams.getAll('target_kind');
  const rawTargetID = searchParams.getAll('target_id');
  const selectedTarget =
    rawTargetKind.length === 1 &&
    rawTargetID.length === 1 &&
    isDiagnosticTarget(rawTargetKind[0], rawTargetID[0])
      ? detail.data?.targets.find(
          (target) =>
            target.id === rawTargetID[0] &&
            target.available &&
            (target.kind === rawTargetKind[0] ||
              (rawTargetKind[0] === 'issue_user' &&
                detail.data.alert.kind === 'issue_projection_incomplete' &&
                target.kind === 'user')),
        )
      : undefined;
  const diagnosticKind = selectedTarget ? (rawTargetKind[0] as DiagnosticTargetKind) : undefined;
  const diagnostic = useAdminTargetDiagnostic(
    accountID,
    diagnosticKind ?? null,
    selectedTarget?.id ?? null,
    scopeReady && !session.isPending && !session.isFetching && Boolean(focusedID),
  );
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
    const currentError = result.error ?? detail.error ?? diagnostic.error;
    if (!isAuthorityError(currentError)) {
      if (!currentError) handledAuthorityError.current = null;
      return;
    }
    if (handledAuthorityError.current === currentError) return;
    handledAuthorityError.current = currentError;
    setAuthorityError(currentError);
    clearStationSession(client, 'admin');
  }, [client, result.error, detail.error, diagnostic.error]);

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
  const focusAlert = (id: string | null) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      if (id) next.set('alert_id', id);
      else {
        next.delete('alert_id');
        next.delete('return_to');
      }
      next.delete('target_kind');
      next.delete('target_id');
      return next;
    });
  };
  const focusTarget = (target: AlertTarget | null, diagnosticTargetKind?: DiagnosticTargetKind) =>
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      const selectedKind = diagnosticTargetKind ?? target?.kind ?? '';
      if (
        target &&
        isDiagnosticTarget(selectedKind, target.id) &&
        target.available &&
        (selectedKind === target.kind ||
          (selectedKind === 'issue_user' &&
            detail.data?.alert.kind === 'issue_projection_incomplete' &&
            target.kind === 'user'))
      ) {
        next.set('target_kind', selectedKind);
        next.set('target_id', target.id);
      } else {
        next.delete('target_kind');
        next.delete('target_id');
      }
      return next;
    });
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
      <p className="field-help time-context-notice" role="status">
        {timeContext.mode === 'site' && timeContext.offset_minutes !== null
          ? t('common.time.zone', { zone: fixedOffsetZone(timeContext.offset_minutes) })
          : t('common.time.siteUnavailable')}
      </p>
      {focusedID ? (
        <Card>
          <div className="ops-actions">
            <h2>
              {copy.details} #{focusedID}
            </h2>
            <button className="btn btn-quiet" type="button" onClick={() => focusAlert(null)}>
              {copy.close}
            </button>
            {returnTo ? <Link to={returnTo}>{copy.back}</Link> : null}
          </div>
          {detail.isPending ? (
            <LoadingState />
          ) : detail.error ? (
            <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
          ) : detail.data ? (
            <div className="ops-stack">
              <p>
                {kindLabels[detail.data.alert.kind]} ·{' '}
                {detail.data.alert.kind === 'maintenance_enabled'
                  ? copy.facts
                  : detail.data.alert.message}
              </p>
              <dl className="ops-kv">
                <dt>{copy.occurred}</dt>
                <dd>{formatDateTime(detail.data.alert.created_at)}</dd>
                <dt>{copy.resolution}</dt>
                <dd>
                  {detail.data.alert.resolved
                    ? `${t('admin.alerts.resolvedValue')} ${detail.data.alert.resolved_at !== null ? formatDateTime(detail.data.alert.resolved_at) : ''} ${detail.data.resolution_kind}`
                    : copy.unresolved}
                </dd>
                <dt>{copy.technicalReference}</dt>
                <dd>
                  <code>{detail.data.alert.ref ?? '—'}</code>
                </dd>
              </dl>
              <h3>{copy.facts}</h3>
              {detail.data.occurred_facts.length === 0 ? (
                <p>{copy.noFacts}</p>
              ) : (
                <dl className="ops-kv">
                  {detail.data.occurred_facts.map((fact, index) => (
                    <div key={`${fact.key}:${index}`}>
                      <dt>{copy.factNames[fact.key as keyof typeof copy.factNames] ?? fact.key}</dt>
                      <dd>{fact.value}</dd>
                    </div>
                  ))}
                </dl>
              )}
              <h3>{copy.targets}</h3>
              {detail.data.targets.length === 0 ? (
                <p>{copy.noTarget}</p>
              ) : (
                <ul>
                  {detail.data.targets.map((target) => {
                    const path = targetPath(target, detail.data.targets);
                    const issueUser =
                      detail.data.alert.kind === 'issue_projection_incomplete' &&
                      target.kind === 'user' &&
                      target.available &&
                      isDiagnosticTarget('issue_user', target.id);
                    return (
                      <li key={`${target.kind}:${target.id}`}>
                        {copy.targetNames[target.kind]} #{target.id} ·{' '}
                        {target.available ? target.status : copy.missing}{' '}
                        {path ? <Link to={path}>{copy.open}</Link> : null}{' '}
                        {target.available && isDiagnosticTarget(target.kind, target.id) ? (
                          <button
                            className="btn btn-secondary"
                            type="button"
                            onClick={() => focusTarget(target)}
                          >
                            {copy.open}
                          </button>
                        ) : null}{' '}
                        {issueUser ? (
                          <button
                            className="btn btn-secondary"
                            type="button"
                            onClick={() => focusTarget(target, 'issue_user')}
                          >
                            {copy.projectionDiagnostic}
                          </button>
                        ) : null}
                      </li>
                    );
                  })}
                </ul>
              )}
              {selectedTarget ? (
                <Card>
                  <div className="ops-actions">
                    <h3>
                      {copy.diagnostic}: {copy.targetNames[selectedTarget.kind]} #
                      {selectedTarget.id}
                    </h3>
                    <button
                      className="btn btn-quiet"
                      type="button"
                      onClick={() => focusTarget(null)}
                    >
                      {copy.closeDiagnostic}
                    </button>
                  </div>
                  {diagnostic.isPending ? (
                    <LoadingState />
                  ) : diagnostic.error ? (
                    <ErrorState
                      error={diagnostic.error}
                      onRetry={() => void diagnostic.refetch()}
                    />
                  ) : diagnostic.data ? (
                    <>
                      <dl className="ops-kv">
                        {diagnostic.data.facts.map((fact) => (
                          <div key={fact.key}>
                            <dt>
                              {copy.factNames[fact.key as keyof typeof copy.factNames] ?? fact.key}
                            </dt>
                            <dd>
                              {fact.key === 'projection_phase'
                                ? (copy.projectionPhases[
                                    fact.value as keyof typeof copy.projectionPhases
                                  ] ?? copy.unknown)
                                : /_at$/.test(fact.key) && /^[0-9]+$/.test(fact.value)
                                  ? formatDateTime(Number(fact.value))
                                  : fact.value}
                            </dd>
                          </div>
                        ))}
                      </dl>
                      {diagnosticKind === 'issue_user' ? (
                        <>
                          <h4>{copy.relatedIssues}</h4>
                          {diagnostic.data.related_issue_ids.length === 0 ? (
                            <p>{copy.noRelatedIssues}</p>
                          ) : (
                            <ul>
                              {diagnostic.data.related_issue_ids.map((id) => (
                                <li key={id}>
                                  <code>{id}</code>
                                </li>
                              ))}
                            </ul>
                          )}
                        </>
                      ) : null}
                    </>
                  ) : null}
                </Card>
              ) : null}
              {detail.data.current_state.length > 0 ? (
                <>
                  <h3>{copy.current}</h3>
                  <dl className="ops-kv">
                    {detail.data.current_state.map((fact, index) => (
                      <div key={`${fact.key}:${index}`}>
                        <dt>
                          {copy.factNames[fact.key as keyof typeof copy.factNames] ?? fact.key}
                        </dt>
                        <dd>
                          {fact.value === 'unknown'
                            ? copy.unknown
                            : /_at$/.test(fact.key) && /^[0-9]+$/.test(fact.value)
                              ? formatDateTime(Number(fact.value))
                              : fact.value}
                        </dd>
                      </div>
                    ))}
                  </dl>
                </>
              ) : null}
              {detail.data.alert.kind === 'donation_failure_disabled' ? (
                <>
                  <h3>{copy.logs}</h3>
                  {detail.data.related_logs?.available ? (
                    <p>
                      <Link
                        to={`/logs?endpoint_key_id=${detail.data.related_logs.endpoint_key_id}&from=${detail.data.related_logs.from}&to=${detail.data.related_logs.to}`}
                      >
                        {copy.eventWindow}
                      </Link>
                    </p>
                  ) : (
                    <p>{copy.noLogs}</p>
                  )}
                </>
              ) : null}
            </div>
          ) : null}
        </Card>
      ) : null}
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
                            onClick={() => focusAlert(alert.id)}
                          >
                            {copy.open}
                          </button>
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
