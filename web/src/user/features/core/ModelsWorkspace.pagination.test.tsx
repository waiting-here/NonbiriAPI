import { act, screen, waitFor, within } from '@testing-library/react';
import { useLocation } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ModelsWorkspace } from './ModelsWorkspace';
import { coreKeys } from './queries';
import type { Binding, BindingCandidate, Endpoint, EndpointKey, Model, UserProfile } from './types';

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location-search">{location.search || '(empty)'}</output>;
}

const account: UserProfile = {
  id: '1',
  username: 'model-page-user',
  avatar: null,
  avatar_url: null,
  guild_nick: null,
  guild_avatar_url: null,
  lang: '',
  is_banned: false,
  banned_until: null,
  charity_suspended_until: null,
  endpoint_limit: null,
  effective_endpoint_limit: '20',
  rpm_limit: null,
  effective_rpm_limit: '60',
  concurrency_limit: null,
  effective_concurrency_limit: '10',
  balance: '1000',
  game_balance: '0',
  donation_credit: '0',
  effective_level: 2,
  level_display_name: 'Member',
  game_profile_public: false,
  charity_profile_public: false,
  automatic_restrictions: [],
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
  usage: {
    total_requests: '0',
    total_uncached_input_tokens: '0',
    total_cache_write_input_tokens: '0',
    total_cache_read_input_tokens: '0',
    total_output_tokens: '0',
    total_prompt_tokens: '0',
    total_completion_tokens: '0',
    total_unknown_usage_requests: '0',
  },
};

const otherAccount: UserProfile = {
  ...account,
  id: '2',
  username: 'second-model-page-user',
};

function modelRecord(id: string, bindingCount = '0'): Model {
  return {
    model_types: ['chat_completions', 'embeddings'],
    id,
    provider: `provider-${id}`,
    model: `model-${id}`,
    full_name: `provider-${id}/model-${id}`,
    route_strategy: 'ordered',
    silent_retry: false,
    transport_rule: 'passthrough',
    flatten_tool_calls: false,
    revision: '1',
    binding_revision: '1',
    binding_count: bindingCount,
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
  };
}

function endpointRecord(id: string): Endpoint {
  return {
    id,
    connector_type: 'openai-compatible',
    base_url: `https://api-${id}.example.test/v1`,
    origin: { kind: 'custom' },
    note: `endpoint-${id}`,
    enabled: true,
    revision: '1',
    key_count: '1',
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
  };
}

function keyRecord(id: string, endpointId: string): EndpointKey {
  return {
    id,
    endpoint_id: endpointId,
    display_head: `sk-${id}`,
    display_tail: 'tail',
    note: `key-${id}`,
    enabled: true,
    force_store_false: false,
    max_concurrency: 0,
    max_rpm: 0,
    suspension_state: 'none',
    revision: '1',
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
  };
}

function candidateRecord(
  endpointKeyId: string,
  upstreamModelId: string,
  source: 'automatic' | 'manual',
): BindingCandidate {
  return {
    endpoint_key_id: endpointKeyId,
    endpoint_base_url: 'https://upstream.example.test/v1',
    connector_type: 'openai-compatible',
    endpoint_note: 'candidate endpoint',
    endpoint_key_display_head: `sk-${endpointKeyId}`,
    endpoint_key_display_tail: 'tail',
    endpoint_key_note: 'candidate key',
    upstream_model_id: upstreamModelId,
    source_types: [source],
  };
}

function bindingRecord(index: number): Binding {
  const id = String(index + 1);
  return {
    id,
    endpoint_key_id: String(1000 + index),
    endpoint_base_url: 'https://bound.example.test/v1',
    connector_type: 'openai-compatible',
    endpoint_note: 'bound endpoint',
    endpoint_key_display_head: `sk-${id}`,
    endpoint_key_display_tail: 'tail',
    endpoint_key_note: `bound key ${id}`,
    upstream_model_id: `upstream-${id}`,
    ord: index,
  };
}

