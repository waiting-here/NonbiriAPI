import { decimal, integer, invalidResponse, record } from './wire';

export const PAGE_SIZES = [10, 20, 50, 100] as const;
export type PageSize = (typeof PAGE_SIZES)[number];
export const MAX_PAGE = 2_147_483_647n;

export function isPageSize(value: unknown): value is PageSize {
  return typeof value === 'number' && PAGE_SIZES.includes(value as PageSize);
}

export interface PageMetadata {
  page: string;
  page_size: PageSize;
  total_items: string;
  total_pages: string;
}

export function normalizePageMetadata(value: unknown): PageMetadata {
  const root = record(value, ['page', 'page_size', 'total_items', 'total_pages'], 'pagination');
  const page = decimal(root.page, 'page', { positive: true });
  const size = integer(root.page_size, 'page size', 10, 100);
  const total = decimal(root.total_items, 'total items');
  const pages = decimal(root.total_pages, 'total pages', { positive: true });
  if (!isPageSize(size) || BigInt(page) > MAX_PAGE || BigInt(total) > 9_223_372_036_854_775_807n) {
    invalidResponse('pagination');
  }
  const expected = BigInt(total) === 0n ? 1n : (BigInt(total) - 1n) / BigInt(size) + 1n;
  if (BigInt(pages) !== expected || BigInt(page) > expected) invalidResponse('pagination totals');
  return { page, page_size: size as PageSize, total_items: total, total_pages: pages };
}

export function isPageNumber(value: unknown, maximum = MAX_PAGE): value is string {
  return typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= maximum;
}

export function validatePageResponse(
  metadata: PageMetadata,
  requestedPage: string,
  requestedSize: PageSize,
  rowCount: number,
): void {
  const requested = BigInt(requestedPage);
  const pages = BigInt(metadata.total_pages);
  const actual = requested < pages ? requested : pages;
  const remaining = BigInt(metadata.total_items) - (actual - 1n) * BigInt(requestedSize);
  const expectedRows = remaining < BigInt(requestedSize) ? remaining : BigInt(requestedSize);
  if (
    metadata.page_size !== requestedSize ||
    metadata.page !== actual.toString() ||
    BigInt(rowCount) !== expectedRows
  ) {
    invalidResponse('pagination request window');
  }
}
