import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiFetch } from '@shared/query/http';
import {
  asArray,
  asRecord,
  booleanValue,
  dateValue,
  idValue,
  integerValue,
  isListPayload,
  listResult,
  optionalText,
  text,
  type UnknownRecord,
} from '@shared/query/normalize';

export const ADMIN_PAGE_SIZE = 20;

export interface AdminPage<T> {
  items: T[];
  hasNext: boolean;
  nextCursor?: string;
}

export interface AdminSessionResponse {
  admin: { username: string };
}

export interface AdminUser {
  id: string;
  username: string;
  avatar?: string;
  discord_id: string;
  is_banned: boolean;
  banned_reason: string;
  /** Nullable ban deadline as unix seconds; absent while there is none. */
  banned_until?: number;
  endpoint_limit?: number;
  effective_endpoint_limit: number;
  rpm_limit?: number;
  effective_rpm_limit: number;
  concurrency_limit?: number;
  effective_concurrency_limit: number;
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_unknown_usage_requests: number;
  /** Canonical decimal milli-credit string; the signed consumption balance. */
  credits_balance: string;
  /** Canonical decimal milli-credit string; the non-negative donor reward. */
  donation_credit_balance: string;
  /** Manual level override (1..5); absent means no override (automatic). */
  level?: number;
  /** Persisted automatic high-water level (1..4). */
  auto_level: number;
  created_at: string;
}

/**
 * One metadata-only administrator log row (frozen projection). There is no
 * user-chosen platform model name and no note field on this shape; the
 * normalizer whitelists exactly these fields, so anything else a faulty
 * server might send is dropped before it can reach the query cache.
 */
export interface AdminRequestLog {
  id: string;
  user_id: string;
  route_kind: string;
  endpoint_base_url: string;
  endpoint_key_id: string;
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

/** Frozen administrator filter set — exact equality on the server. */
export interface AdminLogFilter {
  userId?: string;
  endpointBaseURL?: string;
  upstreamModel?: string;
  errorCode?: string;
  status?: string;
  fromUnix?: number;
  toUnix?: number;
}

export interface AdminUsage {
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_unknown_usage_requests: number;
}

/** One user's expandable entry inside a grouped overview row (frozen metadata only). */
export interface AdminEndpointUserSummary {
  user_id: string;
  endpoint_count: number;
  key_count: number;
  enabled_count: number;
}

/**
 * One canonical base_url's admin overview group. The normalizer whitelists
 * exactly these fields, so a note, username, Discord identifier, or secret
 * can never enter the request cache even if a faulty server sent one.
 */
export interface AdminEndpointOverviewGroup {
  base_url: string;
  user_count: number;
  endpoint_count: number;
  key_count: number;
  users: AdminEndpointUserSummary[];
}

export interface AdminAlert {
  id: string;
  kind: string;
  message: string;
  ref: string;
  subject_user_id: string;
  created_at: string;
  resolved: boolean;
  resolved_at: string;
}

export type SiteConfigValue = string | number | boolean | null;
export type SiteConfig = Record<string, SiteConfigValue>;

export interface LocalizedCatalogText {
  zh: string;
  en: string;
}

export type SiteConfigCatalogValueType =
  | 'text'
  | 'locale'
  | 'optional_locale'
  | 'integer'
  | 'optional_integer'
  | 'boolean'
  | 'multiline_text'
  | 'amount'
  | 'optional_amount'
  | 'enum';

export interface SiteConfigCatalogEntry {
  key: string;
  group: string;
  value_type: SiteConfigCatalogValueType;
  title: LocalizedCatalogText;
  description: LocalizedCatalogText;
  unit: LocalizedCatalogText;
  nullable: boolean;
  null_writable?: boolean;
  raw_default: SiteConfigValue;
  effective_fallback: SiteConfigValue;
  minimum: string | number | null;
  maximum: string | number | null;
  step?: string | number | null;
  allowed_values?: string[];
  zero_semantics: LocalizedCatalogText | null;
  null_semantics: LocalizedCatalogText;
  empty_semantics?: LocalizedCatalogText;
  independent_gates: LocalizedCatalogText[];
  write_endpoint: string;
}

export const adminKeys = {
  all: ['admin'] as const,
  session: ['admin', 'session'] as const,
  users: (page: number, pageSize: number, isBanned?: boolean, q?: string) =>
    [
      'admin',
      'users',
      page,
      pageSize,
      isBanned === undefined ? 'all' : String(isBanned),
      q ?? '',
    ] as const,
  usersRoot: ['admin', 'users'] as const,
  logs: (page: number, filter: AdminLogFilter, pageSize?: number) =>
    [
      'admin',
      'logs',
      page,
      filter.userId ?? '',
      filter.endpointBaseURL ?? '',
      filter.upstreamModel ?? '',
      filter.errorCode ?? '',
      filter.status ?? '',
      filter.fromUnix ?? '',
      filter.toUnix ?? '',
      pageSize ?? ADMIN_PAGE_SIZE,
    ] as const,
  logsRoot: ['admin', 'logs'] as const,
  usage: ['admin', 'usage'] as const,
  endpoints: (page: number, pageSize: number, filter: string) =>
    ['admin', 'endpoints', page, pageSize, filter] as const,
  alerts: (page: number, resolved?: boolean, pageSize?: number) =>
    [
      'admin',
      'alerts',
      page,
      resolved === undefined ? 'all' : String(resolved),
      pageSize ?? ADMIN_PAGE_SIZE,
    ] as const,
  alertsRoot: ['admin', 'alerts'] as const,
  siteConfig: ['admin', 'site-config'] as const,
  siteConfigCatalog: ['admin', 'site-config', 'catalog'] as const,
};

function recordValue(record: UnknownRecord, key: string): unknown {
  return record[key];
}

// Economy amounts stay canonical decimal milli-credit strings end to end (the
// shared formatter renders them; they never pass through Number()). A
// malformed value stays '' and renders as a placeholder.
function amountValue(value: unknown): string {
  return typeof value === 'string' && value.length <= 32 ? value : '';
}

// Nullable integer projection: present only for a finite number.
function optionalNumberValue(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function invalidResponse(message: string): never {
  throw new ApiError('invalid_response', message, 200);
}

function boundedInteger(
  value: unknown,
  minimum: number,
  maximum: number,
  field: string,
): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) {
    return invalidResponse(`The server returned an invalid ${field}.`);
  }
  return value as number;
}

