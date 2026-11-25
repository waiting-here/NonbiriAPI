import { normalizeDelivery, type DeliveryDiagnostics } from '@shared/observability/failure';
import { normalizeRouteDecision } from './routeDecision';
import {
  ATTEMPT_SEQ_PARAM,
  logAttemptSequence,
  normalizeFailureSummary,
} from './managementFailures';
import {
  hashKey,
  useQuery,
  useQueryClient,
  type QueryKey,
  type UseQueryResult,
} from '@tanstack/react-query';
import { useEffect, useMemo, useState } from 'react';
import { ApiError, apiFetch, isApiError } from '@shared/query/http';
import { decoded, queryPath } from '@shared/operations/api';
import { array, invalidResponse, oneOf, record, type CursorPage } from '@shared/operations/wire';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';
import {
  normalizeAdminLogAttempt,
  normalizeAdminLogRow,
  normalizeStewardLogAttempt,
  normalizeStewardLogRow,
  normalizeUserLogAttempt,
  normalizeUserLogRow,
  validLogDiscordID,
  validLogResourceID,
  logRequestID,
  isLogOperation,
  isLogOutcome,
  type AdminLogAttempt,
  type AdminLogDetail,
  type CallerIdentity,
  type LogFiltersValue,
  type LogRole,
  type RoleLogAttempt,
  type RoleLogRow,
  type StewardLogAttempt,
  type StewardLogDetail,
  type StewardLogRow,
  type UserCharityLogRow,
  type UserLogAttempt,
  type UserLogRow,
  type UserSelfLogRow,
} from './data';

export interface NumberedLogPage<Row extends RoleLogRow = RoleLogRow> {
  data: Row[];
  next_cursor: null;
  pagination: PageMetadata;
}

export type NumberedUserLogDetail = { delivery?: DeliveryDiagnostics | null } & (
  | {
      kind: 'self';
      request: UserSelfLogRow;
      attempts: CursorPage<UserLogAttempt>;
      attempt_pagination: PageMetadata;
    }
  | {
      kind: 'charity';
      request: UserCharityLogRow;
      caller_safe_result: { class: 'success' | 'failed' | 'cancelled' };
    }
);

export interface NumberedAdminLogDetail extends AdminLogDetail {
  attempt_pagination: PageMetadata;
}

export interface NumberedStewardLogDetail extends StewardLogDetail {
  attempt_pagination: PageMetadata;
}

export type NumberedRoleLogDetail =
  NumberedUserLogDetail | NumberedAdminLogDetail | NumberedStewardLogDetail;

export interface LogPageWindow {
  page: string;
  pageSize: PageSize;
}

const ROLE_PATHS: Record<LogRole, string> = {
  user: '/api/logs',
  steward: '/api/steward/logs',
  admin: '/admin/api/logs',
};

const LOG_FILTER_KEYS: Record<LogRole, readonly (keyof LogFiltersValue)[]> = {
  user: ['outcome', 'operation', 'model', 'error_code', 'status', 'from', 'to', 'phase'],
  steward: [
    'operation',
    'user_id',
    'discord_id',
    'endpoint_key_id',
    'endpoint_base_url',
    'upstream_model',
    'charity_model',
    'error_code',
    'status',
    'from',
    'to',
    'phase',
    'usage_total_mismatch',
    'outcome',
    'error_category',
    'error_phase',
  ],
  admin: [
    'operation',
    'user_id',
    'discord_id',
    'endpoint_key_id',
    'endpoint_base_url',
    'upstream_model',
    'charity_model',
    'error_code',
    'status',
    'from',
    'to',
    'phase',
    'usage_total_mismatch',
    'outcome',
    'error_category',
    'error_phase',
  ],
};
const MAX_LOG_UNIX_SECOND = 253_402_300_799;

function invalidRequest(label: string): never {
  throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
}

function requestString(value: unknown, label: string, maximum: number): string {
  if (typeof value !== 'string' || value.length === 0 || Array.from(value).length > maximum) {
    invalidRequest(label);
  }
  for (const character of value) {
    const code = character.codePointAt(0) ?? 0;
    if (code < 0x20 || code === 0x7f) invalidRequest(label);
  }
  return value.trim();
}

