import { useQuery } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { decoded, queryPath } from '@shared/operations/api';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';
import { array, invalidResponse, record } from '@shared/operations/wire';
import { normalizeAdminAlert, type AdminAlert } from './core';

export type AdminAlertResolvedFilter = 'all' | 'true' | 'false';
export type AlertResolvedFilter = AdminAlertResolvedFilter;

export interface AdminAlertPageResult {
  data: AdminAlert[];
  next_cursor: null;
  pagination: PageMetadata;
}

function invalidRequest(field: string): never {
  throw new ApiError('invalid_request', `Invalid ${field}.`, 400);
}

function normalizedFilter(value: AdminAlertResolvedFilter): AdminAlertResolvedFilter {
  if (value !== 'all' && value !== 'true' && value !== 'false') {
    invalidRequest('alert resolution filter');
  }
  return value;
}

export function normalizeAdminAlertPageResponse(
  value: unknown,
  resolved: AdminAlertResolvedFilter,
  requestedPage: string,
  requestedSize: PageSize,
): AdminAlertPageResult {
  const root = record(
    value,
    ['data', 'next_cursor', 'pagination'],
    'administrator alert page',
  );
  if (root.next_cursor !== null) invalidResponse('administrator alert page cursor');

  const pagination = normalizePageMetadata(root.pagination);
  const data = array(root.data, 'administrator alert page data', 100).map(normalizeAdminAlert);
  validatePageResponse(pagination, requestedPage, requestedSize, data.length);

  const ids = new Set<string>();
  for (const alert of data) {
    if (ids.has(alert.id)) invalidResponse('administrator alert identities');
    ids.add(alert.id);
    if (resolved !== 'all' && alert.resolved !== (resolved === 'true')) {
      invalidResponse('administrator alert resolution filter');
    }
  }

  return { data, next_cursor: null, pagination };
}

export const adminAlertPageKeys = {
  root: ['admin', 'operations', 'alerts'] as const,
  page: (
    accountID: string,
    resolved: AdminAlertResolvedFilter,
    page: string,
    pageSize: PageSize,
  ) => ['admin', 'operations', 'alerts', accountID, resolved, page, pageSize] as const,
};

export async function getAdminAlertPage(
  resolved: AdminAlertResolvedFilter,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<AdminAlertPageResult> {
  const normalizedResolved = normalizedFilter(resolved);
  if (!isPageNumber(page)) invalidRequest('alert page');
  if (!isPageSize(pageSize)) invalidRequest('alert page size');
  return decoded(
    queryPath('/admin/api/alerts', {
      resolved: normalizedResolved === 'all' ? undefined : normalizedResolved,
      page,
      page_size: pageSize,
    }),
    (value) => normalizeAdminAlertPageResponse(value, normalizedResolved, page, pageSize),
    { signal },
  );
}

export function useAdminAlertPage(
  accountID: string | undefined,
  resolved: AdminAlertResolvedFilter,
  page: string,
  pageSize: PageSize,
  enabled = true,
) {
  const queryKey = adminAlertPageKeys.page(accountID ?? 'none', resolved, page, pageSize);
  return useQuery({
    queryKey,
    queryFn: ({ signal }) => getAdminAlertPage(resolved, page, pageSize, signal),
    enabled: enabled && Boolean(accountID) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountID ? previous : undefined,
    retry: false,
  });
}
