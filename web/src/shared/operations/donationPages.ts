import { ApiError } from '@shared/query/http';
import {
  normalizeDonationHandling,
  normalizeManagedKey,
  normalizeManagedSource,
  type CharityRole,
  type DonationHandling,
  type DonationStatus,
  type ManagedDonationKey,
} from './charity';
import { decoded, queryPath } from './api';
import {
  array,
  decimal,
  decimalID,
  invalidResponse,
  nullableString,
  oneOf,
  record,
  string,
  unixSecond,
} from './wire';
import { normalizeRuleView, type RecurringLimitRuleView } from './recurringLimits';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from './pageNumbers';

export type DonationPageRole = CharityRole;
export type DonationHandlingState = DonationHandling['state'];

export interface ManagedDonationPageFilters {
  status?: DonationStatus | '';
  handling?: DonationHandlingState | '';
  q?: string;
}

export interface DonationSourcePageFilters {
  q?: string;
  scope?: 'active' | 'all';
  handling?: 'pending';
}

export interface DonationSourceKeysPageFilters extends DonationSourcePageFilters {
  idle?: 'yes' | 'no';
}

export interface DonationPageResult<T> {
  data: T[];
  next_cursor: null;
  pagination: PageMetadata;
}

export type DonationPageSafeSource =
  | {
      kind: 'custom';
      connector_type: 'openai-compatible' | 'anthropic-compatible' | 'ai-sdk-gateway-v3';
      base_url: string;
    }
  | {
      kind: 'mainstream';
      connector_type: 'openai-compatible' | 'anthropic-compatible' | 'ai-sdk-gateway-v3';
      base_url: string;
      channel_id: string;
      name: string;
    };

export interface DonationPageReviewResult {
  decision: 'approve' | 'reject';
  reason: string;
  reviewed_at: number;
}

export interface DonationPageReviewer {
  user_id: string | null;
  role: 'admin' | 'steward';
}

export interface AdminDonationPageOwner {
  user_id: string;
  discord_id: string | null;
  display_name: string;
}

export interface StewardDonationPageOwner {
  user_id: string;
  discord_id: string | null;
  display_name: string;
}

export type DonationState =
  'available' | 'pending' | 'disabled' | 'suspended' | 'exhausted' | 'expired' | 'ended';

export type DonationStateCounts = Record<DonationState, string>;

interface DonationPageCommon {
  id: string;
  status: DonationStatus;
  revision: string;
  description: string;
  review_result: DonationPageReviewResult | null;
  created_at: number;
  updated_at: number;
  key_count: string;
  state_counts: DonationStateCounts;
  source_count: string;
  sources: DonationPageSafeSource[];
  handling: DonationHandling;
  reviewer: DonationPageReviewer | null;
}

export interface AdminDonationPageItem extends DonationPageCommon {
  owner: AdminDonationPageOwner | null;
}

export interface StewardDonationPageItem extends DonationPageCommon {
  owner: StewardDonationPageOwner | null;
}

export interface ManagedDonationKeySummary extends ManagedDonationKey {
  donation_id: string;
  key_id: string;
  donation_revision: string;
  rule_count: string;
  rules: RecurringLimitRuleView[];
  handling: DonationHandling;
}

export interface DonationSourceSummary {
  source_key: string;
  safe_source: DonationPageSafeSource;
  donation_count: string;
  key_count: string;
  usable_key_count: string;
  pending_donation_count: string;
}

const DONATION_STATES = [
  'available',
  'pending',
  'disabled',
  'suspended',
  'exhausted',
  'expired',
  'ended',
] as const satisfies readonly DonationState[];

const HANDLING_FIELDS = [
  'state',
  'revision',
  'processed_at',
  'processed_by_role',
  'closed_at',
  'closed_reason',
] as const;

const DONATION_COMMON_FIELDS = [
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
  'handling',
  'reviewer',
] as const;

const DONATION_KEY_FIELDS = [
  'failure_disable_threshold',
  'binding_count',
  'idle',
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
  'authorized_expires_at',
  'safe_note',
  'max_concurrency',
  'max_rpm',
] as const;