function optionalBoundedInteger(
  value: unknown,
  minimum: number,
  maximum: number,
  field: string,
): number | undefined {
  if (value === null || value === undefined) return undefined;
  return boundedInteger(value, minimum, maximum, field);
}

interface PagePayload {
  items: unknown[];
  hasNext: boolean;
  nextCursor?: string;
}

function pagePayload(payload: unknown, pageSize = ADMIN_PAGE_SIZE): PagePayload {
  if (!isListPayload(payload)) {
    throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
  }
  const record = asRecord(payload);
  const result = listResult(payload, pageSize + 1);
  const explicitHasNext = record?.has_more ?? record?.hasMore ?? record?.next_page;
  const hasNext =
    typeof explicitHasNext === 'boolean'
      ? explicitHasNext
      : typeof explicitHasNext === 'number'
        ? explicitHasNext > 0
        : result.items.length > pageSize;
  const items = result.items.slice(0, pageSize);
  const lastRecord = asRecord(items[items.length - 1]);
  const cursor = lastRecord ? idValue(recordValue(lastRecord, 'id')) : undefined;
  return {
    items,
    hasNext,
    ...(hasNext && cursor && cursor !== '—' ? { nextCursor: cursor } : {}),
  };
}

function normalizeSession(payload: unknown): AdminSessionResponse {
  const record = asRecord(payload);
  const admin = record ? asRecord(record.admin) : null;
  if (!admin) throw new ApiError('invalid_response', 'The server returned an invalid session.', 200);
  const username = text(recordValue(admin, 'username'), 128);
  if (!username) throw new ApiError('invalid_response', 'The server returned an invalid session.', 200);
  return { admin: { username } };
}

