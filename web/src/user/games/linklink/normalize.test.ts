import { describe, expect, it } from 'vitest';
import {
  boardWasRearranged,
  normalizeLinkLinkCurrent,
  normalizeLinkLinkMatch,
  normalizeLinkLinkState,
  normalizeLinkLinkSummary,
  normalizeLinkLinkHint,
  normalizeLinkLinkLeaderboard,
  shouldApplyLinkLinkReplacement,
} from './normalize';

const id = 'll_AAAAAAAAAAAAAAAAAAAAAA';
const definitions = {
  '6x8': [6, 8, 150],
  '8x8': [8, 8, 180],
  '10x10': [10, 10, 240],
} as const;
function board(rows = 6, cols = 8) {
  return {
    rows,
    cols,
    tiles: Array.from({ length: rows * cols }, (_, index) => ({
      row: Math.floor(index / cols),
      col: index % cols,
      tile_key: `tile_${String(Math.floor(index / 4) + 1).padStart(2, '0')}`,
      removed: false,
    })),
  };
}
function active(revision = '1', spec: keyof typeof definitions = '6x8') {
  const [rows, cols, seconds] = definitions[spec];
  return {
    session_id: id,
    spec,
    price: '3',
    state: 'active',
    revision,
    board: board(rows, cols),
    pairs_removed: 0,
    total_pairs: (rows * cols) / 2,
    started_at: 1_800_000_000,
    deadline: 1_800_000_000 + seconds,
    server_now: 1_800_000_010,
  };
}

