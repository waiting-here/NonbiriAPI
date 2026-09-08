import { afterEach, describe, expect, it, vi } from 'vitest';
import { getManagedCharityModel, getManagedCharityModelsPage } from './charityModelPages';
import type { CharityRole } from './charity';

const model = (id = '1', extra: Record<string, unknown> = {}) => ({
  route_strategy: 'expiry_weighted',
  id,
  provider: 'Provider',
  model: 'model',
  full_name: '[公益]Provider/model',
  enabled: true,
  allowed_levels: [1, 2, 3, 4, 5],
  public_description: '',
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
  ...extra,
});
const response = (data: unknown[], pagination: Record<string, unknown> = {}) => ({
  data,
  next_cursor: null,
  pagination: {
    page: '1',
    page_size: 20,
    total_items: String(data.length),
    total_pages: '1',
    ...pagination,
  },
});
function mock(value: unknown) {
  const fetchMock = vi.fn(
    async () =>
      new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } }),
  );
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}
afterEach(() => vi.unstubAllGlobals());

describe('managed charity model pages', () => {
  it.each(['admin', 'steward'] as const)(
    'reads a clamped %s page with exact identity and signal',
    async (role) => {
      const fetchMock = mock(
        response([model('101')], {
          page: '2',
          page_size: 100,
          total_items: '101',
          total_pages: '2',
        }),
      );
      const abort = new AbortController();
      const result = await getManagedCharityModelsPage(
        role,
        'provider',
        'true',
        '2147483647',
        100,
        abort.signal,
      );
      expect(result.data.map((row) => row.id)).toEqual(['101']);
      expect(fetchMock).toHaveBeenCalledOnce();
      const [path, options] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
      expect(path).toBe(
        `${role === 'admin' ? '/admin/api' : '/api/steward'}/charity-models?q=provider&enabled=true&page=2147483647&page_size=100`,
      );
      expect(options.signal).toBe(abort.signal);
    },
  );

  it.each([10, 20, 50, 100] as const)('keeps the empty %s-item page explicit', async (size) => {
    mock(response([], { page_size: size }));
    expect(
      (await getManagedCharityModelsPage('admin', '', '', '1', size)).pagination.total_pages,
    ).toBe('1');
  });

  it.each([
    response([model(), model()]),
    response([model()], { total_items: '2' }),
    response([model('1', { enabled: false })]),
    response([model('1', { provider: 'else', full_name: '[公益]else/model' })]),
    { ...response([model()]), next_cursor: 'old-cursor' },
    response([model('1', { secret: 'forbidden projection' })]),
  ])('rejects inconsistent, wrong-filter or over-broad model pages', async (payload) => {
    mock(payload);
    await expect(
      getManagedCharityModelsPage('admin', 'provider', 'true', '1', 20),
    ).rejects.toMatchObject({ code: 'invalid_response', status: 200 });
  });

  it('rejects invalid input before fetching and verifies detail identity', async () => {
    const fetchMock = mock(model('2'));
    for (const query of ['😀'.repeat(129), 'invalid\u0085query', '\ud800']) {
      expect(() => getManagedCharityModelsPage('admin', query, '', '1', 20)).toThrow(
        expect.objectContaining({ status: 400 }),
      );
    }
    expect(() => getManagedCharityModelsPage('user' as CharityRole, '', '', '1', 20)).toThrow(
      expect.objectContaining({ status: 400 }),
    );
    expect(() => getManagedCharityModel('admin', '01')).toThrow(
      expect.objectContaining({ status: 400 }),
    );
    expect(fetchMock).not.toHaveBeenCalled();
    await expect(getManagedCharityModel('admin', '1')).rejects.toMatchObject({
      status: 200,
      code: 'invalid_response',
    });
  });
});
