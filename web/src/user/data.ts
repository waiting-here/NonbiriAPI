import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch } from '@shared/query/http';
import {
  asArray,
  asRecord,
  booleanValue,
  dateValue,
  idValue,
  hasControlCharacters,
  integerValue,
  isListPayload,
  listResult,
  optionalText,
  text,
  type UnknownRecord,
} from '@shared/query/normalize';

export interface UserSummary {
  id: string;
  username: string;
  avatar?: string;
  avatar_url?: string;
  guild_nick: string;
  guild_avatar_url?: string;
  lang: 'zh' | 'en';
  is_banned: boolean;
  blocked_reason?: string;
  endpoint_limit?: number;
  effective_endpoint_limit: number;
  rpm_limit?: number;
  effective_rpm_limit: number;
  concurrency_limit?: number;
  effective_concurrency_limit: number;
  /** Canonical decimal milli-credit string; the signed consumption balance. */
  credits: string;
  /** Canonical decimal milli-credit string; the non-negative donor reward. */
  donation_credit: string;
  /** Server-resolved effective level (1..5) for the request. */
  effective_level: number;
  /** Manual override when non-null; absent means the automatic level applies. */
  manual_level?: number;
  /** Nullable ban deadline as unix seconds; absent while there is none. */
  banned_until?: number;
  /** Nullable charity-only suspension deadline as unix seconds. */
  charity_suspended_until?: number;
  created_at: string;
}

/**
 * The check-in status projection. While the feature is unavailable the server
 * sends exactly `{enabled:false}` — never a reason — so the unavailable shape
 * carries nothing else either.
 */
export type CheckinStatus =
  | { enabled: false }
  | {
      enabled: true;
      checked_in_today: boolean;
      credits: string;
      award_min_milli: string;
      award_max_milli: string;
      credits_cap_milli: string;
    };

/** The committed outcome of one successful check-in (server-drawn award). */
export interface CheckinResult {
  award_milli: string;
  credits: string;
}

export interface UserSessionResponse {
  user: UserSummary;
}

export interface UsageSummary {
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_unknown_usage_requests: number;
}

export interface Endpoint {
  id: string;
  connector_type: string;
  base_url: string;
  note: string;
  enabled: boolean;
  model_fetch_failed: boolean;
  model_fetch_failed_at: string;
  created_at: string;
  updated_at: string;
}

export interface EndpointKey {
  id: string;
  /** Head/tail fragment only. The full secret is never part of this type. */
  display?: string;
  note: string;
  enabled: boolean;
  /** Experimental physical-key policy; writable only by the key owner. */
  force_store_false: boolean;
  created_at: string;
  updated_at: string;
}

export interface UpstreamModel {
  upstream_model_id: string;
  provider: string;
  fetched_at: string;
  status: string;
}

export interface PlatformModel {
  id: string;
  provider: string;
  model: string;
  full_name: string;
  route_strategy: 'ordered' | 'random';
  silent_retry: boolean;
  /** Experimental logical-model policy. */
  flatten_tool_calls: boolean;
  binding_count: number;
  created_at: string;
  updated_at: string;
}

export interface ModelBinding {
  id: string;
  endpoint_key_id: string;
  upstream_model_id: string;
  endpoint_base_url: string;
  /** Optional masked preview of the bound key's secret (head…tail). */
  endpoint_key_display?: string;
  endpoint_key_note: string;
  endpoint_note: string;
  ord: number;
}

export interface CallerKeyMetadata {
  display: string;
  created_at: string;
  updated_at: string;
}

export interface CallerKeySecret {
  secret: string;
}

export interface UserIssue {
  id: string;
  kind: string;
  message: string;
  ref: string;
  created_at: string;
  resolved: boolean;
  resolved_at?: string;
}

export interface IssuePage {
  items: UserIssue[];
  hasMore: boolean;
  nextCursor?: string;
}

/**
 * One metadata-only log row of the session principal's own requests (frozen
 * projection). key_note / endpoint_note are the current notes of the
 * caller's own resources and are empty once a resource is deleted;
 * endpoint_base_url is the durable dispatch-time snapshot. The normalizer
 * whitelists exactly these fields.
 */
export interface UserRequestLog {
  id: string;
  route_kind: string;
  model: string;
  endpoint_key_id: string;
  key_note: string;
  endpoint_note: string;
  endpoint_base_url: string;
  upstream_model_id: string;
  status_code: number;
  duration_ms: number;
  /** Unix seconds. */
  started_at: number;
  /** Unix seconds. */
  completed_at: number;
  uncached_input_tokens: number;
  cache_write_input_tokens: number;
  cache_read_input_tokens: number;
  output_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  usage_unknown: boolean;
  error_code: string;
  error_source: string;
  error_diag: string;
  attempt_id: string;
}

export interface UserLogFilter {
  model?: string;
  errorCode?: string;
  status?: string;
  fromUnix?: number;
  toUnix?: number;
}

export interface UserLogPage {
  items: UserRequestLog[];
  hasMore: boolean;
}

export interface UserLogOptions {
  models: string[];
}

/** Level-5 steward projection. It intentionally has no platform model/name,
 * note, Discord, or administrator fields. Charity resource fields are empty
 * in the server projection and remain masked by the page as a second guard. */
export interface StewardRequestLog {
  id: string;
  user_id: string;
  route_kind: string;
  endpoint_base_url: string;
  endpoint_key_id: string;
  upstream_model_id: string;
  status_code: number;
  duration_ms: number;
  started_at: number;
  completed_at: number;
  uncached_input_tokens: number;
  cache_write_input_tokens: number;
  cache_read_input_tokens: number;
  output_tokens: number;
  usage_unknown: boolean;
  error_code: string;
  error_source: string;
  error_diag: string;
  attempt_id: string;
}

export interface StewardLogFilter {
  userId?: string;
  endpointBaseURL?: string;
  upstreamModel?: string;
  errorCode?: string;
  status?: string;
  fromUnix?: number;
  toUnix?: number;
}

export interface StewardLogPage {
  items: StewardRequestLog[];
  hasNext: boolean;
}

