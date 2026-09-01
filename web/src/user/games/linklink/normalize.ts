import { LINKLINK_SPECS, type LinkLinkSpec } from '../common/types';
import {
  booleanValue,
  creditsValue,
  decimalValue,
  enumValue,
  exactRecord,
  invalidResponse,
  opaqueID,
  safeInteger,
  unixTime,
} from '../common/strict';
import type { LinkLinkCurrent, LinkLinkState, LinkLinkSummary, LinkLinkTile } from './types';

const DIMENSIONS: Readonly<Record<LinkLinkSpec, readonly [number, number]>> = {
  '6x8': [6, 8],
  '8x8': [8, 8],
  '10x10': [10, 10],
};
const SECONDS: Readonly<Record<LinkLinkSpec, 150 | 180 | 240>> = {
  '6x8': 150,
  '8x8': 180,
  '10x10': 240,
};
const TILE_KEY = /^tile_[0-9]{2}$/;

function specValue(value: unknown): LinkLinkSpec {
  return enumValue(value, LINKLINK_SPECS, 'LinkLink spec');
}

export function normalizeLinkLinkState(value: unknown): LinkLinkState {
  const record = exactRecord(
    value,
    [
      'session_id',
      'spec',
      'price',
      'state',
      'revision',
      'board',
      'pairs_removed',
      'total_pairs',
      'started_at',
      'deadline',
      'server_now',
    ],
    [],
    'LinkLink state',
  );
  const spec = specValue(record.spec);
  if (record.state !== 'active') invalidResponse('LinkLink active state');
  const [rows, cols] = DIMENSIONS[spec];
  const board = exactRecord(record.board, ['rows', 'cols', 'tiles'], [], 'LinkLink board');
  safeInteger(board.rows, rows, rows, 'LinkLink board rows');
  safeInteger(board.cols, cols, cols, 'LinkLink board columns');
  if (!Array.isArray(board.tiles) || board.tiles.length !== rows * cols)
    invalidResponse('LinkLink tiles');
  const seen = new Set<string>();
  const keyCounts = new Map<string, number>();
  const removedCounts = new Map<string, number>();
  const tiles: LinkLinkTile[] = board.tiles.map((value, index) => {
    const tile = exactRecord(
      value,
      ['row', 'col', 'tile_key', 'removed'],
      [],
      `LinkLink tile ${index}`,
    );
    const row = safeInteger(tile.row, 0, rows - 1, `LinkLink tile ${index} row`);
    const col = safeInteger(tile.col, 0, cols - 1, `LinkLink tile ${index} column`);
    const coordinate = `${row}:${col}`;
    if (seen.has(coordinate)) invalidResponse('LinkLink duplicate coordinate');
    seen.add(coordinate);
    if (typeof tile.tile_key !== 'string' || !TILE_KEY.test(tile.tile_key))
      invalidResponse(`LinkLink tile ${index} key`);
    const keyNumber = Number(tile.tile_key.slice(5));
    if (keyNumber < 1 || keyNumber > (rows * cols) / 4)
      invalidResponse(`LinkLink tile ${index} roster`);
    const removed = booleanValue(tile.removed, `LinkLink tile ${index} removed`);
    keyCounts.set(tile.tile_key, (keyCounts.get(tile.tile_key) ?? 0) + 1);
    if (removed) removedCounts.set(tile.tile_key, (removedCounts.get(tile.tile_key) ?? 0) + 1);
    return { row, col, tileKey: tile.tile_key, removed };
  });
  if (
    [...keyCounts.values()].some((count) => count !== 4) ||
    [...removedCounts.values()].some((count) => count !== 2 && count !== 4)
  ) {
    invalidResponse('LinkLink tile multiplicity');
  }
  const totalPairs = safeInteger(
    record.total_pairs,
    (rows * cols) / 2,
    (rows * cols) / 2,
    'LinkLink total pairs',
  );
  const pairsRemoved = safeInteger(
    record.pairs_removed,
    0,
    totalPairs - 1,
    'LinkLink removed pairs',
  );
  if (tiles.filter((tile) => tile.removed).length !== pairsRemoved * 2)
    invalidResponse('LinkLink removal count');
  const startedAt = unixTime(record.started_at, 'LinkLink start time');
  const deadline = unixTime(record.deadline, 'LinkLink deadline');
  const serverNow = unixTime(record.server_now, 'LinkLink server time');
  if (deadline - startedAt !== SECONDS[spec] || serverNow < startedAt || serverNow >= deadline)
    invalidResponse('LinkLink time range');
  return {
    kind: 'active',
    sessionID: opaqueID(record.session_id, 'll_', 'LinkLink session id'),
    spec,
    price: creditsValue(record.price, { positive: true }, 'LinkLink price'),
    revision: decimalValue(record.revision, { bits: 128, positive: true }, 'LinkLink revision'),
    board: { rows, cols, tiles },
    pairsRemoved,
    totalPairs,
    startedAt,
    deadline,
    serverNow,
  };
}

