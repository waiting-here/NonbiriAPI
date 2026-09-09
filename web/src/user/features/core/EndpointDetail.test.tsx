import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor, within } from '@testing-library/react';
import { type ReactNode, useEffect, useRef } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { EndpointDetail } from './EndpointDetail';
import * as coreQueries from './queries';

vi.mock('./queries', async () => {
  const actual = await vi.importActual<typeof import('./queries')>('./queries');
  return {
    ...actual,
    useEndpoint: vi.fn(),
  };
});

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

function catalogPage(
  manualEntries: readonly unknown[],
  page = '1',
  pageSize = 20,
  totalItems = manualEntries.length,
  state: 'unknown' | 'checking' = 'unknown',
) {
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));
  return {
    evidence: {
      state,
      revision: '1',
      result: null,
      safe_class: 'none',
      observed_at: state === 'checking' ? 1_700_000_000 : null,
      count: null,
    },
    automatic_entries: [],
    manual_entries: manualEntries,
    next_cursor: null,
    pagination: {
      page,
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(totalPages),
    },
  };
}

function StateSeeder() {
  const location = useLocation();
  const navigate = useNavigate();
  const seeded = useRef(false);
  useEffect(() => {
    if (seeded.current) return;
    seeded.current = true;
    navigate(`${location.pathname}${location.search}`, {
      replace: true,
      state: { returnTo: '/endpoints?page=3&page_size=50' },
    });
  }, [location.pathname, location.search, navigate]);
  return null;
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{`${location.pathname}${location.search}`}</output>;
}

function Wrapper({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

const endpoint = fixture('internal/resources/testdata/endpoint.json');
const endpointKey = {
  ...fixture('internal/resources/testdata/endpoint_key.json'),
  browse: {
    model_count: '0',
    binding_count: '0',
    donation_eligibility: 'eligible',
    available_binding_count: '0',
    preview: [],
    discovery: {
      state: 'succeeded',
      revision: '1',
      result: 'empty',
      safe_class: 'none',
      observed_at: 1_700_000_000,
      count: '0',
    },
  },
};
const endpointKeyTwo = { ...endpointKey, id: '22', note: 'second key note' };
const session = fixture('internal/auth/testdata/user_envelope.json');
const manualEntry = {
  id: '41',
  source_type: 'manual',
  upstream_model_id: 'Vendor/Manual',
  provider: 'Vendor',
  source_revision: '1',
  pair_revision: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_000,
};
const manualEntryTwo = { ...manualEntry, id: '42', upstream_model_id: 'Vendor/Second' };
const manualEntriesPageOne = Array.from({ length: 20 }, (_, index) => ({
  ...manualEntry,
  id: String(41 + index),
  upstream_model_id: `Vendor/Manual-${index}`,
}));

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:user:endpoint-keys-page-size:v1');
  window.localStorage.removeItem('nonbiri:user:manual-catalog-page-size:v1');
});

beforeEach(() => {
  vi.mocked(coreQueries.useEndpoint).mockReturnValue({
    data: endpoint,
    isPending: false,
    error: null,
    refetch: vi.fn().mockResolvedValue({ data: endpoint, error: null }),
  } as unknown as ReturnType<typeof coreQueries.useEndpoint>);
});

