import { screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AnnouncementsPage } from './AnnouncementsPage';
import { normalizeUserAuthority, operationsKeys } from '../features/operations/data';
import { installJsonFetchFixtures, renderWithProviders } from '../../../test/unit/support';

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:user:announcements-page-size:v1');
});

beforeEach(() => {
  window.localStorage.setItem('nonbiri:user:announcements-page-size:v1', '20');
});

const announcementID = (suffix: string): string => `ann_${suffix.padEnd(22, 'A').slice(0, 22)}`;
const epoch = `b1e_${'A'.repeat(22)}`;

function summary(suffix: string) {
  return {
    epoch,
    id: announcementID(suffix),
    revision: '1',
    severity: 'info',
    pinned: false,
    dismissible: true,
    published_at: 1_800_000_000,
    expires_at: null,
    effective_language: 'en',
    fallback_from: null,
    title: `Announcement ${suffix}`,
    excerpt: `Summary ${suffix}`,
  };
}

function page(
  data: unknown[],
  pagination: Record<string, unknown> = {
    page: '1',
    page_size: 20,
    total_items: String(data.length),
    total_pages: '1',
  },
) {
  return { data, next_cursor: null, pagination };
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

function session(id = '7') {
  return {
    user: {
      id,
      username: 'member',
      avatar: null,
      avatar_url: null,
      guild_nick: null,
      guild_avatar_url: null,
      lang: 'en',
      is_banned: false,
      banned_until: null,
      charity_suspended_until: null,
      endpoint_limit: null,
      effective_endpoint_limit: '5',
      rpm_limit: null,
      effective_rpm_limit: '60',
      concurrency_limit: null,
      effective_concurrency_limit: '2',
      balance: '0',
      game_balance: '0',
      donation_credit: '0',
      effective_level: 1,
      level_display_name: 'Lv1',
      game_profile_public: false,
      created_at: 1_800_000_000,
      updated_at: 1_800_000_000,
      usage: {
        total_requests: '0',
        total_uncached_input_tokens: '0',
        total_cache_write_input_tokens: '0',
        total_cache_read_input_tokens: '0',
        total_output_tokens: '0',
        total_prompt_tokens: '0',
        total_completion_tokens: '0',
        total_unknown_usage_requests: '0',
      },
    },
  };
}

describe('user announcements page', () => {
  it('shows the session error instead of waiting on the disabled announcement query', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: {}, status: 401 },
    ]);
    await renderWithProviders(<AnnouncementsPage />, { station: 'user', role: 'user' });

    expect(await screen.findByRole('heading', { name: 'Sign-in required' })).toBeVisible();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('navigates server pages, keeps the selected size, and returns to page one', async () => {
    const firstPage = Array.from({ length: 20 }, (_, index) => summary(String(index + 1)));
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session() },
      {
        method: 'GET',
        path: '/api/announcements?page=1&page_size=20',
        body: page(firstPage, { page: '1', page_size: 20, total_items: '21', total_pages: '2' }),
      },
      {
        method: 'GET',
        path: '/api/announcements?page=2&page_size=20',
        body: page([summary('21')], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: '/api/announcements?page=1&page_size=50',
        body: page([summary('1')], {
          page: '1',
          page_size: 50,
          total_items: '1',
          total_pages: '1',
        }),
      },
    ]);
    const rendered = await renderWithProviders(<AnnouncementsPage />, {
      station: 'user',
      role: 'user',
    });

    expect(await screen.findByText('Announcement 1')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('20');
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Announcement 21')).toBeVisible();

    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Items per page' }),
      '50',
    );
    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50'),
    );
    expect(screen.getByText('Announcement 1')).toBeVisible();
    expect(window.localStorage.getItem('nonbiri:user:announcements-page-size:v1')).toBe('50');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).not.toContain(
      '/api/announcements?cursor=',
    );
  });

  it('keeps pagination metadata visible for an empty result', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session() },
      {
        method: 'GET',
        path: '/api/announcements?page=1&page_size=20',
        body: page([], { page: '1', page_size: 20, total_items: '0', total_pages: '1' }),
      },
    ]);
    await renderWithProviders(<AnnouncementsPage />, { station: 'user', role: 'user' });

    expect(await screen.findByRole('heading', { name: 'No announcements' })).toBeVisible();
    expect(screen.getByText('Page 1 of 1 · Total: 0')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toBeVisible();
  });

  it('drops a page response that returns after the signed-in account changes', async () => {
    let currentAccount = 'A';
    const oldPage = deferred<Response>();
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const target = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (target.pathname === '/api/session') {
        return Promise.resolve(jsonResponse(session(currentAccount === 'A' ? '7' : '8')));
      }
      if (target.pathname === '/api/announcements') {
        if (currentAccount === 'A') return oldPage.promise;
        return Promise.resolve(jsonResponse(page([summary('B')])));
      }
      throw new Error(`Unexpected request: ${target.pathname}${target.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<AnnouncementsPage />, {
      station: 'user',
      role: 'user',
    });

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/announcements?page=1&page_size=20',
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      ),
    );
    currentAccount = 'B';
    rendered.queryClient.setQueryData(operationsKeys.session, normalizeUserAuthority(session('8')));
    expect(await screen.findByText('Announcement B')).toBeVisible();

    oldPage.resolve(jsonResponse(page([summary('A')])));
    await waitFor(() => expect(screen.queryByText('Announcement A')).not.toBeInTheDocument());
    expect(screen.getByText('Announcement B')).toBeVisible();
  });

  it('does not reuse the previous language while the new language page is loading', async () => {
    const translated = deferred<Response>();
    let language = 'en';
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL | Request) => {
        const target = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        );
        if (target.pathname === '/api/session') return Promise.resolve(jsonResponse(session()));
        if (target.pathname === '/api/announcements')
          return language === 'en'
            ? Promise.resolve(jsonResponse(page([summary('English')])))
            : translated.promise;
        throw new Error(`Unexpected request: ${target.pathname}`);
      }),
    );
    const rendered = await renderWithProviders(<AnnouncementsPage />, {
      station: 'user',
      role: 'user',
    });
    expect(await screen.findByText('Announcement English')).toBeVisible();
    language = 'zh';
    const changed = session();
    changed.user.lang = 'zh';
    rendered.queryClient.setQueryData(operationsKeys.session, normalizeUserAuthority(changed));
    await waitFor(() => expect(screen.queryByText('Announcement English')).not.toBeInTheDocument());
    translated.resolve(jsonResponse(page([{ ...summary('Chinese'), effective_language: 'zh' }])));
    expect(await screen.findByText('Announcement Chinese')).toBeVisible();
  });
});
