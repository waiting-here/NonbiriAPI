import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { DonationHistoryRun, type HistoryProgress, type HistoryReceipt } from './donationHistory';

const scope = { action: 'hide' as const, status: null, q: 'ended search' };
const receipt = (nextCursor: string | null, count = '100'): HistoryReceipt => ({
  nextCursor,
  changedCount: count,
  scannedCount: count,
});

describe('bounded owner history runs', () => {
  it('automatically follows each cursor and uses a new identity after each confirmed batch', async () => {
    const send = vi
      .fn()
      .mockResolvedValueOnce(receipt('next'))
      .mockResolvedValueOnce(receipt(null, '17'));
    let progress: HistoryProgress | undefined;
    const run = new DonationHistoryRun(scope, send, (value) => {
      progress = value;
    });
    await run.resume();
    expect(send.mock.calls.map(([input]) => input)).toEqual([
      { ...scope, cursor: null },
      { ...scope, cursor: 'next' },
    ]);
    expect(send.mock.calls[0][1]).not.toBe(send.mock.calls[1][1]);
    expect(progress).toMatchObject({
      state: 'complete',
      changed: '117',
      scanned: '117',
      error: null,
    });
    await run.resume();
    expect(send).toHaveBeenCalledTimes(2);
  });

  it('retries an unknown response with exactly the same body and key without double counting', async () => {
    const send = vi
      .fn()
      .mockResolvedValueOnce(receipt('next'))
      .mockRejectedValueOnce(new ApiError('network_error', 'Lost response', 0))
      .mockResolvedValueOnce(receipt(null, '5'));
    let progress: HistoryProgress | undefined;
    const run = new DonationHistoryRun(scope, send, (value) => {
      progress = value;
    });
    await run.resume();
    expect(progress).toMatchObject({ state: 'paused', changed: '100', scanned: '100' });
    await run.resume();
    expect(send.mock.calls[1]).toEqual(send.mock.calls[2]);
    expect(progress).toMatchObject({ state: 'complete', changed: '105', scanned: '105' });
  });

  it('pauses after the in-flight batch, then continues from its receipt', async () => {
    let finish!: (value: HistoryReceipt) => void;
    const send = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<HistoryReceipt>((resolve) => {
            finish = resolve;
          }),
      )
      .mockResolvedValueOnce(receipt(null, '1'));
    let progress: HistoryProgress | undefined;
    const run = new DonationHistoryRun(scope, send, (value) => {
      progress = value;
    });
    const running = run.resume();
    await run.resume();
    run.stop();
    finish(receipt('next'));
    await running;
    expect(send).toHaveBeenCalledTimes(1);
    expect(progress).toMatchObject({ state: 'paused', changed: '100' });
    await run.resume();
    expect(send.mock.calls[1][0].cursor).toBe('next');
    expect(progress).toMatchObject({ state: 'complete', changed: '101' });
  });
});
