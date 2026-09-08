import { useQuery, type QueryKey, type UseQueryResult } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
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
  type AdminLogAttempt,
  type AdminLogRow,
  type CallerIdentity,
  type LogFiltersValue,
  type LogRole,
  type RoleLogAttempt,
  type RoleLogRow,
  type StewardLogAttempt,
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

export type NumberedUserLogDetail =
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
    };

export interface NumberedAdminLogDetail {
  request: AdminLogRow;
  attempts: CursorPage<AdminLogAttempt>;
  attempt_pagination: PageMetadata;
}

export interface NumberedStewardLogDetail {
  request: StewardLogRow;
  attempts: CursorPage<StewardLogAttempt>;
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
  user: ['model', 'error_code', 'status', 'from', 'to'],
  steward: ['endpoint_base_url', 'upstream_model', 'error_code', 'status', 'from', 'to'],
  admin: ['user_id', 'endpoint_base_url', 'upstream_model', 'error_code', 'status', 'from', 'to'],
};
const MAX_LOG_UNIX_SECOND = 253_402_300_799;
const MAX_LOG_USER_ID = 9_223_372_036_854_775_807n;

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
    const text = requestString(raw, `log ${key} filter`, key === 'status' ? 3 : 512);
    if (text === '') continue;
    if (key === 'status' && !/^[1-5][0-9]{2}$/.test(text)) invalidRequest('log status filter');
    if (key === 'user_id' && (!/^[1-9][0-9]*$/.test(text) || BigInt(text) > MAX_LOG_USER_ID))
      invalidRequest('log user id filter');
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
  validatePageResponse(pagination, window.page, window.pageSize, data.length);
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
): NumberedRoleLogDetail {
  if (role === 'user') {
    const probe = record(
      value,
      ['request', 'attempts', 'attempt_pagination', 'caller_safe_result'],
      'user log detail',
      ['request'],
    );
    const request = normalizeUserLogRow(probe.request);
    if (request.kind === 'charity') {
      const root = record(value, ['request', 'caller_safe_result'], 'charity log detail');
      const safe = record(root.caller_safe_result, ['class'], 'charity caller-safe result');
      return {
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
    const root = record(value, ['request', 'attempts', 'attempt_pagination'], 'self log detail');
    const attemptPagination = normalizePageMetadata(root.attempt_pagination);
    const attempts = normalizedAttempts(
      root.attempts,
      'self log attempts',
      window,
      normalizeUserLogAttempt,
    );
    validatePageResponse(attemptPagination, window.page, window.pageSize, attempts.data.length);
    return { kind: 'self', request, attempts, attempt_pagination: attemptPagination };
  }

  const root = record(value, ['request', 'attempts', 'attempt_pagination'], `${role} log detail`);
  const attemptPagination = normalizePageMetadata(root.attempt_pagination);
  if (role === 'admin') {
    const attempts = normalizedAttempts(
      root.attempts,
      'admin log attempts',
      window,
      normalizeAdminLogAttempt,
    );
    validatePageResponse(attemptPagination, window.page, window.pageSize, attempts.data.length);
    return {
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
  validatePageResponse(attemptPagination, window.page, window.pageSize, attempts.data.length);
  return {
    request: normalizeStewardLogRow(root.request),
    attempts,
    attempt_pagination: attemptPagination,
  };
}

function detailPath(role: LogRole, id: string, window: LogPageWindow): string {
  if (typeof id !== 'string' || !/^req_[A-Za-z0-9_-]{22}$/.test(id) || !/[AQgw]$/.test(id)) {
    invalidRequest('log id');
  }
  return queryPath(`${ROLE_PATHS[role]}/${encodeURIComponent(id)}`, {
    attempt_page: window.page,
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

export async function getRoleLogsPage(
  role: LogRole,
  page: string,
  pageSize: PageSize,
  filters: LogFiltersValue = {},
  signal?: AbortSignal,
): Promise<NumberedLogPage> {
  const window = windowInput(page, pageSize);
  const safeFilters = requestFilter(role, filters);
  return decoded(
    `${ROLE_PATHS[role]}?${pageQuery(role, window, safeFilters)}`,
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
): Promise<NumberedRoleLogDetail> {
  const window = windowInput(attemptPage, attemptPageSize);
  return decoded(
    detailPath(role, id, window),
    (value) => {
      const result = normalizedDetail(value, role, window);
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
  detail: (role: LogRole, accountId: string, id: string, page: string, pageSize: PageSize) =>
    [...numberedLogKeys.root(role), 'detail-page', accountId, id, page, pageSize] as const,
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

export function useRoleLogsPage(
  role: LogRole,
  accountId: string,
  page: string,
  pageSize: PageSize,
  filters: LogFiltersValue,
  enabled = true,
): UseQueryResult<NumberedLogPage, Error> {
  const prefix = [...numberedLogKeys.root(role), 'page', accountId] as const;
  return useQuery<NumberedLogPage, Error>({
    queryKey: numberedLogKeys.list(role, accountId, page, pageSize, filters),
    queryFn: ({ signal }) => getRoleLogsPage(role, page, pageSize, filters, signal),
    enabled: enabled && Boolean(accountId) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: sameScopePlaceholder<NumberedLogPage>(prefix),
    retry: false,
  });
}

export function useRoleLogDetailPage(
  role: LogRole,
  accountId: string,
  id: string | null,
  attemptPage: string,
  attemptPageSize: PageSize,
  enabled = true,
): UseQueryResult<NumberedRoleLogDetail, Error> {
  const prefix = [...numberedLogKeys.root(role), 'detail-page', accountId, id ?? 'none'] as const;
  return useQuery<NumberedRoleLogDetail, Error>({
    queryKey: numberedLogKeys.detail(role, accountId, id ?? 'none', attemptPage, attemptPageSize),
    queryFn: ({ signal }) => {
      if (!id) throw new ApiError('invalid_request', 'A log id is required.', 400);
      return getRoleLogDetailPage(role, id, attemptPage, attemptPageSize, signal);
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