export const userKeys = {
  all: ['user'] as const,
  session: ['user', 'session'] as const,
  me: ['user', 'me'] as const,
  usage: ['user', 'usage'] as const,
  endpoints: ['user', 'endpoints'] as const,
  endpointKeysRoot: ['user', 'endpoint-keys'] as const,
  endpointKeys: (endpointId: string) => ['user', 'endpoint-keys', endpointId] as const,
  keyModelsRoot: ['user', 'key-models'] as const,
  keyModels: (endpointId: string, keyId: string) =>
    ['user', 'key-models', endpointId, keyId] as const,
  models: ['user', 'models'] as const,
  bindingsRoot: ['user', 'bindings'] as const,
  bindings: (modelId: string) => ['user', 'bindings', modelId] as const,
  callerKey: ['user', 'caller-key'] as const,
  issues: ['user', 'issues'] as const,
  issueResolve: (issueId: string) => ['user', 'issues', 'resolve', issueId] as const,
  logs: (page: number, filter: UserLogFilter, pageSize: number) =>
    [
      'user',
      'logs',
      page,
      filter.model ?? '',
      filter.errorCode ?? '',
      filter.status ?? '',
      filter.fromUnix ?? '',
      filter.toUnix ?? '',
      pageSize,
    ] as const,
  logsRoot: ['user', 'logs'] as const,
  logOptions: ['user', 'log-options'] as const,
  stewardLogs: (page: number, filter: StewardLogFilter, pageSize: number) =>
    ['user', 'steward-logs', page, filter.userId ?? '', filter.endpointBaseURL ?? '', filter.upstreamModel ?? '', filter.errorCode ?? '', filter.status ?? '', filter.fromUnix ?? '', filter.toUnix ?? '', pageSize] as const,
  stewardLogsRoot: ['user', 'steward-logs'] as const,
  checkin: ['user', 'checkin'] as const,
  charityModels: ['user', 'charity-models'] as const,
  donations: ['user', 'donations'] as const,
  donation: (id: string) => ['user', 'donations', id] as const,
};

function recordValue(record: UnknownRecord, key: string): unknown {
  return record[key];
}

// Economy amounts are canonical decimal milli-credit strings. The whitelist
// keeps the wire string untouched (never Number()); a malformed value stays ''
// and the shared formatter degrades it to a placeholder instead of guessing.
function amountValue(value: unknown): string {
  return typeof value === 'string' && value.length <= 32 ? value : '';
}

