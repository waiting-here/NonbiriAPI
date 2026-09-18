import { describe, expect, it, vi } from 'vitest';
import recent from './testdata/recent.json';
import anonymous from './testdata/anonymous.json';
import {
  normalizeExport,
  normalizeMatch,
  normalizeRound,
  type ExportPage,
  type Match,
} from './history';
import { HistoryExport } from './export';
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
      normalizeExport({ ...recent, next_cursor: 'x'.repeat(4097) }, 'likes', 'recent'),
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
describe('bounded NDJSON downloads', () => {
  it('preserves pages on failure, retries the same cursor, and completes only at the end', async () => {
    const p = normalizeExport(recent, 'likes', 'recent');
    const first: ExportPage = { ...p, items: [p.items[0]], next_cursor: 'page-2' },
      second: ExportPage = { ...p, items: [p.items[1]], expired_skipped: 2 };
    const read = vi
      .fn()
      .mockResolvedValueOnce(first)
      .mockRejectedValueOnce(new Error('connection lost'))
      .mockResolvedValueOnce(second);
    const write = vi.fn(),
      job = new HistoryExport('likes', 'recent', { mode: 'quick' }, read),
      signal = new AbortController().signal;
    await job.step(signal, write);
    expect(job.progress).toEqual({ parts: 1, matches: 1, rounds: 0, expired: 0, complete: false });
    await expect(job.step(signal, write)).rejects.toThrow('connection lost');
    expect(job.progress.parts).toBe(1);
    await job.step(signal, write);
    expect(read.mock.calls.map((c) => c[3])).toEqual([null, 'page-2', 'page-2']);
    expect(job.progress).toEqual({ parts: 2, matches: 1, rounds: 1, expired: 2, complete: true });
    for (const [bytes, name] of write.mock.calls) {
      expect(bytes.length).toBeLessThanOrEqual(16 * 1024 * 1024);
      const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
      expect(text.endsWith('\n')).toBe(true);
      expect(text).not.toContain('\r');
      expect(JSON.parse(text)).toHaveProperty('kind');
      expect(name).toMatch(/^duel-history-likes-recent-part-000[12]\.ndjson$/);
    }
  });
  it('cancels before publishing a page and keeps the cursor after a failed download', async () => {
    const page = normalizeExport(recent, 'likes', 'recent'),
      controller = new AbortController();
    const read = vi
      .fn()
      .mockImplementationOnce(async () => {
        controller.abort();
        return page;
      })
      .mockResolvedValue(page);
    const write = vi.fn(),
      job = new HistoryExport('likes', 'recent', {}, read);
    await expect(job.step(controller.signal, write)).rejects.toThrow();
    expect(write).not.toHaveBeenCalled();
    await expect(
      job.step(new AbortController().signal, () => {
        throw new Error('download failed');
      }),
    ).rejects.toThrow('download failed');
    expect(job.progress.parts).toBe(0);
    await job.step(new AbortController().signal, write);
    expect(read.mock.calls.map((c) => c[3])).toEqual([null, null, null]);
    expect(job.progress.complete).toBe(true);
  });
  it('refuses overlapping page requests and orphan rounds', async () => {
    const page = normalizeExport(recent, 'likes', 'recent');
    let release!: (page: ExportPage) => void;
    const read = vi.fn().mockImplementation(
      () =>
        new Promise<ExportPage>((resolve) => {
          release = resolve;
        }),
    );
    const job = new HistoryExport('likes', 'recent', {}, read),
      write = vi.fn(),
      signal = new AbortController().signal;
    const active = job.step(signal, write);
    await expect(job.step(signal, write)).rejects.toThrow('already running');
    release({ ...page, items: [page.items[1]] });
    await expect(active).rejects.toThrow('preceding record');
    expect(write).not.toHaveBeenCalled();
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
