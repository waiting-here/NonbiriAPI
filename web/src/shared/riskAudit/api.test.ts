import { beforeEach, expect, it, vi } from 'vitest';
import { riskAPI } from './api';
const fetcher = vi.hoisted(() => vi.fn());
vi.mock('@shared/query/http', async (load) => ({
  ...(await load<typeof import('@shared/query/http')>()),
  apiFetch: fetcher,
}));
beforeEach(() => vi.clearAllMocks());
it('accepts a healthy capture with no recorded gap and no generation samples', async () => {
  fetcher.mockResolvedValue({
    authenticated_events: 0,
    anonymous_events: 2,
    model_list_events: 0,
    generation_requests: 0,
    model_generation_ratio: null,
    coverage: { capture_started_at: 1800000000, dropped: 0, last_gap_at: null },
    paths: [],
  });
  await expect(riskAPI('admin').accessSummary({ lookback_hours: 24 })).resolves.toMatchObject({
    coverage: { last_gap_at: null },
    model_generation_ratio: null,
  });
  expect(fetcher.mock.calls[0][0]).toContain('lookback_hours=24');
});
it('still rejects malformed capture timestamps', async () => {
  fetcher.mockResolvedValue({
    model_generation_ratio: null,
    authenticated_events: 0,
    anonymous_events: 0,
    model_list_events: 0,
    generation_requests: 0,
    coverage: { capture_started_at: 1, dropped: 0, last_gap_at: 'yesterday' },
    paths: [],
  });
  await expect(riskAPI('admin').accessSummary({})).rejects.toMatchObject({
    code: 'invalid_response',
  });
});
it('rejects oversized audit result pages', async () => {
  fetcher.mockResolvedValue({
    items: Array(101).fill({}),
    next: '',
    has_more: false,
    from: 1,
    to: 2,
    coverage: 'source_page',
  });
  await expect(riskAPI('admin').clients({})).rejects.toMatchObject({ code: 'invalid_response' });
});

const completedScan = {
  id: 'scn_AAAAAAAAAAAAAAAAAAAAAA',
  state: 'completed',
  reason: '',
  from: 1800000000,
  to: 1800086400,
  kind: 'total',
  model: '',
  candidates: '257',
  scanned: '257',
  matched: 0,
  rule_count: 1,
  created_at: 1800086400,
  updated_at: 1800086410,
  expires_at: 1800172800,
};
it.each(['admin', 'steward'] as const)(
  'decodes the flat scan results envelope for %s and preserves pagination validation',
  async (role) => {
    const result = {
      scan: completedScan,
      items: [],
      page: '1',
      page_size: 20,
      total_items: '0',
      total_pages: '1',
    };
    fetcher.mockResolvedValue(result);
    await expect(riskAPI(role).scanResults(completedScan.id, '1', 20)).resolves.toEqual(result);
    fetcher.mockResolvedValue({ ...result, total_items: '1' });
    await expect(riskAPI(role).scanResults(completedScan.id, '1', 20)).rejects.toMatchObject({
      code: 'invalid_response',
    });
    fetcher.mockResolvedValue({
      ...result,
      scan: { ...completedScan, id: 'scn_BBBBBBBBBBBBBBBBBBBBBQ' },
    });
    await expect(riskAPI(role).scanResults(completedScan.id, '1', 20)).rejects.toMatchObject({
      code: 'invalid_response',
    });
  },
);
it('sends only mutable rule fields and retains the revision', async () => {
  fetcher.mockResolvedValue({});
  await riskAPI('steward')
    .saveRule(
      {
        name: 'Example',
        status: 'suspected',
        revision: 7,
        enabled: false,
        conditions: [
          { field: 'user_agent', operator: 'prefix', value: 'Example/', case_sensitive: false },
        ],
        evidence_note: '',
        evidence_url: '',
        created_by_role: 'admin',
      } as never,
      'rsk_example',
    )
    .catch(() => undefined);
  const [path, options] = fetcher.mock.calls[0];
  expect(path).toBe('/api/steward/abuse-audit/client-rules/rsk_example');
  expect(options.json.revision).toBe(7);
  expect(options.json).not.toHaveProperty('created_by_role');
});
