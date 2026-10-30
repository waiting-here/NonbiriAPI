import { decoded, idempotentOptions, queryPath } from './api';
import {
  array,
  boolean,
  decimal,
  decimalID,
  nullableDecimalID,
  nullableString,
  nullableUnixSecond,
  oneOf,
  record,
  string,
} from './wire';
import { normalizePageMetadata, type PageMetadata, type PageSize } from './pageNumbers';
import { normalizeNativeServices } from '@shared/connections';
import type { NativeService } from '@shared/nativeServices';
import { normalizeManagedDiscovery, type DiscoveryEvidence } from './donationDiscovery';
import { serviceFailureReset, type ServiceFailureReset } from './failureReset';
export type UseRole = 'owner' | 'admin' | 'steward';
export interface UseVersion {
  credential_id: string;
  credential_version: string;
  grant_revision: string;
  status: 'pending' | 'approved' | 'rejected' | 'withdrawn' | 'superseded';
  review_note: string;
  reviewed_at: number | null;
  expected_review_revision: string | null;
  discovery_source_id: string | null;
  discovery_source_revision: string | null;
  display_head: string;
  display_tail: string;
  review: { required: boolean; revision: string | null; material_available: boolean };
  discovery: DiscoveryEvidence | null;
}
export interface DonationUse {
  failure_reset?: ServiceFailureReset;
  id: string;
  donation_id: string;
  donation_key_id: string;
  endpoint_key_id: string | null;
  native_service: NativeService;
  enabled: boolean;
  discovery_enabled: boolean;
  revision: string;
  approved: UseVersion | null;
  pending: UseVersion | null;
  latest: UseVersion | null;
  health: {
    failure_streak: string;
    failure_disabled: boolean;
    retry_after_until: number | null;
    backoff_retry_at: number | null;
  } | null;
  capabilities: {
    can_review: boolean;
    can_manage: boolean;
    can_reset_failures: boolean;
    can_discover: boolean;
    can_bind: boolean;
    unavailable_reason: string | null;
  };
}
export interface UsePage {
  data: DonationUse[];
  pagination: PageMetadata;
}
export interface UseReviewItem {
  use_id: string;
  expected_revision: string;
  expected_review_revision: string | null;
  decision: 'approve' | 'reject';
  review_note: string;
}
export interface UseReviewResult {
  use_id: string;
  ok: boolean;
  use?: DonationUse;
  error?: { code: string; message: string };
}
export const DONATION_REVIEW_STATUSES = [
  'pending',
  'approved_pending',
  'approved',
  'partially_approved',
  'rejected',
  'withdrawn',
  'mixed',
  'none',
] as const;
export type DonationReviewStatus = (typeof DONATION_REVIEW_STATUSES)[number];
export const DONATION_DISPLAY_STATUSES = [
  'pending',
  'approved_pending',
  'approved',
  'partially_approved',
  'rejected',
  'withdrawn',
  'mixed',
  'deleted',
  'expired',
] as const;
export type DonationDisplayStatus = (typeof DONATION_DISPLAY_STATUSES)[number];
export interface UseSummary {
  total: string;
  pending: string;
  approved: string;
  rejected?: string;
  withdrawn?: string;
  review_status?: DonationReviewStatus;
}
export function normalizeUseSummary(value: unknown): UseSummary {
  const row = record(
    value,
    ['total', 'pending', 'approved', 'rejected', 'withdrawn', 'review_status'],
    'use summary',
    ['total', 'pending', 'approved'],
  );
  return {
    total: decimal(row.total, 'total uses'),
    pending: decimal(row.pending, 'pending uses'),
    approved: decimal(row.approved, 'approved uses'),
    ...(row.rejected === undefined ? {} : { rejected: decimal(row.rejected, 'rejected uses') }),
    ...(row.withdrawn === undefined ? {} : { withdrawn: decimal(row.withdrawn, 'withdrawn uses') }),
    ...(row.review_status === undefined
      ? {}
      : { review_status: oneOf(row.review_status, DONATION_REVIEW_STATUSES, 'use review status') }),
  };
}
export function donationDisplayStatus(
  status: 'pending' | 'approved' | 'rejected' | 'deleted' | 'expired',
  summary?: UseSummary,
): DonationDisplayStatus {
  if (status !== 'pending' && status !== 'approved') return status;
  if (summary?.review_status && summary.review_status !== 'none') return summary.review_status;
  return status;
}
function version(value: unknown): UseVersion | null {
  if (value === null) return null;
  const row = record(
    value,
    [
      'credential_id',
      'credential_version',
      'grant_revision',
      'status',
      'review_note',
      'reviewed_at',
      'expected_review_revision',
      'discovery_source_id',
      'discovery_source_revision',
      'display_head',
      'display_tail',
      'review',
      'discovery',
    ],
    'use version',
  );
  const review = record(
    row.review,
    ['required', 'revision', 'material_available'],
    'use review material',
  );
  return {
    credential_id: decimalID(row.credential_id, 'credential'),
    credential_version: decimalID(row.credential_version, 'credential version'),
    grant_revision: decimalID(row.grant_revision, 'grant revision'),
    status: oneOf(
      row.status,
      ['pending', 'approved', 'rejected', 'withdrawn', 'superseded'] as const,
      'use status',
    ),
    review_note: string(row.review_note, 'review note', { multiline: true }),
    reviewed_at: nullableUnixSecond(row.reviewed_at, 'review time'),
    expected_review_revision: nullableString(row.expected_review_revision, 'body review revision'),
    discovery_source_id: nullableDecimalID(row.discovery_source_id, 'discovery source'),
    discovery_source_revision: nullableDecimalID(
      row.discovery_source_revision,
      'discovery source revision',
    ),
    display_head: string(row.display_head, 'credential head'),
    display_tail: string(row.display_tail, 'credential tail'),
    review: {
      required: boolean(review.required, 'body review required'),
      revision: nullableString(review.revision, 'body review revision'),
      material_available: boolean(review.material_available, 'body material available'),
    },
    discovery: row.discovery === null ? null : normalizeManagedDiscovery(row.discovery),
  };
}
export function normalizeDonationUse(value: unknown): DonationUse {
  const row = record(
    value,
    [
      'id',
      'donation_id',
      'donation_key_id',
      'endpoint_key_id',
      'native_service',
      'enabled',
      'discovery_enabled',
      'revision',
      'approved',
      'pending',
      'latest',
      'health',
      'capabilities',
    ],
    'donation use',
  );
  const caps = record(
    row.capabilities,
    [
      'can_review',
      'can_manage',
      'can_reset_failures',
      'can_discover',
      'can_bind',
      'unavailable_reason',
    ],
    'use capabilities',
  );
  const health =
    row.health === null
      ? null
      : record(
          row.health,
          ['failure_streak', 'failure_disabled', 'retry_after_until', 'backoff_retry_at'],
          'use health',
        );
  return {
    failure_reset: row.failure_reset == null ? undefined : serviceFailureReset(row.failure_reset),
    id: decimalID(row.id, 'use'),
    donation_id: decimalID(row.donation_id, 'donation'),
    donation_key_id: decimalID(row.donation_key_id, 'donation key'),
    endpoint_key_id:
      row.endpoint_key_id === null ? null : decimalID(row.endpoint_key_id, 'endpoint key'),
    native_service: normalizeNativeServices([row.native_service])[0]!,
    enabled: boolean(row.enabled, 'use enabled'),
    discovery_enabled: boolean(row.discovery_enabled, 'discovery enabled'),
    revision: decimalID(row.revision, 'use revision'),
    approved: version(row.approved),
    pending: version(row.pending),
    latest: version(row.latest),
    health: health
      ? {
          failure_streak: decimal(health.failure_streak, 'failure streak'),
          failure_disabled: boolean(health.failure_disabled, 'failure disabled'),
          retry_after_until: nullableUnixSecond(health.retry_after_until, 'retry after'),
          backoff_retry_at: nullableUnixSecond(health.backoff_retry_at, 'backoff'),
        }
      : null,
    capabilities: {
      can_review: boolean(caps.can_review, 'review'),
      can_manage: boolean(caps.can_manage, 'manage'),
      can_reset_failures: boolean(caps.can_reset_failures, 'reset failures'),
      can_discover: boolean(caps.can_discover, 'discover'),
      can_bind: boolean(caps.can_bind, 'bind'),
      unavailable_reason: nullableString(caps.unavailable_reason, 'unavailable reason'),
    },
  };
}
export const donationUseBase = (role: UseRole) =>
  role === 'admin' ? '/admin/api' : role === 'steward' ? '/api/steward' : '/api';