const KEY_SUMMARY_FIELDS = [
  ...DONATION_KEY_FIELDS,
  'donation_id',
  'key_id',
  'donation_revision',
  'rule_count',
  'rules',
  'handling',
] as const;

function invalidInput(label: string): never {
  throw new ApiError('invalid_request', `The ${label} is invalid.`, 400);
}

function basePath(role: DonationPageRole): string {
  if (role === 'admin') return '/admin/api';
  if (role === 'steward') return '/api/steward';
  return invalidInput('management role');
}

function pageArguments(page: unknown, pageSize: unknown): asserts page is string {
  if (!isPageNumber(page)) invalidInput('page');
  if (!isPageSize(pageSize)) invalidInput('page size');
}

function queryText(value: unknown, label: string): string {
  if (typeof value !== 'string') return invalidInput(label);
  if (
    Array.from(value).length > 128 ||
    new TextEncoder().encode(value).byteLength > 512 ||
    value.includes('\0')
  ) {
    return invalidInput(label);
  }
  return value;
}

function donationFilters(value: unknown): Required<ManagedDonationPageFilters> {
  const root = record(value, ['status', 'handling', 'q'], 'donation page filters', []);
  const status =
    root.status === undefined
      ? ''
      : oneOf(
          root.status,
          ['', 'pending', 'approved', 'rejected', 'deleted', 'expired'] as const,
          'donation status filter',
        );
  const handling =
    root.handling === undefined
      ? ''
      : oneOf(
          root.handling,
          ['', 'legacy', 'pending', 'processed', 'closed'] as const,
          'donation handling filter',
        );
  const q = root.q === undefined ? '' : queryText(root.q, 'donation search');
  return { status, handling, q };
}

interface NormalizedSourceFilters {
  q: string;
  scope: '' | 'active' | 'all';
  handling: '' | 'pending';
  idle: '' | 'yes' | 'no';
}

function sourceFilters(value: unknown, allowIdle: boolean): NormalizedSourceFilters {
  const fields = allowIdle ? ['q', 'scope', 'handling', 'idle'] : ['q', 'scope', 'handling'];
  const root = record(
    value,
    fields,
    allowIdle ? 'source key page filters' : 'source page filters',
    [],
  );
  const scope =
    root.scope === undefined
      ? ''
      : oneOf(root.scope, ['active', 'all'] as const, 'source scope filter');
  const handling =
    root.handling === undefined
      ? ''
      : oneOf(root.handling, ['pending'] as const, 'source handling filter');
  const idle =
    !allowIdle || root.idle === undefined
      ? ''
      : oneOf(root.idle, ['yes', 'no'] as const, 'source idle filter');
  const q = root.q === undefined ? '' : queryText(root.q, 'source search');
  return { q, scope, handling, idle };
}

function sharedSource(value: unknown, label: string): DonationPageSafeSource {
  const source = normalizeManagedSource(value, label, 'steward');
  if (source.kind === 'custom') return source;
  return {
    kind: source.kind,
    connector_type: source.connector_type,
    base_url: source.base_url,
    channel_id: source.channel_id,
    name: source.name,
  };
}

function sourceIdentity(value: DonationPageSafeSource): string {
  return value.kind === 'custom'
    ? `custom\0${value.connector_type}\0${value.base_url}`
    : `mainstream\0${value.channel_id}`;
}

function pageHandling(value: unknown, label: string, status?: DonationStatus): DonationHandling {
  const root = record(value, HANDLING_FIELDS, label);
  if (
    root.state === 'closed' &&
    root.closed_reason === 'expired' &&
    root.closed_at === null &&
    root.processed_at === null &&
    root.processed_by_role === null
  ) {
    if (status !== undefined && status !== 'expired')
      invalidResponse(`${label} logical expiration`);
    return {
      state: 'closed',
      revision: decimal(root.revision, `${label} revision`, { positive: true }),
      processed_at: null,
      processed_by_role: null,
      closed_at: null,
      closed_reason: 'expired',
    };
  }
  return normalizeDonationHandling(value);
}