function numbered<T>(data: T[], requestedPage: string, pageSize: number, totalItems: number) {
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));
  const page = Math.min(Number(requestedPage), totalPages);
  return {
    data,
    next_cursor: null,
    pagination: {
      page: String(page),
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(totalPages),
    },
  };
}

function jsonResponse(payload: unknown, status = 200): Response {
  if (status === 204) return new Response(null, { status });
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function requestURL(input: RequestInfo | URL): URL {
  if (input instanceof Request) return new URL(input.url, window.location.origin);
  return new URL(String(input), window.location.origin);
}

function requestMethod(input: RequestInfo | URL, init?: RequestInit): string {
  if (init?.method) return init.method.toUpperCase();
  return input instanceof Request ? input.method.toUpperCase() : 'GET';
}

async function renderWorkspace(route: string, user = account) {
  const rendered = await renderWithProviders(
    <>
      <ModelsWorkspace user={user} />
      <LocationProbe />
    </>,
    { station: 'user', role: 'user', route },
  );
  rendered.queryClient.setQueryData(coreKeys.session, { user });
  return rendered;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('ModelsWorkspace numbered pagination', () => {
  it('clamps an outer model page, keeps the URL, and returns after deletion', async () => {
    const listedModels = Array.from({ length: 5 }, (_, index) => modelRecord(String(index + 21)));
    const detail = modelRecord('21');
    const calls: string[] = [];
    let deleted = false;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        calls.push(`${method} ${url.pathname}${url.search}`);
        if (method === 'GET' && url.pathname === '/api/models') {
          return jsonResponse(
            numbered(
              deleted ? listedModels.slice(1) : listedModels,
              url.searchParams.get('page') ?? '1',
              10,
              deleted ? 24 : 25,
            ),
          );
        }
        if (method === 'GET' && url.pathname === '/api/models/21') return jsonResponse(detail);
        if (method === 'GET' && url.pathname === '/api/models/21/bindings') {
          return jsonResponse({ bindings: [], binding_revision: '1' });
        }
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          return jsonResponse(numbered([], url.searchParams.get('page') ?? '1', 20, 0));
        }
        if (method === 'DELETE' && url.pathname === '/api/models/21') {
          deleted = true;
          return jsonResponse(null, 204);
        }
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates'))
          return jsonResponse(
            numbered(
              [],
              url.searchParams.get('page') ?? '1',
              Number(url.searchParams.get('page_size')),
              0,
            ),
          );
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?page=9&page_size=10');
    expect(await screen.findByText('provider-21/model-21')).toBeInTheDocument();
    expect(screen.getByText('25 items')).toBeInTheDocument();
    expect(
      screen.getByText('That page is no longer available. Showing page 3.'),
    ).toBeInTheDocument();

    await rendered.user.click(
      within(screen.getByText('provider-21/model-21').closest('tr')!).getByRole('button', {
        name: 'Edit model',
      }),
    );
    await waitFor(() =>
      expect(screen.getByTestId('location-search')).toHaveTextContent('model_id=21'),
    );
    expect(screen.getByTestId('location-search')).toHaveTextContent('page=9');
    expect(screen.getByTestId('location-search')).toHaveTextContent('page_size=10');

    await screen.findByRole('button', { name: 'Delete model' });
    await rendered.user.click(screen.getByRole('button', { name: 'Delete model' }));
    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Delete model' }));
    await waitFor(() =>
      expect(screen.getByTestId('location-search')).not.toHaveTextContent('model_id'),
    );
    expect(calls).toContain('DELETE /api/models/21');
    await screen.findByText('24 items');
    expect(screen.queryByText('provider-21/model-21')).not.toBeInTheDocument();
    expect(calls.filter((path) => path === 'GET /api/models/21')).toHaveLength(1);
    expect(rendered.queryClient.getQueryData(coreKeys.model(account.id, '21'))).toBeUndefined();
  });

  it('searches all services, retains unique selections across pages and applies an optional paged service filter', async () => {
    const selectedModel = modelRecord('31');
    const endpoints = Array.from({ length: 20 }, (_, index) => endpointRecord(String(index + 1)));
    const endpoint21 = endpointRecord('21');
    const candidates = Array.from({ length: 11 }, (_, index) => ({
      ...candidateRecord(
        index === 10 ? '221' : '211',
        `gpt-${index + 1}`,
        index === 10 ? 'manual' : 'automatic',
      ),
      endpoint_note: index === 10 ? 'second service' : 'first service',
    }));
    const bodies: unknown[] = [];
    const calls: URL[] = [];
    let bound: Binding[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        calls.push(url);
        if (method === 'GET' && url.pathname === '/api/models/31')
          return jsonResponse(selectedModel);
        if (method === 'GET' && url.pathname.endsWith('/bindings'))
          return jsonResponse({ bindings: bound, binding_revision: bound.length ? '2' : '1' });
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          const page = url.searchParams.get('page') ?? '1';
          return jsonResponse(numbered(page === '2' ? [endpoint21] : endpoints, page, 20, 21));
        }
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates')) {
          expect(url.searchParams.has('key_id')).toBe(false);
          expect(url.searchParams.has('source')).toBe(false);
          const page = url.searchParams.get('page') ?? '1',
            size = Number(url.searchParams.get('page_size'));
          const filtered = candidates.filter(
            (candidate) =>
              candidate.upstream_model_id.includes(url.searchParams.get('q') ?? '') &&
              (!url.searchParams.has('endpoint_id') || candidate.endpoint_key_id === '211'),
          );
          const offset = (Number(page) - 1) * size;
          return jsonResponse(
            numbered(filtered.slice(offset, offset + size), page, size, filtered.length),
          );
        }
        if (method === 'POST' && url.pathname.endsWith('/bindings/batch')) {
          const body = JSON.parse(String(init?.body));
          bodies.push(body);
          bound = body.selections.map((selection: BindingCandidate, index: number) => ({
            ...candidates.find(
              (candidate) =>
                candidate.endpoint_key_id === selection.endpoint_key_id &&
                candidate.upstream_model_id === selection.upstream_model_id,
            ),
            id: String(index + 1),
            ord: index,
          }));
          return jsonResponse({ bindings: bound, binding_revision: '2' }, 201);
        }
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );
    const rendered = await renderWorkspace('/models?model_id=31');
    const results = await screen.findByRole('region', { name: 'Add sources' });
    await within(results).findByText('gpt-1');
    expect(
      calls
        .filter((url) => url.pathname.endsWith('/binding-candidates'))
        .every((url) => !url.searchParams.has('endpoint_id')),
    ).toBe(true);
    await rendered.user.type(screen.getByRole('searchbox', { name: 'Add sources' }), 'gpt');
    await waitFor(() =>
      expect(calls.some((url) => url.searchParams.get('q') === 'gpt')).toBe(true),
    );
    await within(results).findByText('gpt-1');
    await rendered.user.click(within(results).getByText('gpt-1').closest('button')!);
    await rendered.user.click(within(results).getByRole('button', { name: 'Next' }));
    await rendered.user.click((await within(results).findByText('gpt-11')).closest('button')!);
    await rendered.user.click(screen.getByText('Filter by service', { selector: 'summary' }));
    const filter = screen
      .getByText('Filter by service', { selector: 'summary' })
      .closest('details')!;
    await rendered.user.click(within(filter).getByRole('button', { name: 'Next' }));
    await rendered.user.selectOptions(
      within(filter).getByRole('combobox', { name: 'Service' }),
      '21',
    );
    await waitFor(() =>
      expect(
        calls.some(
          (url) =>
            url.searchParams.get('endpoint_id') === '21' &&
            url.searchParams.get('q') === 'gpt' &&
            url.searchParams.get('page') === '1',
        ),
      ).toBe(true),
    );
    expect(
      screen.getByText('2 unique model(s) selected across filters and pages.'),
    ).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('button', { name: 'Add 2 selected sources' }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({
      expected_binding_revision: '1',
      selections: [
        { endpoint_key_id: '211', upstream_model_id: 'gpt-1' },
        { endpoint_key_id: '221', upstream_model_id: 'gpt-11' },
      ],
    });
  });

  it('submits the complete binding order after moving an item on a later page', async () => {
    const selectedModel = modelRecord('41', '25');
    const bindings = Array.from({ length: 25 }, (_, index) => bindingRecord(index));
    const orderBodies: unknown[] = [];

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        if (method === 'GET' && url.pathname === '/api/models/41')
          return jsonResponse(selectedModel);
        if (method === 'GET' && url.pathname === '/api/models/41/bindings') {
          return jsonResponse({ bindings, binding_revision: '1' });
        }
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          return jsonResponse(numbered([], url.searchParams.get('page') ?? '1', 20, 0));
        }
        if (method === 'PUT' && url.pathname === '/api/models/41/bindings/order') {
          const body = JSON.parse(String(init?.body)) as { order: string[] };
          orderBodies.push(body);
          const reordered = body.order.map((id, index) => ({
            ...bindings.find((binding) => binding.id === id),
            ord: index,
          }));
          return jsonResponse({ bindings: reordered, binding_revision: '2' });
        }
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates'))
          return jsonResponse(
            numbered(
              [],
              url.searchParams.get('page') ?? '1',
              Number(url.searchParams.get('page_size')),
              0,
            ),
          );
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=41');
    const orderSection = await screen.findByRole('heading', {
      name: 'Sources and order',
    });
    const section = orderSection.closest('section');
    expect(section).not.toBeNull();
    await within(section!).findByText('upstream-1');
    const pageSize = within(section!).getByRole('combobox', { name: 'Items per page' });
    await rendered.user.selectOptions(pageSize, '10');
    await rendered.user.click(within(section!).getByRole('button', { name: 'Next' }));
    await within(section!).findByText('upstream-11');
    const row = within(section!).getByText('upstream-11').closest('li')!;
    await rendered.user.click(
      within(row).getByRole('button', { name: 'Source actions for upstream-11' }),
    );
    await rendered.user.click(within(row).getByRole('menuitem', { name: 'Move down' }));
    await rendered.user.click(
      within(section!).getByRole('button', { name: 'Save complete order' }),
    );

    await waitFor(() => expect(orderBodies).toHaveLength(1));
    expect(orderBodies[0]).toEqual({
      expected_binding_revision: '1',
      order: [
        ...Array.from({ length: 10 }, (_, index) => String(index + 1)),
        '12',
        '11',
        ...Array.from({ length: 13 }, (_, index) => String(index + 13)),
      ],
    });
  });

  it('keeps account-scoped model data separate when the workspace account changes', async () => {
    const firstModel = modelRecord('51');
    const secondModel = {
      ...modelRecord('51'),
      provider: 'second-provider',
      full_name: 'second-provider/model-51',
    };
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        requests.push(`${method} ${url.pathname}${url.search}`);
        if (method === 'GET' && url.pathname === '/api/models/51') {
          return jsonResponse(
            requests.filter((request) => request === 'GET /api/models/51').length === 1
              ? firstModel
              : secondModel,
          );
        }
        if (method === 'GET' && url.pathname === '/api/models/51/bindings') {
          return jsonResponse({ bindings: [], binding_revision: '1' });
        }
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          return jsonResponse(numbered([], url.searchParams.get('page') ?? '1', 20, 0));
        }
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates'))
          return jsonResponse(
            numbered(
              [],
              url.searchParams.get('page') ?? '1',
              Number(url.searchParams.get('page_size')),
              0,
            ),
          );
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=51', account);
    await screen.findByRole('heading', { name: 'provider-51/model-51' });
    rendered.queryClient.setQueryData(coreKeys.session, { user: otherAccount });
    rendered.rerender(
      <>
        <ModelsWorkspace user={otherAccount} />
        <LocationProbe />
      </>,
    );

    await screen.findByRole('heading', { name: 'second-provider/model-51' });
    expect(rendered.queryClient.getQueryData(coreKeys.model(account.id, '51'))).toEqual({
      ...firstModel,
      role_policy: { default_action: 'native', rules: {} },
    });
    expect(rendered.queryClient.getQueryData(coreKeys.model(otherAccount.id, '51'))).toEqual({
      ...secondModel,
      role_policy: { default_action: 'native', rules: {} },
    });
  });

  it('hides private model detail when the session identity disappears', async () => {
    const selectedModel = modelRecord('56');
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        if (method === 'GET' && url.pathname === '/api/models/56')
          return jsonResponse(selectedModel);
        if (method === 'GET' && url.pathname === '/api/models/56/bindings') {
          return jsonResponse({ bindings: [], binding_revision: '1' });
        }
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          return jsonResponse(numbered([], url.searchParams.get('page') ?? '1', 20, 0));
        }
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates'))
          return jsonResponse(
            numbered(
              [],
              url.searchParams.get('page') ?? '1',
              Number(url.searchParams.get('page_size')),
              0,
            ),
          );
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=56');
    await screen.findByRole('heading', { name: 'provider-56/model-56' });
    rendered.queryClient.setQueryData(coreKeys.session, null);
    await waitFor(() => expect(screen.queryAllByText('provider-56/model-56')).toHaveLength(0));
    expect(screen.queryByRole('button', { name: 'Edit model' })).not.toBeInTheDocument();
  });

  it('shows a permission error and no candidate write path after candidate access is revoked', async () => {
    const selectedModel = modelRecord('61');
    const endpoint = endpointRecord('61');
    const key = keyRecord('611', '61');
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        if (method === 'GET' && url.pathname === '/api/models/61')
          return jsonResponse(selectedModel);
        if (method === 'GET' && url.pathname === '/api/models/61/bindings') {
          return jsonResponse({ bindings: [], binding_revision: '1' });
        }
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          return jsonResponse(numbered([endpoint], url.searchParams.get('page') ?? '1', 20, 1));
        }
        if (method === 'GET' && url.pathname === '/api/endpoints/61/keys') {
          return jsonResponse(numbered([key], url.searchParams.get('page') ?? '1', 20, 1));
        }
        if (method === 'GET' && url.pathname === '/api/models/61/binding-candidates') {
          return jsonResponse({ error: { code: 'permission_denied', message: 'forbidden' } }, 403);
        }
        if (method === 'POST') throw new Error('candidate writes must be unavailable');
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates'))
          return jsonResponse(
            numbered(
              [],
              url.searchParams.get('page') ?? '1',
              Number(url.searchParams.get('page_size')),
              0,
            ),
          );
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    await renderWorkspace('/models?model_id=61');
    expect(
      await screen.findAllByText('Your current session no longer permits this operation.'),
    ).not.toHaveLength(0);
    expect(
      screen.queryByRole('button', { name: 'Add 0 selected sources' }),
    ).not.toBeInTheDocument();
  });

  it('closes the candidate write path when adding a selection loses permission', async () => {
    const selectedModel = modelRecord('81');
    const endpoint = endpointRecord('81');
    const key = keyRecord('811', '81');
    const candidate = candidateRecord('811', 'write-candidate', 'automatic');
    const writes: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        if (method === 'GET' && url.pathname === '/api/models/81')
          return jsonResponse(selectedModel);
        if (method === 'GET' && url.pathname === '/api/models/81/bindings') {
          return jsonResponse({ bindings: [], binding_revision: '1' });
        }
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          return jsonResponse(numbered([endpoint], url.searchParams.get('page') ?? '1', 20, 1));
        }
        if (method === 'GET' && url.pathname === '/api/endpoints/81/keys') {
          return jsonResponse(numbered([key], url.searchParams.get('page') ?? '1', 20, 1));
        }
        if (method === 'GET' && url.pathname === '/api/models/81/binding-candidates') {
          const page = url.searchParams.get('page') ?? '1';
          const pageSize = Number(url.searchParams.get('page_size') ?? '20');
          return !url.searchParams.has('source')
            ? jsonResponse(numbered([candidate], page, pageSize, 1))
            : jsonResponse(numbered([], page, pageSize, 0));
        }
        if (method === 'POST' && url.pathname === '/api/models/81/bindings/batch') {
          writes.push(String(init?.body));
          return jsonResponse({ error: { code: 'permission_denied', message: 'forbidden' } }, 403);
        }
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates'))
          return jsonResponse(
            numbered(
              [],
              url.searchParams.get('page') ?? '1',
              Number(url.searchParams.get('page_size')),
              0,
            ),
          );
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=81');
    await rendered.user.click((await screen.findByText('write-candidate')).closest('button')!);
    await rendered.user.click(screen.getByRole('button', { name: 'Add 1 selected sources' }));

    expect(writes).toHaveLength(1);
    await waitFor(() => expect(rendered.queryClient.getQueryData(coreKeys.session)).toBeNull());
    expect(
      screen.queryByRole('button', { name: 'Add 1 selected sources' }),
    ).not.toBeInTheDocument();
  });

  it('resets the candidate page and hides the previous filter while a new search is pending', async () => {
    const selectedModel = modelRecord('71');
    const endpoint = endpointRecord('71');
    const key = keyRecord('711', '71');
    const initialCandidates = Array.from({ length: 11 }, (_, index) =>
      candidateRecord('711', `searchable-${index + 1}`, 'automatic'),
    );
    const searchedCandidate = candidateRecord('711', 'needle-result', 'automatic');
    const requests: string[] = [];
    let searchSignal: AbortSignal | undefined;
    let resolveSearch: ((response: Response) => void) | undefined;
    const slowSearch = new Promise<Response>((resolve) => {
      resolveSearch = resolve;
    });

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        requests.push(`${method} ${url.pathname}${url.search}`);
        if (method === 'GET' && url.pathname === '/api/models/71')
          return jsonResponse(selectedModel);
        if (method === 'GET' && url.pathname === '/api/models/71/bindings') {
          return jsonResponse({ bindings: [], binding_revision: '1' });
        }
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          return jsonResponse(numbered([endpoint], url.searchParams.get('page') ?? '1', 20, 1));
        }
        if (method === 'GET' && url.pathname === '/api/endpoints/71/keys') {
          return jsonResponse(numbered([key], url.searchParams.get('page') ?? '1', 20, 1));
        }
        if (method === 'GET' && url.pathname === '/api/models/71/binding-candidates') {
          const page = url.searchParams.get('page') ?? '1';
          const pageSize = Number(url.searchParams.get('page_size') ?? '20');
          const source = url.searchParams.get('source');
          const query = url.searchParams.get('q');
          if (!source && query === 'needle') {
            searchSignal = init?.signal ?? undefined;
            return slowSearch;
          }
          if (!source && query === 'latest')
            return jsonResponse(
              numbered(
                [{ ...searchedCandidate, upstream_model_id: 'latest-result' }],
                '1',
                pageSize,
                1,
              ),
            );
          if (!source) {
            const offset = (Number(page) - 1) * pageSize;
            return jsonResponse(
              numbered(
                initialCandidates.slice(offset, offset + pageSize),
                page,
                pageSize,
                initialCandidates.length,
              ),
            );
          }
          return jsonResponse(numbered([], page, pageSize, 0));
        }
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates'))
          return jsonResponse(
            numbered(
              [],
              url.searchParams.get('page') ?? '1',
              Number(url.searchParams.get('page_size')),
              0,
            ),
          );
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=71');
    const automaticSection = await screen.findByRole('region', { name: 'Add sources' });
    await within(automaticSection!).findByText('searchable-1');
    await rendered.user.selectOptions(
      within(automaticSection!).getByRole('combobox', { name: 'Items per page' }),
      '10',
    );
    await rendered.user.click(within(automaticSection!).getByRole('button', { name: 'Next' }));
    await within(automaticSection!).findByText('searchable-11');

    const searchInput = screen.getByRole('searchbox', { name: 'Add sources' });
    await rendered.user.type(searchInput, 'needle');
    expect(requests.some((request) => request.includes('q=needle'))).toBe(false);
    await waitFor(() =>
      expect(requests.some((request) => request.includes('q=needle&page=1&page_size=10'))).toBe(
        true,
      ),
    );
    expect(automaticSection).toHaveAttribute('aria-busy', 'true');
    expect(within(automaticSection!).queryByText('searchable-11')).not.toBeInTheDocument();

    await rendered.user.clear(searchInput);
    await rendered.user.type(searchInput, 'latest');
    await within(automaticSection).findByText('latest-result');
    expect(searchSignal?.aborted).toBe(true);
    await act(async () => resolveSearch!(jsonResponse(numbered([searchedCandidate], '1', 10, 1))));
    expect(within(automaticSection).queryByText('needle-result')).not.toBeInTheDocument();
    expect(within(automaticSection).getByText('latest-result')).toBeInTheDocument();
    expect(within(automaticSection).getByText('1 items')).toBeInTheDocument();
  });
  it('drops source selections, search and late query results across account boundaries', async () => {
    let actor = 'first';
    let finish!: (response: Response) => void;
    let oldSignal: AbortSignal | undefined;
    const slow = new Promise<Response>((resolve) => {
      finish = resolve;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input),
          method = requestMethod(input, init);
        if (method === 'GET' && url.pathname === '/api/models/91')
          return jsonResponse({
            ...modelRecord('91'),
            full_name: `${actor}/model-91`,
            provider: actor,
          });
        if (method === 'GET' && url.pathname.endsWith('/bindings'))
          return jsonResponse({ bindings: [], binding_revision: '1' });
        if (method === 'GET' && url.pathname === '/api/endpoints')
          return jsonResponse(numbered([], '1', 20, 0));
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates')) {
          if (actor === 'first' && url.searchParams.get('q') === 'private-search') {
            oldSignal = init?.signal ?? undefined;
            return slow;
          }
          return jsonResponse(
            numbered(
              [
                candidateRecord(
                  actor === 'first' ? '911' : '921',
                  actor === 'first' ? 'private-first' : 'second-only',
                  'automatic',
                ),
              ],
              '1',
              10,
              1,
            ),
          );
        }
        throw new Error(`Unexpected request: ${method} ${url.pathname}`);
      }),
    );
    const view = await renderWorkspace('/models?model_id=91');
    await view.user.click((await screen.findByText('private-first')).closest('button')!);
    await view.user.type(screen.getByRole('searchbox', { name: 'Add sources' }), 'private-search');
    await waitFor(() => expect(oldSignal).toBeDefined());
    actor = 'second';
    view.queryClient.setQueryData(coreKeys.session, { user: otherAccount });
    view.rerender(
      <>
        <ModelsWorkspace user={otherAccount} />
        <LocationProbe />
      </>,
    );
    await screen.findByText('second-only');
    expect(screen.getByRole('searchbox', { name: 'Add sources' })).toHaveValue('');
    expect(
      screen.queryByRole('button', { name: 'Add 1 selected sources' }),
    ).not.toBeInTheDocument();
    expect(oldSignal?.aborted).toBe(true);
    await act(async () =>
      finish(
        jsonResponse(numbered([candidateRecord('911', 'private-late', 'automatic')], '1', 10, 1)),
      ),
    );
    expect(screen.queryByText('private-late')).not.toBeInTheDocument();
    expect(screen.queryByText('private-first')).not.toBeInTheDocument();
    expect(screen.getByText('second-only')).toBeInTheDocument();
  });
  it.each([false, true])(
    'keeps manual catalog creation separate from binding and recovers a recorded unknown result (%s)',
    async (unknown) => {
      const model = modelRecord('101'),
        endpoint = endpointRecord('11'),
        key = keyRecord('111', '11');
      const entry = {
        id: '151',
        source_type: 'manual',
        upstream_model_id: 'typed-model',
        provider: '',
        source_revision: '1',
        pair_revision: '1',
        created_at: 1700000000,
        updated_at: 1700000001,
      };
      const evidence = {
        state: 'unknown',
        revision: '1',
        result: null,
        safe_class: 'none',
        observed_at: null,
        count: null,
      };
      let made = false;
      let bound: Binding[] = [];
      const manualWrites: unknown[] = [];
      const bindingWrites: unknown[] = [];
      const identities: string[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
          const url = requestURL(input),
            method = requestMethod(input, init);
          if (method === 'GET' && url.pathname === '/api/models/101') return jsonResponse(model);
          if (method === 'GET' && url.pathname.endsWith('/bindings'))
            return jsonResponse({ bindings: bound, binding_revision: bound.length ? '2' : '1' });
          if (method === 'GET' && url.pathname === '/api/endpoints')
            return jsonResponse(numbered([endpoint], '1', 20, 1));
          if (method === 'GET' && url.pathname === '/api/endpoints/11/keys')
            return jsonResponse(numbered([key], '1', 20, 1));
          if (method === 'GET' && url.pathname.endsWith('/binding-candidates'))
            return jsonResponse(
              numbered(
                made ? [candidateRecord('111', 'typed-model', 'manual')] : [],
                '1',
                10,
                made ? 1 : 0,
              ),
            );
          if (method === 'GET' && url.pathname === '/api/endpoints/11/keys/111/models')
            return jsonResponse({
              evidence,
              automatic_entries: [],
              manual_entries: made ? [entry] : [],
              next_cursor: null,
            });
          if (method === 'POST' && url.pathname.endsWith('/models/manual')) {
            manualWrites.push(JSON.parse(String(init?.body)));
            identities.push(new Headers(init?.headers).get('Idempotency-Key')!);
            made = true;
            if (unknown) throw new TypeError('Failed to fetch');
            return jsonResponse({ entries: [entry] }, 201);
          }
          if (method === 'POST' && url.pathname === '/api/resource-operation-status')
            return jsonResponse({
              status: 'recorded',
              stage: 'catalog_manual',
              result: { endpoint_id: '11', endpoint_key_id: '111', entry_ids: ['151'] },
            });
          if (method === 'POST' && url.pathname.endsWith('/bindings/batch')) {
            bindingWrites.push(JSON.parse(String(init?.body)));
            identities.push(new Headers(init?.headers).get('Idempotency-Key')!);
            bound = [{ ...candidateRecord('111', 'typed-model', 'manual'), id: '161', ord: 0 }];
            return jsonResponse({ bindings: bound, binding_revision: '2' }, 201);
          }
          throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
        }),
      );
      const view = await renderWorkspace('/models?model_id=101');
      await view.user.click(await screen.findByText('Filter by service', { selector: 'summary' }));
      await view.user.selectOptions(screen.getByRole('combobox', { name: 'Service' }), '11');
      await view.user.click(screen.getByRole('button', { name: 'Enter a model name manually' }));
      await view.user.selectOptions(await screen.findByRole('combobox', { name: 'Key' }), '111');
      await view.user.type(
        screen.getByRole('textbox', { name: 'Service model name' }),
        'typed-model',
      );
      await view.user.click(screen.getByRole('button', { name: 'Add manual entry' }));
      await screen.findByText('Saved to the model list. You can now add it as a source.');
      expect(manualWrites).toEqual([
        { entries: [{ upstream_model_id: 'typed-model', provider: '' }] },
      ]);
      expect(bindingWrites).toHaveLength(0);
      await view.user.type(
        screen.getByRole('textbox', {
          name: unknown ? 'Service model name' : view.i18n.t('user.core.endpoints.manualProvider'),
        }),
        '-next',
      );
      expect([
        screen.queryByText(view.i18n.t('common.outcome.saved')),
        screen.queryByText('Saved to the model list. You can now add it as a source.'),
      ]).toEqual([null, null]);
      await view.user.click(screen.getByRole('button', { name: 'Add 1 selected sources' }));
      await waitFor(() => expect(bindingWrites).toHaveLength(1));
      expect(bindingWrites[0]).toEqual({
        expected_binding_revision: '1',
        selections: [{ endpoint_key_id: '111', upstream_model_id: 'typed-model' }],
      });
      expect(identities).toHaveLength(2);
      expect(identities.every(Boolean)).toBe(true);
      expect(identities[0]).not.toBe(identities[1]);
      expect(manualWrites).toHaveLength(1);
    },
  );
});
