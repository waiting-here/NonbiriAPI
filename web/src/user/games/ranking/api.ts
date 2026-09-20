import { decoded, queryPath } from '@shared/operations/api';
import { normalizePageMetadata, validatePageResponse } from '@shared/operations/pageNumbers';
import {
  booleanValue,
  creditsValue,
  creditsToMilli,
  decimalValue,
  enumValue,
  exactRecord,
  invalidResponse,
  publicIdentity,
  unixTime,
} from '../common/strict';

export type RankBoard = 'charity' | 'game_charity' | 'bidding' | 'blackjack';
export type RankWindow = '7d' | '30d' | 'history';

function row(value: unknown) {
  const v = exactRecord(value, ['rank', 'amount', 'is_me', 'identity']);
  return {
    rank: decimalValue(v.rank, { bits: 128, positive: true }, 'rank'),
    amount: creditsValue(v.amount, { positive: true }, 'rank amount'),
    isMe: booleanValue(v.is_me, 'rank owner'),
    identity: publicIdentity(v.identity, 'rank identity'),
  };
}

export function normalizeRanking(value: unknown, board: RankBoard, window: RankWindow, page = '1') {
  const v = exactRecord(
    value,
    ['as_of', 'statistics_start', 'window', 'rows', 'me'],
    board === 'charity' ? ['pagination'] : [],
  );
  const asOf = unixTime(v.as_of, 'rank as of');
  const statisticsStart = unixTime(v.statistics_start, 'statistics start');
  if (
    enumValue(v.window, ['7d', '30d', 'history'], 'rank window') !== window ||
    statisticsStart > asOf
  )
    invalidResponse('rank window');
  if (!Array.isArray(v.rows) || v.rows.length > 20) invalidResponse('rank rows');
  const rows = v.rows.map(row);
  const me = v.me === null ? null : row(v.me);
  const pagination = board === 'charity' ? normalizePageMetadata(v.pagination) : null;
  let offset = 0n;
  if (pagination) {
    validatePageResponse(pagination, page, 20, rows.length);
    offset = (BigInt(pagination.page) - 1n) * 20n;
    if (me !== null) invalidResponse('charity extra row');
  }
  rows.forEach((row, i) => {
    if (
      BigInt(row.rank) !== offset + BigInt(i + 1) ||
      (i > 0 && creditsToMilli(row.amount) > creditsToMilli(rows[i - 1].amount))
    )
      invalidResponse('rank order');
  });
  if (
    rows.filter((r) => r.isMe).length > 1 ||
    (me &&
      (!me.isMe ||
        BigInt(me.rank) <= 20n ||
        rows.length !== 20 ||
        rows.some((r) => r.isMe) ||
        creditsToMilli(me.amount) > creditsToMilli(rows[19].amount)))
  )
    invalidResponse('rank owner');
  return { asOf, statisticsStart, window, rows, me, pagination };
}

export function loadRanking(
  board: RankBoard,
  window: RankWindow,
  page: string,
  signal: AbortSignal,
) {
  const path =
    board === 'charity'
      ? '/api/charity/leaderboard'
      : board === 'game_charity'
        ? '/api/games/leaderboards/charity'
        : `/api/games/${board}/leaderboard`;
  return decoded(
    queryPath(path, board === 'charity' ? { page } : board === 'game_charity' ? {} : { window }),
    (value) => normalizeRanking(value, board, window, page),
    { signal },
  );
}
