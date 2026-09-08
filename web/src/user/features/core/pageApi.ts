import {
  isPageNumber,
  normalizePageMetadata,
  PAGE_SIZES,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';
import { ApiError } from '@shared/query/http';
import {
  normalizeBindingCandidatePage,
  normalizeCatalogView,
  normalizeEndpointKeyPage,
  normalizeEndpointPage,
  normalizeModelPage,
  validateResourceId,
  validateScalarInput,
} from './normalizers';
import { coreRequest } from './request';
import type { NumberedCatalogView, NumberedPage, PageWindow } from './pageTypes';
import type {
  BindingCandidate,
  CandidateFilters,
  CatalogSourceType,
  Endpoint,
  EndpointKey,
  Model,
  Page,
} from './types';

export type { NumberedCatalogView, NumberedPage, PageWindow } from './pageTypes';

type UnknownRecord = Record<string, unknown>;
type NumberedCandidateFilters = Omit<CandidateFilters, 'cursor' | 'limit'>;

function invalidRequest(label: string): never {
  throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
}

function invalidResponse(label: string): never {
  throw new ApiError('invalid_response', `The server returned an invalid ${label}.`, 200);
}

function exactRecord(
  value: unknown,
  allowed: readonly string[],
  label: string,
  required: readonly string[] = allowed,
): UnknownRecord {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalidRequest(label);
  const record = value as UnknownRecord;
  const allowedKeys = new Set(allowed);
  if (
    Object.keys(record).some((key) => !allowedKeys.has(key)) ||
    required.some((key) => !Object.hasOwn(record, key))
  ) {
    invalidRequest(label);
  }
  return record;
}

function exactResponseRecord(
  value: unknown,
  allowed: readonly string[],
  label: string,
): UnknownRecord {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalidResponse(label);
  const record = value as UnknownRecord;
  const allowedKeys = new Set(allowed);
  if (
    Object.keys(record).some((key) => !allowedKeys.has(key)) ||
    allowed.some((key) => !Object.hasOwn(record, key))
  ) {
    invalidResponse(label);
  }
  return record;
}

function pageWindowInput(value: unknown): PageWindow {
  const record = exactRecord(value, ['page', 'pageSize'], 'page window');
  if (typeof record.page !== 'string' || !isPageNumber(record.page)) {
    invalidRequest('page');
  }
  if (typeof record.pageSize !== 'number' || !PAGE_SIZES.includes(record.pageSize as PageSize)) {
    invalidRequest('page size');
  }
  return { page: record.page, pageSize: record.pageSize as PageSize };
}

function catalogSourceInput(value: unknown): CatalogSourceType | undefined {
  if (value === undefined) return undefined;
  if (value !== 'automatic' && value !== 'manual') invalidRequest('catalog source');
  return value;
}

function resourcePath(value: string, label: string): string {
  const normalized = resourceID(value, label);
  return encodeURIComponent(normalized);
}

function resourceID(value: unknown, label: string): string {
  if (typeof value !== 'string') invalidRequest(label);
  return validateResourceId(value, label);
}

function pageQuery(window: PageWindow): string {
  return new URLSearchParams({ page: window.page, page_size: String(window.pageSize) }).toString();
}

function resourceSearchQuery(window: PageWindow, search: unknown): string {
  const query = validateScalarInput(search, 128, 'resource search', true);
  const params = new URLSearchParams(pageQuery(window));
  if (query !== '') params.set('q', query);
  return params.toString();
}

function pageQueryWithFilters(window: PageWindow, filters: NumberedCandidateFilters): string {
  const params = new URLSearchParams();
  if (filters.endpointId !== undefined) params.set('endpoint_id', filters.endpointId);
  if (filters.keyId !== undefined) params.set('key_id', filters.keyId);
  if (filters.source !== undefined) params.set('source', filters.source);
  if (filters.query !== undefined && filters.query !== '') params.set('q', filters.query);
  params.set('page', window.page);
  params.set('page_size', String(window.pageSize));
  return params.toString();
}

function normalizeCandidateFilters(value: unknown): NumberedCandidateFilters {
  const record = exactRecord(
    value,
    ['endpointId', 'keyId', 'source', 'query'],
    'candidate filters',
    [],
  );
  const result: NumberedCandidateFilters = {};
  if (record.endpointId !== undefined)
    result.endpointId = resourceID(record.endpointId, 'endpoint id');
  if (record.keyId !== undefined) result.keyId = resourceID(record.keyId, 'endpoint key id');
  if (record.source !== undefined) result.source = catalogSourceInput(record.source);
  if (record.query !== undefined) {
    result.query = validateScalarInput(record.query, 512, 'candidate query', true);
  }
  return result;
}

function expectedRows(metadata: PageMetadata): bigint {
  const total = BigInt(metadata.total_items);
  const page = BigInt(metadata.page);
  const pageSize = BigInt(metadata.page_size);
  const offset = (page - 1n) * pageSize;
  const remaining = total > offset ? total - offset : 0n;
  return remaining < pageSize ? remaining : pageSize;
}

function verifyPagination(
  metadata: PageMetadata,
  window: PageWindow,
  dataLength: number,
  label: string,
): void {
  const requestedPage = BigInt(window.page);
  const totalPages = BigInt(metadata.total_pages);
  const expectedPage = requestedPage > totalPages ? totalPages : requestedPage;
  if (metadata.page !== expectedPage.toString()) invalidResponse(`${label} page clamp`);
  if (metadata.page_size !== window.pageSize) invalidResponse(`${label} page size`);
  if (BigInt(dataLength) !== expectedRows(metadata)) invalidResponse(`${label} row count`);
}

function numberedPage<T>(
  value: unknown,
  window: PageWindow,
  label: string,
  normalize: (value: unknown) => Page<T>,
): NumberedPage<T> {
  const record = exactResponseRecord(value, ['data', 'next_cursor', 'pagination'], `${label} page`);
  if (record.next_cursor !== null) invalidResponse(`${label} cursor`);
  const pagination = normalizePageMetadata(record.pagination);
  const page = normalize({ data: record.data, next_cursor: null });
  verifyPagination(pagination, window, page.data.length, label);
  return { data: page.data, next_cursor: null, pagination };
}

function numberedCatalog(
  value: unknown,
  window: PageWindow,
  source: CatalogSourceType | undefined,
): NumberedCatalogView {
  const record = exactResponseRecord(
    value,
    ['evidence', 'automatic_entries', 'manual_entries', 'next_cursor', 'pagination'],
    'catalog page',
  );
  if (record.next_cursor !== null) invalidResponse('catalog cursor');
  const pagination = normalizePageMetadata(record.pagination);
  const catalog = normalizeCatalogView({
    evidence: record.evidence,
    automatic_entries: record.automatic_entries,
    manual_entries: record.manual_entries,
    next_cursor: null,
  });
  if (source === 'automatic' && catalog.manual_entries.length !== 0) {
    invalidResponse('catalog source projection');
  }
  if (source === 'manual' && catalog.automatic_entries.length !== 0) {
    invalidResponse('catalog source projection');
  }
  verifyPagination(
    pagination,
    window,
    catalog.automatic_entries.length + catalog.manual_entries.length,
    'catalog',
  );
  return { ...catalog, next_cursor: null, pagination };
}

export async function listEndpointsPage(
  window: PageWindow,
  signal?: AbortSignal,
  search = '',
): Promise<NumberedPage<Endpoint>> {
  const normalizedWindow = pageWindowInput(window);
  const response = await coreRequest(
    `/api/endpoints?${resourceSearchQuery(normalizedWindow, search)}`,
    { signal },
  );
  if (response.status !== 200) invalidResponse('endpoint list status');
  return numberedPage(response.payload, normalizedWindow, 'endpoint list', normalizeEndpointPage);
}

export async function listEndpointKeysPage(
  endpointId: string,
  window: PageWindow,
  signal?: AbortSignal,
  search = '',
): Promise<NumberedPage<EndpointKey>> {
  const normalizedWindow = pageWindowInput(window);
  const normalizedEndpointID = resourceID(endpointId, 'endpoint id');
  const response = await coreRequest(
    `/api/endpoints/${encodeURIComponent(normalizedEndpointID)}/keys?${resourceSearchQuery(normalizedWindow, search)}`,
    { signal },
  );
  if (response.status !== 200) invalidResponse('endpoint key list status');
  const page = numberedPage(
    response.payload,
    normalizedWindow,
    'endpoint key list',
    normalizeEndpointKeyPage,
  );
  if (page.data.some((key) => key.endpoint_id !== normalizedEndpointID)) {
    invalidResponse('endpoint key parent');
  }
  return page;
}

export async function listModelsPage(
  window: PageWindow,
  signal?: AbortSignal,
): Promise<NumberedPage<Model>> {
  const normalizedWindow = pageWindowInput(window);
  const response = await coreRequest(`/api/models?${pageQuery(normalizedWindow)}`, { signal });
  if (response.status !== 200) invalidResponse('logical model list status');
  return numberedPage(response.payload, normalizedWindow, 'logical model list', normalizeModelPage);
}

export async function getCatalogPage(
  endpointId: string,
  keyId: string,
  window: PageWindow,
  source?: CatalogSourceType,
  signal?: AbortSignal,
): Promise<NumberedCatalogView> {
  const normalizedWindow = pageWindowInput(window);
  const normalizedSource = catalogSourceInput(source);
  const params = new URLSearchParams({
    page: normalizedWindow.page,
    page_size: String(normalizedWindow.pageSize),
  });
  if (normalizedSource !== undefined) params.set('source', normalizedSource);
  const response = await coreRequest(
    `/api/endpoints/${resourcePath(endpointId, 'endpoint id')}/keys/${resourcePath(keyId, 'endpoint key id')}/models?${params.toString()}`,
    { signal },
  );
  if (response.status !== 200) invalidResponse('catalog status');
  return numberedCatalog(response.payload, normalizedWindow, normalizedSource);
}

export async function getBindingCandidatesPage(
  modelId: string,
  filters: Omit<CandidateFilters, 'cursor' | 'limit'>,
  window: PageWindow,
  signal?: AbortSignal,
): Promise<NumberedPage<BindingCandidate>> {
  const normalizedWindow = pageWindowInput(window);
  const normalizedFilters = normalizeCandidateFilters(filters);
  const response = await coreRequest(
    `/api/models/${resourcePath(modelId, 'model id')}/binding-candidates?${pageQueryWithFilters(normalizedWindow, normalizedFilters)}`,
    { signal },
  );
  if (response.status !== 200) invalidResponse('binding candidate status');
  const page = numberedPage(
    response.payload,
    normalizedWindow,
    'binding candidate list',
    normalizeBindingCandidatePage,
  );
  if (
    normalizedFilters.keyId !== undefined &&
    page.data.some((candidate) => candidate.endpoint_key_id !== normalizedFilters.keyId)
  ) {
    invalidResponse('binding candidate key parent');
  }
  return page;
}
