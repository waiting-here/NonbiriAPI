import { beforeEach, expect, it, vi } from 'vitest';
import { riskAPI } from './api';
const fetcher = vi.hoisted(() => vi.fn());
vi.mock('@shared/query/http', async (load) => ({
  ...(await load<typeof import('@shared/query/http')>()),
  apiFetch: fetcher,
}));
beforeEach(() => vi.clearAllMocks());
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