export function normalizeAdminUser(payload: unknown): AdminUser {
  const record = asRecord(payload) ?? {};
  const endpointLimit = recordValue(record, 'endpoint_limit');
  const rpmLimit = recordValue(record, 'rpm_limit');
  const concurrencyLimit = recordValue(record, 'concurrency_limit');
  const bannedUntil = optionalNumberValue(recordValue(record, 'banned_until'));
  const level = optionalNumberValue(recordValue(record, 'level'));
  const autoLevel = optionalNumberValue(recordValue(record, 'auto_level'));
  const explicitEndpointLimit = optionalBoundedInteger(endpointLimit, 0, 10_000, 'endpoint limit');
  const explicitRPMLimit = optionalBoundedInteger(rpmLimit, 1, 4_096, 'RPM limit');
  const explicitConcurrencyLimit = optionalBoundedInteger(
    concurrencyLimit, 1, 100_000, 'concurrency limit',
  );
  return {
    id: idValue(recordValue(record, 'id')),
    username: text(recordValue(record, 'username'), 128, '—'),
    ...(optionalText(recordValue(record, 'avatar'), 512)
      ? { avatar: optionalText(recordValue(record, 'avatar'), 512) }
      : {}),
    discord_id: text(recordValue(record, 'discord_id'), 128, '—'),
    is_banned: booleanValue(recordValue(record, 'is_banned')),
    banned_reason: text(recordValue(record, 'banned_reason'), 512),
    ...(bannedUntil !== undefined && bannedUntil >= 0
      ? { banned_until: Math.trunc(bannedUntil) }
      : {}),
    ...(explicitEndpointLimit !== undefined
      ? { endpoint_limit: explicitEndpointLimit }
      : {}),
    effective_endpoint_limit: boundedInteger(
      recordValue(record, 'effective_endpoint_limit'), 0, 10_000, 'effective endpoint limit',
    ),
    ...(explicitRPMLimit !== undefined
      ? { rpm_limit: explicitRPMLimit }
      : {}),
    effective_rpm_limit: boundedInteger(
      recordValue(record, 'effective_rpm_limit'), 1, 4_096, 'effective RPM limit',
    ),
    ...(explicitConcurrencyLimit !== undefined
      ? { concurrency_limit: explicitConcurrencyLimit }
      : {}),
    effective_concurrency_limit: boundedInteger(
      recordValue(record, 'effective_concurrency_limit'), 1, 100_000, 'effective concurrency limit',
    ),
    total_requests: Math.max(0, integerValue(recordValue(record, 'total_requests'))),
    total_prompt_tokens: Math.max(0, integerValue(recordValue(record, 'total_prompt_tokens'))),
    total_completion_tokens: Math.max(0, integerValue(recordValue(record, 'total_completion_tokens'))),
    total_unknown_usage_requests: Math.max(
      0,
      integerValue(recordValue(record, 'total_unknown_usage_requests')),
    ),
    credits_balance: amountValue(recordValue(record, 'credits_balance')),
    donation_credit_balance: amountValue(recordValue(record, 'donation_credit_balance')),
    ...(level !== undefined && level >= 1 && level <= 5 ? { level: Math.trunc(level) } : {}),
    auto_level:
      autoLevel !== undefined && autoLevel >= 1 && autoLevel <= 4 ? Math.trunc(autoLevel) : 1,
    created_at: dateValue(recordValue(record, 'created_at')),
  };
}

function unixSecondsValue(value: unknown): number {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
    return Math.trunc(value);
  }
  return 0;
}

