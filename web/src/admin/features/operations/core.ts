import { apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  amount,
  array,
  boolean,
  decimal,
  decimalID,
  integer,
  invalidResponse,
  nullableDecimal,
  nullableString,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  page,
  record,
  string,
  unixSecond,
  type CursorPage,
} from '@shared/operations/wire';

export interface UsageSummary {
  total_requests: string;
  total_uncached_input_tokens: string;
  total_cache_write_input_tokens: string;
  total_cache_read_input_tokens: string;
  total_output_tokens: string;
  total_prompt_tokens: string;
  total_completion_tokens: string;
  total_unknown_usage_requests: string;
}

export function normalizeUsageSummary(value: unknown): UsageSummary {
  const fields = [
    'total_requests', 'total_uncached_input_tokens', 'total_cache_write_input_tokens',
    'total_cache_read_input_tokens', 'total_output_tokens', 'total_prompt_tokens',
    'total_completion_tokens', 'total_unknown_usage_requests',
  ] as const;
  const root = record(value, fields, 'usage summary');
  const result = Object.fromEntries(fields.map((key) => [key, decimal(root[key], `usage ${key}`)])) as unknown as UsageSummary;
  if (BigInt(result.total_prompt_tokens) !== BigInt(result.total_uncached_input_tokens)
      + BigInt(result.total_cache_write_input_tokens) + BigInt(result.total_cache_read_input_tokens)
      || BigInt(result.total_completion_tokens) !== BigInt(result.total_output_tokens)) {
    invalidResponse('usage derived totals');
  }
  return result;
}

export interface ActivityDay {
  day: string;
  product_active: string;
  api_requests: string;
  uncached_input_tokens: string;
  cache_write_input_tokens: string;
  cache_read_input_tokens: string;
  output_tokens: string;
  checkins: string;
  console_writes: string;
  game_active: string;
  game_rounds: string;
  distinct_product_users: string | null;
}

export interface ActivityPage extends CursorPage<ActivityDay> { enabled: boolean }

function normalizeActivityDay(value: unknown): ActivityDay {
  const fields = ['day', 'product_active', 'api_requests', 'uncached_input_tokens', 'cache_write_input_tokens', 'cache_read_input_tokens', 'output_tokens', 'checkins', 'console_writes', 'game_active', 'game_rounds', 'distinct_product_users'] as const;
  const root = record(value, fields, 'activity day');
  const day = string(root.day, 'activity day key', { min: 10, max: 10, ascii: true });
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) invalidResponse('activity day key');
  return {
    day,
    product_active: decimal(root.product_active, 'product active count'),
    api_requests: decimal(root.api_requests, 'API request count'),
    uncached_input_tokens: decimal(root.uncached_input_tokens, 'uncached input count'),
    cache_write_input_tokens: decimal(root.cache_write_input_tokens, 'cache-write count'),
    cache_read_input_tokens: decimal(root.cache_read_input_tokens, 'cache-read count'),
    output_tokens: decimal(root.output_tokens, 'output count'),
    checkins: decimal(root.checkins, 'check-in count'),
    console_writes: decimal(root.console_writes, 'console write count'),
    game_active: decimal(root.game_active, 'game active count'),
    game_rounds: decimal(root.game_rounds, 'game round count'),
    distinct_product_users: nullableDecimal(root.distinct_product_users, 'distinct product user count'),
  };
}

export function normalizeActivityPage(value: unknown): ActivityPage {
  const root = record(value, ['enabled', 'data', 'next_cursor'], 'activity page');
  const result = page({ data: root.data, next_cursor: root.next_cursor }, 'activity page', normalizeActivityDay);
  const enabled = boolean(root.enabled, 'activity enabled');
  if (!enabled && (result.data.length !== 0 || result.next_cursor !== null)) invalidResponse('disabled activity page');
  return { enabled, ...result };
}

export interface EndpointOverviewUser {
  user_id: string;
  endpoint_count: string;
  key_count: string;
  enabled_count: string;
}

export interface EndpointOverview {
  base_url: string;
  user_count: string;
  endpoint_count: string;
  key_count: string;
  users: EndpointOverviewUser[];
}

