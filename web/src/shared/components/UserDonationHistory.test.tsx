import { screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { UserDonationHistory } from './UserDonationHistory';

function item(id: string) {
  return {
    id,
    status: 'pending',
    revision: '1',
    description: `Synthetic donation ${id}`,
    discord_public_thanks: null,
    owner_hidden_at: 1800000000,
    review_result: null,
    created_at: 1800000000,
    updated_at: 1800000000,
    key_count: '1',
    source_count: '1',
    sources: null,
    state_counts: {
      pending: '0',
      available: '1',
      rate_limited: '0',
      disabled: '0',
      suspended: '0',
      exhausted: '0',
      expired: '0',
      ended: '0',
    },
    use_summary: {
      total: '2',
      approved: '1',
      pending: '1',
      rejected: '0',
      withdrawn: '0',
      review_status: 'approved_pending',
    },
    owner: null,
    reviewer: null,
    handling: {
      state: 'pending',
      revision: '1',
      processed_at: null,
      processed_by_role: null,
      closed_at: null,
      closed_reason: null,
    },
  };
}

afterEach(() => vi.unstubAllGlobals());

it('browses all history pages, reads safe details and returns without losing the page', async () => {
  const requests: URL[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      requests.push(url);
      if (url.pathname === '/api/steward/users/7/donations/21') return Response.json(item('21'));
      if (url.pathname !== '/api/steward/users/7/donations')
        throw new Error('Unexpected history request');
      const page = url.searchParams.get('page') ?? '1';
      return Response.json({
        data:
          page === '1'
            ? Array.from({ length: 20 }, (_, index) => item(String(index + 1)))
            : [item('21')],
        next_cursor: null,
        pagination: { page, page_size: 20, total_items: '21', total_pages: '2' },
      });
    }),
  );
  const view = await renderWithProviders(
    <UserDonationHistory role="steward" accountId="9" userId="7" enabled />,
    { station: 'user', role: 'level5', locale: 'en' },
  );
  await screen.findByText('Synthetic donation 1');
  expect(screen.getAllByText('Tucked away by owner')).toHaveLength(20);
  await view.user.click(screen.getByRole('button', { name: 'Next' }));
  await screen.findByText('Synthetic donation 21');
  await view.user.click(screen.getByRole('button', { name: 'View' }));
  await screen.findByRole('heading', { name: 'Donation details' });
  expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull();
  await view.user.click(screen.getByRole('button', { name: 'Back to donation history' }));
  expect(screen.queryByRole('heading', { name: 'Donation details' })).toBeNull();
  expect(screen.getByText('Synthetic donation 21')).toBeInTheDocument();
  expect(requests.some((url) => url.pathname.endsWith('/21'))).toBe(true);
  expect(
    requests
      .filter((url) => !url.pathname.endsWith('/21'))
      .every((url) => !url.searchParams.has('view')),
  ).toBe(true);
});

it('removes the history surface and offers contact guidance on permission loss', async () => {
  const onCapabilityLoss = vi.fn();
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({ error: { code: 'forbidden', message: 'access denied' } }, { status: 403 }),
    ),
  );
  await renderWithProviders(
    <UserDonationHistory
      role="steward"
      accountId="9"
      userId="7"
      enabled
      onCapabilityLoss={onCapabilityLoss}
    />,
    { station: 'user', role: 'level5', locale: 'en' },
  );
  await screen.findByText(
    'Contact an administrator or level 6 steward to request access to donation history.',
  );
  await waitFor(() => expect(onCapabilityLoss).toHaveBeenCalledOnce());
  expect(screen.queryByRole('table')).toBeNull();
});

it('keeps filters and retry available when only the history permission is denied', async () => {
  const onCapabilityLoss = vi.fn();
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json(
        {
          error: {
            code: 'steward_permission_denied',
            message: 'Contact an administrator or level 6 steward.',
          },
        },
        { status: 403 },
      ),
    ),
  );
  await renderWithProviders(
    <UserDonationHistory
      role="steward"
      accountId="9"
      userId="7"
      enabled
      onCapabilityLoss={onCapabilityLoss}
    />,
    { station: 'user', role: 'level5', locale: 'en' },
  );
  await screen.findByRole('button', { name: 'Retry' });
  expect(screen.getByRole('searchbox')).toBeInTheDocument();
  expect(onCapabilityLoss).not.toHaveBeenCalled();
});
