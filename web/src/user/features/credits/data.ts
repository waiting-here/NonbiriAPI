import { decoded, queryPath } from '@shared/operations/api';
import { PAGE_SIZES, type PageSize } from '@shared/operations/pageNumbers';
import { ApiError } from '@shared/query/http';
import {
  amount,
  array,
  decimal,
  integer,
  invalidResponse,
  nullableOpaqueID,
  oneOf,
  opaqueID,
  record,
  unixSecond,
} from '@shared/operations/wire';

export const HISTORY_ASSETS = ['general', 'game', 'sketch_paper', 'sketch_brush'] as const;
export const HISTORY_ASSET_FILTERS = [...HISTORY_ASSETS, 'all'] as const;
export const HISTORY_CATEGORIES = [
  'checkin',
  'onboarding',
  'loan',
  'welfare',
  'thursday',
  'fishing',
  'linklink',
  'rps',
  'picture_book',
  'inactivity',
  'api',
  'charity',
  'donation',
  'admin',
  'penalty',
] as const;
export const HISTORY_KINDS = [
  'admin_user_adjustment',
  'admin_pool_adjustment',
  'account_delete_zero',
  'checkin_award',
  'game_onboarding_reward',
  'activity_loan',
  'anti_abuse_penalty',
  'welfare_claim',
  'thursday_contribution',
  'thursday_payout',
  'forward_reserve',
  'forward_settle',
  'forward_release',
  'charity_reserve',
  'charity_settle',
  'charity_release',
  'donor_reward',
  'thursday_finalize',
  'fishing_reserve',
  'fishing_settle',
  'fishing_release',
  'linklink_entry',
  'rps_queue_reserve',
  'rps_queue_release',
  'rps_session_start',
  'rps_round_cut',
  'rps_terminal',
  'activity_exchange',
  'image_reserve',
  'image_settle',
  'image_refund',
  'image_delete_finalize',
  'inactivity_decay',
] as const;
export const MAX_HISTORY_PAGE = 9_223_372_036_854_775_807n;
export const MAX_HISTORY_UNIX_SECOND = 253_402_300_799;
export type HistoryKind = (typeof HISTORY_KINDS)[number];
export interface HistoryEntry {
  asset_type: (typeof HISTORY_ASSETS)[number];
  operation_id: string;
  line: number;
  kind: HistoryKind;
  delta: string;
  created_at: number;
  request_id: string | null;
}
export interface HistoryPage {
  data: HistoryEntry[];
  page: string;
  page_size: PageSize;
  total: string;
  total_pages: string;
  anchor: string | null;
  current_balance: string;
  game_balance: string;
  server_now: number;
}
export interface HistoryFilter {
  asset_type?: (typeof HISTORY_ASSET_FILTERS)[number];
  page: string;
  page_size: PageSize;
  anchor?: string;
  from?: number;
  to?: number;
  category?: string;
  direction?: string;
}

export function normalizeHistory(value: unknown): HistoryPage {
  const root = record(
    value,
    [
      'data',
      'page',
      'page_size',
      'total',
      'total_pages',
      'anchor',
      'current_balance',
      'game_balance',
      'server_now',
    ],
    'credit history',
  );
  const size = integer(root.page_size, 'credit history page size', 10, 100) as PageSize;
  if (!PAGE_SIZES.includes(size)) invalidResponse('credit history page size');
  const data = array(root.data, 'credit history entries', size).map((raw): HistoryEntry => {
    const entry = record(
      raw,
      ['asset_type', 'operation_id', 'line', 'kind', 'delta', 'created_at', 'request_id'],
      'credit history entry',
    );
    const kind = oneOf(entry.kind, HISTORY_KINDS, 'credit history reason');
    const requestID = nullableOpaqueID(entry.request_id, 'req_', 'credit history request');
    if (
      requestID !== null &&
      ![
        'forward_reserve',
        'forward_settle',
        'forward_release',
        'charity_reserve',
        'charity_settle',
        'charity_release',
        'anti_abuse_penalty',
      ].includes(kind)
    )
      invalidResponse('credit history request association');
    const asset = oneOf(entry.asset_type, HISTORY_ASSETS, 'credit history asset');
    const delta = amount(entry.delta, 'credit history change');
    if (delta === '0') invalidResponse('credit history zero change');
    if ((asset === 'sketch_paper' || asset === 'sketch_brush') && delta.includes('.'))
      invalidResponse('credit history activity units');
    return {
      asset_type: asset,
      operation_id: opaqueID(entry.operation_id, 'op_', 'credit history operation'),
      line: integer(entry.line, 'credit history line', 0, 255),
      kind,
      delta,
      created_at: unixSecond(entry.created_at, 'credit history time'),
      request_id: requestID,
    };
  });
  const total = decimal(root.total, 'credit history count');
  const page = decimal(root.page, 'credit history page', { positive: true });
  const pages = decimal(root.total_pages, 'credit history pages', { positive: true });
  const count = BigInt(total);
  const expectedPages = count === 0n ? 1n : (count - 1n) / BigInt(size) + 1n;
  const remaining = count - (BigInt(page) - 1n) * BigInt(size);
  if (
    count > 9_223_372_036_854_775_807n ||
    BigInt(pages) !== expectedPages ||
    BigInt(page) > expectedPages ||
    BigInt(data.length) !== (remaining < BigInt(size) ? remaining : BigInt(size))
  )
    invalidResponse('credit history pagination');
  if (new Set(data.map((entry) => `${entry.operation_id}:${entry.line}`)).size !== data.length)
    invalidResponse('credit history duplicate entry');
  const anchor = nullableOpaqueID(root.anchor, 'op_', 'credit history anchor');
  if (data.length > 0 && anchor === null) invalidResponse('credit history anchor');
  return {
    data,
    page,
    page_size: size,
    total,
    total_pages: pages,
    anchor,
    game_balance: amount(root.game_balance, 'game balance'),
    current_balance: amount(root.current_balance, 'credit history balance'),
    server_now: unixSecond(root.server_now, 'credit history server time'),
  };
}