export function normalizeLinkLinkSummary(value: unknown): LinkLinkSummary {
  const record = exactRecord(
    value,
    [
      'session_id',
      'spec',
      'price',
      'terminal_reason',
      'started_at',
      'deadline',
      'terminal_at',
      'pairs_removed',
      'total_pairs',
      'score',
    ],
    [],
    'LinkLink summary',
  );
  const spec = specValue(record.spec);
  const [rows, cols] = DIMENSIONS[spec];
  const totalPairs = safeInteger(
    record.total_pairs,
    (rows * cols) / 2,
    (rows * cols) / 2,
    'LinkLink summary pairs',
  );
  const pairsRemoved = safeInteger(
    record.pairs_removed,
    0,
    totalPairs,
    'LinkLink summary progress',
  );
  const terminalReason = enumValue(
    record.terminal_reason,
    ['completed', 'timed_out', 'abandoned'] as const,
    'LinkLink terminal reason',
  );
  const startedAt = unixTime(record.started_at, 'LinkLink summary start');
  const deadline = unixTime(record.deadline, 'LinkLink summary deadline');
  const terminalAt = unixTime(record.terminal_at, 'LinkLink summary terminal time');
  if (
    deadline - startedAt !== SECONDS[spec] ||
    terminalAt < startedAt ||
    (terminalReason === 'completed' && (pairsRemoved !== totalPairs || terminalAt > deadline)) ||
    (terminalReason === 'timed_out' && (terminalAt < deadline || pairsRemoved === totalPairs)) ||
    (terminalReason === 'abandoned' && terminalAt >= deadline)
  ) {
    invalidResponse('LinkLink summary matrix');
  }
  if (
    (terminalReason === 'abandoned' && record.score !== null) ||
    (terminalReason !== 'abandoned' && record.score === null)
  )
    invalidResponse('LinkLink terminal score');
  const score =
    record.score === null ? null : decimalValue(record.score, { bits: 256 }, 'LinkLink score');
  if (score !== null) {
    const expected = BigInt(pairsRemoved * 100) + BigInt(Math.max(0, deadline - terminalAt));
    if (BigInt(score) !== expected) invalidResponse('LinkLink score arithmetic');
  }
  return {
    kind: 'summary',
    sessionID: opaqueID(record.session_id, 'll_', 'LinkLink summary id'),
    spec,
    price: creditsValue(record.price, { positive: true }, 'LinkLink summary price'),
    terminalReason,
    startedAt,
    deadline,
    terminalAt,
    pairsRemoved,
    totalPairs,
    score,
  };
}

export function normalizeLinkLinkCurrent(value: unknown): LinkLinkCurrent {
  if (value === null) return null;
  const discriminator = exactRecord(
    value,
    [],
    [
      'session_id',
      'spec',
      'price',
      'state',
      'revision',
      'board',
      'pairs_removed',
      'total_pairs',
      'started_at',
      'deadline',
      'server_now',
      'terminal_reason',
      'terminal_at',
      'score',
    ],
    'LinkLink current',
  );
  return Object.prototype.hasOwnProperty.call(discriminator, 'terminal_reason')
    ? normalizeLinkLinkSummary(value)
    : normalizeLinkLinkState(value);
}

export function normalizeLinkLinkLease(value: unknown): number {
  return unixTime(
    exactRecord(value, ['expires_at'], [], 'LinkLink lease').expires_at,
    'LinkLink lease expiry',
  );
}

export function boardWasRearranged(before: LinkLinkState, after: LinkLinkState): boolean {
  const removed = new Set(
    before.board.tiles
      .filter(
        (tile) =>
          !tile.removed &&
          after.board.tiles.find((next) => next.row === tile.row && next.col === tile.col)?.removed,
      )
      .map((tile) => `${tile.row}:${tile.col}`),
  );
  return before.board.tiles.some((tile) => {
    if (tile.removed || removed.has(`${tile.row}:${tile.col}`)) return false;
    const next = after.board.tiles.find(
      (candidate) => candidate.row === tile.row && candidate.col === tile.col,
    );
    return next && !next.removed && next.tileKey !== tile.tileKey;
  });
}

export function shouldApplyLinkLinkReplacement(
  current: LinkLinkCurrent | undefined,
  next: LinkLinkCurrent,
): boolean {
  if (!current) return true;
  if (!next) return current.kind === 'summary';
  if (current.kind === 'active') {
    if (current.sessionID !== next.sessionID) return false;
    return next.kind === 'summary' || BigInt(next.revision) > BigInt(current.revision);
  }
  if (next.kind === 'summary') return next.sessionID === current.sessionID;
  return next.sessionID !== current.sessionID;
}
