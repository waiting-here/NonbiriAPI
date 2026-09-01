import { BAITS, type Bait } from '../common/types';
import {
  booleanValue,
  creditsToMilli,
  creditsValue,
  decimalValue,
  enumValue,
  exactRecord,
  invalidResponse,
  opaqueID,
  publicIdentity,
  safeInteger,
  sumCredits,
  textValue,
  unixTime,
} from '../common/strict';
import {
  FISHING_TIERS,
  type FishingBatchResult,
  type FishingLeaderboard,
  type FishingSettlementPending,
  type FishingSingleRow,
  type FishingState,
  type FishingTotalRow,
} from './types';

const SPECIES_TIER = new Map<string, (typeof FISHING_TIERS)[number]>([
  ['whitebait', 'small'],
  ['gudgeon', 'small'],
  ['horse_mouth', 'small'],
  ['smelt', 'small'],
  ['loach', 'small'],
  ['crucian', 'regular'],
  ['tilapia', 'regular'],
  ['yellow_catfish', 'regular'],
  ['ayu', 'regular'],
  ['stream_carp', 'regular'],
  ['common_carp', 'big'],
  ['snakehead', 'big'],
  ['catfish', 'big'],
  ['mandarin_fish', 'big'],
  ['rainbow_trout', 'big'],
  ['grass_carp', 'giant'],
  ['silver_carp', 'giant'],
  ['bighead_carp', 'giant'],
  ['black_carp', 'giant'],
  ['japanese_eel', 'giant'],
  ['yellowcheek', 'legend'],
  ['taimen', 'legend'],
  ['koi', 'legend'],
  ['boot', 'junk'],
  ['seaweed', 'junk'],
  ['plastic_bag', 'junk'],
  ['branch', 'junk'],
  ['old_tire', 'junk'],
  ['glasses', 'junk'],
  ['phone_case', 'junk'],
  ['fry', 'junk'],
  ['bottle', 'treasure'],
  ['clover', 'treasure'],
  ['shell', 'treasure'],
]);

const TIER_SIZE: Readonly<Record<(typeof FISHING_TIERS)[number], readonly [number, number]>> = {
  small: [5, 25],
  regular: [15, 35],
  big: [30, 80],
  giant: [60, 150],
  legend: [100, 200],
  junk: [0, 0],
  treasure: [0, 0],
};

function bait(value: unknown, field: string): Bait {
  return enumValue(value, BAITS, field);
}

function count(value: unknown, field: string): 1 | 10 {
  const parsed = safeInteger(value, 1, 10, field);
  if (parsed !== 1 && parsed !== 10) invalidResponse(field);
  return parsed;
}

export function normalizeFishingPending(value: unknown): FishingSettlementPending {
  const record = exactRecord(
    value,
    ['batch_id', 'bait', 'count', 'entry_total', 'state', 'next_attempt_at', 'retry_exhausted'],
    [],
    'fishing pending',
  );
  const state = enumValue(
    record.state,
    ['settlement_pending', 'recovery_required'] as const,
    'fishing pending state',
  );
  const nextAttemptAt =
    record.next_attempt_at === null ? null : unixTime(record.next_attempt_at, 'next attempt time');
  const retryExhausted = booleanValue(record.retry_exhausted, 'retry exhausted');
  if (
    (state === 'settlement_pending' && (nextAttemptAt === null || retryExhausted)) ||
    (state === 'recovery_required' && (nextAttemptAt !== null || !retryExhausted))
  )
    invalidResponse('fishing pending matrix');
  return {
    batchID: opaqueID(record.batch_id, 'fb_', 'fishing batch id'),
    bait: bait(record.bait, 'fishing bait'),
    count: count(record.count, 'fishing count'),
    entryTotal: creditsValue(record.entry_total, { positive: true }, 'fishing entry total'),
    state,
    nextAttemptAt,
    retryExhausted,
  };
}

export function normalizeFishingResult(value: unknown): FishingBatchResult {
  const record = exactRecord(
    value,
    [
      'batch_id',
      'bait',
      'count',
      'unit_price',
      'entry_total',
      'outcomes',
      'payout_total',
      'balance',
      'settled_at',
      'idempotent_replay',
    ],
    [],
    'fishing result',
  );
  const parsedCount = count(record.count, 'fishing count');
  const unitPrice = creditsValue(record.unit_price, { positive: true }, 'fishing unit price');
  const entryTotal = creditsValue(record.entry_total, { positive: true }, 'fishing entry total');
  if (creditsToMilli(unitPrice) * BigInt(parsedCount) !== creditsToMilli(entryTotal)) {
    invalidResponse('fishing entry arithmetic');
  }
  if (!Array.isArray(record.outcomes) || record.outcomes.length !== parsedCount)
    invalidResponse('fishing outcomes');
  const outcomes = record.outcomes.map((value, index) => {
    const outcome = exactRecord(
      value,
      ['ordinal', 'species_key', 'tier', 'size_cm', 'reward'],
      [],
      `fishing outcome ${index}`,
    );
    const ordinal = safeInteger(outcome.ordinal, index, index, `fishing outcome ${index} ordinal`);
    const speciesKey = textValue(outcome.species_key, 64, `fishing outcome ${index} species`);
    const tier = enumValue(outcome.tier, FISHING_TIERS, `fishing outcome ${index} tier`);
    if (SPECIES_TIER.get(speciesKey) !== tier) invalidResponse(`fishing outcome ${index} roster`);
    const [minimum, maximum] = TIER_SIZE[tier];
    const sizeCM = safeInteger(outcome.size_cm, minimum, maximum, `fishing outcome ${index} size`);
    return {
      ordinal,
      speciesKey,
      tier,
      sizeCM,
      reward: creditsValue(outcome.reward, {}, `fishing outcome ${index} reward`),
    };
  });
  const payoutTotal = creditsValue(record.payout_total, {}, 'fishing payout total');
  if (sumCredits(outcomes.map((outcome) => outcome.reward)) !== payoutTotal)
    invalidResponse('fishing payout arithmetic');
  return {
    batchID: opaqueID(record.batch_id, 'fb_', 'fishing batch id'),
    bait: bait(record.bait, 'fishing bait'),
    count: parsedCount,
    unitPrice,
    entryTotal,
    outcomes,
    payoutTotal,
    balance: creditsValue(record.balance, { signed: true }, 'fishing balance'),
    settledAt: unixTime(record.settled_at, 'fishing settled time'),
    idempotentReplay: booleanValue(record.idempotent_replay, 'fishing replay flag'),
  };
}