function pageReview(value: unknown, label: string): DonationPageReviewResult | null {
  if (value === null) return null;
  const root = record(value, ['decision', 'reason', 'reviewed_at'], label);
  return {
    decision: oneOf(root.decision, ['approve', 'reject'] as const, `${label} decision`),
    reason: string(root.reason, `${label} reason`, { max: 1_024, bytes: 4_096, multiline: true }),
    reviewed_at: unixSecond(root.reviewed_at, `${label} time`),
  };
}

function pageReviewer(value: unknown, label: string): DonationPageReviewer | null {
  if (value === null) return null;
  const root = record(value, ['user_id', 'role'], label);
  const role = oneOf(root.role, ['admin', 'steward', 'level5'] as const, `${label} role`);
  return {
    user_id: root.user_id === null ? null : decimalID(root.user_id, `${label} user id`),
    role: role === 'level5' ? 'steward' : role,
  };
}

function stateCounts(value: unknown, label: string, keyCount: string): DonationStateCounts {
  const root = record(value, DONATION_STATES, label);
  const result = {} as DonationStateCounts;
  let total = 0n;
  for (const state of DONATION_STATES) {
    const count = decimal(root[state], `${label} ${state}`);
    result[state] = count;
    total += BigInt(count);
  }
  if (total !== BigInt(keyCount)) invalidResponse(`${label} total`);
  return result;
}

function donationCommon(
  root: ReturnType<typeof record>,
  label: string,
  filters: Required<ManagedDonationPageFilters>,
): DonationPageCommon {
  const status = oneOf(
    root.status,
    ['pending', 'approved', 'rejected', 'deleted', 'expired'] as const,
    `${label} status`,
  );
  if (filters.status !== '' && status !== filters.status) invalidResponse(`${label} status filter`);
  const review = pageReview(root.review_result, `${label} review`);
  const reviewer = pageReviewer(root.reviewer, `${label} reviewer`);
  if (
    reviewer === null &&
    review !== null &&
    (review.decision !== 'approve' || review.reason !== '')
  ) {
    invalidResponse(`${label} automatic review`);
  }
  if (reviewer !== null && review === null) invalidResponse(`${label} attributed review`);
  if (status === 'pending' && review !== null) invalidResponse(`${label} pending review`);
  if (status === 'approved' && review?.decision !== 'approve') {
    invalidResponse(`${label} approved review`);
  }
  if ((status === 'expired' || status === 'deleted') && review?.decision === 'reject') {
    invalidResponse(`${label} terminal review`);
  }
  if (status === 'rejected' && review?.decision !== 'reject') {
    invalidResponse(`${label} rejected review`);
  }
  const keyCount = decimal(root.key_count, `${label} key count`);
  const handling = pageHandling(root.handling, `${label} handling`, status);
  if (filters.handling !== '' && handling.state !== filters.handling) {
    invalidResponse(`${label} handling filter`);
  }
  const sourceCount = decimal(root.source_count, `${label} source count`);
  const sources = array(root.sources, `${label} sources`, 3).map((entry, index) =>
    sharedSource(entry, `${label} source ${index + 1}`),
  );
  const expectedSources = BigInt(sourceCount) < 3n ? Number(sourceCount) : 3;
  if (sources.length !== expectedSources) invalidResponse(`${label} source count`);
  if (new Set(sources.map(sourceIdentity)).size !== sources.length) {
    invalidResponse(`${label} source identities`);
  }
  return {
    id: decimalID(root.id, `${label} id`),
    status,
    revision: decimal(root.revision, `${label} revision`, { positive: true }),
    description: string(root.description, `${label} description`, {
      max: 1_024,
      bytes: 4_096,
      multiline: true,
    }),
    review_result: review,
    created_at: unixSecond(root.created_at, `${label} creation time`),
    updated_at: unixSecond(root.updated_at, `${label} update time`),
    key_count: keyCount,
    state_counts: stateCounts(root.state_counts, `${label} state counts`, keyCount),
    source_count: sourceCount,
    sources,
    handling,
    reviewer,
  };
}