export function normalizeEndpointOverview(value: unknown): EndpointOverview {
  const root = record(value, ['base_url', 'user_count', 'endpoint_count', 'key_count', 'users'], 'endpoint overview');
  const users = array(root.users, 'endpoint overview users', 100).map((entry) => {
    const item = record(entry, ['user_id', 'endpoint_count', 'key_count', 'enabled_count'], 'endpoint overview user');
    return {
      user_id: decimalID(item.user_id, 'overview user id'),
      endpoint_count: decimal(item.endpoint_count, 'user endpoint count'),
      key_count: decimal(item.key_count, 'user key count'),
      enabled_count: decimal(item.enabled_count, 'user enabled count'),
    };
  });
  return {
    base_url: string(root.base_url, 'canonical endpoint URL', { min: 1, max: 4_096, bytes: 4_096 }),
    user_count: decimal(root.user_count, 'endpoint user count'),
    endpoint_count: decimal(root.endpoint_count, 'endpoint count'),
    key_count: decimal(root.key_count, 'endpoint key count'),
    users,
  };
}

const ALERT_KINDS = [
  'fetch_failed', 'forward_error', 'registration_rejected', 'maintenance_enabled',
  'donation_failure_disabled', 'issue_projection_incomplete', 'report_retry_exhausted',
  'fishing_retry_exhausted', 'rps_terminal_retrying', 'worker_checkpoint_failed',
  'invariant_violation',
] as const;

export interface AdminAlert {
  id: string;
  kind: typeof ALERT_KINDS[number];
  message: string;
  ref: string | null;
  subject_user_id: string | null;
  created_at: number;
  resolved: boolean;
  resolved_at: number | null;
}

export function normalizeAdminAlert(value: unknown): AdminAlert {
  const root = record(value, ['id', 'kind', 'message', 'ref', 'subject_user_id', 'created_at', 'resolved', 'resolved_at'], 'administrator alert');
  const resolved = boolean(root.resolved, 'alert resolution');
  const resolvedAt = nullableUnixSecond(root.resolved_at, 'alert resolution time');
  if (resolved !== (resolvedAt !== null)) invalidResponse('alert resolution state');
  return {
    id: decimalID(root.id, 'alert id'),
    kind: oneOf(root.kind, ALERT_KINDS, 'alert kind'),
    message: string(root.message, 'alert message', { max: 4_096, bytes: 4_096, multiline: true }),
    ref: nullableString(root.ref, 'alert reference', { max: 512, bytes: 2_048 }),
    subject_user_id: root.subject_user_id === null ? null : decimalID(root.subject_user_id, 'alert subject user id'),
    created_at: unixSecond(root.created_at, 'alert creation time'),
    resolved,
    resolved_at: resolvedAt,
  };
}

export interface AdminUser {
  id: string;
  discord_id: string | null;
  username: string;
  avatar_url: string | null;
  guild_nick: string | null;
  guild_avatar_url: string | null;
  is_admin: boolean;
  is_banned: boolean;
  banned_reason: string;
  banned_until: number | null;
  charity_suspended_until: number | null;
  endpoint_limit: string | null;
  effective_endpoint_limit: string;
  rpm_limit: string | null;
  effective_rpm_limit: string;
  concurrency_limit: string | null;
  effective_concurrency_limit: string;
  lang: '' | 'zh' | 'en';
  balance: string;
  donation_credit: string;
  level: { manual: number | null; automatic: number; effective: number; display_name: string };
  game_profile_public: boolean;
  revision: string;
  usage: UsageSummary;
  created_at: number;
  updated_at: number;
}

