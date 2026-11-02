import { beforeEach, expect, it, vi } from 'vitest';
import { getBrowserAccounts, getSharedBrowsers } from './browserAPI';

const fetcher = vi.hoisted(() => vi.fn());
vi.mock('@shared/query/http', async (load) => ({
  ...(await load<typeof import('@shared/query/http')>()),
  apiFetch: fetcher,
}));
beforeEach(() => fetcher.mockReset());

it('accepts additional audit metadata without requiring unused member previews', async () => {
  fetcher.mockResolvedValue({
    page: '1',
    page_size: 20,
    total_items: '1',
    total_pages: '1',
    coverage: 'authenticated_browser_sessions',
    future_metadata: { ready: true },
    items: [
      {
        browser_id: 'opaque-group',
        users: 2,
        first_seen: 1800000000,
        last_seen: 1800003600,
        associations: null,
      },
    ],
  });
  const signal = new AbortController().signal;
  const result = await getSharedBrowsers('steward', '1', 20, signal);
  expect(result.items[0].users).toBe(2);
  expect(fetcher).toHaveBeenCalledWith(
    '/api/steward/abuse-audit/shared-browsers?page=1&page_size=20',
    { signal },
  );
});

it('keeps large account IDs exact in a paginated group', async () => {
  fetcher.mockResolvedValue({
    page: '6',
    page_size: 20,
    total_items: '103',
    total_pages: '6',
    browser_id: 'opaque-group',
    associations: [
      { user_id: '9007199254740993', first_seen: 1800000000, last_seen: 1800003600, future: true },
    ],
  });
  const result = await getBrowserAccounts('admin', 'opaque-group', '6', 20);
  expect(result.associations[0].user_id).toBe('9007199254740993');
  expect(fetcher).toHaveBeenCalledWith(
    '/admin/api/abuse-audit/shared-browsers/opaque-group?page=6&page_size=20',
    { signal: undefined },
  );
});

it('rejects an unusable account structure while keeping an empty list valid', async () => {
  const metadata = { page: '1', page_size: 20, total_items: '0', total_pages: '1' };
  fetcher.mockResolvedValueOnce({ ...metadata, items: [] });
  await expect(getSharedBrowsers('admin', '1', 20)).resolves.toMatchObject({ items: [] });
  fetcher.mockResolvedValueOnce({
    ...metadata,
    associations: null,
  });
  await expect(getBrowserAccounts('admin', 'group', '1', 20)).rejects.toMatchObject({
    code: 'invalid_response',
  });
});
