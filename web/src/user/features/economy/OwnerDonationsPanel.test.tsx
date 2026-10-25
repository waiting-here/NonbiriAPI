import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
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
  rate_limited: '0',
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
    smart_backoff: null,
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
    await view.user.click(screen.getByRole('button', { name: 'Donation actions' }));
    await view.user.click(screen.getByRole('menuitem', { name: 'View donated keys' }));
    let region = screen.getByRole('region', { name: 'View donated keys' });
    await within(region).findByText(/head1…tail/);
    await view.user.click(within(region).getByRole('button', { name: 'Next' }));
    await within(region).findByText(/head21…tail/);
    expect(within(region).queryByText(/head1…tail/)).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'needle' } });
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(requests).toContain('/api/donations?view=current&q=needle&page=1&page_size=20'),
    );
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
        if (url.pathname === '/api/donations/21/uses') return json(page([], url.searchParams));
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
    await waitFor(() =>
      expect(requests).toContain('7:/api/donations/21/uses?page=1&page_size=100&donation_key_id=1'),
    );
    await act(async () => {
      account = '8';
      login(view.queryClient, account);
    });
    await screen.findByText('Submission 1');
    expect(screen.queryByText(/head1…tail/)).not.toBeInTheDocument();
    expect(screen.getByTestId('location').textContent).not.toContain('donation_expanded');
    expect(screen.getByTestId('location').textContent).not.toContain('donation_q');
    expect(requests.filter((request) => request.startsWith('8:'))).toEqual([
      '8:/api/donations?view=current&page=1&page_size=20',
    ]);
  });

  it('retains rows for a retryable background error and closes them on authentication loss', async () => {
    let status = 200;
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://fixture.example');
        if (status !== 200)
          return json(
            {
              error: {
                code: status === 401 ? 'unauthorized' : 'unavailable',
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
    await view.user.click(screen.getByRole('button', { name: 'Donation actions' }));
    expect(screen.getByRole('menuitem', { name: 'View donated keys' })).toBeDisabled();
    status = 200;
    await view.user.click(screen.getByRole('button', { name: 'Retry' }));
    await view.user.click(screen.getByRole('button', { name: 'Donation actions' }));
    await waitFor(() =>
      expect(screen.getByRole('menuitem', { name: 'View donated keys' })).toBeEnabled(),
    );
    status = 401;
    await view.queryClient.invalidateQueries({ queryKey: economyKeys.donations });
    await screen.findByText('Session closed');
    expect(screen.queryByText('Submission 1')).not.toBeInTheDocument();
    expect(view.queryClient.getQueryData(['user', 'session'])).toBeNull();
  });
  it('defaults to current donations and can hide and restore matching history', async () => {
    let hidden = false;
    const requests: string[] = [];
    const writes: { body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, options) => {
        const url = new URL(String(input), 'https://fixture.example');
        requests.push(url.pathname + url.search);
        if (url.pathname === '/api/donations/history/batch') {
          const body = JSON.parse(String(options?.body));
          writes.push({ body, key: new Headers(options?.headers).get('Idempotency-Key') });
          hidden = body.action === 'hide';
          return json({ changed_count: '1', scanned_count: '1', next_cursor: null });
        }
        if (url.pathname === '/api/donations') {
          const row = {
            ...donation(1),
            status: 'deleted',
            owner_hidden_at: hidden ? 1800000100 : null,
            state_counts: { ...counts, pending: '0', ended: '1' },
          };
          const scope = url.searchParams.get('view');
          return json(
            page(
              scope === 'current' ? [donation(2)] : (scope === 'hidden') === hidden ? [row] : [],
              url.searchParams,
            ),
          );
        }
        throw new Error('Unexpected request: ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(<App />, {
      station: 'user',
      role: 'user',
      route: '/charity?tab=donations',
    });
    login(view.queryClient, '7');
    await screen.findByText('Submission 2');
    expect(requests[0]).toContain('view=current');
    expect(screen.getByRole('tab', { name: 'Current donations' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    await view.user.click(screen.getByRole('tab', { name: 'History' }));
    await screen.findByText('Submission 1');
    await view.user.click(screen.getByRole('button', { name: 'Hide matching history' }));
    await waitFor(() => expect(screen.queryByText('Submission 1')).not.toBeInTheDocument());
    expect(writes[0].body).toEqual({ action: 'hide', status: null, q: '', cursor: null });
    expect(writes[0].key).toMatch(/^[A-Za-z0-9_-]{22}$/);
    await view.user.click(screen.getByRole('tab', { name: 'Hidden' }));
    await screen.findByText('Submission 1');
    await view.user.click(screen.getByRole('button', { name: 'Show matching history again' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1].body).toEqual({ action: 'restore', status: null, q: '', cursor: null });
    expect(writes[0].key).not.toBe(writes[1].key);
  });

  it('opens ended keys by default for a terminal donation', async () => {
    const keyViews: (string | null)[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://fixture.example');
        if (url.pathname === '/api/donations')
          return json(
            page(
              [
                {
                  ...donation(21),
                  status: 'deleted',
                  state_counts: { ...counts, pending: '0', ended: '21' },
                },
              ],
              url.searchParams,
            ),
          );
        if (url.pathname === '/api/donations/21/keys') {
          keyViews.push(url.searchParams.get('view'));
          return json(page([], url.searchParams));
        }
        throw new Error('Unexpected request');
      }),
    );
    const view = await renderWithProviders(<App />, {
      station: 'user',
      role: 'user',
      route: '/charity?tab=donations&donation_view=history&donation_expanded=21',
    });
    login(view.queryClient, '7');
    const region = await screen.findByRole('region', { name: 'View donated keys' });
    await within(region).findByRole('tab', { name: 'Ended', selected: true });
    await waitFor(() => expect(keyViews).toEqual(['ended']));
    await view.user.click(within(region).getByRole('tab', { name: 'All keys' }));
    await waitFor(() => expect(keyViews).toEqual(['ended', 'all']));
  });
  it('does not follow a late batch cursor after the account changes', async () => {
    let finish!: (response: Response) => void;
    let writes = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://fixture.example');
        if (url.pathname === '/api/donations/history/batch') {
          writes++;
          return new Promise<Response>((resolve) => {
            finish = resolve;
          });
        }
        if (url.pathname === '/api/donations')
          return json(
            page(
              [
                {
                  ...donation(1),
                  status: 'deleted',
                  state_counts: { ...counts, pending: '0', ended: '1' },
                },
              ],
              url.searchParams,
            ),
          );
        throw new Error('Unexpected request');
      }),
    );
    const view = await renderWithProviders(<App />, {
      station: 'user',
      role: 'user',
      route: '/charity?tab=donations&donation_view=history',
    });
    login(view.queryClient, '7');
    await screen.findByText('Submission 1');
    await view.user.click(screen.getByRole('button', { name: 'Hide matching history' }));
    await waitFor(() => expect(writes).toBe(1));
    login(view.queryClient, '8');
    await screen.findByRole('tab', { name: 'Current donations', selected: true });
    finish(json({ changed_count: '100', scanned_count: '100', next_cursor: 'old-account-cursor' }));
    await waitFor(() => expect(view.queryClient.isFetching()).toBe(0));
    expect(writes).toBe(1);
    expect(screen.queryByText(/100 changed/)).not.toBeInTheDocument();
  });
});
