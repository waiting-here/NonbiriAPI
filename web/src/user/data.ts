import { CancelledError } from '@tanstack/react-query';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch, isNotFoundError } from '@shared/query/http';
import {
  beginManagementSessionRequest,
  failManagementSessionRequest,
  isStationSessionChanged,
  noteManagementSessionSuccess,
  stationSessionWrite,
} from '@shared/charityManagement';
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
  opaqueID,
  text,
  type UnknownRecord,
} from '@shared/query/normalize';
import {
  normalizeEndpoint as normalizeCoreEndpoint,
  normalizeUserEnvelope as normalizeCoreUserEnvelope,
  normalizeUserProfile as normalizeCoreUserProfile,
} from './features/core/normalizers';
import type {
  Endpoint as CoreEndpoint,
  UserProfile as CoreUserProfile,
} from './features/core/types';

export type UserSummary = CoreUserProfile;

export interface UserSessionResponse {
  user: UserSummary;
}

export interface UsageSummary {
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_unknown_usage_requests: number;
}

export type Endpoint = CoreEndpoint;

export interface EndpointKey {
  id: string;
  /** Head/tail fragment only. The full secret is never part of this type. */
  display?: string;
  note: string;
  enabled: boolean;
  /** Experimental physical-key policy; writable only by the key owner. */
  force_store_false: StorePolicy;
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
    [
      'user',
      'steward-logs',
      page,
      filter.userId ?? '',
      filter.endpointBaseURL ?? '',
      filter.upstreamModel ?? '',
      filter.errorCode ?? '',
      filter.status ?? '',
      filter.fromUnix ?? '',
      filter.toUnix ?? '',
      pageSize,
    ] as const,
  stewardLogsRoot: ['user', 'steward-logs'] as const,
  charityModels: ['user', 'charity-models'] as const,
  donations: ['user', 'donations'] as const,
  donation: (id: string) => ['user', 'donations', id] as const,
};

function recordValue(record: UnknownRecord, key: string): unknown {
  return record[key];
}

function strictInteger(value: unknown, minimum: number, maximum: number, field: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  return value as number;
}

function requiredRecord(value: unknown, field: string): UnknownRecord {
  const record = asRecord(value);
  if (!record)
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  return record;
}

/** Required text fields may be empty (notes are valid empty strings), but the
 * wire value itself must be a bounded, control-free string. Display-only
 * fragments use the separate displayText helper below when cleaning is part
 * of their rendering contract. */
function requiredText(value: unknown, max: number, field: string, allowEmpty = false): string {
  if (
    typeof value !== 'string' ||
    hasControlCharacters(value) ||
    Array.from(value).length > max ||
    (!allowEmpty && !value.trim())
  ) {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  return value;
}

function optionalTextStrict(value: unknown, max: number, field: string): string | undefined {
  if (value === undefined || value === null) return undefined;
  return requiredText(value, max, field, true);
}

/** Display-only fragments may be safely cleaned and bounded before rendering. */
function displayText(value: unknown, max: number, field: string): string | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== 'string') {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  return text(value, max) || undefined;
}

function requiredTimestamp(value: unknown, field: string): string {
  // Alpha.2 wire timestamps are Unix seconds, never ISO strings.  Keeping
  // this boundary numeric prevents an invalid string from becoming a blank
  // datetime-local value that a later edit could accidentally clear.
  if (typeof value === 'number' && Number.isSafeInteger(value) && value > 0) {
    const date = new Date(value * 1000);
    if (!Number.isNaN(date.getTime())) return date.toISOString();
  }
  throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
}

function optionalTimestamp(value: unknown, field: string): number | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  return value;
}

function requiredCount(value: unknown, field: string, maximum = Number.MAX_SAFE_INTEGER): number {
  return strictInteger(value, 0, maximum, field);
}

function requiredUnixSeconds(value: unknown, field: string): number {
  return strictInteger(value, 1, Number.MAX_SAFE_INTEGER, field);
}

// Resource policy fields are strict JSON booleans. Connector-specific store
// projections use the explicit not_applicable sentinel when the server omits
// the OpenAI-only field for an Anthropic key.
function requiredID(value: unknown, field = 'id'): string {
  const id = opaqueID(value);
  if (!id) throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  return id;
}

