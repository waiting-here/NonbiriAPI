import { describe, expect, it, vi } from 'vitest';
import type { DonationUse, UsePage } from './donationUses';
import { collectPendingDonationUses } from './donationUsesReview';

const row = (id: number, canReview = true) =>
  ({
    id: String(id),
    donation_id: '3',
    donation_key_id: String(Math.ceil(id / 19)),
    revision: '7',
    native_service: 'responses',
    pending: { expected_review_revision: '9' },
    capabilities: { can_review: canReview },
  }) as DonationUse;
const page = (number: number, rows: DonationUse[], total: number): UsePage => ({
  data: rows,
  pagination: {
    page: String(number),
    page_size: 100,
    total_items: String(total),
    total_pages: String(Math.max(1, Math.ceil(total / 100))),
  },
});
const controller = () => new AbortController();

describe('cross-page donation review', () => {
  it('collects the complete pending set, including blocked material, before the atomic review', async () => {
    const rows = Array.from({ length: 205 }, (_, index) => row(index + 1, index !== 204));
    const read = vi.fn(async (number: string) =>
      page(
        Number(number),
        rows.slice((Number(number) - 1) * 100, Number(number) * 100),
        rows.length,
      ),
    );
    const snapshot = await collectPendingDonationUses(read, controller().signal);
    expect(read.mock.calls.map((call) => call[0])).toEqual(['1', '2', '3']);
    expect(snapshot).toHaveLength(205);
    expect(snapshot[204]!.capabilities.can_review).toBe(false);
  });
  it('rejects changing page counts and cancellation before any write', async () => {
    const signal = controller();
    const read = vi
      .fn()
      .mockResolvedValueOnce(
        page(
          1,
          Array.from({ length: 100 }, (_, index) => row(index + 1)),
          101,
        ),
      )
      .mockResolvedValueOnce(page(2, [], 100));
    await expect(collectPendingDonationUses(read, signal.signal)).rejects.toMatchObject({
      code: 'conflict',
    });
    signal.abort();
    const untouched = vi.fn();
    await expect(collectPendingDonationUses(untouched, signal.signal)).rejects.toBeDefined();
    expect(untouched).not.toHaveBeenCalled();
  });
});
