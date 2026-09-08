import { decoded, queryPath } from '@shared/operations/api';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  type PageMetadata,
  type PageSize,
  validatePageResponse,
} from '@shared/operations/pageNumbers';
import { array, decimal, decimalID, invalidResponse, record } from '@shared/operations/wire';
import { ApiError } from '@shared/query/http';
import {
  normalizeAdminUser,
  normalizeEndpointOverview,
  type EndpointOverview,
  type AdminUser,
} from './core';
import {
  normalizeActivitiesConfig,
  normalizePeriod,
  normalizePool,
  type ActivitiesConfig,
  type Period,
  type Pool,
} from './economy';

export interface AdminPage<T> {
  data: T[];
  next_cursor: null;
  pagination: PageMetadata;
}

function normalizePage<T>(
  value: unknown,
  label: string,
  item: (value: unknown) => T,
  requestedPage: string,
  requestedSize: PageSize,
  identity?: (value: T) => string,
): AdminPage<T> {
  if (!isPageNumber(requestedPage)) invalidResponse(`${label} requested page`);
  const root = record(value, ['data', 'next_cursor', 'pagination'], label);
  if (root.next_cursor !== null) invalidResponse(`${label} cursor`);
  const pagination = normalizePageMetadata(root.pagination);
  if (pagination.page_size !== requestedSize) invalidResponse(`${label} page size`);
  const data = array(root.data, `${label} data`, pagination.page_size).map(item);
  validatePageResponse(pagination, requestedPage, requestedSize, data.length);
  if (identity && new Set(data.map(identity)).size !== data.length) {
    invalidResponse(`${label} duplicate identity`);
  }
  return { data, next_cursor: null, pagination };
}

export function normalizeAdminPageResponse<T>(
  value: unknown,
  label: string,
  item: (value: unknown) => T,
  requestedPage: string,
  requestedSize: PageSize,
): AdminPage<T> {
  return normalizePage(value, label, item, requestedPage, requestedSize);
}

function normalizeEndpointOverviewPageItem(value: unknown): EndpointOverview {
  const group = normalizeEndpointOverview(value);
  if (group.users.length > 3) invalidResponse('endpoint overview preview');
  const users = BigInt(group.user_count);
  if (
    users === 0n ||
    BigInt(group.endpoint_count) < users ||
    BigInt(group.users.length) !== (users < 3n ? users : 3n) ||
    new Set(group.users.map((user) => user.user_id)).size !== group.users.length ||
    group.users.some(
      (user) =>
        BigInt(user.endpoint_count) === 0n ||
        BigInt(user.enabled_count) > BigInt(user.endpoint_count),
    ) ||
    group.users.reduce((sum, user) => sum + BigInt(user.endpoint_count), 0n) >
      BigInt(group.endpoint_count) ||
    group.users.reduce((sum, user) => sum + BigInt(user.key_count), 0n) > BigInt(group.key_count)
  )
    invalidResponse('endpoint overview counts');
  return group;
}

function normalizeEndpointUsersPageItem(value: unknown) {
  const root = record(
    value,
    ['user_id', 'endpoint_count', 'key_count', 'enabled_count'],
    'endpoint overview user',
  );
  const user = {
    user_id: decimalID(root.user_id, 'overview user id'),
    endpoint_count: decimal(root.endpoint_count, 'user endpoint count'),
    key_count: decimal(root.key_count, 'user key count'),
    enabled_count: decimal(root.enabled_count, 'user enabled count'),
  };
  if (
    BigInt(user.endpoint_count) === 0n ||
    BigInt(user.enabled_count) > BigInt(user.endpoint_count)
  ) {
    invalidResponse('endpoint overview user counts');
  }
  return user;
}

export type AdminEndpointUsersPage = AdminPage<EndpointOverview['users'][number]>;

function adminPagePath(
  path: string,
  values: Record<string, string | number | boolean | null | undefined>,
): string {
  return queryPath(path, values);
}

function invalidRequest(): never {
  throw new ApiError('invalid_request', 'Invalid administrator list request.', 400);
}

function validateWindow(page: string, size: PageSize): void {
  if (!isPageNumber(page) || !isPageSize(size)) invalidRequest();
}

function validateText(value: string, maxBytes: number, allowEmpty: boolean): void {
  if (
    typeof value !== 'string' ||
    (!allowEmpty && !value) ||
    value.length > maxBytes ||
    new TextEncoder().encode(value).byteLength > maxBytes
  )
    invalidRequest();
  for (const character of value) {
    const code = character.codePointAt(0)!;
    if (code < 0x20 || (code >= 0x7f && code <= 0x9f) || (code >= 0xd800 && code <= 0xdfff)) {
      invalidRequest();
    }
  }
}