function optionalID(value: unknown, field = 'id'): string | undefined {
  if (value === null || value === undefined) return undefined;
  return requiredID(value, field);
}

function donationStatus(value: unknown): Donation['status'] {
  if (value === 'pending' || value === 'approved' || value === 'rejected' || value === 'deleted') {
    return value;
  }
  throw new ApiError('invalid_response', 'The server returned an invalid donation status.', 200);
}

function connectorType(value: unknown): Endpoint['connector_type'] {
  if (value === 'openai-compatible' || value === 'anthropic-compatible') return value;
  throw new ApiError('invalid_response', 'The server returned an invalid connector type.', 200);
}

function routeStrategy(value: unknown): PlatformModel['route_strategy'] {
  if (value === 'ordered' || value === 'random') return value;
  throw new ApiError('invalid_response', 'The server returned an invalid route strategy.', 200);
}

function requiredBoolean(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') {
    throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
  }
  return value;
}

export type StorePolicy = boolean | 'not_applicable';

export function normalizeUserSummary(value: unknown): UserSummary {
  return normalizeCoreUserProfile(value);
}

function normalizeSession(value: unknown): UserSessionResponse {
  return normalizeCoreUserEnvelope(value);
}

function normalizeUsage(value: unknown): UsageSummary {
  const record = asRecord(value);
  if (!record)
    throw new ApiError('invalid_response', 'The server returned an invalid usage summary.', 200);
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
  if (Array.isArray(value)) return value.slice(0, 1000);
  const record = requiredRecord(value, 'list');
  if (
    !Array.isArray(record.data) ||
    typeof record.has_more !== 'boolean' ||
    !Number.isSafeInteger(record.total) ||
    (record.total as number) < 0
  ) {
    throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
  }
  return record.data.slice(0, 1000);
}

function donationListPayload(value: unknown): unknown[] {
  if (Array.isArray(value)) return value.slice(0, 1000);
  const record = requiredRecord(value, 'donation list');
  if (
    !Array.isArray(record.data) ||
    typeof record.has_more !== 'boolean' ||
    !Number.isSafeInteger(record.total) ||
    (record.total as number) < 0
  ) {
    throw new ApiError('invalid_response', 'The server returned an invalid donation list.', 200);
  }
  return record.data.slice(0, 1000);
}

/**
 * GET /api/charity/models has its own non-paginated wire: the backend emits
 * exactly an object containing the enabled projection under `data`.  Do not
 * route this endpoint through the general list decoder, since accepting a
 * bare array (or inventing pagination metadata) would hide a proxy/backend
 * contract mismatch.
 */
function charityModelsPayload(value: unknown): unknown[] {
  const record = requiredRecord(value, 'charity model list');
  if (!Array.isArray(record.data) || Object.keys(record).some((key) => key !== 'data')) {
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid charity model list.',
      200,
    );
  }
  return record.data.slice(0, 1000);
}

export function normalizeEndpoint(value: unknown): Endpoint {
  return normalizeCoreEndpoint(value);
}

function safeDisplayFragment(value: unknown, max = 64): string | undefined {
  const candidate = displayText(value, max, 'key display');
  return candidate && (candidate.includes('…') || candidate.includes('...'))
    ? candidate
    : undefined;
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
  const head =
    displayText(recordValue(record, 'display_head'), 8, 'key display head') ??
    displayText(recordValue(record, 'secret_head'), 8, 'key display head');
  const tail =
    displayText(recordValue(record, 'display_tail'), 8, 'key display tail') ??
    displayText(recordValue(record, 'secret_tail'), 8, 'key display tail');
  return formatFragment(head, tail);
}

