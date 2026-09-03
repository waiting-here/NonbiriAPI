import { ApiError } from '@shared/query/http';
import type {
  ActivitiesMaster,
  ActivitiesSnapshot,
  CharityCapability,
  CharityCapabilityModel,
  CursorPage,
  Donation,
  DonationKeySource,
  DonationKey,
  DonationKeyEndedReason,
  DonationKeyState,
  DonationReviewResult,
  DonationStatus,
  EndpointKeySummary,
  EndpointOrigin,
  EndpointSummary,
  ThursdayContributionResult,
  ThursdayCurrent,
  ThursdayLastResult,
  ThursdayNext,
  ThursdayState,
  WelfareClaimResult,
  WelfareState,
} from './types';

type UnknownRecord = Record<string, unknown>;

const MAX_U128 = (1n << 128n) - 1n;
const MAX_SM128_MAGNITUDE = (1n << 127n) - 1n;
const MAX_INT64 = (1n << 63n) - 1n;
const MAX_MONEY_MILLI = 9_000_000_000_000_000n;
const CANONICAL_DECIMAL = /^(0|[1-9][0-9]*)$/;
const CANONICAL_AMOUNT = /^(0|[1-9][0-9]*)(?:\.([0-9]{0,2}[1-9]))?$/;
const CANONICAL_SIGNED_AMOUNT = /^(-?)(0|[1-9][0-9]*)(?:\.([0-9]{0,2}[1-9]))?$/;
const OPAQUE_BODY = '[A-Za-z0-9_-]{21}[AQgw]';
const PERIOD_ID = new RegExp(`^thu_${OPAQUE_BODY}$`);
const SSE_ID = new RegExp(`^sse_${OPAQUE_BODY}$`);
const MAX_UNIX_SECONDS = 253_402_300_799;

function invalid(field: string): never {
  throw new ApiError('invalid_response', `The server returned an invalid ${field}.`, 200);
}

function record(value: unknown, field: string, keys: readonly string[]): UnknownRecord {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalid(field);
  const result = value as UnknownRecord;
  const actual = Object.keys(result);
  if (actual.length !== keys.length || actual.some((key) => !keys.includes(key))) invalid(field);
  for (const key of keys) {
    if (!Object.prototype.hasOwnProperty.call(result, key)) invalid(field);
  }
  return result;
}

function array(value: unknown, field: string, maximum: number): unknown[] {
  if (!Array.isArray(value) || value.length > maximum) invalid(field);
  return value;
}

function bool(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') invalid(field);
  return value;
}

function integer(value: unknown, field: string, minimum: number, maximum: number): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum)
    invalid(field);
  return value as number;
}

function hasForbiddenControl(value: string): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint < 0x20 || (codePoint >= 0x7f && codePoint <= 0x9f)) return true;
  }
  return false;
}

function text(
  value: unknown,
  field: string,
  maximum: number,
  allowEmpty = true,
  maximumBytes = Number.POSITIVE_INFINITY,
): string {
  if (
    typeof value !== 'string' ||
    Array.from(value).length > maximum ||
    new TextEncoder().encode(value).byteLength > maximumBytes ||
    hasForbiddenControl(value)
  )
    invalid(field);
  if (!allowEmpty && value.length === 0) invalid(field);
  return value;
}

function asciiText(value: unknown, field: string, maximumBytes: number, allowEmpty = true): string {
  const candidate = text(value, field, maximumBytes, allowEmpty, maximumBytes);
  for (const character of candidate) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint < 0x20 || codePoint > 0x7e) invalid(field);
  }
  return candidate;
}

function decimal(value: unknown, field: string, maximum = MAX_U128): string {
  if (typeof value !== 'string' || value.length > 39 || !CANONICAL_DECIMAL.test(value))
    invalid(field);
  let parsed: bigint;
  try {
    parsed = BigInt(value);
  } catch {
    return invalid(field);
  }
  if (parsed > maximum) invalid(field);
  return value;
}

function positiveDecimal(value: unknown, field: string, maximum = MAX_INT64): string {
  const parsed = decimal(value, field, maximum);
  if (parsed === '0') invalid(field);
  return parsed;
}

function unsignedAmountToMilli(value: unknown, field: string, maximum: bigint): bigint {
  if (typeof value !== 'string' || value.length > 44) invalid(field);
  const match = CANONICAL_AMOUNT.exec(value);
  if (!match) invalid(field);
  let milli: bigint;
  try {
    milli = BigInt(match[1]) * 1000n + BigInt((match[2] ?? '').padEnd(3, '0') || '0');
  } catch {
    return invalid(field);
  }
  if (milli > maximum) invalid(field);
  return milli;
}