function requestFilter(role: LogRole, value: LogFiltersValue): LogFiltersValue {
  if (value === null || typeof value !== 'object' || Array.isArray(value))
    invalidRequest('log filters');
  const allowed = new Set(LOG_FILTER_KEYS[role]);
  if (Object.keys(value).some((key) => !allowed.has(key as keyof LogFiltersValue)))
    invalidRequest('log filters');

  const result: LogFiltersValue = {};
  for (const key of LOG_FILTER_KEYS[role]) {
    const raw = value[key];
    if (raw === undefined) continue;
    if (key === 'from' || key === 'to') {
      if (
        !Number.isSafeInteger(raw) ||
        (raw as number) < 0 ||
        (raw as number) > MAX_LOG_UNIX_SECOND
      )
        invalidRequest(`log ${key} filter`);
      result[key] = raw as never;
      continue;
    }
    if (key === 'usage_total_mismatch') {
      if (raw !== true) invalidRequest('log usage total mismatch filter');
      result.usage_total_mismatch = true;
      continue;
    }
    if (key === 'outcome' && !isLogOutcome(role, raw)) invalidRequest('log outcome filter');
    if (key === 'operation' && !isLogOperation(raw)) invalidRequest('log operation filter');
    const text = requestString(raw, `log ${key} filter`, key === 'status' ? 3 : 512);
    if (text === '') continue;
    if (key === 'phase' && text !== 'handler' && text !== 'pre_handler')
      invalidRequest('log phase filter');
    if (key === 'discord_id' && !validLogDiscordID(text)) invalidRequest('log Discord ID filter');
    if (key === 'status' && !/^[1-5][0-9]{2}$/.test(text)) invalidRequest('log status filter');
    if ((key === 'user_id' || key === 'endpoint_key_id') && !validLogResourceID(text))
      invalidRequest(`log ${key} filter`);
    result[key] = text as never;
  }
  if (result.from !== undefined && result.to !== undefined && result.from >= result.to)
    invalidRequest('log time range');
  return result;
}

function windowInput(page: string, pageSize: PageSize): LogPageWindow {
  if (!isPageNumber(page)) invalidRequest('log page');
  if (!isPageSize(pageSize)) invalidRequest('log page size');
  return { page, pageSize };
}