export function normalizeFishingStart(
  value: unknown,
): FishingBatchResult | FishingSettlementPending {
  const record = exactRecord(
    value as unknown,
    [],
    [
      'batch_id',
      'bait',
      'count',
      'unit_price',
      'entry_total',
      'outcomes',
      'payout_total',
      'balance',
      'settled_at',
      'idempotent_replay',
      'state',
      'next_attempt_at',
      'retry_exhausted',
    ],
    'fishing start result',
  );
  return Object.prototype.hasOwnProperty.call(record, 'outcomes')
    ? normalizeFishingResult(value)
    : normalizeFishingPending(value);
}

export function normalizeFishingState(value: unknown): FishingState {
  const record = exactRecord(
    value,
    ['settlement_pending', 'unrevealed', 'has_more_unrevealed'],
    [],
    'fishing state',
  );
  const settlementPending =
    record.settlement_pending === null ? null : normalizeFishingPending(record.settlement_pending);
  const unrevealed = record.unrevealed === null ? null : normalizeFishingResult(record.unrevealed);
  if (settlementPending && unrevealed) invalidResponse('fishing state priority');
  const hasMoreUnrevealed = booleanValue(record.has_more_unrevealed, 'more fishing results');
  if (hasMoreUnrevealed && unrevealed === null) invalidResponse('more fishing results');
  return {
    settlementPending,
    unrevealed,
    hasMoreUnrevealed,
  };
}

function rowBase(value: unknown, expected: 'single' | 'total', field: string) {
  const required =
    expected === 'single'
      ? ['rank', 'species_key', 'size_cm', 'identity', 'is_me']
      : ['rank', 'total_credits', 'identity', 'is_me'];
  const record = exactRecord(value, required, [], field);
  return {
    record,
    rank: decimalValue(record.rank, { bits: 256, positive: true }, `${field} rank`),
    identity: publicIdentity(record.identity, `${field} identity`),
    isMe: booleanValue(record.is_me, `${field} me flag`),
  };
}

function singleRow(value: unknown, field: string): FishingSingleRow {
  const base = rowBase(value, 'single', field);
  const speciesKey = textValue(base.record.species_key, 64, `${field} species`);
  const tier = SPECIES_TIER.get(speciesKey);
  if (!tier) invalidResponse(`${field} species roster`);
  const [minimum, maximum] = TIER_SIZE[tier];
  return {
    ...base,
    speciesKey,
    sizeCM: safeInteger(base.record.size_cm, minimum, maximum, `${field} size`),
  };
}

function totalRow(value: unknown, field: string): FishingTotalRow {
  const base = rowBase(value, 'total', field);
  return {
    ...base,
    totalCredits: creditsValue(base.record.total_credits, {}, `${field} total credits`),
  };
}

export function normalizeFishingLeaderboard(
  value: unknown,
  expectedBoard: 'single' | 'total',
): FishingLeaderboard {
  const record = exactRecord(
    value,
    ['board', 'window_start', 'entries', 'me'],
    [],
    'fishing leaderboard',
  );
  const board = enumValue(record.board, ['single', 'total'] as const, 'fishing leaderboard board');
  if (board !== expectedBoard || !Array.isArray(record.entries) || record.entries.length > 20) {
    invalidResponse('fishing leaderboard shape');
  }
  if (board === 'single') {
    if (record.window_start !== null) invalidResponse('single leaderboard window');
    const entries = record.entries.map((row, index) => singleRow(row, `single row ${index}`));
    const me = record.me === null ? null : singleRow(record.me, 'single me row');
    if (
      entries.filter((row) => row.isMe).length > 1 ||
      (me && (!me.isMe || entries.some((row) => row.isMe)))
    )
      invalidResponse('single me row');
    return { board, windowStart: null, entries, me };
  }
  const windowStart = unixTime(record.window_start, 'total leaderboard window');
  const entries = record.entries.map((row, index) => totalRow(row, `total row ${index}`));
  const me = record.me === null ? null : totalRow(record.me, 'total me row');
  if (
    entries.filter((row) => row.isMe).length > 1 ||
    (me && (!me.isMe || entries.some((row) => row.isMe)))
  )
    invalidResponse('total me row');
  return { board, windowStart, entries, me };
}