export function amountToMilli(value: unknown, field = 'amount'): bigint {
  return unsignedAmountToMilli(value, field, MAX_U128);
}

function signedSM128Amount(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.length > 45) invalid(field);
  const match = CANONICAL_SIGNED_AMOUNT.exec(value);
  if (!match || (match[1] === '-' && match[2] === '0' && !match[3])) invalid(field);
  const magnitude = unsignedAmountToMilli(
    `${match[2]}${match[3] ? `.${match[3]}` : ''}`,
    field,
    MAX_SM128_MAGNITUDE,
  );
  if (magnitude === 0n && match[1] === '-') invalid(field);
  return value;
}

export function creditAmount(value: unknown, field = 'amount', maximum = MAX_U128): string {
  unsignedAmountToMilli(value, field, maximum);
  return value as string;
}

function sm128CreditAmount(value: unknown, field: string): string {
  return creditAmount(value, field, MAX_SM128_MAGNITUDE);
}

function timestamp(value: unknown, field: string): number {
  return integer(value, field, 0, MAX_UNIX_SECONDS);
}

function nullableTimestamp(value: unknown, field: string): number | null {
  return value === null ? null : timestamp(value, field);
}

function nullableDecimal(
  value: unknown,
  field: string,
  amount = false,
  maximum = MAX_U128,
): string | null {
  if (value === null) return null;
  return amount ? creditAmount(value, field, maximum) : decimal(value, field, maximum);
}

function decimalID(value: unknown, field: string): string {
  return positiveDecimal(value, field);
}

function cursor(value: unknown, field: string): string | null {
  if (value === null) return null;
  return asciiText(value, field, 512, false);
}

function connectorType(value: unknown, field: string): string {
  const candidate = asciiText(value, field, 64, false);
  if (candidate !== 'openai-compatible' && candidate !== 'anthropic-compatible') invalid(field);
  return candidate;
}

const MAINSTREAM_CHANNEL_ID = new RegExp(`^mch_${OPAQUE_BODY}$`);

function mainstreamChannelID(value: unknown, field: string): string {
  const candidate = text(value, field, 26, false, 26);
  if (!MAINSTREAM_CHANNEL_ID.test(candidate)) invalid(field);
  return candidate;
}

function mainstreamChannelName(value: unknown, field: string): string {
  const candidate = text(value, field, 128, false);
  if (candidate.trim() !== candidate) invalid(field);
  return candidate;
}

function normalizeEndpointOrigin(value: unknown): EndpointOrigin {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    invalid('endpoint origin');
  }
  const candidate = value as UnknownRecord;
  if (candidate.kind === 'custom') {
    record(candidate, 'endpoint custom origin', ['kind']);
    return { kind: 'custom' };
  }
  if (candidate.kind === 'mainstream') {
    const origin = record(candidate, 'endpoint mainstream origin', ['kind', 'channel_id', 'name']);
    return {
      kind: 'mainstream',
      channelId: mainstreamChannelID(origin.channel_id, 'endpoint origin channel id'),
      name: mainstreamChannelName(origin.name, 'endpoint origin channel name'),
    };
  }
  invalid('endpoint origin kind');
}

function normalizeDonationSafeSource(value: unknown): DonationKeySource {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    invalid('donation key safe source');
  }
  const candidate = value as UnknownRecord;
  if (candidate.kind === 'custom') {
    const source = record(candidate, 'donation custom safe source', [
      'kind',
      'connector_type',
      'base_url',
    ]);
    return {
      kind: 'custom',
      connectorType: connectorType(source.connector_type, 'donation key connector type'),
      baseUrl: text(source.base_url, 'donation key base URL', 4096, false, 4096),
    };
  }
  if (candidate.kind === 'mainstream') {
    const source = record(candidate, 'donation mainstream safe source', [
      'kind',
      'connector_type',
      'base_url',
      'channel_id',
      'name',
    ]);
    return {
      kind: 'mainstream',
      connectorType: connectorType(source.connector_type, 'donation key connector type'),
      baseUrl: text(source.base_url, 'donation key base URL', 4096, false, 4096),
      channelId: mainstreamChannelID(source.channel_id, 'donation key channel id'),
      name: mainstreamChannelName(source.name, 'donation key channel name'),
    };
  }
  invalid('donation key safe source kind');
}

