import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { FailureResetRun } from './failureResetRun';
import type { FailureResetBatch, FailureResetRef, FailureSelectionPage } from './failureReset';

const refs = (start: number, count: number, revision = '7', donation = '1'): FailureResetRef[] =>
  Array.from({ length: count }, (_, i) => ({
    donation_id: donation,
    key_id: String(start + i),
    expected_revision: revision,
  }));
const success = (items: FailureResetRef[], revision: string): FailureResetBatch => ({
  results: items.map((item) => ({
    donation_id: item.donation_id,
    key_id: item.key_id,
    status: 'reset',
    revision,
  })),
  counts: { processed: String(items.length), reset: String(items.length), skipped: '0' },
});
const noop = () => {};
const yieldNow = async () => {};
describe('failure reset selection and execution', () => {
  it('finishes enumeration before writes and carries only its own revisions across 100-item chunks', async () => {
    const events: string[] = [];
    const select = vi.fn(async (_query, cursor: string | null): Promise<FailureSelectionPage> => {
      events.push('select');
      const offset = cursor ? Number(cursor) : 0;
      return {
        items: refs(offset + 1, Math.min(100, 203 - offset)),
        next_cursor: offset < 200 ? String(offset + 100) : null,
      };
    });
    const reset = vi.fn(async (items: FailureResetRef[]) => {
      events.push('reset');
      expect(items.length).toBeLessThanOrEqual(100);
      const revision = String(BigInt(items[0].expected_revision) + BigInt(items.length));
      expect(items.every((item) => item.expected_revision === items[0].expected_revision)).toBe(
        true,
      );
      return success(items, revision);
    });
    const yieldStep = vi.fn(yieldNow);
    const run = new FailureResetRun([{ view: 'sources' }], {
      select,
      reset,
      guard: noop,
      progress: noop,
      yield: yieldStep,
    });
    await run.resume();
    expect(events).toEqual(['select', 'select', 'select', 'reset', 'reset', 'reset']);
    expect(reset.mock.calls.map(([items]) => items[0].expected_revision)).toEqual([
      '7',
      '107',
      '207',
    ]);
    expect(run.counts).toEqual({ selected: 203, processed: 203, reset: 203, skipped: 0 });
    expect(run.phase).toBe('done');
    expect(yieldStep.mock.calls.length).toBeGreaterThanOrEqual(events.length);
  });
  it('cancels during selection without writing, then continues the fixed cursor', async () => {
    const reset = vi.fn(async (items: FailureResetRef[]) => success(items, '8'));
    const select = vi.fn(async (_query, cursor: string | null) => {
      if (cursor === null) run.stop();
      return { items: refs(cursor ? 2 : 1, 1), next_cursor: cursor ? null : 'next' };
    });
    const run = new FailureResetRun([{ view: 'donations' }], {
      select,
      reset,
      guard: noop,
      progress: noop,
      yield: yieldNow,
    });
    await run.resume();
    expect(run.phase).toBe('paused');
    expect(reset).not.toHaveBeenCalled();
    await run.resume();
    expect(select.mock.calls.map(([, cursor]) => cursor)).toEqual([null, 'next']);
    expect(run.counts.reset).toBe(2);
  });
  it('retains exact items and idempotency key after a lost response', async () => {
    const reset = vi.fn(
      async (items: FailureResetRef[], key: string): Promise<FailureResetBatch> => {
        expect(key).toMatch(/^[A-Za-z0-9_-]{22}$/);
        if (reset.mock.calls.length === 1) throw new ApiError('network_error', 'lost response', 0);
        return success(items, String(BigInt(items[0].expected_revision) + BigInt(items.length)));
      },
    );
    const run = new FailureResetRun(refs(1, 101), {
      select: vi.fn(),
      reset,
      guard: noop,
      progress: noop,
      yield: yieldNow,
    });
    await run.resume();
    expect(run.phase).toBe('paused');
    expect(run.counts.processed).toBe(0);
    await run.resume();
    expect(reset.mock.calls).toHaveLength(3);
    expect(reset.mock.calls[1]).toEqual(reset.mock.calls[0]);
    expect(reset.mock.calls[2][1]).not.toBe(reset.mock.calls[0][1]);
    expect(reset.mock.calls[2][0][0].expected_revision).toBe('107');
    expect(run.counts.reset).toBe(101);
  });
  it('marks a donation with mixed selection revisions as conflicted before writing', async () => {
    const reset = vi.fn(async (items: FailureResetRef[]) => success(items, '8'));
    const run = new FailureResetRun([...refs(1, 2), ...refs(2, 2, '8'), ...refs(4, 1, '7', '2')], {
      select: vi.fn(),
      reset,
      guard: noop,
      progress: noop,
      yield: yieldNow,
    });
    await run.resume();
    expect(reset.mock.calls[0][0]).toEqual(refs(4, 1, '7', '2'));
    expect(run.counts).toEqual({ selected: 4, processed: 4, reset: 1, skipped: 3 });
  });
  it('does not advance later keys from a foreign conflict revision', async () => {
    const reset = vi.fn(async (items: FailureResetRef[]): Promise<FailureResetBatch> => ({
      results: items.map((item) => ({
        donation_id: item.donation_id,
        key_id: item.key_id,
        status: 'conflict',
        revision: '50',
      })),
      counts: { processed: '100', reset: '0', skipped: '100' },
    }));
    const run = new FailureResetRun(refs(1, 103), {
      select: vi.fn(),
      reset,
      guard: noop,
      progress: noop,
      yield: yieldNow,
    });
    await run.resume();
    expect(reset).toHaveBeenCalledTimes(1);
    expect(run.counts).toEqual({ selected: 103, processed: 103, reset: 0, skipped: 103 });
  });
  it('stops future requests and ignores a late response after the account changes', async () => {
    let current = true;
    const reset = vi.fn(async (items: FailureResetRef[]) => {
      current = false;
      return success(items, '107');
    });
    const run = new FailureResetRun(refs(1, 101), {
      select: vi.fn(),
      reset,
      guard: () => {
        if (!current) throw new Error('session changed');
      },
      progress: noop,
      yield: yieldNow,
    });
    await run.resume();
    expect(reset).toHaveBeenCalledTimes(1);
    expect(run.counts.processed).toBe(0);
    expect(run.phase).toBe('paused');
  });
});
