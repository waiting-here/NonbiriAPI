import { normalizeUsageSummary } from '@shared/operations/managedUsers';
export {
  normalizeAdminUser,
  normalizeUsageSummary,
  type AdminUser,
  type UsageSummary,
} from '@shared/operations/managedUsers';
import { apiFetch } from '@shared/query/http';
import { decoded, idempotentOptions, queryPath } from '@shared/operations/api';
import {
  array,
  amount,
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

export interface ActivityDay {
  day: number;
  product_active: boolean;
  api_requests: string;
  uncached_input_tokens: string;
  cache_write_input_tokens: string;
  cache_read_input_tokens: string;
  output_tokens: string;
  checkins: string;
  game_checkins: string;
  console_writes: string;
  game_active: boolean;
  game_rounds: string;
  distinct_product_users: string | null;
}

export interface ActivityPage extends CursorPage<ActivityDay> {
  enabled: boolean;
}

function normalizeActivityDay(value: unknown): ActivityDay {
  const fields = [
    'day',
    'product_active',
    'api_requests',
    'uncached_input_tokens',
    'cache_write_input_tokens',
    'cache_read_input_tokens',
    'output_tokens',
    'checkins',
    'game_checkins',
    'console_writes',
    'game_active',
    'game_rounds',
    'distinct_product_users',
  ] as const;
  const root = record(value, fields, 'activity day');
  return {
    day: unixSecond(root.day, 'activity day key'),
    product_active: boolean(root.product_active, 'product active state'),
    api_requests: decimal(root.api_requests, 'API request count'),
    uncached_input_tokens: decimal(root.uncached_input_tokens, 'uncached input count'),
    cache_write_input_tokens: decimal(root.cache_write_input_tokens, 'cache-write count'),
    cache_read_input_tokens: decimal(root.cache_read_input_tokens, 'cache-read count'),
    output_tokens: decimal(root.output_tokens, 'output count'),
    game_checkins: decimal(root.game_checkins, 'game check-in count'),
    checkins: decimal(root.checkins, 'check-in count'),
    console_writes: decimal(root.console_writes, 'console write count'),
    game_active: boolean(root.game_active, 'game active state'),
    game_rounds: decimal(root.game_rounds, 'game round count'),
    distinct_product_users: nullableDecimal(
      root.distinct_product_users,
      'distinct product user count',
    ),
  };
}

export function normalizeActivityPage(value: unknown): ActivityPage {
  const root = record(value, ['enabled', 'data', 'next_cursor'], 'activity page');
  const result = page(
    { data: root.data, next_cursor: root.next_cursor },
    'activity page',
    normalizeActivityDay,
  );
  const enabled = boolean(root.enabled, 'activity enabled');
  if (!enabled && (result.data.length !== 0 || result.next_cursor !== null))
    invalidResponse('disabled activity page');
  return { enabled, ...result };
}

export function normalizeSiteTimezoneOffset(value: unknown): number {
  const root = record(value, ['revision', 'values'], 'site configuration snapshot');
  decimal(root.revision, 'site configuration revision', { positive: true });
  if (root.values === null || typeof root.values !== 'object' || Array.isArray(root.values)) {
    invalidResponse('site configuration values');
  }
  const offset = integer(
    (root.values as Record<string, unknown>).site_timezone_offset_minutes,
    'site timezone offset',
    -720,
    840,
  );
  if (offset % 30 !== 0) invalidResponse('site timezone offset');
  return offset;
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
  const root = record(
    value,
    ['base_url', 'user_count', 'endpoint_count', 'key_count', 'users'],
    'endpoint overview',
  );
  const users = array(root.users, 'endpoint overview users', 100).map((entry) => {
    const item = record(
      entry,
      ['user_id', 'endpoint_count', 'key_count', 'enabled_count'],
      'endpoint overview user',
    );
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

export const ALERT_KINDS = [
  'fetch_failed',
  'forward_error',
  'registration_rejected',
  'maintenance_enabled',
  'donation_failure_disabled',
  'issue_projection_incomplete',
  'report_retry_exhausted',
  'fishing_retry_exhausted',
  'rps_terminal_retrying',
  'worker_checkpoint_failed',
  'invariant_violation',
  'account_deleted',
] as const;

export interface AccountDeletion {
  user_id: string;
  discord_id: string;
  general_balance: string;
  game_balance: string;
  donation_credit: string;
  sketch_paper: string;
  sketch_brush: string;
}
function normalizeAccountDeletion(value: unknown): AccountDeletion {
  const root = record(
    value,
    [
      'user_id',
      'discord_id',
      'general_balance',
      'game_balance',
      'donation_credit',
      'sketch_paper',
      'sketch_brush',
    ],
    'account deletion',
  );
  return {
    user_id: decimalID(root.user_id, 'deleted user'),
    discord_id: string(root.discord_id, 'Discord ID', { max: 64, bytes: 256 }),
    general_balance: amount(root.general_balance, 'general balance'),
    game_balance: amount(root.game_balance, 'game balance'),
    donation_credit: amount(root.donation_credit, 'donation credit', false, (1n << 128n) - 1n),
    sketch_paper: amount(root.sketch_paper, 'paper', false),
    sketch_brush: amount(root.sketch_brush, 'brush', false),
  };
}
export interface AdminAlert {
  account_deletion?: AccountDeletion;
  id: string;
  kind: (typeof ALERT_KINDS)[number];
  message: string;
  ref: string | null;
  subject_user_id: string | null;
  created_at: number;
  resolved: boolean;
  resolved_at: number | null;
}

export function normalizeAdminAlert(value: unknown): AdminAlert {
  const fields = [
    'id',
    'kind',
    'message',
    'ref',
    'subject_user_id',
    'created_at',
    'resolved',
    'resolved_at',
  ];
  const root = record(value, [...fields, 'account_deletion'], 'administrator alert', fields);
  if ((root.kind === 'account_deleted') !== Object.hasOwn(root, 'account_deletion'))
    invalidResponse('account deletion snapshot');
  const resolved = boolean(root.resolved, 'alert resolution');
  const resolvedAt = nullableUnixSecond(root.resolved_at, 'alert resolution time');
  if (resolved !== (resolvedAt !== null)) invalidResponse('alert resolution state');
  return {
    ...(root.kind === 'account_deleted'
      ? { account_deletion: normalizeAccountDeletion(root.account_deletion) }
      : {}),
    id: decimalID(root.id, 'alert id'),
    kind: oneOf(root.kind, ALERT_KINDS, 'alert kind'),
    message: string(root.message, 'alert message', { max: 4_096, bytes: 4_096, multiline: true }),
    ref: nullableString(root.ref, 'alert reference', { max: 512, bytes: 2_048 }),
    subject_user_id:
      root.subject_user_id === null
        ? null
        : decimalID(root.subject_user_id, 'alert subject user id'),
    created_at: unixSecond(root.created_at, 'alert creation time'),
    resolved,
    resolved_at: resolvedAt,
  };
}

export interface LocalText {
  zh: string;
  en: string;
}
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
  return {
    zh: string(root.zh, `${label} Chinese`, { max: 4_096, bytes: 8_192, multiline: true }),
    en: string(root.en, `${label} English`, { max: 4_096, bytes: 8_192, multiline: true }),
  };
}

function safeCatalogScalar(value: unknown, label: string): unknown {
  if (
    value === null ||
    typeof value === 'boolean' ||
    typeof value === 'string' ||
    (typeof value === 'number' && Number.isSafeInteger(value))
  )
    return value;
  invalidResponse(label);
}

export function normalizeSiteConfigCatalogEntry(value: unknown): SiteConfigCatalogEntry {
  const fields = [
    'key',
    'group',
    'type',
    'title',
    'description',
    'unit',
    'nullable',
    'null_writable',
    'raw_default',
    'effective_fallback',
    'minimum',
    'maximum',
    'step',
    'allowed_values',
    'zero_semantics',
    'null_semantics',
    'empty_semantics',
    'independent_gates',
    'write_endpoint',
  ] as const;
  const root = record(value, fields, 'site configuration catalog entry');
  const key = string(root.key, 'site configuration key', {
    min: 1,
    max: 128,
    bytes: 128,
    ascii: true,
  });
  if (!/^[a-z0-9_]+$/.test(key) || key === 'default_locale')
    invalidResponse('site configuration key');
  const writeEndpoint = string(root.write_endpoint, 'site configuration write endpoint', {
    max: 256,
    bytes: 256,
    ascii: true,
  });
  if (writeEndpoint !== '' && !writeEndpoint.startsWith('/admin/api/'))
    invalidResponse('site configuration write endpoint');
  return {
    key,
    group: string(root.group, 'site configuration group', {
      min: 1,
      max: 64,
      bytes: 64,
      ascii: true,
    }),
    type: oneOf(
      root.type,
      ['boolean', 'integer', 'amount', 'string', 'text', 'enum'] as const,
      'site configuration type',
    ),
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
    allowed_values: array(root.allowed_values, 'site configuration allowed values', 128).map(
      (item) => string(item, 'site configuration allowed value', { max: 256, bytes: 1_024 }),
    ),
    zero_semantics: localText(root.zero_semantics, 'site configuration zero semantics'),
    null_semantics: localText(root.null_semantics, 'site configuration null semantics'),
    empty_semantics: localText(root.empty_semantics, 'site configuration empty semantics'),
    independent_gates: array(
      root.independent_gates,
      'site configuration independent gates',
      64,
    ).map((item) => string(item, 'site configuration gate', { max: 128, bytes: 128, ascii: true })),
    write_endpoint: writeEndpoint,
  };
}

export interface SiteConfigBundle {
  revision: string;
  values: Record<string, string | number | boolean | null>;
  catalog: SiteConfigCatalogEntry[];
}

function normalizeConfigValue(value: unknown, label: string): string | number | boolean | null {
  if (
    value === null ||
    typeof value === 'string' ||
    typeof value === 'boolean' ||
    (typeof value === 'number' && Number.isSafeInteger(value))
  )
    return value;
  return invalidResponse(label);
}

export async function getSiteConfigBundle(): Promise<SiteConfigBundle> {
  const [catalogPayload, configPayload] = await Promise.all([
    apiFetch<unknown>('/admin/api/site-config/catalog'),
    apiFetch<unknown>('/admin/api/site-config'),
  ]);
  const catalogRoot = record(catalogPayload, ['data'], 'site configuration catalog');
  const catalog = array(catalogRoot.data, 'site configuration catalog data', 1_000).map(
    normalizeSiteConfigCatalogEntry,
  );
  const keys = new Set(catalog.map((entry) => entry.key));
  if (keys.size !== catalog.length) invalidResponse('site configuration catalog identity');
  const configRoot = record(configPayload, ['revision', 'values'], 'site configuration snapshot');
  const valuesRoot = record(configRoot.values, [...keys], 'site configuration values');
  const values: SiteConfigBundle['values'] = {};
  for (const [key, value] of Object.entries(valuesRoot)) {
    if (!keys.has(key) || key === 'default_locale') invalidResponse('site configuration value key');
    values[key] = normalizeConfigValue(value, `site configuration value ${key}`);
  }
  return {
    revision: decimal(configRoot.revision, 'site configuration revision', { positive: true }),
    values,
    catalog,
  };
}

export type HeldObjectKind =
  'maintenance_event' | 'report_case' | 'announcement_audit' | 'donation' | 'request_log';
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
export interface LegalHoldDetail extends LegalHoldSummary {
  basis: string;
  end_reason: string | null;
}

export function normalizeLegalHoldSummary(value: unknown): LegalHoldSummary {
  const root = record(
    value,
    [
      'id',
      'object_kind',
      'object_ref',
      'state',
      'revision',
      'created_at',
      'expires_at',
      'ended_at',
    ],
    'legal hold',
  );
  const kind = oneOf(
    root.object_kind,
    ['maintenance_event', 'report_case', 'announcement_audit', 'donation', 'request_log'] as const,
    'held object kind',
  );
  const state = oneOf(root.state, ['active', 'released', 'expired'] as const, 'legal hold state');
  const objectRef =
    kind === 'maintenance_event'
      ? opaqueID(root.object_ref, 'op_', 'held maintenance event')
      : kind === 'report_case'
        ? opaqueID(root.object_ref, 'rpc_', 'held report case')
        : decimalID(root.object_ref, 'held object reference');
  const endedAt = nullableUnixSecond(root.ended_at, 'legal hold end time');
  if ((state === 'active') !== (endedAt === null)) invalidResponse('legal hold terminal state');
  return {
    id: opaqueID(root.id, 'lgh_', 'legal hold id'),
    object_kind: kind,
    object_ref: objectRef,
    state,
    revision: decimal(root.revision, 'legal hold revision', { positive: true }),
    created_at: unixSecond(root.created_at, 'legal hold creation time'),
    expires_at: unixSecond(root.expires_at, 'legal hold expiry'),
    ended_at: endedAt,
  };
}

export function normalizeLegalHoldDetail(value: unknown): LegalHoldDetail {
  const root = record(
    value,
    [
      'id',
      'object_kind',
      'object_ref',
      'state',
      'revision',
      'created_at',
      'expires_at',
      'ended_at',
      'basis',
      'end_reason',
    ],
    'legal hold detail',
  );
  const summary = normalizeLegalHoldSummary(
    Object.fromEntries(
      Object.entries(root).filter(([key]) => key !== 'basis' && key !== 'end_reason'),
    ),
  );
  const endReason = nullableString(root.end_reason, 'legal hold end reason', {
    min: 1,
    max: 1_024,
    bytes: 4_096,
    multiline: true,
  });
  if ((summary.state === 'active') !== (endReason === null))
    invalidResponse('legal hold end reason state');
  return {
    ...summary,
    basis: string(root.basis, 'legal hold basis', {
      min: 1,
      max: 1_024,
      bytes: 4_096,
      multiline: true,
    }),
    end_reason: endReason,
  };
}

export const adminCoreKeys = {
  usage: ['admin', 'operations', 'usage'] as const,
  activity: (cursor: string | null) => ['admin', 'operations', 'activity', cursor] as const,
  siteTimezone: ['admin', 'operations', 'site-timezone'] as const,
  endpoints: (query: string, cursor: string | null) =>
    ['admin', 'operations', 'endpoints', query, cursor] as const,
  alerts: (resolved: string, cursor: string | null) =>
    ['admin', 'operations', 'alerts', resolved, cursor] as const,
  users: (banned: string, query: string, cursor: string | null) =>
    ['admin', 'operations', 'users', banned, query, cursor] as const,
  user: (id: string) => ['admin', 'operations', 'user', id] as const,
  settings: ['admin', 'operations', 'settings'] as const,
  holds: (state: string, kind: string, cursor: string | null) =>
    ['admin', 'operations', 'holds', state, kind, cursor] as const,
  hold: (id: string) => ['admin', 'operations', 'hold', id] as const,
};

export const getAdminUsage = () => decoded('/admin/api/usage?group_by=site', normalizeUsageSummary);
export const getAdminActivity = (cursor: string | null) =>
  decoded(queryPath('/admin/api/activity', { cursor, limit: 50 }), normalizeActivityPage);
export const getAdminSiteTimezoneOffset = () =>
  decoded('/admin/api/site-config', normalizeSiteTimezoneOffset);
export const getAdminEndpoints = (query: string, cursor: string | null) =>
  decoded(
    queryPath('/admin/api/overview/endpoints', { q: query || undefined, cursor, limit: 50 }),
    (value) => page(value, 'endpoint overview page', normalizeEndpointOverview),
  );
export const getAdminAlerts = (resolved: '' | 'true' | 'false', cursor: string | null) =>
  decoded(
    queryPath('/admin/api/alerts', { resolved: resolved || undefined, cursor, limit: 50 }),
    (value) => page(value, 'alert page', normalizeAdminAlert),
  );
export const setAdminAlertResolved = (id: string, resolved: boolean) =>
  decoded(
    `/admin/api/alerts/${encodeURIComponent(decimalID(id, 'alert id'))}/resolve`,
    normalizeAdminAlert,
    { method: 'POST', json: { resolved } },
  );
export async function deleteAdminUser(
  id: string,
  revision: string,
  key: string,
  token: string,
): Promise<void> {
  await apiFetch<void>(
    `/admin/api/users/${encodeURIComponent(decimalID(id, 'administrator user id'))}`,
    idempotentOptions(
      key,
      { method: 'DELETE', json: { expected_revision: revision, confirmation: 'DELETE' } },
      token,
    ),
  );
}
export function patchSiteSetting(
  entry: SiteConfigCatalogEntry,
  value: unknown,
  key: string,
): Promise<{ key: string; value: unknown; revision: string }> {
  return decoded(
    entry.write_endpoint,
    (payload) => {
      const root = record(payload, ['key', 'value', 'revision'], 'site configuration mutation');
      const responseKey = string(root.key, 'site configuration result key', {
        min: 1,
        max: 128,
        bytes: 128,
        ascii: true,
      });
      if (responseKey !== entry.key) invalidResponse('site configuration result key');
      return {
        key: responseKey,
        value: normalizeConfigValue(root.value, 'site configuration result value'),
        revision: decimal(root.revision, 'site configuration result revision', { positive: true }),
      };
    },
    idempotentOptions(key, { method: 'PATCH', json: { value } }),
  );
}
export function patchSiteSettings(
  input: { expected_revision: string; values: SiteConfigBundle['values'] },
  key: string,
): Promise<{ revision: string; changed_keys: string[] }> {
  return decoded(
    '/admin/api/site-config',
    (payload) => {
      const root = record(payload, ['revision', 'changed_keys'], 'site configuration update');
      const changed = array(root.changed_keys, 'changed configuration keys', 64).map((value) =>
        string(value, 'configuration key', { min: 1, max: 128, ascii: true }),
      );
      if (
        changed.some((name) => !Object.hasOwn(input.values, name)) ||
        new Set(changed).size !== changed.length
      )
        invalidResponse('changed configuration keys');
      return {
        revision: decimal(root.revision, 'site configuration revision', { positive: true }),
        changed_keys: changed,
      };
    },
    idempotentOptions(key, { method: 'PATCH', json: input }),
  );
}
export const getLegalHolds = (
  state: string,
  kind: string,
  cursor: string | null,
  signal?: AbortSignal,
) =>
  decoded(
    queryPath('/admin/api/legal-holds', {
      state: state || undefined,
      object_kind: kind || undefined,
      cursor,
      limit: 50,
    }),
    (value) => page(value, 'legal hold page', normalizeLegalHoldSummary),
    { signal },
  );
export const getLegalHold = (id: string, signal?: AbortSignal) =>
  decoded(
    `/admin/api/legal-holds/${encodeURIComponent(opaqueID(id, 'lgh_', 'legal hold id'))}`,
    normalizeLegalHoldDetail,
    { signal },
  );
export const createLegalHold = (body: unknown, key: string, token: string) =>
  decoded(
    '/admin/api/legal-holds',
    normalizeLegalHoldDetail,
    idempotentOptions(key, { method: 'POST', json: body }, token),
  );
export const releaseLegalHold = (id: string, body: unknown, key: string, token: string) =>
  decoded(
    `/admin/api/legal-holds/${encodeURIComponent(opaqueID(id, 'lgh_', 'legal hold id'))}/release`,
    normalizeLegalHoldDetail,
    idempotentOptions(key, { method: 'POST', json: body }, token),
  );

export const resolveAdminAlerts = (ids: string[]) =>
  decoded(
    '/admin/api/alerts/resolve',
    (value) => {
      const root = record(value, ['resolved_count'], 'bulk resolution');
      const count = integer(root.resolved_count, 'resolved count', 1, 100);
      if (count !== ids.length) invalidResponse('bulk resolution count');
      return count;
    },
    { method: 'POST', json: { ids: ids.map((id) => decimalID(id, 'alert id')) } },
  );
