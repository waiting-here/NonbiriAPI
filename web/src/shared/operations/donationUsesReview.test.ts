import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import type { DonationUse, UsePage, UseReviewResult } from './donationUses';
import {
  collectPendingDonationUses,
  prepareDonationUseReview,
  runDonationUseReview,
} from './donationUsesReview';

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
const control = () => ({ assertCurrent: vi.fn(), stopped: () => false, onProgress: vi.fn() });

describe('cross-page donation review', () => {
  it('collects all pages before review and freezes versions without adding later submissions', async () => {
    const rows = Array.from({ length: 205 }, (_, index) => row(index + 1, index !== 204));
    const read = vi.fn(async (number: string) =>
      page(
        Number(number),
        rows.slice((Number(number) - 1) * 100, Number(number) * 100),
        rows.length,
      ),
    );
    const snapshot = await collectPendingDonationUses(read, controller().signal);
    const plan = prepareDonationUseReview(snapshot, 'reject', 'Reason');
    rows[0]!.revision = '8';
    rows.push(row(206));
    expect(read.mock.calls.map((call) => call[0])).toEqual(['1', '2', '3']);
    expect(plan.batches.map((batch) => batch.items.length)).toEqual([100, 100, 4]);
    expect(plan.blocked.map((use) => use.id)).toEqual(['205']);
    expect(plan.batches[0]!.items[0]).toMatchObject({
      use_id: '1',
      expected_revision: '7',
      expected_review_revision: '9',
      decision: 'reject',
      review_note: 'Reason',
    });
    expect(plan.batches.flatMap((batch) => batch.items).some((item) => item.use_id === '206')).toBe(
      false,
    );
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
  it('preserves confirmed batches and replays an uncertain batch with its original key and body', async () => {
    const plan = prepareDonationUseReview(
      Array.from({ length: 101 }, (_, index) => row(index + 1)),
      'approve',
      '',
    );
    const receipts = new Map<string, UseReviewResult[]>();
    const send = vi
      .fn()
      .mockImplementationOnce(async (items) =>
        items.map((item: { use_id: string }) => ({ use_id: item.use_id, ok: true })),
      )
      .mockRejectedValueOnce(new ApiError('result_unknown', 'Response lost', 0))
      .mockResolvedValueOnce([
        { use_id: '101', ok: false, error: { code: 'conflict', message: 'Changed' } },
      ]);
    await expect(runDonationUseReview(plan, receipts, send, control())).rejects.toMatchObject({
      code: 'result_unknown',
    });
    const result = await runDonationUseReview(plan, receipts, send, control());
    expect(send).toHaveBeenCalledTimes(3);
    expect(send.mock.calls[2]).toEqual(send.mock.calls[1]);
    expect(result).toHaveLength(101);
    expect(result.filter((item) => item.ok)).toHaveLength(100);
    expect(result[100]!.error?.code).toBe('conflict');
  });
  it('pauses between batches and resumes the fixed remainder without resending confirmed items', async () => {
    const plan = prepareDonationUseReview(
      Array.from({ length: 101 }, (_, index) => row(index + 1)),
      'approve',
      '',
    );
    const receipts = new Map<string, UseReviewResult[]>();
    let paused = false;
    const send = vi.fn(async (items) => {
      paused = true;
      return items.map((item: { use_id: string }) => ({ use_id: item.use_id, ok: true }));
    });
    const first = await runDonationUseReview(plan, receipts, send, {
      ...control(),
      stopped: () => paused,
    });
    expect(first).toHaveLength(100);
    expect(send).toHaveBeenCalledTimes(1);
    const result = await runDonationUseReview(plan, receipts, send, control());
    expect(result).toHaveLength(101);
    expect(send.mock.calls[1]![0]).toHaveLength(1);
  });
  it('keeps an incomplete receipt uncertain instead of claiming all succeeded', async () => {
    const plan = prepareDonationUseReview([row(1), row(2)], 'reject', '');
    const receipts = new Map<string, UseReviewResult[]>();
    await expect(
      runDonationUseReview(plan, receipts, async () => [{ use_id: '1', ok: true }], control()),
    ).rejects.toMatchObject({ code: 'invalid_response' });
    expect(receipts.size).toBe(0);
  });
});