describe('LinkLink authoritative projection', () => {
  it('normalizes exactly null, active state, and terminal summary', () => {
    expect(normalizeLinkLinkCurrent(null)).toBeNull();
    expect(normalizeLinkLinkCurrent(active())?.kind).toBe('active');
    expect(
      normalizeLinkLinkSummary({
        session_id: id,
        spec: '6x8',
        price: '3',
        terminal_reason: 'completed',
        started_at: 1_800_000_000,
        deadline: 1_800_000_150,
        terminal_at: 1_800_000_140,
        pairs_removed: 24,
        total_pairs: 24,
        score: '2410',
      }).terminalReason,
    ).toBe('completed');
    expect(
      normalizeLinkLinkSummary({
        session_id: id,
        spec: '6x8',
        price: '3',
        terminal_reason: 'abandoned',
        started_at: 1_800_000_000,
        deadline: 1_800_000_150,
        terminal_at: 1_800_000_020,
        pairs_removed: 0,
        total_pairs: 24,
        score: null,
      }).score,
    ).toBeNull();
  });

  it('enforces frozen duration and authoritative score arithmetic', () => {
    const wrongDuration = active();
    wrongDuration.deadline += 1;
    expect(() => normalizeLinkLinkState(wrongDuration)).toThrow(/time range/i);

    const timedOut = {
      session_id: id,
      spec: '6x8',
      price: '3',
      terminal_reason: 'timed_out',
      started_at: 1_800_000_000,
      deadline: 1_800_000_150,
      terminal_at: 1_800_000_151,
      pairs_removed: 7,
      total_pairs: 24,
      score: '700',
    };
    expect(normalizeLinkLinkSummary(timedOut).score).toBe('700');
    timedOut.score = '701';
    expect(() => normalizeLinkLinkSummary(timedOut)).toThrow(/score arithmetic/i);
  });

  it('rejects duplicate cells, malformed multiplicity, and uncontracted local path data', () => {
    const duplicate = active();
    duplicate.board.tiles[1].row = 0;
    duplicate.board.tiles[1].col = 0;
    expect(() => normalizeLinkLinkState(duplicate)).toThrow(/duplicate coordinate/i);
    const path = { ...active(), path: [{ row: 0, col: 0 }] };
    expect(() => normalizeLinkLinkState(path)).toThrow(/LinkLink state/i);
    const key = active();
    key.board.tiles[4].tile_key = 'tile_01';
    expect(() => normalizeLinkLinkState(key)).toThrow(/multiplicity/i);
  });

  it('accepts every canonical tile roster and rejects zero or out-of-range keys', () => {
    for (const spec of Object.keys(definitions) as (keyof typeof definitions)[]) {
      const wire = active('1', spec);
      const parsed = normalizeLinkLinkState(wire);
      const tileTypes = (wire.board.rows * wire.board.cols) / 4;
      expect(parsed.board.tiles.at(-1)?.tileKey).toBe(`tile_${String(tileTypes).padStart(2, '0')}`);

      const zero = active('1', spec);
      zero.board.tiles[0].tile_key = 'tile_00';
      expect(() => normalizeLinkLinkState(zero)).toThrow(/roster/i);

      const beyond = active('1', spec);
      beyond.board.tiles[0].tile_key = `tile_${String(tileTypes + 1).padStart(2, '0')}`;
      expect(() => normalizeLinkLinkState(beyond)).toThrow(/roster/i);
    }
  });

  it('detects only server-returned rearrangements across deterministic board permutations', () => {
    const before = normalizeLinkLinkState(active());
    for (let sample = 0; sample < 24; sample += 1) {
      const wire = active(String(sample + 2));
      wire.board.tiles[0].removed = true;
      wire.board.tiles[1].removed = true;
      wire.pairs_removed = 1;
      const left = 4 + (sample % 4);
      const right = 8 + (sample % 4);
      [wire.board.tiles[left].tile_key, wire.board.tiles[right].tile_key] = [
        wire.board.tiles[right].tile_key,
        wire.board.tiles[left].tile_key,
      ];
      expect(boardWasRearranged(before, normalizeLinkLinkState(wire))).toBe(true);
    }
    const removal = active('2');
    removal.board.tiles[0].removed = true;
    removal.board.tiles[1].removed = true;
    removal.pairs_removed = 1;
    expect(boardWasRearranged(before, normalizeLinkLinkState(removal))).toBe(false);
  });

  it('rejects late mutation or GET projections that would regress the current session', () => {
    const current = normalizeLinkLinkState(active('3'));
    expect(shouldApplyLinkLinkReplacement(current, normalizeLinkLinkState(active('2')))).toBe(
      false,
    );
    expect(shouldApplyLinkLinkReplacement(current, normalizeLinkLinkState(active('4')))).toBe(true);
    const summary = normalizeLinkLinkSummary({
      session_id: id,
      spec: '6x8',
      price: '3',
      terminal_reason: 'completed',
      started_at: 1_800_000_000,
      deadline: 1_800_000_150,
      terminal_at: 1_800_000_140,
      pairs_removed: 24,
      total_pairs: 24,
      score: '2410',
    });
    expect(shouldApplyLinkLinkReplacement(current, summary)).toBe(true);
    expect(shouldApplyLinkLinkReplacement(summary, normalizeLinkLinkState(active('99')))).toBe(
      false,
    );
  });
});

describe('server-confirmed match paths', () => {
  const intent = {
    sessionID: id,
    expectedRevision: '1',
    first: { row: 0, col: 0 },
    second: { row: 0, col: 1 },
    idempotencyKey: 'request-key',
  };
  it('accepts a direct or perimeter path without adding it to current state', () => {
    const path = [intent.first, { row: -1, col: 0 }, { row: -1, col: 1 }, intent.second];
    expect(normalizeLinkLinkMatch({ ...active(), match_path: path }, intent).path).toEqual(path);
    expect(normalizeLinkLinkMatch(active(), intent).path).toBeNull();
    expect(() => normalizeLinkLinkCurrent({ ...active(), match_path: path })).toThrow();
  });
  it.each([
    null,
    [],
    [{ row: 0, col: 0 }],
    [
      { row: 0, col: 0 },
      { row: 0, col: 2 },
    ],
    [
      { row: 0, col: 0 },
      { row: -2, col: 0 },
      { row: -2, col: 1 },
      { row: 0, col: 1 },
    ],
    [
      { row: 0, col: 0 },
      { row: 1, col: 1 },
      { row: 0, col: 1 },
    ],
    [
      { row: 0, col: 0 },
      { row: 0, col: 0 },
      { row: 0, col: 1 },
    ],
    [
      { row: 0, col: 0 },
      { row: -0.5, col: 0 },
      { row: -0.5, col: 1 },
      { row: 0, col: 1 },
    ],
    [
      { row: 0, col: 0, command: 'x' },
      { row: 0, col: 1 },
    ],
  ])('rejects malformed geometry %j', (path) => {
    expect(() => normalizeLinkLinkMatch({ ...active(), match_path: path }, intent)).toThrow();
  });
});