function opaquePeriodID(value: unknown, field: string): string {
  const candidate = text(value, field, 26, false);
  if (!PERIOD_ID.test(candidate)) invalid(field);
  return candidate;
}

function canonicalSiteDay(value: unknown, field: string, allowEmpty = false): string {
  const candidate = text(value, field, 10, allowEmpty);
  if (allowEmpty && candidate === '') return candidate;
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(candidate);
  if (!match) invalid(field);
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (year < 1970 || year > 9999 || month < 1 || month > 12 || day < 1 || day > days[month - 1]) {
    invalid(field);
  }
  return candidate;
}

export function isCanonicalSSEID(value: unknown): value is string {
  return typeof value === 'string' && SSE_ID.test(value);
}

function charityModelName(value: unknown, field: string): string {
  const candidate = text(value, field, 64, false);
  if (candidate.includes('/') || candidate.trim() !== candidate) invalid(field);
  return candidate;
}

export function normalizeCharityCapabilityModel(value: unknown): CharityCapabilityModel {
  const item = record(value, 'charity model', ['id', 'provider', 'model', 'full_name']);
  const provider = charityModelName(item.provider, 'charity model provider');
  const model = charityModelName(item.model, 'charity model name');
  const fullName = text(item.full_name, 'charity model full name', 133, false);
  if (fullName !== `[公益]${provider}/${model}`) invalid('charity model full name');
  return {
    id: decimalID(item.id, 'charity model id'),
    provider,
    model,
    fullName,
  };
}

export function normalizeCharityCapability(value: unknown): CharityCapability {
  const root = record(value, 'charity capability', ['state', 'models', 'donation_intake']);
  const state = root.state;
  if (
    state !== 'feature_disabled' &&
    state !== 'no_models' &&
    state !== 'no_candidates' &&
    state !== 'available'
  ) {
    invalid('charity capability state');
  }
  const models = array(root.models, 'charity capability models', 1000).map(
    normalizeCharityCapabilityModel,
  );
  const donationIntake = root.donation_intake;
  if (donationIntake !== 'open' && donationIntake !== 'closed') {
    invalid('charity donation intake');
  }
  if (state === 'feature_disabled' && donationIntake !== 'closed') {
    invalid('charity donation intake');
  }
  if ((state === 'available') !== models.length > 0) invalid('charity capability models');
  requireUnique(models, (model) => model.id, 'charity model identities');
  requireUnique(models, (model) => model.fullName, 'charity model names');
  return { state, models, donationIntake };
}

function normalizeReviewResult(value: unknown): DonationReviewResult | null {
  if (value === null) return null;
  const result = record(value, 'donation review result', ['decision', 'reason', 'reviewed_at']);
  if (result.decision !== 'approve' && result.decision !== 'reject')
    invalid('donation review decision');
  return {
    decision: result.decision,
    reason: text(result.reason, 'donation review reason', 1024),
    reviewedAt: timestamp(result.reviewed_at, 'donation reviewed timestamp'),
  };
}

const DONATION_STATUSES: readonly DonationStatus[] = [
  'pending',
  'approved',
  'rejected',
  'deleted',
  'expired',
];
const DONATION_KEY_STATES: readonly DonationKeyState[] = [
  'pending',
  'disabled',
  'suspended',
  'exhausted',
  'expired',
  'ended',
  'available',
];
const ENDED_REASONS: readonly DonationKeyEndedReason[] = [
  'member_removed',
  'withdrawn',
  'terminated',
  'expired',
  'account_deleted',
];

function enumValue<T extends string>(value: unknown, field: string, values: readonly T[]): T {
  if (typeof value !== 'string' || !values.includes(value as T)) invalid(field);
  return value as T;
}

function requireUnique<T>(
  values: readonly T[],
  identify: (value: T) => string,
  field: string,
): void {
  const seen = new Set<string>();
  for (const value of values) {
    const identity = identify(value);
    if (seen.has(identity)) invalid(field);
    seen.add(identity);
  }
}

function requireOrderedTimestamps(createdAt: number, updatedAt: number, field: string): void {
  if (updatedAt < createdAt) invalid(field);
}

function validateUsageDimension(
  limit: string | null,
  used: string,
  inflight: string,
  field: string,
  amount = false,
): void {
  const parse = amount ? amountToMilli : (input: string) => BigInt(input);
  const total = parse(used) + parse(inflight);
  if (total > MAX_U128 || (limit !== null && total > parse(limit))) invalid(field);
}

