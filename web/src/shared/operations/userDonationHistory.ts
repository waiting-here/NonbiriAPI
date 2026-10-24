import { apiFetch } from '@shared/query/http';
import { queryPath } from './api';
import {
  normalizeManagedDonationsPage,
  type AdminDonationPageItem,
  type DonationPageResult,
  type DonationPageRole,
  type ManagedDonationPageFilters,
} from './donationPages';
import { array, decimalID, invalidResponse, nullableUnixSecond, record } from './wire';
import type { PageSize } from './pageNumbers';

export type UserDonationHistoryItem = AdminDonationPageItem & { owner_hidden_at: number | null };
export type UserDonationHistoryFilters = Pick<ManagedDonationPageFilters, 'q' | 'review_status'>;

const FIELDS = [
  'id',
  'status',
  'revision',
  'description',
  'review_result',
  'created_at',
  'updated_at',
  'key_count',
  'state_counts',
  'source_count',
  'sources',
  'handling',
  'owner',
  'reviewer',
  'discord_public_thanks',
  'use_summary',
  'owner_hidden_at',
] as const;

export function normalizeUserDonationHistoryPage(
  value: unknown,
  role: DonationPageRole,
  filters: UserDonationHistoryFilters,
  page: string,
  pageSize: PageSize,
): DonationPageResult<UserDonationHistoryItem> {
  const root = record(value, ['data', 'next_cursor', 'pagination'], 'user donation history');
  const hidden: (number | null)[] = [];
  const data = array(root.data, 'user donation history rows', 100).map((value) => {
    const row = record(
      value,
      FIELDS,
      'user donation history row',
      FIELDS.filter((field) => field !== 'use_summary'),
    );
    hidden.push(nullableUnixSecond(row.owner_hidden_at, 'owner hidden time'));
    const { owner_hidden_at: ignored, ...summary } = row;
    void ignored;
    return summary;
  });
  const normalized = normalizeManagedDonationsPage(
    { ...root, data },
    role,
    filters,
    page,
    pageSize,
  );
  return {
    ...normalized,
    data: normalized.data.map((row, index) => ({ ...row, owner_hidden_at: hidden[index]! })),
  };
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
  return normalizeUserDonationHistoryPage(payload, role, filters, page, pageSize);
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
    role,
    {},
    '1',
    20,
  ).data[0]!;
  if (out.id !== id) invalidResponse('donation history identity');
  return out;
}
