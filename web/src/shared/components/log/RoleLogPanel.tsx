import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import { isApiError } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import { Card, EmptyState, ErrorState, LoadingState } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { type PageSize } from '@shared/operations/pageNumbers';
import { CallerIdentity } from './CallerIdentity';
import { LogDetailDrawer } from './LogDetailDrawer';
import { LogFilters, type LogFilterField } from './LogFilters';
import { LogTable, type LogColumn } from './LogTable';
import { TokenBuckets } from './TokenBuckets';
import {
  adminLogExportPath,
  validateLogFilter,
  type LogFiltersValue,
  type LogResultClass,
  type LogRole,
  type LogRouteKind,
  type RoleLogRow,
} from './data';
import {
  numberedLogKeys,
  useRoleLogDetailPage,
  useRoleLogsPage,
  type NumberedRoleLogDetail,
} from './numberedQueries';
import { useLogUrlState } from './useLogUrlState';

const ROUTE_LABEL_KEYS: Record<LogRouteKind, string> = {
  openai_chat_completions: 'common.operations.logs.route.openaiChatCompletions',
  charity_chat_completions: 'common.operations.logs.route.charityChatCompletions',
  model_discovery: 'common.operations.logs.route.modelDiscovery',
};

const RESULT_LABEL_KEYS: Record<LogResultClass, string> = {
  success: 'common.operations.logs.resultValue.success',
  failed: 'common.operations.logs.resultValue.failed',
  cancelled: 'common.operations.logs.resultValue.cancelled',
};

const REQUEST_ID_PATTERN = /^req_[A-Za-z0-9_-]{22}$/;
const LOG_DEFAULT_PAGE_SIZE: PageSize = 20;

function validRequestID(value: string | null): string | null {
  return value !== null && REQUEST_ID_PATTERN.test(value) && /[AQgw]$/.test(value) ? value : null;
}

function requestFrom(detail: NumberedRoleLogDetail): RoleLogRow {
  return detail.request;
}

function isFinalAuthorityError(error: unknown): boolean {
  return isApiError(error) && (error.code === 'unauthorized' || error.code === 'forbidden');
}

function sessionIdentity(role: LogRole, value: unknown): string | undefined {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const root = value as Record<string, unknown>;
  const nested = role === 'admin' ? root.admin : root.user;
  if (nested === null || typeof nested !== 'object' || Array.isArray(nested)) return undefined;
  const identity = nested as Record<string, unknown>;
  const valueKey = role === 'admin' ? 'username' : 'id';
  const result = identity[valueKey];
  return typeof result === 'string' && result.length > 0 ? result : undefined;
}

// Observe the authoritative session without replacing its fetch function.
function useStationScope(
  role: LogRole,
  explicitAccountID: string | undefined,
  explicitScopeReady: boolean | undefined,
) {
  const queryClient = useQueryClient();
  const sessionKey = useMemo(
    () => (role === 'admin' ? (['admin', 'session'] as const) : (['user', 'session'] as const)),
    [role],
  );
  const subscribe = useCallback(
    (notify: () => void) =>
      queryClient.getQueryCache().subscribe((event) => {
        if (
          event.query.queryKey.length === 2 &&
          event.query.queryKey[0] === sessionKey[0] &&
          event.query.queryKey[1] === 'session'
        )
          notify();
      }),
    [queryClient, sessionKey],
  );
  const snapshot = useCallback(
    () => queryClient.getQueryState(sessionKey),
    [queryClient, sessionKey],
  );
  const session = useSyncExternalStore(subscribe, snapshot, snapshot);
  const derivedAccountID = sessionIdentity(role, session?.data);
  const accountID = explicitAccountID ?? derivedAccountID ?? '';
  const scopeReady = explicitScopeReady ?? (accountID.length > 0 && !session?.error);
  return { accountID, scopeReady, sessionError: session?.error };
}