export function normalizeDonationKey(value: unknown): DonationKey {
  const item = record(value, 'donation key', [
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
  ]);
  const source = normalizeDonationSafeSource(item.safe_source);
  const limits = record(item.limits, 'donation key limits', ['price', 'calls', 'tokens']);
  const usage = record(item.usage, 'donation key usage', [
    'price_used',
    'price_inflight',
    'calls_used',
    'calls_inflight',
    'tokens_used',
    'tokens_inflight',
  ]);
  const streak = record(item.streak, 'donation key streak', [
    'generation',
    'count',
    'failure_disabled',
  ]);
  const endedReason =
    item.ended_reason === null
      ? null
      : enumValue(item.ended_reason, 'donation key ended reason', ENDED_REASONS);
  const result: DonationKey = {
    id: decimalID(item.id, 'donation key id'),
    endpointKeyId:
      item.endpoint_key_id === null ? null : decimalID(item.endpoint_key_id, 'endpoint key id'),
    displayHead: asciiText(item.display_head, 'donation key display head', 16),
    displayTail: asciiText(item.display_tail, 'donation key display tail', 16),
    source,
    physicalEnabled: bool(item.physical_enabled, 'donation key physical state'),
    charityState: enumValue(item.charity_state, 'donation key charity state', DONATION_KEY_STATES),
    limits: {
      price: nullableDecimal(limits.price, 'donation price limit', true, MAX_MONEY_MILLI),
      calls: nullableDecimal(limits.calls, 'donation call limit', false, MAX_MONEY_MILLI),
      tokens: nullableDecimal(limits.tokens, 'donation token limit', false, MAX_MONEY_MILLI),
    },
    usage: {
      priceUsed: creditAmount(usage.price_used, 'donation price used'),
      priceInflight: creditAmount(usage.price_inflight, 'donation price inflight'),
      callsUsed: decimal(usage.calls_used, 'donation calls used'),
      callsInflight: decimal(usage.calls_inflight, 'donation calls inflight'),
      tokensUsed: decimal(usage.tokens_used, 'donation tokens used'),
      tokensInflight: decimal(usage.tokens_inflight, 'donation tokens inflight'),
    },
    tokenReserve: integer(item.token_reserve, 'donation token reserve', 0, 2_147_483_647),
    expiresAt: nullableTimestamp(item.expires_at, 'donation key expiry'),
    streak: {
      generation: positiveDecimal(streak.generation, 'donation streak generation', MAX_U128),
      count: decimal(streak.count, 'donation streak count'),
      failureDisabled: bool(streak.failure_disabled, 'donation failure state'),
    },
    endedReason,
  };
  validateUsageDimension(
    result.limits.price,
    result.usage.priceUsed,
    result.usage.priceInflight,
    'donation price usage',
    true,
  );
  validateUsageDimension(
    result.limits.calls,
    result.usage.callsUsed,
    result.usage.callsInflight,
    'donation call usage',
  );
  validateUsageDimension(
    result.limits.tokens,
    result.usage.tokensUsed,
    result.usage.tokensInflight,
    'donation token usage',
  );
  const terminal = result.charityState === 'ended' || result.charityState === 'expired';
  if (result.endedReason !== null && !terminal) invalid('donation key ended reason');
  if (result.endpointKeyId === null && !terminal) invalid('donation key endpoint reference');
  if (
    result.charityState === 'available' &&
    (!result.physicalEnabled ||
      result.streak.failureDisabled ||
      isDimensionExhausted(
        result.limits.price,
        result.usage.priceUsed,
        result.usage.priceInflight,
        true,
      ) ||
      isDimensionExhausted(
        result.limits.calls,
        result.usage.callsUsed,
        result.usage.callsInflight,
      ) ||
      isDimensionExhausted(
        result.limits.tokens,
        result.usage.tokensUsed,
        result.usage.tokensInflight,
      ))
  ) {
    invalid('available donation key state');
  }
  if (
    result.charityState === 'exhausted' &&
    (!result.physicalEnabled ||
      result.streak.failureDisabled ||
      (!isDimensionExhausted(
        result.limits.price,
        result.usage.priceUsed,
        result.usage.priceInflight,
        true,
      ) &&
        !isDimensionExhausted(
          result.limits.calls,
          result.usage.callsUsed,
          result.usage.callsInflight,
        ) &&
        !isDimensionExhausted(
          result.limits.tokens,
          result.usage.tokensUsed,
          result.usage.tokensInflight,
        )))
  ) {
    invalid('exhausted donation key state');
  }
  if (
    (result.charityState === 'suspended' || result.charityState === 'available') &&
    (!result.physicalEnabled || result.streak.failureDisabled)
  ) {
    invalid('donation key availability state');
  }
  if (result.streak.failureDisabled && result.charityState !== 'disabled' && !terminal) {
    invalid('donation key failure state');
  }
  if (
    !result.physicalEnabled &&
    result.charityState !== 'disabled' &&
    result.charityState !== 'pending' &&
    !terminal
  ) {
    invalid('donation key physical state');
  }
  return result;
}

