import { ApiError } from '@shared/query/http';
import { NATIVE_SERVICES, type NativeService } from '@shared/nativeServices';
import { operationKey } from './api';
import type { DonationUse, UsePage, UseReviewItem, UseReviewResult } from './donationUses';

export interface DonationUseReviewPlan {
  decision: 'approve' | 'reject';
  rows: DonationUse[];
  blocked: DonationUse[];
  batches: { key: string; items: UseReviewItem[] }[];
}

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
      throw new ApiError('conflict', 'The pending uses changed. Load the selection again.', 409);
    }
    for (const row of result.data) {
      if (!row.pending || seen.has(row.id)) {
        throw new ApiError('conflict', 'The pending uses changed. Load the selection again.', 409);
      }
      seen.add(row.id);
      rows.push(row);
    }
  }
  if (String(rows.length) !== total)
    throw new ApiError('conflict', 'The pending uses changed. Load the selection again.', 409);
  return rows;
}

export function prepareDonationUseReview(
  rows: DonationUse[],
  decision: 'approve' | 'reject',
  note: string,
): DonationUseReviewPlan {
  const snapshot = structuredClone(rows);
  const eligible = snapshot.filter((row) => row.pending && row.capabilities.can_review);
  const items: UseReviewItem[] = eligible.map((row) => ({
    use_id: row.id,
    expected_revision: row.revision,
    expected_review_revision: row.pending!.expected_review_revision,
    decision,
    review_note: note.trim(),
  }));
  const batches: DonationUseReviewPlan['batches'] = [];
  for (let index = 0; index < items.length; index += 100)
    batches.push({ key: operationKey(), items: items.slice(index, index + 100) });
  return {
    decision,
    rows: snapshot,
    blocked: snapshot.filter((row) => !row.pending || !row.capabilities.can_review),
    batches,
  };
}

export async function runDonationUseReview(
  plan: DonationUseReviewPlan,
  receipts: Map<string, UseReviewResult[]>,
  send: (items: UseReviewItem[], key: string) => Promise<UseReviewResult[]>,
  control: {
    assertCurrent: () => void;
    stopped: () => boolean;
    onProgress: (results: UseReviewResult[]) => void;
  },
): Promise<UseReviewResult[]> {
  for (const batch of plan.batches) {
    control.assertCurrent();
    if (receipts.has(batch.key)) continue;
    if (control.stopped()) break;
    const result = await send(batch.items, batch.key);
    control.assertCurrent();
    const expected = new Set(batch.items.map((item) => item.use_id));
    if (
      result.length !== expected.size ||
      result.some((item) => !expected.delete(item.use_id)) ||
      expected.size
    ) {
      throw new ApiError(
        'invalid_response',
        'The review receipt is incomplete. Check the original action result.',
        0,
      );
    }
    receipts.set(batch.key, result);
    control.onProgress(plan.batches.flatMap((entry) => receipts.get(entry.key) ?? []));
  }
  return plan.batches.flatMap((batch) => receipts.get(batch.key) ?? []);
}
