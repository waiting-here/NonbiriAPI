import { decoded, idempotentOptions, operationKey } from '@shared/operations/api';
import { decimal, decimalID, record, string, unixSecond } from '@shared/operations/wire';
import type { DonationStatus } from './types';

export type HistoryStatus = Extract<DonationStatus, 'rejected' | 'deleted' | 'expired'> | null;
export interface HistoryScope {
  action: 'hide' | 'restore';
  status: HistoryStatus;
  q: string;
}
export interface HistoryBatchInput extends HistoryScope {
  cursor: string | null;
}
export interface HistoryReceipt {
  changedCount: string;
  scannedCount: string;
  nextCursor: string | null;
}

export function changeDonationHistory(
  id: string,
  hidden: boolean,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    '/api/donations/' + encodeURIComponent(id) + '/history',
    (value) => {
      const row = record(value, ['donation_id', 'owner_hidden_at'], 'donation history');
      return {
        donationId: decimalID(row.donation_id, 'donation id'),
        ownerHiddenAt:
          row.owner_hidden_at === null ? null : unixSecond(row.owner_hidden_at, 'hidden time'),
      };
    },
    idempotentOptions(key, { method: 'PUT', json: { hidden }, signal }),
  );
}

export function changeDonationHistoryBatch(
  input: HistoryBatchInput,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    '/api/donations/history/batch',
    (value): HistoryReceipt => {
      const row = record(value, ['changed_count', 'scanned_count', 'next_cursor'], 'history batch');
      return {
        changedCount: decimal(row.changed_count, 'changed count'),
        scannedCount: decimal(row.scanned_count, 'scanned count'),
        nextCursor:
          row.next_cursor === null ? null : string(row.next_cursor, 'history cursor', { min: 1 }),
      };
    },
    idempotentOptions(key, { method: 'POST', json: input, signal }),
  );
}

export interface HistoryProgress extends HistoryScope {
  state: 'running' | 'paused' | 'complete';
  changed: string;
  scanned: string;
  error: unknown | null;
}

/** A cursor and its request identity survive pauses and unknown transport outcomes. */
export class DonationHistoryRun {
  private cursor: string | null = null;
  private pending: { input: HistoryBatchInput; key: string } | null = null;
  private stopped = false;
  private running = false;
  private progress: HistoryProgress;
  constructor(
    scope: HistoryScope,
    private readonly send: (input: HistoryBatchInput, key: string) => Promise<HistoryReceipt>,
    private readonly notify: (progress: HistoryProgress) => void,
  ) {
    this.progress = { ...scope, state: 'paused', changed: '0', scanned: '0', error: null };
  }
  stop() {
    this.stopped = true;
  }
  private publish(patch: Partial<HistoryProgress>) {
    this.progress = { ...this.progress, ...patch };
    this.notify(this.progress);
  }
  async resume() {
    if (this.running || this.progress.state === 'complete') return;
    this.running = true;
    this.stopped = false;
    this.publish({ state: 'running', error: null });
    try {
      while (!this.stopped) {
        this.pending ??= {
          input: {
            action: this.progress.action,
            status: this.progress.status,
            q: this.progress.q,
            cursor: this.cursor,
          },
          key: operationKey(),
        };
        const receipt = await this.send(this.pending.input, this.pending.key);
        this.cursor = receipt.nextCursor;
        this.pending = null;
        this.publish({
          changed: (BigInt(this.progress.changed) + BigInt(receipt.changedCount)).toString(),
          scanned: (BigInt(this.progress.scanned) + BigInt(receipt.scannedCount)).toString(),
          state: this.cursor === null ? 'complete' : 'running',
        });
        if (this.cursor === null) return;
        await new Promise<void>((resolve) => setTimeout(resolve, 0));
      }
      this.publish({ state: 'paused' });
    } catch (error) {
      this.publish({ state: 'paused', error });
    } finally {
      this.running = false;
    }
  }
}
