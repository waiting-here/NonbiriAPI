import { useQuery, type QueryKey, type UseQueryResult } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { decoded, queryPath } from '@shared/operations/api';
import { array, invalidResponse, opaqueID, record, type CursorPage } from '@shared/operations/wire';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';
import {
  normalizeReportDonationMatch,
  normalizeReportDecision,
  normalizeReportMaterial,
  normalizeReportSummary,
  normalizeReportTarget,
  REPORT_STATUSES,
  type ReportCaseDetail,
  type ReportDonationMatch,
  type ReportMaterial,
  type ReportStatus,
  type ReportTarget,
} from './reports';

export interface NumberedReportPage<T> {
  data: T[];
  next_cursor: null;
  pagination: PageMetadata;
}

export interface NumberedReportDetail extends Omit<ReportCaseDetail, 'materials'> {
  materials: CursorPage<ReportMaterial>;
  materials_pagination: PageMetadata;
}

export interface ReportPageWindow {
  page: string;
  pageSize: PageSize;
}

function invalidRequest(label: string): never {
  throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
}

function requestID(value: unknown, prefix: 'rpc_' | 'rpt_'): string {
  try {
    return opaqueID(value, prefix, 'report identifier');
  } catch {
    return invalidRequest('report identifier');
  }
}

function requestStatus(status: string): ReportStatus | '' {
  if (
    typeof status !== 'string' ||
    (status !== '' && !REPORT_STATUSES.includes(status as ReportStatus))
  ) {
    invalidRequest('report status');
  }
  return status as ReportStatus | '';
}

function isReportStatus(status: string): boolean {
  return status === '' || REPORT_STATUSES.includes(status as ReportStatus);
}

function requestWindow(page: string, pageSize: PageSize): ReportPageWindow {
  if (!isPageNumber(page)) invalidRequest('report page');
  if (!isPageSize(pageSize)) invalidRequest('report page size');
  return { page, pageSize };
}

function normalizedPage<T>(
  value: unknown,
  label: string,
  window: ReportPageWindow,
  decode: (value: unknown) => T,
  identity: (value: T) => string,
): NumberedReportPage<T> {
  const root = record(value, ['data', 'next_cursor', 'pagination'], label);
  if (root.next_cursor !== null) invalidResponse(`${label} cursor`);
  const pagination = normalizePageMetadata(root.pagination);
  const data = array(root.data, `${label} data`, window.pageSize).map(decode);
  const identities = new Set(data.map(identity));
  if (identities.size !== data.length) invalidResponse(`${label} identities`);
  validatePageResponse(pagination, window.page, window.pageSize, data.length);
  return { data, next_cursor: null, pagination };
}

function normalizedMaterials(value: unknown, window: ReportPageWindow): CursorPage<ReportMaterial> {
  const root = record(value, ['data', 'next_cursor'], 'report materials');
  if (root.next_cursor !== null) invalidResponse('report materials cursor');
  const data = array(root.data, 'report materials data', window.pageSize).map(
    normalizeReportMaterial,
  );
  const identities = new Set(data.map((item) => item.id));
  if (identities.size !== data.length) invalidResponse('report materials identities');
  return { data, next_cursor: null };
}

function normalizedDetail(value: unknown, window: ReportPageWindow): NumberedReportDetail {
  const root = record(
    value,
    [
      'id',
      'status',
      'progress_state',
      'connector_type',
      'canonical_base_url',
      'material_version',
      'target_version',
      'deadline',
      'counts',
      'retry',
      'created_at',
      'terminal_at',
      'materials',
      'materials_pagination',
      'decision',
    ],
    'report case detail',
  );
  const summary = normalizeReportSummary(
    Object.fromEntries(
      Object.entries(root).filter(
        ([key]) => !['materials', 'materials_pagination', 'decision'].includes(key),
      ),
    ),
  );
  const materialsPagination = normalizePageMetadata(root.materials_pagination);
  const materials = normalizedMaterials(root.materials, window);
  validatePageResponse(materialsPagination, window.page, window.pageSize, materials.data.length);
  const decision = root.decision === null ? null : normalizeReportDecision(root.decision);
  return { ...summary, materials, materials_pagination: materialsPagination, decision };
}

