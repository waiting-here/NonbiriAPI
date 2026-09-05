import { ApiError } from '@shared/query/http';
import {
  CONNECTOR_TYPES,
  type AccountLanguage,
  type AffectedModel,
  type Binding,
  type BindingCandidate,
  type BindingsResponse,
  type CandidateFilters,
  type CallerKeyAuthority,
  type CallerKeyMetadata,
  type CallerKeySecret,
  type CatalogEntry,
  type CatalogSourceType,
  type CatalogView,
  type ConnectorType,
  type DiscoveryAccepted,
  type DiscoveryEvidence,
  type DiscoverySafeClass,
  type Endpoint,
  type EndpointCreateOptions,
  type EndpointOrigin,
  type EndpointKey,
  type HomeAnnouncementSummary,
  type HomeCheckinResult,
  type HomeCheckinStatus,
  type HomeGameSummary,
  type MainstreamChannelOption,
  type ManualEntriesResponse,
  type ManualUpdateResponse,
  type Model,
  type Page,
  type UsageSummary,
  type UserEnvelope,
  type UserProfile,
} from './types';

type UnknownRecord = Record<string, unknown>;

const MAX_PAGE_ITEMS = 100;
const MAX_BINDINGS = 256;
const MAX_UNIX_SECOND = 253_402_300_799;
const MAX_I64 = 9_223_372_036_854_775_807n;
const MAX_U128 = 340_282_366_920_938_463_463_374_607_431_768_211_455n;
const MAX_SM128_MAGNITUDE = 170_141_183_460_469_231_731_687_303_715_884_105_727n;
const DECIMAL = /^(0|[1-9][0-9]*)$/;
const POSITIVE_DECIMAL = /^[1-9][0-9]*$/;
const CREDIT_AMOUNT = /^-?(0|[1-9][0-9]*)(?:\.[0-9]{1,3})?$/;
const NONNEGATIVE_CREDIT_AMOUNT = /^(0|[1-9][0-9]*)(?:\.[0-9]{1,3})?$/;
const CALLER_SECRET = /^nbk_[A-Za-z0-9_-]{42}[AQgw]$/;
const OPERATION_ID = /^op_[A-Za-z0-9_-]{21}[AQgw]$/;
const IDEMPOTENCY_KEY = /^[A-Za-z0-9_-]{22,128}$/;

function invalid(label: string): never {
  throw new ApiError('invalid_response', `The server returned an invalid ${label}.`, 200);
}

function asRecord(value: unknown, label: string): UnknownRecord {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalid(label);
  return value as UnknownRecord;
}

function exactRecord(
  value: unknown,
  required: readonly string[],
  optional: readonly string[] = [],
  label: string,
): UnknownRecord {
  const record = asRecord(value, label);
  const allowed = new Set([...required, ...optional]);
  const keys = Object.keys(record);
  if (
    keys.some((key) => !allowed.has(key)) ||
    required.some((key) => !Object.hasOwn(record, key))
  ) {
    invalid(label);
  }
  return record;
}

function hasForbiddenScalar(value: string): boolean {
  for (const character of value) {
    const point = character.codePointAt(0) ?? 0;
    if (point < 32 || (point >= 127 && point <= 159) || (point >= 0xd800 && point <= 0xdfff))
      return true;
  }
  return false;
}

function scalarString(
  value: unknown,
  maximum: number,
  label: string,
  options: { allowEmpty?: boolean; rejectEdgeWhitespace?: boolean } = {},
): string {
  if (typeof value !== 'string' || hasForbiddenScalar(value)) invalid(label);
  const scalars = Array.from(value);
  if ((!options.allowEmpty && scalars.length === 0) || scalars.length > maximum) invalid(label);
  if (
    options.rejectEdgeWhitespace &&
    value.length > 0 &&
    (/^\p{White_Space}/u.test(value) || /\p{White_Space}$/u.test(value))
  ) {
    invalid(label);
  }
  return value;
}

function exactBoolean(value: unknown, label: string): boolean {
  if (typeof value !== 'boolean') invalid(label);
  return value;
}

function unixTime(value: unknown, label: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < 0 || (value as number) > MAX_UNIX_SECOND)
    invalid(label);
  return value as number;
}

function nullableTime(value: unknown, label: string): number | null {
  return value === null ? null : unixTime(value, label);
}

function decimal(
  value: unknown,
  label: string,
  positive = false,
  maximum: bigint = MAX_I64,
): string {
  if (
    typeof value !== 'string' ||
    value.length > 39 ||
    !(positive ? POSITIVE_DECIMAL : DECIMAL).test(value)
  ) {
    invalid(label);
  }
  try {
    if (BigInt(value) > maximum) invalid(label);
  } catch {
    invalid(label);
  }
  return value;
}

function id(value: unknown, label: string): string {
  return decimal(value, label, true);
}

function nullableDecimal(value: unknown, label: string): string | null {
  return value === null ? null : decimal(value, label);
}

function u128(value: unknown, label: string): string {
  return decimal(value, label, false, MAX_U128);
}

function creditAmount(value: unknown, label: string, signed: boolean): string {
  if (
    typeof value !== 'string' ||
    value.length > 80 ||
    !(signed ? CREDIT_AMOUNT : NONNEGATIVE_CREDIT_AMOUNT).test(value)
  ) {
    invalid(label);
  }
  if (value === '-0' || value.startsWith('-0.')) invalid(label);
  const unsigned = value.startsWith('-') ? value.slice(1) : value;
  const [integer = '', fraction = ''] = unsigned.split('.');
  if (fraction.endsWith('0')) invalid(label);
  try {
    const milli = BigInt(integer) * 1_000n + BigInt(fraction.padEnd(3, '0') || '0');
    if (milli > (signed ? MAX_SM128_MAGNITUDE : MAX_U128)) invalid(label);
  } catch {
    invalid(label);
  }
  return value;
}

