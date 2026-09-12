import { decoded, idempotentOptions } from './api';
import { array, decimalID, oneOf, record, string, invalidResponse } from './wire';
import type { CharityRole } from './charity';
import type {
  ManagedDonationPageFilters,
  DonationSourcePageFilters,
  DonationSourceKeysPageFilters,
} from './donationPages';

export type FailureSelection =
  | ({ view: 'donations' } & ManagedDonationPageFilters)
  | { view: 'donation_keys'; donation_id: string }
  | ({ view: 'sources' } & DonationSourcePageFilters)
  | ({ view: 'source_keys'; source_key: string } & DonationSourceKeysPageFilters);
export interface FailureResetRef {
  donation_id: string;
  key_id: string;
  expected_revision: string;
}
export type FailureResetStatus = 'reset' | 'ineligible' | 'conflict' | 'not_found';
export interface FailureResetResult {
  donation_id: string;
  key_id: string;
  status: FailureResetStatus;
  revision: string | null;
}
export interface FailureSelectionPage {
  items: FailureResetRef[];
  next_cursor: string | null;
}
export interface FailureResetBatch {
  results: FailureResetResult[];
  counts: { processed: string; reset: string; skipped: string };
}
export type FailureResetTarget = FailureSelection | FailureResetRef;

const base = (role: CharityRole) => (role === 'admin' ? '/admin/api' : '/api/steward');
function reference(value: unknown): FailureResetRef {
  const item = record(value, ['donation_id', 'key_id', 'expected_revision'], 'reset reference');
  return {
    donation_id: decimalID(item.donation_id, 'donation id'),
    key_id: decimalID(item.key_id, 'key id'),
    expected_revision: decimalID(item.expected_revision, 'donation revision'),
  };
}
export function selectFailureResets(
  role: CharityRole,
  selection: FailureSelection,
  cursor: string | null,
): Promise<FailureSelectionPage> {
  return decoded(
    base(role) + '/donation-keys/failure-streak-reset/selection',
    (value) => {
      const page = record(value, ['items', 'next_cursor'], 'reset selection');
      return {
        items: array(page.items, 'reset references', 100).map(reference),
        next_cursor:
          page.next_cursor === null
            ? null
            : string(page.next_cursor, 'selection cursor', { min: 1, max: 4096, bytes: 4096 }),
      };
    },
    { method: 'POST', json: { selection, cursor } },
  );
}
export function resetFailures(
  role: CharityRole,
  items: FailureResetRef[],
  key: string,
): Promise<FailureResetBatch> {
  return decoded(
    base(role) + '/donation-keys/failure-streak-reset',
    (value) => {
      const batch = record(value, ['results', 'counts'], 'reset results');
      const results = array(batch.results, 'reset results', 100).map((value, index) => {
        const item = record(value, ['donation_id', 'key_id', 'status', 'revision'], 'reset result');
        if (item.donation_id !== items[index]?.donation_id || item.key_id !== items[index]?.key_id)
          invalidResponse('reset result identity');
        return {
          donation_id: items[index].donation_id,
          key_id: items[index].key_id,
          status: oneOf(
            item.status,
            ['reset', 'ineligible', 'conflict', 'not_found'] as const,
            'reset status',
          ),
          revision: item.revision === null ? null : decimalID(item.revision, 'reset revision'),
        };
      });
      const counts = record(batch.counts, ['processed', 'reset', 'skipped'], 'reset counts');
      const reset = results.filter((item) => item.status === 'reset').length;
      if (
        results.length !== items.length ||
        counts.processed !== String(results.length) ||
        counts.reset !== String(reset) ||
        counts.skipped !== String(results.length - reset) ||
        results.some((item) => item.status === 'reset' && item.revision === null)
      )
        invalidResponse('reset counts');
      return {
        results,
        counts: {
          processed: String(results.length),
          reset: String(reset),
          skipped: String(results.length - reset),
        },
      };
    },
    idempotentOptions(key, { method: 'POST', json: { items } }),
  );
}
export function resetOwnerFailure(
  donationID: string,
  keyID: string,
  revision: string,
  key: string,
) {
  return decoded(
    '/api/donations/' +
      encodeURIComponent(donationID) +
      '/keys/' +
      encodeURIComponent(keyID) +
      '/failure-streak-reset',
    (value) => {
      const result = record(
        value,
        ['donation_id', 'key_id', 'revision', 'failure_streak'],
        'failure reset',
      );
      if (
        result.donation_id !== donationID ||
        result.key_id !== keyID ||
        result.failure_streak !== '0'
      )
        invalidResponse('failure reset identity');
      return { revision: decimalID(result.revision, 'donation revision') };
    },
    idempotentOptions(key, { method: 'POST', json: { expected_revision: revision } }),
  );
}
