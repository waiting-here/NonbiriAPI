import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
} from '@shared/operations/pageNumbers';
import { ApiError } from '@shared/query/http';
import { coreRequest } from './request';
import { coreKeys } from './queries';
import { normalizeKeyBindingView, validateManualValue, validateResourceId } from './normalizers';
import type { NumberedPage, PageWindow } from './pageTypes';
import type { KeyBindingView } from './types';

type UnknownRecord = Record<string, unknown>;

function invalidRequest(label: string): never {
  throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
}

function invalidResponse(label: string): never {
  throw new ApiError('invalid_response', `The server returned an invalid ${label}.`, 200);
}

function exactRecord(value: unknown, allowed: readonly string[], label: string): UnknownRecord {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    invalidRequest(label);
  }
  const record = value as UnknownRecord;
  const allowedKeys = new Set(allowed);
  if (
    Object.keys(record).some((key) => !allowedKeys.has(key)) ||
    allowed.some((key) => !Object.hasOwn(record, key))
  ) {
    invalidRequest(label);
  }
  return record;
}

function pageWindowInput(value: unknown): PageWindow {
  const record = exactRecord(value, ['page', 'pageSize'], 'page window');
  if (!isPageNumber(record.page)) invalidRequest('page');
  if (!isPageSize(record.pageSize)) invalidRequest('page size');
  return { page: record.page, pageSize: record.pageSize };
}

function resourceID(value: unknown, label: string): string {
  if (typeof value !== 'string') invalidRequest(label);
  return validateResourceId(value, label);
}

function resourcePath(value: string, label: string): string {
  return encodeURIComponent(resourceID(value, label));
}

function modelFilterInput(value: unknown): string | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== 'string') invalidRequest('upstream model id');
  try {
    return validateManualValue(value, 512, false);
  } catch {
    invalidRequest('upstream model id');
  }
}

function pageQuery(window: PageWindow, upstreamModel: string | undefined): string {
  const params = new URLSearchParams({
    page: window.page,
    page_size: String(window.pageSize),
  });
  if (upstreamModel !== undefined) params.set('upstream_model_id', upstreamModel);
  return params.toString();
}

function uniqueBindingRows(rows: readonly KeyBindingView[]): void {
  const ids = new Set<string>();
  const pairs = new Set<string>();
  for (const row of rows) {
    if (ids.has(row.id)) invalidResponse('key binding ids');
    ids.add(row.id);
    const pair = `${row.model_id}\u0000${row.endpoint_key_id}\u0000${row.upstream_model_id}`;
    if (pairs.has(pair)) invalidResponse('key binding pairs');
    pairs.add(pair);
  }
}

function numberedKeyBindingPage(
  value: unknown,
  window: PageWindow,
  endpointID: string,
  keyID: string,
  upstreamModel: string | undefined,
): NumberedPage<KeyBindingView> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    invalidResponse('key binding page');
  }
  const record = value as UnknownRecord;
  const allowed = ['data', 'next_cursor', 'pagination'] as const;
  if (
    Object.keys(record).some((key) => !allowed.includes(key as (typeof allowed)[number])) ||
    allowed.some((key) => !Object.hasOwn(record, key))
  ) {
    invalidResponse('key binding page');
  }
  if (record.next_cursor !== null) invalidResponse('key binding cursor');
  const pagination = normalizePageMetadata(record.pagination);
  if (!Array.isArray(record.data) || record.data.length > 100) {
    invalidResponse('key binding data');
  }
  const data = record.data.map((entry) => normalizeKeyBindingView(entry));
  uniqueBindingRows(data);
  if (
    data.some(
      (row) =>
        row.endpoint_id !== endpointID ||
        row.endpoint_key_id !== keyID ||
        (upstreamModel !== undefined && row.upstream_model_id !== upstreamModel),
    )
  ) {
    invalidResponse('key binding parent or filter');
  }
  validatePageResponse(pagination, window.page, window.pageSize, data.length);
  return { data, next_cursor: null, pagination };
}

export async function listKeyBindingsPage(
  endpointId: string,
  keyId: string,
  window: PageWindow,
  upstreamModel?: string,
  signal?: AbortSignal,
): Promise<NumberedPage<KeyBindingView>> {
  const normalizedWindow = pageWindowInput(window);
  const normalizedEndpointID = resourceID(endpointId, 'endpoint id');
  const normalizedKeyID = resourceID(keyId, 'endpoint key id');
  const normalizedFilter = modelFilterInput(upstreamModel);
  const response = await coreRequest(
    `/api/endpoints/${resourcePath(normalizedEndpointID, 'endpoint id')}/keys/${resourcePath(normalizedKeyID, 'endpoint key id')}/bindings?${pageQuery(normalizedWindow, normalizedFilter)}`,
    { signal },
  );
  if (response.status !== 200) invalidResponse('key binding list status');
  return numberedKeyBindingPage(
    response.payload,
    normalizedWindow,
    normalizedEndpointID,
    normalizedKeyID,
    normalizedFilter,
  );
}

export function useKeyBindingsPage(
  accountId: string,
  endpointId: string | undefined,
  keyId: string | undefined,
  window: PageWindow,
  upstreamModel?: string,
  enabled = true,
): UseQueryResult<NumberedPage<KeyBindingView>, Error> {
  const root =
    accountId && endpointId && keyId
      ? coreKeys.endpointRouting(accountId, endpointId, [keyId])
      : [...coreKeys.endpointsRoot(accountId), 'endpoint-routing', 'none'];
  const filterKey = upstreamModel === undefined ? null : upstreamModel;
  const queryKey = [...root, 'key-bindings', filterKey, window.page, window.pageSize] as const;
  return useQuery<NumberedPage<KeyBindingView>, Error, NumberedPage<KeyBindingView>>({
    queryKey,
    queryFn: ({ signal }) => {
      if (!endpointId || !keyId) throw new Error('endpoint and key ids are required');
      return listKeyBindingsPage(endpointId, keyId, window, upstreamModel, signal);
    },
    enabled: enabled && Boolean(accountId && endpointId && keyId),
    retry: false,
  });
}
