import { ApiError } from '@shared/query/http';

const COLLECTION_LIMIT = 10_000;
const MAX_UNIX_SECONDS = 253_402_300_799;
const MAX_UNIX_MILLISECONDS = MAX_UNIX_SECONDS * 1_000;
const MAX_U128_MILLI = (1n << 128n) - 1n;
const DECIMAL_ID = /^[1-9][0-9]*$/;
const NONNEGATIVE_INTEGER = /^(0|[1-9][0-9]*)$/;
const NONNEGATIVE_POINTS = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/;
const COMMITMENT = /^[0-9a-f]{64}$/i;

const ADAPTATION_KEYS = [
  'endpoint_id',
  'revision',
  'forward_headers',
  'fixed_headers',
  'body_defaults',
  'body_forced',
  'native_extension_paths',
] as const;
const ADAPTATION_VALUE_KEYS = ['path', 'has_value'] as const;
const CONTINUITY_KEYS = ['kind', 'scope', 'window', 'state', 'expires_at'] as const;
const FAT_FISH_KEYS = ['summaries', 'progress'] as const;
const SUMMARY_KEYS = [
  'id',
  'period_id',
  'node_id',
  'version_id',
  'engine_version',
  'scoring_version',
  'state',
  'prepared_at_ms',
  'started_at_ms',
  'completed_at_ms',
  'passed',
  'stars',
  'score_units',
  'ticket_charge',
  'ticket_refund',
  'rewards',
  'seed_commit',
  'commitment_verified',
] as const;
const PROGRESS_KEYS = [
  'period_id',
  'node_id',
  'unlocked_at',
  'unlock_operation_id',
  'passed',
  'best_stars',
  'best_score_units',
  'best_at_ms',
  'best_version_id',
] as const;

function invalidExport(): never {
  throw new ApiError('invalid_response', 'The server returned an invalid account export.', 200);
}

function closedObject(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) invalidExport();
  const record = value as Record<string, unknown>;
  const actual = Object.keys(record);
  if (actual.length !== keys.length || actual.some((key) => !keys.includes(key))) invalidExport();
  return record;
}

function boundedArray(value: unknown): unknown[] {
  if (!Array.isArray(value) || value.length > COLLECTION_LIMIT) invalidExport();
  return value;
}

function nonemptyString(value: unknown): string {
  if (typeof value !== 'string' || value.length === 0) invalidExport();
  return value;
}

function canonicalPositiveID(value: unknown): string {
  const text = nonemptyString(value);
  if (text.length > 19 || !DECIMAL_ID.test(text) || BigInt(text) > 9_223_372_036_854_775_807n)
    invalidExport();
  return text;
}

function opaqueID(value: unknown, prefix: string): string {
  const text = nonemptyString(value);
  if (
    text.length !== prefix.length + 22 ||
    !text.startsWith(prefix) ||
    !/^[A-Za-z0-9_-]{21}[AQgw]$/.test(text.slice(prefix.length))
  )
    invalidExport();
  return text;
}

function timestamp(value: unknown, maximum: number): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0 || value > maximum)
    invalidExport();
  return value;
}

function points(value: unknown): bigint {
  const text = nonemptyString(value);
  if (text.length > 48) invalidExport();
  const match = NONNEGATIVE_POINTS.exec(text);
  if (!match) invalidExport();
  const fraction = (match[2] ?? '').padEnd(3, '0');
  const milli = BigInt(match[1]) * 1_000n + BigInt(fraction || '0');
  if (milli > MAX_U128_MILLI) invalidExport();
  return milli;
}

function score(value: unknown): number {
  const text = nonemptyString(value);
  if (!NONNEGATIVE_INTEGER.test(text)) invalidExport();
  const result = Number(text);
  if (!Number.isSafeInteger(result) || result > 100_000_000) invalidExport();
  return result;
}

function strings(value: unknown): void {
  for (const entry of boundedArray(value)) nonemptyString(entry);
}

function adaptationValues(value: unknown): void {
  for (const entry of boundedArray(value)) {
    const record = closedObject(entry, ADAPTATION_VALUE_KEYS);
    nonemptyString(record.path);
    if (typeof record.has_value !== 'boolean') invalidExport();
  }
}

function validateAdaptations(value: unknown, endpoints: unknown): void {
  const endpointIDs = new Set<string>();
  for (const endpoint of boundedArray(endpoints)) {
    if (endpoint === null || typeof endpoint !== 'object' || Array.isArray(endpoint))
      invalidExport();
    const id = canonicalPositiveID((endpoint as Record<string, unknown>).id);
    if (endpointIDs.has(id)) invalidExport();
    endpointIDs.add(id);
  }
  const seen = new Set<string>();
  for (const entry of boundedArray(value)) {
    const record = closedObject(entry, ADAPTATION_KEYS);
    const id = canonicalPositiveID(record.endpoint_id);
    canonicalPositiveID(record.revision);
    if (!endpointIDs.has(id) || seen.has(id)) invalidExport();
    seen.add(id);
    strings(record.forward_headers);
    adaptationValues(record.fixed_headers);
    adaptationValues(record.body_defaults);
    adaptationValues(record.body_forced);
    strings(record.native_extension_paths);
  }
}

