import { describe, expect, it } from 'vitest';
import { normalizeRanking } from './api';

const row = (rank = '1', amount = '9007199254740993.123', me = false) => ({
  rank,
  amount,
  is_me: me,
  identity: { kind: 'anonymous' },
});
const board = () => ({
  as_of: 2000000000,
  statistics_start: 1900000000,
  window: '7d',
  rows: [row()],
  me: null,
});

describe('exact rankings', () => {
  it('preserves wide decimals and rejects anonymous identity leaks', () => {
    expect(normalizeRanking(board(), 'bidding', '7d').rows[0].amount).toBe('9007199254740993.123');
    for (const identity of [
      { kind: 'anonymous', display_name: 'private' },
      { kind: 'anonymous', avatar_url: 'https://example.test/private.png' },
      { kind: 'anonymous', user_id: '7' },
    ])
      expect(() =>
        normalizeRanking({ ...board(), rows: [{ ...row(), identity }] }, 'bidding', '7d'),
      ).toThrow();
  });
  it('rejects unsafe numeric amounts, stale windows and impossible own rows', () => {
    for (const amount of [1, '0', '-1', '1.000', '1e6'])
      expect(() =>
        normalizeRanking({ ...board(), rows: [{ ...row(), amount }] }, 'bidding', '7d'),
      ).toThrow();
    expect(() => normalizeRanking(board(), 'bidding', '30d')).toThrow();
    expect(() =>
      normalizeRanking({ ...board(), me: row('21', '1', true) }, 'bidding', '7d'),
    ).toThrow();
    expect(() =>
      normalizeRanking({ ...board(), rows: [row('1'), row('3')] }, 'bidding', '7d'),
    ).toThrow();
  });
  it('accepts twenty plus me and exact charity page offsets', () => {
    const rows = Array.from({ length: 20 }, (_, i) => row(String(i + 1)));
    expect(
      normalizeRanking({ ...board(), rows, me: row('21', '1', true) }, 'blackjack', '7d').me?.rank,
    ).toBe('21');
    const wire = {
      ...board(),
      window: 'history',
      rows: [row('21', '1', true)],
      pagination: { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
    };
    expect(normalizeRanking(wire, 'charity', 'history', '2').rows[0].isMe).toBe(true);
    expect(() =>
      normalizeRanking({ ...wire, me: row('22', '1', true) }, 'charity', 'history', '2'),
    ).toThrow();
  });
});
