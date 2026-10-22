import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { Route, Routes, useLocation } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { EndpointsPage } from './EndpointsPage';
const session = JSON.parse(
  readFileSync(resolve(process.cwd(), '../internal/auth/testdata/user_envelope.json'), 'utf8'),
);
const response = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
const connection = (id: number) => ({
  id: String(id),
  name: 'connection-' + id,
  note: '',
  revision: '1',
  entry_count: '3',
  credential_count: '1',
  created_at: 1700000000,
  updated_at: 1700000000,
});
const page = (data: unknown[], number = '1', size = 20, total = data.length) => ({
  data,
  next_cursor: null,
  pagination: {
    page: number,
    page_size: size,
    total_items: String(total),
    total_pages: String(Math.max(1, Math.ceil(total / size))),
  },
});
function RoutesWithLocation() {
  const location = useLocation();
  return (
    <>
      <output data-testid="location">{location.pathname + location.search}</output>
      <Routes>
        <Route path="/endpoints" element={<EndpointsPage />} />
        <Route path="/endpoints/:endpointId" element={<EndpointsPage />} />
      </Routes>
    </>
  );
}
afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:user:connections-page-size:v1');
});
describe('connection entry list', () => {
  it('refreshes authoritative connection counts without probing each entry', async () => {
    let entries = '3';
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL | Request) => {
        const path = new URL(String(input), window.location.origin).pathname;
        requests.push(path);
        if (path === '/api/session') return Promise.resolve(response(session));
        if (path === '/api/connections')
          return Promise.resolve(response(page([{ ...connection(1), entry_count: entries }])));
        throw Error(path);
      }),
    );
    const rendered = await renderWithProviders(<EndpointsPage />, {
      station: 'user',
      role: 'user',
      route: '/endpoints',
    });
    const link = await screen.findByRole('link', { name: 'connection-1' });
    expect(within(link.closest('tr')!).getByText('3')).toBeVisible();
    entries = '4';
    await rendered.queryClient.invalidateQueries();
    await waitFor(() => expect(within(link.closest('tr')!).getByText('4')).toBeVisible());
    expect(requests.every((path) => path === '/api/session' || path === '/api/connections')).toBe(
      true,
    );
  });
  it('keeps the current page during a numbered-page request and remembers page size', async () => {
    let resolveSecond!: (response: Response) => void;
    const second = new Promise<Response>((resolve) => {
      resolveSecond = resolve;
    });
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL | Request) => {
        const url = new URL(String(input), window.location.origin);
        requests.push(url.pathname + url.search);
        if (url.pathname === '/api/session') return Promise.resolve(response(session));
        if (url.pathname === '/api/connections') {
          if (url.searchParams.get('page') === '2') return second;
          const size = Number(url.searchParams.get('page_size'));
          return Promise.resolve(
            response(
              page(
                Array.from({ length: size === 50 ? 21 : 20 }, (_, i) => connection(i + 1)),
                '1',
                size,
                21,
              ),
            ),
          );
        }
        throw Error(url.pathname);
      }),
    );
    const rendered = await renderWithProviders(<EndpointsPage />, {
      station: 'user',
      role: 'user',
      route: '/endpoints',
    });
    expect(await screen.findByText('connection-1')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(screen.getByText('connection-1')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    resolveSecond(response(page([connection(21)], '2', 20, 21)));
    expect(await screen.findByText('connection-21')).toBeVisible();
    fireEvent.change(screen.getByRole('combobox', { name: 'Items per page' }), {
      target: { value: '50' },
    });
    expect(await screen.findByText('connection-20')).toBeVisible();
    expect(requests).toContain('/api/connections?page=1&page_size=50');
    expect(window.localStorage.getItem('nonbiri:user:connections-page-size:v1')).toBe('50');
  });
  it('provides creation on an empty list without pagination', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL | Request) => {
        const path = new URL(String(input), window.location.origin).pathname;
        if (path === '/api/session') return Promise.resolve(response(session));
        if (path === '/api/connections') return Promise.resolve(response(page([])));
        throw Error(path);
      }),
    );
    await renderWithProviders(<EndpointsPage />, {
      station: 'user',
      role: 'user',
      route: '/endpoints',
    });
    expect(await screen.findByText('Nothing here yet')).toBeVisible();
    expect(screen.queryByRole('navigation', { name: 'Pagination' })).not.toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Add connection' })).toHaveLength(2);
  });
  it('returns to the same connection search and page from a real detail route', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL | Request) => {
        const url = new URL(String(input), window.location.origin);
        if (url.pathname === '/api/session') return Promise.resolve(response(session));
        if (url.pathname === '/api/connections')
          return Promise.resolve(response(page([connection(21)], '2', 20, 21)));
        if (url.pathname === '/api/connections/21')
          return Promise.resolve(response(connection(21)));
        if (url.pathname === '/api/connections/21/entries')
          return Promise.resolve(response(page([])));
        throw Error(url.pathname);
      }),
    );
    const route = '/endpoints?connections_q=shared&connections_page=2&connections_page_size=20';
    const rendered = await renderWithProviders(<RoutesWithLocation />, {
      station: 'user',
      role: 'user',
      route,
    });
    await rendered.user.click(await screen.findByRole('link', { name: 'connection-21' }));
    expect(await screen.findByRole('heading', { name: 'connection-21' })).toBeVisible();
    await rendered.user.click(screen.getByRole('link', { name: 'Back to connections' }));
    expect(await screen.findByText('connection-21')).toBeVisible();
    expect(screen.getByTestId('location')).toHaveTextContent(route);
    expect(screen.getByRole('searchbox')).toHaveValue('shared');
  });
});
