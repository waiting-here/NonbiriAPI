import { decoded, queryPath } from '@shared/operations/api';
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

export const HISTORY_CATEGORIES = [
  'checkin',
  'welfare',
  'thursday',
  'fishing',
  'linklink',
  'rps',
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
] as const;
export type HistoryKind = (typeof HISTORY_KINDS)[number];
export interface HistoryEntry {
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
  page_size: number;
  total: string;
  total_pages: string;
  anchor: string | null;
  current_balance: string;
  server_now: number;
}
export interface HistoryFilter {
  page: string;
  page_size: number;
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
      'server_now',
    ],
    'credit history',
  );
  const size = integer(root.page_size, 'credit history page size', 20, 100);
  if (![20, 50, 100].includes(size)) invalidResponse('credit history page size');
  const data = array(root.data, 'credit history entries', size).map((raw): HistoryEntry => {
    const entry = record(
      raw,
      ['operation_id', 'line', 'kind', 'delta', 'created_at', 'request_id'],
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
    const delta = amount(entry.delta, 'credit history change');
    if (delta === '0') invalidResponse('credit history zero change');
    return {
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
    current_balance: amount(root.current_balance, 'credit history balance'),
    server_now: unixSecond(root.server_now, 'credit history server time'),
  };
}

export function loadHistory(filter: HistoryFilter, signal?: AbortSignal): Promise<HistoryPage> {
  return decoded(queryPath('/api/credits/history', { ...filter }), normalizeHistory, { signal });
}