function normalizeLog(payload: unknown): AdminRequestLog {
  const record = asRecord(payload) ?? {};
  const bucket = (key: string): number => {
    const raw = recordValue(record, key);
    return typeof raw === 'number' && Number.isSafeInteger(raw) && raw >= 0 ? raw : 0;
  };
  return {
    id: idValue(recordValue(record, 'id')),
    user_id: idValue(recordValue(record, 'user_id')),
    route_kind: text(recordValue(record, 'route_kind'), 64, '—'),
    endpoint_base_url: text(recordValue(record, 'endpoint_base_url'), 2048, '—'),
    endpoint_key_id: idValue(recordValue(record, 'endpoint_key_id')),
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

function normalizeUsage(payload: unknown): AdminUsage {
  const record = asRecord(payload);
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

function normalizeEndpointUser(payload: unknown): AdminEndpointUserSummary {
  const record = asRecord(payload) ?? {};
  return {
    user_id: idValue(recordValue(record, 'user_id')),
    endpoint_count: Math.max(0, integerValue(recordValue(record, 'endpoint_count'))),
    key_count: Math.max(0, integerValue(recordValue(record, 'key_count'))),
    enabled_count: Math.max(0, integerValue(recordValue(record, 'enabled_count'))),
  };
}

// Field whitelist only: any extra server field (note, username, Discord ID,
// secret material) is dropped here before it can reach the query cache.
function normalizeEndpointGroup(payload: unknown): AdminEndpointOverviewGroup {
  const record = asRecord(payload) ?? {};
  return {
    base_url: text(recordValue(record, 'base_url'), 2048, '—'),
    user_count: Math.max(0, integerValue(recordValue(record, 'user_count'))),
    endpoint_count: Math.max(0, integerValue(recordValue(record, 'endpoint_count'))),
    key_count: Math.max(0, integerValue(recordValue(record, 'key_count'))),
    users: asArray(recordValue(record, 'users')).map(normalizeEndpointUser),
  };
}

function normalizeAlert(payload: unknown): AdminAlert {
  const record = asRecord(payload) ?? {};
  return {
    id: idValue(recordValue(record, 'id')),
    kind: text(recordValue(record, 'kind'), 96, '—'),
    message: text(recordValue(record, 'message'), 1024, '—'),
    ref: text(recordValue(record, 'ref'), 256),
    subject_user_id: idValue(recordValue(record, 'subject_user_id')),
    created_at: dateValue(recordValue(record, 'created_at')),
    resolved: booleanValue(recordValue(record, 'resolved')),
    resolved_at: dateValue(recordValue(record, 'resolved_at')),
  };
}

const SITE_CONFIG_CATALOG_TYPES = new Set<SiteConfigCatalogValueType>([
  'text', 'locale', 'optional_locale', 'integer', 'optional_integer', 'boolean',
  'multiline_text', 'amount', 'optional_amount', 'enum',
]);

function legalTextBytes(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

const LOSSLESS_LEGAL_CONFIG_KEYS = new Set([
  'legal_privacy_override_zh',
  'legal_privacy_override_en',
  'legal_terms_override_zh',
  'legal_terms_override_en',
]);

const CANONICAL_DECIMAL = /^-?(0|[1-9][0-9]*)$/;

function hasDisallowedControl(value: string, legalText: boolean): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code === 0x7f || (code < 0x20 && (!legalText || (code !== 0x09 && code !== 0x0a && code !== 0x0d)))) {
      return true;
    }
  }
  return false;
}

function canonicalCatalogAmount(value: unknown, entry: SiteConfigCatalogEntry): string {
  if (typeof value !== 'string' || !CANONICAL_DECIMAL.test(value) || value === '-0') {
    return invalidResponse(`The server returned an invalid ${entry.key} amount.`);
  }
  try {
    const parsed = BigInt(value);
    if (typeof entry.minimum !== 'string' || typeof entry.maximum !== 'string' ||
        !CANONICAL_DECIMAL.test(entry.minimum) || !CANONICAL_DECIMAL.test(entry.maximum) ||
        parsed < BigInt(entry.minimum) || parsed > BigInt(entry.maximum)) {
      return invalidResponse(`The server returned an out-of-range ${entry.key} amount.`);
    }
  } catch {
    return invalidResponse(`The server returned an invalid ${entry.key} amount.`);
  }
  return value;
}

function normalizeCatalogConfigValue(raw: unknown, entry: SiteConfigCatalogEntry): SiteConfigValue {
  if (raw === null) {
    if (entry.nullable) return null;
    return invalidResponse(`The server returned null for non-nullable ${entry.key}.`);
  }
  switch (entry.value_type) {
    case 'boolean':
      if (typeof raw !== 'boolean') return invalidResponse(`The server returned an invalid ${entry.key} boolean.`);
      return raw;
    case 'integer':
    case 'optional_integer': {
      if (!Number.isSafeInteger(raw)) return invalidResponse(`The server returned an invalid ${entry.key} integer.`);
      const parsed = raw as number;
      if (typeof entry.minimum !== 'number' || typeof entry.maximum !== 'number' ||
          parsed < entry.minimum || parsed > entry.maximum ||
          (typeof entry.step === 'number' && parsed % entry.step !== 0)) {
        return invalidResponse(`The server returned an out-of-range ${entry.key} integer.`);
      }
      return parsed;
    }
    case 'amount':
    case 'optional_amount':
      return canonicalCatalogAmount(raw, entry);
    case 'locale':
    case 'optional_locale':
    case 'enum': {
      if (typeof raw !== 'string' || hasDisallowedControl(raw, false) ||
          !entry.allowed_values || entry.allowed_values.length === 0 ||
          (!entry.allowed_values.includes(raw) && raw !== entry.raw_default)) {
        return invalidResponse(`The server returned an invalid ${entry.key} choice.`);
      }
      return raw;
    }
    case 'text':
    case 'multiline_text': {
      if (typeof raw !== 'string') return invalidResponse(`The server returned an invalid ${entry.key} text.`);
      const legal = LOSSLESS_LEGAL_CONFIG_KEYS.has(entry.key);
      const maximum = legal ? 65_536 : entry.maximum;
      if (typeof maximum !== 'number' || !Number.isSafeInteger(maximum) || maximum < 0 ||
          legalTextBytes(raw) > maximum || hasDisallowedControl(raw, legal)) {
        return invalidResponse(`The server returned an invalid ${entry.key} text.`);
      }
      // Only the four legal override values are lossless large-body text.
      // No trimming or newline conversion is permitted on those fields.
      return raw;
    }
  }
  return invalidResponse(`The server returned an unsupported ${entry.key} value type.`);
}

export function normalizeSiteConfig(
  payload: unknown,
  catalog: readonly SiteConfigCatalogEntry[],
): SiteConfig {
  const record = asRecord(payload);
  if (!record) return invalidResponse('The server returned an invalid site configuration.');
  const byKey = new Map(catalog.map((entry) => [entry.key, entry]));
  const result: SiteConfig = {};
  for (const [rawKey, raw] of Object.entries(record)) {
    if (!rawKey || rawKey.length > 128) {
      return invalidResponse('The server returned an invalid site configuration key.');
    }
    const entry = byKey.get(rawKey);
    if (!entry) return invalidResponse('The server returned an unknown site configuration key.');
    result[rawKey] = normalizeCatalogConfigValue(raw, entry);
  }
  return result;
}

function localizedCatalogText(value: unknown, field: string): LocalizedCatalogText {
  const record = asRecord(value);
  if (!record || typeof record.zh !== 'string' || typeof record.en !== 'string' ||
      !record.zh.trim() || !record.en.trim() || record.zh.length > 4_096 || record.en.length > 4_096) {
    return invalidResponse(`The server returned invalid catalog ${field}.`);
  }
  return { zh: record.zh, en: record.en };
}

function catalogScalar(value: unknown, field: string): SiteConfigValue {
  if (value === null || typeof value === 'boolean' || typeof value === 'string') return value;
  if (typeof value === 'number' && Number.isSafeInteger(value)) return value;
  return invalidResponse(`The server returned invalid catalog ${field}.`);
}

function catalogBound(value: unknown, field: string): string | number | null {
  if (value === null) return value;
  if (typeof value === 'string' && CANONICAL_DECIMAL.test(value) && value !== '-0') return value;
  if (typeof value === 'number' && Number.isSafeInteger(value)) return value;
  return invalidResponse(`The server returned invalid catalog ${field}.`);
}

export function normalizeSiteConfigCatalog(payload: unknown): SiteConfigCatalogEntry[] {
  const root = asRecord(payload);
  if (!root || !Array.isArray(root.data)) {
    return invalidResponse('The server returned an invalid configuration catalog.');
  }
  const seen = new Set<string>();
  let previous = '';
  return root.data.map((value, index) => {
    const record = asRecord(value);
    if (!record || typeof record.key !== 'string' || !record.key || record.key.length > 128 ||
        typeof record.group !== 'string' || !record.group || record.group.length > 64 ||
        typeof record.value_type !== 'string' ||
        !SITE_CONFIG_CATALOG_TYPES.has(record.value_type as SiteConfigCatalogValueType) ||
        typeof record.nullable !== 'boolean' ||
        typeof record.write_endpoint !== 'string' ||
        !record.write_endpoint.startsWith('/') || record.write_endpoint.length > 256 ||
        !Array.isArray(record.independent_gates)) {
      return invalidResponse(`The server returned an invalid catalog entry at ${index}.`);
    }
    if (record.null_writable !== undefined && typeof record.null_writable !== 'boolean') {
      return invalidResponse('The server returned invalid catalog null write semantics.');
    }
    if (record.allowed_values !== undefined && !Array.isArray(record.allowed_values)) {
      return invalidResponse('The server returned invalid catalog allowed values.');
    }
    if (record.step !== undefined && record.step !== null &&
        typeof record.step !== 'string' && !Number.isSafeInteger(record.step)) {
      return invalidResponse('The server returned invalid catalog step.');
    }
    if (seen.has(record.key) || (previous && previous >= record.key)) {
      return invalidResponse('The configuration catalog is not uniquely key-sorted.');
    }
    previous = record.key;
    seen.add(record.key);
    const allowedValues = (record.allowed_values as unknown[] | undefined)?.map((item) => {
      if (typeof item !== 'string' || item.length > 256) {
        return invalidResponse('The server returned invalid catalog allowed values.');
      }
      return item;
    });
    if (allowedValues && new Set(allowedValues).size !== allowedValues.length) {
      return invalidResponse('The server returned duplicate catalog allowed values.');
    }
    const zeroSemantics = record.zero_semantics === null
      ? null
      : localizedCatalogText(record.zero_semantics, 'zero semantics');
    const emptySemantics = record.empty_semantics === undefined
      ? undefined
      : localizedCatalogText(record.empty_semantics, 'empty semantics');
    return {
      key: record.key,
      group: record.group,
      value_type: record.value_type as SiteConfigCatalogValueType,
      title: localizedCatalogText(record.title, 'title'),
      description: localizedCatalogText(record.description, 'description'),
      unit: localizedCatalogText(record.unit, 'unit'),
      nullable: record.nullable,
      ...(record.null_writable === undefined ? {} : { null_writable: record.null_writable }),
      raw_default: catalogScalar(record.raw_default, 'raw default'),
      effective_fallback: catalogScalar(record.effective_fallback, 'effective fallback'),
      minimum: catalogBound(record.minimum, 'minimum'),
      maximum: catalogBound(record.maximum, 'maximum'),
      ...(record.step === undefined ? {} : { step: catalogBound(record.step, 'step') }),
      ...(allowedValues === undefined ? {} : { allowed_values: allowedValues }),
      zero_semantics: zeroSemantics,
      null_semantics: localizedCatalogText(record.null_semantics, 'null semantics'),
      ...(emptySemantics === undefined ? {} : { empty_semantics: emptySemantics }),
      independent_gates: record.independent_gates.map((gate) => localizedCatalogText(gate, 'gate')),
      write_endpoint: record.write_endpoint,
    };
  });
}

export function useAdminSession() {
  return useQuery({
    queryKey: adminKeys.session,
    queryFn: async () => normalizeSession(await apiFetch<unknown>('/admin/api/session')),
    staleTime: 15_000,
    retry: false,
  });
}

export function useAdminUsers(
  page: number,
  pageSize = ADMIN_PAGE_SIZE,
  isBanned?: boolean,
  q?: string,
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (isBanned !== undefined) params.set('is_banned', isBanned ? 'true' : 'false');
  if (q) params.set('q', q);
  return useQuery({
    queryKey: adminKeys.users(page, pageSize, isBanned, q),
    queryFn: async () => {
      const result = pagePayload(
        await apiFetch<unknown>(`/admin/api/users?${params}`),
        pageSize,
      );
      return {
        items: result.items.map(normalizeAdminUser),
        hasNext: result.hasNext,
        ...(result.nextCursor ? { nextCursor: result.nextCursor } : {}),
      } satisfies AdminPage<AdminUser>;
    },
    enabled,
  });
}

/**
 * Server-paginated administrator log list. The frozen filter set is exact
 * equality on the server; the client sends only non-empty single values and
 * never a platform model name or note filter.
 */
export function useAdminLogs(
  page: number,
  filter: AdminLogFilter = {},
  pageSize = ADMIN_PAGE_SIZE,
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
    queryKey: adminKeys.logs(page, filter, pageSize),
    queryFn: async () => {
      const result = pagePayload(await apiFetch<unknown>(`/admin/api/logs?${params}`), pageSize);
      return {
        items: result.items.map(normalizeLog),
        hasNext: result.hasNext,
        ...(result.nextCursor ? { nextCursor: result.nextCursor } : {}),
      } satisfies AdminPage<AdminRequestLog>;
    },
    enabled,
  });
}

