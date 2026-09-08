import { describe, expect, it, vi } from 'vitest';
import {
  getLegalHoldPage,
  normalizeLegalHoldPageResponse,
  type LegalHoldKindFilter,
  type LegalHoldStateFilter,
} from './legalHoldPages';

const holdID = `lgh_${'A'.repeat(22)}`;
const reportID = `rpc_${'B'.repeat(21)}A`;

function hold(overrides: Record<string, unknown> = {}) {
  return {
    id: holdID,
    object_kind: 'report_case',
    object_ref: reportID,
    state: 'active',
    revision: '1',
    created_at: 1_800_000_000,
    expires_at: 1_800_086_400,
    ended_at: null,
    ...overrides,
  };
}

function page(data: unknown[], overrides: Record<string, unknown> = {}) {
  return {
    data,
    next_cursor: null,
    pagination: {
      page: '1',
      page_size: 20,
      total_items: String(data.length),
      total_pages: '1',
      ...overrides,
    },
  };
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    headers: { 'content-type': 'application/json' },
  });
}

describe('legal hold page adapter', () => {
  it('sends the page wire without the legacy cursor parameters and forwards AbortSignal', async () => {
    const fetchMock = vi.fn(async (_input: string | URL | Request, _init?: RequestInit) => {
      void _input;
      void _init;
      return jsonResponse(page([hold()], { page_size: 50 }));
    });
    vi.stubGlobal('fetch', fetchMock);
    const signal = new AbortController().signal;

    await getLegalHoldPage('active', 'report_case', '1', 50, signal);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [input, init] = fetchMock.mock.calls[0] ?? [];
    expect(input).toBe(
      '/admin/api/legal-holds?state=active&object_kind=report_case&page=1&page_size=50',
    );
    expect((init as RequestInit | undefined)?.signal).toBe(signal);
  });

  it('rejects rows outside the selected filter and malformed page windows', () => {
    const state: LegalHoldStateFilter = 'active';
    const kind: LegalHoldKindFilter = 'report_case';
    expect(() =>
      normalizeLegalHoldPageResponse(
        page([hold({ state: 'released', ended_at: 1_800_000_100 })]),
        state,
        kind,
        '1',
        20,
      ),
    ).toThrow(/state filter/i);
    expect(() =>
      normalizeLegalHoldPageResponse(
        page([hold()], { page_size: 20, total_items: '21', total_pages: '2' }),
        state,
        kind,
        '1',
        20,
      ),
    ).toThrow(/pagination request window/i);
  });
});