function creditMilli(value: string): bigint {
  const negative = value.startsWith('-');
  const unsigned = negative ? value.slice(1) : value;
  const [integer = '', fraction = ''] = unsigned.split('.');
  const result = BigInt(integer) * 1_000n + BigInt(fraction.padEnd(3, '0') || '0');
  return negative ? -result : result;
}

function httpsURL(value: unknown, label: string): string {
  const candidate = scalarString(value, 2_048, label);
  let parsed: URL;
  try {
    parsed = new URL(candidate);
  } catch {
    invalid(label);
  }
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password) invalid(label);
  return candidate;
}

function nullableHTTPSURL(value: unknown, label: string): string | null {
  return value === null ? null : httpsURL(value, label);
}

function connectorType(value: unknown): ConnectorType {
  if (typeof value !== 'string' || !CONNECTOR_TYPES.includes(value as ConnectorType)) {
    invalid('connector type');
  }
  return value as ConnectorType;
}

function boundedArray<T>(
  value: unknown,
  normalize: (item: unknown) => T,
  label: string,
  maximum = MAX_PAGE_ITEMS,
): T[] {
  if (!Array.isArray(value) || value.length > maximum) invalid(label);
  return value.map(normalize);
}

function nullableCursor(value: unknown): string | null {
  if (value === null) return null;
  return scalarString(value, 512, 'pagination cursor');
}

function uniqueBy<T>(values: readonly T[], key: (value: T) => string, label: string): void {
  const seen = new Set<string>();
  for (const value of values) {
    const candidate = key(value);
    if (seen.has(candidate)) invalid(label);
    seen.add(candidate);
  }
}

function opaqueID(value: unknown, prefix: string, label: string): string {
  const expectedLength = prefix.length + 22;
  const candidate = scalarString(value, expectedLength, label);
  const suffix = candidate.slice(prefix.length);
  if (
    candidate.length !== expectedLength ||
    !candidate.startsWith(prefix) ||
    !/^[A-Za-z0-9_-]{22}$/.test(suffix) ||
    !/[AQgw]$/.test(suffix)
  ) {
    invalid(label);
  }
  return candidate;
}

function wireText(
  value: unknown,
  maximumScalars: number,
  maximumBytes: number,
  label: string,
  allowEmpty = false,
): string {
  const candidate = scalarString(value, maximumScalars, label, { allowEmpty });
  if (new TextEncoder().encode(candidate).byteLength > maximumBytes) invalid(label);
  return candidate;
}

export function normalizePage<T>(
  value: unknown,
  normalize: (item: unknown) => T,
  label: string,
): Page<T> {
  const record = exactRecord(value, ['data', 'next_cursor'], [], `${label} page`);
  const data = boundedArray(record.data, normalize, label);
  uniqueBy(data, (item) => JSON.stringify(item), `${label} page`);
  return { data, next_cursor: nullableCursor(record.next_cursor) };
}

export function normalizeHomeCheckinStatus(value: unknown): HomeCheckinStatus {
  const root = asRecord(value, 'check-in status');
  if (root.enabled === false) {
    exactRecord(value, ['enabled'], [], 'check-in status');
    return { enabled: false };
  }
  const record = exactRecord(
    value,
    ['enabled', 'checked_in_today', 'balance', 'award_min', 'award_max', 'balance_cap'],
    [],
    'check-in status',
  );
  if (!exactBoolean(record.enabled, 'check-in enabled state')) invalid('check-in status');
  const awardMin = creditAmount(record.award_min, 'check-in minimum award', false);
  const awardMax = creditAmount(record.award_max, 'check-in maximum award', false);
  if (creditMilli(awardMin) > creditMilli(awardMax)) invalid('check-in award range');
  return {
    enabled: true,
    checked_in_today: exactBoolean(record.checked_in_today, 'check-in day state'),
    balance: creditAmount(record.balance, 'check-in balance', true),
    award_min: awardMin,
    award_max: awardMax,
    balance_cap: creditAmount(record.balance_cap, 'check-in balance cap', false),
  };
}

export function normalizeHomeCheckinResult(value: unknown): HomeCheckinResult {
  const record = exactRecord(value, ['award', 'balance'], [], 'check-in result');
  return {
    award: creditAmount(record.award, 'check-in award', false),
    balance: creditAmount(record.balance, 'check-in balance', true),
  };
}

function normalizeHomeContinue(value: unknown): HomeGameSummary {
  const record = exactRecord(
    value,
    ['game', 'resource_id', 'state', 'route_id'],
    [],
    'home game continuation',
  );
  if (
    record.game === 'fishing' &&
    record.route_id === 'game-fishing' &&
    (record.state === 'settlement_pending' || record.state === 'recovery_required')
  ) {
    return {
      game: record.game,
      route_id: record.route_id,
      kind: 'continue',
      resource_id: opaqueID(record.resource_id, 'fb_', 'fishing batch id'),
      state: record.state,
    };
  }
  if (
    record.game === 'linklink' &&
    record.route_id === 'game-linklink' &&
    record.state === 'active'
  ) {
    return {
      game: record.game,
      route_id: record.route_id,
      kind: 'continue',
      resource_id: opaqueID(record.resource_id, 'll_', 'LinkLink session id'),
      state: record.state,
    };
  }
  if (
    record.game === 'rps' &&
    record.route_id === 'game-rps' &&
    (record.state === 'started' || record.state === 'terminal_processing')
  ) {
    return {
      game: record.game,
      route_id: record.route_id,
      kind: 'continue',
      resource_id: opaqueID(record.resource_id, 'rps_', 'RPS session id'),
      state: record.state,
    };
  }
  return invalid('home game continuation');
}