// Nullable integer projection: present only for a finite number.
function optionalNumberValue(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function strictInteger(value: unknown, minimum: number, maximum: number, field: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  return value as number;
}

// Additive experimental policy fields may be absent while the preceding backend
// wave is integrated, but once present they are strict JSON booleans.
function additivePolicyBoolean(value: unknown, field: string): boolean {
  if (value === undefined) return false;
  if (typeof value !== 'boolean') {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  return value;
}

function optionalStrictInteger(
  value: unknown,
  minimum: number,
  maximum: number,
  field: string,
): number | undefined {
  if (value === null || value === undefined) return undefined;
  return strictInteger(value, minimum, maximum, field);
}

function levelValue(value: unknown, fallback: number): number {
  const parsed = optionalNumberValue(value);
  return parsed !== undefined && parsed >= 1 && parsed <= 5 ? Math.trunc(parsed) : fallback;
}

export function normalizeUserSummary(value: unknown): UserSummary {
  const record = asRecord(value) ?? {};
  const lang = recordValue(record, 'lang') === 'zh' ? 'zh' : 'en';
  const endpointLimit = recordValue(record, 'endpoint_limit');
  const rpmLimit = recordValue(record, 'rpm_limit');
  const concurrencyLimit = recordValue(record, 'concurrency_limit');
  const manualLevel = optionalNumberValue(recordValue(record, 'manual_level'));
  const bannedUntil = optionalNumberValue(recordValue(record, 'banned_until'));
  const charitySuspendedUntil = optionalNumberValue(recordValue(record, 'charity_suspended_until'));
  const explicitEndpointLimit = optionalStrictInteger(endpointLimit, 0, 10_000, 'endpoint limit');
  const explicitRPMLimit = optionalStrictInteger(rpmLimit, 1, 4_096, 'RPM limit');
  const explicitConcurrencyLimit = optionalStrictInteger(
    concurrencyLimit, 1, 100_000, 'concurrency limit',
  );
  return {
    id: idValue(recordValue(record, 'id')),
    username: text(recordValue(record, 'username'), 128, '—'),
    ...(optionalText(recordValue(record, 'avatar'), 512)
      ? { avatar: optionalText(recordValue(record, 'avatar'), 512) }
      : {}),
    ...(optionalText(recordValue(record, 'avatar_url'), 512)
      ? { avatar_url: optionalText(recordValue(record, 'avatar_url'), 512) }
      : {}),
    guild_nick: text(recordValue(record, 'guild_nick'), 128),
    ...(optionalText(recordValue(record, 'guild_avatar_url'), 512)
      ? { guild_avatar_url: optionalText(recordValue(record, 'guild_avatar_url'), 512) }
      : {}),
    lang,
    is_banned: booleanValue(recordValue(record, 'is_banned')),
    ...(optionalText(recordValue(record, 'blocked_reason'), 512)
      ? { blocked_reason: optionalText(recordValue(record, 'blocked_reason'), 512) }
      : {}),
    ...(explicitEndpointLimit !== undefined
      ? { endpoint_limit: explicitEndpointLimit }
      : {}),
    effective_endpoint_limit: strictInteger(
      recordValue(record, 'effective_endpoint_limit'), 0, 10_000, 'effective endpoint limit',
    ),
    ...(explicitRPMLimit !== undefined
      ? { rpm_limit: explicitRPMLimit }
      : {}),
    effective_rpm_limit: strictInteger(
      recordValue(record, 'effective_rpm_limit'), 1, 4_096, 'effective RPM limit',
    ),
    ...(explicitConcurrencyLimit !== undefined
      ? { concurrency_limit: explicitConcurrencyLimit }
      : {}),
    effective_concurrency_limit: strictInteger(
      recordValue(record, 'effective_concurrency_limit'), 1, 100_000, 'effective concurrency limit',
    ),
    credits: amountValue(recordValue(record, 'credits')),
    donation_credit: amountValue(recordValue(record, 'donation_credit')),
    effective_level: levelValue(recordValue(record, 'effective_level'), 1),
    ...(manualLevel !== undefined && manualLevel >= 1 && manualLevel <= 5
      ? { manual_level: Math.trunc(manualLevel) }
      : {}),
    ...(bannedUntil !== undefined && bannedUntil >= 0 ? { banned_until: Math.trunc(bannedUntil) } : {}),
    ...(charitySuspendedUntil !== undefined && charitySuspendedUntil >= 0
      ? { charity_suspended_until: Math.trunc(charitySuspendedUntil) }
      : {}),
    created_at: dateValue(recordValue(record, 'created_at')),
  };
}

function normalizeSession(value: unknown): UserSessionResponse {
  const record = asRecord(value);
  if (!record || !asRecord(record.user)) {
    throw new ApiError('invalid_response', 'The server returned an invalid session.', 200);
  }
  return { user: normalizeUserSummary(record.user) };
}

function normalizeUsage(value: unknown): UsageSummary {
  const record = asRecord(value);
  if (!record) throw new ApiError('invalid_response', 'The server returned an invalid usage summary.', 200);
  const required = (key: string): number => {
    const raw = recordValue(record, key);
    if (typeof raw !== 'number' || !Number.isFinite(raw)) {
      throw new ApiError('invalid_response', 'The server returned an invalid usage summary.', 200);
    }
    return Math.max(0, Math.trunc(raw));
  };
  return {
    total_requests: required('total_requests'),
    total_prompt_tokens: required('total_prompt_tokens'),
    total_completion_tokens: required('total_completion_tokens'),
    total_unknown_usage_requests: required('total_unknown_usage_requests'),
  };
}

function listPayload(value: unknown): unknown[] {
  if (!isListPayload(value)) {
    throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
  }
  return listResult(value, 1000).items;
}

function normalizeEndpoint(value: unknown): Endpoint {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    connector_type: text(recordValue(record, 'connector_type'), 96, 'openai-compatible'),
    base_url: text(recordValue(record, 'base_url'), 2048, '—'),
    note: text(recordValue(record, 'note'), 512),
    enabled: booleanValue(recordValue(record, 'enabled'), true),
    model_fetch_failed: booleanValue(recordValue(record, 'model_fetch_failed')),
    model_fetch_failed_at: dateValue(recordValue(record, 'model_fetch_failed_at')),
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function safeDisplayFragment(value: unknown, max = 64): string | undefined {
  const candidate = optionalText(value, max);
  return candidate && (candidate.includes('…') || candidate.includes('...')) ? candidate : undefined;
}

// Build the masked key preview (head…tail) from persisted first/last fragments.
// Returns undefined when neither fragment is present (e.g. very short secrets
// where both fragments are suppressed to avoid re-covering the whole secret).
function formatFragment(head: string | undefined, tail: string | undefined): string | undefined {
  if (head && tail) return `${head}…${tail}`;
  if (head) return `${head}…`;
  if (tail) return `…${tail}`;
  return undefined;
}

function fragmentFromRecord(record: UnknownRecord): string | undefined {
  const directDisplay = safeDisplayFragment(recordValue(record, 'display'));
  if (directDisplay) return directDisplay;
  const head = optionalText(recordValue(record, 'display_head'), 4) ?? optionalText(recordValue(record, 'secret_head'), 4);
  const tail = optionalText(recordValue(record, 'display_tail'), 4) ?? optionalText(recordValue(record, 'secret_tail'), 4);
  return formatFragment(head, tail);
}

export function normalizeEndpointKey(value: unknown): EndpointKey {
  const record = asRecord(value) ?? {};
  const fragment = fragmentFromRecord(record);
  return {
    id: idValue(recordValue(record, 'id')),
    ...(fragment ? { display: fragment } : {}),
    note: text(recordValue(record, 'note'), 512),
    enabled: booleanValue(recordValue(record, 'enabled'), true),
    force_store_false: additivePolicyBoolean(
      recordValue(record, 'force_store_false'), 'store policy',
    ),
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function normalizeUpstreamModel(value: unknown): UpstreamModel {
  const record = asRecord(value) ?? {};
  return {
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256, '—'),
    provider: text(recordValue(record, 'provider'), 128, '—'),
    fetched_at: dateValue(recordValue(record, 'fetched_at')),
    status: text(recordValue(record, 'status'), 64, 'unknown'),
  };
}

export function normalizePlatformModel(value: unknown): PlatformModel {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    provider: text(recordValue(record, 'provider'), 64, '—'),
    model: text(recordValue(record, 'model'), 64, '—'),
    full_name: text(recordValue(record, 'full_name'), 160, '—'),
    route_strategy: recordValue(record, 'route_strategy') === 'random' ? 'random' : 'ordered',
    silent_retry: booleanValue(recordValue(record, 'silent_retry')),
    flatten_tool_calls: additivePolicyBoolean(
      recordValue(record, 'flatten_tool_calls'), 'tool-call policy',
    ),
    binding_count: Math.max(0, integerValue(recordValue(record, 'binding_count'))),
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function normalizeBinding(value: unknown): ModelBinding {
  const record = asRecord(value) ?? {};
  const keyHead = optionalText(recordValue(record, 'endpoint_key_display_head'), 4);
  const keyTail = optionalText(recordValue(record, 'endpoint_key_display_tail'), 4);
  const keyDisplay = formatFragment(keyHead, keyTail);
  return {
    id: idValue(recordValue(record, 'id')),
    endpoint_key_id: idValue(recordValue(record, 'endpoint_key_id')),
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256, '—'),
    endpoint_base_url: text(recordValue(record, 'endpoint_base_url'), 2048, '—'),
    ...(keyDisplay ? { endpoint_key_display: keyDisplay } : {}),
    endpoint_key_note: text(recordValue(record, 'endpoint_key_note'), 512),
    endpoint_note: text(recordValue(record, 'endpoint_note'), 512),
    ord: Math.max(0, integerValue(recordValue(record, 'ord'))),
  };
}

/**
 * Normalize a binding list payload. The reorder endpoint returns the same
 * array shape as the list endpoint, so both paths share this normalizer.
 */
export function normalizeBindingList(value: unknown): ModelBinding[] {
  return listPayload(value).map(normalizeBinding);
}

function normalizeCallerKey(value: unknown): CallerKeyMetadata {
  const record = asRecord(value) ?? {};
  return {
    display: fragmentFromRecord(record) ?? '—',
    created_at: dateValue(recordValue(record, 'created_at')),
    updated_at: dateValue(recordValue(record, 'updated_at')),
  };
}

function normalizeUserIssue(value: unknown): UserIssue {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    kind: text(recordValue(record, 'kind'), 128, '—'),
    message: text(recordValue(record, 'message'), 1024),
    ref: text(recordValue(record, 'ref'), 256),
    created_at: dateValue(recordValue(record, 'created_at')),
    resolved: booleanValue(recordValue(record, 'resolved')),
    ...(optionalText(recordValue(record, 'resolved_at'), 64)
      ? { resolved_at: dateValue(recordValue(record, 'resolved_at')) }
      : {}),
  };
}

/**
 * Normalize one paginated issue page `{data, has_more}`. The browser never
 * invents issues: an invalid envelope throws so a broken server cannot fake
 * a local issue list.
 */
function normalizeIssuePage(value: unknown, pageSize = 20): IssuePage {
  if (!isListPayload(value)) {
    throw new ApiError('invalid_response', 'The server returned an invalid issue list.', 200);
  }
  const record = asRecord(value);
  const result = listResult(value, pageSize);
  const hasMore =
    typeof record?.has_more === 'boolean' ? record.has_more : result.hasNext;
  const items = result.items.map(normalizeUserIssue);
  const last = items[items.length - 1];
  return {
    items,
    hasMore,
    ...(hasMore && last && last.id !== '—' ? { nextCursor: last.id } : {}),
  };
}

function unixSecondsValue(value: unknown): number {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    return Math.trunc(value);
  }
  return 0;
}

function normalizeUserLog(payload: unknown): UserRequestLog {
  const record = asRecord(payload) ?? {};
  const bucket = (key: string): number => {
    const raw = recordValue(record, key);
    return typeof raw === 'number' && Number.isSafeInteger(raw) && raw >= 0 ? raw : 0;
  };
  return {
    id: idValue(recordValue(record, 'id')),
    route_kind: text(recordValue(record, 'route_kind'), 64, '—'),
    model: text(recordValue(record, 'model'), 256, '—'),
    endpoint_key_id: idValue(recordValue(record, 'endpoint_key_id')),
    key_note: text(recordValue(record, 'key_note'), 512),
    endpoint_note: text(recordValue(record, 'endpoint_note'), 512),
    endpoint_base_url: text(recordValue(record, 'endpoint_base_url'), 2048, '—'),
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256, '—'),
    status_code: Math.max(0, integerValue(recordValue(record, 'status_code'))),
    duration_ms: Math.max(0, integerValue(recordValue(record, 'duration_ms'))),
    started_at: unixSecondsValue(recordValue(record, 'started_at')),
    completed_at: unixSecondsValue(recordValue(record, 'completed_at')),
    uncached_input_tokens: bucket('uncached_input_tokens'),
    cache_write_input_tokens: bucket('cache_write_input_tokens'),
    cache_read_input_tokens: bucket('cache_read_input_tokens'),
    output_tokens: bucket('output_tokens'),
    prompt_tokens: bucket('prompt_tokens'),
    completion_tokens: bucket('completion_tokens'),
    total_tokens: bucket('total_tokens'),
    usage_unknown: booleanValue(recordValue(record, 'usage_unknown')),
    error_code: text(recordValue(record, 'error_code'), 96),
    error_source: text(recordValue(record, 'error_source'), 32),
    error_diag: text(recordValue(record, 'error_diag'), 4096),
    attempt_id: text(recordValue(record, 'attempt_id'), 128),
  };
}

/**
 * Normalize one paginated log page `{data, has_more}`. Ownership is enforced
 * server-side; the browser only renders what the session principal owns.
 */
function normalizeUserLogPage(value: unknown): UserLogPage {
  if (!isListPayload(value)) {
    throw new ApiError('invalid_response', 'The server returned an invalid log list.', 200);
  }
  const record = asRecord(value);
  const hasMore = typeof record?.has_more === 'boolean' ? record.has_more : false;
  return { items: listResult(value).items.map(normalizeUserLog), hasMore };
}

function normalizeStewardLog(payload: unknown): StewardRequestLog {
  const record = asRecord(payload) ?? {};
  const bucket = (key: string): number => {
    const raw = recordValue(record, key);
    return typeof raw === 'number' && Number.isSafeInteger(raw) && raw >= 0 ? raw : 0;
  };
  return {
    id: idValue(recordValue(record, 'id')),
    user_id: idValue(recordValue(record, 'user_id')),
    route_kind: text(recordValue(record, 'route_kind'), 64, '—'),
    endpoint_base_url: text(recordValue(record, 'endpoint_base_url'), 2048),
    endpoint_key_id: idValue(recordValue(record, 'endpoint_key_id')),
    upstream_model_id: text(recordValue(record, 'upstream_model_id'), 256),
    status_code: Math.max(0, integerValue(recordValue(record, 'status_code'))),
    duration_ms: Math.max(0, integerValue(recordValue(record, 'duration_ms'))),
    started_at: unixSecondsValue(recordValue(record, 'started_at')),
    completed_at: unixSecondsValue(recordValue(record, 'completed_at')),
    uncached_input_tokens: bucket('uncached_input_tokens'),
    cache_write_input_tokens: bucket('cache_write_input_tokens'),
    cache_read_input_tokens: bucket('cache_read_input_tokens'),
    output_tokens: bucket('output_tokens'),
    usage_unknown: booleanValue(recordValue(record, 'usage_unknown')),
    error_code: text(recordValue(record, 'error_code'), 96),
    error_source: text(recordValue(record, 'error_source'), 32),
    error_diag: text(recordValue(record, 'error_diag'), 4096),
    attempt_id: text(recordValue(record, 'attempt_id'), 128),
  };
}

function normalizeStewardLogPage(value: unknown): StewardLogPage {
  if (!isListPayload(value)) {
    throw new ApiError('invalid_response', 'The server returned an invalid steward log list.', 200);
  }
  const record = asRecord(value);
  const result = listResult(value);
  return {
    items: result.items.map(normalizeStewardLog),
    hasNext: typeof record?.has_more === 'boolean' ? record.has_more : result.hasNext,
  };
}

/** Validate the one-time response without ever normalizing it into a query. */
export function normalizeSecret(value: unknown): CallerKeySecret {
  const record = asRecord(value);
  const rawSecret = record?.secret;
  if (typeof rawSecret !== 'string') {
    throw new ApiError('invalid_response', 'The server returned an invalid caller key.', 200);
  }
  const secret = rawSecret;
  if (secret !== secret.trim() || !secret.startsWith('nbk_') || secret.length < 8 || secret.length > 512) {
    throw new ApiError('invalid_response', 'The server returned an invalid caller key.', 200);
  }
  if (hasControlCharacters(secret)) {
    throw new ApiError('invalid_response', 'The server returned an invalid caller key.', 200);
  }
  return { secret };
}

export function useUserSession() {
  return useQuery({
    queryKey: userKeys.session,
    queryFn: async () => normalizeSession(await apiFetch<unknown>('/api/session')),
    staleTime: 15_000,
    retry: false,
  });
}

export function useUserMe(enabled = true) {
  return useQuery({
    queryKey: userKeys.me,
    queryFn: async () => {
      const payload = asRecord(await apiFetch<unknown>('/api/me'));
      return normalizeUserSummary(payload?.user ?? payload);
    },
    enabled,
  });
}

/**
 * Session-only self-service profile update (lang only). Per-user RPM
 * limits are administrator-set; the browser never sends rpm_limit.
 * endpoint_limit / admin / ban fields are rejected by the server.
 */
export function useUpdateUserProfile() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (values: { lang?: 'zh' | 'en' }) =>
      apiFetch<unknown>('/api/me', { method: 'PATCH', json: values }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: userKeys.me });
    },
  });
}

