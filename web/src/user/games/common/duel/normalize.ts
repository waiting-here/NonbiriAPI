import {
  booleanValue,
  creditsToMilli,
  creditsValue,
  decimalValue,
  enumValue,
  exactRecord,
  gamePayment,
  httpsURL,
  invalidResponse,
  opaqueID,
  safeInteger,
  textValue,
  unixTime,
} from '../strict';
import type {
  DuelCodec,
  DuelConfig,
  DuelDetail,
  DuelGame,
  DuelHome,
  DuelResult,
  DuelRound,
  DuelState,
  Pair,
  Profile,
  Rates,
  Resolution,
  RoundStart,
  Seat,
} from './types';

export const seatValue = (value: unknown): Seat => enumNumber(value, [0, 1], 'seat');
function enumNumber<T extends number>(value: unknown, allowed: readonly T[], field: string): T {
  const number = safeInteger(value, Math.min(...allowed), Math.max(...allowed), field);
  const found = allowed.find((item) => item === number);
  if (found === undefined) invalidResponse(field);
  return found;
}
export function pair<T>(value: unknown, decode: (item: unknown) => T): Pair<T> {
  if (!Array.isArray(value) || value.length !== 2) invalidResponse('seat pair');
  return [decode(value[0]), decode(value[1])];
}
export function list<T>(value: unknown, maximum: number, decode: (item: unknown) => T): T[] {
  if (!Array.isArray(value) || value.length > maximum) invalidResponse('list');
  return value.map(decode);
}
export function nullable<T>(value: unknown, decode: (item: unknown) => T): T | null {
  return value === null ? null : decode(value);
}
export function hashValue(value: unknown): string {
  if (typeof value !== 'string' || !/^[a-f0-9]{64}$/.test(value))
    invalidResponse('content identity');
  return value;
}
export const revisionValue = (value: unknown) =>
  decimalValue(value, { bits: 128, positive: true }, 'revision');
export const prefix = (game: DuelGame, queue = false) =>
  game === 'bidding' ? (queue ? 'bidq_' : 'bid_') : queue ? 'likq_' : 'lik_';
