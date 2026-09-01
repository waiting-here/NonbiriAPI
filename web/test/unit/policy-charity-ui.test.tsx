import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useEffect, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Route, Routes } from 'react-router';
import { describe, expect, test, vi } from 'vitest';
import { CharityManagement } from '../../src/shared/components/CharityManagement';
import {
  beginManagementSessionRequest,
  charityManagementKeys,
  clearManagementSession,
  failManagementSessionRequest,
  isStationSessionChanged,
  noteManagementSessionSuccess,
  stationSessionWrite,
  useManagementCapability,
  useCreateManagedModel,
  useManagementBindings,
} from '../../src/shared/charityManagement';
import { useAdminSession } from '../../src/admin/data';
import { CharityPage } from '../../src/user/pages/CharityPage';
import { useUserSession } from '../../src/user/data';
import { EndpointsPage } from '../../src/user/pages/EndpointsPage';
import { UserLayout } from '../../src/user/layouts/UserLayout';
import { ModelsPage } from '../../src/user/pages/ModelsPage';
import { StewardPage } from '../../src/user/pages/StewardPage';
import { CallerKeyPanel } from '../../src/user/pages/KeysPage';
import { AccountLifecyclePanel } from '../../src/user/features/core/AccountWorkspace';
import { coreKeys } from '../../src/user/features/core/queries';
import type {
  AccountExportAttachment,
  AccountLifecycleAdapter,
} from '../../src/user/features/core/types';
import { userKeys } from '../../src/user/data';
import { ApiError } from '../../src/shared/query/http';
import {
  assertNoSensitiveQueryCache,
  installJsonFetchFixtures,
  renderWithProviders,
} from './support';

const coreSession = {
  user: {
    id: '1',
    username: 'fixture-user',
    avatar: null,
    avatar_url: null,
    guild_nick: null,
    guild_avatar_url: null,
    lang: 'en',
    is_banned: false,
    banned_until: null,
    charity_suspended_until: null,
    endpoint_limit: null,
    effective_endpoint_limit: '10',
    rpm_limit: null,
    effective_rpm_limit: '60',
    concurrency_limit: null,
    effective_concurrency_limit: '5',
    balance: '0',
    donation_credit: '0',
    effective_level: 2,
    level_display_name: 'Lv2',
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
  },
};

const session = coreSession;

const endpoint = {
  id: 1,
  connector_type: 'openai-compatible',
  base_url: 'https://upstream.test/v1',
  note: 'primary',
  enabled: true,
  created_at: 1,
  updated_at: 2,
};

const model = {
  id: 3,
  provider: 'provider',
  model: 'model',
  full_name: 'provider/model',
  route_strategy: 'ordered',
  silent_retry: false,
  flatten_tool_calls: false,
  binding_count: 2,
  created_at: 1,
  updated_at: 2,
};

const coreEndpoint = {
  id: '1',
  connector_type: 'openai-compatible',
  base_url: 'https://upstream.test/v1',
  note: 'primary',
  enabled: true,
  revision: '1',
  key_count: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
};

const coreEndpointKey = {
  id: '2',
  endpoint_id: '1',
  display_head: 'sk-a',
  display_tail: 'tail',
  note: 'key note',
  enabled: true,
  force_store_false: false,
  suspension_state: 'none',
  revision: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
};

const coreModel = {
  id: '3',
  provider: 'provider',
  model: 'model',
  full_name: 'provider/model',
  route_strategy: 'ordered',
  silent_retry: false,
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '2',
  binding_count: '2',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
};

const coreCatalogUnknown = {
  evidence: {
    state: 'unknown',
    revision: '1',
    result: null,
    safe_class: 'none',
    observed_at: null,
    count: null,
  },
  automatic_entries: [],
  manual_entries: [],
  next_cursor: null,
};

const coreManualCatalog = {
  ...coreCatalogUnknown,
  manual_entries: [
    {
      id: '31',
      source_type: 'manual',
      upstream_model_id: 'Vendor/Exact',
      provider: 'Vendor',
      source_revision: '1',
      pair_revision: '2',
      created_at: 1_700_000_003,
      updated_at: 1_700_000_004,
    },
  ],
};

function corePage<T>(data: T[]) {
  return { data, next_cursor: null };
}

function coreBinding(id: string, upstreamModelId: string, ord: number) {
  return {
    id,
    endpoint_key_id: '2',
    upstream_model_id: upstreamModelId,
    endpoint_base_url: coreEndpoint.base_url,
    connector_type: 'openai-compatible',
    endpoint_note: coreEndpoint.note,
    endpoint_key_display_head: coreEndpointKey.display_head,
    endpoint_key_display_tail: coreEndpointKey.display_tail,
    endpoint_key_note: coreEndpointKey.note,
    ord,
  };
}

function endpointRouteTree() {
  return (
    <Routes>
      <Route path="/endpoints" element={<EndpointsPage />} />
      <Route path="/endpoints/:endpointId" element={<EndpointsPage />} />
    </Routes>
  );
}

function requestPath(input: string | URL | Request): string {
  const url = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
  return `${url.pathname}${url.search}`;
}

const managedModel = {
  id: 7,
  provider: 'provider',
  model: 'charity-model',
  full_name: 'provider/charity-model',
  enabled: true,
  flatten_tool_calls: false,
  pricing_mode: 'per_request',
  prices: {
    request_user_price_milli: '0',
    request_donor_reward_milli: '0',
    uncached_user_price_milli: '0',
    cache_write_user_price_milli: '0',
    cache_read_user_price_milli: '0',
    output_user_price_milli: '0',
    uncached_donor_reward_milli: '0',
    cache_write_donor_reward_milli: '0',
    cache_read_donor_reward_milli: '0',
    output_donor_reward_milli: '0',
    current_request_user_price_milli: '0',
    current_uncached_user_price_milli: '0',
    current_cache_write_user_price_milli: '0',
    current_cache_read_user_price_milli: '0',
    current_output_user_price_milli: '0',
  },
  discount: { percent: 100, enabled: false, start_at: null, end_at: null },
  success_samples: 0,
  success_count: 0,
};