describe('endpoint detail numbered resource panels', () => {
  it('requests the key page and manual catalog page separately and carries list context back', async () => {
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, window.location.origin);
      const path = `${url.pathname}${url.search}`;
      if (path === '/api/endpoints/11/keys?page=1&page_size=20') {
        return Promise.resolve(
          jsonResponse({
            data: [endpointKey],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
          }),
        );
      }
      if (path === '/api/endpoints/11/keys/21/models?page=1&page_size=20&source=manual') {
        return Promise.resolve(jsonResponse(catalogPage([manualEntry])));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(
      <Wrapper>
        <StateSeeder />
        <LocationProbe />
        <EndpointDetail accountId="1" endpointId="11" />
      </Wrapper>,
      { station: 'user', role: 'user', route: '/endpoints/11' },
    );

    expect(await screen.findByText('key note')).toBeVisible();
    await rendered.user.click(screen.getByText('Manual catalog', { selector: 'summary' }));
    expect(await screen.findByText('Vendor/Manual')).toBeVisible();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/api/endpoints/11/keys?page=1&page_size=20',
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/api/endpoints/11/keys/21/models?page=1&page_size=20&source=manual',
    );
    expect(screen.getAllByRole('combobox', { name: 'Items per page' })).toHaveLength(2);
    expect(screen.getByRole('heading', { name: 'Key' }).closest('section')).toHaveAttribute(
      'aria-busy',
      'false',
    );
    expect(screen.getByText('Vendor/Manual').closest('section')).toHaveAttribute(
      'aria-busy',
      'false',
    );

    await rendered.user.click(screen.getByRole('link', { name: 'Back' }));
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/endpoints?page=3&page_size=50'),
    );
  });

  it('lazily isolates two manual catalogs and preserves each URL page', async () => {
    const requests: string[] = [];
    const fetchMock = vi.fn((input: string | URL | Request) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, window.location.origin);
      const path = `${url.pathname}${url.search}`;
      requests.push(path);
      if (path === '/api/endpoints/11/keys?page=1&page_size=20') {
        return Promise.resolve(
          jsonResponse({
            data: [endpointKey, endpointKeyTwo],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '2', total_pages: '1' },
          }),
        );
      }
      if (path === '/api/endpoints/11/keys/21/models?page=1&page_size=20&source=manual') {
        return Promise.resolve(
          jsonResponse(catalogPage(manualEntriesPageOne, '1', 20, 21, 'checking')),
        );
      }
      if (path === '/api/endpoints/11/keys/21/models?page=2&page_size=20&source=manual') {
        return Promise.resolve(jsonResponse(catalogPage([manualEntry], '2', 20, 21, 'checking')));
      }
      if (path === '/api/endpoints/11/keys/22/models?page=3&page_size=100&source=manual') {
        return Promise.resolve(
          jsonResponse(catalogPage([manualEntryTwo], '3', 100, 201, 'checking')),
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(
      <>
        <LocationProbe />
        <EndpointDetail accountId="1" endpointId="11" />
      </>,
      {
        station: 'user',
        role: 'user',
        route:
          '/endpoints/11?keys_page=1&keys_page_size=20&manual_21_page=1&manual_21_page_size=20&manual_22_page=3&manual_22_page_size=100',
      },
    );

    expect(await screen.findByText('key note')).toBeVisible();
    expect(await screen.findByText('second key note')).toBeVisible();
    expect(requests).toEqual(['/api/endpoints/11/keys?page=1&page_size=20']);

    const summaries = screen.getAllByText('Manual catalog', { selector: 'summary' });
    expect(summaries).toHaveLength(2);
    await rendered.user.click(summaries[0]);
    expect(await screen.findByText('Vendor/Manual-0')).toBeVisible();
    expect(requests).toContain(
      '/api/endpoints/11/keys/21/models?page=1&page_size=20&source=manual',
    );
    expect(requests).not.toContain(
      '/api/endpoints/11/keys/22/models?page=3&page_size=100&source=manual',
    );

    const manual21Next = within(
      screen.getAllByRole('navigation', { name: 'Pagination' })[0],
    ).getByRole('button', { name: 'Next' });
    expect(manual21Next).toBeEnabled();
    await rendered.user.click(manual21Next);
    expect(await screen.findByText('Page 2 of 2 · Total: 21')).toBeVisible();
    let search = new URL(`https://example.test${screen.getByTestId('location').textContent ?? ''}`)
      .searchParams;
    expect(search.get('manual_21_page')).toBe('2');
    expect(search.get('manual_21_page_size')).toBe('20');
    expect(search.get('manual_22_page')).toBe('3');
    expect(search.get('manual_22_page_size')).toBe('100');

    const requestsBeforeCollapse = requests.filter((path) => path.includes('/keys/21/models'));
    await rendered.user.click(summaries[0]);
    await new Promise((resolve) => setTimeout(resolve, 1_100));
    expect(requests.filter((path) => path.includes('/keys/21/models'))).toEqual(
      requestsBeforeCollapse,
    );

    await rendered.user.click(summaries[1]);
    expect(await screen.findByText('Vendor/Second')).toBeVisible();
    expect(requests).toContain(
      '/api/endpoints/11/keys/22/models?page=3&page_size=100&source=manual',
    );
    search = new URL(`https://example.test${screen.getByTestId('location').textContent ?? ''}`)
      .searchParams;
    expect(search.get('manual_21_page')).toBe('2');
    expect(search.get('manual_22_page')).toBe('3');
  });

  it('returns to the validated list context after an endpoint delete', async () => {
    const requests: Array<{ path: string; method: string; body: string | undefined }> = [];
    const fetchMock = vi.fn((input: string | URL | Request, init?: RequestInit) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, window.location.origin);
      const path = `${url.pathname}${url.search}`;
      requests.push({ path, method: init?.method ?? 'GET', body: init?.body?.toString() });
      if (path === '/api/endpoints/11/keys?page=1&page_size=20') {
        return Promise.resolve(
          jsonResponse({
            data: [],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
          }),
        );
      }
      if (path === '/api/endpoints/11' && init?.method === 'DELETE') {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      throw new Error(`Unexpected request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(
      <>
        <StateSeeder />
        <LocationProbe />
        <EndpointDetail accountId="1" endpointId="11" />
      </>,
      { station: 'user', role: 'user', route: '/endpoints/11' },
    );
    rendered.queryClient.setQueryData(coreQueries.coreKeys.session, session);

    expect(await screen.findByRole('heading', { name: 'Endpoint details' })).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Delete endpoint' }));
    const dialog = screen.getByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Delete endpoint' }));
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/endpoints?page=3&page_size=50'),
    );
    const deletion = requests.find(
      (request) => request.path === '/api/endpoints/11' && request.method === 'DELETE',
    );
    expect(deletion).toBeDefined();
    expect(JSON.parse(deletion?.body ?? '{}')).toMatchObject({ expected_revision: '3' });
  });
});