export function ratesValue(value: unknown): Rates {
  const r = exactRecord(value, ['platform', 'welfare', 'thursday']);
  const v = {
    platform: safeInteger(r.platform, 0, 9999, 'platform rate'),
    welfare: safeInteger(r.welfare, 0, 9999, 'welfare rate'),
    thursday: safeInteger(r.thursday, 0, 9999, 'Thursday rate'),
  };
  if (v.platform + v.welfare + v.thursday >= 10000) invalidResponse('total rate');
  return v;
}
export function configValue(value: unknown, game: DuelGame, modes: readonly string[]): DuelConfig {
  const timers =
    game === 'bidding' ? ['joker_seconds', 'bid_seconds'] : ['plan_seconds', 'settlement_seconds'];
  const r = exactRecord(value, [
    'enabled',
    'available',
    'modes',
    'queue_seconds',
    'queue_capacity',
    ...timers,
  ]);
  safeInteger(r.queue_seconds, 120, 120, 'queue duration');
  safeInteger(r.queue_capacity, 4096, 4096, 'queue capacity');
  safeInteger(
    r[timers[0]],
    game === 'bidding' ? 10 : 20,
    game === 'bidding' ? 10 : 20,
    'phase duration',
  );
  if (game === 'bidding') safeInteger(r.bid_seconds, 20, 20, 'phase duration');
  else if (r.settlement_seconds !== 0 && r.settlement_seconds !== 5)
    invalidResponse('presentation duration');
  const raw = exactRecord(r.modes, modes);
  const normalized: Record<string, DuelConfig['modes'][string]> = {};
  for (const mode of modes) {
    const m = exactRecord(raw[mode], [
      'enabled',
      'available',
      'ticket',
      'rake_bp',
      'terms_hash',
      'content_hash',
    ]);
    const ticket = creditsValue(m.ticket, { positive: true }, 'ticket');
    if (creditsToMilli(ticket) > 9000000000000000n) invalidResponse('ticket range');
    normalized[mode] = {
      enabled: booleanValue(m.enabled, 'mode enabled'),
      available: booleanValue(m.available, 'mode available'),
      ticket,
      rates: ratesValue(m.rake_bp),
      termsHash: hashValue(m.terms_hash),
      contentHash: hashValue(m.content_hash),
    };
  }
  return {
    enabled: booleanValue(r.enabled, 'enabled'),
    available: booleanValue(r.available, 'available'),
    modes: normalized,
  };
}
function profileValue(value: unknown): Profile {
  const r = exactRecord(value, ['kind'], ['display_name', 'avatar_url']);
  const kind = enumValue(r.kind, ['public', 'anonymous', 'deleted'], 'profile kind');
  if (kind !== 'public') {
    exactRecord(value, ['kind']);
    return { kind };
  }
  return {
    kind,
    displayName: textValue(r.display_name, 128, 'display name'),
    avatarURL: r.avatar_url === undefined ? null : httpsURL(r.avatar_url, 'avatar'),
  };
}
function optionalDecode<T>(value: unknown, decode?: (item: unknown) => T): T | null {
  if (value === null) return null;
  if (!decode) invalidResponse('unexpected game field');
  return decode(value);
}
function resolutionValue<P>(
  value: unknown,
  decode?: (item: unknown) => P,
  duration?: (value: P) => number,
): Resolution<P> | null {
  if (value === null) return null;
  const r = exactRecord(value, ['round', 'started_at', 'ends_at', 'summary']);
  const startedAt = unixTime(r.started_at, 'resolution start');
  const endsAt = unixTime(r.ends_at, 'resolution end');
  if (!decode) invalidResponse('resolution decoder');
  const summary = decode(r.summary);
  if (endsAt <= startedAt || endsAt - startedAt !== (duration?.(summary) ?? 5))
    invalidResponse('resolution duration');
  return {
    round: safeInteger(r.round, 1, 75, 'resolution round'),
    startedAt,
    endsAt,
    summary,
  };
}
function roundStartValue<S>(value: unknown, decode?: (item: unknown) => S): RoundStart<S> | null {
  if (value === null) return null;
  const r = exactRecord(value, ['round', 'started_at', 'events']);
  if (!decode) invalidResponse('round start');
  return {
    round: safeInteger(r.round, 1, 75, 'round start'),
    startedAt: unixTime(r.started_at, 'round start time'),
    events: decode(r.events),
  };
}
export function resultValue<V, F, P, S, L, A>(
  value: unknown,
  c: DuelCodec<V, F, P, S, L, A>,
): DuelResult<V, P> {
  const r = exactRecord(value, [
    'id',
    'game',
    'mode',
    'terminal_at',
    'outcome',
    'reason',
    'scores',
    'own_payment',
    'own_refund',
    'prize_general',
    'rake',
    'resolution',
    'you',
    'view',
    'profiles',
  ]);
  enumValue(r.game, [c.game], 'game');
  const rake = exactRecord(r.rake, ['platform', 'welfare', 'thursday']);
  return {
    id: opaqueID(r.id, prefix(c.game), 'result'),
    game: c.game,
    mode: enumValue(r.mode, c.modes, 'mode'),
    terminalAt: unixTime(r.terminal_at, 'terminal time'),
    outcome: enumValue(r.outcome, ['win', 'loss', 'draw', 'system_cancelled'], 'outcome'),
    reason: enumValue(
      r.reason,
      [
        'rounds',
        'target',
        'double-overload',
        'limit',
        'surrender',
        'server_restart',
        'account_unavailable',
      ],
      'reason',
    ),
    scores: pair(r.scores, (v) => safeInteger(v, 0, 1000000000, 'score')),
    payment: gamePayment(r.own_payment, 'payment'),
    refund: gamePayment(r.own_refund, 'refund'),
    prize: creditsValue(r.prize_general),
    rake: {
      platform: creditsValue(rake.platform),
      welfare: creditsValue(rake.welfare),
      thursday: creditsValue(rake.thursday),
    },
    profiles: pair(r.profiles, profileValue),
    you: seatValue(r.you),
    view: nullable(r.view, c.view),
    resolution: resolutionValue(r.resolution, c.presentation, c.presentationDuration),
  };
}
function stateValue<V, F, P, S, L, A>(
  value: unknown,
  c: DuelCodec<V, F, P, S, L, A>,
): DuelState<V, P, S> {
  const r = exactRecord(value, [
    'id',
    'game',
    'mode',
    'rules_version',
    'content_hash',
    'revision',
    'phase_seq',
    'phase',
    'round',
    'deadline',
    'server_now',
    'you',
    'locked',
    'ticket',
    'rake_bp',
    'own_payment',
    'view',
    'resolution',
    'round_start',
    'profiles',
  ]);
  enumValue(r.game, [c.game], 'game');
  safeInteger(r.rules_version, 1, 1, 'rules version');
  return {
    id: opaqueID(r.id, prefix(c.game), 'session'),
    game: c.game,
    mode: enumValue(r.mode, c.modes, 'mode'),
    contentHash: hashValue(r.content_hash),
    revision: revisionValue(r.revision),
    phaseSeq: revisionValue(r.phase_seq),
    phase: enumValue(
      r.phase,
      c.game === 'bidding' ? (['joker', 'bid'] as const) : (['plan', 'settlement'] as const),
      'phase',
    ),
    round: safeInteger(
      r.round,
      1,
      c.game === 'bidding' ? 13 : r.mode === 'quick' ? 25 : 75,
      'round',
    ),
    deadline: unixTime(r.deadline, 'deadline'),
    serverNow: unixTime(r.server_now, 'server time'),
    you: seatValue(r.you),
    locked: pair(r.locked, (v) => booleanValue(v, 'locked')),
    ticket: creditsValue(r.ticket, { positive: true }),
    rates: ratesValue(r.rake_bp),
    payment: gamePayment(r.own_payment, 'payment'),
    profiles: pair(r.profiles, profileValue),
    view: c.view(r.view),
    resolution: resolutionValue(r.resolution, c.presentation, c.presentationDuration),
    roundStart: roundStartValue(r.round_start, c.start),
  };
}
export function homeValue<V, F, P, S, L, A>(
  value: unknown,
  c: DuelCodec<V, F, P, S, L, A>,
): DuelHome<V, P, S, L> {
  const r = exactRecord(value, ['server_now', 'queue', 'current', 'latest_result']);
  if (r.queue !== null && r.current !== null) invalidResponse('occupied slot');
  const queue = nullable(r.queue, (value) => {
    const q = exactRecord(
      value,
      ['id', 'revision', 'mode', 'deadline', 'ticket', 'payment', 'terms_hash', 'rules_version'],
      ['loadout'],
    );
    safeInteger(q.rules_version, 1, 1, 'queue rules');
    if (c.game === 'bidding' && q.loadout !== undefined) invalidResponse('bidding loadout');
    if (c.game === 'likes' && (q.loadout === undefined || q.loadout === null))
      invalidResponse('likes loadout');
    return {
      id: opaqueID(q.id, prefix(c.game, true), 'queue'),
      revision: revisionValue(q.revision),
      mode: enumValue(q.mode, c.modes, 'queue mode'),
      deadline: unixTime(q.deadline, 'queue deadline'),
      ticket: creditsValue(q.ticket, { positive: true }),
      payment: gamePayment(q.payment, 'queue payment'),
      termsHash: hashValue(q.terms_hash),
      loadout: optionalDecode(q.loadout ?? null, c.loadout),
    };
  });
  return {
    serverNow: unixTime(r.server_now, 'server time'),
    queue,
    current: nullable(r.current, (v) => stateValue(v, c)),
    latestResult: nullable(r.latest_result, (v) => resultValue(v, c)),
  };
}
export function roundValue<V, F, P, S, L, A>(
  value: unknown,
  c: DuelCodec<V, F, P, S, L, A>,
): DuelRound<V, F, S> {
  const r = exactRecord(value, ['round', 'before', 'after', 'facts', 'start_events', 'timeouts']);
  return {
    round: safeInteger(r.round, 1, c.game === 'bidding' ? 13 : 75, 'round'),
    before: c.view(r.before),
    after: c.view(r.after),
    facts: c.facts(r.facts),
    startEvents: optionalDecode(r.start_events, c.start),
    timeouts: pair(r.timeouts, (v) => booleanValue(v, 'timeout')),
  };
}
export function detailValue<V, F, P, S, L, A>(
  value: unknown,
  c: DuelCodec<V, F, P, S, L, A>,
): DuelDetail<V, P, S, A> {
  const r = exactRecord(value, [
    'result',
    'rules_version',
    'content_hash',
    'ticket',
    'rake_bp',
    'initial',
    'terminal_actions',
    'round_start_events',
  ]);
  safeInteger(r.rules_version, 1, 1, 'history rules');
  return {
    result: resultValue(r.result, c),
    contentHash: hashValue(r.content_hash),
    ticket: creditsValue(r.ticket, { positive: true }),
    rates: ratesValue(r.rake_bp),
    initial: c.view(r.initial),
    terminalActions: pair(r.terminal_actions, (v) => optionalDecode(v, c.action)),
    roundStartEvents: optionalDecode(r.round_start_events, c.start),
  };
}
export function cursorValue(value: unknown): string | null {
  return nullable(value, (v) => textValue(v, 4096, 'cursor'));
}
