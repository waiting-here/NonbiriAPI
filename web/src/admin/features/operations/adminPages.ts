import { record } from '@shared/operations/wire';
import {
  getEndpointsPage,
  getEndpointUsersPage,
  setEndpointTags,
  type EndpointUsersPage,
} from '@shared/operations/endpoints';
import type { ManagementRole } from '@shared/operations/managedUsers';
export type AdminEndpointUsersPage = EndpointUsersPage;
import {
  normalizeNumberedPage as normalizePage,
  validateWindow,
  invalidRequest,
  type NumberedPage,
} from '@shared/operations/numberedPage';
import {
  getManagedUsersPage,
  getManagedUserDetail,
  managedUserKeys,
} from '@shared/operations/managedUsers';
import { decoded, queryPath } from '@shared/operations/api';
import type { PageSize } from '@shared/operations/pageNumbers';
import { type AdminUser } from './core';
import {
  normalizeActivitiesConfig,
  normalizePeriod,
  normalizePool,
  type ActivitiesConfig,
  type Period,
  type Pool,
} from './economy';

export type AdminPage<T> = NumberedPage<T>;

export function normalizeAdminPageResponse<T>(
  value: unknown,
  label: string,
  item: (value: unknown) => T,
  requestedPage: string,
  requestedSize: PageSize,
): AdminPage<T> {
  return normalizePage(value, label, item, requestedPage, requestedSize);
}

function adminPagePath(
  path: string,
  values: Record<string, string | number | boolean | null | undefined>,
): string {
  return queryPath(path, values);
}

export const adminPageKeys = {
  users: (
    account: string,
    banned: string,
    query: string,
    page: string,
    size: PageSize,
    level = '',
  ) => managedUserKeys.list('admin', account, banned, query, level, '', page, size),
  user: (account: string, id: string) => ['admin', 'operations', 'user', account, id] as const,
  endpoints: (
    account: string,
    query: string,
    page: string,
    size: PageSize,
    tag = '',
    userId = '',
  ) => ['admin', 'operations', 'endpoints', account, query, page, size, tag, userId] as const,
  endpointUsers: (account: string, baseURL: string, page: string, size: PageSize) =>
    ['admin', 'operations', 'endpoint-users', account, baseURL, page, size] as const,
  activitiesConfig: (account: string) =>
    ['admin', 'operations', 'activities', 'config', account] as const,
  thursday: (account: string) =>
    ['admin', 'operations', 'activities', 'thursday', account] as const,
  pools: (account: string, type: string, state: string, page: string, size: PageSize) =>
    ['admin', 'operations', 'pools', account, type, state, page, size] as const,
};

export function getAdminUsersPage(
  banned: '' | 'true' | 'false',
  query: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
  level = '',
): Promise<AdminPage<AdminUser>> {
  return getManagedUsersPage('admin', banned, query, level, page, pageSize, signal);
}

export function getAdminUserDetail(id: string, signal?: AbortSignal): Promise<AdminUser> {
  return getManagedUserDetail('admin', id, signal);
}

export const getAdminEndpointsPage = (
  ...args: Parameters<typeof getEndpointsPage> extends [ManagementRole, ...infer Rest]
    ? Rest
    : never
) => getEndpointsPage('admin', ...args);
export const getAdminEndpointUsersPage = (
  ...args: Parameters<typeof getEndpointUsersPage> extends [ManagementRole, ...infer Rest]
    ? Rest
    : never
) => getEndpointUsersPage('admin', ...args);

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

export const setAdminEndpointTags = (baseURLs: string[], tag: string, add: boolean) =>
  setEndpointTags('admin', baseURLs, tag, add);