export function useUserUsage(enabled = true) {
  return useQuery({
    queryKey: userKeys.usage,
    queryFn: async () => normalizeUsage(await apiFetch<unknown>('/api/me/usage')),
    enabled,
  });
}

export function useEndpoints(enabled = true) {
  return useQuery({
    queryKey: userKeys.endpoints,
    queryFn: async () => listPayload(await apiFetch<unknown>('/api/endpoints')).map(normalizeEndpoint),
    enabled,
  });
}

export function useEndpointKeys(endpointId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: endpointId ? userKeys.endpointKeys(endpointId) : ['user', 'endpoint-keys', 'none'],
    queryFn: async () => {
      if (!endpointId) return [];
      return listPayload(
        await apiFetch<unknown>(`/api/endpoints/${encodeURIComponent(endpointId)}/keys`),
      ).map(normalizeEndpointKey);
    },
    enabled: enabled && Boolean(endpointId),
  });
}

export function useKeyModels(
  endpointId: string | undefined,
  keyId: string | undefined,
  enabled = true,
) {
  return useQuery({
    queryKey:
      endpointId && keyId
        ? userKeys.keyModels(endpointId, keyId)
        : ['user', 'key-models', 'none', 'none'],
    queryFn: async () => {
      if (!endpointId || !keyId) return [];
      return listPayload(
        await apiFetch<unknown>(
          `/api/endpoints/${encodeURIComponent(endpointId)}/keys/${encodeURIComponent(keyId)}/models`,
        ),
      ).map(normalizeUpstreamModel);
    },
    enabled: enabled && Boolean(endpointId && keyId),
  });
}

