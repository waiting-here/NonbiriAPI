import {
  amount,
  array,
  boolean,
  decimal,
  integer,
  invalidResponse,
  nullableInteger,
  nullableString,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';

export const BLACKJACK_ACTIONS = ['hit', 'stand', 'double', 'split'] as const;
export const BLACKJACK_EMOTES = ['hello', 'nice', 'wow', 'good_luck', 'thanks', 'gg'] as const;
export type BlackjackAction = (typeof BLACKJACK_ACTIONS)[number];
export type BlackjackEmote = (typeof BLACKJACK_EMOTES)[number];
const phases = ['seating', 'decision', 'result', 'cancelled'] as const;
const outcomes = ['', 'loss', 'push', 'win', 'natural'] as const;
const configFields = [
  'enabled',
  'min_stake',
  'max_stake',
  'stake_step',
  'default_stake',
  'rake_bp',
  'quick_stakes',
];
const snapshotFields = [
  'available',
  'config_hash',
  'queue_capacity',
  'seats',
  'seating_seconds',
  'decision_seconds',
  'round_seconds',
];
export interface BlackjackConfig {
  enabled: boolean;
  min_stake: string;
  max_stake: string;
  stake_step: string;
  default_stake: string;
  rake_bp: { platform: number; welfare: number; thursday: number };
  quick_stakes: string[];
}
const money = (v: unknown) => amount(v, 'blackjack amount', false);
const wide = (v: unknown) => decimal(v, 'blackjack integer');
const seatNo = (v: unknown) => integer(v, 'blackjack seat', 0, 8);
function rates(v: unknown) {
  const r = record(v, ['platform', 'welfare', 'thursday'], 'blackjack fees');
  const result = {
    platform: integer(r.platform, 'platform fee', 0, 9999),
    welfare: integer(r.welfare, 'welfare fee', 0, 9999),
    thursday: integer(r.thursday, 'Thursday fee', 0, 9999),
  };
  if (result.platform + result.welfare + result.thursday >= 10000)
    invalidResponse('blackjack fees');
  return result;
}
export function blackjackConfig(v: unknown): BlackjackConfig {
  const r = record(v, configFields, 'blackjack configuration');
  const quickStakes = array(r.quick_stakes, 'quick stakes', 8).map(money);
  const milli = (v: string) => {
    const [whole, fraction = ''] = v.split('.');
    return BigInt(whole) * 1000n + BigInt(fraction.padEnd(3, '0'));
  };
  const minimum = milli(money(r.min_stake));
  const maximum = milli(money(r.max_stake));
  const step = milli(money(r.stake_step));
  let previous = 0n;
  for (const value of quickStakes) {
    const stake = milli(value);
    if (step <= 0n || stake < minimum || stake > maximum || stake <= previous || (stake - minimum) % step !== 0n)
      invalidResponse('quick stakes');
    previous = stake;
  }
  return {
    enabled: boolean(r.enabled, 'blackjack enabled'),
    min_stake: money(r.min_stake),
    max_stake: money(r.max_stake),
    stake_step: money(r.stake_step),
    default_stake: money(r.default_stake),
    rake_bp: rates(r.rake_bp),
    quick_stakes: quickStakes,
  };
}
function hash(v: unknown) {
  const value = string(v, 'configuration hash', { min: 64, max: 64 });
  if (!/^[a-f0-9]{64}$/.test(value)) invalidResponse('configuration hash');
  return value;
}
export function blackjackSnapshot(v: unknown) {
  const r = record(v, [...configFields, ...snapshotFields], 'blackjack snapshot');
  const cfg = blackjackConfig(Object.fromEntries(configFields.map((k) => [k, r[k]])));
  integer(r.queue_capacity, 'queue capacity', 4096, 4096);
  integer(r.seats, 'seats', 9, 9);
  integer(r.seating_seconds, 'seating seconds', 15, 15);
  integer(r.decision_seconds, 'decision seconds', 30, 30);
  integer(r.round_seconds, 'round seconds', 60, 60);
  return {
    ...cfg,
    available: boolean(r.available, 'availability'),
    config_hash: hash(r.config_hash),
  };
}
export type BlackjackSnapshot = ReturnType<typeof blackjackSnapshot>;
function payment(v: unknown) {
  const r = record(v, ['general', 'game'], 'payment');
  return { general: money(r.general), game: money(r.game) };
}
function total(v: unknown) {
  const r = record(v, ['value', 'soft'], 'hand total');
  return { value: integer(r.value, 'hand total', 0, 31), soft: boolean(r.soft, 'soft hand') };
}
export function blackjackCard(v: unknown) {
  const r = record(v, ['rank', 'suit'], 'card');
  return { rank: integer(r.rank, 'card rank', 1, 13), suit: integer(r.suit, 'card suit', 0, 3) };
}
export type BlackjackCard = ReturnType<typeof blackjackCard>;
function hand(v: unknown) {
  const required = ['cards', 'revision', 'units', 'total', 'natural', 'split', 'stood'];
  const r = record(v, [...required, 'outcome'], 'hand', required);
  return {
    cards: array(r.cards, 'cards', 21).map(blackjackCard),
    revision: decimal(r.revision, 'hand revision', { positive: true }),
    units: integer(r.units, 'hand stake', 1, 2),
    total: total(r.total),
    natural: boolean(r.natural, 'natural'),
    split: boolean(r.split, 'split'),
    stood: boolean(r.stood, 'stood'),
    outcome: oneOf(r.outcome ?? '', outcomes, 'hand outcome'),
  };
}
export type BlackjackHand = ReturnType<typeof hand>;
function cards(v: unknown) {
  const r = record(
    v,
    ['seats', 'dealer', 'dealer_total', 'hole_hidden', 'finished'],
    'table cards',
  );
  const seats = array(r.seats, 'seats', 9).map((v) => {
    const p = record(v, ['number', 'hands'], 'seat');
    return { number: seatNo(p.number), hands: array(p.hands, 'hands', 2).map(hand) };
  });
  if (new Set(seats.map((s) => s.number)).size !== seats.length) invalidResponse('duplicate seat');
  const dealer = array(r.dealer, 'dealer cards', 21).map(blackjackCard);
  const hidden = boolean(r.hole_hidden, 'dealer hole card');
  if (hidden && dealer.length !== 1) invalidResponse('hidden dealer card');
  return {
    seats,
    dealer,
    dealer_total: total(r.dealer_total),
    hole_hidden: hidden,
    finished: boolean(r.finished, 'finished'),
  };
}
function settlement(v: unknown) {
  const r = record(
    v,
    [
      'stake_milli',
      'gross_milli',
      'net_milli',
      'platform_milli',
      'welfare_milli',
      'thursday_milli',
      'outcome',
    ],
    'settlement',
  );
  return {
    stake_milli: wide(r.stake_milli),
    gross_milli: wide(r.gross_milli),
    net_milli: wide(r.net_milli),
    platform_milli: wide(r.platform_milli),
    welfare_milli: wide(r.welfare_milli),
    thursday_milli: wide(r.thursday_milli),
    outcome: oneOf(r.outcome, ['loss', 'push', 'win', 'natural'] as const, 'settlement outcome'),
  };
}
export function blackjackFact(v: unknown) {
  const r = record(v, ['cards', 'seats', 'settlements', 'refunds'], 'table facts', [
    'cards',
    'seats',
    'settlements',
  ]);
  return {
    cards: r.cards === null ? null : cards(r.cards),
    seats: array(r.seats, 'seat terms', 9).map((v) => {
      const required = ['seat', 'stake', 'rake_bp', 'stopped'];
      const p = record(v, [...required, 'emote', 'emote_at'], 'seat terms', required);
      return {
        seat: seatNo(p.seat),
        stake: money(p.stake),
        rake_bp: rates(p.rake_bp),
        stopped: boolean(p.stopped, 'stopped'),
        ...(p.emote === undefined ? {} : { emote: oneOf(p.emote, BLACKJACK_EMOTES, 'emote') }),
        ...(p.emote_at === undefined ? {} : { emote_at: unixSecond(p.emote_at, 'emote time') }),
      };
    }),
    settlements: array(r.settlements, 'settlements', 9).map((v) => {
      const p = record(v, ['seat', 'hands'], 'seat settlement');
      return { seat: seatNo(p.seat), hands: array(p.hands, 'settled hands', 2).map(settlement) };
    }),
    ...(r.refunds === undefined
      ? {}
      : {
          refunds: array(r.refunds, 'refunds', 9).map((v) => {
            const p = record(v, ['seat', 'amount'], 'refund');
            return { seat: seatNo(p.seat), amount: money(p.amount) };
          }),
        }),
  };
}
export type BlackjackFact = ReturnType<typeof blackjackFact>;
export function blackjackTable(v: unknown) {
  const required = [
    'id',
    'revision',
    'started_at',
    'phase',
    'deadline',
    'next_round_at',
    'terminal_at',
    'fact',
  ];
  const r = record(v, [...required, 'reason'], 'table', required);
  return {
    id: opaqueID(r.id, 'bjt_', 'table id'),
    revision: decimal(r.revision, 'table revision', { positive: true }),
    started_at: unixSecond(r.started_at, 'start'),
    phase: oneOf(r.phase, phases, 'phase'),
    deadline: unixSecond(r.deadline, 'deadline'),
    next_round_at: unixSecond(r.next_round_at, 'next round'),
    terminal_at: nullableUnixSecond(r.terminal_at, 'terminal time'),
    reason: oneOf(
      r.reason ?? '',
      ['', 'completed', 'server_restart', 'closed', 'maintenance'] as const,
      'reason',
    ),
    fact: blackjackFact(r.fact),
  };
}
export type BlackjackTable = ReturnType<typeof blackjackTable>;
function own(v: unknown) {
  const r = record(
    v,
    [
      'id',
      'position',
      'state',
      'stake',
      'rake_bp',
      'payment',
      'seat',
      'session_id',
      'pending',
      'legal_actions',
    ],
    'own entry',
  );
  const legal = record(r.legal_actions, ['0', '1'], 'legal hands', []);
  const actions: Record<string, BlackjackAction[]> = {};
  for (const key of Object.keys(legal))
    actions[key] =
      legal[key] === null
        ? []
        : array(legal[key], 'legal actions', 4).map((v) => oneOf(v, BLACKJACK_ACTIONS, 'action'));
  return {
    id: opaqueID(r.id, 'bjq_', 'entry id'),
    position: wide(r.position),
    state: oneOf(r.state, ['waiting', 'seated', 'playing'] as const, 'entry state'),
    stake: money(r.stake),
    rake_bp: rates(r.rake_bp),
    payment: payment(r.payment),
    seat: nullableInteger(r.seat, 'own seat', 0, 8),
    session_id: r.session_id === null ? null : opaqueID(r.session_id, 'bjt_', 'own table'),
    pending: boolean(r.pending, 'pending action'),
    legal_actions: actions,
  };
}
export function blackjackState(v: unknown) {
  const r = record(
    v,
    [
      'server_now',
      'phase',
      'deadline',
      'next_round_at',
      'config',
      'config_hash',
      'queue_count',
      'you',
      'your_seat',
      'table',
    ],
    'blackjack state',
  );
  return {
    server_now: unixSecond(r.server_now, 'server time'),
    phase: oneOf(r.phase, phases, 'phase'),
    deadline: unixSecond(r.deadline, 'deadline'),
    next_round_at: unixSecond(r.next_round_at, 'next round'),
    config: blackjackConfig(r.config),
    config_hash: hash(r.config_hash),
    queue_count: wide(r.queue_count),
    you: r.you === null ? null : own(r.you),
    your_seat: nullableInteger(r.your_seat, 'your seat', 0, 8),
    table: r.table === null ? null : blackjackTable(r.table),
  };
}
export type BlackjackState = ReturnType<typeof blackjackState>;
export function blackjackSummary(v: unknown) {
  const r = record(
    v,
    [
      'id',
      'started_at',
      'terminal_at',
      'phase',
      'reason',
      'seat',
      'stake',
      'total_stake',
      'net',
      'payment',
    ],
    'history summary',
  );
  return {
    id: opaqueID(r.id, 'bjt_', 'history id'),
    started_at: unixSecond(r.started_at, 'start'),
    terminal_at: unixSecond(r.terminal_at, 'end'),
    phase: oneOf(r.phase, ['result', 'cancelled'] as const, 'result phase'),
    reason: oneOf(
      r.reason,
      ['completed', 'server_restart', 'closed', 'maintenance'] as const,
      'reason',
    ),
    seat: seatNo(r.seat),
    stake: money(r.stake),
    total_stake: money(r.total_stake),
    net: money(r.net),
    payment: payment(r.payment),
  };
}
export function blackjackDetail(v: unknown) {
  const r = record(v, ['summary', 'table'], 'history detail');
  return { summary: blackjackSummary(r.summary), table: blackjackTable(r.table) };
}
export function blackjackHistory(v: unknown) {
  const r = record(v, ['items', 'next_cursor'], 'blackjack history');
  return {
    items: array(r.items, 'history', 50).map(blackjackSummary),
    next_cursor: nullableString(r.next_cursor, 'cursor', { min: 1, max: 2048 }),
  };
}