describe('hint and score projections', () => {
  const intent = { sessionID: id, expectedRevision: '1', idempotencyKey: 'hint-key' };
  function current() {
    return { ...active('2'), rules_version: 2, payment: { general: '1', game: '2' }, opportunities_initial: 2, opportunities_remaining: 1 };
  }
  const hint = { first: { row: 0, col: 0 }, second: { row: 0, col: 1 }, path: [{ row: 0, col: 0 }, { row: 0, col: 1 }] };
  it('keeps live opportunities and transient hint separate, including zero', () => {
    const result = normalizeLinkLinkHint({ ...current(), hint, reshuffled: false }, intent);
    expect(result.hint).toEqual(hint);
    expect(result.result.opportunitiesRemaining).toBe(1);
    expect(normalizeLinkLinkHint({ ...current(), opportunities_remaining: 0, hint: null, reshuffled: true }, intent).reshuffled).toBe(true);
    expect(() => normalizeLinkLinkState({ ...current(), hint })).toThrow();
    expect(() => normalizeLinkLinkState({ ...current(), opportunities_remaining: undefined })).toThrow();
    expect(() => normalizeLinkLinkState({ ...current(), opportunities_initial: 3 })).toThrow();
    expect(() => normalizeLinkLinkHint({ ...current(), hint: null, reshuffled: false }, intent)).toThrow();
    expect(() => normalizeLinkLinkHint({ ...current(), revision: '3', hint, reshuffled: false }, intent)).toThrow();
    expect(() => normalizeLinkLinkHint({ ...current(), hint, reshuffled: true }, intent)).toThrow();
    expect(normalizeLinkLinkState(active()).opportunitiesInitial).toBe(0);
  });
  it('adds unused opportunities only to completed v2 scores', () => {
    const summary = { session_id: id, spec: '6x8', price: '3', rules_version: 2, payment: { general: '3', game: '0' },
      opportunities_initial: 2, opportunities_remaining: 1, terminal_reason: 'completed',
      started_at: 1_800_000_000, deadline: 1_800_000_150, terminal_at: 1_800_000_140, pairs_removed: 24, total_pairs: 24, score: '2510' };
    expect(normalizeLinkLinkSummary(summary).score).toBe('2510');
    expect(() => normalizeLinkLinkSummary({ ...summary, score: '2410' })).toThrow();
    expect(normalizeLinkLinkSummary({ ...summary, terminal_reason: 'timed_out', terminal_at: 1_800_000_151, pairs_removed: 10, score: '1000' }).score).toBe('1000');
  });
  it('rejects invalid leaderboard windows, identity leakage and mixed selections', () => {
    const wire = { spec: '6x8', window_days: 7, window_start: 1_800_000_000 - 7 * 86400, as_of: 1_800_000_000, rules_version: 2,
      rows: [{ rank: '1', score: '2500', achieved_at: 1_800_000_000 - 1, identity: { kind: 'anonymous' }, is_me: false }], me: null };
    expect(normalizeLinkLinkLeaderboard(wire, '6x8', 7).rows).toHaveLength(1);
    expect(() => normalizeLinkLinkLeaderboard(wire, '8x8', 7)).toThrow();
    expect(() => normalizeLinkLinkLeaderboard({ ...wire, window_start: wire.window_start + 1 }, '6x8', 7)).toThrow();
    expect(() => normalizeLinkLinkLeaderboard({ ...wire, rows: [{ ...wire.rows[0], achieved_at: wire.window_start }] }, '6x8', 7)).toThrow();
    expect(() => normalizeLinkLinkLeaderboard({ ...wire, rows: [{ ...wire.rows[0], identity: { kind: 'anonymous', display_name: 'private' } }] }, '6x8', 7)).toThrow();
    expect(() => normalizeLinkLinkLeaderboard({ ...wire, me: { ...wire.rows[0], is_me: true } }, '6x8', 7)).toThrow();
  });
});
