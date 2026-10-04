import { beforeEach, expect, it, vi } from 'vitest';
import { riskAPI } from './api';
const fetcher = vi.hoisted(() => vi.fn());
vi.mock('@shared/query/http', async (load) => ({
  ...(await load<typeof import('@shared/query/http')>()),
  apiFetch: fetcher,
}));
beforeEach(() => vi.clearAllMocks());
it('preserves a historical Discord window, original accounts and partial coverage without an active-user lookup', async () => {
  const scan = {
    id: 'scn_AAAAAAAAAAAAAAAAAAAAAA',
    state: 'completed',
    status: 'completed',
    reason: '',
    from: 1799900000,
    to: 1800000000,
    kind: 'user_ips',
    call_kind: 'total',
    model: '',
    candidates: '1',
    scanned: '1',
    scanned_candidates: '1',
    matched: 1,
    rule_count: 0,
    created_at: 1800000000,
    updated_at: 1800000001,
    expires_at: 1800086400,
    filter_revision: 1,
    changed: false,
    coverage: 'partial',
    truncated_reason: 'identity_or_ip_unavailable',
    source_watermark: '4097',
    last_source_at: 1799999990,
    last_source_id: '4096',
  };
  const item = {
    discord_id: '123456789012345678',
    peak: 3,
    window_from: 1799990000,
    window_to: 1799999990,
    ips: ['203.0.113.1', '203.0.113.2', '203.0.113.3'],
    ips_truncated: false,
    accounts_truncated: false,
    accounts: [
      {
        user_id: '7',
        call_kind: 'self',
        requests: 4,
        dispatched: 3,
        rejected: 1,
        first_seen: 1799991000,
        last_seen: 1799999990,
      },
    ],
  };
  fetcher.mockResolvedValue({
    scan,
    items: [item],
    page: '1',
    page_size: 20,
    total_items: '1',
    total_pages: '1',
    coverage: 'partial',
  });
  await expect(riskAPI('steward').taskResults(scan.id, 'user_ips', '1', 20)).resolves.toMatchObject(
    {
      items: [item],
      scan: { source_watermark: '4097', last_source_id: '4096' },
      coverage: 'partial',
    },
  );
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0][0]).toContain('/api/steward/abuse-audit/scans/');
});
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

it('decodes a complete numbered user task result', async () => {
  const scan = {
    id: 'scn_BBBBBBBBBBBBBBBBBBBBBQ',
    state: 'completed',
    status: 'completed',
    reason: '',
    from: 1799900000,
    to: 1800000000,
    kind: 'users',
    call_kind: 'total',
    model: '',
    candidates: '41',
    scanned: '41',
    scanned_candidates: '41',
    matched: 41,
    rule_count: 0,
    created_at: 1800000000,
    updated_at: 1800000001,
    expires_at: 1800086400,
    filter_revision: 1,
    changed: false,
    coverage: 'complete',
  };
  fetcher.mockResolvedValue({
    scan,
    page: '1',
    page_size: 20,
    total_items: '41',
    total_pages: '3',
    coverage: 'complete',
    items: Array.from({ length: 20 }, (_, index) => ({
      user_id: String(index + 1),
      rpm_committed: 1,
      rpm_denied: 0,
      concurrency_denied: 0,
      peak: 1,
      occupancy_millis: 1000,
      complete_minutes: 1,
      incomplete_minutes: 0,
      high_rpm_minutes: 0,
      high_concurrency_minutes: 0,
      rpm_risk: false,
      concurrency_risk: false,
      risk_scope: 'total',
    })),
  });
  await expect(riskAPI('admin').taskResults(scan.id, 'users', '1', 20)).resolves.toMatchObject({
    total_items: '41',
    scan: { kind: 'users' },
  });
});

it('decodes numbered rule, source and access envelopes with their non-pagination fields', async () => {
  fetcher.mockResolvedValueOnce({
    items: [],
    page: '1',
    page_size: 20,
    total_items: '0',
    total_pages: '1',
    revision: 'revision-a',
    changed: false,
  });
  await expect(riskAPI('admin').numberedRules('1', 20)).resolves.toMatchObject({
    revision: 'revision-a',
    total_items: '0',
  });
  fetcher.mockResolvedValueOnce({
    items: [],
    next: '',
    has_more: false,
    from: 1,
    to: 2,
    coverage: 'source_page',
    scanned: 0,
    page: '1',
    page_size: 20,
    total_items: '0',
    total_pages: '1',
    watermark: '0',
    changed: false,
  });
  await expect(riskAPI('admin').users({ page: 1, page_size: 20 })).resolves.toMatchObject({
    watermark: '0',
    total_items: '0',
  });
  fetcher.mockResolvedValueOnce({
    data: [],
    page: '1',
    page_size: 20,
    total_items: '0',
    total_pages: '1',
    watermark: '0',
    changed: false,
    from: 1,
    to: 2,
  });
  await expect(riskAPI('admin').numberedAccess({}, '1', 20)).resolves.toMatchObject({
    watermark: '0',
    total_items: '0',
  });
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
    await expect(riskAPI(role).scanResults(completedScan.id, '1', 20)).resolves.toMatchObject({ total_items: '1' });
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

it('sends an explicit unbind only when requested', async () => {
  fetcher.mockResolvedValue({});
  const input = {
    name: 'Example',
    status: 'suspected' as const,
    revision: 7,
    enabled: true,
    conditions: [
      {
        field: 'user_agent' as const,
        operator: 'prefix' as const,
        value: 'Example/',
        case_sensitive: false,
      },
    ],
    evidence_note: '',
    evidence_url: '',
  };
  await riskAPI('admin')
    .saveRule(input, 'rsk_example')
    .catch(() => undefined);
  expect(fetcher.mock.calls[0][1].json).not.toHaveProperty('auto_ban');
  await riskAPI('admin')
    .saveRule({ ...input, auto_ban: null }, 'rsk_example')
    .catch(() => undefined);
  expect(fetcher.mock.calls[1][1].json).toHaveProperty('auto_ban', null);
});