export function normalizeDonation(value: unknown): Donation {
  const item = record(value, 'donation', [
    'id',
    'status',
    'revision',
    'description',
    'review_result',
    'keys',
    'created_at',
    'updated_at',
  ]);
  const status = enumValue(item.status, 'donation status', DONATION_STATUSES);
  const reviewResult = normalizeReviewResult(item.review_result);
  if (status === 'pending' && reviewResult !== null) invalid('pending donation review result');
  if (status === 'approved' && reviewResult?.decision !== 'approve')
    invalid('approved donation review result');
  if (status === 'rejected' && reviewResult?.decision !== 'reject')
    invalid('rejected donation review result');
  const keys = array(item.keys, 'donation keys', 100).map(normalizeDonationKey);
  if (keys.length === 0) invalid('donation keys');
  requireUnique(keys, (key) => key.id, 'donation key identities');
  const createdAt = timestamp(item.created_at, 'donation created timestamp');
  const updatedAt = timestamp(item.updated_at, 'donation updated timestamp');
  requireOrderedTimestamps(createdAt, updatedAt, 'donation timestamps');
  if (
    reviewResult &&
    (reviewResult.reviewedAt < createdAt || reviewResult.reviewedAt > updatedAt)
  ) {
    invalid('donation review timestamp');
  }
  if (status === 'pending') {
    if (
      keys.some(
        (key) =>
          key.charityState !== 'pending' ||
          key.limits.price !== null ||
          key.limits.calls !== null ||
          key.limits.tokens !== null ||
          key.usage.priceUsed !== '0' ||
          key.usage.priceInflight !== '0' ||
          key.usage.callsUsed !== '0' ||
          key.usage.callsInflight !== '0' ||
          key.usage.tokensUsed !== '0' ||
          key.usage.tokensInflight !== '0' ||
          key.tokenReserve !== 0 ||
          key.streak.generation !== '1' ||
          key.streak.count !== '0' ||
          key.streak.failureDisabled ||
          key.endedReason !== null,
      )
    ) {
      invalid('pending donation state');
    }
  } else if (keys.some((key) => key.charityState === 'pending')) {
    invalid('terminal donation key state');
  }
  if (
    status === 'approved' &&
    keys.every((key) => key.charityState === 'ended' || key.charityState === 'expired')
  ) {
    invalid('approved donation keys');
  }
  if (status === 'rejected') {
    if (keys.some((key) => key.charityState !== 'ended')) {
      invalid('rejected donation state');
    }
  }
  if (
    status === 'deleted' &&
    (reviewResult?.decision === 'reject' ||
      keys.some((key) => key.charityState !== 'ended' && key.charityState !== 'expired'))
  ) {
    invalid('deleted donation state');
  }
  if (
    status === 'expired' &&
    (reviewResult?.decision !== 'approve' ||
      keys.some((key) => key.charityState !== 'ended' && key.charityState !== 'expired') ||
      !keys.some((key) => key.charityState === 'expired'))
  ) {
    invalid('expired donation state');
  }
  return {
    id: decimalID(item.id, 'donation id'),
    status,
    revision: positiveDecimal(item.revision, 'donation revision'),
    description: text(item.description, 'donation description', 1024),
    reviewResult,
    keys,
    createdAt,
    updatedAt,
  };
}