/**
 * Builds the admin export download path (CSV or JSON) for the current
 * filter. Export endpoints reject paging parameters, so none are sent; the
 * browser only ever follows this link — it never parses or renders the file.
 */
export function adminLogExportPath(filter: AdminLogFilter, format: 'csv' | 'json'): string {
  const params = new URLSearchParams();
  if (filter.userId) params.set('user_id', filter.userId);
  if (filter.endpointBaseURL) params.set('endpoint_base_url', filter.endpointBaseURL);
  if (filter.upstreamModel) params.set('upstream_model', filter.upstreamModel);
  if (filter.errorCode) params.set('error_code', filter.errorCode);
  if (filter.status) params.set('status', filter.status);
  if (filter.fromUnix !== undefined) params.set('from', String(filter.fromUnix));
  if (filter.toUnix !== undefined) params.set('to', String(filter.toUnix));
  const query = params.toString();
  return `/admin/api/logs/export.${format}${query ? `?${query}` : ''}`;
}

export function useAdminUsage(enabled = true) {
  return useQuery({
    queryKey: adminKeys.usage,
    queryFn: async () => normalizeUsage(await apiFetch<unknown>('/admin/api/usage')),
    enabled,
  });
}

/**
 * Server-paginated grouped endpoint overview. Page, page size, and the
 * literal base_url substring filter are all server-evaluated; the client
 * never rebuilds a full cross-user list and never merges URLs itself.
 */