export function normalizeAdminUser(value: unknown): AdminUser {
  const root = record(value, [
    'id', 'discord_id', 'username', 'avatar_url', 'guild_nick', 'guild_avatar_url', 'is_admin', 'is_banned',
    'banned_reason', 'banned_until', 'charity_suspended_until', 'endpoint_limit', 'effective_endpoint_limit',
    'rpm_limit', 'effective_rpm_limit', 'concurrency_limit', 'effective_concurrency_limit', 'lang', 'balance',
    'donation_credit', 'level', 'game_profile_public', 'revision', 'usage', 'created_at', 'updated_at',
  ], 'administrator user');
  const level = record(root.level, ['manual', 'automatic', 'effective', 'display_name'], 'administrator user level');
  const automatic = integer(level.automatic, 'automatic level', 1, 5);
  const effective = integer(level.effective, 'effective level', 1, 5);
  const manual = level.manual === null ? null : integer(level.manual, 'manual level', 1, 5);
  const isBanned = boolean(root.is_banned, 'user banned state');
  const bannedUntil = nullableUnixSecond(root.banned_until, 'ban expiry');
  const bannedReason = string(root.banned_reason, 'ban reason', { max: 1_024, bytes: 4_096, multiline: true });
  if (!isBanned && (bannedUntil !== null || bannedReason !== '')) invalidResponse('user ban state');
  return {
    id: decimalID(root.id, 'administrator user id'),
    discord_id: nullableString(root.discord_id, 'Discord id', { max: 128, bytes: 128, ascii: true }),
    username: string(root.username, 'username', { min: 1, max: 128, bytes: 512 }),
    avatar_url: nullableString(root.avatar_url, 'avatar URL', { max: 4_096, bytes: 4_096 }),
    guild_nick: nullableString(root.guild_nick, 'guild nickname', { max: 128, bytes: 512 }),
    guild_avatar_url: nullableString(root.guild_avatar_url, 'guild avatar URL', { max: 4_096, bytes: 4_096 }),
    is_admin: boolean(root.is_admin, 'administrator marker'),
    is_banned: isBanned,
    banned_reason: bannedReason,
    banned_until: bannedUntil,
    charity_suspended_until: nullableUnixSecond(root.charity_suspended_until, 'charity suspension expiry'),
    endpoint_limit: nullableDecimal(root.endpoint_limit, 'endpoint limit'),
    effective_endpoint_limit: decimal(root.effective_endpoint_limit, 'effective endpoint limit'),
    rpm_limit: nullableDecimal(root.rpm_limit, 'RPM limit'),
    effective_rpm_limit: decimal(root.effective_rpm_limit, 'effective RPM limit'),
    concurrency_limit: nullableDecimal(root.concurrency_limit, 'concurrency limit'),
    effective_concurrency_limit: decimal(root.effective_concurrency_limit, 'effective concurrency limit'),
    lang: oneOf(root.lang, ['', 'zh', 'en'] as const, 'user language'),
    balance: amount(root.balance, 'user balance'),
    donation_credit: amount(root.donation_credit, 'donation credit', false),
    level: { manual, automatic, effective, display_name: string(level.display_name, 'level display name', { max: 64, bytes: 256 }) },
    game_profile_public: boolean(root.game_profile_public, 'game profile setting'),
    revision: decimal(root.revision, 'user revision', { positive: true }),
    usage: normalizeUsageSummary(root.usage),
    created_at: unixSecond(root.created_at, 'user creation time'),
    updated_at: unixSecond(root.updated_at, 'user update time'),
  };
}

export interface LocalText { zh: string; en: string }
export interface SiteConfigCatalogEntry {
  key: string;
  group: string;
  type: 'boolean' | 'integer' | 'amount' | 'string' | 'text' | 'enum';
  title: LocalText;
  description: LocalText;
  unit: LocalText | null;
  nullable: boolean;
  null_writable: boolean;
  raw_default: unknown;
  effective_fallback: unknown;
  minimum: unknown;
  maximum: unknown;
  step: unknown;
  allowed_values: string[];
  zero_semantics: LocalText;
  null_semantics: LocalText;
  empty_semantics: LocalText;
  independent_gates: string[];
  write_endpoint: string;
}

function localText(value: unknown, label: string): LocalText {
  const root = record(value, ['zh', 'en'], label);
  return { zh: string(root.zh, `${label} Chinese`, { max: 4_096, bytes: 8_192, multiline: true }), en: string(root.en, `${label} English`, { max: 4_096, bytes: 8_192, multiline: true }) };
}

function safeCatalogScalar(value: unknown, label: string): unknown {
  if (value === null || typeof value === 'boolean' || typeof value === 'string'
      || (typeof value === 'number' && Number.isSafeInteger(value))) return value;
  invalidResponse(label);
}

