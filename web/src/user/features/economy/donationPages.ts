import {
  DONATION_DISPLAY_STATUSES,
  normalizeUseSummary,
  type DonationDisplayStatus,
  type UseSummary,
} from '@shared/operations/donationUses';
import { ApiError, apiFetch } from '@shared/query/http';
import {
  array,
  boolean,
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

export type OwnerDonationView = 'current' | 'history' | 'hidden' | 'all';
export type OwnerDonationKeyView = 'current' | 'ended' | 'all';

export interface OwnerDonationPageFilters {
  view?: OwnerDonationView;
  status?: DonationStatus | '';
  review_status?: DonationDisplayStatus | '';
  q?: string;
}

export type OwnerDonationStateCounts = Record<DonationKey['charityState'], string>;

export interface OwnerDonationSummary {
  useSummary?: UseSummary;
  id: string;
  ownerHiddenAt: number | null;
  discordPublicThanks?: boolean | null;
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
  'failure_disable_threshold',
  'smart_backoff',
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
  'rate_limited',
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
  if (
    Object.keys(root).some(
      (key) => key !== 'status' && key !== 'review_status' && key !== 'q' && key !== 'view',
    )
  ) {
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
  const view = root.view ?? 'all';
  if (!['current', 'history', 'hidden', 'all'].includes(view as string))
    invalidInput('donation view');
  const review_status =
    root.review_status === undefined
      ? ''
      : oneOf(
          root.review_status,
          ['', ...DONATION_DISPLAY_STATUSES],
          'donation review status filter',
        );
  return { status, review_status, q, view: view as OwnerDonationView };
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
  validatePageResponse(pagination, requestedPage, requestedSize);
  return { data, nextCursor: null, pagination };
}

function normalizeReviewResult(value: unknown, label: string): DonationReviewResult | null {
  if (value === null) return null;
  const root = record(value, ['decision', 'reason', 'reviewed_at'], label);
  return {
    decision: oneOf(
      root.decision === 'force_reject' ? 'reject' : root.decision,
      ['approve', 'reject'] as const,
      `${label} decision`,
    ),
    reason: string(root.reason, `${label} reason`, { multiline: true }),
    reviewedAt: unixSecond(root.reviewed_at, `${label} time`),
  };
}

// Summaries are server projections. Parse what the page displays without
// reimplementing approval, lifecycle or preview-count relationships here.
function normalizeStateCounts(value: unknown, label: string): OwnerDonationStateCounts {
  const root = record(value, OWNER_DONATION_STATES, label);
  return Object.fromEntries(
    OWNER_DONATION_STATES.map((state) => [state, decimal(root[state], `${label} ${state}`)]),
  ) as OwnerDonationStateCounts;
}

function normalizeOwnerDonation(value: unknown, index: number): OwnerDonationSummary {
  const label = `owner donation ${index + 1}`;
  const root = record(
    value,
    [...OWNER_DONATION_FIELDS, 'discord_public_thanks', 'owner_hidden_at', 'use_summary'],
    label,
    OWNER_DONATION_FIELDS,
  );
  const status = oneOf(root.status, OWNER_DONATION_STATUSES, `${label} status`);
  const summary = root.use_summary == null ? undefined : normalizeUseSummary(root.use_summary);
  const reviewResult = normalizeReviewResult(root.review_result, `${label} review`);
  const createdAt = unixSecond(root.created_at, `${label} creation time`);
  const updatedAt = unixSecond(root.updated_at, `${label} update time`);
  const keyCount = decimal(root.key_count, `${label} key count`);
  const stateCounts = normalizeStateCounts(root.state_counts, `${label} state counts`);
  const sourceCount = decimal(root.source_count, `${label} source count`);
  const sources = array(root.sources, `${label} sources`).map(normalizeDonationSafeSource);
  return {
    ...(summary === undefined ? {} : { useSummary: summary }),
    id: decimalID(root.id, `${label} id`),
    ownerHiddenAt:
      root.owner_hidden_at == null ? null : unixSecond(root.owner_hidden_at, 'hidden time'),
    discordPublicThanks:
      root.discord_public_thanks == null
        ? null
        : boolean(root.discord_public_thanks, `${label} public thanks`),
    status,
    revision: decimal(root.revision, `${label} revision`, { positive: true }),
    description: string(root.description, `${label} description`, { multiline: true }),
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
  const root = record(
    value,
    [
      ...OWNER_KEY_FIELDS,
      'endpoint_id',
      'review',
      'rate_limited_until',
      'service_statuses',
      'input_token_reserve',
      'output_token_reserve',
      'breakdown_started_at',
    ],
    label,
    OWNER_KEY_FIELDS,
  );
  const key = normalizeDonationKey({
    ...(root.endpoint_id === undefined ? {} : { endpoint_id: root.endpoint_id }),
    id: root.id,
    endpoint_key_id: root.endpoint_key_id,
    display_head: root.display_head,
    display_tail: root.display_tail,
    safe_source: root.safe_source,
    physical_enabled: root.physical_enabled,
    charity_state: root.charity_state,
    rate_limited_until: root.rate_limited_until,
    limits: root.limits,
    usage: root.usage,
    token_reserve: root.token_reserve,
    ...(root.input_token_reserve !== undefined
      ? { input_token_reserve: root.input_token_reserve }
      : {}),
    ...(root.output_token_reserve !== undefined
      ? { output_token_reserve: root.output_token_reserve }
      : {}),
    ...(root.breakdown_started_at !== undefined
      ? { breakdown_started_at: root.breakdown_started_at }
      : {}),
    failure_disable_threshold: root.failure_disable_threshold,
    smart_backoff: root.smart_backoff,
    service_statuses: root.service_statuses,
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
  const rawRules = array(root.rules, `${label} rules`);
  const rules = rawRules.map((entry, ruleIndex) =>
    normalizeRuleView(entry, `${label} rule ${ruleIndex + 1}`),
  );
  const ruleIDs = rules.map((rule) => rule.id);
  if (ruleIDs.some((id) => id === null) || new Set(ruleIDs).size !== ruleIDs.length) {
    invalidResponse(`${label} rule ids`);
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
  normalizeOwnerFilters(filters);
  const result = pageResponse(
    value,
    'owner donation page',
    window.page,
    window.pageSize,
    normalizeOwnerDonation,
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
      view: filters.view === undefined ? undefined : normalizedFilters.view,
      status: normalizedFilters.status || undefined,
      review_status: normalizedFilters.review_status || undefined,
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
  view?: OwnerDonationKeyView,
): Promise<OwnerDonationPage<OwnerDonationKeySummary>> {
  const id = requestDonationID(donationID);
  const window = requestPage(page, pageSize);
  const payload = await apiFetch<unknown>(
    queryPath(`/api/donations/${encodeURIComponent(id)}/keys`, {
      view,
      page: window.page,
      page_size: window.pageSize,
    }),
    { signal },
  );
  return normalizeOwnerDonationKeysPage(payload, id, window.page, window.pageSize);
}