export function normalizeEndpoint(value: unknown): EndpointSummary {
  const item = record(value, 'endpoint', [
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
  ]);
  const createdAt = timestamp(item.created_at, 'endpoint created timestamp');
  const updatedAt = timestamp(item.updated_at, 'endpoint updated timestamp');
  requireOrderedTimestamps(createdAt, updatedAt, 'endpoint timestamps');
  return {
    id: decimalID(item.id, 'endpoint id'),
    connectorType: connectorType(item.connector_type, 'endpoint connector type'),
    baseUrl: text(item.base_url, 'endpoint base URL', 4096, false, 4096),
    origin: normalizeEndpointOrigin(item.origin),
    note: text(item.note, 'endpoint note', 1024),
    enabled: bool(item.enabled, 'endpoint enabled state'),
    revision: positiveDecimal(item.revision, 'endpoint revision'),
    keyCount: decimal(item.key_count, 'endpoint key count'),
    createdAt,
    updatedAt,
  };
}

export function normalizeEndpointKey(value: unknown): EndpointKeySummary {
  const item = record(value, 'endpoint key', [
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
  ]);
  if (item.suspension_state !== 'none' && item.suspension_state !== 'security_processing') {
    invalid('endpoint key suspension state');
  }
  const createdAt = timestamp(item.created_at, 'endpoint key created timestamp');
  const updatedAt = timestamp(item.updated_at, 'endpoint key updated timestamp');
  requireOrderedTimestamps(createdAt, updatedAt, 'endpoint key timestamps');
  return {
    id: decimalID(item.id, 'endpoint key id'),
    endpointId: decimalID(item.endpoint_id, 'endpoint id'),
    displayHead: asciiText(item.display_head, 'endpoint key display head', 16),
    displayTail: asciiText(item.display_tail, 'endpoint key display tail', 16),
    note: text(item.note, 'endpoint key note', 1024),
    enabled: bool(item.enabled, 'endpoint key enabled state'),
    forceStoreFalse: bool(item.force_store_false, 'endpoint key store policy'),
    suspensionState: item.suspension_state,
    revision: positiveDecimal(item.revision, 'endpoint key revision'),
    createdAt,
    updatedAt,
  };
}

export function normalizeCursorPage<T>(
  value: unknown,
  field: string,
  normalizeItem: (item: unknown) => T,
  maximum = 100,
  identify?: (item: T) => string,
): CursorPage<T> {
  const page = record(value, field, ['data', 'next_cursor']);
  const data = array(page.data, `${field} data`, maximum).map(normalizeItem);
  if (identify) requireUnique(data, identify, `${field} identities`);
  return {
    data,
    nextCursor: cursor(page.next_cursor, `${field} cursor`),
  };
}

function normalizeMaster(value: unknown): ActivitiesMaster {
  const item = record(value, 'activities master', ['enabled', 'available', 'reason']);
  const reason = enumValue(item.reason, 'activities master reason', [
    'available',
    'disabled',
    'configuration_error',
  ] as const);
  const enabled = bool(item.enabled, 'activities master enabled');
  const available = bool(item.available, 'activities master availability');
  if (
    (reason === 'available' && (!enabled || !available)) ||
    (reason === 'disabled' && (enabled || available)) ||
    (reason === 'configuration_error' && (!enabled || available))
  ) {
    invalid('activities master state');
  }
  return { enabled, available, reason };
}

function normalizeWelfare(value: unknown): ActivitiesSnapshot['welfare'] {
  const item = record(value, 'welfare view', [
    'enabled',
    'state',
    'site_day',
    'threshold',
    'cap',
    'pool_balance',
    'claimed_today',
  ]);
  const state = enumValue(item.state, 'welfare state', [
    'unavailable',
    'available',
    'claimed',
    'ineligible',
    'empty',
    'configuration_error',
  ] as readonly WelfareState[]);
  const enabled = bool(item.enabled, 'welfare enabled state');
  const siteDay = canonicalSiteDay(item.site_day, 'welfare site day', true);
  const claimedToday = bool(item.claimed_today, 'welfare claimed state');
  if (state === 'claimed' && !claimedToday) invalid('welfare claimed state');
  if (claimedToday && state !== 'claimed' && state !== 'unavailable') {
    invalid('welfare claimed state');
  }
  if (!enabled && state !== 'unavailable') invalid('welfare enabled state');
  if (state !== 'unavailable' && !enabled) invalid('welfare state');
  if (state === 'configuration_error' && (siteDay !== '' || claimedToday)) {
    invalid('welfare configuration state');
  }
  if (state !== 'unavailable' && state !== 'configuration_error' && siteDay === '') {
    invalid('welfare site day');
  }
  const result: ActivitiesSnapshot['welfare'] = {
    enabled,
    state,
    siteDay,
    threshold: creditAmount(item.threshold, 'welfare threshold', MAX_MONEY_MILLI),
    cap: creditAmount(item.cap, 'welfare cap', MAX_MONEY_MILLI),
    poolBalance: sm128CreditAmount(item.pool_balance, 'welfare pool balance'),
    claimedToday,
  };
  const award = amountToMilli(result.poolBalance, 'welfare pool balance') / 10n;
  const cappedAward =
    award < amountToMilli(result.cap, 'welfare cap')
      ? award
      : amountToMilli(result.cap, 'welfare cap');
  if (state === 'available' && cappedAward === 0n) invalid('welfare available award');
  if (state === 'empty' && cappedAward !== 0n) invalid('welfare empty award');
  return result;
}