function normalizeHomePendingResult(value: unknown): HomeGameSummary {
  const record = exactRecord(
    value,
    ['game', 'resource_id', 'created_at', 'route_id'],
    [],
    'home pending game result',
  );
  if (record.game === 'fishing' && record.route_id === 'game-fishing') {
    return {
      game: record.game,
      route_id: record.route_id,
      kind: 'view',
      resource_id: opaqueID(record.resource_id, 'fb_', 'fishing batch id'),
      created_at: unixTime(record.created_at, 'pending fishing result time'),
    };
  }
  if (record.game === 'rps' && record.route_id === 'game-rps') {
    return {
      game: record.game,
      route_id: record.route_id,
      kind: 'view',
      resource_id: opaqueID(record.resource_id, 'rps_', 'RPS session id'),
      created_at: unixTime(record.created_at, 'pending RPS result time'),
    };
  }
  return invalid('home pending game result');
}

export function normalizeHomeGameSummary(value: unknown): HomeGameSummary[] {
  const record = exactRecord(value, ['continue', 'pending_results'], [], 'home game summary');
  const continuations = boundedArray(
    record.continue,
    normalizeHomeContinue,
    'home game continuations',
  );
  const pending = boundedArray(
    record.pending_results,
    normalizeHomePendingResult,
    'home pending game results',
  );
  const result = [...continuations, ...pending];
  uniqueBy(result, (item) => `${item.game}:${item.resource_id}`, 'home game summary');
  return result;
}

function normalizeHomeAnnouncement(value: unknown): HomeAnnouncementSummary {
  const record = exactRecord(
    value,
    [
      'epoch',
      'id',
      'revision',
      'severity',
      'pinned',
      'dismissible',
      'published_at',
      'expires_at',
      'effective_language',
      'fallback_from',
      'title',
      'excerpt',
    ],
    [],
    'announcement summary',
  );
  if (!['info', 'warning', 'important'].includes(record.severity as string))
    invalid('announcement severity');
  if (record.effective_language !== 'zh' && record.effective_language !== 'en')
    invalid('announcement language');
  if (
    record.fallback_from !== null &&
    record.fallback_from !== 'zh' &&
    record.fallback_from !== 'en'
  ) {
    invalid('announcement fallback language');
  }
  opaqueID(record.epoch, 'b1e_', 'announcement epoch');
  decimal(record.revision, 'announcement revision', true);
  exactBoolean(record.pinned, 'announcement pinned state');
  exactBoolean(record.dismissible, 'announcement dismissible state');
  unixTime(record.published_at, 'announcement publish time');
  nullableTime(record.expires_at, 'announcement expiry');
  return {
    id: opaqueID(record.id, 'ann_', 'announcement id'),
    title: wireText(record.title, 160, 640, 'announcement title'),
    excerpt: wireText(record.excerpt, 240, 960, 'announcement excerpt', true),
  };
}

export function normalizeHomeAnnouncementPage(value: unknown): Page<HomeAnnouncementSummary> {
  const result = normalizePage(value, normalizeHomeAnnouncement, 'announcements');
  uniqueBy(result.data, (item) => item.id, 'announcement page');
  return result;
}

export function normalizeUsageSummary(value: unknown): UsageSummary {
  const fields = [
    'total_requests',
    'total_uncached_input_tokens',
    'total_cache_write_input_tokens',
    'total_cache_read_input_tokens',
    'total_output_tokens',
    'total_prompt_tokens',
    'total_completion_tokens',
    'total_unknown_usage_requests',
  ] as const;
  const record = exactRecord(value, fields, [], 'usage summary');
  const result = {
    total_requests: u128(record.total_requests, 'request count'),
    total_uncached_input_tokens: u128(
      record.total_uncached_input_tokens,
      'uncached input token count',
    ),
    total_cache_write_input_tokens: u128(
      record.total_cache_write_input_tokens,
      'cache write token count',
    ),
    total_cache_read_input_tokens: u128(
      record.total_cache_read_input_tokens,
      'cache read token count',
    ),
    total_output_tokens: u128(record.total_output_tokens, 'output token count'),
    total_prompt_tokens: u128(record.total_prompt_tokens, 'prompt token count'),
    total_completion_tokens: u128(record.total_completion_tokens, 'completion token count'),
    total_unknown_usage_requests: u128(record.total_unknown_usage_requests, 'unknown usage count'),
  };
  if (
    BigInt(result.total_prompt_tokens) !==
      BigInt(result.total_uncached_input_tokens) +
        BigInt(result.total_cache_write_input_tokens) +
        BigInt(result.total_cache_read_input_tokens) ||
    result.total_completion_tokens !== result.total_output_tokens ||
    BigInt(result.total_unknown_usage_requests) > BigInt(result.total_requests)
  ) {
    invalid('usage summary projection');
  }
  return result;
}

