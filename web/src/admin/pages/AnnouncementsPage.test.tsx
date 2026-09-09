import { screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AnnouncementsPage } from './AnnouncementsPage';
import { installJsonFetchFixtures, renderWithProviders } from '../../../test/unit/support';

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:admin:announcements-page-size:v1');
});

beforeEach(() => {
  window.localStorage.setItem('nonbiri:admin:announcements-page-size:v1', '20');
});

const announcementID = (suffix: string): string => `ann_${suffix.padEnd(22, 'A').slice(0, 22)}`;

function announcement(suffix: string, title = `Announcement ${suffix}`) {
  return {
    id: announcementID(suffix),
    state: 'draft',
    revision: '1',
    draft: { zh: { title, body: '正文' }, en: null },
    published: null,
    severity: 'info',
    pinned: false,
    dismissible: true,
    expires_at: null,
    withdrawn_at: null,
    created_at: 1_800_000_000,
    updated_at: 1_800_000_001,
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

describe('administrator announcements page', () => {
  it('shows the session error instead of waiting on the disabled announcement query', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: {}, status: 401 },
    ]);
    await renderWithProviders(<AnnouncementsPage />, { station: 'admin', role: 'admin' });

    expect(await screen.findByRole('heading', { name: 'Sign-in required' })).toBeVisible();
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('resets server paging for filters and remembers the selected size', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: { admin: { username: 'root' } } },
      {
        method: 'GET',
        path: '/admin/api/announcements?page=1&page_size=20',
        body: page(
          Array.from({ length: 20 }, (_, index) => announcement(String(index + 1))),
          { page: '1', page_size: 20, total_items: '21', total_pages: '2' },
        ),
      },
      {
        method: 'GET',
        path: '/admin/api/announcements?page=2&page_size=20',
        body: page([announcement('21', 'Announcement 21')], {
          page: '2',
          page_size: 20,
          total_items: '21',
          total_pages: '2',
        }),
      },
      {
        method: 'GET',
        path: '/admin/api/announcements?state=published&page=1&page_size=20',
        body: page([announcement('filtered', 'Filtered state')]),
      },
      {
        method: 'GET',
        path: '/admin/api/announcements?state=published&severity=warning&page=1&page_size=20',
        body: page([announcement('warning', 'Filtered severity')]),
      },
      {
        method: 'GET',
        path: '/admin/api/announcements?state=published&severity=warning&page=1&page_size=50',
        body: page([announcement('size', 'Filtered size')], {
          page: '1',
          page_size: 50,
          total_items: '1',
          total_pages: '1',
        }),
      },
    ]);
    const rendered = await renderWithProviders(<AnnouncementsPage />, {
      station: 'admin',
      role: 'admin',
    });

    expect(await screen.findByText('Announcement 1')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Announcement 21')).toBeVisible();

    await rendered.user.selectOptions(screen.getByRole('combobox', { name: 'State' }), 'published');
    expect(await screen.findByText('Filtered state')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();

    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Severity' }),
      'warning',
    );
    expect(await screen.findByText('Filtered severity')).toBeVisible();
    await rendered.user.selectOptions(
      screen.getByRole('combobox', { name: 'Items per page' }),
      '50',
    );
    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50'),
    );
    expect(screen.getByText('Filtered size')).toBeVisible();
    expect(window.localStorage.getItem('nonbiri:admin:announcements-page-size:v1')).toBe('50');
    expect(fetchMock.mock.calls.map(([input]) => String(input))).not.toContain(
      '/admin/api/announcements?cursor=',
    );
  });

  it('keeps pagination metadata visible for an empty filtered result', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/session', body: { admin: { username: 'root' } } },
      {
        method: 'GET',
        path: '/admin/api/announcements?page=1&page_size=20',
        body: page([], { page: '1', page_size: 20, total_items: '0', total_pages: '1' }),
      },
      {
        method: 'GET',
        path: '/admin/api/announcements?state=published&page=1&page_size=20',
        body: page([], { page: '1', page_size: 20, total_items: '0', total_pages: '1' }),
      },
    ]);
    const rendered = await renderWithProviders(<AnnouncementsPage />, {
      station: 'admin',
      role: 'admin',
    });
    await rendered.user.selectOptions(screen.getByRole('combobox', { name: 'State' }), 'published');

    expect(await screen.findByRole('heading', { name: 'No announcements' })).toBeVisible();
    expect(screen.getByText('Page 1 of 1 · Total: 0')).toBeVisible();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toBeVisible();
  });
});
