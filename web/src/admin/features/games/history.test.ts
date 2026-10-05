import { describe, expect, it } from 'vitest';
import recent from './testdata/recent.json';
import anonymous from './testdata/anonymous.json';
import { normalizeExport, normalizeMatch, normalizeRound, type Match } from './history';
import { historyDownloadURL } from './export';
import { normalizeDuelConfig, percentBP, validateDuelConfigurations } from './config';

describe('admin match records', () => {
  it('decodes real server records and refuses identities in anonymous data', () => {
    const p = normalizeExport(recent, 'likes', 'recent'),
      a = normalizeExport(anonymous, 'likes', 'anonymous');
    expect(p.items).toHaveLength(2);
    expect(a.items).toHaveLength(2);
    expect((p.items[0] as Match).recent?.participants).toHaveLength(2);
    expect(a.items[0]).not.toHaveProperty('recent');
    expect(() => normalizeExport(recent, 'likes', 'anonymous')).toThrow();
    const polluted = structuredClone(anonymous);
    Object.assign(polluted.items[0].facts!.initial, { user_id: '17' });
    expect(() => normalizeExport(polluted, 'likes', 'anonymous')).toThrow();
    expect(() => normalizeMatch({ ...recent.items[0], extra: true }, 'likes', 'recent')).toThrow();
    expect(() => normalizeRound({ ...recent.items[1].record, round: 76 }, 'recent')).toThrow();
  });
  it('rejects oversized pages, misplaced round references, and malformed cursors', () => {
    expect(() =>
      normalizeExport({ ...recent, items: Array(101).fill(recent.items[0]) }, 'likes', 'recent'),
    ).toThrow();
    expect(() =>
      normalizeExport(
        { ...recent, items: [{ ...recent.items[1], round_no: 2 }] },
        'likes',
        'recent',
      ),
    ).toThrow();
  });
});
describe('game configuration percentages', () => {
  it('uses exact basis points and validates the complete enabled hierarchy', () => {
    expect(percentBP('1.25')).toBe(125);
    expect(percentBP('99.99')).toBe(9999);
    expect(percentBP('0')).toBe(0);
    expect(percentBP('1.001')).toBeNaN();
    expect(percentBP('100')).toBeNaN();
    const m = {
      enabled: true,
      ticket: '5',
      rake_bp: { platform: 125, welfare: 100, thursday: 50 },
    };
    const likes = normalizeDuelConfig({ enabled: true, modes: { quick: m, standard: m } }, 'likes');
    const t = (_zh: string, en: string) => en;
    expect(validateDuelConfigurations({ master_enabled: true, likes }, t)).toBeNull();
    expect(validateDuelConfigurations({ master_enabled: false, likes }, t)).toContain('master');
    likes.modes.quick.rake_bp.platform = 9850;
    expect(validateDuelConfigurations({ master_enabled: true, likes }, t)).toContain('below 100%');
    expect(() => normalizeDuelConfig({ ...likes, ignored: 1 }, 'likes')).toThrow();
  });
});

it('downloads one archive with the selected dates and filters', () => {
  const url = new URL(
    historyDownloadURL('bidding', 'anonymous', { from: 100, to: 200, mode: 'ai' }),
    'https://admin.example.test',
  );
  expect(url.pathname).toBe('/admin/api/games/bidding/history/download');
  expect(Object.fromEntries(url.searchParams)).toEqual({
    dataset: 'anonymous',
    from: '100',
    to: '200',
    mode: 'ai',
  });
});