const path = (role: UseRole, donationId: string) =>
  donationUseBase(role) + '/donations/' + encodeURIComponent(donationId) + '/uses';
export function getDonationUses(
  role: UseRole,
  donationId: string,
  page: string,
  pageSize: PageSize,
  state: string,
  signal?: AbortSignal,
  donationKeyId?: string,
): Promise<UsePage> {
  return decoded(
    queryPath(path(role, donationId), {
      page,
      page_size: pageSize,
      state,
      donation_key_id: donationKeyId,
    }),
    (value) => {
      const row = record(value, ['data', 'pagination', 'next_cursor'], 'donation use page');
      return {
        data: array(row.data, 'donation uses').map(normalizeDonationUse),
        pagination: normalizePageMetadata(row.pagination),
      };
    },
    { signal },
  );
}
export function reviewDonationUses(
  role: Exclude<UseRole, 'owner'>,
  donationId: string,
  items: UseReviewItem[],
  key: string,
  signal?: AbortSignal,
): Promise<UseReviewResult[]> {
  return decoded(
    path(role, donationId) + '/review',
    (value) => {
      const row = record(value, ['results'], 'use review results');
      return array(row.results, 'use results').map((value) => {
        const result = record(value, ['use_id', 'ok', 'use', 'error'], 'use result', [
          'use_id',
          'ok',
        ]);
        const error =
          result.error == null ? null : record(result.error, ['code', 'message'], 'use error');
        return {
          use_id: decimalID(result.use_id, 'review use'),
          ok: boolean(result.ok, 'review success'),
          ...(result.use == null ? {} : { use: normalizeDonationUse(result.use) }),
          ...(error
            ? {
                error: {
                  code: string(error.code, 'error code'),
                  message: string(error.message, 'error message', { multiline: true }),
                },
              }
            : {}),
        };
      });
    },
    idempotentOptions(key, { method: 'POST', json: { items }, signal }),
  );
}
export function mutateDonationUse(
  role: UseRole,
  donationId: string,
  useId: string,
  action: 'patch' | 'withdraw' | 'reset',
  input: Record<string, unknown>,
  key: string,
  signal?: AbortSignal,
): Promise<{ failure_reset?: ServiceFailureReset }> {
  return decoded(
    path(role, donationId) +
      '/' +
      encodeURIComponent(useId) +
      (action === 'withdraw' ? '/pending' : action === 'reset' ? '/failure-streak-reset' : ''),
    (value) => {
      const row = record(value ?? {}, [], 'use receipt');
      return {
        failure_reset:
          row.failure_reset == null ? undefined : serviceFailureReset(row.failure_reset),
      };
    },
    idempotentOptions(key, {
      method: action === 'withdraw' ? 'DELETE' : action === 'reset' ? 'POST' : 'PATCH',
      json: input,
      signal,
    }),
  );
}

export interface AddDonationUsesInput {
  expected_revision: string;
  donation_key_id: string;
  credential_id: string;
  credential_version: string;
  grant_revision: string;
  items: { native_service: NativeService; expected_revision: string | null }[];
  discovery_source_id: string | null;
  discovery_source_revision: string | null;
}
export function addDonationUses(
  donationId: string,
  input: AddDonationUsesInput,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    path('owner', donationId),
    (value) => {
      const receipt = record(value, ['donation_revision', 'uses'], 'added donation uses');
      return {
        donation_revision: decimal(receipt.donation_revision, 'donation revision', {
          positive: true,
        }),
        uses: array(receipt.uses, 'added uses', 19).map((value) => {
          const item = record(value, ['id', 'revision', 'native_service'], 'added use');
          return {
            id: decimalID(item.id, 'use'),
            revision: decimal(item.revision, 'use revision', { positive: true }),
            native_service: normalizeNativeServices([item.native_service])[0]!,
          };
        }),
      };
    },
    idempotentOptions(key, { method: 'POST', json: input, signal }),
  );
}
