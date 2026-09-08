import { fireEvent, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { charityKeys, type DonationHandling } from '@shared/operations/charity';
import { DonationHandlingControl } from './DonationHandling';

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

const pendingHandling: DonationHandling = {
  state: 'pending',
  revision: '7',
  processed_at: null,
  processed_by_role: null,
  closed_at: null,
  closed_reason: null,
};

const processedHandling: DonationHandling = {
  state: 'processed',
  revision: '8',
  processed_at: 100,
  processed_by_role: 'admin',
  closed_at: null,
  closed_reason: null,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('DonationHandlingControl', () => {
  it('sends the clicked revision once and refreshes to the processed state', async () => {
    const response = deferred<Response>();
    const requests: { path: string; init?: RequestInit }[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      requests.push({ path: String(input), init });
      return response.promise;
    });
    vi.stubGlobal('fetch', fetchMock);
    const refreshSpy = vi.fn();

    function Harness() {
      const [handling, setHandling] = useState(pendingHandling);
      return (
        <DonationHandlingControl
          donationID="7"
          role="admin"
          handling={handling}
          refresh={async () => {
            refreshSpy();
            setHandling(processedHandling);
          }}
        />
      );
    }

    await renderWithProviders(<Harness />, { station: 'admin', role: 'admin' });
    const process = await screen.findByRole('button', { name: 'Mark as processed' });
    fireEvent.click(process);
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(process).toBeDisabled();
    fireEvent.click(process);
    expect(requests).toHaveLength(1);
    expect(requests[0]?.path).toBe('/admin/api/donations/7/handling/processed');
    expect(requests[0]?.init?.method).toBe('POST');
    expect(JSON.parse(String(requests[0]?.init?.body))).toEqual({
      expected_handling_revision: '7',
    });
    expect(new Headers(requests[0]?.init?.headers).get('Idempotency-Key')).toMatch(
      /^[A-Za-z0-9_-]{22,128}$/,
    );

    response.resolve(jsonResponse({ donation_id: '7', handling: processedHandling }));
    await waitFor(() => expect(refreshSpy).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('Processed', { exact: true })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Mark as processed' })).not.toBeInTheDocument();
  });

  it('refreshes after a revision conflict without presenting a false success', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse({ error: { code: 'conflict', message: 'stale handling revision' } }, 409),
    );
    vi.stubGlobal('fetch', fetchMock);
    const refresh = vi.fn(async () => undefined);

    const view = await renderWithProviders(
      <DonationHandlingControl
        donationID="7"
        role="admin"
        handling={pendingHandling}
        refresh={refresh}
      />,
      { station: 'admin', role: 'admin' },
    );
    await view.user.click(await screen.findByRole('button', { name: 'Mark as processed' }));

    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(screen.getByText('Pending follow-up', { exact: true })).toBeVisible();
    expect(screen.queryByText('Processed', { exact: true })).not.toBeInTheDocument();
    expect(
      screen.getByText(
        'Reload the current status before trying again if another manager has changed it.',
      ),
    ).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(refresh).toHaveBeenCalledTimes(2);
  });

  it.each([
    {
      status: 401,
      role: 'admin' as const,
      station: 'admin' as const,
      endpoint: '/admin/api/donations/7/handling/processed',
      sentinel: [...charityKeys.root('admin'), 'sentinel'] as const,
    },
    {
      status: 403,
      role: 'admin' as const,
      station: 'admin' as const,
      endpoint: '/admin/api/donations/7/handling/processed',
      sentinel: [...charityKeys.root('admin'), 'sentinel'] as const,
    },
    {
      status: 401,
      role: 'steward' as const,
      station: 'user' as const,
      endpoint: '/api/steward/donations/7/handling/processed',
      sentinel: [...charityKeys.root('steward'), 'sentinel'] as const,
    },
    {
      status: 403,
      role: 'steward' as const,
      station: 'user' as const,
      endpoint: '/api/steward/donations/7/handling/processed',
      sentinel: [...charityKeys.root('steward'), 'sentinel'] as const,
    },
  ])(
    'evicts station data and reports authority loss after HTTP $status for $role',
    async ({ status, role, station, endpoint, sentinel }) => {
      const fetchMock = vi.fn<typeof fetch>(async (input) => {
        expect(String(input)).toBe(endpoint);
        return jsonResponse(
          { error: { code: status === 401 ? 'unauthorized' : 'forbidden', message: 'denied' } },
          status,
        );
      });
      vi.stubGlobal('fetch', fetchMock);
      const refresh = vi.fn(async () => undefined);
      const onCapabilityLoss = vi.fn();

      const view = await renderWithProviders(
        <DonationHandlingControl
          donationID="7"
          role={role}
          handling={pendingHandling}
          refresh={refresh}
          onCapabilityLoss={onCapabilityLoss}
        />,
        { station, role: role === 'admin' ? 'admin' : 'level5' },
      );
      view.queryClient.setQueryData(sentinel, { private: 'marker' });
      await view.user.click(await screen.findByRole('button', { name: 'Mark as processed' }));

      await waitFor(() => expect(onCapabilityLoss).toHaveBeenCalledTimes(1));
      expect(screen.getByRole('alert')).toHaveTextContent(
        'Charity management access is no longer available.',
      );
      expect(screen.queryByRole('button', { name: 'Mark as processed' })).not.toBeInTheDocument();
      expect(view.queryClient.getQueryData(sentinel)).toBeUndefined();
      expect(refresh).not.toHaveBeenCalled();
    },
  );
});