function normalizeThursdayCurrent(value: unknown): ThursdayCurrent | null {
  if (value === null) return null;
  const item = record(value, 'Thursday current period', [
    'period_id',
    'revision',
    'opens_at',
    'closes_at',
    'literature',
    'entry',
    'per_user_limit',
    'pool_balance',
    'my_count',
    'my_contributed',
  ]);
  const opensAt = timestamp(item.opens_at, 'Thursday opens timestamp');
  const closesAt = timestamp(item.closes_at, 'Thursday closes timestamp');
  if (closesAt <= opensAt) invalid('Thursday period interval');
  const result: ThursdayCurrent = {
    periodId: opaquePeriodID(item.period_id, 'Thursday period id'),
    revision: positiveDecimal(item.revision, 'Thursday period revision'),
    opensAt,
    closesAt,
    literature: text(item.literature, 'Thursday literature', 1024, true, 4096),
    entry: creditAmount(item.entry, 'Thursday entry', MAX_MONEY_MILLI),
    perUserLimit: integer(item.per_user_limit, 'Thursday per-user limit', 1, 1_000),
    poolBalance: sm128CreditAmount(item.pool_balance, 'Thursday pool balance'),
    myCount: decimal(item.my_count, 'Thursday contribution count'),
    myContributed: creditAmount(item.my_contributed, 'Thursday contributed amount'),
  };
  if (
    amountToMilli(result.entry) === 0n ||
    BigInt(result.myCount) > BigInt(result.perUserLimit) ||
    amountToMilli(result.myContributed) !== BigInt(result.myCount) * amountToMilli(result.entry)
  ) {
    invalid('Thursday participant contribution');
  }
  return result;
}

function normalizeThursdayNext(value: unknown): ThursdayNext | null {
  if (value === null) return null;
  const item = record(value, 'Thursday next period', ['period_id', 'opens_at', 'pool_balance']);
  return {
    periodId: opaquePeriodID(item.period_id, 'Thursday next period id'),
    opensAt: timestamp(item.opens_at, 'Thursday next opens timestamp'),
    poolBalance: sm128CreditAmount(item.pool_balance, 'Thursday next pool balance'),
  };
}

function normalizeThursdayLastResult(value: unknown): ThursdayLastResult | null {
  if (value === null) return null;
  const item = record(value, 'Thursday result', [
    'period_id',
    'my_count',
    'my_contributed',
    'payout',
    'unpaid_reason',
  ]);
  const unpaidReason = item.unpaid_reason;
  if (
    unpaidReason !== null &&
    unpaidReason !== 'account_banned' &&
    unpaidReason !== 'account_deleted'
  ) {
    invalid('Thursday unpaid reason');
  }
  const result: ThursdayLastResult = {
    periodId: opaquePeriodID(item.period_id, 'Thursday result period id'),
    myCount: positiveDecimal(item.my_count, 'Thursday result count', 1_000n),
    myContributed: creditAmount(item.my_contributed, 'Thursday result contribution'),
    payout: creditAmount(item.payout, 'Thursday result payout'),
    unpaidReason,
  };
  if (amountToMilli(result.myContributed) === 0n) invalid('Thursday result contribution');
  if (result.unpaidReason !== null && amountToMilli(result.payout) !== 0n) {
    invalid('Thursday unpaid payout');
  }
  return result;
}