function validateContinuity(value: unknown, generatedAt: number): void {
  const kinds = new Set(['checkin_general', 'checkin_game', 'welfare', 'game_onboarding']);
  const seen = new Set<string>();
  for (const entry of boundedArray(value)) {
    const record = closedObject(entry, CONTINUITY_KEYS);
    const kind = nonemptyString(record.kind);
    const scope = nonemptyString(record.scope);
    const window = nonemptyString(record.window);
    if (!kinds.has(kind) || record.state !== 'completed') invalidExport();
    if (kind === 'game_onboarding') {
      if (record.expires_at !== null) invalidExport();
    } else if (timestamp(record.expires_at, MAX_UNIX_SECONDS) <= generatedAt) {
      invalidExport();
    }
    const identity = JSON.stringify([kind, scope, window]);
    if (seen.has(identity)) invalidExport();
    seen.add(identity);
  }
}

function validateSummary(value: unknown): void {
  const record = closedObject(value, SUMMARY_KEYS);
  opaqueID(record.id, 'ffc_');
  opaqueID(record.period_id, 'ffp_');
  opaqueID(record.node_id, 'ffn_');
  opaqueID(record.version_id, 'ffv_');
  if (
    timestamp(record.engine_version, Number.MAX_SAFE_INTEGER) < 1 ||
    timestamp(record.scoring_version, Number.MAX_SAFE_INTEGER) < 1
  )
    invalidExport();
  const states = new Set([
    'settled_pass',
    'settled_fail',
    'abandoned',
    'expired',
    'cancelled_refunded',
  ]);
  if (!states.has(record.state as string)) invalidExport();
  const prepared = timestamp(record.prepared_at_ms, MAX_UNIX_MILLISECONDS);
  const completed = timestamp(record.completed_at_ms, MAX_UNIX_MILLISECONDS);
  if (completed < prepared) invalidExport();
  if (record.started_at_ms !== null) {
    const started = timestamp(record.started_at_ms, MAX_UNIX_MILLISECONDS);
    // A challenge can be abandoned or cancelled during its server countdown.
    if (
      started < prepared ||
      (started > completed && (record.state === 'settled_pass' || record.state === 'settled_fail'))
    )
      invalidExport();
  }
  if (typeof record.passed !== 'boolean' || typeof record.commitment_verified !== 'boolean')
    invalidExport();
  const stars = timestamp(record.stars, 3);
  const units = score(record.score_units);
  if (
    record.passed
      ? stars < 1 || units < 1 || record.state !== 'settled_pass'
      : stars !== 0 || units !== 0 || record.state === 'settled_pass'
  )
    invalidExport();
  const charge = points(record.ticket_charge);
  const refund = points(record.ticket_refund);
  points(record.rewards);
  if (refund > charge || (record.state !== 'cancelled_refunded' && refund !== 0n)) invalidExport();
  if (typeof record.seed_commit !== 'string' || !COMMITMENT.test(record.seed_commit))
    invalidExport();
}

function validateProgress(value: unknown): string {
  const record = closedObject(value, PROGRESS_KEYS);
  const period = opaqueID(record.period_id, 'ffp_');
  const node = opaqueID(record.node_id, 'ffn_');
  timestamp(record.unlocked_at, MAX_UNIX_SECONDS);
  if (record.unlock_operation_id !== null) opaqueID(record.unlock_operation_id, 'op_');
  if (typeof record.passed !== 'boolean') invalidExport();
  const stars = timestamp(record.best_stars, 3);
  const units = score(record.best_score_units);
  if (record.passed) {
    if (stars < 1 || units < 1) invalidExport();
    timestamp(record.best_at_ms, MAX_UNIX_MILLISECONDS);
    opaqueID(record.best_version_id, 'ffv_');
  } else if (
    stars !== 0 ||
    units !== 0 ||
    record.best_at_ms !== null ||
    record.best_version_id !== null
  ) {
    invalidExport();
  }
  return JSON.stringify([period, node]);
}

function validateFatFish(value: unknown): void {
  const record = closedObject(value, FAT_FISH_KEYS);
  const summaries = boundedArray(record.summaries);
  const progress = boundedArray(record.progress);
  if (summaries.length + progress.length > COLLECTION_LIMIT) invalidExport();
  const summaryIDs = new Set<string>();
  for (const entry of summaries) {
    validateSummary(entry);
    const id = (entry as Record<string, unknown>).id as string;
    if (summaryIDs.has(id)) invalidExport();
    summaryIDs.add(id);
  }
  const progressIDs = new Set<string>();
  for (const entry of progress) {
    const id = validateProgress(entry);
    if (progressIDs.has(id)) invalidExport();
    progressIDs.add(id);
  }
}

export function validateAccountExportV11(record: Record<string, unknown>, accountId: string): void {
  if (record.user === null || typeof record.user !== 'object' || Array.isArray(record.user))
    invalidExport();
  if (canonicalPositiveID((record.user as Record<string, unknown>).id) !== accountId)
    invalidExport();
  const generatedAt = timestamp(record.generated_at, MAX_UNIX_SECONDS);
  validateAdaptations(record.request_adaptations, record.endpoints);
  validateContinuity(record.continuity, generatedAt);
  validateFatFish(record.fat_fish);
}