function adminOwner(value: unknown, label: string): AdminDonationPageOwner | null {
  if (value === null) return null;
  const root = record(value, ['user_id', 'discord_id', 'display_name'], label);
  return {
    user_id: decimalID(root.user_id, `${label} id`),
    discord_id: nullableString(root.discord_id, `${label} Discord id`, {
      max: 128,
      bytes: 128,
      ascii: true,
    }),
    display_name: string(root.display_name, `${label} display`, { min: 1, max: 128, bytes: 512 }),
  };
}

function stewardOwner(value: unknown, label: string): StewardDonationPageOwner | null {
  if (value === null) return null;
  const root = record(value, ['user_id', 'discord_id', 'display_name'], label);
  return {
    user_id: decimalID(root.user_id, `${label} id`),
    discord_id: nullableString(root.discord_id, `${label} Discord id`, {
      max: 128,
      bytes: 128,
      ascii: true,
    }),
    display_name: string(root.display_name, `${label} display`, { min: 1, max: 128, bytes: 512 }),
  };
}

function normalizeAdminDonationPageItem(
  value: unknown,
  index: number,
  filters: Required<ManagedDonationPageFilters>,
): AdminDonationPageItem {
  const label = `administrator donation ${index + 1}`;
  const root = record(value, [...DONATION_COMMON_FIELDS, 'owner'], label);
  return {
    ...donationCommon(root, label, filters),
    owner: adminOwner(root.owner, `${label} owner`),
  };
}

function normalizeStewardDonationPageItem(
  value: unknown,
  index: number,
  filters: Required<ManagedDonationPageFilters>,
): StewardDonationPageItem {
  const label = `steward donation ${index + 1}`;
  const root = record(value, [...DONATION_COMMON_FIELDS, 'owner'], label);
  return {
    ...donationCommon(root, label, filters),
    owner: stewardOwner(root.owner, `${label} owner`),
  };
}

function unique<T>(values: T[], label: string): void {
  if (new Set(values).size !== values.length) invalidResponse(label);
}

function normalizeKeySummary(value: unknown, index: number): ManagedDonationKeySummary {
  const label = `managed donation key ${index + 1}`;
  const root = record(value, KEY_SUMMARY_FIELDS, label);
  const key = normalizeManagedKey(
    {
      binding_count: root.binding_count,
      failure_disable_threshold: root.failure_disable_threshold,
      idle: root.idle,
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
      authorized_expires_at: root.authorized_expires_at,
      safe_note: root.safe_note,
      max_concurrency: root.max_concurrency,
      max_rpm: root.max_rpm,
    },
    `${label} base`,
    'steward',
  );
  const donationID = decimalID(root.donation_id, `${label} donation id`);
  const keyID = decimalID(root.key_id, `${label} key id`);
  if (key.id !== keyID) invalidResponse(`${label} id mismatch`);
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
  unique(
    rules.map((rule) => rule.id),
    `${label} rule ids`,
  );
  if (rules.some((rule) => rule.id === null)) invalidResponse(`${label} rule ids`);
  let sawNonLimited = false;
  for (const rule of rules) {
    if (rule.state === 'limited' && sawNonLimited) invalidResponse(`${label} rule order`);
    if (rule.state !== 'limited') sawNonLimited = true;
  }
  return {
    ...key,
    donation_id: donationID,
    key_id: keyID,
    donation_revision: donationRevision,
    rule_count: ruleCount,
    rules,
    handling: pageHandling(root.handling, `${label} handling`),
  };
}

function canonicalSourceKey(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.length !== 47 || !value.startsWith('dsg_')) {
    invalidResponse(label);
  }
  const raw = value.slice(4);
  if (!/^[A-Za-z0-9_-]{43}$/.test(raw)) invalidResponse(label);
  try {
    const binary = globalThis.atob(`${raw.replaceAll('-', '+').replaceAll('_', '/')}=`);
    if (binary.length !== 32) invalidResponse(label);
    const canonical = globalThis
      .btoa(binary)
      .replaceAll('+', '-')
      .replaceAll('/', '_')
      .replace(/=+$/, '');
    if (canonical !== raw) invalidResponse(label);
  } catch {
    invalidResponse(label);
  }
  return value;
}

