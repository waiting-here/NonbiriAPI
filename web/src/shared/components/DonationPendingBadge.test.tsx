import { act, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { charityKeys } from '@shared/operations/charity';
import { DonationPendingBadge } from './DonationPendingBadge';

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function badge(count: string) {
  return { pending_count: count, server_now: 1 };
}

const exactBadgeCount = '9007199254740993';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('DonationPendingBadge', () => {
  it.each([
    {
      role: 'admin' as const,
      station: 'admin' as const,
      endpoint: '/admin/api/donations/badge',
      href: '/charity?handling=pending',
    },
    {
      role: 'steward' as const,
      station: 'user' as const,
      endpoint: '/api/steward/donations/badge',
      href: '/steward?tab=charity&handling=pending',
    },
  ])(
    'shows the exact count and role-specific pending link for $role',
    async ({ role, station, endpoint, href }) => {
      const fetchMock = vi.fn<typeof fetch>(async (input) => {
        expect(String(input)).toBe(endpoint);
        return jsonResponse(badge(exactBadgeCount));
      });
      vi.stubGlobal('fetch', fetchMock);

      const view = await renderWithProviders(
        <DonationPendingBadge role={role} accountID="account-a" />,
        { station, role: role === 'admin' ? 'admin' : 'level5' },
      );

      const link = await screen.findByRole('link', {
        name: `${exactBadgeCount} donations awaiting shared follow-up`,
      });
      expect(link).toHaveAttribute('href', href);
      expect(within(link).getByText('Pending')).toBeVisible();
      expect(within(link).getByText('99+')).toBeVisible();
      expect(view.queryClient.getQueryData(charityKeys.badge(role, 'account-a'))).toEqual(
        badge(exactBadgeCount),
      );
    },
  );

  it('shows an unknown count after a failed read and recovers through retry', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    fetchMock
      .mockImplementationOnce(async () =>
        jsonResponse({ error: { code: 'service_unavailable', message: 'try later' } }, 503),
      )
      .mockImplementationOnce(async () => jsonResponse(badge('2')));
    vi.stubGlobal('fetch', fetchMock);

    const view = await renderWithProviders(
      <DonationPendingBadge role="admin" accountID="account-a" />,
      { station: 'admin', role: 'admin' },
    );

    expect(
      await screen.findByRole('link', { name: 'Pending donation count is unavailable' }),
    ).toBeVisible();
    expect(screen.getByText('?')).toBeVisible();
    const retry = await screen.findByRole('button', { name: 'Retry pending donation count' });
    await view.user.click(retry);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(
      await screen.findByRole('link', { name: '2 donations awaiting shared follow-up' }),
    ).toBeVisible();
    expect(
      screen.queryByRole('button', { name: 'Retry pending donation count' }),
    ).not.toBeInTheDocument();
  });

  it('refreshes after 30 seconds and when the document becomes visible', async () => {
    vi.useFakeTimers();
    let responseIndex = 0;
    const counts = ['1', '2', '3'];
    const fetchMock = vi.fn<typeof fetch>(async () => {
      const count = counts[Math.min(responseIndex++, counts.length - 1)];
      return jsonResponse(badge(count));
    });
    vi.stubGlobal('fetch', fetchMock);

    await renderWithProviders(<DonationPendingBadge role="admin" accountID="account-a" />, {
      station: 'admin',
      role: 'admin',
    });

    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(
      screen.getByRole('link', { name: '1 donations awaiting shared follow-up' }),
    ).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
      await Promise.resolve();
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(
      screen.getByRole('link', { name: '2 donations awaiting shared follow-up' }),
    ).toBeVisible();

    await act(async () => {
      window.dispatchEvent(new Event('visibilitychange'));
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
      await Promise.resolve();
    });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(
      screen.getByRole('link', { name: '3 donations awaiting shared follow-up' }),
    ).toBeVisible();
  });

  it('uses a separate query key when the account identity changes', async () => {
    let nextCount = 1;
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(badge(String(nextCount++))));
    vi.stubGlobal('fetch', fetchMock);

    const view = await renderWithProviders(
      <DonationPendingBadge role="admin" accountID="account-a" />,
      { station: 'admin', role: 'admin' },
    );
    const firstKey = charityKeys.badge('admin', 'account-a');
    const secondKey = charityKeys.badge('admin', 'account-b');
    expect(
      await screen.findByRole('link', { name: '1 donations awaiting shared follow-up' }),
    ).toBeVisible();
    expect(view.queryClient.getQueryData(firstKey)).toEqual(badge('1'));

    view.rerender(<DonationPendingBadge role="admin" accountID="account-b" />);
    expect(
      await screen.findByRole('link', { name: '2 donations awaiting shared follow-up' }),
    ).toBeVisible();
    expect(firstKey).not.toEqual(secondKey);
    expect(view.queryClient.getQueryData(firstKey)).toEqual(badge('1'));
    expect(view.queryClient.getQueryData(secondKey)).toEqual(badge('2'));
  });

  it.each([
    {
      status: 401,
      role: 'admin' as const,
      station: 'admin' as const,
      endpoint: '/admin/api/donations/badge',
      sentinel: [...charityKeys.root('admin'), 'sentinel'] as const,
    },
    {
      status: 403,
      role: 'admin' as const,
      station: 'admin' as const,
      endpoint: '/admin/api/donations/badge',
      sentinel: [...charityKeys.root('admin'), 'sentinel'] as const,
    },
    {
      status: 401,
      role: 'steward' as const,
      station: 'user' as const,
      endpoint: '/api/steward/donations/badge',
      sentinel: [...charityKeys.root('steward'), 'sentinel'] as const,
    },
    {
      status: 403,
      role: 'steward' as const,
      station: 'user' as const,
      endpoint: '/api/steward/donations/badge',
      sentinel: [...charityKeys.root('steward'), 'sentinel'] as const,
    },
  ])(
    'hides the badge and evicts its station cache after HTTP $status for $role',
    async ({ status, role, station, endpoint, sentinel }) => {
      const fetchMock = vi.fn<typeof fetch>(async (input) => {
        expect(String(input)).toBe(endpoint);
        return jsonResponse(
          { error: { code: status === 401 ? 'unauthorized' : 'forbidden', message: 'denied' } },
          status,
        );
      });
      vi.stubGlobal('fetch', fetchMock);

      const view = await renderWithProviders(
        <DonationPendingBadge role={role} accountID="account-a" />,
        { station, role: role === 'admin' ? 'admin' : 'level5' },
      );
      view.queryClient.setQueryData(sentinel, { private: 'marker' });

      await waitFor(() => expect(screen.queryByRole('link')).not.toBeInTheDocument());
      expect(screen.queryByText('Pending')).not.toBeInTheDocument();
      expect(view.queryClient.getQueryData(sentinel)).toBeUndefined();
      expect(view.queryClient.getQueryData(charityKeys.badge(role, 'account-a'))).toBeUndefined();
    },
  );
});
