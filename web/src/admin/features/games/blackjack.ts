import { blackjackFact } from '@shared/games/blackjack';
import { decoded, queryPath } from '@shared/operations/api';
import {
  amount,
  array,
  decimal,
  integer,
  nullableString,
  nullableUnixSecond,
  oneOf,
  opaqueID,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';

export type BlackjackDataset = 'recent' | 'anonymous';
const datasets = ['recent', 'anonymous'] as const;
const phase = (v: unknown) => oneOf(v, ['result', 'cancelled'] as const, 'result phase');
const reason = (v: unknown) =>
  oneOf(v, ['completed', 'server_restart', 'closed', 'maintenance'] as const, 'result reason');
const cursor = (v: unknown) => nullableString(v, 'history cursor', { min: 1, max: 2048 });
function ref(v: unknown, dataset: BlackjackDataset) {
  return opaqueID(v, dataset === 'recent' ? 'bjt_' : 'bja_', 'table reference');
}
function summary(v: unknown, dataset: BlackjackDataset) {
  const keys = ['id', 'phase', 'reason', 'seats', 'total_stake', 'net'];
  const required = dataset === 'recent' ? [...keys, 'started_at', 'terminal_at'] : keys;
  const r = record(v, required, 'table summary');
  return {
    id: ref(r.id, dataset),
    phase: phase(r.phase),
    reason: reason(r.reason),
    seats: integer(r.seats, 'seat count', 1, 9),
    total_stake: amount(r.total_stake, 'total stake', false),
    net: amount(r.net, 'net paid', false),
    started_at: nullableUnixSecond(r.started_at ?? null, 'start'),
    terminal_at: nullableUnixSecond(r.terminal_at ?? null, 'finish'),
  };
}
export function blackjackAdminDetail(v: unknown) {
  const root = record(v, ['id', 'dataset', 'record', 'recent'], 'table detail', [
    'id',
    'dataset',
    'record',
  ]);
  const dataset = oneOf(root.dataset, datasets, 'history dataset');
  record(
    root,
    dataset === 'recent' ? ['id', 'dataset', 'record', 'recent'] : ['id', 'dataset', 'record'],
    'table detail',
  );
  const r = record(root.record, ['rules_version', 'phase', 'reason', 'fact'], 'table facts');
  integer(r.rules_version, 'rules version', 1, 1);
  const facts = {
    rules_version: 1,
    phase: phase(r.phase),
    reason: reason(r.reason),
    fact: blackjackFact(r.fact),
  };
  const recent =
    dataset === 'recent'
      ? (() => {
          const v = record(
            root.recent,
            ['started_at', 'terminal_at', 'participants'],
            'recent details',
          );
          return {
            started_at: unixSecond(v.started_at, 'start'),
            terminal_at: unixSecond(v.terminal_at, 'finish'),
            participants: array(v.participants, 'participants', 9).map((v) => {
              const p = record(v, ['seat', 'user_id', 'payment', 'operations'], 'participant');
              const paid = record(p.payment, ['general', 'game'], 'payment');
              return {
                seat: integer(p.seat, 'seat', 0, 8),
                user_id:
                  p.user_id === null ? null : decimal(p.user_id, 'user id', { positive: true }),
                payment: {
                  general: amount(paid.general, 'general paid', false),
                  game: amount(paid.game, 'game paid', false),
                },
                operations: array(p.operations, 'operations', 8).map((v) =>
                  opaqueID(v, 'op_', 'operation'),
                ),
              };
            }),
          };
        })()
      : null;
  return { id: ref(root.id, dataset), dataset, record: facts, ...(recent ? { recent } : {}) };
}
export function blackjackAdminHistory(v: unknown) {
  const r = record(v, ['dataset', 'items', 'next_cursor'], 'table history');
  const dataset = oneOf(r.dataset, datasets, 'history dataset');
  return {
    dataset,
    items: array(r.items, 'history items', 50).map((v) => summary(v, dataset)),
    next_cursor: cursor(r.next_cursor),
  };
}
export function blackjackAdminExport(v: unknown) {
  const r = record(v, ['format', 'dataset', 'items', 'next_cursor'], 'history export');
  oneOf(r.format, ['blackjack-history/v1'] as const, 'export format');
  const dataset = oneOf(r.dataset, datasets, 'export dataset');
  return {
    format: string(r.format, 'export format'),
    dataset,
    items: array(r.items, 'export items', 10).map(blackjackAdminDetail),
    next_cursor: cursor(r.next_cursor),
  };
}
const base = '/admin/api/games/blackjack/history';
export const getBlackjackHistory = (
  dataset: BlackjackDataset,
  cursor: string | null,
  signal: AbortSignal,
) => decoded(queryPath(base, { dataset, cursor, limit: 20 }), blackjackAdminHistory, { signal });
export const getBlackjackDetail = (dataset: BlackjackDataset, id: string, signal: AbortSignal) =>
  decoded(queryPath(`${base}/${ref(id, dataset)}`, { dataset }), blackjackAdminDetail, { signal });
export const exportBlackjack = (
  dataset: BlackjackDataset,
  cursor: string | null,
  signal: AbortSignal,
) =>
  decoded(`${base}/export`, blackjackAdminExport, {
    method: 'POST',
    json: { dataset, cursor },
    signal,
  });
