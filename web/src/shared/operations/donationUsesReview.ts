import { ApiError } from '@shared/query/http';
import { NATIVE_SERVICES, type NativeService } from '@shared/nativeServices';
import type { DonationUse, UsePage } from './donationUses';

export interface ReviewedUseLabel {
  use_id: string;
  donation_key_id: string;
  native_service: NativeService;
  display_head: string;
  display_tail: string;
  decision: 'approve' | 'reject';
}

export function reviewedUseLabels(
  rows: DonationUse[],
  decision: 'approve' | 'reject',
): ReviewedUseLabel[] {
  return rows.map((row) => ({
    use_id: row.id,
    donation_key_id: row.donation_key_id,
    native_service: row.native_service,
    display_head: row.pending?.display_head ?? row.approved?.display_head ?? '',
    display_tail: row.pending?.display_tail ?? row.approved?.display_tail ?? '',
    decision,
  }));
}

export async function collectPendingDonationUses(
  read: (page: string) => Promise<UsePage>,
  signal: AbortSignal,
): Promise<DonationUse[]> {
  const rows: DonationUse[] = [];
  const seen = new Set<string>();
  let total: string | undefined;
  let pages = 1n;
  for (let page = 1n; page <= pages; page++) {
    signal.throwIfAborted();
    const result = await read(page.toString());
    signal.throwIfAborted();
    if (total === undefined) {
      total = result.pagination.total_items;
      pages = BigInt(result.pagination.total_pages);
    }
    // Donation creation caps members at 100; each member has one use per service.
    if (
      BigInt(total) > BigInt(100 * NATIVE_SERVICES.length) ||
      result.pagination.total_items !== total ||
      BigInt(result.pagination.total_pages) !== pages ||
      result.pagination.page !== page.toString() ||
      result.pagination.page_size !== 100 ||
      pages !== BigInt(Math.max(1, Math.ceil(Number(total) / 100)))
    ) {
      throw new ApiError('conflict', 'Pending content changed. Try the review again.', 409);
    }
    for (const row of result.data) {
      if (!row.pending || seen.has(row.id)) {
        throw new ApiError('conflict', 'Pending content changed. Try the review again.', 409);
      }
      seen.add(row.id);
      rows.push(row);
    }
  }
  if (String(rows.length) !== total)
    throw new ApiError('conflict', 'Pending content changed. Try the review again.', 409);
  return rows;
}
