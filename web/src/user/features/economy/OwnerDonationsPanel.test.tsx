import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useQuery } from '@tanstack/react-query';
import { Link, Route, Routes, useLocation } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { listReturnPath } from '@shared/operations/listReturn';
import { OwnerDonationsPanel } from './OwnerDonationsPanel';
import { economyKeys } from './queries';

const source = {
  kind: 'custom',
  connector_type: 'openai-compatible',
  base_url: 'https://fixture.example/v1',
};
const counts = {
  available: '0',
  pending: '1',
  disabled: '0',
  suspended: '0',
  exhausted: '0',
  expired: '0',
  ended: '0',
};
function donation(index: number) {
  return {
    id: String(index),
    status: 'pending',
    revision: '1',
    description: index === 21 ? 'needle submission' : `Submission ${index}`,
    review_result: null,
    created_at: 1800000000,
    updated_at: 1800000000,
    key_count: index === 21 ? '21' : '1',
    state_counts: { ...counts, pending: index === 21 ? '21' : '1' },
    source_count: '1',
    sources: [source],
  };
}
function key(index: number) {
  return {
    id: String(index),
    key_id: String(index),
    donation_id: '21',
    donation_revision: '1',
    endpoint_key_id: String(index + 100),
    display_head: `head${index}`,
    display_tail: 'tail',
    safe_source: source,
    physical_enabled: true,
    charity_state: 'pending',
    limits: { price: null, calls: null, tokens: null },
    usage: {
      price_used: '0',
      price_inflight: '0',
      calls_used: '0',
      calls_inflight: '0',
      tokens_used: '0',
      tokens_inflight: '0',
    },
    token_reserve: 0,
    expires_at: null,
    failure_disable_threshold: '10',
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    rule_count: '0',
    rules: [],
  };
}
function page(rows: unknown[], params: URLSearchParams) {
  const size = Number(params.get('page_size'));
  const total = Math.max(1, Math.ceil(rows.length / size));
  const current = Math.min(Number(params.get('page')), total);
  return {
    data: rows.slice((current - 1) * size, current * size),
    next_cursor: null,
    pagination: {
      page: String(current),
      page_size: size,
      total_items: String(rows.length),
      total_pages: String(total),
    },
  };
}
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
function login(client: Parameters<typeof beginManagementSessionRequest>[0], id: string) {
  const value = { user: { id, username: `fixture-${id}`, effective_level: 1 } };
  const generation = beginManagementSessionRequest(client, 'steward');
  expect(noteManagementSessionSuccess(client, 'steward', value, generation)).toBe(true);
  client.setQueryData(['user', 'session'], value);
}
function List() {
  const session = useQuery<{ user: { id: string } } | null>({
    queryKey: ['user', 'session'],
    queryFn: async () => null,
    enabled: false,
  });
  return session.data ? (
    <OwnerDonationsPanel accountID={session.data.user.id} />
  ) : (
    <p>Session closed</p>
  );
}
function Detail() {
  const location = useLocation();
  return <Link to={listReturnPath(location.state, '/charity')}>Return to donations</Link>;
}
function App() {
  const location = useLocation();
  return (
    <>
      <output data-testid="location">
        {location.pathname}
        {location.search}
      </output>
      <Routes>
        <Route path="/charity" element={<List />} />
        <Route path="/charity/donations/:id" element={<Detail />} />
      </Routes>
    </>
  );
}
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('owner donation pages', () => {
  it('keeps independent key pages and list context through filtering and detail return', async () => {
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://fixture.example');
        requests.push(`${url.pathname}${url.search}`);
        expect(url.searchParams.has('page')).toBe(true);
        expect(url.searchParams.has('cursor')).toBe(false);
        if (url.pathname === '/api/donations')
          return json(
            page(
              url.searchParams.get('q')
                ? [donation(21)]
                : Array.from({ length: 21 }, (_, index) => donation(index + 1)),
              url.searchParams,
            ),
          );
        if (url.pathname === '/api/donations/21/keys')
          return json(
            page(
              Array.from({ length: 21 }, (_, index) => key(index + 1)),
              url.searchParams,
            ),
          );
        throw new Error(`Unexpected request: ${url.pathname}`);
      }),
    );
    const view = await renderWithProviders(<App />, {
      station: 'user',
      role: 'user',
      route: '/charity?tab=donations',
    });
    login(view.queryClient, '7');
    await screen.findByText('Submission 1');
    expect(requests.filter((path) => path.includes('/keys'))).toHaveLength(0);
    await view.user.click(screen.getByRole('button', { name: 'Next' }));
    await screen.findByText('needle submission');
    expect(screen.queryByText('Submission 1')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'View donated keys' }));
    let region = screen.getByRole('region', { name: 'View donated keys' });
    await within(region).findByText(/head1…tail/);
    await view.user.click(within(region).getByRole('button', { name: 'Next' }));
    await within(region).findByText(/head21…tail/);
    expect(within(region).queryByText(/head1…tail/)).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'needle' } });
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => expect(requests).toContain('/api/donations?q=needle&page=1&page_size=20'));
    expect(screen.getByTestId('location').textContent).toContain('donation_keys_page=2');
    await view.user.click(await screen.findByRole('link', { name: 'Open details' }));
    await view.user.click(screen.getByRole('link', { name: 'Return to donations' }));
    region = await screen.findByRole('region', { name: 'View donated keys' });
    await within(region).findByText(/head21…tail/);
    expect(screen.getByTestId('location').textContent).toContain('donation_q=needle');
    expect(requests.every((path) => !path.includes('limit='))).toBe(true);
  });

  it('clears the previous account selection and list context before reading for the next account', async () => {
    let account = '7';
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://fixture.example');
        requests.push(`${account}:${url.pathname}${url.search}`);
        if (url.pathname === '/api/donations')
          return json(page([donation(account === '7' ? 21 : 1)], url.searchParams));
        if (url.pathname === '/api/donations/21/keys')
          return json(page([key(1)], url.searchParams));
        throw new Error(`Unexpected request: ${url.pathname}`);
      }),
    );
    const view = await renderWithProviders(<App />, {
      station: 'user',
      role: 'user',
      route: '/charity?tab=donations&donation_q=needle&donation_expanded=21',
    });
    login(view.queryClient, account);
    await screen.findByText(/head1…tail/);
    account = '8';
    login(view.queryClient, account);
    await screen.findByText('Submission 1');
    expect(screen.queryByText(/head1…tail/)).not.toBeInTheDocument();
    expect(screen.getByTestId('location').textContent).not.toContain('donation_expanded');
    expect(screen.getByTestId('location').textContent).not.toContain('donation_q');
    expect(requests.filter((request) => request.startsWith('8:'))).toEqual([
      '8:/api/donations?page=1&page_size=20',
    ]);
  });

  it('retains rows for a retryable background error and closes them on current permission loss', async () => {
    let status = 200;
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://fixture.example');
        if (status !== 200)
          return json(
            {
              error: {
                code: status === 403 ? 'forbidden' : 'unavailable',
                message: 'controlled read failure',
              },
            },
            status,
          );
        return json(page([donation(1)], url.searchParams));
      }),
    );
    const view = await renderWithProviders(<App />, {
      station: 'user',
      role: 'user',
      route: '/charity?tab=donations',
    });
    login(view.queryClient, '7');
    await screen.findByText('Submission 1');
    status = 503;
    await view.queryClient.invalidateQueries({ queryKey: economyKeys.donations });
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible();
    expect(screen.getByText('Submission 1')).toBeVisible();
    expect(screen.getByRole('button', { name: 'View donated keys' })).toBeDisabled();
    status = 200;
    await view.user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'View donated keys' })).toBeEnabled(),
    );
    status = 403;
    await view.queryClient.invalidateQueries({ queryKey: economyKeys.donations });
    await screen.findByText('Session closed');
    expect(screen.queryByText('Submission 1')).not.toBeInTheDocument();
    expect(view.queryClient.getQueryData(['user', 'session'])).toBeNull();
  });
});