function AttemptTable({
  detail,
  page,
  busy,
  onPageChange,
  onPageSizeChange,
}: {
  detail: NumberedRoleLogDetail;
  page: string;
  busy: boolean;
  onPageChange: (page: string) => void;
  onPageSizeChange: (size: PageSize) => void;
}) {
  const { t } = useTranslation();
  if (!('attempts' in detail)) return null;
  const attempts = detail.attempts;
  return (
    <div className="ops-stack" aria-busy={busy}>
      {attempts.data.length === 0 ? (
        <p>{t('common.operations.logs.noAttempts')}</p>
      ) : (
        <ol className="log-attempts">
          {attempts.data.map((attempt) => (
            <li className="log-attempt" key={attempt.attempt_seq}>
              <div className="log-attempt-heading">
                <h3>
                  {t('common.operations.logs.attemptNumber', { number: attempt.attempt_seq })}
                </h3>
                <span className="status-badge">
                  {t(
                    attempt.result_kind === 'synthetic'
                      ? 'common.operations.logs.platformRecord'
                      : 'common.operations.logs.upstreamResponse',
                  )}
                </span>
              </div>
              {attempt.result_kind === 'synthetic' ? (
                <p className="log-attempt-notice">
                  {t('common.operations.logs.syntheticExplanation')}
                </p>
              ) : null}
              <dl>
                <div>
                  <dt>{t('logs.endpointBaseUrl')}</dt>
                  <dd className="mono">{attempt.endpoint_base_url}</dd>
                </div>
                {'endpoint_note' in attempt && (attempt.endpoint_note || attempt.key_note) ? (
                  <div>
                    <dt>{t('common.operations.logs.notes')}</dt>
                    <dd>
                      {attempt.endpoint_note || '—'} / {attempt.key_note || '—'}
                    </dd>
                  </div>
                ) : null}
                <div>
                  <dt>{t('common.operations.logs.connectorModel')}</dt>
                  <dd>
                    {attempt.connector_type}
                    <br />
                    <span className="mono">{attempt.upstream_model_id}</span>
                  </dd>
                </div>
                <div>
                  <dt>
                    {t(
                      attempt.result_kind === 'synthetic'
                        ? 'common.operations.logs.recordedStatus'
                        : 'common.operations.logs.upstreamStatus',
                    )}
                  </dt>
                  <dd>
                    {attempt.status_code ?? '—'}{' '}
                    <span className="mono">{attempt.upstream_code ?? ''}</span>
                  </dd>
                </div>
                <div>
                  <dt>{t('logs.time')}</dt>
                  <dd>
                    {formatDateTime(attempt.started_at)} ·{' '}
                    {Math.max(0, attempt.completed_at - attempt.started_at)}s
                  </dd>
                </div>
                <div>
                  <dt>{t('logs.tokens')}</dt>
                  <dd>
                    <TokenBuckets row={attempt.usage} />
                  </dd>
                </div>
                <div>
                  <dt>{t('common.operations.logs.charge')}</dt>
                  <dd>{attempt.usage.charge}</dd>
                </div>
                <div>
                  <dt>{t('logs.diag')}</dt>
                  <dd className="mono log-attempt-diagnostic">{attempt.diag ?? '—'}</dd>
                </div>
              </dl>
            </li>
          ))}
        </ol>
      )}
      <PagePagination
        metadata={detail.attempt_pagination}
        requestedPage={page}
        busy={busy}
        onPageChange={onPageChange}
        onPageSizeChange={onPageSizeChange}
      />
    </div>
  );
}

interface RoleLogPanelProps {
  role: LogRole;
  language?: string;
  enabled?: boolean;
  onAuthorityLoss?: () => void;
  requestID?: string | null;
  onRequestClose?: () => void;
  accountId?: string;
  scopeReady?: boolean;
}

export function RoleLogPanel(props: RoleLogPanelProps) {
  const scope = useStationScope(props.role, props.accountId, props.scopeReady);
  const queryClient = useQueryClient();
  const previousScope = useRef({ role: props.role, accountID: scope.accountID });
  useEffect(() => {
    const previous = previousScope.current;
    previousScope.current = { role: props.role, accountID: scope.accountID };
    if (
      !previous.accountID ||
      (previous.role === props.role && previous.accountID === scope.accountID)
    )
      return;
    for (const kind of ['page', 'detail-page']) {
      queryClient.removeQueries({
        queryKey: [...numberedLogKeys.root(previous.role), kind, previous.accountID],
      });
    }
  }, [props.role, queryClient, scope.accountID]);
  return <ScopedRoleLogPanel key={`${props.role}:${scope.accountID}`} {...props} {...scope} />;
}