function invalidHistoryFilter(): never {
  throw new ApiError('invalid_request', 'Invalid credit history filter.', 400);
}

export function isHistoryPage(value: unknown): value is string {
  if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value)) return false;
  try {
    return BigInt(value) <= MAX_HISTORY_PAGE;
  } catch {
    return false;
  }
}

export function isHistoryAnchor(value: unknown): value is string {
  return typeof value === 'string' && /^op_[A-Za-z0-9_-]{21}[AQgw]$/.test(value);
}

export function isHistoryAssetFilter(
  value: unknown,
): value is NonNullable<HistoryFilter['asset_type']> {
  return typeof value === 'string' && HISTORY_ASSET_FILTERS.some((asset) => asset === value);
}

function isHistoryUnixSecond(value: unknown): value is number {
  return (
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value >= 0 &&
    value <= MAX_HISTORY_UNIX_SECOND
  );
}

function isHistoryCategory(value: unknown): value is (typeof HISTORY_CATEGORIES)[number] {
  return typeof value === 'string' && HISTORY_CATEGORIES.some((category) => category === value);
}

function isHistoryDirection(value: unknown): value is 'income' | 'expense' {
  return value === 'income' || value === 'expense';
}

export function normalizeHistoryFilter(filter: HistoryFilter): HistoryFilter {
  if (filter === null || typeof filter !== 'object' || Array.isArray(filter)) {
    return invalidHistoryFilter();
  }
  const allowed = new Set([
    'asset_type',
    'page',
    'page_size',
    'anchor',
    'from',
    'to',
    'category',
    'direction',
  ]);
  if (Object.keys(filter).some((key) => !allowed.has(key))) return invalidHistoryFilter();
  if (!isHistoryPage(filter.page) || !PAGE_SIZES.includes(filter.page_size)) {
    return invalidHistoryFilter();
  }
  if (filter.asset_type !== undefined && !isHistoryAssetFilter(filter.asset_type))
    return invalidHistoryFilter();
  if (filter.anchor !== undefined && !isHistoryAnchor(filter.anchor)) return invalidHistoryFilter();
  if (filter.from !== undefined && !isHistoryUnixSecond(filter.from)) {
    return invalidHistoryFilter();
  }
  if (filter.to !== undefined && !isHistoryUnixSecond(filter.to)) return invalidHistoryFilter();
  if (filter.from !== undefined && filter.to !== undefined && filter.from >= filter.to) {
    return invalidHistoryFilter();
  }
  if (filter.category !== undefined && !isHistoryCategory(filter.category)) {
    return invalidHistoryFilter();
  }
  if (filter.direction !== undefined && !isHistoryDirection(filter.direction)) {
    return invalidHistoryFilter();
  }
  return {
    ...(filter.asset_type !== undefined ? { asset_type: filter.asset_type } : {}),
    page: filter.page,
    page_size: filter.page_size,
    ...(filter.anchor !== undefined ? { anchor: filter.anchor } : {}),
    ...(filter.from !== undefined ? { from: filter.from } : {}),
    ...(filter.to !== undefined ? { to: filter.to } : {}),
    ...(filter.category !== undefined ? { category: filter.category } : {}),
    ...(filter.direction !== undefined ? { direction: filter.direction } : {}),
  };
}

function validateHistoryResponse(page: HistoryPage, filter: HistoryFilter): HistoryPage {
  const requested = BigInt(filter.page);
  const totalPages = BigInt(page.total_pages);
  const expected = requested > totalPages ? totalPages : requested;
  if (
    page.page_size !== filter.page_size ||
    page.page !== expected.toString() ||
    (filter.anchor !== undefined && page.anchor !== filter.anchor)
  ) {
    invalidResponse('credit history page window');
  }
  return page;
}

export function loadHistory(filter: HistoryFilter, signal?: AbortSignal): Promise<HistoryPage> {
  const normalizedFilter = normalizeHistoryFilter(filter);
  return decoded(
    queryPath('/api/credits/history', { ...normalizedFilter }),
    (value) => validateHistoryResponse(normalizeHistory(value), normalizedFilter),
    { signal },
  );
}