function normalizeDonationSource(value: unknown, index: number): DonationSourceSummary {
  const label = `donation source ${index + 1}`;
  const root = record(
    value,
    [
      'source_key',
      'safe_source',
      'donation_count',
      'key_count',
      'usable_key_count',
      'pending_donation_count',
    ],
    label,
  );
  const donationCount = decimal(root.donation_count, `${label} donation count`);
  const keyCount = decimal(root.key_count, `${label} key count`);
  const usableKeyCount = decimal(root.usable_key_count, `${label} usable key count`);
  const pendingDonationCount = decimal(
    root.pending_donation_count,
    `${label} pending donation count`,
  );
  if (
    BigInt(donationCount) > BigInt(keyCount) ||
    BigInt(pendingDonationCount) > BigInt(donationCount) ||
    BigInt(usableKeyCount) > BigInt(keyCount)
  ) {
    invalidResponse(`${label} count relationships`);
  }
  return {
    source_key: canonicalSourceKey(root.source_key, `${label} key`),
    safe_source: sharedSource(root.safe_source, `${label} source`),
    donation_count: donationCount,
    key_count: keyCount,
    usable_key_count: usableKeyCount,
    pending_donation_count: pendingDonationCount,
  };
}

function normalizePage<T>(
  value: unknown,
  label: string,
  page: string,
  pageSize: PageSize,
  item: (value: unknown, index: number) => T,
): DonationPageResult<T> {
  const root = record(value, ['data', 'next_cursor', 'pagination'], label);
  if (root.next_cursor !== null) invalidResponse(`${label} cursor`);
  const metadata = normalizePageMetadata(root.pagination);
  const data = array(root.data, `${label} data`, 100).map(item);
  validatePageResponse(metadata, page, pageSize, data.length);
  return { data, next_cursor: null, pagination: metadata };
}

export function normalizeManagedDonationsPage(
  value: unknown,
  role: DonationPageRole,
  filters: ManagedDonationPageFilters,
  page: string,
  pageSize: PageSize,
): DonationPageResult<AdminDonationPageItem | StewardDonationPageItem> {
  basePath(role);
  pageArguments(page, pageSize);
  const normalizedRole = role;
  const normalizedFilters = donationFilters(filters);
  const result = normalizePage(
    value,
    `${normalizedRole} donation page`,
    page,
    pageSize,
    (entry, index) =>
      normalizedRole === 'admin'
        ? normalizeAdminDonationPageItem(entry, index, normalizedFilters)
        : normalizeStewardDonationPageItem(entry, index, normalizedFilters),
  );
  unique(
    result.data.map((item) => item.id),
    `${normalizedRole} donation ids`,
  );
  return result;
}

export function normalizeManagedDonationKeysPage(
  value: unknown,
  donationID: string,
  page: string,
  pageSize: PageSize,
): DonationPageResult<ManagedDonationKeySummary> {
  pageArguments(page, pageSize);
  const expectedDonationID = decimalID(donationID, 'donation page id');
  const result = normalizePage(
    value,
    'managed donation key page',
    page,
    pageSize,
    normalizeKeySummary,
  );
  if (result.data.some((item) => item.donation_id !== expectedDonationID)) {
    invalidResponse('managed donation key donation id');
  }
  unique(
    result.data.map((item) => item.key_id),
    'managed donation key ids',
  );
  return result;
}

export function normalizeDonationSourcesPage(
  value: unknown,
  role: DonationPageRole,
  filters: DonationSourcePageFilters,
  page: string,
  pageSize: PageSize,
): DonationPageResult<DonationSourceSummary> {
  basePath(role);
  pageArguments(page, pageSize);
  const normalizedRole = role;
  sourceFilters(filters, false);
  const result = normalizePage(
    value,
    `${normalizedRole} donation source page`,
    page,
    pageSize,
    normalizeDonationSource,
  );
  unique(
    result.data.map((item) => item.source_key),
    `${normalizedRole} source keys`,
  );
  return result;
}