export function normalizeEndpointKey(value: unknown, connectorTypeValue: string): EndpointKey {
  const record = requiredRecord(value, 'endpoint key');
  const fragment = fragmentFromRecord(record);
  const connector = connectorType(connectorTypeValue);
  const hasStorePolicy = Object.prototype.hasOwnProperty.call(record, 'force_store_false');
  const rawStorePolicy = recordValue(record, 'force_store_false');
  let storePolicyValue: StorePolicy;
  if (connector === 'openai-compatible') {
    if (!hasStorePolicy || typeof rawStorePolicy !== 'boolean') {
      throw new ApiError('invalid_response', 'The server returned an invalid store policy.', 200);
    }
    storePolicyValue = rawStorePolicy;
  } else {
    if (hasStorePolicy) {
      throw new ApiError(
        'invalid_response',
        'The server returned an unexpected store policy.',
        200,
      );
    }
    storePolicyValue = 'not_applicable';
  }
  return {
    id: requiredID(recordValue(record, 'id'), 'endpoint key id'),
    ...(fragment ? { display: fragment } : {}),
    note: requiredText(recordValue(record, 'note'), 512, 'endpoint key note', true),
    enabled: requiredBoolean(recordValue(record, 'enabled'), 'endpoint key enabled'),
    force_store_false: storePolicyValue,
    created_at: requiredTimestamp(
      recordValue(record, 'created_at'),
      'endpoint key created timestamp',
    ),
    updated_at: requiredTimestamp(
      recordValue(record, 'updated_at'),
      'endpoint key updated timestamp',
    ),
  };
}

export function normalizeUpstreamModel(value: unknown): UpstreamModel {
  const record = requiredRecord(value, 'upstream model');
  const status = recordValue(record, 'status');
  if (status !== 'ok') {
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid upstream model status.',
      200,
    );
  }
  return {
    upstream_model_id: requiredText(
      recordValue(record, 'upstream_model_id'),
      512,
      'upstream model id',
    ),
    provider: requiredText(recordValue(record, 'provider'), 128, 'upstream model provider'),
    fetched_at: requiredTimestamp(
      recordValue(record, 'fetched_at'),
      'upstream model fetched timestamp',
    ),
    status,
  };
}

export function normalizePlatformModel(value: unknown): PlatformModel {
  const record = requiredRecord(value, 'model');
  return {
    id: requiredID(recordValue(record, 'id'), 'model id'),
    provider: requiredText(recordValue(record, 'provider'), 64, 'model provider'),
    model: requiredText(recordValue(record, 'model'), 64, 'model name'),
    full_name: requiredText(recordValue(record, 'full_name'), 160, 'model full name'),
    route_strategy: routeStrategy(recordValue(record, 'route_strategy')),
    silent_retry: requiredBoolean(recordValue(record, 'silent_retry'), 'silent-retry policy'),
    flatten_tool_calls: requiredBoolean(
      recordValue(record, 'flatten_tool_calls'),
      'tool-call policy',
    ),
    binding_count: requiredCount(recordValue(record, 'binding_count'), 'binding count'),
    created_at: requiredTimestamp(recordValue(record, 'created_at'), 'model created timestamp'),
    updated_at: requiredTimestamp(recordValue(record, 'updated_at'), 'model updated timestamp'),
  };
}

export function normalizeBinding(value: unknown): ModelBinding {
  const record = requiredRecord(value, 'model binding');
  const keyHead = optionalTextStrict(
    recordValue(record, 'endpoint_key_display_head'),
    8,
    'endpoint key display head',
  );
  const keyTail = optionalTextStrict(
    recordValue(record, 'endpoint_key_display_tail'),
    8,
    'endpoint key display tail',
  );
  const keyDisplay = formatFragment(keyHead, keyTail);
  return {
    id: requiredID(recordValue(record, 'id'), 'binding id'),
    endpoint_key_id: requiredID(recordValue(record, 'endpoint_key_id'), 'endpoint key id'),
    upstream_model_id: requiredText(
      recordValue(record, 'upstream_model_id'),
      256,
      'upstream model id',
    ),
    endpoint_base_url: requiredText(
      recordValue(record, 'endpoint_base_url'),
      2048,
      'endpoint base URL',
    ),
    ...(keyDisplay ? { endpoint_key_display: keyDisplay } : {}),
    endpoint_key_note:
      optionalTextStrict(recordValue(record, 'endpoint_key_note'), 512, 'endpoint key note') ?? '',
    endpoint_note:
      optionalTextStrict(recordValue(record, 'endpoint_note'), 512, 'endpoint note') ?? '',
    ord: strictInteger(recordValue(record, 'ord'), 0, 1_000_000, 'binding order'),
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
  const hasMore = typeof record?.has_more === 'boolean' ? record.has_more : result.hasNext;
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
  if (
    secret !== secret.trim() ||
    !secret.startsWith('nbk_') ||
    secret.length < 8 ||
    secret.length > 512
  ) {
    throw new ApiError('invalid_response', 'The server returned an invalid caller key.', 200);
  }
  if (hasControlCharacters(secret)) {
    throw new ApiError('invalid_response', 'The server returned an invalid caller key.', 200);
  }
  return { secret };
}

export function useUserSession(enabled = true) {
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: userKeys.session,
    queryFn: async () => {
      const generation = beginManagementSessionRequest(queryClient, 'steward');
      try {
        const value = normalizeSession(await apiFetch<unknown>('/api/session'));
        if (!noteManagementSessionSuccess(queryClient, 'steward', value, generation)) {
          // An older response must not replace the current session projection
          // after a logout/login or same-level account switch.
          throw new CancelledError();
        }
        return value;
      } catch (error) {
        failManagementSessionRequest(queryClient, 'steward', generation);
        throw error;
      }
    },
    staleTime: 15_000,
    retry: false,
    enabled,
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
      stationSessionWrite(queryClient, 'steward', () =>
        apiFetch<unknown>('/api/me', { method: 'PATCH', json: values }),
      ),
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
    queryFn: async () =>
      listPayload(await apiFetch<unknown>('/api/endpoints')).map(normalizeEndpoint),
    enabled,
  });
}