function pagePath(path: string, window: ReportPageWindow): string {
  return queryPath(path, { page: window.page, page_size: window.pageSize });
}

export function normalizeReportPage(
  value: unknown,
  page: string,
  pageSize: PageSize,
): NumberedReportPage<ReturnType<typeof normalizeReportSummary>> {
  const window = requestWindow(page, pageSize);
  return normalizedPage(
    value,
    'report case page',
    window,
    normalizeReportSummary,
    (item) => item.id,
  );
}

export function normalizeReportDetailPage(
  value: unknown,
  materialsPage: string,
  materialsPageSize: PageSize,
): NumberedReportDetail {
  return normalizedDetail(value, requestWindow(materialsPage, materialsPageSize));
}

export function normalizeReportTargetsPage(
  value: unknown,
  page: string,
  pageSize: PageSize,
): NumberedReportPage<ReportTarget> {
  const window = requestWindow(page, pageSize);
  return normalizedPage(
    value,
    'report target page',
    window,
    normalizeReportTarget,
    (item) => item.id,
  );
}

export function normalizeReportTargetDonationsPage(
  value: unknown,
  page: string,
  pageSize: PageSize,
): NumberedReportPage<ReportDonationMatch> {
  const window = requestWindow(page, pageSize);
  return normalizedPage(
    value,
    'report donation lineage page',
    window,
    normalizeReportDonationMatch,
    (item) => item.donation_key_id,
  );
}

