import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import {
  normalizeManagedDiscovery,
  type DiscoveryEvidence,
  type DiscoveryRef,
  type DiscoverySelection,
} from './donationDiscovery';
import { DonationDiscoveryRun } from './donationDiscoveryRun';

const item = { donation_id: '2', key_id: '3' };
const checking: DiscoveryEvidence = {
  state: 'checking',
  revision: '2',
  result: null,
  safe_class: 'none',
  observed_at: 1800000000,
  count: null,
};
const empty: DiscoveryEvidence = { ...checking, state: 'succeeded', result: 'empty', count: '0' };
const transport = () => ({
  select: vi
    .fn<(donation: string | null, cursor: string | null) => Promise<DiscoverySelection>>()
    .mockResolvedValue({ items: [item], next_cursor: null }),
  start: vi
    .fn<(item: DiscoveryRef, key: string) => Promise<DiscoveryEvidence>>()
    .mockResolvedValue(checking),
  read: vi.fn<(item: DiscoveryRef) => Promise<DiscoveryEvidence>>().mockResolvedValue(empty),
  guard: vi.fn(),
  progress: vi.fn(),
  wait: vi.fn(async () => {}),
});

describe('managed donation discovery', () => {
  it('continues through sparse pages and includes keys beyond the visible donation page', async () => {
    const io = transport();
    io.select.mockResolvedValueOnce({ items: [], next_cursor: 'next' });
    const run = new DonationDiscoveryRun({ donation_id: null }, io);
    await run.resume();
    expect(io.select.mock.calls).toEqual([
      [null, null],
      [null, 'next'],
    ]);
    expect(run.phase).toBe('done');
    expect(run.counts).toEqual({ selected: 1, processed: 1, succeeded: 1, failed: 0, skipped: 0 });
    expect(run.results[0]).toMatchObject({ ...item, status: 'succeeded', count: '0' });
  });
  it('reuses the same request key after an uncertain POST and prevents overlapping resumes', async () => {
    const io = transport();
    io.start.mockRejectedValueOnce(new TypeError('connection lost'));
    const run = new DonationDiscoveryRun(item, io);
    await Promise.all([run.resume(), run.resume()]);
    expect(run.phase).toBe('paused');
    expect(io.start).toHaveBeenCalledTimes(1);
    await run.resume();
    expect(io.start.mock.calls[0]).toEqual(io.start.mock.calls[1]);
    expect(run.phase).toBe('done');
  });
  it('resumes an accepted operation by reading it without resending', async () => {
    const io = transport();
    io.read.mockRejectedValueOnce(new TypeError('connection lost'));
    const run = new DonationDiscoveryRun(item, io);
    await run.resume();
    await run.resume();
    expect(io.start).toHaveBeenCalledTimes(1);
    expect(io.read).toHaveBeenCalledTimes(2);
    expect(run.phase).toBe('done');
  });
  it('stops before the next key, then resumes without skipping or repeating the first', async () => {
    const io = transport();
    const next = { donation_id: '8', key_id: '9' };
    io.select.mockResolvedValue({ items: [item, next], next_cursor: null });
    const run = new DonationDiscoveryRun({ donation_id: null }, io);
    io.read.mockImplementationOnce(async () => {
      run.stop();
      return empty;
    });
    await run.resume();
    expect(run.phase).toBe('paused');
    expect(io.start).toHaveBeenCalledTimes(1);
    await run.resume();
    expect(io.start.mock.calls.map((call) => call[0])).toEqual([item, next]);
    expect(run.counts.succeeded).toBe(2);
  });
  it('distinguishes ineligible, concurrent, superseded, and failed results', async () => {
    const io = transport();
    io.select.mockResolvedValue({
      items: [1, 2, 3, 4].map((id) => ({ donation_id: '2', key_id: String(id) })),
      next_cursor: null,
    });
    io.start
      .mockRejectedValueOnce(new ApiError('resource_locked', 'Unavailable', 409))
      .mockRejectedValueOnce(new ApiError('conflict', 'Checking', 409));
    io.read
      .mockResolvedValueOnce({ ...empty, revision: '3' })
      .mockResolvedValueOnce({ ...checking, state: 'failed', safe_class: 'auth' });
    const run = new DonationDiscoveryRun({ donation_id: '2' }, io);
    await run.resume();
    expect(run.results.map((row) => row.status)).toEqual([
      'ineligible',
      'conflict',
      'superseded',
      'failed',
    ]);
    expect(run.counts).toEqual({ selected: 4, processed: 4, succeeded: 0, failed: 1, skipped: 3 });
  });
  it('stops on capability loss and rejects contradictory or leaking evidence', async () => {
    const io = transport();
    io.start.mockRejectedValue(new ApiError('forbidden', 'Forbidden', 403));
    const run = new DonationDiscoveryRun(item, io);
    await run.resume();
    expect(run.phase).toBe('paused');
    expect(io.read).not.toHaveBeenCalled();
    expect(normalizeManagedDiscovery(empty)).toEqual(empty);
    for (const malformed of [
      { ...empty, count: '1001' },
      { ...empty, result: 'nonempty' },
      { ...checking, count: '1' },
      { ...empty, secret: 'unexpected' },
    ]) {
      expect(() => normalizeManagedDiscovery(malformed)).toThrow();
    }
  });
});