export function useEndpointKeys(
  endpointId: string | undefined,
  enabled = true,
  connectorTypeValue: string | undefined,
) {
  return useQuery({
    queryKey: endpointId
      ? [...userKeys.endpointKeys(endpointId), connectorTypeValue ?? 'connector-unresolved']
      : ['user', 'endpoint-keys', 'none'],
    queryFn: async () => {
      if (!endpointId || !connectorTypeValue) {
        throw new ApiError('invalid_response', 'The endpoint connector type is unavailable.', 200);
      }
      const endpointID = requiredID(endpointId, 'endpoint id');
      return listPayload(
        await apiFetch<unknown>(`/api/endpoints/${encodeURIComponent(endpointID)}/keys`),
      ).map((value) => normalizeEndpointKey(value, connectorTypeValue));
    },
    enabled: enabled && Boolean(endpointId && connectorTypeValue),
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
      const endpointID = requiredID(endpointId, 'endpoint id');
      const keyID = requiredID(keyId, 'endpoint key id');
      return listPayload(
        await apiFetch<unknown>(
          `/api/endpoints/${encodeURIComponent(endpointID)}/keys/${encodeURIComponent(keyID)}/models`,
        ),
      ).map(normalizeUpstreamModel);
    },
    enabled: enabled && Boolean(endpointId && keyId),
  });
}

export function usePlatformModels(enabled = true) {
  return useQuery({
    queryKey: userKeys.models,
    queryFn: async () =>
      listPayload(await apiFetch<unknown>('/api/models')).map(normalizePlatformModel),
    enabled,
  });
}