export function getReportPage(
  status: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<NumberedReportPage<ReturnType<typeof normalizeReportSummary>>> {
  const safeStatus = requestStatus(status);
  const window = requestWindow(page, pageSize);
  return decoded(
    queryPath('/admin/api/reports', {
      status: safeStatus || undefined,
      page: window.page,
      page_size: window.pageSize,
    }),
    (value) => {
      const result = normalizedPage(
        value,
        'report case page',
        window,
        normalizeReportSummary,
        (item) => item.id,
      );
      if (safeStatus && result.data.some((item) => item.status !== safeStatus))
        invalidResponse('report status filter');
      return result;
    },
    { signal },
  );
}

export function getReportDetailPage(
  id: string,
  materialsPage: string,
  materialsPageSize: PageSize,
  signal?: AbortSignal,
): Promise<NumberedReportDetail> {
  const window = requestWindow(materialsPage, materialsPageSize);
  const caseID = requestID(id, 'rpc_');
  return decoded(
    queryPath(`/admin/api/reports/${encodeURIComponent(caseID)}`, {
      materials_page: window.page,
      materials_page_size: window.pageSize,
    }),
    (value) => {
      const result = normalizedDetail(value, window);
      if (result.id !== caseID) invalidResponse('report detail identity');
      return result;
    },
    { signal },
  );
}

export function getReportTargetsPage(
  id: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<NumberedReportPage<ReportTarget>> {
  const window = requestWindow(page, pageSize);
  const caseID = requestID(id, 'rpc_');
  return decoded(
    pagePath(`/admin/api/reports/${encodeURIComponent(caseID)}/targets`, window),
    (value) =>
      normalizedPage(value, 'report target page', window, normalizeReportTarget, (item) => item.id),
    { signal },
  );
}

export function getReportTargetDonationsPage(
  id: string,
  targetId: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<NumberedReportPage<ReportDonationMatch>> {
  const window = requestWindow(page, pageSize);
  const caseID = requestID(id, 'rpc_');
  const target = requestID(targetId, 'rpt_');
  return decoded(
    pagePath(
      `/admin/api/reports/${encodeURIComponent(caseID)}/targets/${encodeURIComponent(target)}/donations`,
      window,
    ),
    (value) =>
      normalizedPage(
        value,
        'report donation lineage page',
        window,
        normalizeReportDonationMatch,
        (item) => item.donation_key_id,
      ),
    { signal },
  );
}

export const reportPageKeys = {
  root: ['admin', 'operations', 'reports'] as const,
  badge: (accountId: string) => [...reportPageKeys.root, 'badge', accountId] as const,
  list: (accountId: string, status: string, page: string, pageSize: PageSize) =>
    [...reportPageKeys.root, 'page', accountId, status, page, pageSize] as const,
  detail: (accountId: string, id: string, page: string, pageSize: PageSize) =>
    [...reportPageKeys.root, 'detail-page', accountId, id, page, pageSize] as const,
  targets: (accountId: string, id: string, page: string, pageSize: PageSize) =>
    [...reportPageKeys.root, 'targets-page', accountId, id, page, pageSize] as const,
  lineage: (accountId: string, id: string, targetId: string, page: string, pageSize: PageSize) =>
    [...reportPageKeys.root, 'lineage-page', accountId, id, targetId, page, pageSize] as const,
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

export function useReportsPage(
  accountId: string,
  status: string,
  page: string,
  pageSize: PageSize,
  enabled = true,
): UseQueryResult<NumberedReportPage<ReturnType<typeof normalizeReportSummary>>, Error> {
  const prefix = [...reportPageKeys.root, 'page', accountId, status] as const;
  return useQuery<NumberedReportPage<ReturnType<typeof normalizeReportSummary>>, Error>({
    queryKey: reportPageKeys.list(accountId, status, page, pageSize),
    queryFn: ({ signal }) => getReportPage(status, page, pageSize, signal),
    enabled:
      enabled &&
      Boolean(accountId) &&
      isReportStatus(status) &&
      isPageNumber(page) &&
      isPageSize(pageSize),
    placeholderData:
      sameScopePlaceholder<NumberedReportPage<ReturnType<typeof normalizeReportSummary>>>(prefix),
    retry: false,
  });
}

export function useReportDetailPage(
  accountId: string,
  id: string,
  page: string,
  pageSize: PageSize,
  enabled = true,
): UseQueryResult<NumberedReportDetail, Error> {
  const prefix = [...reportPageKeys.root, 'detail-page', accountId, id] as const;
  return useQuery<NumberedReportDetail, Error>({
    queryKey: reportPageKeys.detail(accountId, id, page, pageSize),
    queryFn: ({ signal }) => getReportDetailPage(id, page, pageSize, signal),
    enabled: enabled && Boolean(accountId && id) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: sameScopePlaceholder<NumberedReportDetail>(prefix),
    retry: false,
  });
}

export function useReportTargetsPage(
  accountId: string,
  id: string,
  page: string,
  pageSize: PageSize,
  enabled = true,
): UseQueryResult<NumberedReportPage<ReportTarget>, Error> {
  const prefix = [...reportPageKeys.root, 'targets-page', accountId, id] as const;
  return useQuery<NumberedReportPage<ReportTarget>, Error>({
    queryKey: reportPageKeys.targets(accountId, id, page, pageSize),
    queryFn: ({ signal }) => getReportTargetsPage(id, page, pageSize, signal),
    enabled: enabled && Boolean(accountId && id) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: sameScopePlaceholder<NumberedReportPage<ReportTarget>>(prefix),
    retry: false,
  });
}

export function useReportTargetDonationsPage(
  accountId: string,
  id: string,
  targetId: string,
  page: string,
  pageSize: PageSize,
  enabled = true,
): UseQueryResult<NumberedReportPage<ReportDonationMatch>, Error> {
  const prefix = [...reportPageKeys.root, 'lineage-page', accountId, id, targetId] as const;
  return useQuery<NumberedReportPage<ReportDonationMatch>, Error>({
    queryKey: reportPageKeys.lineage(accountId, id, targetId, page, pageSize),
    queryFn: ({ signal }) => getReportTargetDonationsPage(id, targetId, page, pageSize, signal),
    enabled:
      enabled && Boolean(accountId && id && targetId) && isPageNumber(page) && isPageSize(pageSize),
    placeholderData: sameScopePlaceholder<NumberedReportPage<ReportDonationMatch>>(prefix),
    retry: false,
  });
}

export const getReportsPage = getReportPage;
export const numberedReportKeys = reportPageKeys;
export const useReportListPage = useReportsPage;
export const useReportLineagePage = useReportTargetDonationsPage;
