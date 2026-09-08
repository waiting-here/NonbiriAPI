import { ApiError, apiFetch } from '@shared/query/http';
import {
  array,
  decimal,
  decimalID,
  invalidResponse,
  oneOf,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from '@shared/operations/pageNumbers';
import { queryPath } from '@shared/operations/api';
import { normalizeRuleView, type RecurringLimitRuleView } from '@shared/operations/recurringLimits';
import { normalizeDonationKey, normalizeDonationSafeSource } from './normalize';
import type { DonationKey, DonationKeySource, DonationReviewResult, DonationStatus } from './types';

export interface OwnerDonationPageFilters {
  status?: DonationStatus | '';
  q?: string;
}

export type OwnerDonationStateCounts = Record<DonationKey['charityState'], string>;

export interface OwnerDonationSummary {
  id: string;
  status: DonationStatus;
  revision: string;
  description: string;
  reviewResult: DonationReviewResult | null;
  createdAt: number;
  updatedAt: number;
  keyCount: string;
  stateCounts: OwnerDonationStateCounts;
  sourceCount: string;
  sources: DonationKeySource[];
}

export interface OwnerDonationKeySummary extends DonationKey {
  donationId: string;
  keyId: string;
  donationRevision: string;
  ruleCount: string;
  rules: RecurringLimitRuleView[];
}

export interface OwnerDonationPage<T> {
  data: T[];
  nextCursor: null;
  pagination: PageMetadata;
}

export type OwnerDonationPageResult<T> = OwnerDonationPage<T>;

const OWNER_DONATION_FIELDS = [
  'id',
  'status',
  'revision',
  'description',
  'review_result',
  'created_at',
  'updated_at',
  'key_count',
  'state_counts',
  'source_count',
  'sources',
] as const;

const OWNER_KEY_FIELDS = [
  'id',
  'endpoint_key_id',
  'display_head',
  'display_tail',
  'safe_source',
  'physical_enabled',
  'charity_state',
  'limits',
  'usage',
  'token_reserve',
  'expires_at',
  'streak',
  'ended_reason',
  'donation_id',
  'key_id',
  'donation_revision',
  'rule_count',
  'rules',
] as const;

const OWNER_DONATION_STATES = [
  'available',
  'pending',
  'disabled',
  'suspended',
  'exhausted',
  'expired',
  'ended',
] as const satisfies readonly DonationKey['charityState'][];

const OWNER_DONATION_STATUSES = [
  'pending',
  'approved',
  'rejected',
  'deleted',
  'expired',
] as const satisfies readonly DonationStatus[];

const MAX_INT64 = 9_223_372_036_854_775_807n;

function invalidInput(field: string): never {
  throw new ApiError('invalid_request', `Invalid ${field}.`, 400);
}

function requestPage(page: unknown, pageSize: unknown): { page: string; pageSize: PageSize } {
  if (!isPageNumber(page)) invalidInput('page');
  if (!isPageSize(pageSize)) invalidInput('page size');
  return { page, pageSize };
}

function requestDonationID(value: unknown): string {
  if (typeof value !== 'string' || !/^[1-9][0-9]{0,18}$/.test(value) || BigInt(value) > MAX_INT64) {
    invalidInput('donation id');
  }
  return value;
}

function wellFormedText(value: string): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) return false;
  }
  return true;
}

function rejectLoneSurrogates(value: unknown, label: string, seen = new WeakSet<object>()): void {
  if (typeof value === 'string') {
    if (!wellFormedText(value)) invalidResponse(label);
    return;
  }
  if (value === null || typeof value !== 'object') return;
  if (seen.has(value)) return;
  seen.add(value);
  if (Array.isArray(value)) {
    for (const entry of value) rejectLoneSurrogates(entry, label, seen);
    return;
  }
  for (const entry of Object.values(value)) rejectLoneSurrogates(entry, label, seen);
}

