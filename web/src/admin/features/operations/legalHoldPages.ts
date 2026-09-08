import { CancelledError, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
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
import {
  getLegalHold,
  normalizeLegalHoldSummary,
  type HeldObjectKind,
  type LegalHoldSummary,
} from './core';

export type LegalHoldStateFilter = '' | 'active' | 'released' | 'expired';
export type LegalHoldKindFilter = '' | HeldObjectKind;

export interface LegalHoldPageResult {
  data: LegalHoldSummary[];
  next_cursor: null;
  pagination: PageMetadata;
}

const STATES: readonly LegalHoldStateFilter[] = ['', 'active', 'released', 'expired'];
const KINDS: readonly LegalHoldKindFilter[] = [
  '',
  'maintenance_event',
  'report_case',
  'announcement_audit',
  'donation',
  'request_log',
];

function invalidRequest(field: string): never {
  throw new ApiError('invalid_request', `Invalid ${field}.`, 400);
}

function validFilter<T extends string>(value: T, values: readonly T[], field: string): T {
  if (!values.includes(value)) invalidRequest(field);
  return value;
}

export function normalizeLegalHoldPageResponse(
  value: unknown,
  state: LegalHoldStateFilter,
  kind: LegalHoldKindFilter,
  requestedPage: string,
  requestedSize: PageSize,
): LegalHoldPageResult {
  const normalizedState = validFilter(state, STATES, 'legal hold state filter');
  const normalizedKind = validFilter(kind, KINDS, 'legal hold object-kind filter');
  if (!isPageNumber(requestedPage)) invalidRequest('legal hold page');
  if (!isPageSize(requestedSize)) invalidRequest('legal hold page size');

  const root = record(value, ['data', 'next_cursor', 'pagination'], 'legal hold page');
  if (root.next_cursor !== null) invalidResponse('legal hold page cursor');
  const pagination = normalizePageMetadata(root.pagination);
  const data = array(root.data, 'legal hold page data', 100).map(normalizeLegalHoldSummary);
  validatePageResponse(pagination, requestedPage, requestedSize, data.length);

  const ids = new Set<string>();
  for (const hold of data) {
    if (ids.has(hold.id)) invalidResponse('legal hold identities');
    ids.add(hold.id);
    if (normalizedState !== '' && hold.state !== normalizedState) {
      invalidResponse('legal hold state filter');
    }
    if (normalizedKind !== '' && hold.object_kind !== normalizedKind) {
      invalidResponse('legal hold object-kind filter');
    }
  }

  return { data, next_cursor: null, pagination };
}

export const legalHoldPageKeys = {
  root: ['admin', 'operations', 'legal-holds'] as const,
  page: (
    accountID: string,
    state: LegalHoldStateFilter,
    kind: LegalHoldKindFilter,
    page: string,
    pageSize: PageSize,
  ) => ['admin', 'operations', 'legal-holds', accountID, state, kind, page, pageSize] as const,
  detail: (accountID: string, id: string) =>
    ['admin', 'operations', 'legal-hold', accountID, id] as const,
};

export async function getLegalHoldPage(
  state: LegalHoldStateFilter,
  kind: LegalHoldKindFilter,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<LegalHoldPageResult> {
  const normalizedState = validFilter(state, STATES, 'legal hold state filter');
  const normalizedKind = validFilter(kind, KINDS, 'legal hold object-kind filter');
  if (!isPageNumber(page)) invalidRequest('legal hold page');
  if (!isPageSize(pageSize)) invalidRequest('legal hold page size');
  return decoded(
    queryPath('/admin/api/legal-holds', {
      state: normalizedState || undefined,
      object_kind: normalizedKind || undefined,
      page,
      page_size: pageSize,
    }),
    (value) =>
      normalizeLegalHoldPageResponse(value, normalizedState, normalizedKind, page, pageSize),
    { signal },
  );
}

export function useLegalHoldPage(
  accountID: string | undefined,
  state: LegalHoldStateFilter,
  kind: LegalHoldKindFilter,
  page: string,
  pageSize: PageSize,
  enabled = true,
) {
  const client = useQueryClient();
  const queryKey = legalHoldPageKeys.page(accountID ?? 'none', state, kind, page, pageSize);
  return useQuery({
    queryKey,
    queryFn: ({ signal }) => {
      requireCurrentAccount(client, accountID);
      return getLegalHoldPage(state, kind, page, pageSize, signal);
    },
    enabled: enabled && Boolean(accountID) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: (previous, previousQuery) =>
      previousQuery !== undefined &&
      previousQuery.queryKey[3] === accountID &&
      previousQuery.queryKey[4] === state &&
      previousQuery.queryKey[5] === kind
        ? previous
        : undefined,
    retry: false,
  });
}

export function isLegalHoldID(value: string): boolean {
  return /^lgh_[A-Za-z0-9_-]{22}$/.test(value) && /[AQgw]$/.test(value);
}

function requireCurrentAccount(client: QueryClient, accountID: string | undefined): void {
  const session = client.getQueryData<{ admin: { username: string } } | null>(['admin', 'session']);
  if (!session?.admin?.username || `admin:${session.admin.username}` !== accountID) throw new CancelledError();
}

export function useLegalHoldDetail(accountID: string | undefined, id: string, enabled = true) {
  const client = useQueryClient();
  return useQuery({
    queryKey: legalHoldPageKeys.detail(accountID ?? 'none', id),
    queryFn: async ({ signal }) => {
      requireCurrentAccount(client, accountID);
      const detail = await getLegalHold(id, signal);
      if (detail.id !== id) invalidResponse('legal hold identity');
      return detail;
    },
    enabled: enabled && Boolean(accountID) && isLegalHoldID(id),
    retry: false,
  });
}