export function normalizeUserProfile(value: unknown): UserProfile {
  const fields = [
    'id',
    'username',
    'avatar',
    'avatar_url',
    'guild_nick',
    'guild_avatar_url',
    'lang',
    'is_banned',
    'banned_until',
    'charity_suspended_until',
    'endpoint_limit',
    'effective_endpoint_limit',
    'rpm_limit',
    'effective_rpm_limit',
    'concurrency_limit',
    'effective_concurrency_limit',
    'balance',
    'donation_credit',
    'effective_level',
    'level_display_name',
    'game_profile_public',
    'created_at',
    'updated_at',
    'usage',
  ] as const;
  const record = exactRecord(value, fields, [], 'user profile');
  const lang = record.lang;
  if (lang !== '' && lang !== 'zh' && lang !== 'en') invalid('account language');
  if (
    !Number.isSafeInteger(record.effective_level) ||
    (record.effective_level as number) < 1 ||
    (record.effective_level as number) > 5
  ) {
    invalid('effective level');
  }
  const createdAt = unixTime(record.created_at, 'account creation time');
  const updatedAt = unixTime(record.updated_at, 'account update time');
  if (updatedAt < createdAt) invalid('account update time');
  return {
    id: id(record.id, 'user id'),
    username: scalarString(record.username, 128, 'username'),
    avatar: record.avatar === null ? null : scalarString(record.avatar, 512, 'avatar hash'),
    avatar_url: nullableHTTPSURL(record.avatar_url, 'avatar URL'),
    guild_nick:
      record.guild_nick === null
        ? null
        : scalarString(record.guild_nick, 128, 'guild nickname', { allowEmpty: true }),
    guild_avatar_url: nullableHTTPSURL(record.guild_avatar_url, 'guild avatar URL'),
    lang: lang as AccountLanguage,
    is_banned: exactBoolean(record.is_banned, 'ban state'),
    banned_until: nullableTime(record.banned_until, 'ban deadline'),
    charity_suspended_until: nullableTime(
      record.charity_suspended_until,
      'charity suspension deadline',
    ),
    endpoint_limit: nullableDecimal(record.endpoint_limit, 'endpoint limit'),
    effective_endpoint_limit: decimal(record.effective_endpoint_limit, 'effective endpoint limit'),
    rpm_limit: nullableDecimal(record.rpm_limit, 'RPM limit'),
    effective_rpm_limit: decimal(record.effective_rpm_limit, 'effective RPM limit'),
    concurrency_limit: nullableDecimal(record.concurrency_limit, 'concurrency limit'),
    effective_concurrency_limit: decimal(
      record.effective_concurrency_limit,
      'effective concurrency limit',
    ),
    balance: creditAmount(record.balance, 'balance', true),
    donation_credit: creditAmount(record.donation_credit, 'donation credit', false),
    effective_level: record.effective_level as UserProfile['effective_level'],
    level_display_name: scalarString(record.level_display_name, 64, 'level display name', {
      allowEmpty: true,
    }),
    game_profile_public: exactBoolean(record.game_profile_public, 'game profile setting'),
    created_at: createdAt,
    updated_at: updatedAt,
    usage: normalizeUsageSummary(record.usage),
  };
}

export function normalizeUserEnvelope(value: unknown): UserEnvelope {
  const record = exactRecord(value, ['user'], [], 'user envelope');
  return { user: normalizeUserProfile(record.user) };
}

export function normalizeEndpointOrigin(value: unknown): EndpointOrigin {
  const root = asRecord(value, 'endpoint origin');
  if (root.kind === 'custom') {
    exactRecord(value, ['kind'], [], 'endpoint origin');
    return { kind: 'custom' };
  }
  if (root.kind === 'mainstream') {
    const record = exactRecord(value, ['kind', 'channel_id', 'name'], [], 'endpoint origin');
    return {
      kind: 'mainstream',
      channel_id: opaqueID(record.channel_id, 'mch_', 'endpoint origin channel id'),
      name: scalarString(record.name, 128, 'endpoint origin channel name', {
        rejectEdgeWhitespace: true,
      }),
    };
  }
  invalid('endpoint origin');
}

export function normalizeMainstreamChannelOption(value: unknown): MainstreamChannelOption {
  const record = exactRecord(
    value,
    ['id', 'name', 'connector_type', 'base_url'],
    [],
    'mainstream channel option',
  );
  return {
    id: opaqueID(record.id, 'mch_', 'mainstream channel option id'),
    name: scalarString(record.name, 128, 'mainstream channel option name', {
      rejectEdgeWhitespace: true,
    }),
    connector_type: connectorType(record.connector_type),
    base_url: normalizeBaseURL(record.base_url, 'mainstream channel option base URL'),
  };
}

export function normalizeEndpointCreateOptions(value: unknown): EndpointCreateOptions {
  const record = exactRecord(
    value,
    ['base_connector_types', 'mainstream_channels'],
    [],
    'endpoint creation options',
  );
  const baseConnectorTypes = boundedArray(
    record.base_connector_types,
    (item) => connectorType(item),
    'base connector types',
    CONNECTOR_TYPES.length,
  );
  uniqueBy(baseConnectorTypes, (item) => item, 'base connector types');
  const mainstreamChannels = boundedArray(
    record.mainstream_channels,
    normalizeMainstreamChannelOption,
    'mainstream channel options',
  );
  uniqueBy(mainstreamChannels, (item) => item.id, 'mainstream channel option ids');
  if (mainstreamChannels.some((item) => !baseConnectorTypes.includes(item.connector_type))) {
    invalid('mainstream channel option connector type');
  }
  return {
    base_connector_types: baseConnectorTypes,
    mainstream_channels: mainstreamChannels,
  };
}

