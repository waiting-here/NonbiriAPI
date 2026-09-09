import { screen, waitFor, within } from '@testing-library/react';
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
  donation_credit: '0',
  effective_level: 2,
  level_display_name: 'Member',
  game_profile_public: false,
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
    id,
    provider: `provider-${id}`,
    model: `model-${id}`,
    full_name: `provider-${id}/model-${id}`,
    route_strategy: 'ordered',
    silent_retry: false,
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
  rendered.queryClient.setQueryData(coreKeys.session, { user: { id: user.id } });
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
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?page=9&page_size=10');
    expect(await screen.findByText('provider-21/model-21')).toBeInTheDocument();
    expect(screen.getByText('Page 3 of 3 · Total: 25')).toBeInTheDocument();
    expect(
      screen.getByText('That page is no longer available. Showing page 3.'),
    ).toBeInTheDocument();

    await rendered.user.click(
      within(screen.getByText('provider-21/model-21').closest('li')!).getByRole('button', {
        name: 'Manage connections',
      }),
    );
    await waitFor(() =>
      expect(screen.getByTestId('location-search')).toHaveTextContent('model_id=21'),
    );
    expect(screen.getByTestId('location-search')).toHaveTextContent('page=9');
    expect(screen.getByTestId('location-search')).toHaveTextContent('page_size=10');

    await screen.findByRole('button', { name: 'Delete platform model' });
    await rendered.user.click(screen.getByRole('button', { name: 'Delete platform model' }));
    const dialog = await screen.findByRole('alertdialog');
    await rendered.user.click(
      within(dialog).getByRole('button', { name: 'Delete platform model' }),
    );
    await waitFor(() =>
      expect(screen.getByTestId('location-search')).not.toHaveTextContent('model_id'),
    );
    expect(calls).toContain('DELETE /api/models/21');
    await screen.findByText('Page 3 of 3 · Total: 24');
    expect(screen.queryByText('provider-21/model-21')).not.toBeInTheDocument();
    expect(calls.filter((path) => path === 'GET /api/models/21')).toHaveLength(1);
    expect(rendered.queryClient.getQueryData(coreKeys.model(account.id, '21'))).toBeUndefined();
  });

  it('keeps endpoint, key, and candidate choices across numbered pages', async () => {
    const selectedModel = modelRecord('31');
    const endpoints = Array.from({ length: 20 }, (_, index) => endpointRecord(String(index + 1)));
    const endpoint21 = endpointRecord('21');
    const key21 = keyRecord('211', '21');
    const automaticCandidates = Array.from({ length: 11 }, (_, index) =>
      candidateRecord('211', `auto-${index + 1}`, 'automatic'),
    );
    const automaticOne = automaticCandidates[0];
    const automaticTwo = automaticCandidates[10];
    const manualOne = candidateRecord('211', 'manual-one', 'manual');
    const selections: unknown[] = [];

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        if (method === 'GET' && url.pathname === '/api/models/31')
          return jsonResponse(selectedModel);
        if (method === 'GET' && url.pathname === '/api/models/31/bindings') {
          return jsonResponse({ bindings: [], binding_revision: '1' });
        }
        if (method === 'GET' && url.pathname === '/api/endpoints') {
          const page = url.searchParams.get('page') ?? '1';
          return jsonResponse(numbered(page === '2' ? [endpoint21] : endpoints, page, 20, 21));
        }
        if (method === 'GET' && url.pathname === '/api/endpoints/21/keys') {
          return jsonResponse(numbered([key21], url.searchParams.get('page') ?? '1', 20, 1));
        }
        if (method === 'GET' && url.pathname === '/api/models/31/binding-candidates') {
          const source = url.searchParams.get('source');
          const page = url.searchParams.get('page') ?? '1';
          const pageSize = Number(url.searchParams.get('page_size') ?? '20');
          if (source === 'automatic') {
            const candidates = automaticCandidates;
            const offset = (Number(page) - 1) * pageSize;
            return jsonResponse(
              numbered(
                candidates.slice(offset, offset + pageSize),
                page,
                pageSize,
                candidates.length,
              ),
            );
          }
          return jsonResponse(numbered([manualOne], page, pageSize, 1));
        }
        if (method === 'POST' && url.pathname === '/api/models/31/bindings/batch') {
          selections.push(JSON.parse(String(init?.body)));
          return jsonResponse(
            {
              bindings: [automaticOne, automaticTwo].map((candidate, index) => ({
                ...candidate,
                id: String(index + 1),
                ord: index,
              })),
              binding_revision: '2',
            },
            201,
          );
        }
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=31');
    await screen.findByRole('heading', { name: 'Add connections' });
    const endpointSection = screen
      .getByRole('heading', { name: '1 · Endpoint' })
      .closest('section');
    expect(endpointSection).not.toBeNull();
    await rendered.user.click(within(endpointSection!).getByRole('button', { name: 'Next' }));
    await rendered.user.click(
      await within(endpointSection!).findByRole('button', { name: /endpoint-21/ }),
    );

    const keySection = screen.getByRole('heading', { name: '2 · Key' }).closest('section');
    expect(keySection).not.toBeNull();
    await rendered.user.click(await within(keySection!).findByRole('button', { name: /key-211/ }));

    const automaticSection = screen
      .getByRole('heading', { name: 'Automatically found models' })
      .closest('section');
    expect(automaticSection).not.toBeNull();
    await rendered.user.selectOptions(
      await within(automaticSection!).findByRole('combobox', { name: 'Items per page' }),
      '10',
    );
    await rendered.user.click(
      (await within(automaticSection!).findByText('auto-1')).closest('button')!,
    );
    await rendered.user.click(within(automaticSection!).getByRole('button', { name: 'Next' }));
    await rendered.user.click(
      (await within(automaticSection!).findByText('auto-11')).closest('button')!,
    );

    expect(
      screen.getByText('2 unique model(s) selected across filters and pages.'),
    ).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('button', { name: 'Add 2 selected connection(s)' }));
    await waitFor(() => expect(selections).toHaveLength(1));
    expect(selections[0]).toEqual({
      expected_binding_revision: '1',
      selections: [
        { endpoint_key_id: '211', upstream_model_id: 'auto-1' },
        { endpoint_key_id: '211', upstream_model_id: 'auto-11' },
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
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=41');
    const orderSection = await screen.findByRole('heading', {
      name: 'Current connections and order',
    });
    const section = orderSection.closest('section');
    expect(section).not.toBeNull();
    await within(section!).findByText('upstream-1');
    const pageSize = within(section!).getByRole('combobox', { name: 'Items per page' });
    await rendered.user.selectOptions(pageSize, '10');
    await rendered.user.click(within(section!).getByRole('button', { name: 'Next' }));
    await within(section!).findByText('upstream-11');
    await rendered.user.click(within(section!).getAllByRole('button', { name: 'Move down' })[0]);
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
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=51', account);
    await screen.findByText('provider-51/model-51');
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: otherAccount.id } });
    rendered.rerender(
      <>
        <ModelsWorkspace user={otherAccount} />
        <LocationProbe />
      </>,
    );

    await screen.findByText('second-provider/model-51');
    expect(rendered.queryClient.getQueryData(coreKeys.model(account.id, '51'))).toEqual(firstModel);
    expect(rendered.queryClient.getQueryData(coreKeys.model(otherAccount.id, '51'))).toEqual(
      secondModel,
    );
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
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=56');
    await screen.findByText('provider-56/model-56');
    rendered.queryClient.setQueryData(coreKeys.session, null);
    await waitFor(() => expect(screen.queryByText('provider-56/model-56')).not.toBeInTheDocument());
    expect(screen.queryByRole('button', { name: 'Edit platform model' })).not.toBeInTheDocument();
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
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=61');
    await rendered.user.click(await screen.findByRole('button', { name: /endpoint-61/ }));
    await rendered.user.click(await screen.findByRole('button', { name: /key-611/ }));
    expect(
      await screen.findAllByText('Your current session no longer permits this operation.'),
    ).not.toHaveLength(0);
    expect(
      screen.queryByRole('button', { name: 'Add 0 selected connection(s)' }),
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
          return url.searchParams.get('source') === 'automatic'
            ? jsonResponse(numbered([candidate], page, pageSize, 1))
            : jsonResponse(numbered([], page, pageSize, 0));
        }
        if (method === 'POST' && url.pathname === '/api/models/81/bindings/batch') {
          writes.push(String(init?.body));
          return jsonResponse({ error: { code: 'permission_denied', message: 'forbidden' } }, 403);
        }
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=81');
    await rendered.user.click(await screen.findByRole('button', { name: /endpoint-81/ }));
    await rendered.user.click(await screen.findByRole('button', { name: /key-811/ }));
    await rendered.user.click((await screen.findByText('write-candidate')).closest('button')!);
    await rendered.user.click(screen.getByRole('button', { name: 'Add 1 selected connection(s)' }));

    expect(writes).toHaveLength(1);
    expect(
      await screen.findByText('Your current session no longer permits this operation.'),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Add 1 selected connection(s)' }),
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
          if (source === 'automatic' && query === 'needle') return slowSearch;
          if (source === 'automatic') {
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
        throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
      }),
    );

    const rendered = await renderWorkspace('/models?model_id=71');
    await rendered.user.click(await screen.findByRole('button', { name: /endpoint-71/ }));
    await rendered.user.click(await screen.findByRole('button', { name: /key-711/ }));
    const automaticSection = screen
      .getByRole('heading', { name: 'Automatically found models' })
      .closest('section');
    expect(automaticSection).not.toBeNull();
    await within(automaticSection!).findByText('searchable-1');
    await rendered.user.selectOptions(
      within(automaticSection!).getByRole('combobox', { name: 'Items per page' }),
      '10',
    );
    await rendered.user.click(within(automaticSection!).getByRole('button', { name: 'Next' }));
    await within(automaticSection!).findByText('searchable-11');

    const searchInput = screen.getByRole('textbox', { name: 'Search upstream models' });
    await rendered.user.type(searchInput, 'needle');
    expect(requests.some((request) => request.includes('q=needle'))).toBe(false);
    await rendered.user.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(requests.some((request) => request.includes('q=needle&page=1&page_size=10'))).toBe(
        true,
      ),
    );
    expect(automaticSection).toHaveAttribute('aria-busy', 'true');
    expect(within(automaticSection!).queryByText('searchable-11')).not.toBeInTheDocument();

    resolveSearch!(jsonResponse(numbered([searchedCandidate], '1', 10, 1)));
    await within(automaticSection!).findByText('needle-result');
    expect(within(automaticSection!).getByText('Page 1 of 1 · Total: 1')).toBeInTheDocument();
  });
});