export function useModelBindings(modelId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: modelId ? userKeys.bindings(modelId) : ['user', 'bindings', 'none'],
    queryFn: async () => {
      if (!modelId) return [];
      const modelID = requiredID(modelId, 'model id');
      return normalizeBindingList(
        await apiFetch<unknown>(`/api/models/${encodeURIComponent(modelID)}/bindings`),
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
  /** Experimental logical-model policy; the server validates connector support. */
  flatten_tool_calls?: boolean;
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
      const modelID = requiredID(modelId, 'model id');
      const payload = await stationSessionWrite(queryClient, 'steward', () =>
        apiFetch<unknown>(`/api/models/${encodeURIComponent(modelID)}`, {
          method: 'PATCH',
          json: patch,
        }),
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
      const modelID = requiredID(modelId, 'model id');
      const bindingID = requiredID(bindingId, 'binding id');
      if (patch.ord !== undefined) strictInteger(patch.ord, 0, 1_000_000, 'binding order');
      const payload = await stationSessionWrite(queryClient, 'steward', () =>
        apiFetch<unknown>(
          `/api/models/${encodeURIComponent(modelID)}/bindings/${encodeURIComponent(bindingID)}`,
          { method: 'PATCH', json: patch },
        ),
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
      const endpoints = listPayload(await apiFetch<unknown>('/api/endpoints')).map(
        normalizeEndpoint,
      );
      const candidates = endpoints.filter(
        (endpoint) => endpoint.enabled && endpoint.base_url === binding.endpoint_base_url,
      );
      for (const endpoint of candidates) {
        const endpointID = requiredID(endpoint.id, 'endpoint id');
        const keys = listPayload(
          await apiFetch<unknown>(`/api/endpoints/${encodeURIComponent(endpointID)}/keys`),
        ).map((value) => normalizeEndpointKey(value, endpoint.connector_type));
        if (!keys.some((key) => key.id === binding.endpoint_key_id)) continue;
        return listPayload(
          await apiFetch<unknown>(
            `/api/endpoints/${encodeURIComponent(endpointID)}/keys/${encodeURIComponent(binding.endpoint_key_id)}/models`,
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
    mutationFn: (issueId: string) => {
      const issueID = requiredID(issueId, 'issue id');
      return stationSessionWrite(queryClient, 'steward', () =>
        apiFetch<void>(`/api/issues/${encodeURIComponent(issueID)}/resolve`, { method: 'POST' }),
      );
    },
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
    queryFn: async () =>
      normalizeStewardLogPage(await apiFetch<unknown>(`/api/steward/logs?${params}`)),
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
  /** Required server-projected prices for the currently active discount. */
  current_request_user_price_milli: string;
  current_uncached_user_price_milli: string;
  current_cache_write_user_price_milli: string;
  current_cache_read_user_price_milli: string;
  current_output_user_price_milli: string;
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
  force_store_false: StorePolicy;
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

function optionalUnix(value: unknown, field: string): number | undefined {
  return optionalTimestamp(value, field);
}

function safeFragment(head: unknown, tail: unknown): string | undefined {
  const h = displayText(head, 8, 'donation key display head') ?? '';
  const t = displayText(tail, 8, 'donation key display tail') ?? '';
  return h || t ? `${h}${h && t ? '…' : ''}${t}` : undefined;
}

export function normalizeCharityModel(value: unknown): CharityModel {
  const record = requiredRecord(value, 'charity model');
  const rawPrices = requiredRecord(recordValue(record, 'prices'), 'charity model prices');
  const rawDiscount = requiredRecord(recordValue(record, 'discount'), 'charity model discount');
  const pricingMode = recordValue(record, 'pricing_mode');
  if (pricingMode !== 'per_token' && pricingMode !== 'per_request') {
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid charity pricing mode.',
      200,
    );
  }
  const price = (key: string) => amountString(recordValue(rawPrices, key));
  const current = (key: string) =>
    amountString(recordValue(rawPrices, key), `charity model ${key}`);
  const samples = requiredCount(
    recordValue(record, 'success_samples'),
    'charity model success sample count',
  );
  const success = requiredCount(
    recordValue(record, 'success_count'),
    'charity model success count',
  );
  if (success > samples) {
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid charity model success count.',
      200,
    );
  }
  const start = optionalUnix(
    recordValue(rawDiscount, 'start_at'),
    'charity discount start timestamp',
  );
  const end = optionalUnix(recordValue(rawDiscount, 'end_at'), 'charity discount end timestamp');
  const available = requiredBoolean(recordValue(record, 'available'), 'charity model availability');
  const availabilityReason = recordValue(record, 'availability_reason');
  if (
    typeof availabilityReason !== 'string' ||
    (available && availabilityReason !== 'ok') ||
    (!available && availabilityReason !== 'no_candidate')
  ) {
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid charity model availability reason.',
      200,
    );
  }
  return {
    id: requiredID(recordValue(record, 'id'), 'charity model id'),
    provider: requiredText(recordValue(record, 'provider'), 128, 'charity model provider'),
    model: requiredText(recordValue(record, 'model'), 256, 'charity model name'),
    full_name: requiredText(recordValue(record, 'full_name'), 512, 'charity model full name'),
    enabled: requiredBoolean(recordValue(record, 'enabled'), 'charity model enabled'),
    flatten_tool_calls: requiredBoolean(
      recordValue(record, 'flatten_tool_calls'),
      'charity tool-call policy',
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
      current_request_user_price_milli: current('current_request_user_price_milli'),
      current_uncached_user_price_milli: current('current_uncached_user_price_milli'),
      current_cache_write_user_price_milli: current('current_cache_write_user_price_milli'),
      current_cache_read_user_price_milli: current('current_cache_read_user_price_milli'),
      current_output_user_price_milli: current('current_output_user_price_milli'),
    },
    discount: {
      percent: strictInteger(
        recordValue(rawDiscount, 'percent'),
        0,
        100,
        'charity discount percent',
      ),
      enabled: requiredBoolean(recordValue(rawDiscount, 'enabled'), 'charity discount enabled'),
      ...(start !== undefined ? { start_at: start } : {}),
      ...(end !== undefined ? { end_at: end } : {}),
    },
    success_samples: samples,
    success_count: success,
    available,
    availability_reason: availabilityReason,
  };
}

/**
 * Donation detail responses intentionally omit the connector-specific store
 * field for Anthropic keys.  The owner page must supply the connector from
 * its own endpoint projection before interpreting that omission; accepting a
 * missing context here would turn a malformed OpenAI response into the
 * Anthropic sentinel.
 */
export function normalizeDonationKey(value: unknown, connectorTypeValue: string): DonationKey {
  const record = requiredRecord(value, 'donation key');
  const endpointKeyID = optionalID(recordValue(record, 'endpoint_key_id'), 'endpoint key id');
  const display = safeFragment(
    recordValue(record, 'display_head'),
    recordValue(record, 'display_tail'),
  );
  const connector = connectorType(connectorTypeValue);
  const hasStorePolicy = Object.prototype.hasOwnProperty.call(record, 'force_store_false');
  const rawStorePolicy = recordValue(record, 'force_store_false');
  let storePolicyValue: StorePolicy;
  if (connector === 'openai-compatible') {
    if (!hasStorePolicy || typeof rawStorePolicy !== 'boolean') {
      throw new ApiError(
        'invalid_response',
        'The server returned an invalid donation-key store policy.',
        200,
      );
    }
    storePolicyValue = rawStorePolicy;
  } else {
    if (hasStorePolicy) {
      throw new ApiError(
        'invalid_response',
        'The server returned an unexpected donation-key store policy.',
        200,
      );
    }
    storePolicyValue = 'not_applicable';
  }
  return {
    id: requiredID(recordValue(record, 'id'), 'donation key id'),
    ...(endpointKeyID ? { endpoint_key_id: endpointKeyID } : {}),
    ...(display ? { display } : {}),
    max_concurrency: strictInteger(
      recordValue(record, 'max_concurrency'),
      0,
      100_000,
      'donation key concurrency',
    ),
    rpm_limit: strictInteger(recordValue(record, 'rpm_limit'), 0, 4_096, 'donation key RPM'),
    credits_usage_cap_milli: amountString(recordValue(record, 'credits_usage_cap_milli')),
    credits_used_milli: amountString(recordValue(record, 'credits_used_milli')),
    credits_reserved_milli: amountString(recordValue(record, 'credits_reserved_milli')),
    enabled: requiredBoolean(recordValue(record, 'enabled'), 'donation key enabled'),
    force_store_false: storePolicyValue,
  };
}

export function normalizeDonation(
  value: unknown,
  detailed = false,
  connectorTypeValue?: string,
): Donation {
  const record = requiredRecord(value, 'donation');
  const status = donationStatus(recordValue(record, 'status'));
  const rawKeys = detailed ? recordValue(record, 'keys') : undefined;
  const rawReviews = detailed ? recordValue(record, 'reviews') : undefined;
  const endpointID = optionalID(recordValue(record, 'endpoint_id'), 'endpoint id');
  const expiresAt = optionalUnix(recordValue(record, 'expires_at'), 'donation expiry timestamp');
  const reviewedAt = optionalUnix(recordValue(record, 'reviewed_at'), 'donation review timestamp');
  if (detailed && !Array.isArray(rawKeys))
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid donation keys list.',
      200,
    );
  if (detailed && rawReviews !== undefined && !Array.isArray(rawReviews))
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid donation review list.',
      200,
    );
  if (detailed && (rawKeys as unknown[]).length > 0 && !connectorTypeValue) {
    throw new ApiError('invalid_response', 'The donation key connector type is unavailable.', 200);
  }
  return {
    id: requiredID(recordValue(record, 'id'), 'donation id'),
    ...(endpointID ? { endpoint_id: endpointID } : {}),
    endpoint_base_url: requiredText(
      recordValue(record, 'endpoint_base_url'),
      2048,
      'donation endpoint base URL',
    ),
    status,
    enabled: requiredBoolean(recordValue(record, 'enabled'), 'donation enabled'),
    description: requiredText(
      recordValue(record, 'description'),
      4096,
      'donation description',
      true,
    ),
    review_note: requiredText(
      recordValue(record, 'review_note'),
      4096,
      'donation review note',
      true,
    ),
    ...(expiresAt !== undefined ? { expires_at: expiresAt } : {}),
    ...(reviewedAt !== undefined ? { reviewed_at: reviewedAt } : {}),
    created_at: requiredUnixSeconds(
      recordValue(record, 'created_at'),
      'donation created timestamp',
    ),
    updated_at: requiredUnixSeconds(
      recordValue(record, 'updated_at'),
      'donation updated timestamp',
    ),
    keys: detailed
      ? (rawKeys as unknown[]).map((key) => normalizeDonationKey(key, connectorTypeValue!))
      : [],
    reviews:
      detailed && Array.isArray(rawReviews)
        ? rawReviews.map((review) => {
            const item = requiredRecord(review, 'donation review');
            return {
              id: requiredID(recordValue(item, 'id'), 'review id'),
              reviewer_role: requiredText(recordValue(item, 'reviewer_role'), 32, 'reviewer role'),
              action: requiredText(recordValue(item, 'action'), 32, 'review action'),
              note: requiredText(recordValue(item, 'note'), 4096, 'review note', true),
              created_at: requiredUnixSeconds(
                recordValue(item, 'created_at'),
                'review created timestamp',
              ),
            };
          })
        : [],
  };
}

export function useCharityModels(enabled = true) {
  return useQuery({
    queryKey: userKeys.charityModels,
    queryFn: async () =>
      charityModelsPayload(await apiFetch<unknown>('/api/charity/models')).map((value) =>
        normalizeCharityModel(value),
      ),
    enabled,
    staleTime: 15_000,
  });
}

export function useDonations(enabled = true) {
  return useQuery({
    queryKey: userKeys.donations,
    queryFn: async () =>
      donationListPayload(await apiFetch<unknown>('/api/donations')).map((value) =>
        normalizeDonation(value),
      ),
    enabled,
  });
}

export function useDonation(id: string | undefined, enabled = true, connectorTypeValue?: string) {
  return useQuery({
    queryKey: id
      ? [...userKeys.donation(id), connectorTypeValue ?? 'connector-unresolved']
      : [...userKeys.donations, 'none'],
    queryFn: async () => {
      if (!id) throw new ApiError('invalid_request', 'A donation id is required.', 400);
      const donationID = requiredID(id, 'donation id');
      return normalizeDonation(
        await apiFetch<unknown>(`/api/donations/${encodeURIComponent(donationID)}`),
        true,
        connectorTypeValue,
      );
    },
    enabled: enabled && Boolean(id),
  });
}

export interface DonationKeyLimitInput {
  endpoint_key_id: number;
  max_concurrency?: number;
  rpm_limit?: number;
}

export interface DonationPayload {
  description: string;
  expires_at?: number | null;
  /** Existing endpoint create wire: physical endpoint-key ids in both fields. */
  existing_endpoint?: { endpoint_id: number; key_ids: number[]; keys: DonationKeyLimitInput[] };
  /** Pending owner edit wire: physical endpoint-key ids, never donation-key ids. */
  keys?: { key_ids: number[]; limits: DonationKeyLimitInput[] };
  new_endpoint?: {
    connector_type?: string;
    base_url: string;
    note?: string;
    keys: Array<{
      secret: string;
      note?: string;
      max_concurrency?: number;
      rpm_limit?: number;
      /** OpenAI-compatible only; omitted for every other connector. */
      force_store_false?: boolean;
    }>;
  };
}

export function useDeleteDonation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const donationID = requiredID(id, 'donation id');
      try {
        await stationSessionWrite(queryClient, 'steward', () =>
          apiFetch<void>(`/api/donations/${encodeURIComponent(donationID)}`, { method: 'DELETE' }),
        );
      } catch (error) {
        // A missing donation is the authoritative result of a repeated delete.
        // Session-boundary errors must still stay silent and never become a
        // success for the newly authenticated subject.
        if (!isNotFoundError(error) || isStationSessionChanged(error)) throw error;
      }
    },
    onSuccess: (_value, id) => {
      queryClient.removeQueries({ queryKey: userKeys.donation(id), exact: false });
      void queryClient.invalidateQueries({ queryKey: userKeys.donations });
    },
  });
}