export function normalizeSiteConfigCatalogEntry(value: unknown): SiteConfigCatalogEntry {
  const fields = ['key', 'group', 'type', 'title', 'description', 'unit', 'nullable', 'null_writable', 'raw_default', 'effective_fallback', 'minimum', 'maximum', 'step', 'allowed_values', 'zero_semantics', 'null_semantics', 'empty_semantics', 'independent_gates', 'write_endpoint'] as const;
  const root = record(value, fields, 'site configuration catalog entry');
  const key = string(root.key, 'site configuration key', { min: 1, max: 128, bytes: 128, ascii: true });
  if (!/^[a-z0-9_]+$/.test(key) || key === 'default_locale') invalidResponse('site configuration key');
  const writeEndpoint = string(root.write_endpoint, 'site configuration write endpoint', { max: 256, bytes: 256, ascii: true });
  if (writeEndpoint !== '' && !writeEndpoint.startsWith('/admin/api/')) invalidResponse('site configuration write endpoint');
  return {
    key,
    group: string(root.group, 'site configuration group', { min: 1, max: 64, bytes: 64, ascii: true }),
    type: oneOf(root.type, ['boolean', 'integer', 'amount', 'string', 'text', 'enum'] as const, 'site configuration type'),
    title: localText(root.title, 'site configuration title'),
    description: localText(root.description, 'site configuration description'),
    unit: root.unit === null ? null : localText(root.unit, 'site configuration unit'),
    nullable: boolean(root.nullable, 'site configuration nullable marker'),
    null_writable: boolean(root.null_writable, 'site configuration null-write marker'),
    raw_default: safeCatalogScalar(root.raw_default, 'site configuration default'),
    effective_fallback: safeCatalogScalar(root.effective_fallback, 'site configuration fallback'),
    minimum: safeCatalogScalar(root.minimum, 'site configuration minimum'),
    maximum: safeCatalogScalar(root.maximum, 'site configuration maximum'),
    step: safeCatalogScalar(root.step, 'site configuration step'),
    allowed_values: array(root.allowed_values, 'site configuration allowed values', 128).map((item) => string(item, 'site configuration allowed value', { max: 256, bytes: 1_024 })),
    zero_semantics: localText(root.zero_semantics, 'site configuration zero semantics'),
    null_semantics: localText(root.null_semantics, 'site configuration null semantics'),
    empty_semantics: localText(root.empty_semantics, 'site configuration empty semantics'),
    independent_gates: array(root.independent_gates, 'site configuration independent gates', 64).map((item) => string(item, 'site configuration gate', { max: 128, bytes: 128, ascii: true })),
    write_endpoint: writeEndpoint,
  };
}

export interface SiteConfigBundle {
  revision: string;
  values: Record<string, string | number | boolean | null>;
  catalog: SiteConfigCatalogEntry[];
}

function normalizeConfigValue(value: unknown, label: string): string | number | boolean | null {
  if (value === null || typeof value === 'string' || typeof value === 'boolean'
      || (typeof value === 'number' && Number.isSafeInteger(value))) return value;
  return invalidResponse(label);
}

export async function getSiteConfigBundle(): Promise<SiteConfigBundle> {
  const [catalogPayload, configPayload] = await Promise.all([
    apiFetch<unknown>('/admin/api/site-config/catalog'),
    apiFetch<unknown>('/admin/api/site-config'),
  ]);
  const catalogRoot = record(catalogPayload, ['data'], 'site configuration catalog');
  const catalog = array(catalogRoot.data, 'site configuration catalog data', 1_000).map(normalizeSiteConfigCatalogEntry);
  const keys = new Set(catalog.map((entry) => entry.key));
  if (keys.size !== catalog.length) invalidResponse('site configuration catalog identity');
  const configRoot = record(configPayload, ['revision', 'values'], 'site configuration snapshot');
  const valuesRoot = record(configRoot.values, [...keys], 'site configuration values');
  const values: SiteConfigBundle['values'] = {};
  for (const [key, value] of Object.entries(valuesRoot)) {
    if (!keys.has(key) || key === 'default_locale') invalidResponse('site configuration value key');
    values[key] = normalizeConfigValue(value, `site configuration value ${key}`);
  }
  return { revision: decimal(configRoot.revision, 'site configuration revision', { positive: true }), values, catalog };
}

