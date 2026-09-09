import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getReportDetailPage,
  getReportPage,
  getReportTargetDonationsPage,
  getReportTargetsPage,
  normalizeReportDetailPage,
  normalizeReportPage,
  normalizeReportTargetDonationsPage,
  normalizeReportTargetsPage,
} from './reportPages';

const caseId = `rpc_${'A'.repeat(22)}`;
const targetId = `rpt_${'A'.repeat(22)}`;
const summary = {
  id: caseId,
  status: 'expired',
  progress_state: 'complete',
  connector_type: 'openai-compatible',
  canonical_base_url: 'https://api.example.test/v1',
  material_version: '1',
  target_version: '1',
  deadline: 2,
  counts: {
    materials: '1',
    targets: '1',
    distinct_owners: '1',
    processed: '0',
    deleted: '0',
    released: '0',
  },
  retry: null,
  created_at: 1,
  terminal_at: 2,
};
const material = {
  id: '7',
  note_text: 'unsafe endpoint',
  reporter: null,
  source_ip: '127.0.0.1',
  created_at: 1,
};
const target = {
  id: targetId,
  target_seq: '1',
  state: 'released',
  endpoint_key_id: null,
  key_ref: 'A'.repeat(43),
  owner: null,
  endpoint: {
    connector_type: 'openai-compatible',
    canonical_base_url: 'https://api.example.test/v1',
    display_head: 'head',
    display_tail: 'tail',
  },
  discovered_version: '2',
  decided_version: '3',
  donation_match_count: '1',
  created_at: 1,
  updated_at: 2,
};
const donation = {
  donation_id: '7',
  donation_key_id: '8',
  donation_status: 'deleted',
  key_state: 'ended',
  expires_at: 10,
  ended_reason: 'account_deleted',
  ended_at: 11,
};

function metadata(page: string, pageSize: 10 | 20 | 50 | 100, totalItems: string) {
  const total = BigInt(totalItems);
  const pages = total === 0n ? 1n : (total - 1n) / BigInt(pageSize) + 1n;
  return {
    page,
    page_size: pageSize,
    total_items: totalItems,
    total_pages: pages.toString(),
  };
}

function detailBody() {
  return {
    ...summary,
    materials: { data: [material], next_cursor: null },
    materials_pagination: metadata('1', 20, '1'),
    decision: null,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('numbered administrator report adapters', () => {
  it('decodes list, targets, and donation pages with exact numbered metadata', () => {
    expect(
      normalizeReportPage(
        { data: [summary], next_cursor: null, pagination: metadata('1', 20, '1') },
        '1',
        20,
      ).pagination,
    ).toEqual(metadata('1', 20, '1'));
    expect(
      normalizeReportTargetsPage(
        { data: [target], next_cursor: null, pagination: metadata('1', 20, '1') },
        '1',
        20,
      ).data[0].id,
    ).toBe(targetId);
    expect(
      normalizeReportTargetDonationsPage(
        { data: [donation], next_cursor: null, pagination: metadata('1', 20, '1') },
        '1',
        20,
      ).data[0].donation_key_id,
    ).toBe('8');
  });

  it('keeps materials pagination at the detail root', () => {
    expect(normalizeReportDetailPage(detailBody(), '1', 20).materials.data).toEqual([material]);
    expect(() =>
      normalizeReportDetailPage(
        {
          ...detailBody(),
          materials: { data: [material], next_cursor: null, pagination: metadata('1', 20, '1') },
        },
        '1',
        20,
      ),
    ).toThrow(/report materials/i);
  });

  it('rejects unknown/private fields, cursor responses, invalid windows, and mixed page metadata', () => {
    const list = { data: [summary], next_cursor: null, pagination: metadata('1', 20, '1') };
    expect(() => normalizeReportPage({ ...list, private_field: 'secret' }, '1', 20)).toThrow();
    expect(() => normalizeReportPage({ ...list, next_cursor: 'YWJj' }, '1', 20)).toThrow(/cursor/i);
    expect(() =>
      normalizeReportPage({ ...list, pagination: metadata('1', 20, '21') }, '2', 20),
    ).toThrow(/window/i);
    expect(() => normalizeReportPage(list, '0', 20)).toThrow(/page/i);
    expect(() => normalizeReportPage(list, '1', 15 as never)).toThrow(/page size/i);
    expect(() =>
      normalizeReportDetailPage({ ...detailBody(), materials_cursor: 'YWJj' }, '1', 20),
    ).toThrow();
  });

  it('uses only numbered query parameters and forwards abort signals', async () => {
    const requests: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      requests.push(path);
      expect(init?.signal).toBe(controller.signal);
      if (path.includes('/targets/') && path.endsWith('/donations?page=2&page_size=50')) {
        return new Response(
          JSON.stringify({
            data: [donation],
            next_cursor: null,
            pagination: metadata('1', 50, '1'),
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        );
      }
      if (path.endsWith('/targets?page=1&page_size=10')) {
        return new Response(
          JSON.stringify({ data: [target], next_cursor: null, pagination: metadata('1', 10, '1') }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        );
      }
      if (path.includes('?materials_page=1&materials_page_size=20')) {
        return new Response(JSON.stringify(detailBody()), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      return new Response(
        JSON.stringify({
          data: [{ ...summary, status: 'pending_review', terminal_at: null }],
          next_cursor: null,
          pagination: metadata('2', 20, '21'),
        }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      );
    });
    const controller = new AbortController();
    vi.stubGlobal('fetch', fetchMock);
    await getReportPage('pending_review', '2', 20, controller.signal);
    await getReportDetailPage(caseId, '1', 20, controller.signal);
    await getReportTargetsPage(caseId, '1', 10, controller.signal);
    await getReportTargetDonationsPage(caseId, targetId, '2', 50, controller.signal);
    expect(requests).toEqual([
      `/admin/api/reports?status=pending_review&page=2&page_size=20`,
      `/admin/api/reports/${caseId}?materials_page=1&materials_page_size=20`,
      `/admin/api/reports/${caseId}/targets?page=1&page_size=10`,
      `/admin/api/reports/${caseId}/targets/${targetId}/donations?page=2&page_size=50`,
    ]);
    expect(requests.every((path) => !path.includes('cursor') && !path.includes('limit'))).toBe(
      true,
    );
  });

  it('rejects a mismatched detail or list filter before either can populate review state', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ...detailBody(), id: `rpc_${'B'.repeat(21)}A` }), {
          status: 200,
        }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            data: [summary],
            next_cursor: null,
            pagination: metadata('1', 20, '1'),
          }),
          { status: 200 },
        ),
      );
    vi.stubGlobal('fetch', fetchMock);
    await expect(getReportDetailPage(caseId, '1', 20)).rejects.toMatchObject({
      code: 'invalid_response',
    });
    await expect(getReportPage('pending_review', '1', 20)).rejects.toMatchObject({
      code: 'invalid_response',
    });
  });

  it('refuses invalid identifiers before any request', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    for (const id of ['', '../outside', `rpc_${'A'.repeat(21)}B`]) {
      expect(() => getReportDetailPage(id, '1', 20)).toThrow(/identifier/);
    }
    expect(() => getReportTargetDonationsPage(caseId, 'invalid', '1', 20)).toThrow(/identifier/);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
