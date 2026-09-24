import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  HISTORY_KINDS,
  MAX_HISTORY_PAGE,
  loadHistory,
  normalizeHistory,
  normalizeHistoryFilter,
} from './data';

const entry = {
  asset_type: 'general',
  operation_id: `op_${'A'.repeat(22)}`,
  line: 1,
  kind: 'charity_settle',
  delta: '0.007',
  created_at: 1_800_000_000,
  request_id: `req_${'B'.repeat(21)}A`,
};
const page = {
  data: [entry],
  page: '1',
  page_size: 20,
  total: '1',
  total_pages: '1',
  anchor: entry.operation_id,
  game_balance: '0',
  current_balance: '9000000000000.007',
  server_now: 1_800_000_001,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('owner credit history projection', () => {
  it('preserves wide signed decimal values without coercion', () => {
    expect(normalizeHistory(page).current_balance).toBe('9000000000000.007');
    expect(normalizeHistory({ ...page, data: [{ ...entry, delta: '-0.007' }] }).data[0].delta).toBe(
      '-0.007',
    );
  });

  it('decodes exchange and inactivity debits in the General view', async () => {
    const data = [
      { ...entry, line: 1, kind: 'activity_exchange', delta: '-2000', request_id: null },
      { ...entry, line: 2, kind: 'inactivity_decay', delta: '-0.125', request_id: null },
    ];
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify({ ...page, data, total: '2' }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);
    expect((await loadHistory({ page: '1', page_size: 20 })).data).toEqual(data);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/credits/history?page=1&page_size=20');
  });

  it('keeps activity units as exact integers and rejects fractional or unknown assets', () => {
    const data = [
      {
        ...entry,
        line: 1,
        asset_type: 'sketch_paper',
        kind: 'activity_exchange',
        delta: '9007199254740993',
        request_id: null,
      },
      {
        ...entry,
        line: 2,
        asset_type: 'sketch_brush',
        kind: 'image_reserve',
        delta: '-3',
        request_id: null,
      },
      {
        ...entry,
        line: 3,
        asset_type: 'sketch_brush',
        kind: 'image_refund',
        delta: '3',
        request_id: null,
      },
    ];
    expect(normalizeHistory({ ...page, data, total: '3' }).data).toEqual(data);
    for (const asset_type of ['sketch_paper', 'sketch_brush']) {
      for (const delta of ['0.001', '-0.5'])
        expect(() =>
          normalizeHistory({ ...page, data: [{ ...data[0], asset_type, delta }] }),
        ).toThrow(/activity units/i);
    }
    for (const invalid of [
      { ...data[0], asset_type: 'unknown_asset' },
      { ...data[0], kind: 'unknown_operation' },
      { ...data[0], request_id: entry.request_id },
    ])
      expect(() => normalizeHistory({ ...page, data: [invalid] })).toThrow();
  });

  it('accepts activity and inactivity filters without broadening unknown values', () => {
    for (const asset_type of ['sketch_paper', 'sketch_brush', 'all'] as const) {
      const filter = { asset_type, page: '1', page_size: 20 as const, category: 'picture_book' };
      expect(normalizeHistoryFilter(filter)).toEqual(filter);
    }
    expect(
      normalizeHistoryFilter({ page: '1', page_size: 20, category: 'inactivity' }).category,
    ).toBe('inactivity');
    expect(() =>
      normalizeHistoryFilter({ page: '1', page_size: 20, category: 'unknown_category' }),
    ).toThrow(/filter/i);
  });

  it.each(HISTORY_KINDS)('accepts the %s reason without leaking a source', (kind) => {
    expect(
      normalizeHistory({ ...page, data: [{ ...entry, kind, request_id: null }] }).data[0].kind,
    ).toBe(kind);
  });

  it('rejects a request attached to donor rewards or unrelated entries', () => {
    for (const kind of [
      'donor_reward',
      'admin_user_adjustment',
      'fishing_settle',
      'activity_exchange',
      'image_reserve',
      'image_refund',
      'inactivity_decay',
    ]) {
      expect(() => normalizeHistory({ ...page, data: [{ ...entry, kind }] })).toThrow(
        /association/i,
      );
    }
  });

  it('accepts an owner-scoped request link for a short-request penalty', () => {
    expect(
      normalizeHistory({
        ...page,
        data: [{ ...entry, kind: 'anti_abuse_penalty', delta: '-0.007' }],
      }).data[0].request_id,
    ).toBe(entry.request_id);
  });

  it('rejects unsafe extra fields, duplicate rows and inconsistent pagination', () => {
    for (const invalid of [
      { ...page, data: [{ ...entry, source_id: 'private-claim' }] },
      { ...page, data: [{ ...entry, reason: 'private-admin-note' }] },
      { ...page, data: [{ ...entry, delta: '0' }] },
      { ...page, data: [{ ...entry, delta: 0.007 }] },
      { ...page, data: [{ ...entry, request_id: 'req_wrong' }] },
      { ...page, data: [entry, entry], total: '2' },
      { ...page, page: '2' },
      { ...page, total_pages: '3' },
      { ...page, page_size: 21 },
      { ...page, total: '20' },
      { ...page, anchor: null },
    ])
      expect(() => normalizeHistory(invalid)).toThrow();
  });

  it('accepts an empty filter result with or without a browsing anchor', () => {
    for (const anchor of [null, entry.operation_id])
      expect(normalizeHistory({ ...page, data: [], total: '0', anchor }).data).toEqual([]);
  });

  it('accepts the new ten-row page size and the full positive int64 page range', () => {
    expect(normalizeHistory({ ...page, page_size: 10 }).page_size).toBe(10);
    expect(normalizeHistoryFilter({ page: MAX_HISTORY_PAGE.toString(), page_size: 10 })).toEqual({
      page: MAX_HISTORY_PAGE.toString(),
      page_size: 10,
    });
  });

  it('sends the original filter and signal, then accepts a server clamp', async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn((_input: string | URL | Request, init?: RequestInit) => {
      expect(init?.signal).toBe(controller.signal);
      return Promise.resolve(
        new Response(JSON.stringify({ ...page, page: '1', page_size: 10 }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      );
    });
    vi.stubGlobal('fetch', fetchMock);

    const result = await loadHistory(
      {
        page: MAX_HISTORY_PAGE.toString(),
        page_size: 10,
        anchor: entry.operation_id,
        from: 0,
        to: 253_402_300_799,
        category: 'charity',
        direction: 'income',
      },
      controller.signal,
    );

    expect(result.page).toBe('1');
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      `/api/credits/history?page=${MAX_HISTORY_PAGE}&page_size=10&anchor=${entry.operation_id}&from=0&to=253402300799&category=charity&direction=income`,
    );
  });

  it('rejects invalid requests before fetch and rejects an unexpected clamp', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    expect(() => loadHistory({ page: '0', page_size: 20 })).toThrow(/filter/i);
    expect(fetchMock).not.toHaveBeenCalled();

    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ ...page, page: '1', total: '20', total_pages: '2' }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    await expect(loadHistory({ page: '2', page_size: 20 })).rejects.toMatchObject({
      code: 'invalid_response',
    });
  });

  it('rejects a changed browsing anchor even when the page window is valid', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify(page), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    );
    await expect(
      loadHistory({ page: '1', page_size: 20, anchor: `op_${'B'.repeat(21)}A` }),
    ).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