function lastBody(
  fetchMock: { mock: { calls: unknown[][] } },
  method: string,
  path: string,
): Record<string, unknown> {
  const call = [...fetchMock.mock.calls].reverse().find((entry) => {
    const request = entry[0] as string;
    const init = entry[1] as RequestInit | undefined;
    return (
      new URL(request, window.location.origin).pathname === path &&
      (init?.method ?? 'GET') === method
    );
  });
  if (!call) throw new Error(`Missing ${method} ${path}`);
  return JSON.parse(String((call[1] as RequestInit).body)) as Record<string, unknown>;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

function ManagementBindingsProbe() {
  const query = useManagementBindings('admin', '7');
  return (
    <output data-testid="management-bindings-state">
      {query.isPending
        ? 'pending'
        : query.error instanceof Error
          ? query.error.message
          : JSON.stringify(query.data)}
    </output>
  );
}

function ManagedModelMutationProbe() {
  const mutation = useCreateManagedModel('admin');
  return (
    <>
      <button
        type="button"
        onClick={() =>
          mutation.mutate({
            provider: 'provider',
            model: 'charity-model',
            pricing_mode: 'per_request',
            flatten_tool_calls: false,
            prices: managedModel.prices,
            discount: { percent: 100, enabled: false, start_at: null, end_at: null },
          })
        }
      >
        run mutation
      </button>
      <output data-testid="management-mutation-state">
        {mutation.isPending
          ? 'pending'
          : mutation.data
            ? mutation.data.full_name
            : mutation.error instanceof Error
              ? mutation.error.message
              : 'idle'}
      </output>
    </>
  );
}

function ManagementCapabilityProbe({ frame }: { frame: 'admin' | 'steward' }) {
  const capability = useManagementCapability(frame);
  return (
    <output data-testid={`${frame}-capability-state`}>
      {capability.authorityReady && capability.data !== true ? 'ready' : 'closed'}
    </output>
  );
}

function UserSessionProbe() {
  const query = useUserSession();
  return (
    <output data-testid="user-session-state">
      {query.isPending
        ? 'pending'
        : query.error
          ? 'error'
          : (query.data?.user?.username ?? 'anonymous')}
    </output>
  );
}

function AdminSessionProbe() {
  const query = useAdminSession();
  return (
    <output data-testid="admin-session-state">
      {query.isPending
        ? 'pending'
        : query.error
          ? 'error'
          : (query.data?.admin?.username ?? 'anonymous')}
    </output>
  );
}

function StationMutationProbe() {
  const mutation = useMutation({
    mutationFn: async () => ({ marker: 'account-a-mutation-result' }),
  });
  return (
    <>
      <button type="button" onClick={() => mutation.mutate()}>
        run station mutation
      </button>
      <output data-testid="station-mutation-state">{mutation.data?.marker ?? 'idle'}</output>
    </>
  );
}

function ManagementSessionGate({
  frame,
  children,
}: {
  frame: 'admin' | 'steward';
  children: ReactNode;
}) {
  const client = useQueryClient();
  const ready = useQuery({
    queryKey: ['management-session-test-seed', frame],
    queryFn: () => true,
    enabled: false,
  });
  useEffect(() => {
    const generation = beginManagementSessionRequest(client, frame);
    const value =
      frame === 'admin'
        ? { admin: { username: 'fixture-admin' } }
        : { user: { id: '1', username: 'fixture-user', effective_level: 5 } };
    if (!noteManagementSessionSuccess(client, frame, value, generation)) return;
    client.setQueryData(['management-session-test-seed', frame], true);
  }, [client, frame]);
  return ready.data ? <>{children}</> : null;
}

describe('experimental policy and charity controls', () => {
  test('edits the owner-only endpoint key policy and keeps a new secret out of query state', async () => {
    const marker = 'synthetic-secret-123456';
    let currentKey = { ...coreEndpointKey };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/endpoints/1') return jsonResponse(coreEndpoint);
      if (method === 'GET' && path === '/api/endpoints/1/keys?limit=50')
        return jsonResponse(corePage([currentKey]));
      if (method === 'GET' && path === '/api/models?limit=50') return jsonResponse(corePage([]));
      if (method === 'GET' && path === '/api/endpoints/1/keys/2/models?limit=50')
        return jsonResponse(coreCatalogUnknown);
      if (method === 'PATCH' && path === '/api/endpoints/1/keys/2') {
        currentKey = {
          ...currentKey,
          force_store_false: true,
          revision: '2',
          updated_at: 1_700_000_002,
        };
        return jsonResponse(currentKey);
      }
      if (method === 'POST' && path === '/api/endpoints/1/keys') {
        return jsonResponse(
          {
            ...coreEndpointKey,
            id: '4',
            display_head: 'sk-new',
            display_tail: 'tail2',
            note: 'created key',
            force_store_false: true,
          },
          201,
        );
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/1',
    });
    await screen.findByRole('heading', { name: 'Endpoint details' });
    const keyCard = (await screen.findByText('sk-a…tail')).closest('li');
    if (!keyCard) throw new Error('EndpointKey card not found');
    await rendered.user.click(within(keyCard).getByRole('button', { name: 'Require store=false' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'PATCH', '/api/endpoints/1/keys/2')).toEqual({
        force_store_false: true,
        expected_revision: '1',
      }),
    );
    await screen.findByRole('button', { name: 'Stop requiring store=false' });

    await rendered.user.click(screen.getByRole('button', { name: 'Add EndpointKey' }));
    await rendered.user.type(screen.getByLabelText('Upstream secret'), marker);
    await rendered.user.type(screen.getByLabelText('Key note'), 'created key');
    await rendered.user.click(screen.getByLabelText(/I own this credential/));
    await rendered.user.click(screen.getByLabelText('Force store=false'));
    await rendered.user.click(screen.getByRole('button', { name: 'Add key' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'POST', '/api/endpoints/1/keys')).toEqual({
        secret: marker,
        note: 'created key',
        enabled: true,
        force_store_false: true,
        ownership_confirmed: true,
      }),
    );
    await waitFor(() => expect(screen.queryByDisplayValue(marker)).toBeNull());
    expect(assertNoSensitiveQueryCache(rendered.queryClient, [marker]).hitSurfaces).toEqual([]);
  });

  test('does not expose the OpenAI-only store policy action on an Anthropic key', async () => {
    const anthropicEndpoint = { ...coreEndpoint, id: '4', connector_type: 'anthropic-compatible' };
    const anthropicKey = { ...coreEndpointKey, id: '5', endpoint_id: '4' };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: coreSession },
      { method: 'GET', path: '/api/endpoints/4', body: anthropicEndpoint },
      { method: 'GET', path: '/api/endpoints/4/keys?limit=50', body: corePage([anthropicKey]) },
      { method: 'GET', path: '/api/models?limit=50', body: corePage([]) },
      { method: 'GET', path: '/api/endpoints/4/keys/5/models?limit=50', body: coreCatalogUnknown },
    ]);
    const rendered = await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/4',
    });
    await screen.findByRole('heading', { name: 'Endpoint details' });
    expect(screen.queryByRole('button', { name: 'Require store=false' })).toBeNull();
    await rendered.user.click(screen.getByRole('button', { name: 'Add EndpointKey' }));
    expect(screen.queryByLabelText('Force store=false')).toBeNull();
    expect(fetchMock).toHaveBeenCalled();
  });

  test('disables destructive manual catalog actions while routing authority cannot be read', async () => {
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/endpoints/1') return jsonResponse(coreEndpoint);
      if (method === 'GET' && path === '/api/endpoints/1/keys?limit=50') {
        return jsonResponse(corePage([coreEndpointKey]));
      }
      if (method === 'GET' && path === '/api/models?limit=100') {
        return jsonResponse(corePage([coreModel]));
      }
      if (method === 'GET' && path === '/api/models/3/bindings') {
        return jsonResponse(
          { error: { code: 'temporarily_unavailable', message: 'try later' } },
          503,
        );
      }
      if (method === 'GET' && path === '/api/endpoints/1/keys/2/models?limit=50') {
        return jsonResponse(coreManualCatalog);
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/1',
    });

    const entryRow = (await screen.findByText('Vendor/Exact')).closest('li');
    if (!entryRow) throw new Error('Manual catalog row not found');
    await rendered.user.click(within(entryRow).getByRole('button', { name: 'Edit' }));

    expect(await within(entryRow).findByRole('alert')).toHaveTextContent(
      /Binding impact could not be confirmed/,
    );
    const deleteButton = within(entryRow).getByRole('button', {
      name: 'Delete entry atomically',
    });
    const updateButton = within(entryRow).getByRole('button', {
      name: 'Update entry atomically',
    });
    expect(deleteButton).toBeDisabled();
    expect(updateButton).toBeEnabled();

    await rendered.user.clear(within(entryRow).getByLabelText('Exact upstream model ID'));
    await rendered.user.type(
      within(entryRow).getByLabelText('Exact upstream model ID'),
      'Vendor/Changed',
    );
    expect(updateButton).toBeDisabled();
    expect(
      fetchMock.mock.calls.filter((call) => {
        const method = ((call[1] as RequestInit | undefined)?.method ?? 'GET').toUpperCase();
        return method === 'PATCH' || method === 'DELETE';
      }),
    ).toHaveLength(0);
  });

  test('refreshes model authority after a conflict without automatically retrying the mutation', async () => {
    let modelReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50')
        return jsonResponse(corePage([coreModel]));
      if (method === 'GET' && path === '/api/models/3') {
        modelReads += 1;
        return jsonResponse(coreModel);
      }
      if (method === 'GET' && path === '/api/models/3/bindings') {
        return jsonResponse({
          bindings: [coreBinding('10', 'gpt-a', 0), coreBinding('11', 'gpt-b', 1)],
          binding_revision: '2',
        });
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50') return jsonResponse(corePage([]));
      if (method === 'PATCH' && path === '/api/models/3') {
        return jsonResponse({ error: { code: 'conflict', message: 'conflict' } }, 409);
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await rendered.user.click(await screen.findByRole('button', { name: 'Edit logical model' }));
    await rendered.user.click(screen.getByRole('checkbox', { name: 'Flatten tool calls' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await expect(screen.findByText(/This resource changed/)).resolves.toBeVisible();
    await waitFor(() => expect(modelReads).toBeGreaterThan(1));
    expect(
      fetchMock.mock.calls.filter(
        (call) =>
          requestPath(call[0] as string) === '/api/models/3' &&
          (call[1] as RequestInit | undefined)?.method === 'PATCH',
      ),
    ).toHaveLength(1);
  });

  test('adopts a committed model policy after a conflict response and authoritative refetch', async () => {
    let modelReads = 0;
    const committed = {
      ...coreModel,
      flatten_tool_calls: true,
      revision: '2',
      updated_at: 1_700_000_002,
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50')
        return jsonResponse(corePage([modelReads > 0 ? committed : coreModel]));
      if (method === 'GET' && path === '/api/models/3') {
        modelReads += 1;
        return jsonResponse(modelReads === 1 ? coreModel : committed);
      }
      if (method === 'GET' && path === '/api/models/3/bindings') {
        return jsonResponse({
          bindings: [coreBinding('10', 'gpt-a', 0), coreBinding('11', 'gpt-b', 1)],
          binding_revision: '2',
        });
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50') return jsonResponse(corePage([]));
      if (method === 'PATCH' && path === '/api/models/3') {
        return jsonResponse(
          { error: { code: 'conflict', message: 'response lost after commit' } },
          409,
        );
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await rendered.user.click(await screen.findByRole('button', { name: 'Edit logical model' }));
    await rendered.user.click(screen.getByRole('checkbox', { name: 'Flatten tool calls' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(modelReads).toBeGreaterThan(1));
    expect(screen.getByRole('checkbox', { name: 'Flatten tool calls' })).toBeChecked();
  });

  test('refetches key authority after a lost response and keeps the committed store policy', async () => {
    let keyReads = 0;
    const committed = {
      ...coreEndpointKey,
      force_store_false: true,
      revision: '2',
      updated_at: 1_700_000_002,
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/endpoints/1') return jsonResponse(coreEndpoint);
      if (method === 'GET' && path === '/api/endpoints/1/keys?limit=50') {
        keyReads += 1;
        return jsonResponse(corePage([keyReads === 1 ? coreEndpointKey : committed]));
      }
      if (method === 'GET' && path === '/api/models?limit=50') return jsonResponse(corePage([]));
      if (method === 'GET' && path === '/api/endpoints/1/keys/2/models?limit=50')
        return jsonResponse(coreCatalogUnknown);
      if (method === 'PATCH' && path === '/api/endpoints/1/keys/2')
        throw new TypeError('connection reset');
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/1',
    });
    const action = await screen.findByRole('button', { name: 'Require store=false' });
    await rendered.user.click(action);
    await waitFor(() => expect(keyReads).toBeGreaterThan(1));
    expect(await screen.findByRole('button', { name: 'Stop requiring store=false' })).toBeVisible();
    expect(
      fetchMock.mock.calls.filter(
        (call) =>
          requestPath(call[0] as string) === '/api/endpoints/1/keys/2' &&
          (call[1] as RequestInit | undefined)?.method === 'PATCH',
      ),
    ).toHaveLength(1);
  });

  test('fails closed on an invalid model mutation response without polluting authority', async () => {
    let modelReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50')
        return jsonResponse(corePage([coreModel]));
      if (method === 'GET' && path === '/api/models/3') {
        modelReads += 1;
        return jsonResponse(coreModel);
      }
      if (method === 'GET' && path === '/api/models/3/bindings') {
        return jsonResponse({
          bindings: [coreBinding('10', 'gpt-a', 0), coreBinding('11', 'gpt-b', 1)],
          binding_revision: '2',
        });
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50') return jsonResponse(corePage([]));
      if (method === 'PATCH' && path === '/api/models/3') return jsonResponse({ committed: true });
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await rendered.user.click(await screen.findByRole('button', { name: 'Edit logical model' }));
    await rendered.user.click(screen.getByRole('checkbox', { name: 'Flatten tool calls' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await expect(screen.findByText(/The response was lost/)).resolves.toBeVisible();
    expect(modelReads).toBeGreaterThan(1);
    expect(screen.getByRole('button', { name: 'Retry the same operation' })).toBeVisible();
    expect(rendered.queryClient.getQueryData(coreKeys.model('1', '3'))).toMatchObject({
      flatten_tool_calls: false,
      revision: '1',
    });
  });

  test('reconciles a lost endpoint-key create response before offering exact replay', async () => {
    let keyReads = 0;
    let keyPosts = 0;
    const createdKey = {
      ...coreEndpointKey,
      id: '4',
      display_head: 'sk-new',
      display_tail: 'tail2',
      note: 'created key',
    };
    const marker = 'synthetic-key-create-secret-123456';
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/endpoints/1') return jsonResponse(coreEndpoint);
      if (method === 'GET' && path === '/api/endpoints/1/keys?limit=50') {
        keyReads += 1;
        return jsonResponse(
          corePage(keyReads === 1 ? [coreEndpointKey] : [coreEndpointKey, createdKey]),
        );
      }
      if (method === 'GET' && path === '/api/models?limit=50') return jsonResponse(corePage([]));
      if (method === 'GET' && /^\/api\/endpoints\/1\/keys\/(2|4)\/models\?limit=50$/.test(path))
        return jsonResponse(coreCatalogUnknown);
      if (method === 'POST' && path === '/api/endpoints/1/keys') {
        keyPosts += 1;
        return jsonResponse(
          { error: { code: 'internal', message: 'safe uncertain response' } },
          503,
        );
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/1',
    });
    await screen.findByText('sk-a…tail');
    await rendered.user.click(screen.getByRole('button', { name: 'Add EndpointKey' }));
    await rendered.user.type(screen.getByLabelText('Upstream secret'), marker);
    await rendered.user.type(screen.getByLabelText('Key note'), 'created key');
    await rendered.user.click(screen.getByLabelText(/I own this credential/));
    await rendered.user.click(screen.getByRole('button', { name: 'Add key' }));
    await screen.findByText('sk-new…tail2');
    await waitFor(() => expect(keyReads).toBeGreaterThan(1));
    expect(screen.getByRole('button', { name: 'Retry the same operation' })).toBeVisible();
    expect(keyPosts).toBe(1);
    expect(assertNoSensitiveQueryCache(rendered.queryClient, [marker]).hitSurfaces).toEqual([]);
  });

  test('reconciles a lost personal-model create response before offering exact replay', async () => {
    let modelReads = 0;
    let modelPosts = 0;
    const createdModel = {
      ...coreModel,
      id: '4',
      provider: 'created-provider',
      model: 'created-model',
      full_name: 'created-provider/created-model',
      revision: '1',
      binding_revision: '0',
      binding_count: '0',
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50') {
        modelReads += 1;
        return jsonResponse(corePage(modelReads === 1 ? [coreModel] : [coreModel, createdModel]));
      }
      if (method === 'POST' && path === '/api/models') {
        modelPosts += 1;
        return jsonResponse(
          { error: { code: 'internal', message: 'safe uncertain response' } },
          503,
        );
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(screen.getByRole('button', { name: 'Create logical model' }));
    await rendered.user.type(screen.getByLabelText('Provider'), 'created-provider');
    await rendered.user.type(screen.getByLabelText('Model'), 'created-model');
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('created-provider/created-model');
    await waitFor(() => expect(modelReads).toBeGreaterThan(1));
    expect(screen.getByRole('button', { name: 'Retry the same operation' })).toBeVisible();
    expect(modelPosts).toBe(1);
  });

  test('handles an Anthropic binding conflict as a frozen 409 with authoritative reconciliation', async () => {
    const anthropicEndpoint = { ...coreEndpoint, id: '4', connector_type: 'anthropic-compatible' };
    const anthropicKey = { ...coreEndpointKey, id: '5', endpoint_id: '4' };
    const anthropicModel = {
      ...coreModel,
      flatten_tool_calls: true,
      binding_revision: '0',
      binding_count: '0',
    };
    const candidate = {
      endpoint_key_id: '5',
      endpoint_base_url: anthropicEndpoint.base_url,
      connector_type: 'anthropic-compatible',
      endpoint_note: anthropicEndpoint.note,
      endpoint_key_display_head: anthropicKey.display_head,
      endpoint_key_display_tail: anthropicKey.display_tail,
      endpoint_key_note: anthropicKey.note,
      upstream_model_id: 'claude-3',
      source_types: ['automatic'],
    };
    let bindingReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50')
        return jsonResponse(corePage([anthropicModel]));
      if (method === 'GET' && path === '/api/models/3') return jsonResponse(anthropicModel);
      if (method === 'GET' && path === '/api/models/3/bindings') {
        bindingReads += 1;
        return jsonResponse({ bindings: [], binding_revision: '0' });
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50')
        return jsonResponse(corePage([anthropicEndpoint]));
      if (method === 'GET' && path === '/api/endpoints/4/keys?limit=50')
        return jsonResponse(corePage([anthropicKey]));
      if (method === 'GET' && path.startsWith('/api/models/3/binding-candidates?')) {
        const params = new URL(path, window.location.origin).searchParams;
        return jsonResponse(corePage(params.get('source') === 'automatic' ? [candidate] : []));
      }
      if (method === 'POST' && path === '/api/models/3/bindings/batch') {
        return jsonResponse(
          { error: { code: 'conflict', message: 'binding policy conflict' } },
          409,
        );
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await rendered.user.click(
      await screen.findByRole('button', { name: /Anthropic-compatible.*upstream\.test/ }),
    );
    await rendered.user.click(await screen.findByRole('button', { name: /sk-a…tail/ }));
    await rendered.user.click(await screen.findByRole('button', { name: /claude-3/ }));
    await rendered.user.click(screen.getByRole('button', { name: 'Add 1 selected binding(s)' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'POST', '/api/models/3/bindings/batch')).toEqual({
        expected_binding_revision: '0',
        selections: [{ endpoint_key_id: '5', upstream_model_id: 'claude-3' }],
      }),
    );
    await waitFor(() => expect(bindingReads).toBeGreaterThan(1));
    expect(screen.getByRole('button', { name: 'Add 1 selected binding(s)' })).toBeEnabled();
    expect(
      fetchMock.mock.calls.filter(
        (call) =>
          requestPath(call[0] as string) === '/api/models/3/bindings/batch' &&
          (call[1] as RequestInit | undefined)?.method === 'POST',
      ),
    ).toHaveLength(1);
  });

  test('sends the complete binding order and the revision-bound logical-model policy', async () => {
    let currentModel = { ...coreModel };
    let bindings = [coreBinding('10', 'gpt-a', 0), coreBinding('11', 'gpt-b', 1)];
    let bindingRevision = '2';
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50')
        return jsonResponse(corePage([currentModel]));
      if (method === 'GET' && path === '/api/models/3') return jsonResponse(currentModel);
      if (method === 'GET' && path === '/api/models/3/bindings')
        return jsonResponse({ bindings, binding_revision: bindingRevision });
      if (method === 'GET' && path === '/api/endpoints?limit=50') return jsonResponse(corePage([]));
      if (method === 'PUT' && path === '/api/models/3/bindings/order') {
        bindings = [coreBinding('11', 'gpt-b', 0), coreBinding('10', 'gpt-a', 1)];
        bindingRevision = '3';
        return jsonResponse({ bindings, binding_revision: bindingRevision });
      }
      if (method === 'PATCH' && path === '/api/models/3') {
        currentModel = {
          ...currentModel,
          flatten_tool_calls: true,
          revision: '2',
          updated_at: 1_700_000_002,
        };
        return jsonResponse(currentModel);
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await rendered.user.click((await screen.findAllByRole('button', { name: 'Move down' }))[0]);
    await rendered.user.click(screen.getByRole('button', { name: 'Save complete order' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'PUT', '/api/models/3/bindings/order')).toEqual({
        expected_binding_revision: '2',
        order: ['11', '10'],
      }),
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Edit logical model' }));
    await rendered.user.click(screen.getByRole('checkbox', { name: 'Flatten tool calls' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'PATCH', '/api/models/3')).toEqual({
        provider: 'provider',
        model: 'model',
        route_strategy: 'ordered',
        silent_retry: false,
        flatten_tool_calls: true,
        expected_revision: '1',
      }),
    );
  });

  test('keeps authoritative binding deletion available when the candidate endpoint page is empty', async () => {
    const binding = coreBinding('10', 'gpt-a', 0);
    const oneBindingModel = { ...coreModel, binding_count: '1' };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: coreSession },
      { method: 'GET', path: '/api/models?limit=50', body: corePage([oneBindingModel]) },
      { method: 'GET', path: '/api/models/3', body: oneBindingModel },
      {
        method: 'GET',
        path: '/api/models/3/bindings',
        body: { bindings: [binding], binding_revision: '2' },
      },
      { method: 'GET', path: '/api/endpoints?limit=50', body: corePage([]) },
      {
        method: 'DELETE',
        path: '/api/models/3/bindings/10',
        body: { bindings: [], binding_revision: '3' },
      },
    ]);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    const remove = await screen.findByRole('button', { name: 'Remove binding' });
    expect(remove).toBeEnabled();
    await rendered.user.click(remove);
    const dialog = screen.getByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Remove binding' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'DELETE', '/api/models/3/bindings/10')).toEqual({
        expected_binding_revision: '2',
      }),
    );
    await waitFor(() => expect(screen.queryByText('gpt-a')).toBeNull());
  });

  test('submits only an owned existing key through the closed beta.1 donation wire', async () => {
    const capability = {
      state: 'available',
      donation_intake: 'open',
      models: [
        {
          id: '7',
          provider: 'provider',
          model: 'charity-model',
          full_name: '[公益]provider/charity-model',
        },
      ],
    };
    const donation = {
      id: '9',
      status: 'pending',
      revision: '1',
      description: 'fixture donation',
      review_result: null,
      expires_at: null,
      keys: [
        {
          id: '6',
          endpoint_key_id: '2',
          display_head: 'sk-a',
          display_tail: 'tail',
          safe_source: {
            base_url: coreEndpoint.base_url,
            connector_type: coreEndpoint.connector_type,
          },
          physical_enabled: false,
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
          streak: { generation: '1', count: '0', failure_disabled: false },
          ended_reason: null,
        },
      ],
      created_at: 1_700_000_000,
      updated_at: 1_700_000_000,
    };
    let submitted = false;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(session);
      if (method === 'GET' && path === '/api/charity/models') return jsonResponse(capability);
      if (method === 'GET' && path === '/api/donations?limit=100') {
        return jsonResponse(corePage(submitted ? [donation] : []));
      }
      if (method === 'GET' && path === '/api/endpoints?limit=100') {
        return jsonResponse(corePage([coreEndpoint]));
      }
      if (method === 'GET' && path === '/api/endpoints/1/keys?limit=100') {
        return jsonResponse(corePage([coreEndpointKey]));
      }
      if (method === 'POST' && path === '/api/donations') {
        submitted = true;
        return jsonResponse(donation);
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const rendered = await renderWithProviders(<CharityPage />, {
      station: 'user',
      role: 'user',
    });
    await screen.findByText('[公益]provider/charity-model');
    await rendered.user.click(await screen.findByRole('checkbox', { name: /sk-a…tail/ }));
    await rendered.user.type(screen.getByLabelText('Donation description'), 'fixture donation');
    await rendered.user.click(
      screen.getByRole('checkbox', {
        name: 'I own every selected resource and am authorized to contribute its capacity.',
      }),
    );
    expect(screen.queryByPlaceholderText(/new key/i)).toBeNull();
    expect(screen.queryByPlaceholderText(/base url/i)).toBeNull();
    await rendered.user.click(screen.getByRole('button', { name: 'Submit for review' }));

    await waitFor(() =>
      expect(lastBody(fetchMock, 'POST', '/api/donations')).toEqual({
        description: 'fixture donation',
        endpoint_key_ids: ['2'],
        ownership_authorized: true,
      }),
    );
    expect(JSON.stringify(lastBody(fetchMock, 'POST', '/api/donations'))).not.toMatch(
      /secret|base_url|max_concurrency|rpm_limit/i,
    );
    await expect(screen.findByText('fixture donation')).resolves.toBeVisible();
  });

  test('fails closed when the charity capability omits donation intake', async () => {
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(session);
      if (method === 'GET' && path === '/api/charity/models') {
        return jsonResponse({ state: 'no_models', models: [] });
      }
      if (method === 'GET' && path === '/api/donations?limit=100') {
        return jsonResponse(corePage([]));
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    const alerts = await screen.findAllByRole('alert');
    expect(alerts.some((alert) => /invalid response/i.test(alert.textContent ?? ''))).toBe(true);
    expect(screen.queryByText('Submit a charity donation')).toBeNull();
    expect(
      fetchMock.mock.calls.some((call) =>
        requestPath(call[0] as string).startsWith('/api/endpoints'),
      ),
    ).toBe(false);
  });

  test('fails closed when an OpenAI key response omits its required store policy', async () => {
    const malformedKey = { ...coreEndpointKey } as Record<string, unknown>;
    delete malformedKey.force_store_false;
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: coreSession },
      { method: 'GET', path: '/api/endpoints/1', body: coreEndpoint },
      { method: 'GET', path: '/api/endpoints/1/keys?limit=50', body: corePage([malformedKey]) },
    ]);
    await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/1',
    });
    await expect(screen.findByRole('alert')).resolves.toBeVisible();
  });

  test('fails closed when a binding order exceeds the frozen range', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: coreSession },
      { method: 'GET', path: '/api/models?limit=50', body: corePage([coreModel]) },
      { method: 'GET', path: '/api/models/3', body: coreModel },
      {
        method: 'GET',
        path: '/api/models/3/bindings',
        body: {
          bindings: [{ ...coreBinding('10', 'gpt-a', 0), ord: 1_000_001 }],
          binding_revision: '2',
        },
      },
      { method: 'GET', path: '/api/endpoints?limit=50', body: corePage([]) },
    ]);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await expect(screen.findByRole('alert')).resolves.toBeVisible();
  });

  test('rejects a non-empty invalid reviewer expiry and sends no PATCH', async () => {
    const listDonation = {
      id: 20,
      user_id: 1,
      endpoint_base_url: endpoint.base_url,
      status: 'pending',
      enabled: false,
      description: 'review expiry fixture',
      review_note: '',
      created_at: 1,
      updated_at: 2,
    };
    const detailDonation = { ...listDonation, expires_at: 1700000000, keys: [], reviews: [] };
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/admin/api/donations?page=1&page_size=20&status=pending',
        body: { data: [listDonation], has_more: false, total: 1 },
      },
      { method: 'GET', path: '/admin/api/donations/20', body: detailDonation },
      {
        method: 'GET',
        path: '/admin/api/charity-models?page=1&page_size=100',
        body: { data: [], has_more: false, total: 0 },
      },
      {
        method: 'GET',
        path: '/admin/api/site-config',
        body: {
          charity_enabled: true,
          donation_accept_enabled: true,
          charity_token_reserve_milli: null,
        },
      },
      { method: 'PATCH', path: '/admin/api/donations/20', body: detailDonation },
    ]);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin">
        <CharityManagement frame="admin" />
      </ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByRole('heading', { name: 'Donation review queue' });
    await screen.findByText('review expiry fixture');
    const expiry = document.querySelector<HTMLInputElement>('input[type="datetime-local"]');
    if (!expiry) throw new Error('review expiry control not found');
    Object.defineProperty(expiry, 'value', {
      configurable: true,
      writable: true,
      value: 'not-a-date',
    });
    fireEvent.change(expiry);
    await rendered.user.click(screen.getByRole('button', { name: 'Approve donation' }));
    await expect(
      screen.findByText('Enter a valid expiry date, or leave it blank to clear the expiry.'),
    ).resolves.toBeVisible();
    expect(
      fetchMock.mock.calls.filter((call) => {
        const requestURL = new URL(String(call[0]), window.location.origin);
        const requestInit = call[1] as RequestInit | undefined;
        return requestURL.pathname === '/admin/api/donations/20' && requestInit?.method === 'PATCH';
      }),
    ).toHaveLength(0);
    Object.defineProperty(expiry, 'value', { configurable: true, writable: true, value: '' });
    fireEvent.change(expiry);
    await rendered.user.click(screen.getByRole('button', { name: 'Approve donation' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'PATCH', '/admin/api/donations/20')).toMatchObject({
        action: 'approve',
        expires_at: null,
      }),
    );
  });

  test('keeps management actions disabled on detail failure and enables them after retry', async () => {
    const listDonation = {
      id: 21,
      user_id: 1,
      endpoint_base_url: endpoint.base_url,
      status: 'pending',
      enabled: false,
      description: 'detail retry fixture',
      review_note: '',
      created_at: 1,
      updated_at: 2,
    };
    const detailDonation = { ...listDonation, expires_at: null, keys: [], reviews: [] };
    let detailReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && requestURL.pathname === '/admin/api/donations')
        return jsonResponse({ data: [listDonation], has_more: false, total: 1 });
      if (method === 'GET' && requestURL.pathname === '/admin/api/donations/21') {
        detailReads += 1;
        return detailReads === 1
          ? jsonResponse(
              { error: { code: 'service_unavailable', message: 'detail temporarily unavailable' } },
              503,
            )
          : jsonResponse(detailDonation);
      }
      if (method === 'GET' && requestURL.pathname === '/admin/api/charity-models')
        return jsonResponse({ data: [], has_more: false, total: 0 });
      if (method === 'GET' && requestURL.pathname === '/admin/api/site-config')
        return jsonResponse({
          charity_enabled: true,
          donation_accept_enabled: true,
          charity_token_reserve_milli: null,
        });
      if (method === 'PATCH' && requestURL.pathname === '/admin/api/donations/21')
        return jsonResponse(detailDonation);
      throw new Error(
        `Unexpected fixture request: ${method} ${requestURL.pathname}${requestURL.search}`,
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin">
        <CharityManagement frame="admin" />
      </ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByText('detail retry fixture');
    const approve = screen.getByRole('button', { name: 'Approve donation' });
    expect(approve).toBeDisabled();
    await waitFor(() => expect(detailReads).toBeGreaterThan(0));
    await rendered.user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(detailReads).toBe(2));
    await waitFor(() => expect(approve).toBeEnabled());
    await rendered.user.click(approve);
    await waitFor(() =>
      expect(lastBody(fetchMock, 'PATCH', '/admin/api/donations/21')).toMatchObject({
        action: 'approve',
      }),
    );
  });

  test.each([
    {
      frame: 'admin' as const,
      basePath: '/admin/api',
      station: 'admin' as const,
      role: 'admin' as const,
    },
    {
      frame: 'steward' as const,
      basePath: '/api/steward',
      station: 'user' as const,
      role: 'level5' as const,
    },
  ])(
    'allows the $frame management role to create and update charity flatten policy',
    async ({ frame, basePath, station, role }) => {
      const fetchMock = installJsonFetchFixtures([
        {
          method: 'GET',
          path: `${basePath}/donations?page=1&page_size=20&status=pending`,
          body: { data: [], has_more: false, total: 0 },
        },
        {
          method: 'GET',
          path: `${basePath}/charity-models?page=1&page_size=100`,
          body: { data: [managedModel], has_more: false, total: 1 },
        },
        {
          method: 'GET',
          path: `${basePath}/charity-models/7/bindings`,
          body: {
            data: [
              {
                id: 10,
                charity_model_id: 7,
                donation_key_id: 6,
                upstream_model_id: 'gpt-a',
                ord: 0,
                endpoint_base_url: endpoint.base_url,
                key_display_head: 'sk-a',
                key_display_tail: 'tail',
                donation_key_enabled: true,
              },
            ],
          },
        },
        ...(frame === 'admin'
          ? [
              {
                method: 'GET',
                path: '/admin/api/site-config',
                body: {
                  charity_enabled: true,
                  donation_accept_enabled: true,
                  charity_token_reserve_milli: null,
                },
              },
            ]
          : []),
        {
          method: 'PATCH',
          path: `${basePath}/charity-models/7`,
          body: { ...managedModel, flatten_tool_calls: true },
        },
        {
          method: 'POST',
          path: `${basePath}/charity-models`,
          body: {
            ...managedModel,
            id: 8,
            provider: 'new-provider',
            model: 'new-model',
            full_name: 'new-provider/new-model',
            flatten_tool_calls: true,
          },
        },
      ]);
      const rendered = await renderWithProviders(
        <ManagementSessionGate frame={frame}>
          <CharityManagement frame={frame} />
        </ManagementSessionGate>,
        { station, role },
      );
      await screen.findByRole('heading', { name: 'Donation review queue' });
      await screen.findByText('provider/charity-model');
      await rendered.user.click(screen.getByText('Bindings', { exact: true }));
      await expect(screen.findByText('sk-a…tail')).resolves.toBeVisible();
      await rendered.user.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
      const editForm = rendered.container.querySelector('form.charity-editor');
      if (!editForm) throw new Error('Missing charity model editor');
      await rendered.user.click(
        within(editForm as HTMLElement).getByRole('checkbox', {
          name: 'Experimental: flatten tool calls',
        }),
      );
      await rendered.user.click(
        within(editForm as HTMLElement).getByRole('button', { name: 'Save' }),
      );
      await waitFor(() =>
        expect(lastBody(fetchMock, 'PATCH', `${basePath}/charity-models/7`)).toMatchObject({
          flatten_tool_calls: true,
        }),
      );

      await rendered.user.click(screen.getByRole('button', { name: 'Add charity model' }));
      const createForm = rendered.container.querySelector('form.charity-editor');
      if (!createForm) throw new Error('Missing charity model creation form');
      await rendered.user.type(
        within(createForm as HTMLElement).getByLabelText('Provider'),
        'new-provider',
      );
      await rendered.user.type(
        within(createForm as HTMLElement).getByLabelText('Model'),
        'new-model',
      );
      await rendered.user.click(
        within(createForm as HTMLElement).getByRole('checkbox', {
          name: 'Experimental: flatten tool calls',
        }),
      );
      await rendered.user.click(
        within(createForm as HTMLElement).getByRole('button', { name: 'Save' }),
      );
      await waitFor(() =>
        expect(lastBody(fetchMock, 'POST', `${basePath}/charity-models`)).toMatchObject({
          flatten_tool_calls: true,
        }),
      );
    },
  );

  test('keeps management controls hidden after a mutation 403 even if the sensitive root is evicted', async () => {
    // A sparse response must not borrow identity fields from the stale cache
    // and accidentally reopen the capability latch.
    const revokedSession = { user: { effective_level: 5 } };
    const fetchMock = installJsonFetchFixtures([
      {
        method: 'GET',
        path: '/api/steward/donations?page=1&page_size=20&status=pending',
        body: { data: [], has_more: false, total: 0 },
      },
      {
        method: 'GET',
        path: '/api/steward/charity-models?page=1&page_size=100',
        body: { data: [managedModel], has_more: false, total: 1 },
      },
      { method: 'GET', path: '/api/steward/charity-models/7/bindings', body: { data: [] } },
      {
        method: 'PATCH',
        path: '/api/steward/charity-models/7',
        body: { error: { code: 'forbidden', message: 'capability revoked' } },
        status: 403,
      },
      // A stale/insufficient authoritative session must keep the external
      // capability latch closed after the write is rejected.
      { method: 'GET', path: '/api/session', body: revokedSession },
    ]);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="steward">
        <CharityManagement frame="steward" />
      </ManagementSessionGate>,
      { station: 'user', role: 'level5' },
    );
    await screen.findByText('provider/charity-model');
    await rendered.user.click(screen.getByRole('button', { name: 'Edit' }));
    const editForm = rendered.container.querySelector('form.charity-editor');
    if (!editForm) throw new Error('Missing charity model editor');
    await rendered.user.click(
      within(editForm as HTMLElement).getByRole('checkbox', {
        name: 'Experimental: flatten tool calls',
      }),
    );
    await rendered.user.click(
      within(editForm as HTMLElement).getByRole('button', { name: 'Save' }),
    );

    await waitFor(() => {
      expect(screen.getByText(/capability is no longer active/i)).toBeVisible();
      expect(
        screen.queryByRole('checkbox', { name: 'Experimental: flatten tool calls' }),
      ).toBeNull();
      expect(screen.queryByRole('button', { name: 'Add charity model' })).toBeNull();
    });
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('steward'))).toBe(
      true,
    );

    // The normal forbidden effect evicts the management root.  The revoke
    // sentinel is deliberately outside that root and must not recreate a
    // writable view while the session refresh is still in flight.
    rendered.queryClient.removeQueries({ queryKey: charityManagementKeys.root('steward') });
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('steward'))).toBe(
      true,
    );
    expect(screen.queryByRole('button', { name: 'Add charity model' })).toBeNull();

    // Cache invalidation must not refetch the local placeholder and reopen
    // controls after an authoritative rejection. A separately successful,
    // identity-checked session refresh is the only recovery path.
    await rendered.queryClient.invalidateQueries({
      queryKey: charityManagementKeys.capability('steward'),
    });
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('steward'))).toBe(
      true,
    );
    expect(screen.queryByRole('button', { name: 'Add charity model' })).toBeNull();

    expect(
      fetchMock.mock.calls.some((call) => {
        const requestURL = new URL(String(call[0]), window.location.origin);
        return requestURL.pathname === '/api/session';
      }),
    ).toBe(true);
  });

  test.each([
    {
      frame: 'admin' as const,
      basePath: '/admin/api',
      station: 'admin' as const,
      role: 'admin' as const,
    },
    {
      frame: 'steward' as const,
      basePath: '/api/steward',
      station: 'user' as const,
      role: 'level5' as const,
    },
  ])(
    'shows the physical-key store policy read-only for the $frame review role and preserves donation-key ids',
    async ({ frame, basePath, station, role }) => {
      const listDonation = {
        id: 9,
        user_id: 1,
        endpoint_base_url: endpoint.base_url,
        status: 'pending',
        enabled: false,
        description: 'review fixture',
        review_note: '',
        created_at: 1,
        updated_at: 2,
      };
      const detailDonation = {
        ...listDonation,
        keys: [
          {
            id: 6,
            endpoint_key_id: 2,
            display_head: 'sk-a',
            display_tail: 'tail',
            max_concurrency: 2,
            rpm_limit: 30,
            credits_usage_cap_milli: '1000',
            credits_used_milli: '10',
            credits_reserved_milli: '0',
            enabled: true,
            force_store_false: true,
          },
        ],
        reviews: [],
      };
      const fetchMock = installJsonFetchFixtures([
        {
          method: 'GET',
          path: `${basePath}/donations?page=1&page_size=20&status=pending`,
          body: { data: [listDonation], has_more: false, total: 1 },
        },
        { method: 'GET', path: `${basePath}/donations/9`, body: detailDonation },
        {
          method: 'GET',
          path: `${basePath}/charity-models?page=1&page_size=100`,
          body: { data: [], has_more: false, total: 0 },
        },
        ...(frame === 'admin'
          ? [
              {
                method: 'GET',
                path: '/admin/api/site-config',
                body: {
                  charity_enabled: false,
                  donation_accept_enabled: false,
                  charity_token_reserve_milli: null,
                },
              },
            ]
          : []),
        { method: 'PATCH', path: `${basePath}/donations/9`, body: detailDonation },
      ]);
      const rendered = await renderWithProviders(
        <ManagementSessionGate frame={frame}>
          <CharityManagement frame={frame} />
        </ManagementSessionGate>,
        { station, role },
      );
      await screen.findByRole('heading', { name: 'Donation review queue' });
      expect(
        await screen.findByText(
          /Upstream prompt storage policy \(read-only\): Require upstream not to store prompts \(Experimental\)/,
        ),
      ).toBeVisible();
      await rendered.user.click(screen.getByRole('button', { name: 'Save key limits' }));
      await waitFor(() =>
        expect(lastBody(fetchMock, 'PATCH', `${basePath}/donations/9`)).toEqual({
          action: 'update',
          expires_at: null,
          keys: [
            {
              id: 6,
              max_concurrency: 2,
              rpm_limit: 30,
              credits_usage_cap_milli: '1000',
              enabled: true,
            },
          ],
        }),
      );
      expect(lastBody(fetchMock, 'PATCH', `${basePath}/donations/9`)).not.toHaveProperty(
        'keys.0.force_store_false',
      );

      const maxConcurrency = screen.getByLabelText('Max concurrency (0 = unlimited)');
      const rpmLimit = screen.getByLabelText('Requests per minute (0 = unlimited)');
      await rendered.user.clear(maxConcurrency);
      await rendered.user.click(screen.getByRole('button', { name: 'Save key limits' }));
      await waitFor(() =>
        expect(lastBody(fetchMock, 'PATCH', `${basePath}/donations/9`)).toEqual({
          action: 'update',
          expires_at: null,
          keys: [{ id: 6, rpm_limit: 30, credits_usage_cap_milli: '1000', enabled: true }],
        }),
      );
      await rendered.user.clear(rpmLimit);
      await rendered.user.type(rpmLimit, '0');
      await rendered.user.click(screen.getByRole('button', { name: 'Save key limits' }));
      await waitFor(() =>
        expect(lastBody(fetchMock, 'PATCH', `${basePath}/donations/9`)).toEqual({
          action: 'update',
          expires_at: null,
          keys: [{ id: 6, rpm_limit: 0, credits_usage_cap_milli: '1000', enabled: true }],
        }),
      );
      const usageCap = screen.getByLabelText('Usage cap (milli-credits; 0 = unlimited)');
      await rendered.user.clear(usageCap);
      await rendered.user.type(usageCap, '9223372036854775808');
      await rendered.user.click(screen.getByRole('button', { name: 'Save key limits' }));
      await expect(
        screen.findByText(/Leave a reviewer limit blank to omit it/i),
      ).resolves.toBeVisible();
    },
  );

  test('clears level-5 steward data after a server-forced demotion', async () => {
    const levelFive = { ...session, user: { ...session.user, effective_level: 5 } };
    const demoted = { ...session, user: { ...session.user, effective_level: 4 } };
    let sessionCalls = 0;
    let revoke = false;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (method === 'GET' && requestURL.pathname === '/api/session') {
        const body = sessionCalls++ === 0 ? levelFive : demoted;
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/logs') {
        if (!revoke)
          return new Response(JSON.stringify({ data: [], has_more: false, total: 0 }), {
            status: 200,
            headers: { 'content-type': 'application/json' },
          });
        return new Response(
          JSON.stringify({ error: { code: 'forbidden', message: 'steward capability revoked' } }),
          {
            status: 403,
            headers: { 'content-type': 'application/json' },
          },
        );
      }
      throw new Error(
        `Unregistered test request: ${method} ${requestURL.pathname}${requestURL.search}`,
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<StewardPage />, {
      station: 'user',
      role: 'level5',
    });
    await screen.findByText('No request logs');
    rendered.queryClient.setQueryData(charityManagementKeys.models('steward'), [managedModel]);
    revoke = true;
    await rendered.queryClient.invalidateQueries({ queryKey: userKeys.stewardLogsRoot });
    await expect(
      screen.findByText(/requires the server-resolved level 5 capability/i),
    ).resolves.toBeVisible();
    expect(sessionCalls).toBeGreaterThanOrEqual(2);
    expect(
      rendered.queryClient.getQueryData(charityManagementKeys.models('steward')),
    ).toBeUndefined();
    expect(
      rendered.queryClient
        .getQueryCache()
        .getAll()
        .filter((query) => query.queryKey[1] === 'steward-logs')
        .every((query) => query.state.data === undefined),
    ).toBe(true);
  });

  test('reopens the same steward user after a newer authoritative session success', async () => {
    const levelFive = { ...session, user: { ...session.user, effective_level: 5 } };
    let sessionCalls = 0;
    let logCalls = 0;
    let revoke = false;
    let releaseRefresh!: () => void;
    const refreshGate = new Promise<void>((resolve) => {
      releaseRefresh = resolve;
    });
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (method === 'GET' && requestURL.pathname === '/api/session') {
        sessionCalls += 1;
        if (sessionCalls > 1) await refreshGate;
        return jsonResponse(levelFive);
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/logs') {
        logCalls += 1;
        if (revoke && logCalls === 2) {
          return jsonResponse(
            { error: { code: 'forbidden', message: 'temporary capability rejection' } },
            403,
          );
        }
        return jsonResponse({ data: [], has_more: false, total: 0 });
      }
      throw new Error(
        `Unregistered test request: ${method} ${requestURL.pathname}${requestURL.search}`,
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<StewardPage />, {
      station: 'user',
      role: 'level5',
    });
    await screen.findByText('No request logs');
    revoke = true;
    await rendered.queryClient.invalidateQueries({ queryKey: userKeys.stewardLogsRoot });
    await expect(
      screen.findByText(/requires the server-resolved level 5 capability/i),
    ).resolves.toBeVisible();
    await waitFor(() => expect(sessionCalls).toBeGreaterThanOrEqual(2));
    releaseRefresh();
    await waitFor(() =>
      expect(screen.queryByText(/requires the server-resolved level 5 capability/i)).toBeNull(),
    );
    expect(screen.getByText('No request logs')).toBeVisible();
  });

  test('clears charity management data and removes write controls after an L5 downgrade', async () => {
    const levelFive = { ...session, user: { ...session.user, effective_level: 5 } };
    const demoted = { ...session, user: { ...session.user, effective_level: 4 } };
    let sessionCalls = 0;
    let demote = false;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (method === 'GET' && requestURL.pathname === '/api/session') {
        sessionCalls += 1;
        return new Response(JSON.stringify(demote ? demoted : levelFive), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/logs') {
        return new Response(JSON.stringify({ data: [], has_more: false, total: 0 }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/donations') {
        return new Response(JSON.stringify({ data: [], has_more: false, total: 0 }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/charity-models') {
        return new Response(JSON.stringify({ data: [managedModel], has_more: false, total: 1 }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/charity-models/7/bindings') {
        return new Response(JSON.stringify({ data: [] }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      throw new Error(
        `Unregistered test request: ${method} ${requestURL.pathname}${requestURL.search}`,
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<StewardPage />, {
      station: 'user',
      role: 'level5',
    });
    await screen.findByText('No request logs');
    await rendered.user.click(screen.getByRole('tab', { name: 'Charity management' }));
    await screen.findByText('provider/charity-model');
    expect(
      rendered.queryClient.getQueryData(charityManagementKeys.models('steward')),
    ).toBeDefined();

    demote = true;
    await rendered.queryClient.invalidateQueries({ queryKey: userKeys.session });
    await screen.findByText(
      'This co-management surface requires the server-resolved level 5 capability.',
    );
    expect(
      rendered.queryClient.getQueryData(charityManagementKeys.models('steward')),
    ).toBeUndefined();
    expect(screen.queryByRole('checkbox', { name: 'Experimental: flatten tool calls' })).toBeNull();
    expect(
      fetchMock.mock.calls.some((call) => {
        const requestURL = new URL(String(call[0]), window.location.origin);
        const requestInit = call[1] as RequestInit | undefined;
        return requestInit?.method === 'PATCH' && requestURL.pathname.includes('/charity-models/');
      }),
    ).toBe(false);
    expect(sessionCalls).toBeGreaterThanOrEqual(2);
  });

  test('binds recovery to the exact subject and generation, including same-level switches', async () => {
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="steward">
        <span data-testid="session-gate-ready">ready</span>
      </ManagementSessionGate>,
      { station: 'user', role: 'level5' },
    );
    await screen.findByTestId('session-gate-ready');
    const client = rendered.queryClient;
    const staleGeneration = 1;
    client.setQueryData(charityManagementKeys.capability('steward'), true);
    const nextGeneration = beginManagementSessionRequest(client, 'steward');
    expect(noteManagementSessionSuccess(client, 'steward', session, staleGeneration)).toBe(false);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    const otherUser = {
      user: { ...session.user, id: '2', username: 'other-user', effective_level: 5 },
    };
    expect(noteManagementSessionSuccess(client, 'steward', otherUser, nextGeneration)).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);

    const downgradeGeneration = beginManagementSessionRequest(client, 'steward');
    expect(
      noteManagementSessionSuccess(
        client,
        'steward',
        {
          user: { ...otherUser.user, effective_level: 4 },
        },
        downgradeGeneration,
      ),
    ).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    const reauthGeneration = beginManagementSessionRequest(client, 'steward');
    expect(noteManagementSessionSuccess(client, 'steward', otherUser, reauthGeneration)).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);
  });

  test('binds administrator recovery to username rather than an administrator role', async () => {
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin">
        <span data-testid="session-gate-ready">ready</span>
      </ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByTestId('session-gate-ready');
    const client = rendered.queryClient;
    client.setQueryData(charityManagementKeys.capability('admin'), true);
    const nextGeneration = beginManagementSessionRequest(client, 'admin');
    expect(
      noteManagementSessionSuccess(client, 'admin', { admin: { username: 'fixture-admin' } }, 1),
    ).toBe(false);
    expect(client.getQueryData(charityManagementKeys.capability('admin'))).toBe(true);
    expect(
      noteManagementSessionSuccess(
        client,
        'admin',
        { admin: { username: 'other-admin' } },
        nextGeneration,
      ),
    ).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('admin'))).toBe(false);
  });

  test('rejects a bindings response with fields outside the exact data envelope', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ data: [], extra: true }));
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin">
        <ManagementBindingsProbe />
      </ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await expect(screen.findByText(/invalid charity bindings list data/i)).resolves.toBeVisible();
    expect(
      rendered.queryClient.getQueryData(charityManagementKeys.bindings('admin', '7')),
    ).toBeUndefined();
  });

  test('drops a management read that returns after the session generation changes', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (requestURL.pathname === '/admin/api/charity-models/7/bindings') {
        await responseGate;
        return jsonResponse({ data: [] });
      }
      throw new Error(`Unexpected fixture request: ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin">
        <ManagementBindingsProbe />
      </ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(0));
    const nextGeneration = beginManagementSessionRequest(rendered.queryClient, 'admin');
    expect(nextGeneration).toBe(2);
    release();
    await expect(screen.findByText(/management session changed/i)).resolves.toBeVisible();
    expect(
      rendered.queryClient.getQueryState(charityManagementKeys.bindings('admin', '7'))?.data,
    ).toBeUndefined();
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('admin'))).not.toBe(
      true,
    );
  });

  test('does not cache or reconcile a management mutation response from an old subject', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (requestURL.pathname === '/admin/api/charity-models' && init?.method === 'POST') {
        await responseGate;
        return jsonResponse(managedModel);
      }
      throw new Error(
        `Unexpected fixture request: ${init?.method ?? 'GET'} ${requestURL.pathname}`,
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin">
        <ManagedModelMutationProbe />
      </ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByRole('button', { name: 'run mutation' });
    await rendered.user.click(screen.getByRole('button', { name: 'run mutation' }));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBe(1));
    const nextGeneration = beginManagementSessionRequest(rendered.queryClient, 'admin');
    expect(
      noteManagementSessionSuccess(
        rendered.queryClient,
        'admin',
        { admin: { username: 'other-admin' } },
        nextGeneration,
      ),
    ).toBe(true);
    release();
    await expect(screen.findByText(/management session changed/i)).resolves.toBeVisible();
    const mutations = rendered.queryClient.getMutationCache().getAll();
    expect(mutations.at(-1)?.state.data).toBeUndefined();
    expect(fetchMock.mock.calls).toHaveLength(1);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('admin'))).not.toBe(
      true,
    );
  });

  test('does not revoke a current administrator after an old management read returns 403', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (requestURL.pathname === '/admin/api/charity-models/7/bindings') {
        await responseGate;
        return jsonResponse({ error: { code: 'forbidden', message: 'old administrator' } }, 403);
      }
      throw new Error(`Unexpected fixture request: ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin">
        <ManagementBindingsProbe />
      </ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await waitFor(() => expect(fetchMock.mock.calls.length).toBe(1));
    beginManagementSessionRequest(rendered.queryClient, 'admin');
    release();
    await expect(screen.findByText(/management session changed/i)).resolves.toBeVisible();
    expect(fetchMock.mock.calls).toHaveLength(1);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('admin'))).not.toBe(
      true,
    );
  });

  test('does not revoke or reconcile a current administrator after an old mutation returns 401', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (requestURL.pathname === '/admin/api/charity-models' && init?.method === 'POST') {
        await responseGate;
        return jsonResponse({ error: { code: 'unauthorized', message: 'old administrator' } }, 401);
      }
      throw new Error(
        `Unexpected fixture request: ${init?.method ?? 'GET'} ${requestURL.pathname}`,
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin">
        <ManagedModelMutationProbe />
      </ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByRole('button', { name: 'run mutation' });
    await rendered.user.click(screen.getByRole('button', { name: 'run mutation' }));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBe(1));
    beginManagementSessionRequest(rendered.queryClient, 'admin');
    release();
    await expect(screen.findByText(/management session changed/i)).resolves.toBeVisible();
    expect(fetchMock.mock.calls).toHaveLength(1);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('admin'))).not.toBe(
      true,
    );
  });

  test('evicts every user-station projection on a same-level account switch', async () => {
    const rendered = await renderWithProviders(<span>session fixture</span>, {
      station: 'user',
      role: 'level5',
    });
    const client = rendered.queryClient;
    const firstGeneration = beginManagementSessionRequest(client, 'steward');
    expect(
      noteManagementSessionSuccess(
        client,
        'steward',
        {
          user: { id: '1', username: 'account-a', effective_level: 5 },
        },
        firstGeneration,
      ),
    ).toBe(true);
    client.setQueryData(userKeys.session, { user: { id: '1', username: 'account-a' } });
    client.setQueryData(userKeys.endpoints, [{ id: 'endpoint-a' }]);
    client.setQueryData(userKeys.endpointKeysRoot, [{ id: 'key-a' }]);
    client.setQueryData(userKeys.models, [{ id: 'model-a' }]);
    client.setQueryData(userKeys.logsRoot, [{ id: 'log-a' }]);
    client.setQueryData(userKeys.callerKey, { id: 'caller-a' });
    client.setQueryData(userKeys.charityModels, [{ id: 'charity-a' }]);
    client.setQueryData(userKeys.donations, [{ id: 'donation-a' }]);
    client.setQueryData(charityManagementKeys.models('steward'), [{ id: 'managed-a' }]);

    const secondGeneration = beginManagementSessionRequest(client, 'steward');
    expect(
      noteManagementSessionSuccess(
        client,
        'steward',
        {
          user: { id: '2', username: 'account-b', effective_level: 5 },
        },
        secondGeneration,
      ),
    ).toBe(true);
    expect(client.getQueryData(userKeys.session)).toBeNull();
    for (const key of [
      userKeys.endpoints,
      userKeys.endpointKeysRoot,
      userKeys.models,
      userKeys.logsRoot,
      userKeys.callerKey,
      userKeys.charityModels,
      userKeys.donations,
      charityManagementKeys.models('steward'),
    ]) {
      expect(client.getQueryData(key)).toBeUndefined();
    }
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);
  });

  test('purges mutation results and remounts route-local state on an account switch', async () => {
    const otherSession = {
      user: { ...session.user, id: '2', username: 'account-b' },
    };
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      {
        method: 'GET',
        path: '/api/config',
        body: {
          site_name: 'NonbiriAPI',
          site_logo_url: '',
          legal_privacy_override_zh: '',
          legal_privacy_override_en: '',
          legal_terms_override_zh: '',
          legal_terms_override_en: '',
          legal_authoritative_locale: '',
          maintenance_mode: false,
          registration_open: true,
          announcement_epoch: 'b1e_AAAAAAAAAAAAAAAAAAAAAA',
        },
      },
    ]);
    const rendered = await renderWithProviders(
      <Routes>
        <Route element={<UserLayout />}>
          <Route index element={<StationMutationProbe />} />
        </Route>
      </Routes>,
      { station: 'user', role: 'user' },
    );
    await screen.findByText('fixture-user');
    await rendered.user.click(screen.getByRole('button', { name: 'run station mutation' }));
    await waitFor(() =>
      expect(screen.getByTestId('station-mutation-state')).toHaveTextContent(
        'account-a-mutation-result',
      ),
    );
    expect(
      rendered.queryClient
        .getMutationCache()
        .getAll()
        .some((mutation) =>
          JSON.stringify(mutation.state.data).includes('account-a-mutation-result'),
        ),
    ).toBe(true);

    const generation = beginManagementSessionRequest(rendered.queryClient, 'steward');
    expect(
      noteManagementSessionSuccess(rendered.queryClient, 'steward', otherSession, generation),
    ).toBe(true);
    rendered.queryClient.setQueryData(userKeys.session, otherSession);

    await screen.findByText('account-b');
    await waitFor(() =>
      expect(screen.getByTestId('station-mutation-state')).toHaveTextContent('idle'),
    );
    expect(rendered.queryClient.getMutationCache().getAll()).toHaveLength(0);
  });

  test('turns an old subject 401 into a session-change result without closing the new account', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="steward">
        <span data-testid="session-gate-ready">ready</span>
      </ManagementSessionGate>,
      { station: 'user', role: 'level5' },
    );
    await screen.findByTestId('session-gate-ready');
    const oldWrite = stationSessionWrite(rendered.queryClient, 'steward', async () => {
      await responseGate;
      throw new ApiError('unauthorized', 'old account rejected', 401);
    });
    const otherSession = {
      user: { ...session.user, id: '2', username: 'account-b', effective_level: 5 },
    };
    const generation = beginManagementSessionRequest(rendered.queryClient, 'steward');
    expect(
      noteManagementSessionSuccess(rendered.queryClient, 'steward', otherSession, generation),
    ).toBe(true);
    rendered.queryClient.setQueryData(userKeys.session, otherSession);
    release();

    await expect(oldWrite).rejects.toSatisfy(isStationSessionChanged);
    expect(rendered.queryClient.getQueryData(userKeys.session)).toEqual(otherSession);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('steward'))).toBe(
      false,
    );
  });

  test('never reveals a caller key returned after the account changes', async () => {
    const marker = `nbk_${'A'.repeat(43)}`;
    const lateResponse = deferred<Response>();
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/caller-key') {
        const headers = new Headers({ 'content-type': 'application/json' });
        headers.set('X-Nonbiri-CallerKey-Generation', '0');
        return new Response('null', { status: 200, headers });
      }
      if (method === 'POST' && path === '/api/caller-key/regenerate') {
        return lateResponse.promise;
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CallerKeyPanel accountId="1" />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });
    await screen.findByText('No CallerKey exists');
    await rendered.user.click(screen.getByRole('button', { name: 'Generate CallerKey' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(true),
    );

    rendered.rerender(<CallerKeyPanel accountId="2" />);
    rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '2' } });
    await act(async () => {
      lateResponse.resolve(
        jsonResponse({
          secret: marker,
          metadata: {
            display: 'nbk_AAAA…AAAA',
            created_at: 1_700_000_000,
            updated_at: 1_700_000_000,
            generation: '1',
          },
        }),
      );
      await lateResponse.promise;
    });

    await waitFor(() => expect(screen.queryByText(marker)).toBeNull());
    expect(rendered.queryClient.getQueryData(coreKeys.callerKey('2'))).toEqual({
      generation: '0',
      metadata: null,
    });
    expect(assertNoSensitiveQueryCache(rendered.queryClient, [marker]).hitSurfaces).toEqual([]);
  });

  test('does not download an export returned for the previous account', async () => {
    const marker = 'account-a-export-marker-123456';
    const completion = deferred<AccountExportAttachment>();
    const exportV4 = vi.fn(() => completion.promise);
    const adapter: AccountLifecycleAdapter = {
      capabilities: { exportV4: true, deleteAccount: false },
      beginElevation: vi.fn(async () => 'https://identity.example.test/elevate'),
      exportV4,
      deleteAccount: vi.fn(async () => undefined),
      readAccountAuthority: vi.fn(async () => 'active' as const),
    };
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => undefined);
    window.sessionStorage.setItem('nb.pending.elevation', 'export');
    window.sessionStorage.setItem('nb.pending.elevation.account', '1');
    document.cookie = 'nb_elevated=test-token-123456; Path=/; SameSite=Lax';
    try {
      const rendered = await renderWithProviders(
        <AccountLifecyclePanel accountId="1" adapter={adapter} />,
        { station: 'user', role: 'user', locale: 'en' },
      );
      rendered.queryClient.setQueryData(coreKeys.session, { user: { id: '1' } });
      const dialog = await screen.findByRole('alertdialog');
      await rendered.user.click(within(dialog).getByRole('button', { name: 'Create export' }));
      await waitFor(() => expect(exportV4).toHaveBeenCalledTimes(1));

      rendered.rerender(<AccountLifecyclePanel accountId="2" adapter={adapter} />);
      const currentSession = { user: { id: '2' } };
      rendered.queryClient.setQueryData(coreKeys.session, currentSession);
      await act(async () => {
        completion.resolve({
          blob: new Blob([marker], { type: 'application/json' }),
          schemaVersion: 4,
        });
        await completion.promise;
      });

      expect(clickSpy).not.toHaveBeenCalled();
      expect(rendered.queryClient.getQueryData(coreKeys.session)).toEqual(currentSession);
      expect(assertNoSensitiveQueryCache(rendered.queryClient, [marker]).hitSurfaces).toEqual([]);
    } finally {
      document.cookie = 'nb_elevated=; Max-Age=0; path=/';
      window.sessionStorage.removeItem('nb.pending.elevation');
      window.sessionStorage.removeItem('nb.pending.elevation.account');
      clickSpy.mockRestore();
    }
  });

  test('failed user logout clears the station before showing the server error', async () => {
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (requestURL.pathname === '/api/session') return jsonResponse(session);
      if (requestURL.pathname === '/api/config')
        return jsonResponse({
          site_name: 'NonbiriAPI',
          site_logo_url: '',
          legal_privacy_override_zh: '',
          legal_privacy_override_en: '',
          legal_terms_override_zh: '',
          legal_terms_override_en: '',
          legal_authoritative_locale: '',
          maintenance_mode: false,
          registration_open: true,
          announcement_epoch: 'b1e_AAAAAAAAAAAAAAAAAAAAAA',
        });
      if (requestURL.pathname === '/api/auth/logout' && init?.method === 'POST') {
        return jsonResponse({ error: { code: 'logout_failed', message: 'logout refused' } }, 401);
      }
      throw new Error(
        `Unexpected fixture request: ${init?.method ?? 'GET'} ${requestURL.pathname}`,
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<UserLayout />, { station: 'user', role: 'user' });
    await screen.findByText('fixture-user');
    rendered.queryClient.setQueryData(userKeys.models, [model]);
    await rendered.user.click(screen.getByRole('button', { name: 'fixture-user' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Sign out' }));
    await expect(screen.findByText('logout refused')).resolves.toBeVisible();
    expect(screen.getByRole('link', { name: 'Sign in' })).toBeVisible();
    expect(rendered.queryClient.getQueryData(userKeys.models)).toBeUndefined();
    expect(rendered.queryClient.getQueryData(userKeys.session)).toBeNull();
    expect(
      fetchMock.mock.calls.filter((call) => {
        const requestURL = new URL(String(call[0]), window.location.origin);
        return requestURL.pathname === '/api/session';
      }),
    ).toHaveLength(1);
  });

  test('closes steward management when the current user session fails', async () => {
    const levelFive = { ...session, user: { ...session.user, effective_level: 5 } };
    let healthy = true;
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (requestURL.pathname === '/api/session') {
        return healthy
          ? jsonResponse(levelFive)
          : jsonResponse({ error: { code: 'unauthorized', message: 'session expired' } }, 401);
      }
      throw new Error(`Unexpected fixture request: ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <>
        <UserSessionProbe />
        <ManagementCapabilityProbe frame="steward" />
      </>,
      { station: 'user', role: 'level5' },
    );
    await screen.findByText('fixture-user');
    await waitFor(() =>
      expect(screen.getByTestId('steward-capability-state')).toHaveTextContent('ready'),
    );
    rendered.queryClient.setQueryData(charityManagementKeys.models('steward'), [managedModel]);
    healthy = false;
    await rendered.queryClient.refetchQueries({
      queryKey: userKeys.session,
      exact: true,
      type: 'all',
    });
    await waitFor(() =>
      expect(screen.getByTestId('user-session-state')).toHaveTextContent('error'),
    );
    expect(screen.getByTestId('steward-capability-state')).toHaveTextContent('closed');
    expect(
      rendered.queryClient.getQueryData(charityManagementKeys.models('steward')),
    ).toBeUndefined();
  });

  test('closes administrator management when the current admin session is invalid', async () => {
    const adminSession = { admin: { username: 'fixture-admin' } };
    let healthy = true;
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const requestURL = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (requestURL.pathname === '/admin/api/session') {
        return healthy
          ? jsonResponse(adminSession)
          : jsonResponse({ error: { code: 'invalid_response', message: 'session invalid' } }, 200);
      }
      throw new Error(`Unexpected fixture request: ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <>
        <AdminSessionProbe />
        <ManagementCapabilityProbe frame="admin" />
      </>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByText('fixture-admin');
    await waitFor(() =>
      expect(screen.getByTestId('admin-capability-state')).toHaveTextContent('ready'),
    );
    rendered.queryClient.setQueryData(charityManagementKeys.models('admin'), [managedModel]);
    healthy = false;
    await rendered.queryClient.refetchQueries({
      queryKey: ['admin', 'session'],
      exact: true,
      type: 'all',
    });
    await waitFor(() =>
      expect(screen.getByTestId('admin-session-state')).toHaveTextContent('error'),
    );
    expect(screen.getByTestId('admin-capability-state')).toHaveTextContent('closed');
    expect(
      rendered.queryClient.getQueryData(charityManagementKeys.models('admin')),
    ).toBeUndefined();
  });

  test('ignores a stale session failure after a newer administrator identity succeeds', async () => {
    const rendered = await renderWithProviders(<span>session fixture</span>, {
      station: 'admin',
      role: 'admin',
    });
    const client = rendered.queryClient;
    const staleGeneration = beginManagementSessionRequest(client, 'admin');
    const freshGeneration = beginManagementSessionRequest(client, 'admin');
    expect(
      noteManagementSessionSuccess(
        client,
        'admin',
        { admin: { username: 'new-admin' } },
        freshGeneration,
      ),
    ).toBe(true);
    client.setQueryData(charityManagementKeys.capability('admin'), false);
    client.setQueryData(charityManagementKeys.models('admin'), [managedModel]);
    expect(failManagementSessionRequest(client, 'admin', staleGeneration)).toBe(false);
    expect(client.getQueryData(charityManagementKeys.capability('admin'))).toBe(false);
    expect(client.getQueryData(charityManagementKeys.models('admin'))).toEqual([managedModel]);
  });

  test('keeps explicit logout closed until a new authoritative steward session succeeds', async () => {
    const rendered = await renderWithProviders(<span>session fixture</span>, {
      station: 'user',
      role: 'level5',
    });
    const client = rendered.queryClient;
    const initialGeneration = beginManagementSessionRequest(client, 'steward');
    expect(
      noteManagementSessionSuccess(
        client,
        'steward',
        {
          user: { ...session.user, effective_level: 5 },
        },
        initialGeneration,
      ),
    ).toBe(true);
    client.setQueryData(charityManagementKeys.capability('steward'), false);
    client.setQueryData(charityManagementKeys.models('steward'), [managedModel]);
    const staleGeneration = beginManagementSessionRequest(client, 'steward');
    clearManagementSession(client, 'steward');
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    expect(client.getQueryData(charityManagementKeys.models('steward'))).toBeUndefined();
    expect(
      noteManagementSessionSuccess(
        client,
        'steward',
        {
          user: { ...session.user, effective_level: 5 },
        },
        staleGeneration,
      ),
    ).toBe(false);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    const freshGeneration = beginManagementSessionRequest(client, 'steward');
    expect(
      noteManagementSessionSuccess(
        client,
        'steward',
        {
          user: { ...session.user, effective_level: 5 },
        },
        freshGeneration,
      ),
    ).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);
  });

  test('treats a 204 key deletion as authoritative and removes the key projection', async () => {
    let deleted = false;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/endpoints/1') {
        return jsonResponse(
          deleted ? { ...coreEndpoint, revision: '2', key_count: '0' } : coreEndpoint,
        );
      }
      if (method === 'GET' && path === '/api/endpoints/1/keys?limit=50') {
        return jsonResponse(corePage(deleted ? [] : [coreEndpointKey]));
      }
      if (method === 'GET' && path === '/api/models?limit=50') return jsonResponse(corePage([]));
      if (method === 'GET' && path === '/api/endpoints/1/keys/2/models?limit=50')
        return jsonResponse(coreCatalogUnknown);
      if (method === 'DELETE' && path === '/api/endpoints/1/keys/2') {
        deleted = true;
        return new Response(null, { status: 204 });
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/1',
    });
    rendered.queryClient.setQueryData(coreKeys.endpoints('1'), corePage([coreEndpoint]));
    expect(rendered.queryClient.getQueryState(coreKeys.endpoints('1'))?.isInvalidated).toBe(false);
    await screen.findByText('sk-a…tail');
    await rendered.user.click(screen.getByRole('button', { name: 'Delete key' }));
    const dialog = screen.getByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Delete key' }));
    await waitFor(() => expect(screen.queryByText('sk-a…tail')).toBeNull());
    expect(lastBody(fetchMock, 'DELETE', '/api/endpoints/1/keys/2')).toEqual({
      expected_revision: '1',
    });
    expect(
      new Headers(
        (
          fetchMock.mock.calls.find(
            (call) =>
              requestPath(call[0] as string) === '/api/endpoints/1/keys/2' &&
              (call[1] as RequestInit | undefined)?.method === 'DELETE',
          )?.[1] as RequestInit | undefined
        )?.headers,
      ).get('Idempotency-Key'),
    ).toMatch(/^[A-Za-z0-9_-]{22,128}$/);
    expect(rendered.queryClient.getQueryState(coreKeys.endpoints('1'))?.isInvalidated).toBe(true);
  });

  test('invalidates the H1 endpoint list after an authoritative endpoint deletion', async () => {
    let deleted = false;
    let listReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/endpoints/1') return jsonResponse(coreEndpoint);
      if (method === 'GET' && path === '/api/endpoints/1/keys?limit=50') {
        return jsonResponse(corePage([]));
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50') {
        listReads += 1;
        return jsonResponse(corePage(deleted ? [] : [coreEndpoint]));
      }
      if (method === 'DELETE' && path === '/api/endpoints/1') {
        deleted = true;
        return new Response(null, { status: 204 });
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/1',
    });
    rendered.queryClient.setQueryDefaults(coreKeys.endpointsRoot('1'), { staleTime: Infinity });
    rendered.queryClient.setQueryData(coreKeys.endpoints('1'), corePage([coreEndpoint]));

    await screen.findByRole('heading', { name: 'Endpoint details' });
    await rendered.user.click(screen.getByRole('button', { name: 'Delete endpoint' }));
    await rendered.user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete endpoint' }),
    );

    expect(await screen.findByText('No endpoints yet')).toBeVisible();
    expect(listReads).toBe(1);
    expect(lastBody(fetchMock, 'DELETE', '/api/endpoints/1')).toEqual({
      expected_revision: '1',
    });
  });

  test('keeps endpoint deletion reconciliation reachable until a later 404 refreshes the list', async () => {
    let detailReads = 0;
    let deleteCalls = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/endpoints/1') {
        detailReads += 1;
        if (detailReads === 1) return jsonResponse(coreEndpoint);
        if (detailReads === 2) {
          return jsonResponse(
            { error: { code: 'internal', message: 'authority unavailable' } },
            503,
          );
        }
        return jsonResponse({ error: { code: 'not_found', message: 'already deleted' } }, 404);
      }
      if (method === 'GET' && path === '/api/endpoints/1/keys?limit=50') {
        return jsonResponse(corePage([]));
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50') {
        return jsonResponse(corePage(detailReads >= 3 ? [] : [coreEndpoint]));
      }
      if (method === 'DELETE' && path === '/api/endpoints/1') {
        deleteCalls += 1;
        throw new TypeError('connection reset before authority was readable');
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(endpointRouteTree(), {
      station: 'user',
      role: 'user',
      route: '/endpoints/1',
    });
    rendered.queryClient.setQueryDefaults(coreKeys.endpointsRoot('1'), { staleTime: Infinity });
    rendered.queryClient.setQueryData(coreKeys.endpoints('1'), corePage([coreEndpoint]));

    await screen.findByRole('heading', { name: 'Endpoint details' });
    await rendered.user.click(screen.getByRole('button', { name: 'Delete endpoint' }));
    await rendered.user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete endpoint' }),
    );

    expect(await screen.findByText(/The response was lost/)).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Cancel' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Check authoritative state' }));

    expect(await screen.findByText('No endpoints yet')).toBeVisible();
    expect(deleteCalls).toBe(1);
    expect(detailReads).toBeGreaterThanOrEqual(3);
  });

  test('treats a 204 model deletion as authoritative and returns to the model list', async () => {
    let deleted = false;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50')
        return jsonResponse(corePage(deleted ? [] : [coreModel]));
      if (method === 'GET' && path === '/api/models/3') return jsonResponse(coreModel);
      if (method === 'GET' && path === '/api/models/3/bindings') {
        return jsonResponse({
          bindings: [coreBinding('10', 'gpt-a', 0), coreBinding('11', 'gpt-b', 1)],
          binding_revision: '2',
        });
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50') return jsonResponse(corePage([]));
      if (method === 'DELETE' && path === '/api/models/3') {
        deleted = true;
        return new Response(null, { status: 204 });
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await rendered.user.click(await screen.findByRole('button', { name: 'Delete logical model' }));
    const dialog = screen.getByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Delete logical model' }));
    await screen.findByText('No logical models');
    expect(lastBody(fetchMock, 'DELETE', '/api/models/3')).toEqual({ expected_revision: '1' });
  });

  test('invalidates the model list when a lost delete response reconciles directly to 404', async () => {
    let detailReads = 0;
    let deleteCalls = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50') {
        return jsonResponse(corePage(detailReads >= 2 ? [] : [coreModel]));
      }
      if (method === 'GET' && path === '/api/models/3') {
        detailReads += 1;
        return detailReads === 1
          ? jsonResponse(coreModel)
          : jsonResponse({ error: { code: 'not_found', message: 'already deleted' } }, 404);
      }
      if (method === 'GET' && path === '/api/models/3/bindings') {
        return jsonResponse({ bindings: [], binding_revision: '2' });
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50') {
        return jsonResponse(corePage([]));
      }
      if (method === 'DELETE' && path === '/api/models/3') {
        deleteCalls += 1;
        throw new TypeError('connection reset after commit');
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    rendered.queryClient.setQueryDefaults(coreKeys.modelsRoot('1'), { staleTime: Infinity });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await rendered.user.click(await screen.findByRole('button', { name: 'Delete logical model' }));
    await rendered.user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete logical model',
      }),
    );

    expect(await screen.findByText('No logical models')).toBeVisible();
    expect(deleteCalls).toBe(1);
    expect(detailReads).toBe(2);
  });

  test('invalidates the model list when manual deletion reconciliation later confirms 404', async () => {
    let detailReads = 0;
    let deleteCalls = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = requestPath(input);
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(coreSession);
      if (method === 'GET' && path === '/api/models?limit=50') {
        return jsonResponse(corePage(detailReads >= 3 ? [] : [coreModel]));
      }
      if (method === 'GET' && path === '/api/models/3') {
        detailReads += 1;
        if (detailReads === 1) return jsonResponse(coreModel);
        if (detailReads === 2) {
          return jsonResponse(
            { error: { code: 'internal', message: 'authority unavailable' } },
            503,
          );
        }
        return jsonResponse({ error: { code: 'not_found', message: 'already deleted' } }, 404);
      }
      if (method === 'GET' && path === '/api/models/3/bindings') {
        return jsonResponse({ bindings: [], binding_revision: '2' });
      }
      if (method === 'GET' && path === '/api/endpoints?limit=50') {
        return jsonResponse(corePage([]));
      }
      if (method === 'DELETE' && path === '/api/models/3') {
        deleteCalls += 1;
        throw new TypeError('connection reset before authority was readable');
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    rendered.queryClient.setQueryDefaults(coreKeys.modelsRoot('1'), { staleTime: Infinity });
    await screen.findByRole('heading', { name: 'Logical models' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Manage bindings' }));
    await rendered.user.click(await screen.findByRole('button', { name: 'Delete logical model' }));
    await rendered.user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete logical model',
      }),
    );

    expect(await screen.findByText(/The response was lost/)).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Cancel' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Check authoritative state' }));

    expect(await screen.findByText('No logical models')).toBeVisible();
    expect(deleteCalls).toBe(1);
    expect(detailReads).toBe(3);
  });
});
