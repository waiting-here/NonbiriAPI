import { queryPath } from '@shared/operations/api';
import {
  amount,
  array,
  decimal,
  integer,
  invalidResponse,
  nullableString,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';
import { gameRequest } from '../../../user/games/common/request';
import { modesFor, type GameID } from './copy';

export type Dataset = 'recent' | 'anonymous';
export interface Selection {
  mode?: string;
  rules_version?: number;
  outcome?: string;
  from?: number;
  to?: number;
}
export type JSONValue =
  null | boolean | number | string | JSONValue[] | { [key: string]: JSONValue };
export interface Recent {
  started_at: number;
  terminal_at: number;
  operation_id: string;
  ledger_seq: string;
  participants: {
    user_id: string | null;
    display_name: string;
    general_paid: string;
    game_paid: string;
  }[];
}
export interface Summary {
  match_ref: string;
  game: GameID;
  mode: string;
  rules_version: number;
  content_hash: string;
  outcome: 'normal' | 'draw' | 'system_cancelled';
  reason: string;
  winner: number | null;
  scores: number[];
  ticket: string;
  prize: string;
  cuts: { platform: string; welfare: string; thursday: string };
  recent?: Recent;
}
export interface Facts extends Omit<Summary, 'match_ref' | 'recent'> {
  initial: JSONValue;
  final: JSONValue;
  terminal_actions: JSONValue[];
  round_start_events: JSONValue;
  rake_bp: { platform: number; welfare: number; thursday: number };
}
export interface Match {
  kind: 'match';
  match_ref: string;
  facts: Facts;
  recent?: Recent;
}
export interface Round {
  round: number;
  before: JSONValue;
  after: JSONValue;
  facts: JSONValue;
  start_events: JSONValue;
  timeouts: boolean[];
}
export interface RoundRecord {
  kind: 'round';
  match_ref: string;
  round_no: number;
  record: Round;
}
export type ExportRecord = Match | RoundRecord;
export interface ExportPage {
  format: 'duel-history-v1';
  dataset: Dataset;
  items: ExportRecord[];
  next_cursor: string | null;
  expired_skipped: number;
}
const encoder = new TextEncoder();
const cursor = (value: unknown) =>
  nullableString(value, 'history cursor', { min: 1, max: 4096, bytes: 4096, ascii: true });
const ref = (value: unknown, game: GameID, dataset: Dataset) =>
  opaqueID(
    value,
    dataset === 'anonymous' ? 'dah_' : game === 'likes' ? 'lik_' : 'bid_',
    'match reference',
  );
const hash = (value: unknown) => {
  const s = string(value, 'content hash', { min: 64, max: 64, ascii: true });
  if (!/^[a-f0-9]{64}$/.test(s)) invalidResponse('content hash');
  return s;
};
const pair = (value: unknown, label: string) => {
  const a = array(value, label, 2);
  if (a.length !== 2) invalidResponse(label);
  return a;
};
const forbidden = new Set(['__proto__', 'constructor', 'prototype']);
const anonymousPrivate = new Set([
  'user_id',
  'discord_id',
  'display_name',
  'avatar_url',
  'session_id',
  'queue_id',
  'operation_id',
  'request_id',
  'started_at',
  'terminal_at',
  'deadline',
  'device_hash',
  'ip_hash',
  'export_seq',
  'general_paid',
  'game_paid',
]);
function jsonValue(value: unknown, anonymous = false): JSONValue {
  let nodes = 0;
  const visit = (v: unknown, depth: number): JSONValue => {
    if (++nodes > 200_000 || depth > 32) invalidResponse('game record depth');
    if (v === null || typeof v === 'boolean') return v;
    if (typeof v === 'number') return integer(v, 'game number', -Number.MAX_SAFE_INTEGER);
    if (typeof v === 'string')
      return string(v, 'game text', { max: 65_536, bytes: 262_144, multiline: true });
    if (Array.isArray(v))
      return array(v, 'game array', 4096).map((entry) => visit(entry, depth + 1));
    if (typeof v !== 'object') invalidResponse('game value');
    const entries = Object.entries(v as Record<string, unknown>);
    if (
      entries.length > 4096 ||
      entries.some(([key]) => forbidden.has(key) || (anonymous && anonymousPrivate.has(key)))
    )
      invalidResponse('game record fields');
    return Object.fromEntries(
      entries.map(([key, entry]) => [
        string(key, 'game field', { max: 160 }),
        visit(entry, depth + 1),
      ]),
    );
  };
  return visit(value, 0);
}
function recent(value: unknown): Recent {
  const r = record(
    value,
    ['started_at', 'terminal_at', 'operation_id', 'ledger_seq', 'participants'],
    'recent match',
  );
  return {
    started_at: unixSecond(r.started_at, 'match start'),
    terminal_at: unixSecond(r.terminal_at, 'match end'),
    operation_id: opaqueID(r.operation_id, 'op_', 'terminal operation'),
    ledger_seq: decimal(r.ledger_seq, 'terminal order', { positive: true }),
    participants: pair(r.participants, 'participants').map((value) => {
      const p = record(
        value,
        ['user_id', 'display_name', 'general_paid', 'game_paid'],
        'participant',
      );
      return {
        user_id: p.user_id === null ? null : decimal(p.user_id, 'participant', { positive: true }),
        display_name: string(p.display_name, 'participant name', { max: 128 }),
        general_paid: amount(p.general_paid, 'general payment', false),
        game_paid: amount(p.game_paid, 'game payment', false),
      };
    }),
  };
}
const summaryKeys = [
  'game',
  'mode',
  'rules_version',
  'content_hash',
  'outcome',
  'reason',
  'winner',
  'scores',
  'ticket',
  'prize',
  'cuts',
];
function summaryFields(
  r: Record<string, unknown>,
  game: GameID,
): Omit<Summary, 'match_ref' | 'recent'> {
  const cuts = record(r.cuts, ['platform', 'welfare', 'thursday'], 'match cuts');
  return {
    game: oneOf(r.game, [game], 'game'),
    mode: oneOf(r.mode, modesFor(game), 'mode'),
    rules_version: integer(r.rules_version, 'rules version', 1, 2147483647),
    content_hash: hash(r.content_hash),
    outcome: oneOf(r.outcome, ['normal', 'draw', 'system_cancelled'], 'outcome'),
    reason: string(r.reason, 'outcome reason', { max: 64, ascii: true }),
    winner: r.winner === null ? null : integer(r.winner, 'winner', 0, 1),
    scores: pair(r.scores, 'scores').map((s) => integer(s, 'score', 0, 1_000_000_000)),
    ticket: amount(r.ticket, 'ticket', false),
    prize: amount(r.prize, 'prize', false),
    cuts: {
      platform: amount(cuts.platform, 'platform cut', false),
      welfare: amount(cuts.welfare, 'welfare cut', false),
      thursday: amount(cuts.thursday, 'Thursday cut', false),
    },
  };
}
export function normalizeSummary(value: unknown, game: GameID, dataset: Dataset): Summary {
  const keys = [...summaryKeys, 'match_ref', ...(dataset === 'recent' ? ['recent'] : [])];
  const r = record(value, keys, 'history summary');
  return {
    ...summaryFields(r, game),
    match_ref: ref(r.match_ref, game, dataset),
    ...(dataset === 'recent' ? { recent: recent(r.recent) } : {}),
  };
}
export function normalizeMatch(value: unknown, game: GameID, dataset: Dataset): Match {
  const r = record(
    value,
    ['kind', 'match_ref', 'facts', ...(dataset === 'recent' ? ['recent'] : [])],
    'match record',
  );
  const f = record(
    r.facts,
    [...summaryKeys, 'initial', 'final', 'terminal_actions', 'round_start_events', 'rake_bp'],
    'match facts',
  );
  const bp = record(f.rake_bp, ['platform', 'welfare', 'thursday'], 'rake');
  const rates = {
    platform: integer(bp.platform, 'platform rake', 0, 9999),
    welfare: integer(bp.welfare, 'welfare rake', 0, 9999),
    thursday: integer(bp.thursday, 'Thursday rake', 0, 9999),
  };
  if (rates.platform + rates.welfare + rates.thursday >= 10000) invalidResponse('rake');
  const anonymous = dataset === 'anonymous';
  return {
    kind: oneOf(r.kind, ['match'], 'record kind'),
    match_ref: ref(r.match_ref, game, dataset),
    facts: {
      ...summaryFields(f, game),
      initial: jsonValue(f.initial, anonymous),
      final: jsonValue(f.final, anonymous),
      terminal_actions: pair(f.terminal_actions, 'terminal actions').map((a) =>
        jsonValue(a, anonymous),
      ),
      round_start_events: jsonValue(f.round_start_events, anonymous),
      rake_bp: rates,
    },
    ...(dataset === 'recent' ? { recent: recent(r.recent) } : {}),
  };
}
export function normalizeRound(value: unknown, dataset: Dataset): Round {
  const r = record(
    value,
    ['round', 'before', 'after', 'facts', 'start_events', 'timeouts'],
    'round record',
  );
  return {
    round: integer(r.round, 'round', 1, 75),
    before: jsonValue(r.before, dataset === 'anonymous'),
    after: jsonValue(r.after, dataset === 'anonymous'),
    facts: jsonValue(r.facts, dataset === 'anonymous'),
    start_events: jsonValue(r.start_events, dataset === 'anonymous'),
    timeouts: pair(r.timeouts, 'timeouts').map((v) => {
      if (typeof v !== 'boolean') invalidResponse('timeout');
      return v as boolean;
    }),
  };
}
export function normalizeExport(value: unknown, game: GameID, dataset: Dataset): ExportPage {
  const r = record(
    value,
    ['format', 'dataset', 'items', 'next_cursor', 'expired_skipped'],
    'export page',
  );
  return {
    format: oneOf(r.format, ['duel-history-v1'], 'export format'),
    dataset: oneOf(r.dataset, [dataset], 'dataset'),
    next_cursor: cursor(r.next_cursor),
    expired_skipped: integer(r.expired_skipped, 'expired matches', 0, 200),
    items: array(r.items, 'export records', 100).map((value) => {
      if (encoder.encode(JSON.stringify(value)).byteLength > 1_048_576)
        invalidResponse('record size');
      const k = record(
        value,
        ['kind', 'match_ref', 'facts', 'recent', 'round_no', 'record'],
        'export record',
        ['kind', 'match_ref'],
      );
      if (k.kind === 'match') return normalizeMatch(value, game, dataset);
      const item = record(value, ['kind', 'match_ref', 'round_no', 'record'], 'round export');
      const n = integer(item.round_no, 'round number', 1, 75),
        round = normalizeRound(item.record, dataset);
      if (n !== round.round) invalidResponse('round reference');
      return {
        kind: oneOf(item.kind, ['round'], 'record kind'),
        match_ref: ref(item.match_ref, game, dataset),
        round_no: n,
        record: round,
      };
    }),
  };
}
const base = (game: GameID) =>
  `/admin/api/games/${oneOf(game, ['bidding', 'likes'], 'game')}/history`;
export async function getHistory(
  game: GameID,
  dataset: Dataset,
  selection: Selection,
  after: string | null,
  signal?: AbortSignal,
) {
  const { data } = await gameRequest<unknown>(
    queryPath(base(game), { dataset, ...selection, cursor: after, limit: 20 }),
    { signal, maxResponseBytes: 1_048_576 },
  );
  const r = record(data, ['dataset', 'items', 'next_cursor'], 'history page');
  oneOf(r.dataset, [dataset], 'dataset');
  return {
    items: array(r.items, 'history items', 20).map((v) => normalizeSummary(v, game, dataset)),
    next_cursor: cursor(r.next_cursor),
  };
}
export async function getMatch(game: GameID, dataset: Dataset, id: string, signal?: AbortSignal) {
  const { data } = await gameRequest<unknown>(
    queryPath(`${base(game)}/${encodeURIComponent(ref(id, game, dataset))}`, { dataset }),
    { signal, maxResponseBytes: 1_048_576 },
  );
  return normalizeMatch(data, game, dataset);
}
export async function getRounds(
  game: GameID,
  dataset: Dataset,
  id: string,
  after: string | null,
  signal?: AbortSignal,
) {
  const { data } = await gameRequest<unknown>(
    queryPath(`${base(game)}/${encodeURIComponent(ref(id, game, dataset))}/rounds`, {
      dataset,
      cursor: after,
      limit: 5,
    }),
    { signal, maxResponseBytes: 8_388_608 },
  );
  const r = record(data, ['items', 'next_cursor', 'server_now'], 'round page');
  unixSecond(r.server_now, 'server clock');
  return {
    items: array(r.items, 'rounds', 5).map((v) => normalizeRound(v, dataset)),
    next_cursor: cursor(r.next_cursor),
  };
}
export async function getExportPage(
  game: GameID,
  dataset: Dataset,
  selection: Selection,
  after: string | null,
  signal?: AbortSignal,
) {
  const { data } = await gameRequest<unknown>(`${base(game)}/export`, {
    method: 'POST',
    json: { dataset, selection, cursor: after },
    signal,
    maxResponseBytes: 8_388_608,
    expectedStatuses: [200],
  });
  return normalizeExport(data, game, dataset);
}
