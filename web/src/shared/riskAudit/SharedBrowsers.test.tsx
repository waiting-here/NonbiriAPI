import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { RiskAuditPanel } from './Panel';

const fetchBrowsers = vi.hoisted(() => vi.fn());
const fetchAccounts = vi.hoisted(() => vi.fn());
vi.mock('./browserAPI', () => ({
  getSharedBrowsers: fetchBrowsers,
  getBrowserAccounts: fetchAccounts,
}));

const group = {
  browser_id: 'private-group-key',
  users: 103,
  first_seen: 1800000000,
  last_seen: 1800003600,
};

beforeEach(() => {
  fetchBrowsers.mockReset();
  fetchAccounts.mockReset();
  fetchAccounts.mockImplementation((_role: string, _id: string, page: string, size: number) =>
    Promise.resolve({
      associations:
        page === '6'
          ? [{ user_id: '101', first_seen: 1800000000, last_seen: 1800003600 }]
          : [
              { user_id: '9007199254740993', first_seen: 1800000000, last_seen: 1800003600 },
              { user_id: '7', first_seen: 1800000300, last_seen: 1800003000 },
            ],
      page,
      page_size: size,
      total_items: '103',
      total_pages: String(Math.ceil(103 / size)),
    }),
  );
  fetchBrowsers.mockImplementation((_role: string, page: string, size: number) =>
    Promise.resolve({
      items: [group],
      page,
      page_size: size,
      total_items: '120',
      total_pages: String(Math.ceil(120 / size)),
    }),
  );
});

it('shows an evidence caveat, preserves account IDs and navigates pages without request-log filters', async () => {
  const view = await renderWithProviders(<RiskAuditPanel role="steward" scopeKey="6" />, {
    station: 'user',
    role: 'level6',
    route: '/steward?tab=risk&audit_tab=browsers&audit_lookback_hours=1&audit_kind=charity',
  });
  expect(await screen.findByRole('heading', { name: 'Browser group 1' })).toBeVisible();
  expect(screen.getByText(/does not establish that accounts belong to one person/)).toBeVisible();
  expect(screen.queryByLabelText('Time range')).not.toBeInTheDocument();
  expect(screen.queryByText(group.browser_id)).not.toBeInTheDocument();
  expect(fetchAccounts).not.toHaveBeenCalled();
  await view.user.click(screen.getByText('View linked accounts'));
  const link = await screen.findByRole('link', { name: 'User 9007199254740993' });
  expect(link).toHaveAttribute('href', '/steward?tab=users&user=9007199254740993');
  const accounts = within(screen.getByRole('group', { name: 'Account pages' }));
  await view.user.click(accounts.getByRole('button', { name: '6' }));
  expect(await screen.findByRole('link', { name: 'User 101' })).toBeVisible();
  expect(fetchAccounts).toHaveBeenLastCalledWith(
    'steward',
    group.browser_id,
    '6',
    20,
    expect.any(AbortSignal),
  );
  await view.user.click(
    within(screen.getByRole('group', { name: 'Browser group pages' })).getByRole('button', {
      name: 'Next',
    }),
  );
  await waitFor(() =>
    expect(fetchBrowsers).toHaveBeenLastCalledWith('steward', '2', 20, expect.any(AbortSignal)),
  );
  expect(await screen.findByRole('heading', { name: 'Browser group 21' })).toBeVisible();
  expect(screen.queryByRole('link', { name: 'User 9007199254740993' })).not.toBeInTheDocument();
  await view.user.selectOptions(screen.getByLabelText('Items per page'), '50');
  await waitFor(() =>
    expect(fetchBrowsers).toHaveBeenLastCalledWith('steward', '1', 50, expect.any(AbortSignal)),
  );
});

it.each(['forbidden', 'steward_permission_denied'])(
  'removes browser associations after %s',
  async (code) => {
    const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
      station: 'admin',
      role: 'admin',
      route: '/abuse-audit?audit_tab=browsers',
    });
    await screen.findByRole('heading', { name: 'Browser group 1' });
    await view.user.click(screen.getByText('View linked accounts'));
    expect(await screen.findByRole('link', { name: 'User 7' })).toBeVisible();
    fetchBrowsers.mockRejectedValueOnce(new ApiError(code, 'Audit permission revoked.', 403));
    await view.user.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() =>
      expect(screen.queryByRole('link', { name: 'User 7' })).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole('heading', { name: 'Browser group 1' })).not.toBeInTheDocument();
    expect(view.queryClient.getQueriesData({ queryKey: ['risk', 'admin', 'operator'] })).toEqual(
      [],
    );
  },
);

it('keeps an MFA failure recoverable by the active-query refresh after verification', async () => {
  const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
    station: 'admin',
    role: 'admin',
    route: '/risk-audit?audit_tab=browsers',
  });
  await screen.findByRole('heading', { name: 'Browser group 1' });
  await view.user.click(screen.getByText('View linked accounts'));
  await screen.findByRole('link', { name: 'User 7' });
  fetchBrowsers.mockRejectedValueOnce(new ApiError('mfa_required', 'Verify your account.', 403));
  await view.user.click(screen.getByRole('button', { name: 'Refresh' }));
  await waitFor(() =>
    expect(screen.queryByRole('link', { name: 'User 7' })).not.toBeInTheDocument(),
  );
  expect(
    view.queryClient
      .getQueryCache()
      .findAll({ queryKey: ['risk', 'admin', 'operator'], type: 'active' }),
  ).toHaveLength(1);
  await view.queryClient.refetchQueries({
    queryKey: ['risk', 'admin', 'operator'],
    type: 'active',
  });
  expect(await screen.findByRole('link', { name: 'User 7' })).toBeVisible();
});

it('refreshes the currently expanded account page together with its group summary', async () => {
  const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
    station: 'admin',
    role: 'admin',
    route: '/risk-audit?audit_tab=browsers',
  });
  await screen.findByRole('heading', { name: 'Browser group 1' });
  await view.user.click(screen.getByText('View linked accounts'));
  await screen.findByRole('link', { name: 'User 7' });
  fetchAccounts.mockResolvedValueOnce({
    associations: [{ user_id: '99', first_seen: 1800000000, last_seen: 1800003600 }],
    page: '1',
    page_size: 20,
    total_items: '2',
    total_pages: '1',
  });
  await view.user.click(screen.getByRole('button', { name: 'Refresh' }));
  expect(await screen.findByRole('link', { name: 'User 99' })).toBeVisible();
  expect(screen.queryByRole('link', { name: 'User 7' })).not.toBeInTheDocument();
});

it('explains empty coverage and retries a failed page without losing its position', async () => {
  fetchBrowsers.mockRejectedValueOnce(new ApiError('service_unavailable', 'Try again.', 503));
  fetchBrowsers.mockResolvedValueOnce({
    items: [],
    page: '1',
    page_size: 20,
    total_items: '0',
    total_pages: '1',
  });
  const view = await renderWithProviders(<RiskAuditPanel role="admin" scopeKey="operator" />, {
    station: 'admin',
    role: 'admin',
    route: '/abuse-audit?audit_tab=browsers&audit_browser_page=3',
  });
  await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
  expect(await screen.findByText('No shared-browser records yet')).toBeVisible();
  expect(fetchBrowsers).toHaveBeenLastCalledWith('admin', '3', 20, expect.any(AbortSignal));
  expect(screen.getByText(/Missing records do not rule out multiple accounts/)).toBeVisible();
});
