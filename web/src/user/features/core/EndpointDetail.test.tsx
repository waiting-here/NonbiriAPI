import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor, within } from '@testing-library/react';
import { type ReactNode, useEffect, useRef } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
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

const modal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const close = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close');
beforeAll(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value() {
      this.setAttribute('open', '');
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value() {
      this.removeAttribute('open');
    },
  });
});
afterAll(() => {
  if (modal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', modal);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  if (close) Object.defineProperty(HTMLDialogElement.prototype, 'close', close);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'close');
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
      if (url.searchParams.get('source') === 'automatic')
        return Promise.resolve(jsonResponse(catalogPage([])));
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
    await rendered.user.click(screen.getByRole('button', { name: 'View models' }));
    await rendered.user.click(screen.getByText('Manual models', { selector: 'strong' }));
    expect(await screen.findByText('Vendor/Manual')).toBeVisible();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/api/endpoints/11/keys?page=1&page_size=20',
    );
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain(
      '/api/endpoints/11/keys/21/models?page=1&page_size=20&source=manual',
    );
    expect(screen.queryByRole('combobox', { name: 'Per page' })).not.toBeInTheDocument();
    expect(screen.getByText('Vendor/Manual').closest('section')).toHaveAttribute(
      'aria-busy',
      'false',
    );

    await rendered.user.click(
      within(screen.getByRole('region', { name: /^View models/ })).getByRole('button', {
        name: 'Close',
      }),
    );
    expect(screen.getByRole('heading', { name: 'Key' }).closest('section')).toHaveAttribute(
      'aria-busy',
      'false',
    );
    await rendered.user.click(screen.getByRole('link', { name: /My services/ }));
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
      if (path === '/api/endpoints/11/request-adaptation') {
        return Promise.resolve(
          jsonResponse({
            revision: '0',
            forward_headers: { mode: 'replace', values: [] },
            fixed_headers: { mode: 'replace', values: {} },
            body_defaults: { mode: 'replace', values: {} },
            body_forced: { mode: 'replace', values: {} },
            native_extension_paths: { mode: 'replace', values: [] },
          }),
        );
      }
      if (url.searchParams.get('source') === 'automatic')
        return Promise.resolve(jsonResponse(catalogPage([])));
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

    await screen.findByRole('combobox', { name: 'Current key' });
    expect(requests).not.toContain(
      '/api/endpoints/11/keys/22/models?page=3&page_size=100&source=manual',
    );
    await rendered.user.click(screen.getByText('Manual models', { selector: 'strong' }));
    const first = await screen.findByText('Vendor/Manual-0');
    const panel = first.closest('section')!;
    await rendered.user.click(within(panel).getByRole('button', { name: 'Next' }));
    await screen.findByText('Vendor/Manual');
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('manual_21_page=2'),
    );
    await rendered.user.selectOptions(screen.getByRole('combobox', { name: 'Current key' }), '22');
    await rendered.user.click(screen.getByText('Manual models', { selector: 'strong' }));
    expect(await screen.findByText('Vendor/Second')).toBeVisible();
    expect(requests).toContain(
      '/api/endpoints/11/keys/22/models?page=3&page_size=100&source=manual',
    );
    const search = new URL('https://example.test' + screen.getByTestId('location').textContent)
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
      if (url.searchParams.get('source') === 'automatic')
        return Promise.resolve(jsonResponse(catalogPage([])));
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

    expect(await screen.findByRole('heading', { name: 'endpoint note' })).toBeVisible();
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

function keyPage(key: object) {
  return {
    data: [key],
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
  };
}
async function renderKeyFeedback(fetchMock: typeof fetch, route = '/endpoints/11') {
  vi.stubGlobal('fetch', vi.fn(fetchMock));
  const view = await renderWithProviders(<EndpointDetail accountId="1" endpointId="11" />, {
    station: 'user',
    role: 'user',
    route,
  });
  view.queryClient.setQueryData(['user', 'session'], session);
  await screen.findAllByText('key note');
  return view;
}
describe('key model operation feedback', () => {
  it.each([400, 429])(
    'preserves a concrete HTTP %s failure in a full-width row',
    async (status) => {
      const message = 'Choose the catalog for the same credential version before continuing.';
      const view = await renderKeyFeedback(async (input, init) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname === '/api/endpoints/11/keys') return jsonResponse(keyPage(endpointKey));
        if (url.pathname.endsWith('/models')) return jsonResponse(catalogPage([]));
        if (url.pathname.endsWith('/models/refresh') && init?.method === 'POST')
          return jsonResponse(
            { error: { code: status === 429 ? 'rate_limited' : 'invalid_request', message } },
            status,
          );
        throw Error('Unexpected ' + url.pathname);
      });
      await view.user.click(screen.getByRole('button', { name: 'Load available models' }));
      const failure = await screen.findByRole('alert');
      expect(failure).toHaveTextContent(message);
      expect(failure.closest('td')).toHaveAttribute('colspan', '6');
      expect(failure.closest('tr')).toHaveClass('core-key-feedback');
      expect(failure.closest('td')).not.toHaveAttribute('data-cell', 'action');
    },
  );
  it('keeps an uncertain refresh and its recovery controls outside the action cell without resending', async () => {
    let writes = 0;
    const view = await renderKeyFeedback(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoints/11/keys') return jsonResponse(keyPage(endpointKey));
      if (url.pathname.endsWith('/models/refresh') && init?.method === 'POST') {
        writes += 1;
        throw new TypeError('Response lost');
      }
      if (url.pathname === '/api/resource-operation-status')
        return jsonResponse({ status: 'not_recorded' });
      throw Error('Unexpected ' + url.pathname);
    });
    await view.user.click(screen.getByRole('button', { name: 'Load available models' }));
    const unknown = await screen.findByText("Couldn't confirm the save");
    const feedback = unknown.closest('tr')!;
    expect(feedback).toHaveClass('core-key-feedback');
    expect(feedback.querySelector('td')).toHaveAttribute('colspan', '6');
    await waitFor(() =>
      expect(
        within(feedback).getAllByRole('button', { name: 'Check again' }).length,
      ).toBeGreaterThan(0),
    );
    expect(writes).toBe(1);
  });
  it('preserves a manual import draft and shows the actual rejection before a corrected save', async () => {
    let imported = false;
    let writes = 0;
    const view = await renderKeyFeedback(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoints/11/keys') return jsonResponse(keyPage(endpointKey));
      if (url.pathname === '/api/endpoints/11/keys/21/models/manual' && init?.method === 'POST') {
        writes += 1;
        if (writes === 1)
          return jsonResponse(
            {
              error: {
                code: 'invalid_request',
                message: 'The model identifier is unavailable. Choose another identifier.',
              },
            },
            400,
          );
        imported = true;
        return jsonResponse({ entries: [manualEntry] }, 201);
      }
      if (url.pathname === '/api/endpoints/11/keys/21/models')
        return jsonResponse(
          catalogPage(imported && url.searchParams.get('source') === 'manual' ? [manualEntry] : []),
        );
      throw Error('Unexpected ' + url.pathname);
    });
    await view.user.click(screen.getByRole('button', { name: 'View models' }));
    await view.user.click(screen.getByText('Manual models', { selector: 'strong' }));
    const input = await screen.findByRole('textbox', { name: 'Provider model name' });
    await view.user.type(input, 'Vendor/Manual');
    await view.user.type(screen.getByRole('textbox', { name: 'Note (optional)' }), 'Vendor');
    await view.user.click(screen.getByRole('button', { name: 'Add manual entry' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The model identifier is unavailable. Choose another identifier.',
    );
    expect(input).toHaveValue('Vendor/Manual');
    await view.user.click(screen.getByRole('button', { name: 'Add manual entry' }));
    await waitFor(() => expect(input).toHaveValue(''));
    expect(await screen.findByText('Vendor/Manual')).toBeVisible();
    expect(writes).toBe(2);
  });
});

