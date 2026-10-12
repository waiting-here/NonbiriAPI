import { LINKLINK_SPECS, type LinkLinkSpec } from '../common/types';
import {
  booleanValue,
  creditsValue,
  decimalValue,
  enumValue,
  exactRecord,
  gamePayment,
  invalidResponse,
  opaqueID,
  safeInteger,
  unixTime,
  publicIdentity,
} from '../common/strict';
import type {
  LinkLinkCurrent,
  LinkLinkState,
  LinkLinkSummary,
  LinkLinkTile,
  LinkLinkMatchIntent,
  LinkLinkMatchResult,
  LinkLinkCoordinate,
  LinkLinkHintIntent,
  LinkLinkHintResult,
  LinkLinkLeaderboard,
  LinkLinkRank,
} from './types';

const DIMENSIONS: Readonly<Record<LinkLinkSpec, readonly [number, number]>> = {
  '6x8': [6, 8],
  '8x8': [8, 8],
  '10x10': [10, 10],
};
function funding(record: Record<string, unknown>, price: string) {
  const rulesVersion =
    record.rules_version === undefined
      ? 1
      : safeInteger(record.rules_version, 1, 2, 'LinkLink rules version');
  if (rulesVersion === 2 && record.payment === undefined) invalidResponse('LinkLink payment');
  return {
    rulesVersion,
    payment:
      record.payment === undefined
        ? { general: price, game: '0' }
        : gamePayment(record.payment, 'LinkLink payment'),
  };
}