export function usePlatformModels(enabled = true) {
  return useQuery({
    queryKey: userKeys.models,
    queryFn: async () => listPayload(await apiFetch<unknown>('/api/models')).map(normalizePlatformModel),
    enabled,
  });
}

export function useModelBindings(modelId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: modelId ? userKeys.bindings(modelId) : ['user', 'bindings', 'none'],
    queryFn: async () => {
      if (!modelId) return [];
      return normalizeBindingList(
        await apiFetch<unknown>(`/api/models/${encodeURIComponent(modelId)}/bindings`),
      );
    },
    enabled: enabled && Boolean(modelId),
  });
}

export interface UpdateModelInput {
  modelId: string;
  provider?: string;
  model?: string;
  route_strategy?: 'ordered' | 'random';
  silent_retry?: boolean;
}

/**
 * Patch one of the caller's platform models. Only supplied fields change;
 * changing provider/model recomputes full_name server-side and a colliding
 * name comes back as conflict. The response is normalized so callers can
 * read the authoritative updated row.
 */
export function useUpdateModel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ modelId, ...patch }: UpdateModelInput) => {
      const payload = await apiFetch<unknown>(
        `/api/models/${encodeURIComponent(modelId)}`,
        { method: 'PATCH', json: patch },
      );
      if (!asRecord(payload)) {
        throw new ApiError('invalid_response', 'The server returned an invalid model.', 200);
      }
      return normalizePlatformModel(payload);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: userKeys.models });
    },
  });
}

export interface UpdateBindingInput {
  modelId: string;
  bindingId: string;
  ord?: number;
  upstream_model_id?: string;
}

/**
 * Patch one existing binding. Only ord/upstream_model_id are mutable through
 * this path (the bound endpoint key never changes); the server validates the
 * resulting upstream id against the key's fetched cache and returns the
 * enriched binding row.
 */
export function useUpdateBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ modelId, bindingId, ...patch }: UpdateBindingInput) => {
      const payload = await apiFetch<unknown>(
        `/api/models/${encodeURIComponent(modelId)}/bindings/${encodeURIComponent(bindingId)}`,
        { method: 'PATCH', json: patch },
      );
      if (!asRecord(payload)) {
        throw new ApiError('invalid_response', 'The server returned an invalid binding.', 200);
      }
      return normalizeBinding(payload);
    },
    onSuccess: (_updated, variables) => {
      // The ordered list view is authoritative; models refresh updated_at.
      void queryClient.invalidateQueries({ queryKey: userKeys.bindings(variables.modelId) });
      void queryClient.invalidateQueries({ queryKey: userKeys.models });
    },
  });
}

/**
 * Fetched upstream-model options for one existing binding. A binding row
 * carries its key id and the endpoint base_url but not the endpoint id, so
 * the owning endpoint is resolved by matching base_url candidates and then
 * key membership (key ids are globally unique). Both lists are server-capped,
 * keeping the sequential probe bounded. When no enabled owner resolves — for
 * example the endpoint or key was disabled after the binding was created —
 * the query yields an empty option list instead of a fabricated one.
 */