const catalogSource = {
  id: '17',
  connection_id: '9',
  name: 'Exact catalog',
  base_url: 'https://catalog.example',
  models_url: 'https://catalog.example/models',
  list_format: 'openai',
  credential_id: '7',
  credential_version: '2',
  revision: '1',
  discovery_source_revision: '1',
  enabled: true,
  credential_available: true,
  state: 'succeeded',
  checked_at: 1,
  safe_class: 'none',
  safe_diag: '',
  created_at: 1,
  updated_at: 1,
};
describe('per-pair list settings', () => {
  it('opens the current key list settings without a credential or discovery-source chooser', async () => {
    const view = await renderKeyFeedback(async (input) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoints/11/keys')
        return jsonResponse(keyPage({ ...endpointKey, discovery_source_id: '51' }));
      if (url.pathname.endsWith('/models')) return jsonResponse(catalogPage([]));
      if (url.pathname === '/api/discovery-sources/51') return jsonResponse(catalogSource);
      throw Error('Unexpected ' + url.pathname);
    });
    await view.user.click(screen.getByRole('button', { name: 'More actions · key note' }));
    await view.user.click(screen.getByRole('menuitem', { name: 'Edit note and limits' }));
    await view.user.click(screen.getByText('Model list settings', { selector: 'strong' }));
    expect(await screen.findByLabelText('Model list base URL')).toHaveValue(catalogSource.base_url);
    expect(screen.queryByRole('table', { name: 'Choose a service key' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Discovery sources' })).not.toBeInTheDocument();
  });
});