function ScopedRoleLogPanel({
  role,
  enabled = true,
  onAuthorityLoss,
  requestID,
  onRequestClose,
  accountID,
  scopeReady,
  sessionError,
}: RoleLogPanelProps & {
  accountID: string;
  scopeReady: boolean;
  sessionError: unknown;
}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const station = role === 'admin' ? 'admin' : 'user';
  const textParams = useMemo(
    () =>
      role === 'user'
        ? (['model', 'error_code', 'status'] as const)
        : role === 'admin'
          ? (['user_id', 'endpoint_base_url', 'upstream_model', 'error_code', 'status'] as const)
          : (['endpoint_base_url', 'upstream_model', 'error_code', 'status'] as const),
    [role],
  );
  const { state: urlState, patch: patchUrlState } = useLogUrlState(
    textParams,
    LOG_DEFAULT_PAGE_SIZE,
  );
  const filter = useMemo(() => {
    const next = validateLogFilter(role, urlState.filters);
    const from = urlState.fromUnix;
    const to = urlState.toUnix;
    if (from !== undefined && to !== undefined && from >= to) return next;
    return {
      ...next,
      ...(from === undefined ? {} : { from }),
      ...(to === undefined ? {} : { to }),
    } satisfies LogFiltersValue;
  }, [role, urlState.filters, urlState.fromUnix, urlState.toUnix]);
  const filterKey = useMemo(() => JSON.stringify(filter), [filter]);
  const pager = useUrlPagePager({
    station,
    listType: 'logs',
    scopeKey: accountID,
    scopeReady,
    resetKey: `${role}:${filterKey}`,
  });
  const requestIDValues = searchParams.getAll('request_id');
  const urlRequestID = validRequestID(requestIDValues.length === 1 ? requestIDValues[0] : null);
  const [selectedID, setSelectedID] = useState<string | null>(requestID ?? urlRequestID);
  const previousURLID = useRef(urlRequestID);
  const previousPropID = useRef(requestID);
  useEffect(() => {
    if (urlRequestID !== previousURLID.current) {
      previousURLID.current = urlRequestID;
      setSelectedID(urlRequestID);
    }
  }, [urlRequestID]);
  useEffect(() => {
    if (requestID === undefined || requestID === previousPropID.current) return;
    previousPropID.current = requestID;
    setSelectedID(requestID);
  }, [requestID]);
  const attemptPager = useUrlPagePager({
    station,
    listType: 'log-attempts',
    // Opening a request clears its nested URL window below. Deep links and
    // browser history already carry that request's window; closing the drawer
    // must not enqueue a new page reset after those parameters were removed.
    scopeKey: accountID,
    scopeReady,
    pageParam: 'attempt_page',
    pageSizeParam: 'attempt_page_size',
  });
  const [revoked, setRevoked] = useState(false);
  const [revokedError, setRevokedError] = useState<unknown>(null);
  const authorityClosedRef = useRef(false);
  const observerEnabled = enabled && scopeReady && !revoked;
  const logs = useRoleLogsPage(
    role,
    accountID,
    pager.page,
    pager.pageSize,
    filter,
    observerEnabled,
  );
  const detail = useRoleLogDetailPage(
    role,
    accountID,
    selectedID,
    attemptPager.page,
    attemptPager.pageSize,
    observerEnabled,
  );
  const authorityError = isFinalAuthorityError(sessionError)
    ? sessionError
    : isFinalAuthorityError(logs.error)
      ? logs.error
      : isFinalAuthorityError(detail.error)
        ? detail.error
        : null;
  const authorityClosed = revoked || authorityError !== null;

  useEffect(() => {
    if (!authorityError || authorityClosedRef.current) return;
    authorityClosedRef.current = true;
    setRevoked(true);
    setRevokedError(authorityError);
    setSelectedID(null);
    patchUrlState(
      { filters: {}, fromUnix: undefined, toUnix: undefined },
      { clearDetail: true, replace: true },
    );
    clearStationSession(queryClient, role === 'admin' ? 'admin' : 'steward');
    onAuthorityLoss?.();
  }, [authorityError, onAuthorityLoss, patchUrlState, queryClient, role]);

  const fields = useMemo<readonly LogFilterField[]>(() => {
    const values: LogFilterField[] = [];
    if (role === 'user')
      values.push({
        name: 'model',
        label: t('common.model'),
        ariaLabel: t('common.model'),
        maxLength: 133,
      });
    if (role === 'admin')
      values.push({
        name: 'user_id',
        label: t('common.userId'),
        ariaLabel: t('common.userId'),
        inputType: 'number',
        maxLength: 39,
      });
    if (role !== 'user') {
      values.push({
        name: 'endpoint_base_url',
        label: t('logs.endpointBaseUrl'),
        ariaLabel: t('logs.endpointBaseUrl'),
        maxLength: 512,
      });
      values.push({
        name: 'upstream_model',
        label: t('logs.upstreamModel'),
        ariaLabel: t('logs.upstreamModel'),
        maxLength: 512,
      });
    }
    values.push({
      name: 'error_code',
      label: t('logs.errorCode'),
      ariaLabel: t('logs.errorCode'),
      maxLength: 96,
    });
    values.push({
      name: 'status',
      label: t('common.status'),
      ariaLabel: t('common.status'),
      inputType: 'number',
      maxLength: 3,
    });
    return values;
  }, [role, t]);
  const routeLabel = (route: LogRouteKind) => t(ROUTE_LABEL_KEYS[route]);
  const resultLabel = (row: RoleLogRow) =>
    row.caller_result_class
      ? t(RESULT_LABEL_KEYS[row.caller_result_class])
      : t('common.operations.logs.resultValue.pending');

  const clearDetailURL = () => {
    setSearchParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.delete('request_id');
        next.delete('attempt_page');
        next.delete('attempt_page_size');
        const actualPage = logs.data?.pagination.page;
        if (actualPage !== undefined) next.set('page', actualPage);
        next.set('page_size', String(pager.pageSize));
        return next;
      },
      { replace: true },
    );
  };
  const closeDetail = () => {
    setSelectedID(null);
    onRequestClose?.();
    clearDetailURL();
  };
  const openDetail = (id: string) => {
    setSelectedID(id);
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set('request_id', id);
      const actualPage = logs.data?.pagination.page;
      if (actualPage !== undefined) next.set('page', actualPage);
      next.set('page_size', String(pager.pageSize));
      next.delete('attempt_page');
      next.delete('attempt_page_size');
      return next;
    });
  };
  const applyFilters = (next: {
    filters: Record<string, string>;
    fromUnix?: number;
    toUnix?: number;
  }) => {
    setSelectedID(null);
    patchUrlState({ ...next, page: 1 }, { clearDetail: true });
  };

  const columns: LogColumn<RoleLogRow>[] = [
    { key: 'time', header: t('logs.time'), render: (row) => formatDateTime(row.started_at) },
    { key: 'route', header: t('logs.routeKind'), render: (row) => routeLabel(row.route_kind) },
    ...(role === 'user'
      ? [
          {
            key: 'model',
            header: t('common.model'),
            render: (row: RoleLogRow) => ('model' in row ? row.model : '—'),
          },
        ]
      : []),
    ...(role === 'admin'
      ? [
          {
            key: 'user',
            header: t('common.userId'),
            render: (row: RoleLogRow) => ('user_id' in row ? (row.user_id ?? '—') : '—'),
          },
        ]
      : []),
    ...(role === 'steward'
      ? [
          {
            key: 'caller',
            header: t('logs.caller'),
            render: (row: RoleLogRow) =>
              row.role === 'steward' ? <CallerIdentity identity={row.caller_identity} /> : '—',
          },
        ]
      : []),
    { key: 'result', header: t('common.operations.logs.result'), render: resultLabel },
    { key: 'status', header: t('common.status'), render: (row) => row.caller_status ?? '—' },
    {
      key: 'error',
      header: t('logs.error'),
      render: (row) => <span className="mono">{row.caller_error_code ?? '—'}</span>,
    },
    { key: 'usage', header: t('logs.tokens'), render: (row) => <TokenBuckets row={row.usage} /> },
    {
      key: 'charge',
      header: t('common.operations.logs.charge'),
      render: (row) => <span className="mono">{row.usage.charge}</span>,
    },
  ];

  let detailBody: ReactNode = null;
  if (selectedID && detail.isPending) detailBody = t('common.loading');
  else if (selectedID && detail.error)
    detailBody =
      isApiError(detail.error) && detail.error.code === 'not_found' ? (
        <p role="status">{t('common.operations.logs.requestUnavailable')}</p>
      ) : (
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      );

  const detailData = detail.error ? undefined : detail.data;
  const detailRequest = detailData ? requestFrom(detailData) : null;
  const detailFields =
    detailData && detailRequest
      ? [
          {
            label: t('common.operations.logs.request'),
            value: <span className="mono">{detailRequest.id}</span>,
          },
          ...(role === 'steward' && detailRequest.role === 'steward'
            ? [
                {
                  label: t('logs.caller'),
                  value: <CallerIdentity identity={detailRequest.caller_identity} />,
                },
              ]
            : []),
          { label: t('logs.routeKind'), value: routeLabel(detailRequest.route_kind) },
          {
            label: t('common.operations.logs.callerResult'),
            value: `${resultLabel(detailRequest)} / ${detailRequest.caller_status ?? '—'}`,
          },
          {
            label: t('common.operations.logs.callerError'),
            value: <span className="mono">{detailRequest.caller_error_code ?? '—'}</span>,
          },
          { label: t('logs.tokens'), value: <TokenBuckets row={detailRequest.usage} /> },
          ...('attempts' in detailData
            ? [
                {
                  label: t('common.operations.logs.attempts'),
                  wide: true,
                  value: (
                    <AttemptTable
                      detail={detailData}
                      page={attemptPager.page}
                      busy={detail.isFetching}
                      onPageChange={attemptPager.setPage}
                      onPageSizeChange={attemptPager.setPageSize}
                    />
                  ),
                },
              ]
            : []),
          ...('kind' in detailData && detailData.kind === 'charity'
            ? [
                {
                  label: t('common.operations.logs.result'),
                  value: t(RESULT_LABEL_KEYS[detailData.caller_safe_result.class]),
                },
              ]
            : []),
        ]
      : detailBody
        ? [{ label: t('logs.details'), value: detailBody }]
        : [];

  const title =
    role === 'admin'
      ? t('admin.logs.logsTitle')
      : role === 'user'
        ? t('user.logs.title')
        : t('common.operations.logs.stewardTitle');
  const empty =
    role === 'admin'
      ? t('admin.logs.noLogs')
      : role === 'user'
        ? t('user.logs.empty')
        : t('common.operations.logs.stewardEmpty');
  const emptyBody =
    role === 'admin'
      ? t('admin.logs.noLogsBody')
      : role === 'user'
        ? t('user.logs.emptyBody')
        : t('common.operations.logs.stewardEmptyBody');

  if (authorityClosed) {
    return (
      <Card>
        <ErrorState error={authorityError ?? revokedError} />
      </Card>
    );
  }

  if (!enabled || !scopeReady || !accountID)
    return (
      <Card>
        <LoadingState />
      </Card>
    );

  const pageData = logs.data;
  const listBusy = logs.isFetching;
  return (
    <Card className="ops-stack">
      <div className="card-title-row">
        <h2>{title}</h2>
        {role === 'admin' ? (
          <div className="ops-actions">
            <a className="btn btn-secondary" href={adminLogExportPath(filter, 'csv')} download>
              {t('admin.logs.exportCsv')}
            </a>
            <a className="btn btn-secondary" href={adminLogExportPath(filter, 'json')} download>
              {t('admin.logs.exportJson')}
            </a>
          </div>
        ) : null}
      </div>
      <LogFilters station={station} fields={fields} state={urlState} onApply={applyFilters} />
      {logs.error && !pageData ? (
        <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
      ) : pageData ? (
        <div className="ops-stack" aria-busy={listBusy}>
          {logs.error ? (
            <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />
          ) : null}
          {pageData.data.length === 0 ? (
            <EmptyState title={empty} body={emptyBody} />
          ) : (
            <LogTable
              caption={title}
              columns={columns}
              rows={pageData.data}
              rowKey={(row) => row.id}
              actions={(row) => (
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={() => openDetail(row.id)}
                >
                  {t('logs.details')}
                </button>
              )}
            />
          )}
          <PagePagination
            metadata={pageData.pagination}
            requestedPage={pager.page}
            busy={listBusy}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        </div>
      ) : logs.isPending ? (
        <LoadingState />
      ) : (
        <LoadingState />
      )}
      <LogDetailDrawer
        open={Boolean(selectedID)}
        onClose={closeDetail}
        title={selectedID ? `${t('logs.drawerTitle')} ${selectedID}` : t('logs.drawerTitle')}
        fields={detailFields}
      />
    </Card>
  );
}