export function useBindingUpstreamModels(
  binding: Pick<ModelBinding, 'endpoint_key_id' | 'endpoint_base_url'> | undefined,
  enabled = true,
) {
  return useQuery({
    queryKey: binding
      ? [...userKeys.keyModelsRoot, 'binding', binding.endpoint_key_id]
      : [...userKeys.keyModelsRoot, 'binding', 'none'],
    queryFn: async () => {
      if (!binding) return [];
      const endpoints = listPayload(await apiFetch<unknown>('/api/endpoints')).map(normalizeEndpoint);
      const candidates = endpoints.filter(
        (endpoint) => endpoint.enabled && endpoint.base_url === binding.endpoint_base_url,
      );
      for (const endpoint of candidates) {
        const keys = listPayload(
          await apiFetch<unknown>(`/api/endpoints/${encodeURIComponent(endpoint.id)}/keys`),
        ).map(normalizeEndpointKey);
        if (!keys.some((key) => key.id === binding.endpoint_key_id)) continue;
        return listPayload(
          await apiFetch<unknown>(
            `/api/endpoints/${encodeURIComponent(endpoint.id)}/keys/${encodeURIComponent(binding.endpoint_key_id)}/models`,
          ),
        ).map(normalizeUpstreamModel);
      }
      return [];
    },
    enabled: enabled && Boolean(binding),
    staleTime: 15_000,
  });
}

export function useCallerKey(enabled = true) {
  return useQuery({
    queryKey: userKeys.callerKey,
    queryFn: async () => normalizeCallerKey(await apiFetch<unknown>('/api/caller-key')),
    enabled,
    staleTime: 5_000,
  });
}

/**
 * The user issue center page. resolved is a tri-state: undefined shows every
 * issue, true only acknowledged ones, false only open ones. Pagination is
 * server-side offset paging (page + page_size) inside a `{data, has_more}`
 * envelope, so any page can be sought directly.
 */
export function useUserIssues(page: number, resolved?: boolean, pageSize = 20, enabled = true) {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (resolved !== undefined) query.set('resolved', resolved ? 'true' : 'false');
  const filterKey = resolved === undefined ? 'all' : String(resolved);
  return useQuery({
    queryKey: [...userKeys.issues, filterKey, page, pageSize],
    queryFn: async () =>
      normalizeIssuePage(await apiFetch<unknown>(`/api/issues?${query}`), pageSize),
    enabled,
  });
}

/** Acknowledge one issue; the server treats repeats as a success. */
export function useResolveUserIssue() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (issueId: string) =>
      apiFetch<void>(`/api/issues/${encodeURIComponent(issueId)}/resolve`, { method: 'POST' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: userKeys.issues });
    },
  });
}

/**
 * Server-paginated list of the session principal's own request logs. The
 * frozen filter set is exact equality on the server; only non-empty single
 * values are sent, and there is no user-side export.
 */
export function useUserLogs(
  page: number,
  filter: UserLogFilter = {},
  pageSize = 20,
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (filter.model) params.set('model', filter.model);
  if (filter.errorCode) params.set('error_code', filter.errorCode);
  if (filter.status) params.set('status', filter.status);
  if (filter.fromUnix !== undefined) params.set('from', String(filter.fromUnix));
  if (filter.toUnix !== undefined) params.set('to', String(filter.toUnix));
  return useQuery({
    queryKey: userKeys.logs(page, filter, pageSize),
    queryFn: async () => normalizeUserLogPage(await apiFetch<unknown>(`/api/logs?${params}`)),
    enabled,
  });
}

/** Server-paginated level-5 projection. This is the only request made by the
 * steward log view; it has no options or export endpoint. */
export function useStewardLogs(
  page: number,
  filter: StewardLogFilter = {},
  pageSize = 20,
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (filter.userId) params.set('user_id', filter.userId);
  if (filter.endpointBaseURL) params.set('endpoint_base_url', filter.endpointBaseURL);
  if (filter.upstreamModel) params.set('upstream_model', filter.upstreamModel);
  if (filter.errorCode) params.set('error_code', filter.errorCode);
  if (filter.status) params.set('status', filter.status);
  if (filter.fromUnix !== undefined) params.set('from', String(filter.fromUnix));
  if (filter.toUnix !== undefined) params.set('to', String(filter.toUnix));
  return useQuery({
    queryKey: userKeys.stewardLogs(page, filter, pageSize),
    queryFn: async () => normalizeStewardLogPage(await apiFetch<unknown>(`/api/steward/logs?${params}`)),
    enabled,
  });
}

/**
 * Bounded candidate list of the caller's own platform model names for the
 * model filter dropdown. The server never substring-searches logs; any
 * narrowing happens locally against this capped list.
 */
export function useUserLogOptions(enabled = true) {
  return useQuery({
    queryKey: userKeys.logOptions,
    queryFn: async (): Promise<UserLogOptions> => {
      const record = asRecord(await apiFetch<unknown>('/api/logs/options'));
      if (!record) {
        throw new ApiError('invalid_response', 'The server returned an invalid option list.', 200);
      }
      const models = asArray(record.models)
        .map((value) => text(value, 256))
        .filter((value) => value.length > 0);
      return { models };
    },
    enabled,
    staleTime: 30_000,
  });
}

/**
 * Normalizes the GET /api/checkin projection. While unavailable the payload is
 * exactly `{enabled:false}` with no reason; while enabled every amount is a
 * canonical decimal milli-credit string the page renders through the shared
 * formatter (never Number()). A missing or malformed field fails the query
 * rather than letting the page invent an award range or status.
 */
function normalizeCheckinStatus(payload: unknown): CheckinStatus {
  const record = asRecord(payload);
  if (!record) {
    throw new ApiError('invalid_response', 'The server returned an invalid check-in status.', 200);
  }
  if (recordValue(record, 'enabled') !== true) {
    return { enabled: false };
  }
  const requiredAmount = (key: string): string => {
    const value = amountValue(recordValue(record, key));
    if (!value) {
      throw new ApiError('invalid_response', 'The server returned an invalid check-in status.', 200);
    }
    return value;
  };
  return {
    enabled: true,
    checked_in_today: booleanValue(recordValue(record, 'checked_in_today')),
    credits: requiredAmount('credits'),
    award_min_milli: requiredAmount('award_min_milli'),
    award_max_milli: requiredAmount('award_max_milli'),
    credits_cap_milli: requiredAmount('credits_cap_milli'),
  };
}

/** The read-only daily check-in status; the server decides availability. */
export function useCheckinStatus(enabled = true) {
  return useQuery({
    queryKey: userKeys.checkin,
    queryFn: async () => normalizeCheckinStatus(await apiFetch<unknown>('/api/checkin')),
    enabled,
    staleTime: 5_000,
  });
}