export function normalizeDonationSourceKeysPage(
  value: unknown,
  sourceKey: string,
  filters: DonationSourceKeysPageFilters,
  page: string,
  pageSize: PageSize,
): DonationPageResult<ManagedDonationKeySummary> {
  pageArguments(page, pageSize);
  canonicalSourceKey(sourceKey, 'source page key');
  const normalizedFilters = sourceFilters(filters, true);
  const result = normalizePage(
    value,
    'donation source key page',
    page,
    pageSize,
    normalizeKeySummary,
  );
  unique(
    result.data.map((item) => item.key_id),
    'donation source key ids',
  );
  if (
    result.data.some(
      (item) =>
        (normalizedFilters.idle === 'yes' && !item.idle) ||
        (normalizedFilters.idle === 'no' && item.idle) ||
        (normalizedFilters.handling === 'pending' && item.handling.state !== 'pending'),
    )
  ) {
    invalidResponse('donation source key filters');
  }
  return result;
}

export function getManagedDonationsPage(
  role: DonationPageRole,
  filters: ManagedDonationPageFilters,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<DonationPageResult<AdminDonationPageItem | StewardDonationPageItem>> {
  const root = basePath(role);
  const normalizedFilters = donationFilters(filters);
  pageArguments(page, pageSize);
  return decoded(
    queryPath(`${root}/donations`, {
      status: normalizedFilters.status || undefined,
      handling: normalizedFilters.handling || undefined,
      q: normalizedFilters.q || undefined,
      page,
      page_size: pageSize,
    }),
    (value) => normalizeManagedDonationsPage(value, role, normalizedFilters, page, pageSize),
    { signal },
  );
}

export function getManagedDonationKeysPage(
  role: DonationPageRole,
  donationID: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<DonationPageResult<ManagedDonationKeySummary>> {
  const root = basePath(role);
  const id = decimalID(donationID, 'donation id');
  pageArguments(page, pageSize);
  return decoded(
    queryPath(`${root}/donations/${encodeURIComponent(id)}/keys`, { page, page_size: pageSize }),
    (value) => normalizeManagedDonationKeysPage(value, id, page, pageSize),
    { signal },
  );
}

export function getDonationSourcesPage(
  role: DonationPageRole,
  filters: DonationSourcePageFilters,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<DonationPageResult<DonationSourceSummary>> {
  const root = basePath(role);
  const normalizedFilters = sourceFilters(filters, false);
  pageArguments(page, pageSize);
  return decoded(
    queryPath(`${root}/donation-sources`, {
      q: normalizedFilters.q || undefined,
      scope: normalizedFilters.scope || undefined,
      handling: normalizedFilters.handling || undefined,
      page,
      page_size: pageSize,
    }),
    (value) => normalizeDonationSourcesPage(value, role, filters, page, pageSize),
    { signal },
  );
}

export function getDonationSourceKeysPage(
  role: DonationPageRole,
  sourceKey: string,
  filters: DonationSourceKeysPageFilters,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<DonationPageResult<ManagedDonationKeySummary>> {
  const root = basePath(role);
  const normalizedSourceKey = canonicalSourceKey(sourceKey, 'source page key');
  const normalizedFilters = sourceFilters(filters, true);
  pageArguments(page, pageSize);
  return decoded(
    queryPath(`${root}/donation-sources/${encodeURIComponent(normalizedSourceKey)}/keys`, {
      q: normalizedFilters.q || undefined,
      scope: normalizedFilters.scope || undefined,
      handling: normalizedFilters.handling || undefined,
      idle: normalizedFilters.idle || undefined,
      page,
      page_size: pageSize,
    }),
    (value) => normalizeDonationSourceKeysPage(value, normalizedSourceKey, filters, page, pageSize),
    { signal },
  );
}
