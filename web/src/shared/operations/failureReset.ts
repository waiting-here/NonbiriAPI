import { decoded, idempotentOptions } from './api';
import {
  array,
  boolean,
  decimal,
  decimalID,
  nullableUnixSecond,
  integer,
  oneOf,
  record,
  string,
  invalidResponse,
} from './wire';
import { NATIVE_SERVICES, type NativeService } from '@shared/nativeServices';
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
export type FailureResetStatus = 'reset' | 'ineligible' | 'conflict' | 'not_found' | 'no_service';
export interface ServiceFailureReset {
  use_id: string;
  native_service: NativeService;
  status: string;
  reason?: string;
  failure_streak_before: string;
  failure_disabled_before: boolean;
  retry_after_until: number | null;
  backoff_retry_at: number | null;
  manually_disabled: boolean;
}
export function serviceFailureReset(value: unknown): ServiceFailureReset {
  const row = record(value, ['use_id', 'native_service', 'status'], 'service reset');
  return {
    use_id: decimalID(row.use_id, 'use id'),
    native_service: oneOf(row.native_service, NATIVE_SERVICES, 'native service'),
    status: string(row.status, 'service reset status'),
    reason: row.reason == null ? undefined : string(row.reason, 'reset reason'),
    failure_streak_before: decimal(row.failure_streak_before ?? '0', 'previous failures'),
    failure_disabled_before: boolean(
      row.failure_disabled_before ?? false,
      'previous failure pause',
    ),
    retry_after_until: nullableUnixSecond(row.retry_after_until ?? null, 'retry after'),
    backoff_retry_at: nullableUnixSecond(row.backoff_retry_at ?? null, 'backoff'),
    manually_disabled: boolean(row.manually_disabled ?? false, 'manual pause'),
  };
}
function serviceCounts(value: unknown) {
  const counts = record(value, ['reset', 'already_clear', 'skipped'], 'service counts');
  return {
    reset: integer(counts.reset, 'reset services'),
    already_clear: integer(counts.already_clear, 'clear services'),
    skipped: integer(counts.skipped, 'skipped services'),
  };
}
export interface FailureResetResult {
  donation_id: string;
  key_id: string;
  status: FailureResetStatus;
  outcome?: string;
  services?: ServiceFailureReset[];
  services_truncated?: boolean;
  service_counts?: { reset: number; already_clear: number; skipped: number };
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
            : string(page.next_cursor, 'selection cursor', { min: 1 }),
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
            ['reset', 'ineligible', 'conflict', 'not_found', 'no_service'] as const,
            'reset status',
          ),
          revision: item.revision === null ? null : decimalID(item.revision, 'reset revision'),
          outcome: item.outcome == null ? undefined : string(item.outcome, 'reset outcome'),
          services: array(item.services ?? [], 'reset services').map(serviceFailureReset),
          services_truncated: boolean(
            item.services_truncated ?? false,
            'service details truncated',
          ),
          service_counts:
            item.service_counts == null ? undefined : serviceCounts(item.service_counts),
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
      return {
        revision: decimalID(result.revision, 'donation revision'),
        outcome: result.outcome == null ? undefined : string(result.outcome, 'reset outcome'),
        services: array(result.services ?? [], 'reset services').map(serviceFailureReset),
      };
    },
    idempotentOptions(key, { method: 'POST', json: { expected_revision: revision } }),
  );
}