export interface CharityPrices {
  request_user_price_milli: string;
  request_donor_reward_milli: string;
  uncached_user_price_milli: string;
  cache_write_user_price_milli: string;
  cache_read_user_price_milli: string;
  output_user_price_milli: string;
  uncached_donor_reward_milli: string;
  cache_write_donor_reward_milli: string;
  cache_read_donor_reward_milli: string;
  output_donor_reward_milli: string;
  /** Optional server-projected prices for the currently active discount. */
  current_request_user_price_milli?: string;
  current_uncached_user_price_milli?: string;
  current_cache_write_user_price_milli?: string;
  current_cache_read_user_price_milli?: string;
  current_output_user_price_milli?: string;
}

export interface CharityModel {
  id: string;
  provider: string;
  model: string;
  full_name: string;
  enabled: boolean;
  flatten_tool_calls: boolean;
  pricing_mode: 'per_request' | 'per_token';
  prices: CharityPrices;
  discount: { percent: number; enabled: boolean; start_at?: number; end_at?: number };
  success_samples: number;
  success_count: number;
  /** Server projection: availability never comes from a client-side route choice. */
  available: boolean;
  availability_reason: string;
}

export interface DonationKey {
  id: string;
  endpoint_key_id?: string;
  display?: string;
  max_concurrency: number;
  rpm_limit: number;
  credits_usage_cap_milli: string;
  credits_used_milli: string;
  credits_reserved_milli: string;
  enabled: boolean;
  /** Read-only outside the physical key owner's own resource form. */
  force_store_false: boolean;
}

export interface DonationReview {
  id: string;
  reviewer_role: string;
  action: string;
  note: string;
  created_at: number;
}

export interface Donation {
  id: string;
  endpoint_id?: string;
  endpoint_base_url: string;
  status: 'pending' | 'approved' | 'rejected' | 'deleted' | 'expired' | string;
  enabled: boolean;
  description: string;
  review_note: string;
  expires_at?: number;
  reviewed_at?: number;
  created_at: number;
  updated_at: number;
  keys: DonationKey[];
  reviews: DonationReview[];
}

