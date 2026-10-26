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

    expect(await screen.findByText('key note')).toBeVisible();
    expect(await screen.findByText('second key note')).toBeVisible();
    expect(requests).toContain('/api/endpoints/11/keys?page=1&page_size=20');
    expect(requests).toEqual(['/api/endpoints/11/keys?page=1&page_size=20']);
    const adaptation = screen.getByText('Request rewriting (advanced)', { selector: 'strong' });
    await rendered.user.click(adaptation);
    await screen.findByRole('button', { name: 'Save request rewriting' });
    expect(requests.filter((path) => path.endsWith('/request-adaptation'))).toHaveLength(1);
    await rendered.user.click(adaptation);
    await rendered.user.click(adaptation);
    expect(requests.filter((path) => path.endsWith('/request-adaptation'))).toHaveLength(1);

    const summaries = screen.getAllByRole('button', { name: 'View models' });
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
    expect(await screen.findByText('Page 2 of 2')).toBeVisible();
    let search = new URL(`https://example.test${screen.getByTestId('location').textContent ?? ''}`)
      .searchParams;
    expect(search.get('manual_21_page')).toBe('2');
    expect(search.get('manual_21_page_size')).toBe('20');
    expect(search.get('manual_22_page')).toBe('3');
    expect(search.get('manual_22_page_size')).toBe('100');

    const requestsBeforeCollapse = requests.filter((path) => path.includes('/keys/21/models'));
    await rendered.user.click(
      within(screen.getByRole('region', { name: /^View models/ })).getByRole('button', {
        name: 'Close',
      }),
    );
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
    await rendered.user.click(screen.getByRole('button', { name: 'Delete service' }));
    const dialog = screen.getByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Delete service' }));
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
describe('missing catalog recovery', () => {
  it('selects and saves the exact catalog in the real key editor, then reads its available models', async () => {
    let current: Record<string, unknown> & { discovery_source_id: string | null } = {
      ...endpointKey,
      discovery_source_id: null,
    };
    const writes: { path: string; body?: Record<string, unknown> }[] = [];
    let catalogReads = 0;
    const view = await renderKeyFeedback(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoints/11/keys') return jsonResponse(keyPage(current));
      if (url.pathname === '/api/endpoint-create-options')
        return jsonResponse({
          base_connector_types: ['openai-compatible'],
          mainstream_channels: [],
          native_service_options: [
            {
              connector_type: 'openai-compatible',
              configuration_type: 'openai-compatible',
              native_services: ['chat_completions', 'responses'],
              default_native_services: ['chat_completions'],
              rerank_formats: [],
            },
          ],
        });
      if (url.pathname === '/api/endpoints/11/keys/21' && (init?.method ?? 'GET') === 'GET')
        return jsonResponse(current);
      if (url.pathname === '/api/connections/9/discovery-sources') {
        catalogReads++;
        return jsonResponse({ ...keyPage(catalogSource) });
      }
      if (url.pathname === '/api/endpoints/11/keys/21' && init?.method === 'PATCH') {
        const body = JSON.parse(String(init.body));
        writes.push({ path: url.pathname, body });
        current = {
          ...current,
          discovery_source_id: body.discovery_source_id,
          revision: '5',
          grant_revision: '4',
        };
        return jsonResponse(current);
      }
      if (url.pathname.endsWith('/models/refresh') && init?.method === 'POST') {
        writes.push({ path: url.pathname });
        expect(current.discovery_source_id).toBe('17');
        return jsonResponse(
          {
            operation_id: 'op_AAAAAAAAAAAAAAAAAAAAAA',
            evidence: catalogPage([], '1', 20, 0, 'checking').evidence,
          },
          202,
        );
      }
      if (url.pathname === '/api/endpoints/11/keys/21/models') {
        const automatic = url.searchParams.get('source') === 'automatic';
        return jsonResponse({
          ...catalogPage([]),
          evidence: {
            state: 'succeeded',
            revision: '2',
            result: automatic ? 'nonempty' : 'empty',
            safe_class: 'none',
            observed_at: 1700000020,
            count: automatic ? '1' : '0',
          },
          automatic_entries: automatic
            ? [
                {
                  ...manualEntry,
                  id: '31',
                  source_type: 'automatic',
                  upstream_model_id: 'Vendor/Found',
                },
              ]
            : [],
          pagination: {
            page: '1',
            page_size: 20,
            total_items: automatic ? '1' : '0',
            total_pages: '1',
          },
        });
      }
      throw Error('Unexpected ' + url.pathname);
    });
    expect(screen.queryByRole('button', { name: 'Load available models' })).toBeNull();
    expect(catalogReads).toBe(0);
    await view.user.click(screen.getByRole('button', { name: 'Choose model catalog' }));
    expect(writes).toHaveLength(0);
    const scope = screen
      .getByText('Credential and service authorization', { selector: 'strong' })
      .closest('details')!;
    expect(scope).toHaveAttribute('open');
    const catalog = within(scope)
      .getByText('Catalog association (optional)', { selector: 'strong' })
      .closest('details')!;
    expect(catalog).toHaveAttribute('open');
    expect(within(catalog).getByRole('link', { name: 'Discovery sources' })).toHaveAttribute(
      'href',
      '/endpoints?connection_id=9&connection_view=discovery',
    );
    await view.user.click(await within(scope).findByRole('button', { name: 'Use source' }));
    expect(writes).toHaveLength(0);
    const keyEditor = screen.getByRole('region', { name: 'Edit note and limits' });
    await view.user.click(within(keyEditor).getByRole('button', { name: 'Close' }));
    await view.user.click(screen.getByRole('button', { name: 'Choose model catalog' }));
    expect(catalogReads).toBe(1);
    expect(within(catalog).getByText('Exact catalog', { selector: 'p' })).toBeVisible();
    const note = screen.getByRole('textbox', { name: 'Key note' });
    await view.user.clear(note);
    await view.user.type(note, 'Keep this note draft');
    await view.user.click(
      within(catalog).getByText('Catalog association (optional)', { selector: 'strong' }),
    );
    await view.user.click(
      within(scope).getByText('Credential and service authorization', { selector: 'strong' }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Choose model catalog' }));
    expect(scope).toHaveAttribute('open');
    expect(catalog).toHaveAttribute('open');
    expect(note).toHaveValue('Keep this note draft');
    expect(within(catalog).getByText('Exact catalog', { selector: 'p' })).toBeVisible();
    expect(writes).toHaveLength(0);
    await view.user.click(within(scope).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({
      path: '/api/endpoints/11/keys/21',
      body: {
        native_services: ['chat_completions', 'responses'],
        discovery_source_id: '17',
        expected_revision: '4',
      },
    });
    const editor = screen.getByRole('region', { name: 'Edit note and limits' });
    await view.user.click(within(editor).getByRole('button', { name: 'Close' }));
    await view.user.click(await screen.findByRole('button', { name: 'Load available models' }));
    expect(await screen.findByText('Vendor/Found')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Add manual entry' })).toBeVisible();
    expect(writes.map((item) => item.path)).toEqual([
      '/api/endpoints/11/keys/21',
      '/api/endpoints/11/keys/21/models/refresh',
    ]);
  });
  it('opens grouping from a focused ungrouped key and saves a new connection only after explicit confirmation', async () => {
    let currentEndpoint = {
      ...endpoint,
      revision: String(endpoint.revision),
      connection_id: null as string | null,
    };
    let connection: object | null = null;
    const writes: { path: string; body: Record<string, unknown> }[] = [];
    vi.mocked(coreQueries.useEndpoint).mockReturnValue({
      data: currentEndpoint,
      isPending: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof coreQueries.useEndpoint>);
    const view = await renderKeyFeedback(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoints/11/keys')
        return jsonResponse(keyPage({ ...endpointKey, discovery_source_id: null }));
      if (url.pathname === '/api/endpoint-create-options')
        return jsonResponse({
          base_connector_types: ['openai-compatible'],
          mainstream_channels: [],
          native_service_options: [
            {
              connector_type: 'openai-compatible',
              configuration_type: 'openai-compatible',
              native_services: ['chat_completions', 'responses'],
              default_native_services: ['chat_completions'],
              rerank_formats: [],
            },
          ],
        });
      if (url.pathname === '/api/connections' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        writes.push({ path: url.pathname, body });
        connection = {
          id: '9',
          name: body.name,
          note: body.note,
          revision: '1',
          entry_count: '0',
          credential_count: '0',
          created_at: 1,
          updated_at: 1,
        };
        return jsonResponse(connection, 201);
      }
      if (url.pathname === '/api/connections')
        return jsonResponse({
          data: connection ? [connection] : [],
          next_cursor: null,
          pagination: {
            page: '1',
            page_size: 20,
            total_items: connection ? '1' : '0',
            total_pages: '1',
          },
        });
      if (url.pathname === '/api/endpoints/11' && init?.method === 'PATCH') {
        const body = JSON.parse(String(init.body));
        writes.push({ path: url.pathname, body });
        currentEndpoint = { ...currentEndpoint, connection_id: body.connection_id, revision: '4' };
        return jsonResponse(currentEndpoint);
      }
      if (url.pathname === '/api/endpoints/11') return jsonResponse(currentEndpoint);
      throw Error('Unexpected ' + url.pathname);
    }, '/endpoints/11?focus_key_id=21');
    await view.user.click(screen.getByRole('button', { name: 'Move to another connection' }));
    const scope = screen.getByText('Edit service', { selector: 'strong' }).closest('details')!;
    expect(scope).toHaveAttribute('open');
    expect(scope.querySelector('summary')).toHaveFocus();
    expect(writes).toHaveLength(0);
    await view.user.click(within(scope).getByRole('button', { name: 'Add connection' }));
    const create = screen.getByRole('region', { name: 'Add connection' });
    await view.user.type(
      within(create).getByRole('textbox', { name: 'Name' }),
      'Catalog connection',
    );
    await view.user.click(within(create).getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({
      path: '/api/connections',
      body: { name: 'Catalog connection', note: '' },
    });
    expect(currentEndpoint.connection_id).toBeNull();
    await waitFor(() => expect(within(scope).getByRole('button', { name: 'Save' })).toBeEnabled());
    await view.user.click(within(scope).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toEqual({
      path: '/api/endpoints/11',
      body: {
        native_services: ['chat_completions', 'responses'],
        connection_id: '9',
        expected_revision: '3',
      },
    });
  });
});