export type HeldObjectKind = 'maintenance_event' | 'report_case' | 'announcement_audit' | 'donation' | 'request_log';
export interface LegalHoldSummary {
  id: string;
  object_kind: HeldObjectKind;
  object_ref: string;
  state: 'active' | 'released' | 'expired';
  revision: string;
  created_at: number;
  expires_at: number;
  ended_at: number | null;
}
export interface LegalHoldDetail extends LegalHoldSummary { basis: string; end_reason: string | null }

export function normalizeLegalHoldSummary(value: unknown): LegalHoldSummary {
  const root = record(value, ['id', 'object_kind', 'object_ref', 'state', 'revision', 'created_at', 'expires_at', 'ended_at'], 'legal hold');
  const kind = oneOf(root.object_kind, ['maintenance_event', 'report_case', 'announcement_audit', 'donation', 'request_log'] as const, 'held object kind');
  const state = oneOf(root.state, ['active', 'released', 'expired'] as const, 'legal hold state');
  const objectRef = kind === 'maintenance_event'
    ? opaqueID(root.object_ref, 'op_', 'held maintenance event')
    : kind === 'report_case'
      ? opaqueID(root.object_ref, 'rpc_', 'held report case')
      : decimalID(root.object_ref, 'held object reference');
  const endedAt = nullableUnixSecond(root.ended_at, 'legal hold end time');
  if ((state === 'active') !== (endedAt === null)) invalidResponse('legal hold terminal state');
  return {
    id: opaqueID(root.id, 'lgh_', 'legal hold id'), object_kind: kind, object_ref: objectRef, state,
    revision: decimal(root.revision, 'legal hold revision', { positive: true }),
    created_at: unixSecond(root.created_at, 'legal hold creation time'),
    expires_at: unixSecond(root.expires_at, 'legal hold expiry'), ended_at: endedAt,
  };
}

export function normalizeLegalHoldDetail(value: unknown): LegalHoldDetail {
  const root = record(value, ['id', 'object_kind', 'object_ref', 'state', 'revision', 'created_at', 'expires_at', 'ended_at', 'basis', 'end_reason'], 'legal hold detail');
  const summary = normalizeLegalHoldSummary(Object.fromEntries(Object.entries(root).filter(([key]) => key !== 'basis' && key !== 'end_reason')));
  const endReason = nullableString(root.end_reason, 'legal hold end reason', { min: 1, max: 1_024, bytes: 4_096, multiline: true });
  if ((summary.state === 'active') !== (endReason === null)) invalidResponse('legal hold end reason state');
  return { ...summary, basis: string(root.basis, 'legal hold basis', { min: 1, max: 1_024, bytes: 4_096, multiline: true }), end_reason: endReason };
}

export const adminCoreKeys = {
  usage: ['admin', 'operations', 'usage'] as const,
  activity: (cursor: string | null) => ['admin', 'operations', 'activity', cursor] as const,
  endpoints: (query: string, cursor: string | null) => ['admin', 'operations', 'endpoints', query, cursor] as const,
  alerts: (resolved: string, cursor: string | null) => ['admin', 'operations', 'alerts', resolved, cursor] as const,
  users: (banned: string, query: string, cursor: string | null) => ['admin', 'operations', 'users', banned, query, cursor] as const,
  user: (id: string) => ['admin', 'operations', 'user', id] as const,
  settings: ['admin', 'operations', 'settings'] as const,
  holds: (state: string, kind: string, cursor: string | null) => ['admin', 'operations', 'holds', state, kind, cursor] as const,
  hold: (id: string) => ['admin', 'operations', 'hold', id] as const,
};