function amountString(value: unknown, field = 'amount'): string {
  if (typeof value !== 'string' || !/^(0|[1-9]\d*)$/.test(value) || value.length > 19) {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  try {
    if (BigInt(value) > 9_223_372_036_854_775_807n) throw new Error('overflow');
  } catch {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  return value;
}

function optionalUnix(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

function safeFragment(head: unknown, tail: unknown): string | undefined {
  const h = typeof head === 'string' && head.length <= 8 ? head : '';
  const t = typeof tail === 'string' && tail.length <= 8 ? tail : '';
  return h || t ? `${h}${h && t ? '…' : ''}${t}` : undefined;
}

export function normalizeCharityModel(value: unknown): CharityModel {
  const record = asRecord(value) ?? {};
  const rawPrices = asRecord(recordValue(record, 'prices')) ?? {};
  const rawDiscount = asRecord(recordValue(record, 'discount')) ?? {};
  const pricingMode = recordValue(record, 'pricing_mode') === 'per_token' ? 'per_token' : 'per_request';
  const price = (key: string) => amountString(recordValue(rawPrices, key));
  const current = (key: string) => {
    const value = recordValue(rawPrices, key);
    return typeof value === 'string' && value.length <= 32 ? value : undefined;
  };
  const samples = Math.max(0, integerValue(recordValue(record, 'success_samples')));
  const success = Math.max(0, Math.min(samples, integerValue(recordValue(record, 'success_count'))));
  return {
    id: idValue(recordValue(record, 'id')),
    provider: text(recordValue(record, 'provider'), 128, '—'),
    model: text(recordValue(record, 'model'), 256, '—'),
    full_name: text(recordValue(record, 'full_name'), 512, '—'),
    enabled: booleanValue(recordValue(record, 'enabled')),
    flatten_tool_calls: additivePolicyBoolean(
      recordValue(record, 'flatten_tool_calls'), 'charity tool-call policy',
    ),
    pricing_mode: pricingMode,
    prices: {
      request_user_price_milli: price('request_user_price_milli'),
      request_donor_reward_milli: price('request_donor_reward_milli'),
      uncached_user_price_milli: price('uncached_user_price_milli'),
      cache_write_user_price_milli: price('cache_write_user_price_milli'),
      cache_read_user_price_milli: price('cache_read_user_price_milli'),
      output_user_price_milli: price('output_user_price_milli'),
      uncached_donor_reward_milli: price('uncached_donor_reward_milli'),
      cache_write_donor_reward_milli: price('cache_write_donor_reward_milli'),
      cache_read_donor_reward_milli: price('cache_read_donor_reward_milli'),
      output_donor_reward_milli: price('output_donor_reward_milli'),
      ...(current('current_request_user_price_milli') ? { current_request_user_price_milli: current('current_request_user_price_milli') } : {}),
      ...(current('current_uncached_user_price_milli') ? { current_uncached_user_price_milli: current('current_uncached_user_price_milli') } : {}),
      ...(current('current_cache_write_user_price_milli') ? { current_cache_write_user_price_milli: current('current_cache_write_user_price_milli') } : {}),
      ...(current('current_cache_read_user_price_milli') ? { current_cache_read_user_price_milli: current('current_cache_read_user_price_milli') } : {}),
      ...(current('current_output_user_price_milli') ? { current_output_user_price_milli: current('current_output_user_price_milli') } : {}),
    },
    discount: {
      percent: Math.max(0, Math.min(100, integerValue(recordValue(rawDiscount, 'percent')))),
      enabled: booleanValue(recordValue(rawDiscount, 'enabled')),
      ...(optionalUnix(recordValue(rawDiscount, 'start_at')) ? { start_at: optionalUnix(recordValue(rawDiscount, 'start_at')) } : {}),
      ...(optionalUnix(recordValue(rawDiscount, 'end_at')) ? { end_at: optionalUnix(recordValue(rawDiscount, 'end_at')) } : {}),
    },
    success_samples: samples,
    success_count: success,
    available: recordValue(record, 'available') !== false,
    availability_reason: text(recordValue(record, 'availability_reason'), 256),
  };
}

export function normalizeDonationKey(value: unknown): DonationKey {
  const record = asRecord(value) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    ...(recordValue(record, 'endpoint_key_id') !== null && recordValue(record, 'endpoint_key_id') !== undefined
      ? { endpoint_key_id: idValue(recordValue(record, 'endpoint_key_id')) }
      : {}),
    ...(safeFragment(recordValue(record, 'display_head'), recordValue(record, 'display_tail'))
      ? { display: safeFragment(recordValue(record, 'display_head'), recordValue(record, 'display_tail')) }
      : {}),
    max_concurrency: strictInteger(recordValue(record, 'max_concurrency'), 0, 100_000, 'donation key concurrency'),
    rpm_limit: strictInteger(recordValue(record, 'rpm_limit'), 0, 4_096, 'donation key RPM'),
    credits_usage_cap_milli: amountString(recordValue(record, 'credits_usage_cap_milli')),
    credits_used_milli: amountString(recordValue(record, 'credits_used_milli')),
    credits_reserved_milli: amountString(recordValue(record, 'credits_reserved_milli')),
    enabled: booleanValue(recordValue(record, 'enabled'), true),
    force_store_false: additivePolicyBoolean(
      recordValue(record, 'force_store_false'), 'donation-key store policy',
    ),
  };
}

function normalizeDonation(value: unknown, detailed = false): Donation {
  const record = asRecord(value) ?? {};
  const status = text(recordValue(record, 'status'), 32, 'pending') as Donation['status'];
  const rawKeys = asArray(recordValue(record, 'keys'));
  const rawReviews = asArray(recordValue(record, 'reviews'));
  return {
    id: idValue(recordValue(record, 'id')),
    ...(recordValue(record, 'endpoint_id') !== null && recordValue(record, 'endpoint_id') !== undefined
      ? { endpoint_id: idValue(recordValue(record, 'endpoint_id')) }
      : {}),
    endpoint_base_url: text(recordValue(record, 'endpoint_base_url'), 2048, '—'),
    status,
    enabled: booleanValue(recordValue(record, 'enabled')),
    description: text(recordValue(record, 'description'), 4096),
    review_note: text(recordValue(record, 'review_note'), 4096),
    ...(optionalUnix(recordValue(record, 'expires_at')) ? { expires_at: optionalUnix(recordValue(record, 'expires_at')) } : {}),
    ...(optionalUnix(recordValue(record, 'reviewed_at')) ? { reviewed_at: optionalUnix(recordValue(record, 'reviewed_at')) } : {}),
    created_at: unixSecondsValue(recordValue(record, 'created_at')),
    updated_at: unixSecondsValue(recordValue(record, 'updated_at')),
    keys: detailed ? rawKeys.map(normalizeDonationKey) : [],
    reviews: detailed ? rawReviews.map((review) => {
      const item = asRecord(review) ?? {};
      return {
        id: idValue(recordValue(item, 'id')),
        reviewer_role: text(recordValue(item, 'reviewer_role'), 32),
        action: text(recordValue(item, 'action'), 32),
        note: text(recordValue(item, 'note'), 4096),
        created_at: unixSecondsValue(recordValue(item, 'created_at')),
      };
    }) : [],
  };
}

export function useCharityModels(enabled = true) {
  return useQuery({
    queryKey: userKeys.charityModels,
    queryFn: async () => listPayload(await apiFetch<unknown>('/api/charity/models')).map((value) => normalizeCharityModel(value)),
    enabled,
    staleTime: 15_000,
  });
}

export function useDonations(enabled = true) {
  return useQuery({
    queryKey: userKeys.donations,
    queryFn: async () => listPayload(await apiFetch<unknown>('/api/donations')).map((value) => normalizeDonation(value)),
    enabled,
  });
}

export function useDonation(id: string | undefined, enabled = true) {
  return useQuery({
    queryKey: id ? userKeys.donation(id) : [...userKeys.donations, 'none'],
    queryFn: async () => {
      if (!id) throw new ApiError('invalid_request', 'A donation id is required.', 400);
      return normalizeDonation(await apiFetch<unknown>(`/api/donations/${encodeURIComponent(id)}`), true);
    },
    enabled: enabled && Boolean(id),
  });
}

export interface DonationPayload {
  description: string;
  expires_at?: number | null;
  existing_endpoint?: { endpoint_id: number; key_ids: number[]; keys?: Array<{ endpoint_key_id: number; max_concurrency?: number; rpm_limit?: number }> };
  new_endpoint?: { connector_type?: string; base_url: string; note?: string; keys: Array<{ secret: string; note?: string; max_concurrency?: number; rpm_limit?: number }> };
}

export function useCreateDonation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (payload: DonationPayload) => apiFetch<unknown>('/api/donations', { method: 'POST', json: payload }),
    onSuccess: () => { void queryClient.invalidateQueries({ queryKey: userKeys.donations }); },
  });
}

export function useUpdateDonation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...payload }: { id: string } & Partial<DonationPayload>) =>
      apiFetch<unknown>(`/api/donations/${encodeURIComponent(id)}`, { method: 'PATCH', json: payload }),
    onSuccess: (_value, variables) => {
      void queryClient.invalidateQueries({ queryKey: userKeys.donations });
      void queryClient.invalidateQueries({ queryKey: userKeys.donation(variables.id) });
    },
  });
}

export function useDeleteDonation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiFetch<void>(`/api/donations/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    onSuccess: () => { void queryClient.invalidateQueries({ queryKey: userKeys.donations }); },
  });
}

/**
 * The atomic daily check-in. The client supplies nothing (no award, no day);
 * the mutation returns the server-drawn award and the new balance. Balances
 * are never patched locally — the session and check-in queries are invalidated
 * so the refetch is authoritative.
 */
export function useCheckin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const record = asRecord(await apiFetch<unknown>('/api/checkin', { method: 'POST' }));
      const award = record ? amountValue(recordValue(record, 'award_milli')) : '';
      const credits = record ? amountValue(recordValue(record, 'credits')) : '';
      if (!award || !credits) {
        throw new ApiError('invalid_response', 'The server returned an invalid check-in result.', 200);
      }
      return { award_milli: award, credits } satisfies CheckinResult;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: userKeys.session });
      void queryClient.invalidateQueries({ queryKey: userKeys.me });
      void queryClient.invalidateQueries({ queryKey: userKeys.checkin });
    },
    onError: (error) => {
      // A 409 means the day was already consumed: refetch and settle into the
      // checked-in state instead of leaving a stale "not checked in" view.
      if (error instanceof ApiError && error.code === 'already_checked_in') {
        void queryClient.invalidateQueries({ queryKey: userKeys.checkin });
      }
    },
  });
}