export const adminPageKeys = {
  users: (account: string, banned: string, query: string, page: string, size: PageSize) =>
    ['admin', 'operations', 'users', account, banned, query, page, size] as const,
  user: (account: string, id: string) => ['admin', 'operations', 'user', account, id] as const,
  endpoints: (account: string, query: string, page: string, size: PageSize) =>
    ['admin', 'operations', 'endpoints', account, query, page, size] as const,
  endpointUsers: (account: string, baseURL: string, page: string, size: PageSize) =>
    ['admin', 'operations', 'endpoint-users', account, baseURL, page, size] as const,
  activitiesConfig: (account: string) =>
    ['admin', 'operations', 'activities', 'config', account] as const,
  thursday: (account: string) =>
    ['admin', 'operations', 'activities', 'thursday', account] as const,
  pools: (account: string, type: string, state: string, page: string, size: PageSize) =>
    ['admin', 'operations', 'pools', account, type, state, page, size] as const,
};

export async function getAdminUsersPage(
  banned: '' | 'true' | 'false',
  query: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<AdminPage<AdminUser>> {
  validateWindow(page, pageSize);
  validateText(query, 512, true);
  if (banned !== '' && banned !== 'true' && banned !== 'false') invalidRequest();
  const path = adminPagePath('/admin/api/users', {
    is_banned: banned || undefined,
    q: query || undefined,
    page,
    page_size: pageSize,
  });
  return decoded(
    path,
    (value) =>
      normalizePage(
        value,
        'administrator user page',
        normalizeAdminUser,
        page,
        pageSize,
        (user) => user.id,
      ),
    { signal },
  );
}

export async function getAdminUserDetail(id: string, signal?: AbortSignal): Promise<AdminUser> {
  if (!isPageNumber(id, 9_223_372_036_854_775_807n)) invalidRequest();
  return decoded(
    `/admin/api/users/${encodeURIComponent(decimalID(id, 'administrator user id'))}`,
    (value) => {
      const user = normalizeAdminUser(value);
      if (user.id !== id) invalidResponse('administrator user identity');
      return user;
    },
    { signal },
  );
}

export async function getAdminEndpointsPage(
  query: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<AdminPage<EndpointOverview>> {
  validateWindow(page, pageSize);
  validateText(query, 512, true);
  const path = adminPagePath('/admin/api/overview/endpoints', {
    q: query || undefined,
    page,
    page_size: pageSize,
  });
  return decoded(
    path,
    (value) =>
      normalizePage(
        value,
        'endpoint overview page',
        normalizeEndpointOverviewPageItem,
        page,
        pageSize,
        (group) => group.base_url,
      ),
    { signal },
  );
}

export async function getAdminEndpointUsersPage(
  baseURL: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<AdminEndpointUsersPage> {
  validateWindow(page, pageSize);
  validateText(baseURL, 4096, false);
  const path = adminPagePath('/admin/api/overview/endpoints/users', {
    base_url: baseURL,
    page,
    page_size: pageSize,
  });
  return decoded(
    path,
    (value) =>
      normalizePage(
        value,
        'endpoint overview users page',
        normalizeEndpointUsersPageItem,
        page,
        pageSize,
        (user) => user.user_id,
      ),
    { signal },
  );
}

export async function getAdminPoolsPage(
  type: string,
  state: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<AdminPage<Pool>> {
  validateWindow(page, pageSize);
  if (
    (type !== '' && type !== 'welfare' && type !== 'thursday') ||
    (state !== '' && state !== 'open' && state !== 'closed')
  )
    invalidRequest();
  const path = adminPagePath('/admin/api/pools', {
    pool_type: type || undefined,
    state: state || undefined,
    page,
    page_size: pageSize,
  });
  return decoded(
    path,
    (value) =>
      normalizePage(value, 'shared pool page', normalizePool, page, pageSize, (pool) => pool.id),
    { signal },
  );
}

export function getAdminActivitiesConfig(signal?: AbortSignal): Promise<ActivitiesConfig> {
  return decoded('/admin/api/activities/config', normalizeActivitiesConfig, { signal });
}

export function getAdminThursday(signal?: AbortSignal): Promise<{ period: Period | null }> {
  return decoded(
    '/admin/api/activities/thursday',
    (value) => {
      const root = record(value, ['period'], 'administrator Thursday state');
      return { period: root.period === null ? null : normalizePeriod(root.period) };
    },
    { signal },
  );
}