function normalizeThursday(value: unknown): ActivitiesSnapshot['thursday'] {
  const item = record(value, 'Thursday view', [
    'enabled',
    'state',
    'server_now',
    'current',
    'next',
    'last_result',
  ]);
  const state = enumValue(item.state, 'Thursday state', [
    'unavailable',
    'not_open',
    'open',
    'settling',
    'ended',
    'configuration_error',
  ] as readonly ThursdayState[]);
  const enabled = bool(item.enabled, 'Thursday enabled state');
  const serverNow = timestamp(item.server_now, 'Thursday server timestamp');
  const current = normalizeThursdayCurrent(item.current);
  const next = normalizeThursdayNext(item.next);
  const lastResult = normalizeThursdayLastResult(item.last_result);
  if (!enabled && state !== 'unavailable') invalid('Thursday enabled state');
  if (state !== 'unavailable' && !enabled) invalid('Thursday state');
  if (current && current.closesAt !== current.opensAt + 86_400) {
    invalid('Thursday period interval');
  }
  if (current && current.opensAt > serverNow) invalid('Thursday current period');
  if (next && next.opensAt <= serverNow) invalid('Thursday next period');
  if (current && next && current.periodId === next.periodId) invalid('Thursday period identities');
  if (
    lastResult &&
    (lastResult.periodId === current?.periodId || lastResult.periodId === next?.periodId)
  ) {
    invalid('Thursday result identity');
  }
  if (state === 'open') {
    if (!current || serverNow < current.opensAt || serverNow >= current.closesAt || lastResult) {
      invalid('Thursday open state');
    }
  }
  if (state === 'settling' && (!current || serverNow < current.closesAt)) {
    invalid('Thursday settling state');
  }
  if (state === 'not_open' && (current !== null || next === null || lastResult !== null)) {
    invalid('Thursday not-open state');
  }
  if (state === 'ended' && current !== null) invalid('Thursday ended state');
  return {
    enabled,
    state,
    serverNow,
    current,
    next,
    lastResult,
  };
}

export function normalizeActivitiesSnapshot(value: unknown): ActivitiesSnapshot {
  const item = record(value, 'activities snapshot', ['master', 'welfare', 'thursday']);
  const master = normalizeMaster(item.master);
  const welfare = normalizeWelfare(item.welfare);
  const thursday = normalizeThursday(item.thursday);
  if (master.reason === 'disabled') {
    if (welfare.state !== 'unavailable' || thursday.state !== 'unavailable') {
      invalid('activities disabled overlay');
    }
  }
  if (master.available) {
    if ((welfare.state === 'unavailable') !== !welfare.enabled) {
      invalid('welfare availability overlay');
    }
    if ((thursday.state === 'unavailable') !== !thursday.enabled) {
      invalid('Thursday availability overlay');
    }
  }
  return { master, welfare, thursday };
}

export function normalizeWelfareClaimResult(value: unknown): WelfareClaimResult {
  const item = record(value, 'welfare claim result', [
    'awarded',
    'balance',
    'pool_balance',
    'site_day',
  ]);
  const siteDay = canonicalSiteDay(item.site_day, 'welfare result site day');
  return {
    awarded: creditAmount(item.awarded, 'welfare awarded amount', MAX_MONEY_MILLI),
    balance: signedSM128Amount(item.balance, 'welfare balance'),
    poolBalance: sm128CreditAmount(item.pool_balance, 'welfare pool balance'),
    siteDay,
  };
}

export function normalizeThursdayContributionResult(value: unknown): ThursdayContributionResult {
  const item = record(value, 'Thursday contribution result', ['count', 'balance', 'pool_balance']);
  return {
    count: positiveDecimal(item.count, 'Thursday contribution count', 1_000n),
    balance: signedSM128Amount(item.balance, 'Thursday balance'),
    poolBalance: sm128CreditAmount(item.pool_balance, 'Thursday pool balance'),
  };
}

export function calculateWelfareAward(view: ActivitiesSnapshot['welfare']): string {
  const pool = amountToMilli(view.poolBalance, 'welfare pool balance');
  const cap = amountToMilli(view.cap, 'welfare cap');
  const award = pool / 10n < cap ? pool / 10n : cap;
  const whole = award / 1000n;
  const fraction = (award % 1000n).toString().padStart(3, '0').replace(/0+$/, '');
  return `${whole}${fraction ? `.${fraction}` : ''}`;
}

export function isDimensionExhausted(
  limit: string | null,
  used: string,
  inflight: string,
  amount = false,
): boolean {
  if (limit === null) return false;
  const parse = amount
    ? amountToMilli
    : (value: unknown) => BigInt(decimal(value, 'usage dimension'));
  return parse(used) + parse(inflight) >= parse(limit);
}