export function useAdminEndpointOverview(
  page: number,
  pageSize = ADMIN_PAGE_SIZE,
  filter = '',
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (filter) params.set('filter', filter);
  return useQuery({
    queryKey: adminKeys.endpoints(page, pageSize, filter),
    queryFn: async () => {
      const payload = await apiFetch<unknown>(`/admin/api/overview/endpoints?${params}`);
      if (!isListPayload(payload)) {
        throw new ApiError('invalid_response', 'The server returned an invalid list.', 200);
      }
      const result = listResult(payload, pageSize);
      return {
        items: result.items.map(normalizeEndpointGroup),
        hasMore: result.hasNext,
      } satisfies { items: AdminEndpointOverviewGroup[]; hasMore: boolean };
    },
    enabled,
  });
}

export function useAdminAlerts(
  page: number,
  resolved?: boolean,
  pageSize = ADMIN_PAGE_SIZE,
  enabled = true,
) {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (resolved !== undefined) params.set('resolved', resolved ? 'true' : 'false');
  return useQuery({
    queryKey: adminKeys.alerts(page, resolved, pageSize),
    queryFn: async () => {
      const result = pagePayload(await apiFetch<unknown>(`/admin/api/alerts?${params}`), pageSize);
      return {
        items: result.items.map(normalizeAlert),
        hasNext: result.hasNext,
        ...(result.nextCursor ? { nextCursor: result.nextCursor } : {}),
      } satisfies AdminPage<AdminAlert>;
    },
    enabled,
  });
}