function opportunities(record: Record<string, unknown>, rulesVersion: number) {
  if (
    rulesVersion === 1 &&
    record.opportunities_initial === undefined &&
    record.opportunities_remaining === undefined
  )
    return { opportunitiesInitial: 0, opportunitiesRemaining: 0 };
  return {
    opportunitiesInitial: safeInteger(
      record.opportunities_initial,
      0,
      100,
      'LinkLink initial opportunities',
    ),
    opportunitiesRemaining: safeInteger(
      record.opportunities_remaining,
      0,
      100,
      'LinkLink remaining opportunities',
    ),
  };
}

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
    ['rules_version', 'payment', 'opportunities_initial', 'opportunities_remaining'],
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
    if (keyNumber < 1 || keyNumber > 25) invalidResponse(`LinkLink tile ${index} roster`);
    const removed = booleanValue(tile.removed, `LinkLink tile ${index} removed`);
    return { row, col, tileKey: tile.tile_key, removed };
  });
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
  const startedAt = unixTime(record.started_at, 'LinkLink start time');
  const deadline = unixTime(record.deadline, 'LinkLink deadline');
  const serverNow = unixTime(record.server_now, 'LinkLink server time');
  const price = creditsValue(record.price, { positive: true }, 'LinkLink price');
  const payment = funding(record, price);
  return {
    ...payment,
    ...opportunities(record, payment.rulesVersion),
    kind: 'active',
    sessionID: opaqueID(record.session_id, 'll_', 'LinkLink session id'),
    spec,
    price,
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
    ['rules_version', 'payment', 'opportunities_initial', 'opportunities_remaining'],
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
  const score =
    record.score === null ? null : decimalValue(record.score, { bits: 256 }, 'LinkLink score');
  const price = creditsValue(record.price, { positive: true }, 'LinkLink summary price');
  const payment = funding(record, price);
  const assistance = opportunities(record, payment.rulesVersion);
  return {
    ...payment,
    ...assistance,
    kind: 'summary',
    sessionID: opaqueID(record.session_id, 'll_', 'LinkLink summary id'),
    spec,
    price,
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
      'rules_version',
      'payment',
      'opportunities_initial',
      'opportunities_remaining',
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

export function normalizeLinkLinkMatch(
  value: unknown,
  intent: LinkLinkMatchIntent,
): LinkLinkMatchResult {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    invalidResponse('LinkLink match');
  const { match_path: rawPath, ...body } = value as Record<string, unknown>;
  const result = Object.prototype.hasOwnProperty.call(body, 'terminal_reason')
    ? normalizeLinkLinkSummary(body)
    : normalizeLinkLinkState(body);
  if (result.sessionID !== intent.sessionID) invalidResponse('LinkLink match session');
  if (rawPath === undefined) return { result, path: null };
  return { result, path: normalizePath(rawPath, result.spec) };
}

function normalizePath(rawPath: unknown, spec: LinkLinkSpec): readonly LinkLinkCoordinate[] {
  if (!Array.isArray(rawPath) || rawPath.length < 2 || rawPath.length > 4)
    invalidResponse('LinkLink match path');
  const [rows, cols] = DIMENSIONS[spec];
  const path = rawPath.map((value) => {
    const point = exactRecord(value, ['row', 'col'], [], 'LinkLink path point');
    return {
      row: safeInteger(point.row, -1, rows, 'LinkLink path row'),
      col: safeInteger(point.col, -1, cols, 'LinkLink path column'),
    };
  });
  return path;
}

export function boardWasRearranged(before: LinkLinkState, after: LinkLinkState): boolean {
  const nextTiles = new Map(after.board.tiles.map((tile) => [`${tile.row}:${tile.col}`, tile]));
  return before.board.tiles.some((tile) => {
    if (tile.removed) return false;
    const next = nextTiles.get(`${tile.row}:${tile.col}`);
    return !!next && !next.removed && next.tileKey !== tile.tileKey;
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

export function normalizeLinkLinkHint(
  value: unknown,
  intent: LinkLinkHintIntent,
): LinkLinkHintResult {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalidResponse('LinkLink hint');
  const { hint: rawHint, reshuffled: rawReshuffled, ...body } = value as Record<string, unknown>;
  const result = normalizeLinkLinkState(body);
  const reshuffled = booleanValue(rawReshuffled, 'LinkLink refreshed');
  if (
    result.rulesVersion !== 2 ||
    result.sessionID !== intent.sessionID ||
    BigInt(result.revision) !== BigInt(intent.expectedRevision) + 1n
  )
    invalidResponse('LinkLink hint state');
  if (rawHint === null) {
    if (!reshuffled) invalidResponse('LinkLink missing hint');
    return { result, hint: null, reshuffled };
  }
  if (reshuffled) invalidResponse('LinkLink mixed hint and refresh');
  const hint = exactRecord(rawHint, ['first', 'second', 'path'], [], 'LinkLink hint pair');
  const point = (value: unknown): LinkLinkCoordinate => {
    const record = exactRecord(value, ['row', 'col'], [], 'LinkLink hint coordinate');
    return {
      row: safeInteger(record.row, 0, result.board.rows - 1, 'LinkLink hint row'),
      col: safeInteger(record.col, 0, result.board.cols - 1, 'LinkLink hint column'),
    };
  };
  const first = point(hint.first),
    second = point(hint.second);
  return {
    result,
    reshuffled,
    hint: { first, second, path: normalizePath(hint.path, result.spec) },
  };
}

export function normalizeLinkLinkLeaderboard(
  value: unknown,
  spec: LinkLinkSpec,
  windowDays: 7 | 30,
): LinkLinkLeaderboard {
  const record = exactRecord(
    value,
    ['spec', 'window_days', 'window_start', 'as_of', 'rules_version', 'rows', 'me'],
    [],
    'LinkLink leaderboard',
  );
  if (record.spec !== spec || record.window_days !== windowDays || record.rules_version !== 2)
    invalidResponse('LinkLink leaderboard selection');
  const asOf = unixTime(record.as_of, 'LinkLink leaderboard time');
  const windowStart = unixTime(record.window_start, 'LinkLink leaderboard window');
  if (!Array.isArray(record.rows) || record.rows.length > 20)
    invalidResponse('LinkLink leaderboard window or rows');
  const row = (value: unknown): LinkLinkRank => {
    const r = exactRecord(
      value,
      ['rank', 'score', 'achieved_at', 'identity', 'is_me'],
      [],
      'LinkLink rank',
    );
    const achievedAt = unixTime(r.achieved_at, 'LinkLink achievement');
    const score = decimalValue(r.score, { bits: 128, positive: true }, 'LinkLink rank score');
    return {
      rank: decimalValue(r.rank, { bits: 128, positive: true }, 'LinkLink rank'),
      score,
      achievedAt,
      identity: publicIdentity(r.identity, 'LinkLink rank identity'),
      isMe: booleanValue(r.is_me, 'LinkLink own rank'),
    };
  };
  const rows = record.rows.map(row);
  const me = record.me === null ? null : row(record.me);
  if (
    rows.some((r, index) => r.rank !== String(index + 1)) ||
    rows.filter((r) => r.isMe).length > 1 ||
    (me && (!me.isMe || BigInt(me.rank) <= 20n || rows.some((r) => r.isMe)))
  )
    invalidResponse('LinkLink rank positions');
  return { spec, windowDays, windowStart, asOf, rows, me };
}
