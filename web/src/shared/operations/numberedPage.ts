import { ApiError } from '@shared/query/http';
import { array, invalidResponse, record } from './wire';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from './pageNumbers';

export interface NumberedPage<T> {
  data: T[];
  next_cursor: null;
  pagination: PageMetadata;
}

export function normalizeNumberedPage<T>(
  value: unknown,
  label: string,
  item: (value: unknown) => T,
  requestedPage: string,
  requestedSize: PageSize,
  identity?: (value: T) => string,
): NumberedPage<T> {
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

export function invalidRequest(): never {
  throw new ApiError('invalid_request', 'Invalid administrator list request.', 400);
}

export function validateWindow(page: string, size: PageSize): void {
  if (!isPageNumber(page) || !isPageSize(size)) invalidRequest();
}

export function validateText(value: string, maxBytes: number, allowEmpty: boolean): void {
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