function pageQuery(role: LogRole, window: LogPageWindow, filters: LogFiltersValue): string {
  const params = new URLSearchParams();
  for (const key of LOG_FILTER_KEYS[role]) {
    const value = filters[key];
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  params.set('page', window.page);
  params.set('page_size', String(window.pageSize));
  return params.toString();
}

function roleRowDecoder(role: LogRole): (value: unknown) => RoleLogRow {
  if (role === 'admin') return normalizeAdminLogRow;
  if (role === 'steward') return normalizeStewardLogRow;
  return normalizeUserLogRow;
}

function normalizedPage<Row extends RoleLogRow>(
  value: unknown,
  label: string,
  window: LogPageWindow,
  decoder: (value: unknown) => Row,
): NumberedLogPage<Row> {
  const root = record(value, ['data', 'next_cursor', 'pagination'], label);
  if (root.next_cursor !== null) invalidResponse(`${label} cursor`);
  const pagination = normalizePageMetadata(root.pagination);
  const data = array(root.data, `${label} data`, window.pageSize).map(decoder);
  const identities = new Set(data.map((entry) => entry.id));
  if (identities.size !== data.length) invalidResponse(`${label} identities`);
  validatePageResponse(pagination, window.page, window.pageSize);
  return { data, next_cursor: null, pagination };
}

function normalizedAttempts<Row extends RoleLogAttempt>(
  value: unknown,
  label: string,
  window: LogPageWindow,
  decoder: (value: unknown) => Row,
): CursorPage<Row> {
  const root = record(value, ['data', 'next_cursor'], label);
  if (root.next_cursor !== null) invalidResponse(`${label} cursor`);
  const data = array(root.data, `${label} data`, window.pageSize).map(decoder);
  const identities = new Set(data.map((entry) => entry.attempt_seq));
  if (identities.size !== data.length) invalidResponse(`${label} identities`);
  return { data, next_cursor: null };
}

function normalizedDetail(
  value: unknown,
  role: LogRole,
  window: LogPageWindow,
  attemptSeq?: string,
): NumberedRoleLogDetail {
  if (role === 'user') {
    const probe = record(
      value,
      ['request', 'attempts', 'attempt_pagination', 'caller_safe_result', 'delivery'],
      'user log detail',
      ['request'],
    );
    const request = normalizeUserLogRow(probe.request);
    if (request.kind === 'charity') {
      const root = record(
        value,
        ['request', 'caller_safe_result', 'delivery'],
        'charity log detail',
        ['request', 'caller_safe_result'],
      );
      const safe = record(root.caller_safe_result, ['class'], 'charity caller-safe result');
      return {
        delivery: normalizeDelivery(root.delivery),
        kind: 'charity',
        request,
        caller_safe_result: {
          class: oneOf(
            safe.class,
            ['success', 'failed', 'cancelled'] as const,
            'charity caller-safe class',
          ),
        },
      };
    }
    const root = record(
      value,
      ['request', 'attempts', 'attempt_pagination', 'delivery'],
      'self log detail',
      ['request', 'attempts', 'attempt_pagination'],
    );
    const attemptPagination = normalizePageMetadata(root.attempt_pagination);
    const attempts = normalizedAttempts(
      root.attempts,
      'self log attempts',
      window,
      normalizeUserLogAttempt,
    );
    validatePageResponse(attemptPagination, window.page, window.pageSize);
    return {
      kind: 'self',
      delivery: normalizeDelivery(root.delivery),
      request,
      attempts,
      attempt_pagination: attemptPagination,
    };
  }

  const root = record(
    value,
    ['request', 'attempts', 'attempt_pagination', 'delivery', 'route_decision', 'failure_summary'],
    `${role} log detail`,
    ['request', 'attempts', 'attempt_pagination'],
  );
  const attemptPagination = normalizePageMetadata(root.attempt_pagination);
  validatePageResponse(
    attemptPagination,
    attemptSeq === undefined ? window.page : attemptPagination.page,
    window.pageSize,
  );
  const failureSummary = normalizeFailureSummary(root.failure_summary);
  if (role === 'admin') {
    const attempts = normalizedAttempts(
      root.attempts,
      'admin log attempts',
      window,
      normalizeAdminLogAttempt,
    );
    return {
      route_decision: normalizeRouteDecision(root.route_decision),
      failure_summary: failureSummary,
      delivery: normalizeDelivery(root.delivery),
      request: normalizeAdminLogRow(root.request),
      attempts,
      attempt_pagination: attemptPagination,
    };
  }
  const attempts = normalizedAttempts(
    root.attempts,
    'steward log attempts',
    window,
    normalizeStewardLogAttempt,
  );
  return {
    route_decision: normalizeRouteDecision(root.route_decision),
    failure_summary: failureSummary,
    delivery: normalizeDelivery(root.delivery),
    request: normalizeStewardLogRow(root.request),
    attempts,
    attempt_pagination: attemptPagination,
  };
}

function detailPath(role: LogRole, id: string, window: LogPageWindow, attemptSeq?: string): string {
  if (!logRequestID(id)) {
    invalidRequest('log id');
  }
  if (attemptSeq !== undefined && (role === 'user' || !logAttemptSequence(attemptSeq))) {
    invalidRequest('attempt sequence');
  }
  return queryPath(`${ROLE_PATHS[role]}/${encodeURIComponent(id)}`, {
    attempt_page: attemptSeq === undefined ? window.page : undefined,
    [ATTEMPT_SEQ_PARAM]: attemptSeq,
    attempt_page_size: window.pageSize,
  });
}

export function normalizeRoleLogPage(
  value: unknown,
  role: LogRole,
  page: string,
  pageSize: PageSize,
): NumberedLogPage {
  const window = windowInput(page, pageSize);
  return normalizedPage(value, `${role} logs`, window, roleRowDecoder(role));
}

export function normalizeRoleLogDetail(
  value: unknown,
  role: LogRole,
  attemptPage: string,
  attemptPageSize: PageSize,
): NumberedRoleLogDetail {
  return normalizedDetail(value, role, windowInput(attemptPage, attemptPageSize));
}

function listPath(role: LogRole, window: LogPageWindow, filters: LogFiltersValue): string {
  return `${ROLE_PATHS[role]}?${pageQuery(role, window, requestFilter(role, filters))}`;
}

function searchPath(role: LogRole, id: string): string {
  return `${ROLE_PATHS[role].replace(/\/logs$/, '/log-searches')}/${encodeURIComponent(id)}`;
}

const LONG_SEARCH_MS = 5_000;
const HANDOFF_GRACE_MS = 5_000;
const SEARCH_POLL_MS = 1_000;

function decodeSearchPage(value: unknown, role: LogRole, window: LogPageWindow) {
  if (value && typeof value === 'object' && 'status' in value && value.status === 'running') {
    const id = 'search_id' in value ? value.search_id : undefined;
    if (typeof id !== 'string' || !/^[A-Za-z0-9_-]+$/.test(id)) invalidResponse('log search');
    return { id };
  }
  return { page: normalizedPage(value, `${role} logs`, window, roleRowDecoder(role)) };
}

function deleteSearch(role: LogRole, id: string): Promise<void> {
  return apiFetch<void>(searchPath(role, id), { method: 'DELETE', keepalive: true });
}

async function startSearch(
  role: LogRole,
  path: string,
  window: LogPageWindow,
  signal: AbortSignal,
) {
  signal.throwIfAborted();
  const started = performance.now();
  const request = new AbortController();
  let grace: ReturnType<typeof globalThis.setTimeout> | undefined;
  const stop = () => {
    // Once handoff is due, accept a late receipt so we can release the server search.
    if (performance.now() - started < LONG_SEARCH_MS) request.abort();
    else grace = globalThis.setTimeout(() => request.abort(), HANDOFF_GRACE_MS);
  };
  signal.addEventListener('abort', stop, { once: true });
  try {
    return await decoded(path, (value) => decodeSearchPage(value, role, window), {
      signal: request.signal,
      headers: { Prefer: 'respond-async' },
    });
  } finally {
    signal.removeEventListener('abort', stop);
    globalThis.clearTimeout(grace);
  }
}

function waitForSearch(signal: AbortSignal) {
  signal.throwIfAborted();
  return new Promise<void>((resolve, reject) => {
    const cancel = () => {
      globalThis.clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = globalThis.setTimeout(() => {
      signal.removeEventListener('abort', cancel);
      resolve();
    }, SEARCH_POLL_MS);
    signal.addEventListener('abort', cancel, { once: true });
  });
}

function lostSearch(error: unknown): boolean {
  return (
    isApiError(error) &&
    (error.status === 404 || error.status === 410 || error.code === 'refresh_required')
  );
}

function createSearchSession(role: LogRole, scope: string, invalidate: () => void) {
  let lifetime = new AbortController();
  let job:
    { id: string; pending: boolean; cancelRequested?: true; deletion?: Promise<void> } | undefined;
  const release = () => {
    if (!job) return;
    const { id, deletion } = job;
    job = undefined;
    // The server expires searches whose cancellation could not be delivered.
    void (deletion ?? deleteSearch(role, id)).catch(() => {});
    invalidate();
  };

  return {
    scope,
    release,
    hasJob: () => job !== undefined,
    async cancel() {
      if (!job) return;
      const current = job;
      current.cancelRequested = true;
      current.deletion ??= deleteSearch(role, current.id);
      try {
        await current.deletion;
      } catch (error) {
        if (!isApiError(error) || (error.status !== 404 && error.status !== 410)) throw error;
      } finally {
        delete current.deletion;
      }
      if (job === current) {
        job = undefined;
        invalidate();
      }
    },
    close: () => {
      lifetime.abort();
      lifetime = new AbortController();
      release();
    },
    async read(
      page: string,
      pageSize: PageSize,
      filters: LogFiltersValue,
      requestSignal: AbortSignal,
      showLong: () => void,
    ): Promise<NumberedLogPage> {
      const signal = AbortSignal.any([requestSignal, lifetime.signal]);
      const window = windowInput(page, pageSize);
      if (job?.cancelRequested) release();
      let recovered = false;
      for (;;) {
        signal.throwIfAborted();
        if (!job) {
          const result = await startSearch(role, listPath(role, window, filters), window, signal);
          if (signal.aborted && result.id) void deleteSearch(role, result.id).catch(() => {});
          signal.throwIfAborted();
          if (result.page) return result.page;
          job = { id: result.id, pending: true };
          // Cached pages from before handoff describe a different set of matches.
          invalidate();
        }

        const current = job;
        const path = queryPath(searchPath(role, current.id), { page, page_size: pageSize });
        if (current.pending) showLong();
        try {
          for (;;) {
            const result = await decoded(path, (value) => decodeSearchPage(value, role, window), {
              signal,
            });
            signal.throwIfAborted();
            if (result.page) {
              current.pending = false;
              return result.page;
            }
            if (result.id !== current.id) invalidResponse('log search identity');
            current.pending = true;
            showLong();
            await waitForSearch(signal);
          }
        } catch (error) {
          signal.throwIfAborted();
          if (!lostSearch(error)) {
            // A broken connection does not invalidate a server search.
            if (!isApiError(error) || error.code !== 'network_error') release();
            throw error;
          }
          job = undefined;
          invalidate();
          if (recovered) throw error;
          recovered = true;
        }
      }
    },
  };
}

export async function getRoleLogsPage(
  role: LogRole,
  page: string,
  pageSize: PageSize,
  filters: LogFiltersValue = {},
  signal?: AbortSignal,
): Promise<NumberedLogPage> {
  const window = windowInput(page, pageSize);
  return decoded(
    listPath(role, window, filters),
    (value) => normalizedPage(value, `${role} logs`, window, roleRowDecoder(role)),
    { signal },
  );
}

export async function getRoleLogDetailPage(
  role: LogRole,
  id: string,
  attemptPage: string,
  attemptPageSize: PageSize,
  signal?: AbortSignal,
  attemptSeq?: string,
): Promise<NumberedRoleLogDetail> {
  const window = windowInput(attemptPage, attemptPageSize);
  return decoded(
    detailPath(role, id, window, attemptSeq),
    (value) => {
      const result = normalizedDetail(value, role, window, attemptSeq);
      if (result.request.id !== id) invalidResponse('log detail identity');
      return result;
    },
    {
      signal,
    },
  );
}

export const numberedLogKeys = {
  root: (role: LogRole) =>
    role === 'admin'
      ? (['admin', 'operations', 'logs'] as const)
      : role === 'steward'
        ? (['user', 'operations', 'steward', 'logs'] as const)
        : (['user', 'operations', 'logs'] as const),
  list: (
    role: LogRole,
    accountId: string,
    page: string,
    pageSize: PageSize,
    filters: LogFiltersValue,
  ) => [...numberedLogKeys.root(role), 'page', accountId, page, pageSize, filters] as const,
  detail: (
    role: LogRole,
    accountId: string,
    id: string,
    page: string,
    pageSize: PageSize,
    attemptSeq?: string,
  ) =>
    [
      ...numberedLogKeys.root(role),
      'detail-page',
      accountId,
      id,
      ...(attemptSeq === undefined ? [page] : ['attempt', attemptSeq]),
      pageSize,
    ] as const,
};

function hasPrefix(query: { queryKey: QueryKey } | undefined, prefix: QueryKey): boolean {
  const key = query?.queryKey;
  return Boolean(
    key &&
    key.length >= prefix.length &&
    prefix.every((part, index) => Object.is(key[index], part)),
  );
}

function sameScopePlaceholder<T>(prefix: QueryKey) {
  return (
    previousData: T | undefined,
    previousQuery: { queryKey: QueryKey } | undefined,
  ): T | undefined =>
    previousData !== undefined && hasPrefix(previousQuery, prefix) ? previousData : undefined;
}

type LogCancellationStatus = 'canceling' | 'canceled' | 'cancel-error';

export type LogSearchStatus = 'loading' | 'long' | 'complete' | 'error' | LogCancellationStatus;

export function useRoleLogsPage(
  role: LogRole,
  accountId: string,
  page: string,
  pageSize: PageSize,
  filters: LogFiltersValue,
  enabled = true,
) {
  const client = useQueryClient();
  const prefix = useMemo(
    () => [...numberedLogKeys.root(role), 'page', accountId] as const,
    [role, accountId],
  );
  const scope = hashKey([prefix, filters, enabled]);
  const search = useMemo(
    () =>
      createSearchSession(role, scope, () => {
        void client.invalidateQueries({ queryKey: prefix, refetchType: 'none' });
      }),
    [client, prefix, role, scope],
  );
  useEffect(() => search.close, [search]);

  const queryKey = numberedLogKeys.list(role, accountId, page, pageSize, filters);
  const windowKey = hashKey([page, pageSize]);
  const [long, setLong] = useState<{ scope: typeof search; window: string }>();
  const [cancellation, setCancellation] = useState<{
    scope: typeof search;
    window: string;
    status: LogCancellationStatus;
  }>();
  const cancelStatus =
    cancellation?.scope === search && cancellation.window === windowKey
      ? cancellation.status
      : undefined;
  const query = useQuery<NumberedLogPage, Error>({
    queryKey,
    queryFn: async ({ signal }) => {
      setLong(undefined);
      let announcedLong = false;
      const showLong = () => {
        if (signal.aborted || announcedLong) return;
        announcedLong = true;
        setLong({ scope: search, window: windowKey });
      };
      const timer = globalThis.setTimeout(showLong, LONG_SEARCH_MS);
      try {
        return await search.read(page, pageSize, filters, signal, showLong);
      } finally {
        globalThis.clearTimeout(timer);
      }
    },
    enabled:
      enabled && !cancelStatus && Boolean(accountId) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: sameScopePlaceholder<NumberedLogPage>(prefix),
    networkMode: 'always',
    retry: false,
  });
  // Leaving a canceled page via history starts a search for that requested window.
  if (cancellation && !cancelStatus) setCancellation(undefined);

  const searchStatus: LogSearchStatus =
    cancelStatus ??
    (query.isFetching
      ? long?.scope === search && long.window === windowKey
        ? 'long'
        : 'loading'
      : query.error
        ? 'error'
        : query.data
          ? 'complete'
          : 'loading');

  return {
    ...query,
    searchStatus,
    settledOrdinaryResult: () =>
      searchStatus === 'complete' &&
      !search.hasJob() &&
      client.isFetching({ queryKey, exact: true }) === 0,
    retry: () => void query.refetch(),
    refresh: () => {
      search.release();
      setCancellation(undefined);
      void query.refetch();
    },
    cancel: () => {
      if (!query.isFetching && cancelStatus !== 'cancel-error') return;
      const intent = { scope: search, window: windowKey, status: 'canceling' } as const;
      setCancellation(intent);
      void client.cancelQueries({ queryKey, exact: true });
      const settle = (status: LogCancellationStatus) =>
        setCancellation((current) => (current === intent ? { ...intent, status } : current));
      void search.cancel().then(
        () => settle('canceled'),
        () => settle('cancel-error'),
      );
    },
  };
}

export function useRoleLogDetailPage(
  role: LogRole,
  accountId: string,
  id: string | null,
  attemptPage: string,
  attemptPageSize: PageSize,
  enabled = true,
  attemptSeq?: string,
): UseQueryResult<NumberedRoleLogDetail, Error> {
  const prefix = [...numberedLogKeys.root(role), 'detail-page', accountId, id ?? 'none'] as const;
  return useQuery<NumberedRoleLogDetail, Error>({
    queryKey: numberedLogKeys.detail(
      role,
      accountId,
      id ?? 'none',
      attemptPage,
      attemptPageSize,
      attemptSeq,
    ),
    queryFn: ({ signal }) => {
      if (!id) throw new ApiError('invalid_request', 'A log id is required.', 400);
      return getRoleLogDetailPage(role, id, attemptPage, attemptPageSize, signal, attemptSeq);
    },
    enabled:
      enabled &&
      Boolean(accountId && id) &&
      isPageNumber(attemptPage) &&
      isPageSize(attemptPageSize),
    placeholderData: id ? sameScopePlaceholder<NumberedRoleLogDetail>(prefix) : undefined,
    retry: false,
  });
}

// These aliases make the numbered layer easy to consume from tests and from
// future role-specific pages while keeping the shared panel's names concise.
export const getRoleLogPage = getRoleLogsPage;
export const useRoleLogPage = useRoleLogsPage;

export type {
  AdminLogAttempt,
  CallerIdentity,
  RoleLogAttempt,
  StewardLogAttempt,
  StewardLogRow,
  UserLogAttempt,
  UserLogRow,
};
