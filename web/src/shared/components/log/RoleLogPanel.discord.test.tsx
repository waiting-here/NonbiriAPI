import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { RoleLogPanel } from './RoleLogPanel';

const discordID = '18446744073709551615';
const scenarios = [
  { role: 'admin', station: 'admin', accountRole: 'admin', root: '/admin/api/logs' },
  { role: 'steward', station: 'user', accountRole: 'level5', root: '/api/steward/logs' },
  { role: 'steward', station: 'user', accountRole: 'level6', root: '/api/steward/logs' },
] as const;

function installEmptyLogs() {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), window.location.origin);
    const body = url.pathname.endsWith('/time-zones')
      ? { version: 'go1.26.6-zoneinfo', zones: ['UTC'] }
      : url.pathname.endsWith('/time-context')
        ? { mode: 'site', offset_minutes: 0 }
        : {
            data: [],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
          };
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

function HistoryProbe() {
  const { search } = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="query">{search}</output>
      <button onClick={() => navigate(-1)}>Previous view</button>
      <button onClick={() => navigate(1)}>Next view</button>
    </>
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it.each(scenarios)(
  'keeps common filters visible and restores the exact $accountRole Discord filter and export',
  async ({ role, station, accountRole, root }) => {
    const fetchMock = installEmptyLogs();
    const view = await renderWithProviders(
      <>
        <RoleLogPanel accountId="viewer" role={role} />
        <HistoryProbe />
      </>,
      {
        station,
        role: accountRole,
        route: '/logs?discord_id=' + discordID + '&user_id=7&charity_model=Shared',
      },
    );
    const form = screen.getByTestId('log-filters');
    const discord = within(form).getByLabelText('Discord ID');
    const charity = within(form).getByLabelText('Called charity model');
    const upstream = within(form).getByLabelText('Model used');
    expect(discord).toHaveValue(discordID);
    expect(discord).toHaveAttribute('type', 'text');
    expect(discord.closest('details')).toBeNull();
    expect(charity.closest('details')).toBeNull();
    expect(upstream.closest('details')).not.toBeNull();
    expect(upstream).not.toBeVisible();
    const expected = `${root}?user_id=7&discord_id=${discordID}&charity_model=Shared&page=1&page_size=20`;
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(expected),
    );
    await view.user.click(screen.getByText('Export ▾'));
    expect(screen.getByRole('link', { name: 'Export CSV (spreadsheets)' })).toHaveAttribute(
      'href',
      `${root}/export.csv?user_id=7&discord_id=${discordID}&charity_model=Shared`,
    );
    await view.user.click(within(form).getByText('More filters', { exact: true }));
    expect(upstream).toBeVisible();
    fireEvent.change(upstream, { target: { value: 'vendor/model' } });
    fireEvent.submit(form);
    await waitFor(() =>
      expect(screen.getByTestId('query')).toHaveTextContent('upstream_model=vendor%2Fmodel'),
    );
    expect(discord).toHaveValue(discordID);
    await view.user.click(screen.getByRole('button', { name: 'Reset' }));
    await waitFor(() => expect(discord).toHaveValue(''));
    expect(screen.getByTestId('query')).not.toHaveTextContent('discord_id=');
    await view.user.click(screen.getByRole('button', { name: 'Previous view' }));
    await waitFor(() => expect(discord).toHaveValue(discordID));
    expect(upstream).toHaveValue('vendor/model');
    await view.user.click(screen.getByRole('button', { name: 'Next view' }));
    await waitFor(() => expect(discord).toHaveValue(''));
  },
);

it.each(['0', '01', '18446744073709551616', discordID + '&discord_id=' + discordID])(
  'blocks an invalid or repeated Discord URL filter %s until reset',
  async (value) => {
    const fetchMock = installEmptyLogs();
    const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="admin" />, {
      station: 'admin',
      role: 'admin',
      route: '/logs?discord_id=' + value,
    });
    expect(screen.getByRole('alert')).toHaveTextContent('Discord ID filter is invalid');
    expect(screen.queryByText('Export ▾')).toBeNull();
    expect(
      fetchMock.mock.calls
        .map(([path]) => String(path))
        .some((path) => new URL(path, window.location.origin).pathname === '/admin/api/logs'),
    ).toBe(false);
    await view.user.click(screen.getByRole('button', { name: 'Reset' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
        '/admin/api/logs?page=1&page_size=20',
      ),
    );
    expect(screen.queryByRole('alert')).toBeNull();
  },
);

it('rejects an invalid Discord draft without applying a broader query', async () => {
  installEmptyLogs();
  const view = await renderWithProviders(<RoleLogPanel accountId="viewer" role="steward" />, {
    station: 'user',
    role: 'level5',
  });
  await waitFor(() => expect(screen.getByRole('button', { name: 'Apply filter' })).toBeEnabled());
  fireEvent.change(screen.getByLabelText('Discord ID'), { target: { value: '01' } });
  fireEvent.submit(screen.getByTestId('log-filters'));
  expect(screen.getByRole('alert')).toHaveTextContent('Discord ID filter is invalid');
  await view.user.click(screen.getByRole('button', { name: 'Last hour' }));
  expect(screen.getByRole('alert')).toHaveTextContent('Discord ID filter is invalid');
  fireEvent.change(screen.getByLabelText('Discord ID'), { target: { value: discordID } });
  fireEvent.submit(screen.getByTestId('log-filters'));
  await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
  expect(screen.getByLabelText('Discord ID')).toHaveValue(discordID);
});

it('keeps personal log filters limited to the signed-in account', async () => {
  const fetchMock = installEmptyLogs();
  await renderWithProviders(<RoleLogPanel accountId="viewer" role="user" />, {
    station: 'user',
    role: 'user',
    route: '/logs?discord_id=' + discordID,
  });
  expect(screen.queryByLabelText('Discord ID')).toBeNull();
  await waitFor(() =>
    expect(fetchMock.mock.calls.map(([path]) => String(path))).toContain(
      '/api/logs?page=1&page_size=20',
    ),
  );
});
