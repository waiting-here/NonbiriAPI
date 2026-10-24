import { apiFetch } from '@shared/query/http';
import { decoded, queryPath, idempotentOptions, operationKey } from './api';
import {
  normalizeNumberedPage as normalizePage,
  validateWindow,
  validateText,
  type NumberedPage,
} from './numberedPage';
import { array, decimal, decimalID, invalidResponse, record, string } from './wire';
import type { ManagementRole } from './managedUsers';
import type { PageSize } from './pageNumbers';

export const ENDPOINT_TAGS = [
  'abusive_third_party',
  'community_charity',
  'noncommercial_gateway',
  'user_gateway',
  'steward_gateway',
  'invalid_entry',
] as const;
export const endpointKeys = {
  list: (
    role: ManagementRole,
    account: string,
    query: string,
    page: string,
    size: PageSize,
    tag = '',
    userId = '',
  ) =>
    [
      role === 'admin' ? 'admin' : 'user',
      'operations',
      'endpoints',
      account,
      query,
      page,
      size,
      tag,
      userId,
    ] as const,
  users: (role: ManagementRole, account: string, baseURL: string, page: string, size: PageSize) =>
    [
      role === 'admin' ? 'admin' : 'user',
      'operations',
      'endpoint-users',
      account,
      baseURL,
      page,
      size,
    ] as const,
};
const endpointPath = (role: ManagementRole) =>
  (role === 'admin' ? '/admin/api' : '/api/steward') + '/overview/endpoints';

export interface EndpointOverviewUser {
  user_id: string;
  endpoint_count: string;
  key_count: string;
  enabled_count: string;
}

export interface EndpointOverview {
  tags: string[];
  base_url: string;
  user_count: string;
  endpoint_count: string;
  key_count: string;
  users: EndpointOverviewUser[];
}

export function normalizeEndpointOverview(value: unknown): EndpointOverview {
  const root = record(
    value,
    ['base_url', 'user_count', 'endpoint_count', 'key_count', 'users', 'tags'],
    'endpoint overview',
    ['base_url', 'user_count', 'endpoint_count', 'key_count', 'users'],
  );
  const users = array(root.users, 'endpoint overview users', 100).map((entry) => {
    const item = record(
      entry,
      ['user_id', 'endpoint_count', 'key_count', 'enabled_count'],
      'endpoint overview user',
    );
    return {
      user_id: decimalID(item.user_id, 'overview user id'),
      endpoint_count: decimal(item.endpoint_count, 'user endpoint count'),
      key_count: decimal(item.key_count, 'user key count'),
      enabled_count: decimal(item.enabled_count, 'user enabled count'),
    };
  });
  return {
    base_url: string(root.base_url, 'canonical endpoint URL', { min: 1 }),
    tags: array(root.tags ?? [], 'endpoint tags').map((tag) => string(tag, 'endpoint tag')),
    user_count: decimal(root.user_count, 'endpoint user count'),
    endpoint_count: decimal(root.endpoint_count, 'endpoint count'),
    key_count: decimal(root.key_count, 'endpoint key count'),
    users,
  };
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

export type EndpointUsersPage = NumberedPage<EndpointOverview['users'][number]>;

export async function getEndpointsPage(
  role: ManagementRole,
  query: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
  tag = '',
  userId = '',
): Promise<NumberedPage<EndpointOverview>> {
  validateWindow(page, pageSize);
  validateText(query, 512, true);
  const path = queryPath(endpointPath(role), {
    q: query || undefined,
    tag: tag || undefined,
    user_id: userId || undefined,
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

export async function getEndpointUsersPage(
  role: ManagementRole,
  baseURL: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<EndpointUsersPage> {
  validateWindow(page, pageSize);
  validateText(baseURL, 4096, false);
  const path = queryPath(endpointPath(role) + '/users', {
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

export async function setEndpointTags(
  role: ManagementRole,
  baseURLs: string[],
  tag: string,
  add: boolean,
): Promise<void> {
  await apiFetch(
    endpointPath(role) + '/tags',
    idempotentOptions(operationKey(), {
      method: 'PATCH',
      json: { base_urls: baseURLs, tag, add },
    }),
  );
}