export function normalizeEndpoint(value: unknown): Endpoint {
  const record = exactRecord(
    value,
    [
      'id',
      'connector_type',
      'base_url',
      'origin',
      'note',
      'enabled',
      'revision',
      'key_count',
      'created_at',
      'updated_at',
    ],
    [],
    'endpoint',
  );
  const createdAt = unixTime(record.created_at, 'endpoint creation time');
  const updatedAt = unixTime(record.updated_at, 'endpoint update time');
  if (updatedAt < createdAt) invalid('endpoint update time');
  return {
    id: id(record.id, 'endpoint id'),
    connector_type: connectorType(record.connector_type),
    base_url: normalizeBaseURL(record.base_url, 'endpoint base URL'),
    origin: normalizeEndpointOrigin(record.origin),
    note: scalarString(record.note, 1_024, 'endpoint note', { allowEmpty: true }),
    enabled: exactBoolean(record.enabled, 'endpoint enabled state'),
    revision: decimal(record.revision, 'endpoint revision', true),
    key_count: decimal(record.key_count, 'endpoint key count'),
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

export function normalizeEndpointKey(value: unknown): EndpointKey {
  const record = exactRecord(
    value,
    [
      'id',
      'endpoint_id',
      'display_head',
      'display_tail',
      'note',
      'enabled',
      'force_store_false',
      'suspension_state',
      'revision',
      'created_at',
      'updated_at',
    ],
    ['max_concurrency', 'max_rpm'],
    'endpoint key',
  );
  if (record.suspension_state !== 'none' && record.suspension_state !== 'security_processing') {
    invalid('endpoint key suspension state');
  }
  const createdAt = unixTime(record.created_at, 'endpoint key creation time');
  const updatedAt = unixTime(record.updated_at, 'endpoint key update time');
  if (updatedAt < createdAt) invalid('endpoint key update time');
  return {
    id: id(record.id, 'endpoint key id'),
    endpoint_id: id(record.endpoint_id, 'endpoint owner id'),
    display_head: asciiFragment(record.display_head, 'key display head'),
    display_tail: asciiFragment(record.display_tail, 'key display tail'),
    note: scalarString(record.note, 1_024, 'endpoint key note', { allowEmpty: true }),
    enabled: exactBoolean(record.enabled, 'endpoint key enabled state'),
    force_store_false: exactBoolean(record.force_store_false, 'endpoint key store policy'),
    max_concurrency: keyRoutingLimit(record.max_concurrency),
    max_rpm: keyRoutingLimit(record.max_rpm),
    suspension_state: record.suspension_state,
    revision: decimal(record.revision, 'endpoint key revision', true),
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

function keyRoutingLimit(value: unknown): number {
  if (value === undefined) return 0;
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0 || value > 2_147_483_647)
    invalid('endpoint key routing limit');
  return value;
}

export function normalizeCallerKeyMetadata(value: unknown): CallerKeyMetadata {
  const record = exactRecord(
    value,
    ['display', 'created_at', 'updated_at', 'generation'],
    [],
    'CallerKey metadata',
  );
  const display = scalarString(record.display, 13, 'CallerKey display');
  if (!/^nbk_[A-Za-z0-9_-]{4}…[A-Za-z0-9_-]{4}$/.test(display)) invalid('CallerKey display');
  const createdAt = unixTime(record.created_at, 'CallerKey creation time');
  const updatedAt = unixTime(record.updated_at, 'CallerKey update time');
  if (updatedAt < createdAt) invalid('CallerKey update time');
  return {
    display,
    created_at: createdAt,
    updated_at: updatedAt,
    generation: decimal(record.generation, 'CallerKey generation', true),
  };
}

export function normalizeCallerKeyAuthority(
  value: unknown,
  generationHeader: string | null,
): CallerKeyAuthority {
  const generation = decimal(generationHeader, 'CallerKey generation header');
  if (value === null) return { generation, metadata: null };
  const metadata = normalizeCallerKeyMetadata(value);
  if (metadata.generation !== generation) invalid('CallerKey generation projection');
  return { generation, metadata };
}

export function normalizeCallerKeySecret(
  value: unknown,
  expectedGeneration: string,
): CallerKeySecret {
  const record = exactRecord(value, ['secret', 'metadata'], [], 'CallerKey one-time response');
  const secret = scalarString(record.secret, 47, 'CallerKey secret');
  if (!CALLER_SECRET.test(secret)) invalid('CallerKey secret');
  const metadata = normalizeCallerKeyMetadata(record.metadata);
  let expectedNext: string;
  try {
    const expected = BigInt(decimal(expectedGeneration, 'CallerKey generation'));
    if (expected >= MAX_I64) invalid('CallerKey generation transition');
    expectedNext = (expected + 1n).toString();
  } catch {
    invalid('CallerKey generation');
  }
  if (metadata.generation !== expectedNext) invalid('CallerKey generation transition');
  return { secret, metadata };
}

function catalogSource(value: unknown): CatalogSourceType {
  if (value !== 'automatic' && value !== 'manual') invalid('catalog source type');
  return value;
}

export function normalizeDiscoveryEvidence(value: unknown): DiscoveryEvidence {
  const record = exactRecord(
    value,
    ['state', 'revision', 'result', 'safe_class', 'observed_at', 'count'],
    [],
    'discovery evidence',
  );
  const state = record.state;
  if (state !== 'unknown' && state !== 'checking' && state !== 'succeeded' && state !== 'failed') {
    invalid('discovery state');
  }
  const revision = decimal(record.revision, 'discovery revision', true);
  if (state === 'unknown') {
    if (
      record.result !== null ||
      record.safe_class !== 'none' ||
      record.observed_at !== null ||
      record.count !== null
    ) {
      invalid('unknown discovery evidence');
    }
    return { state, revision, result: null, safe_class: 'none', observed_at: null, count: null };
  }
  if (state === 'checking') {
    if (
      record.result !== null ||
      record.safe_class !== 'none' ||
      record.observed_at === null ||
      record.count !== null
    ) {
      invalid('checking discovery evidence');
    }
    return {
      state,
      revision,
      result: null,
      safe_class: 'none',
      observed_at: unixTime(record.observed_at, 'discovery start time'),
      count: null,
    };
  }
  if (state === 'succeeded') {
    if (
      (record.result !== 'empty' && record.result !== 'nonempty') ||
      record.safe_class !== 'none' ||
      record.observed_at === null
    ) {
      invalid('successful discovery evidence');
    }
    const count = decimal(record.count, 'discovery result count');
    if (
      (record.result === 'empty' && count !== '0') ||
      (record.result === 'nonempty' && count === '0')
    ) {
      invalid('discovery result count');
    }
    return {
      state,
      revision,
      result: record.result,
      safe_class: 'none',
      observed_at: unixTime(record.observed_at, 'discovery completion time'),
      count,
    };
  }
  const failureClasses: DiscoverySafeClass[] = [
    'auth',
    'rate_limit',
    'timeout',
    'protocol',
    'transport',
    'interrupted',
  ];
  if (
    record.result !== null ||
    typeof record.safe_class !== 'string' ||
    !failureClasses.includes(record.safe_class as DiscoverySafeClass) ||
    record.observed_at === null ||
    record.count !== null
  ) {
    invalid('failed discovery evidence');
  }
  return {
    state,
    revision,
    result: null,
    safe_class: record.safe_class as DiscoverySafeClass,
    observed_at: unixTime(record.observed_at, 'discovery failure time'),
    count: null,
  };
}

function manualValue(value: unknown, maximum: number, allowEmpty: boolean, label: string): string {
  return scalarString(value, maximum, label, {
    allowEmpty,
    rejectEdgeWhitespace: typeof value === 'string' && value.length > 0,
  });
}

export function validateManualValue(value: string, maximum: number, allowEmpty: boolean): string {
  const validated = clientScalarString(value, maximum, 'manual catalog value', allowEmpty);
  if (validated && (/^\p{White_Space}/u.test(validated) || /\p{White_Space}$/u.test(validated))) {
    throw new ApiError('invalid_request', 'Invalid manual catalog value.', 400);
  }
  return validated;
}

function logicalName(value: unknown, label: string): string {
  return scalarString(value, 64, label, { rejectEdgeWhitespace: true });
}

export function validateLogicalName(value: string): string {
  const validated = clientScalarString(value, 64, 'logical model name', false);
  if (/^\p{White_Space}/u.test(validated) || /\p{White_Space}$/u.test(validated)) {
    throw new ApiError('invalid_request', 'Invalid logical model name.', 400);
  }
  return validated;
}

export function validatePersonalProviderName(value: string): string {
  const validated = validateLogicalName(value);
  if (validated.startsWith('[公益]')) {
    throw new ApiError('invalid_request', 'Invalid logical model provider.', 400);
  }
  return validated;
}

export function validateEndpointSecret(value: string): string {
  const validated = clientScalarString(value, 65_536, 'endpoint key secret', false);
  if (new TextEncoder().encode(validated).byteLength > 65_536) {
    throw new ApiError('invalid_request', 'Invalid endpoint key secret.', 400);
  }
  return validated;
}

export function validateIdempotencyKey(value: string): string {
  if (!IDEMPOTENCY_KEY.test(value))
    throw new ApiError('invalid_request', 'Invalid operation identity.', 400);
  return value;
}

export function normalizeCatalogEntry(value: unknown): CatalogEntry {
  const record = exactRecord(
    value,
    [
      'id',
      'source_type',
      'upstream_model_id',
      'provider',
      'source_revision',
      'pair_revision',
      'created_at',
      'updated_at',
    ],
    [],
    'catalog entry',
  );
  const sourceType = catalogSource(record.source_type);
  const createdAt = unixTime(record.created_at, 'catalog creation time');
  const updatedAt = unixTime(record.updated_at, 'catalog update time');
  if (updatedAt < createdAt) invalid('catalog update time');
  return {
    id: id(record.id, 'catalog entry id'),
    source_type: sourceType,
    upstream_model_id: manualValue(record.upstream_model_id, 512, false, 'upstream model id'),
    provider: manualValue(record.provider, 128, true, 'catalog provider'),
    source_revision: decimal(record.source_revision, 'catalog source revision', true),
    pair_revision: decimal(record.pair_revision, 'catalog pair revision', true),
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

export function normalizeCatalogView(value: unknown): CatalogView {
  const record = exactRecord(
    value,
    ['evidence', 'automatic_entries', 'manual_entries', 'next_cursor'],
    [],
    'catalog view',
  );
  const automaticEntries = boundedArray(
    record.automatic_entries,
    normalizeCatalogEntry,
    'automatic catalog',
  );
  const manualEntries = boundedArray(
    record.manual_entries,
    normalizeCatalogEntry,
    'manual catalog',
  );
  if (automaticEntries.length + manualEntries.length > MAX_PAGE_ITEMS) invalid('catalog page');
  if (
    automaticEntries.some((entry) => entry.source_type !== 'automatic') ||
    manualEntries.some((entry) => entry.source_type !== 'manual')
  ) {
    invalid('catalog source projection');
  }
  const evidence = normalizeDiscoveryEvidence(record.evidence);
  if (
    evidence.state === 'succeeded' &&
    evidence.result === 'empty' &&
    automaticEntries.length !== 0
  ) {
    invalid('empty discovery catalog');
  }
  uniqueBy([...automaticEntries, ...manualEntries], (entry) => entry.id, 'catalog entry ids');
  return {
    evidence,
    automatic_entries: automaticEntries,
    manual_entries: manualEntries,
    next_cursor: nullableCursor(record.next_cursor),
  };
}

export function normalizeModel(value: unknown): Model {
  const record = exactRecord(
    value,
    [
      'id',
      'provider',
      'model',
      'full_name',
      'route_strategy',
      'silent_retry',
      'flatten_tool_calls',
      'revision',
      'binding_revision',
      'binding_count',
      'created_at',
      'updated_at',
    ],
    [],
    'logical model',
  );
  const provider = logicalName(record.provider, 'logical model provider');
  if (provider.startsWith('[公益]')) invalid('logical model provider');
  const model = logicalName(record.model, 'logical model name');
  const fullName = scalarString(record.full_name, 129, 'logical model full name');
  if (fullName !== `${provider}/${model}`) invalid('logical model full name');
  if (record.route_strategy !== 'ordered' && record.route_strategy !== 'random')
    invalid('route strategy');
  const createdAt = unixTime(record.created_at, 'logical model creation time');
  const updatedAt = unixTime(record.updated_at, 'logical model update time');
  if (updatedAt < createdAt) invalid('logical model update time');
  return {
    id: id(record.id, 'logical model id'),
    provider,
    model,
    full_name: fullName,
    route_strategy: record.route_strategy,
    silent_retry: exactBoolean(record.silent_retry, 'silent retry setting'),
    flatten_tool_calls: exactBoolean(record.flatten_tool_calls, 'tool call setting'),
    revision: decimal(record.revision, 'logical model revision', true),
    binding_revision: decimal(record.binding_revision, 'binding revision'),
    binding_count: decimal(record.binding_count, 'binding count'),
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

function normalizeSourceTypes(value: unknown): CatalogSourceType[] {
  if (!Array.isArray(value) || value.length < 1 || value.length > 2)
    invalid('candidate source types');
  const result = value.map(catalogSource);
  if (
    new Set(result).size !== result.length ||
    (result.length === 2 && (result[0] !== 'automatic' || result[1] !== 'manual'))
  ) {
    invalid('candidate source type order');
  }
  return result;
}

export function normalizeBindingCandidate(value: unknown): BindingCandidate {
  const record = exactRecord(
    value,
    [
      'endpoint_key_id',
      'endpoint_base_url',
      'connector_type',
      'endpoint_note',
      'endpoint_key_display_head',
      'endpoint_key_display_tail',
      'endpoint_key_note',
      'upstream_model_id',
      'source_types',
    ],
    [],
    'binding candidate',
  );
  return {
    endpoint_key_id: id(record.endpoint_key_id, 'candidate endpoint key id'),
    endpoint_base_url: normalizeBaseURL(record.endpoint_base_url, 'candidate endpoint base URL'),
    connector_type: connectorType(record.connector_type),
    endpoint_note: scalarString(record.endpoint_note, 1_024, 'candidate endpoint note', {
      allowEmpty: true,
    }),
    endpoint_key_display_head: asciiFragment(
      record.endpoint_key_display_head,
      'candidate key display head',
    ),
    endpoint_key_display_tail: asciiFragment(
      record.endpoint_key_display_tail,
      'candidate key display tail',
    ),
    endpoint_key_note: scalarString(record.endpoint_key_note, 1_024, 'candidate key note', {
      allowEmpty: true,
    }),
    upstream_model_id: manualValue(
      record.upstream_model_id,
      512,
      false,
      'candidate upstream model id',
    ),
    source_types: normalizeSourceTypes(record.source_types),
  };
}

export function normalizeBinding(value: unknown): Binding {
  const record = exactRecord(
    value,
    [
      'id',
      'endpoint_key_id',
      'endpoint_base_url',
      'connector_type',
      'endpoint_note',
      'endpoint_key_display_head',
      'endpoint_key_display_tail',
      'endpoint_key_note',
      'upstream_model_id',
      'ord',
    ],
    [],
    'binding',
  );
  if (
    !Number.isSafeInteger(record.ord) ||
    (record.ord as number) < 0 ||
    (record.ord as number) > 255
  ) {
    invalid('binding order');
  }
  return {
    id: id(record.id, 'binding id'),
    endpoint_key_id: id(record.endpoint_key_id, 'binding endpoint key id'),
    endpoint_base_url: normalizeBaseURL(record.endpoint_base_url, 'binding endpoint base URL'),
    connector_type: connectorType(record.connector_type),
    endpoint_note: scalarString(record.endpoint_note, 1_024, 'binding endpoint note', {
      allowEmpty: true,
    }),
    endpoint_key_display_head: asciiFragment(
      record.endpoint_key_display_head,
      'binding key display head',
    ),
    endpoint_key_display_tail: asciiFragment(
      record.endpoint_key_display_tail,
      'binding key display tail',
    ),
    endpoint_key_note: scalarString(record.endpoint_key_note, 1_024, 'binding key note', {
      allowEmpty: true,
    }),
    upstream_model_id: manualValue(
      record.upstream_model_id,
      512,
      false,
      'binding upstream model id',
    ),
    ord: record.ord as number,
  };
}

function normalizeBindings(value: unknown): Binding[] {
  const bindings = boundedArray(value, normalizeBinding, 'bindings', MAX_BINDINGS);
  uniqueBy(bindings, (binding) => binding.id, 'binding ids');
  uniqueBy(
    bindings,
    (binding) => `${binding.endpoint_key_id}\u0000${binding.upstream_model_id}`,
    'binding pairs',
  );
  uniqueBy(bindings, (binding) => String(binding.ord), 'binding order');
  const ordered = [...bindings].sort((left, right) => left.ord - right.ord);
  if (ordered.some((binding, index) => binding.ord !== index)) invalid('binding order');
  return ordered;
}

export function normalizeBindingsResponse(value: unknown): BindingsResponse {
  const record = exactRecord(value, ['bindings', 'binding_revision'], [], 'bindings response');
  return {
    bindings: normalizeBindings(record.bindings),
    binding_revision: decimal(record.binding_revision, 'binding revision'),
  };
}

export function normalizeDiscoveryAccepted(value: unknown): DiscoveryAccepted {
  const record = exactRecord(value, ['operation_id', 'evidence'], [], 'discovery acceptance');
  const operationId = scalarString(record.operation_id, 25, 'discovery operation id');
  if (!OPERATION_ID.test(operationId)) invalid('discovery operation id');
  return {
    operation_id: operationId,
    evidence: normalizeDiscoveryEvidence(record.evidence),
  };
}

export function normalizeManualEntriesResponse(value: unknown): ManualEntriesResponse {
  const record = exactRecord(value, ['entries'], [], 'manual catalog response');
  const entries = boundedArray(record.entries, normalizeCatalogEntry, 'manual catalog entries');
  if (entries.some((entry) => entry.source_type !== 'manual')) invalid('manual catalog response');
  uniqueBy(entries, (entry) => entry.id, 'manual catalog entry ids');
  return { entries };
}

function normalizeAffectedModel(value: unknown): AffectedModel {
  const record = exactRecord(value, ['model', 'bindings'], [], 'affected model');
  const model = normalizeModel(record.model);
  const bindings = normalizeBindings(record.bindings);
  if (model.binding_count !== String(bindings.length)) invalid('affected model binding count');
  return { model, bindings };
}

export function normalizeManualUpdateResponse(value: unknown): ManualUpdateResponse {
  const record = exactRecord(
    value,
    ['entries', 'affected_models'],
    [],
    'manual catalog update response',
  );
  const entries = boundedArray(record.entries, normalizeCatalogEntry, 'manual catalog entries');
  const affectedModels = boundedArray(
    record.affected_models,
    normalizeAffectedModel,
    'affected models',
    MAX_BINDINGS,
  );
  if (entries.length !== 1 || entries.some((entry) => entry.source_type !== 'manual')) {
    invalid('manual catalog update response');
  }
  uniqueBy(entries, (entry) => entry.id, 'manual catalog entry ids');
  uniqueBy(affectedModels, (entry) => entry.model.id, 'affected model ids');
  return { entries, affected_models: affectedModels };
}

export function normalizeEndpointPage(value: unknown): Page<Endpoint> {
  const page = normalizePage(value, normalizeEndpoint, 'endpoints');
  uniqueBy(page.data, (endpoint) => endpoint.id, 'endpoint ids');
  return page;
}

export function normalizeEndpointKeyPage(value: unknown): Page<EndpointKey> {
  const page = normalizePage(value, normalizeEndpointKey, 'endpoint keys');
  uniqueBy(page.data, (key) => key.id, 'endpoint key ids');
  return page;
}

export function normalizeModelPage(value: unknown): Page<Model> {
  const page = normalizePage(value, normalizeModel, 'logical models');
  uniqueBy(page.data, (model) => model.id, 'logical model ids');
  return page;
}

export function normalizeBindingCandidatePage(value: unknown): Page<BindingCandidate> {
  const page = normalizePage(value, normalizeBindingCandidate, 'binding candidates');
  uniqueBy(
    page.data,
    (candidate) => `${candidate.endpoint_key_id}\u0000${candidate.upstream_model_id}`,
    'binding candidate pairs',
  );
  return page;
}

export function canonicalBaseURLPreview(value: string): string {
  const trimmed = value.trim();
  if (
    trimmed.length === 0 ||
    Array.from(trimmed).length > 4_096 ||
    new TextEncoder().encode(trimmed).byteLength > 4_096 ||
    hasForbiddenScalar(trimmed)
  ) {
    throw new ApiError('invalid_request', 'Enter a valid endpoint URL.', 400);
  }
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    throw new ApiError('invalid_request', 'Enter a valid endpoint URL.', 400);
  }
  if (
    (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash
  ) {
    throw new ApiError(
      'invalid_request',
      'Enter a URL without credentials, query parameters, or a fragment.',
      400,
    );
  }
  const rendered = parsed.toString();
  return rendered.endsWith('/') ? rendered.slice(0, -1) : rendered;
}

function asciiFragment(value: unknown, label: string): string {
  const fragment = scalarString(value, 16, label, { allowEmpty: true });
  if (!/^[\x20-\x7e]*$/.test(fragment) || fragment.length > 16) invalid(label);
  return fragment;
}

function normalizeBaseURL(value: unknown, label: string): string {
  const candidate = scalarString(value, 4_096, label);
  if (new TextEncoder().encode(candidate).byteLength > 4_096) invalid(label);
  let parsed: URL;
  try {
    parsed = new URL(candidate);
  } catch {
    invalid(label);
  }
  if (
    (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash
  ) {
    invalid(label);
  }
  return candidate;
}

export function validateResourceId(value: string, label: string): string {
  try {
    return id(value, label);
  } catch {
    throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
  }
}

export function validateMainstreamChannelID(value: string): string {
  try {
    return opaqueID(value, 'mch_', 'mainstream channel id');
  } catch {
    throw new ApiError('invalid_request', 'Invalid mainstream channel id.', 400);
  }
}

export function validateRevisionInput(value: string, label: string, positive = false): string {
  try {
    return decimal(value, label, positive);
  } catch {
    throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
  }
}

export function validatePaginationCursor(value: string): string {
  return clientScalarString(value, 512, 'pagination cursor', false);
}

export interface CanonicalCandidateFilters {
  endpointId: string;
  keyId: string;
  source: CatalogSourceType | '';
  query: string;
  cursor: string;
  limit: number;
}

function clientScalarString(
  value: unknown,
  maximum: number,
  label: string,
  allowEmpty: boolean,
): string {
  if (
    typeof value !== 'string' ||
    hasForbiddenScalar(value) ||
    (!allowEmpty && value.length === 0) ||
    Array.from(value).length > maximum
  ) {
    throw new ApiError('invalid_request', `Invalid ${label}.`, 400);
  }
  return value;
}

export function validateScalarInput(
  value: unknown,
  maximum: number,
  label: string,
  allowEmpty = true,
): string {
  return clientScalarString(value, maximum, label, allowEmpty);
}

export function canonicalCandidateFilters(filters: CandidateFilters): CanonicalCandidateFilters {
  const endpointId =
    filters.endpointId === undefined ? '' : validateResourceId(filters.endpointId, 'endpoint id');
  const keyId =
    filters.keyId === undefined ? '' : validateResourceId(filters.keyId, 'endpoint key id');
  if (
    filters.source !== undefined &&
    filters.source !== 'automatic' &&
    filters.source !== 'manual'
  ) {
    throw new ApiError('invalid_request', 'Invalid candidate source.', 400);
  }
  const query =
    filters.query === undefined
      ? ''
      : clientScalarString(filters.query, 512, 'candidate query', true);
  const cursor =
    filters.cursor === undefined
      ? ''
      : clientScalarString(filters.cursor, 512, 'pagination cursor', false);
  const limit = filters.limit ?? 50;
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100) {
    throw new ApiError('invalid_request', 'Invalid page limit.', 400);
  }
  return { endpointId, keyId, source: filters.source ?? '', query, cursor, limit };
}

export function normalizeAuthorizationURL(value: unknown): string {
  const record = exactRecord(value, ['authorization_url'], [], 'elevation response');
  return httpsURL(record.authorization_url, 'elevation authorization URL');
}
