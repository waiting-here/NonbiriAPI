import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { Route, Routes, useLocation } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { EndpointsPage } from './EndpointsPage';

function fixture(path: string): Record<string, unknown> {
  return JSON.parse(readFileSync(resolve(process.cwd(), '..', path), 'utf8')) as Record<
    string,
    unknown
  >;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function endpoint(id: number): Record<string, unknown> {
  return {
    ...fixture('internal/resources/testdata/endpoint.json'),
    id: String(id),
    base_url: `https://example.com/v1/${id}`,
    note: `endpoint-${id}`,
  };
}

function endpointPage(
  data: readonly Record<string, unknown>[],
  page: string,
  pageSize: number,
  totalItems: number,
): Record<string, unknown> {
  return {
    data,
    next_cursor: null,
    pagination: {
      page,
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(Math.max(1, Math.ceil(totalItems / pageSize))),
    },
  };
}

const session = fixture('internal/auth/testdata/user_envelope.json');

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{`${location.pathname}${location.search}`}</output>;
}

function EndpointRoutes() {
  return (
    <>
      <LocationProbe />
      <Routes>
        <Route path="/endpoints" element={<EndpointsPage />} />
        <Route path="/endpoints/:endpointId" element={<EndpointsPage />} />
      </Routes>
    </>
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:user:endpoints-page-size:v1');
});

describe('user endpoint list page', () => {
  it('requests numbered pages, keeps the old page while busy, and remembers page size', async () => {
    const firstPage = Array.from({ length: 20 }, (_, index) => endpoint(index + 1));
    const secondPage = [endpoint(21)];
    const allEndpoints = Array.from({ length: 21 }, (_, index) => endpoint(index + 1));
    let resolveSecond!: (response: Response) => void;
    const secondResponse = new Promise<Response>((resolve) => {
      resolveSecond = resolve;
    });
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const url = new URL(String(input), window.location.origin);
      const path = `${url.pathname}${url.search}`;
      requests.push(path);
      if (path === '/api/session') return Promise.resolve(jsonResponse(session));
      if (path === '/api/endpoints?page=1&page_size=20') {
        return Promise.resolve(jsonResponse(endpointPage(firstPage, '1', 20, 21)));
      }
      if (path === '/api/endpoints?page=2&page_size=20') return secondResponse;
      if (path === '/api/endpoints?page=1&page_size=50') {
        return Promise.resolve(jsonResponse(endpointPage(allEndpoints, '1', 50, 21)));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<EndpointsPage />, {
      station: 'user',
      role: 'user',
      route: '/endpoints',
    });
    expect(await screen.findByText('endpoint-1')).toBeVisible();
    expect(requests).toContain('/api/endpoints?page=1&page_size=20');
    expect(screen.getByText('Page 1 of 2 · Total: 21')).toBeVisible();
    expect(document.querySelector('section.core-card[aria-busy="false"]')).not.toBeNull();

    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(screen.getByText('endpoint-1')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(screen.getByText('Page 1 of 2 · Total: 21')).toBeVisible();
    expect(document.querySelector('section.core-card[aria-busy="true"]')).not.toBeNull();
    resolveSecond(jsonResponse(endpointPage(secondPage, '2', 20, 21)));
    expect(await screen.findByText('endpoint-21')).toBeVisible();
    expect(screen.getByText('Page 2 of 2 · Total: 21')).toBeVisible();

    fireEvent.change(screen.getByRole('combobox', { name: 'Items per page' }), {
      target: { value: '50' },
    });
    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('50'),
    );
    expect(await screen.findByText('endpoint-20')).toBeVisible();
    expect(requests).toContain('/api/endpoints?page=1&page_size=50');
    expect(window.localStorage.getItem('nonbiri:user:endpoints-page-size:v1')).toBe('50');
  });

  it('keeps pagination controls and the server total for an empty page', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((input: string | URL | Request) => {
        const path = new URL(String(input), window.location.origin).pathname;
        if (path === '/api/session') return Promise.resolve(jsonResponse(session));
        if (path === '/api/endpoints') {
          return Promise.resolve(jsonResponse(endpointPage([], '1', 20, 0)));
        }
        throw new Error(`Unexpected request: ${String(input)}`);
      }),
    );

    await renderWithProviders(<EndpointsPage />, {
      station: 'user',
      role: 'user',
      route: '/endpoints',
    });
    expect(await screen.findByText('No endpoints yet')).toBeVisible();
    expect(screen.getByText('Page 1 of 1 · Total: 0')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Items per page' })).toHaveValue('20');
  });

  it('returns through a real detail route with the clamped page and refreshes from that URL', async () => {
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, window.location.origin);
      const path = `${url.pathname}${url.search}`;
      requests.push(path);
      if (path === '/api/session') return Promise.resolve(jsonResponse(session));
      if (path === '/api/endpoints?page=999&page_size=20') {
        return Promise.resolve(jsonResponse(endpointPage([endpoint(11)], '2', 20, 21)));
      }
      if (path === '/api/endpoints?page=2&page_size=20') {
        return Promise.resolve(jsonResponse(endpointPage([endpoint(11)], '2', 20, 21)));
      }
      if (path === '/api/endpoints/11') {
        return Promise.resolve(
          jsonResponse({ ...fixture('internal/resources/testdata/endpoint.json') }),
        );
      }
      if (path === '/api/endpoints/11/keys?page=1&page_size=20') {
        return Promise.resolve(
          jsonResponse({
            data: [],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
          }),
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<EndpointRoutes />, {
      station: 'user',
      role: 'user',
      route: '/endpoints?page=999&page_size=20',
    });
    expect(await screen.findByText('endpoint-11')).toBeVisible();
    expect(screen.getByText('That page is no longer available. Showing page 2.')).toBeVisible();
    expect(screen.getByTestId('location')).toHaveTextContent('/endpoints?page=999&page_size=20');
    expect(requests).toContain('/api/endpoints?page=999&page_size=20');

    await rendered.user.click(screen.getByRole('link', { name: 'Manage endpoint' }));
    expect(await screen.findByRole('heading', { name: 'Endpoint details' })).toBeVisible();
    expect(screen.getByTestId('location')).toHaveTextContent('/endpoints/11');
    await rendered.user.click(screen.getByRole('link', { name: 'Back' }));
    expect(await screen.findByText('endpoint-11')).toBeVisible();
    expect(screen.getByTestId('location')).toHaveTextContent('/endpoints?page=2&page_size=20');

    rendered.unmount();
    await renderWithProviders(<EndpointRoutes />, {
      station: 'user',
      role: 'user',
      route: '/endpoints?page=2&page_size=20',
    });
    expect(await screen.findByText('endpoint-11')).toBeVisible();
    expect(requests.filter((path) => path === '/api/endpoints?page=2&page_size=20')).toHaveLength(
      2,
    );
  });
});