export function useSiteConfig(
  catalog: readonly SiteConfigCatalogEntry[] | undefined,
  enabled = true,
) {
  return useQuery({
    queryKey: adminKeys.siteConfig,
    queryFn: async () => normalizeSiteConfig(
      await apiFetch<unknown>('/admin/api/site-config'),
      catalog ?? [],
    ),
    enabled: enabled && catalog !== undefined,
    staleTime: 0,
  });
}

export function useSiteConfigCatalog(enabled = true) {
  return useQuery({
    queryKey: adminKeys.siteConfigCatalog,
    queryFn: async () => normalizeSiteConfigCatalog(
      await apiFetch<unknown>('/admin/api/site-config/catalog'),
    ),
    enabled,
    staleTime: 30_000,
  });
}

export function useResolveAdminAlert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ alertId, resolved }: { alertId: string; resolved: boolean }) =>
      apiFetch<unknown>(`/admin/api/alerts/${encodeURIComponent(alertId)}/resolve`, {
        method: 'POST',
        json: { resolved },
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.alertsRoot });
    },
  });
}

export interface AdminCreditAdjustmentInput {
  userId: string;
  /** Canonical decimal milli-credit delta; omitted when the field is blank. */
  creditsDelta?: string;
  /** Canonical decimal milli-credit delta; omitted when the field is blank. */
  donationCreditDelta?: string;
  /**
   * Idempotency key. One key per logical confirmation: a retry of the same
   * submission reuses it verbatim (the server returns the first application's
   * result), a changed submission gets a fresh key.
   */
  operationId: string;
  /** Bounded, non-empty reason required by the server for every adjustment. */
  reason: string;
}