export const getAdminUsage = () => decoded('/admin/api/usage?group_by=site', normalizeUsageSummary);
export const getAdminActivity = (cursor: string | null) => decoded(queryPath('/admin/api/activity', { cursor, limit: 50 }), normalizeActivityPage);
export const getAdminEndpoints = (query: string, cursor: string | null) => decoded(queryPath('/admin/api/overview/endpoints', { q: query || undefined, cursor, limit: 50 }), (value) => page(value, 'endpoint overview page', normalizeEndpointOverview));
export const getAdminAlerts = (resolved: '' | 'true' | 'false', cursor: string | null) => decoded(queryPath('/admin/api/alerts', { resolved: resolved || undefined, cursor, limit: 50 }), (value) => page(value, 'alert page', normalizeAdminAlert));
export const setAdminAlertResolved = (id: string, resolved: boolean) => decoded(`/admin/api/alerts/${encodeURIComponent(decimalID(id, 'alert id'))}/resolve`, normalizeAdminAlert, { method: 'POST', json: { resolved } });
export const getAdminUsers = (banned: '' | 'true' | 'false', query: string, cursor: string | null) => decoded(queryPath('/admin/api/users', { is_banned: banned || undefined, q: query || undefined, cursor, limit: 50 }), (value) => page(value, 'administrator user page', normalizeAdminUser));
export const getAdminUser = (id: string) => decoded(`/admin/api/users/${encodeURIComponent(decimalID(id, 'administrator user id'))}`, normalizeAdminUser);

export function mutateAdminUser(id: string, body: unknown, key: string): Promise<AdminUser> {
  return decoded(`/admin/api/users/${encodeURIComponent(decimalID(id, 'administrator user id'))}`, normalizeAdminUser, idempotentOptions(key, { method: 'PATCH', json: body }));
}
export async function banAdminUser(id: string, body: unknown, key: string): Promise<void> {
  await apiFetch<void>(`/admin/api/users/${encodeURIComponent(decimalID(id, 'administrator user id'))}/ban`, idempotentOptions(key, { method: 'POST', json: body }));
}
export async function unbanAdminUser(id: string, revision: string, key: string): Promise<void> {
  await apiFetch<void>(`/admin/api/users/${encodeURIComponent(decimalID(id, 'administrator user id'))}/unban`, idempotentOptions(key, { method: 'POST', json: { expected_revision: revision } }));
}
export async function deleteAdminUser(id: string, revision: string, key: string, token: string): Promise<void> {
  await apiFetch<void>(`/admin/api/users/${encodeURIComponent(decimalID(id, 'administrator user id'))}`, idempotentOptions(key, { method: 'DELETE', json: { expected_revision: revision, confirmation: 'DELETE' } }, token));
}
export function patchSiteSetting(entry: SiteConfigCatalogEntry, value: unknown, key: string): Promise<{ key: string; value: unknown; revision: string }> {
  return decoded(entry.write_endpoint, (payload) => {
    const root = record(payload, ['key', 'value', 'revision'], 'site configuration mutation');
    const responseKey = string(root.key, 'site configuration result key', { min: 1, max: 128, bytes: 128, ascii: true });
    if (responseKey !== entry.key) invalidResponse('site configuration result key');
    return { key: responseKey, value: normalizeConfigValue(root.value, 'site configuration result value'), revision: decimal(root.revision, 'site configuration result revision', { positive: true }) };
  }, idempotentOptions(key, { method: 'PATCH', json: { value } }));
}
export const getLegalHolds = (state: string, kind: string, cursor: string | null, signal?: AbortSignal) => decoded(queryPath('/admin/api/legal-holds', { state: state || undefined, object_kind: kind || undefined, cursor, limit: 50 }), (value) => page(value, 'legal hold page', normalizeLegalHoldSummary), { signal });
export const getLegalHold = (id: string, signal?: AbortSignal) => decoded(`/admin/api/legal-holds/${encodeURIComponent(opaqueID(id, 'lgh_', 'legal hold id'))}`, normalizeLegalHoldDetail, { signal });
export const createLegalHold = (body: unknown, key: string, token: string) => decoded('/admin/api/legal-holds', normalizeLegalHoldDetail, idempotentOptions(key, { method: 'POST', json: body }, token));
export const releaseLegalHold = (id: string, body: unknown, key: string, token: string) => decoded(`/admin/api/legal-holds/${encodeURIComponent(opaqueID(id, 'lgh_', 'legal hold id'))}/release`, normalizeLegalHoldDetail, idempotentOptions(key, { method: 'POST', json: body }, token));