function requestSearch(value: unknown): string {
  if (typeof value !== 'string' || !wellFormedText(value)) invalidInput('donation search');
  if (
    Array.from(value).length > 128 ||
    new TextEncoder().encode(value).byteLength > 512 ||
    value.includes('\u0000')
  ) {
    invalidInput('donation search');
  }
  return value;
}

function normalizeOwnerFilters(value: unknown): Required<OwnerDonationPageFilters> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    invalidInput('donation page filters');
  }
  const root = value as Record<string, unknown>;
  if (Object.keys(root).some((key) => key !== 'status' && key !== 'q')) {
    invalidInput('donation page filters');
  }
  let status: DonationStatus | '' = '';
  if (root.status !== undefined) {
    if (
      typeof root.status !== 'string' ||
      !(['', 'pending', 'approved', 'rejected', 'deleted', 'expired'] as const).includes(
        root.status as DonationStatus | '',
      )
    ) {
      invalidInput('donation status filter');
    }
    status = root.status as DonationStatus | '';
  }
  const q = root.q === undefined ? '' : requestSearch(root.q);
  return { status, q };
}

function pageResponse<T>(
  value: unknown,
  label: string,
  requestedPage: string,
  requestedSize: PageSize,
  normalizeItem: (value: unknown, index: number) => T,
): OwnerDonationPage<T> {
  const root = record(value, ['data', 'next_cursor', 'pagination'], label);
  if (root.next_cursor !== null) invalidResponse(`${label} cursor`);
  const pagination = normalizePageMetadata(root.pagination);
  const data = array(root.data, `${label} data`, 100).map(normalizeItem);
  validatePageResponse(pagination, requestedPage, requestedSize, data.length);
  return { data, nextCursor: null, pagination };
}

function normalizeReviewResult(value: unknown, label: string): DonationReviewResult | null {
  if (value === null) return null;
  rejectLoneSurrogates(value, label);
  const root = record(value, ['decision', 'reason', 'reviewed_at'], label);
  return {
    decision: oneOf(root.decision, ['approve', 'reject'] as const, `${label} decision`),
    reason: string(root.reason, `${label} reason`, { max: 1_024, bytes: 4_096 }),
    reviewedAt: unixSecond(root.reviewed_at, `${label} time`),
  };
}

function normalizeStateCounts(
  value: unknown,
  keyCount: string,
  label: string,
): OwnerDonationStateCounts {
  const root = record(value, OWNER_DONATION_STATES, label);
  const counts = {} as OwnerDonationStateCounts;
  let total = 0n;
  for (const state of OWNER_DONATION_STATES) {
    const count = decimal(root[state], `${label} ${state}`);
    counts[state] = count;
    total += BigInt(count);
  }
  if (total !== BigInt(keyCount)) invalidResponse(`${label} total`);
  return counts;
}

function sourceIdentity(value: DonationKeySource): string {
  return value.kind === 'custom'
    ? `custom\u0000${value.connectorType}\u0000${value.baseUrl}`
    : `mainstream\u0000${value.channelId}`;
}

function normalizeSources(value: unknown, sourceCount: string, label: string): DonationKeySource[] {
  const entries = array(value, label, 3);
  const sources = entries.map((entry, index) => {
    rejectLoneSurrogates(entry, `${label} ${index + 1}`);
    return normalizeDonationSafeSource(entry);
  });
  const expected = BigInt(sourceCount) < 3n ? Number(sourceCount) : 3;
  if (sources.length !== expected) invalidResponse(`${label} count`);
  if (new Set(sources.map(sourceIdentity)).size !== sources.length) {
    invalidResponse(`${label} identities`);
  }
  return sources;
}

