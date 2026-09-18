import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { DashboardPage } from './DashboardPage';
import { renderWithProviders } from '../../../test/unit/support';
import { adminPageKeys } from '../features/operations/adminPages';

vi.mock('../features/operations/core', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../features/operations/core')>()),
  getAdminUsage: async () => null,
  getAdminActivity: async () => ({ enabled: false, data: [], next_cursor: null }),
  getAdminSiteTimezoneOffset: async () => 0,
}));
vi.mock('../features/operations/reports', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../features/operations/reports')>()),
  getReportBadge: async () => null,
}));

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function overview(userCount: number, total = 37) {
  return {
    data: Array.from({ length: total === 0 ? 0 : 20 }, (_, index) => ({
      base_url: `https://endpoint-${index}.example/v1`,
      user_count: String(userCount),
      endpoint_count: String(userCount),
      key_count: '0',
      users: Array.from({ length: Math.min(3, userCount) }, (_, user) => ({
        user_id: String(user + 1),
        endpoint_count: '1',
        key_count: '0',
        enabled_count: '1',
      })),
    })),
    next_cursor: null,
    pagination: {
      page: '1',
      page_size: 20,
      total_items: String(total),
      total_pages: String(Math.max(1, Math.ceil(total / 20))),
    },
  };
}

describe('dashboard endpoint overview', () => {
  it.each([101, 10017])(
    'uses a bounded numbered page for %i users sharing an endpoint',
    async (count) => {
      const requests: URL[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL) => {
          const url = new URL(
            input instanceof Request ? input.url : String(input),
            window.location.origin,
          );
          requests.push(url);
          if (url.pathname === '/admin/api/session')
            return response({ admin: { username: 'dashboard-admin' } });
          expect(url.pathname).toBe('/admin/api/overview/endpoints');
          if (!url.searchParams.has('page'))
            return response(
              { error: { code: 'payload_too_large', message: 'payload is too large' } },
              413,
            );
          return response(overview(count));
        }),
      );
      const { queryClient } = await renderWithProviders(<DashboardPage />, {
        station: 'admin',
        locale: 'en',
      });
      expect(
        await screen.findByText(
          'There are 37 endpoint groups. Open the endpoints page for details.',
        ),
      ).toBeVisible();
      expect(screen.queryByText(/payload is too large/)).not.toBeInTheDocument();
      expect(screen.getByRole('link', { name: /endpoints/i })).toHaveAttribute(
        'href',
        '/endpoints',
      );
      const calls = requests.filter((url) => url.pathname === '/admin/api/overview/endpoints');
      expect(calls).toHaveLength(1);
      expect(calls[0].searchParams.get('page')).toBe('1');
      expect(calls[0].searchParams.get('page_size')).toBe('20');
      expect(calls[0].searchParams.has('limit')).toBe(false);
      expect(
        queryClient.getQueryData(adminPageKeys.endpoints('dashboard-admin', '', '1', 20)),
      ).toBeDefined();
    },
  );

  it('can retry an endpoint error and displays an empty result', async () => {
    let attempts = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        );
        if (url.pathname === '/admin/api/session')
          return response({ admin: { username: 'dashboard-admin' } });
        attempts += 1;
        return attempts === 1
          ? response({ error: { code: 'service_unavailable', message: 'Please retry' } }, 503)
          : response(overview(0, 0));
      }),
    );
    const { user } = await renderWithProviders(<DashboardPage />, {
      station: 'admin',
      locale: 'en',
    });
    const error = await screen.findByRole('alert');
    await user.click(within(error).getByRole('button', { name: /retry/i }));
    await waitFor(() => expect(attempts).toBe(2));
    expect(
      await screen.findByText('The administrator API returned no endpoint overview records.'),
    ).toBeVisible();
  });
});
