import { apiFetch } from '@shared/query/http';
import { queryPath } from './api';
import {
  normalizeDonationPageHandling,
  type DonationPageResult,
  type DonationPageRole,
  type ManagedDonationPageFilters,
} from './donationPages';
import {
  decimal,
  decimalID,
  invalidResponse,
  nullableUnixSecond,
  oneOf,
  record,
  string,
  unixSecond,
} from './wire';
import { normalizeNumberedPage } from './numberedPage';
import { normalizeUseSummary, type UseSummary } from './donationUses';
import type { DonationHandling, DonationStatus } from './charity';
import type { PageSize } from './pageNumbers';

export interface UserDonationHistoryItem {
  id: string;
  status: DonationStatus;
  description: string;
  review_result: { reason: string } | null;
  created_at: number;
  updated_at: number;
  key_count: string;
  source_count: string;
  handling: DonationHandling;
  use_summary?: UseSummary;
  owner_hidden_at: number | null;
}
export type UserDonationHistoryFilters = Pick<ManagedDonationPageFilters, 'q' | 'review_status'>;

const FIELDS = [
  'id',
  'status',
  'description',
  'review_result',
  'created_at',
  'updated_at',
  'key_count',
  'source_count',
  'handling',
  'use_summary',
  'owner_hidden_at',
] as const;

export function normalizeUserDonationHistoryPage(
  value: unknown,
  page: string,
  pageSize: PageSize,
): DonationPageResult<UserDonationHistoryItem> {
  return normalizeNumberedPage(
    value,
    'user donation history',
    (value): UserDonationHistoryItem => {
      const row = record(
        value,
        FIELDS,
        'user donation history row',
        FIELDS.filter((field) => field !== 'use_summary'),
      );
      const status = oneOf(
        row.status,
        ['pending', 'approved', 'rejected', 'deleted', 'expired'] as const,
        'history donation status',
      );
      return {
        id: decimalID(row.id, 'history donation id'),
        status,
        description: string(row.description, 'history description', { multiline: true }),
        review_result:
          row.review_result === null
            ? null
            : {
                reason: string(
                  record(row.review_result, ['reason'], 'history review').reason,
                  'history review reason',
                  { multiline: true },
                ),
              },
        created_at: unixSecond(row.created_at, 'history creation time'),
        updated_at: unixSecond(row.updated_at, 'history update time'),
        key_count: decimal(row.key_count, 'history key count'),
        source_count: decimal(row.source_count, 'history source count'),
        handling: normalizeDonationPageHandling(row.handling, 'history handling', status),
        ...(row.use_summary == null ? {} : { use_summary: normalizeUseSummary(row.use_summary) }),
        owner_hidden_at: nullableUnixSecond(row.owner_hidden_at, 'owner hidden time'),
      };
    },
    page,
    pageSize,
    (row) => row.id,
  );
}

function path(role: DonationPageRole, userID: string): string {
  const id = decimalID(userID, 'history user id');
  return `${role === 'admin' ? '/admin/api' : '/api/steward'}/users/${encodeURIComponent(id)}/donations`;
}

export async function getUserDonationHistoryPage(
  role: DonationPageRole,
  userID: string,
  filters: UserDonationHistoryFilters,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<DonationPageResult<UserDonationHistoryItem>> {
  const payload = await apiFetch<unknown>(
    queryPath(path(role, userID), {
      q: filters.q || undefined,
      review_status: filters.review_status || undefined,
      page,
      page_size: pageSize,
    }),
    { signal },
  );
  return normalizeUserDonationHistoryPage(payload, page, pageSize);
}

export async function getUserDonationHistoryDetail(
  role: DonationPageRole,
  userID: string,
  donationID: string,
  signal?: AbortSignal,
): Promise<UserDonationHistoryItem> {
  const id = decimalID(donationID, 'history donation id');
  const payload = await apiFetch<unknown>(`${path(role, userID)}/${encodeURIComponent(id)}`, {
    signal,
  });
  const out = normalizeUserDonationHistoryPage(
    {
      data: [payload],
      next_cursor: null,
      pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
    },
    '1',
    20,
  ).data[0]!;
  if (out.id !== id) invalidResponse('donation history identity');
  return out;
}