function validateSummaryState(
  status: DonationStatus,
  reviewResult: DonationReviewResult | null,
  keyCount: string,
  stateCounts: OwnerDonationStateCounts,
  label: string,
): void {
  const total = BigInt(keyCount);
  const pending = BigInt(stateCounts.pending);
  const expired = BigInt(stateCounts.expired);
  const ended = BigInt(stateCounts.ended);
  const terminal = expired + ended;
  if (status !== 'pending' && pending !== 0n) {
    invalidResponse(`${label} terminal state`);
  }
  if (status === 'pending') {
    // The backend keeps the header pending while any key is logically live,
    // but its per-key projection may already contain expired or ended keys.
    if (reviewResult !== null || pending === 0n || pending + terminal !== total) {
      invalidResponse(`${label} pending state`);
    }
    return;
  }
  if (status === 'approved') {
    if (reviewResult?.decision !== 'approve' || terminal === total) {
      invalidResponse(`${label} approved state`);
    }
    return;
  }
  if (status === 'rejected') {
    if (reviewResult?.decision !== 'reject' || ended !== total) {
      invalidResponse(`${label} rejected state`);
    }
    return;
  }
  if (status === 'deleted') {
    if (reviewResult?.decision === 'reject' || terminal !== total) {
      invalidResponse(`${label} deleted state`);
    }
    return;
  }
  // logicalHeaderTx can expose an unreviewed pending donation as expired once
  // all keys are terminal; that projection deliberately keeps review_result null.
  if (reviewResult?.decision === 'reject' || expired === 0n || terminal !== total) {
    invalidResponse(`${label} expired state`);
  }
}

function normalizeOwnerDonation(
  value: unknown,
  index: number,
  filters: Required<OwnerDonationPageFilters>,
): OwnerDonationSummary {
  const label = `owner donation ${index + 1}`;
  rejectLoneSurrogates(value, label);
  const root = record(value, OWNER_DONATION_FIELDS, label);
  const status = oneOf(root.status, OWNER_DONATION_STATUSES, `${label} status`);
  if (filters.status !== '' && filters.status !== status) invalidResponse(`${label} status filter`);
  const reviewResult = normalizeReviewResult(root.review_result, `${label} review`);
  const createdAt = unixSecond(root.created_at, `${label} creation time`);
  const updatedAt = unixSecond(root.updated_at, `${label} update time`);
  if (updatedAt < createdAt) invalidResponse(`${label} timestamps`);
  if (
    reviewResult !== null &&
    (reviewResult.reviewedAt < createdAt || reviewResult.reviewedAt > updatedAt)
  ) {
    invalidResponse(`${label} review timestamp`);
  }
  const keyCount = decimal(root.key_count, `${label} key count`);
  if (keyCount === '0') invalidResponse(`${label} key count`);
  const stateCounts = normalizeStateCounts(root.state_counts, keyCount, `${label} state counts`);
  const sourceCount = decimal(root.source_count, `${label} source count`);
  if (sourceCount === '0' || BigInt(sourceCount) > BigInt(keyCount)) {
    invalidResponse(`${label} source count`);
  }
  const sources = normalizeSources(root.sources, sourceCount, `${label} sources`);
  validateSummaryState(status, reviewResult, keyCount, stateCounts, label);
  return {
    id: decimalID(root.id, `${label} id`),
    status,
    revision: decimal(root.revision, `${label} revision`, { positive: true }),
    description: string(root.description, `${label} description`, { max: 1_024, bytes: 4_096 }),
    reviewResult,
    createdAt,
    updatedAt,
    keyCount,
    stateCounts,
    sourceCount,
    sources,
  };
}