/**
 * The economy mode of PATCH /admin/api/users/{id}: canonical decimal-string
 * DELTAS (never target balances) plus the mandatory operation_id and reason.
 * The request carries no profile/ban field — the modes are mutually exclusive
 * server-side. The response's credit balances are the balances as of THIS
 * operation's application (the first one on an operation-id replay), so the
 * returned row is rendered as-is; the user list is refetched, never patched.
 */
export function useAdminAdjustUserCredits() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: AdminCreditAdjustmentInput) => {
      const json: Record<string, string> = {
        operation_id: input.operationId,
        reason: input.reason,
      };
      if (input.creditsDelta !== undefined) json.credits = input.creditsDelta;
      if (input.donationCreditDelta !== undefined) json.donation_credit = input.donationCreditDelta;
      const payload = await apiFetch<unknown>(
        `/admin/api/users/${encodeURIComponent(input.userId)}`,
        { method: 'PATCH', json },
      );
      if (!asRecord(payload)) {
        throw new ApiError('invalid_response', 'The server returned an invalid user.', 200);
      }
      return normalizeAdminUser(payload);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });
}

/**
 * The level tri-state of the profile mode: a number 1..5 sets the manual
 * override, null resets to the automatic high-water mark. Only the level
 * field is sent (profile mode must not mix with economy fields).
 */
export function useAdminUpdateUserLevel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ userId, level }: { userId: string; level: number | null }) => {
      const payload = await apiFetch<unknown>(`/admin/api/users/${encodeURIComponent(userId)}`, {
        method: 'PATCH',
        json: { level },
      });
      if (!asRecord(payload)) {
        throw new ApiError('invalid_response', 'The server returned an invalid user.', 200);
      }
      return normalizeAdminUser(payload);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });
}

export interface AdminBanInput {
  userId: string;
  /** Optional bounded reason. */
  reason?: string;
  /**
   * Ban lifetime in seconds. Omitted (undefined) means a permanent ban; the
   * server stores the resulting absolute deadline and reports banned_until.
   */
  durationSeconds?: number;
}

/** POST /admin/api/users/{id}/ban — sessions and caller keys die atomically. */
export function useAdminBanUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, reason, durationSeconds }: AdminBanInput) =>
      apiFetch<void>(`/admin/api/users/${encodeURIComponent(userId)}/ban`, {
        method: 'POST',
        json: {
          ...(reason ? { reason } : {}),
          ...(durationSeconds !== undefined ? { duration_seconds: durationSeconds } : {}),
        },
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });
}

/** POST /admin/api/users/{id}/unban — no body; the user re-issues their key. */
export function useAdminUnbanUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      apiFetch<void>(`/admin/api/users/${encodeURIComponent(userId)}/unban`, { method: 'POST' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });
}