function normalizeOwnerKey(
  value: unknown,
  index: number,
  expectedDonationID: string,
): OwnerDonationKeySummary {
  const label = `owner donation key ${index + 1}`;
  rejectLoneSurrogates(value, label);
  const root = record(value, OWNER_KEY_FIELDS, label);
  const key = normalizeDonationKey({
    id: root.id,
    endpoint_key_id: root.endpoint_key_id,
    display_head: root.display_head,
    display_tail: root.display_tail,
    safe_source: root.safe_source,
    physical_enabled: root.physical_enabled,
    charity_state: root.charity_state,
    limits: root.limits,
    usage: root.usage,
    token_reserve: root.token_reserve,
    expires_at: root.expires_at,
    streak: root.streak,
    ended_reason: root.ended_reason,
  });
  const donationID = decimalID(root.donation_id, `${label} donation id`);
  const keyID = decimalID(root.key_id, `${label} key id`);
  if (donationID !== expectedDonationID || key.id !== keyID) {
    invalidResponse(`${label} identity`);
  }
  const donationRevision = decimal(root.donation_revision, `${label} donation revision`, {
    positive: true,
  });
  const ruleCount = decimal(root.rule_count, `${label} rule count`);
  if (BigInt(ruleCount) > 16n) invalidResponse(`${label} rule count`);
  const rawRules = array(root.rules, `${label} rules`, 3);
  const expectedRules = BigInt(ruleCount) < 3n ? Number(ruleCount) : 3;
  if (rawRules.length !== expectedRules) invalidResponse(`${label} rule count`);
  const rules = rawRules.map((entry, ruleIndex) =>
    normalizeRuleView(entry, `${label} rule ${ruleIndex + 1}`),
  );
  const ruleIDs = rules.map((rule) => rule.id);
  if (ruleIDs.some((id) => id === null) || new Set(ruleIDs).size !== ruleIDs.length) {
    invalidResponse(`${label} rule ids`);
  }
  let sawNonLimited = false;
  for (const rule of rules) {
    if (rule.state === 'limited' && sawNonLimited) invalidResponse(`${label} rule order`);
    if (rule.state !== 'limited') sawNonLimited = true;
  }
  return {
    ...key,
    donationId: donationID,
    keyId: keyID,
    donationRevision,
    ruleCount,
    rules,
  };
}

export function normalizeOwnerDonationsPage(
  value: unknown,
  filters: OwnerDonationPageFilters,
  page: string,
  pageSize: PageSize,
): OwnerDonationPage<OwnerDonationSummary> {
  const window = requestPage(page, pageSize);
  const normalizedFilters = normalizeOwnerFilters(filters);
  const result = pageResponse(
    value,
    'owner donation page',
    window.page,
    window.pageSize,
    (entry, index) => normalizeOwnerDonation(entry, index, normalizedFilters),
  );
  if (new Set(result.data.map((item) => item.id)).size !== result.data.length) {
    invalidResponse('owner donation ids');
  }
  return result;
}

export const normalizeOwnerDonationPage = normalizeOwnerDonationsPage;

export function normalizeOwnerDonationKeysPage(
  value: unknown,
  donationID: string,
  page: string,
  pageSize: PageSize,
): OwnerDonationPage<OwnerDonationKeySummary> {
  const window = requestPage(page, pageSize);
  const expectedDonationID = requestDonationID(donationID);
  const result = pageResponse(
    value,
    'owner donation key page',
    window.page,
    window.pageSize,
    (entry, index) => normalizeOwnerKey(entry, index, expectedDonationID),
  );
  if (new Set(result.data.map((item) => item.keyId)).size !== result.data.length) {
    invalidResponse('owner donation key ids');
  }
  return result;
}

export const normalizeOwnerDonationKeyPage = normalizeOwnerDonationKeysPage;

export async function getOwnerDonationsPage(
  filters: OwnerDonationPageFilters,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<OwnerDonationPage<OwnerDonationSummary>> {
  const normalizedFilters = normalizeOwnerFilters(filters);
  const window = requestPage(page, pageSize);
  const payload = await apiFetch<unknown>(
    queryPath('/api/donations', {
      status: normalizedFilters.status || undefined,
      q: normalizedFilters.q || undefined,
      page: window.page,
      page_size: window.pageSize,
    }),
    { signal },
  );
  return normalizeOwnerDonationsPage(payload, normalizedFilters, window.page, window.pageSize);
}

export async function getOwnerDonationKeysPage(
  donationID: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<OwnerDonationPage<OwnerDonationKeySummary>> {
  const id = requestDonationID(donationID);
  const window = requestPage(page, pageSize);
  const payload = await apiFetch<unknown>(
    queryPath(`/api/donations/${encodeURIComponent(id)}/keys`, {
      page: window.page,
      page_size: window.pageSize,
    }),
    { signal },
  );
  return normalizeOwnerDonationKeysPage(payload, id, window.page, window.pageSize);
}
