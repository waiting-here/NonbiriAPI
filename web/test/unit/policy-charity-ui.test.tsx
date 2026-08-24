import { fireEvent, screen, waitFor, within } from '@testing-library/react';
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
import { KeysPage } from '../../src/user/pages/KeysPage';
import { AccountPage } from '../../src/user/pages/AccountPage';
import { userKeys } from '../../src/user/data';
import { ApiError } from '../../src/shared/query/http';
import {
  assertNoSensitiveQueryCache,
  installJsonFetchFixtures,
  renderWithProviders,
} from './support';

const session = {
  user: {
    id: '1', username: 'fixture-user', lang: 'en', is_banned: false,
    endpoint_limit: null, effective_endpoint_limit: 10, rpm_limit: null,
    effective_rpm_limit: 60, concurrency_limit: null, effective_concurrency_limit: 5,
    credits: '0', donation_credit: '0', effective_level: 2,
    created_at: '2026-08-23T00:00:00Z',
  },
};

const endpoint = {
  id: 1, connector_type: 'openai-compatible', base_url: 'https://upstream.test/v1',
  note: 'primary', enabled: true, model_fetch_failed: false, model_fetch_failed_at: 0,
  created_at: 1, updated_at: 2,
};

const endpointKey = {
  id: 2, display_head: 'sk-a', display_tail: 'tail', note: 'key note', enabled: true,
  force_store_false: false, created_at: 1, updated_at: 2,
};

const model = {
  id: 3, provider: 'provider', model: 'model', full_name: 'provider/model',
  route_strategy: 'ordered', silent_retry: false, flatten_tool_calls: false,
  binding_count: 2, created_at: 1, updated_at: 2,
};

const managedModel = {
  id: 7, provider: 'provider', model: 'charity-model', full_name: 'provider/charity-model',
  enabled: true, flatten_tool_calls: false, pricing_mode: 'per_request',
  prices: {
    request_user_price_milli: '0', request_donor_reward_milli: '0',
    uncached_user_price_milli: '0', cache_write_user_price_milli: '0',
    cache_read_user_price_milli: '0', output_user_price_milli: '0',
    uncached_donor_reward_milli: '0', cache_write_donor_reward_milli: '0',
    cache_read_donor_reward_milli: '0', output_donor_reward_milli: '0',
    current_request_user_price_milli: '0', current_uncached_user_price_milli: '0',
    current_cache_write_user_price_milli: '0', current_cache_read_user_price_milli: '0',
    current_output_user_price_milli: '0',
  },
  discount: { percent: 100, enabled: false, start_at: null, end_at: null },
  success_samples: 0, success_count: 0,
};

const userCharityModel = { ...managedModel, flatten_tool_calls: true, available: true, availability_reason: 'ok' };

function lastBody(fetchMock: { mock: { calls: unknown[][] } }, method: string, path: string): Record<string, unknown> {
  const call = [...fetchMock.mock.calls].reverse().find((entry) => {
    const request = entry[0] as string;
    const init = entry[1] as RequestInit | undefined;
    return new URL(request, window.location.origin).pathname === path && (init?.method ?? 'GET') === method;
  });
  if (!call) throw new Error(`Missing ${method} ${path}`);
  return JSON.parse(String((call[1] as RequestInit).body)) as Record<string, unknown>;
}

function expiryControl(): HTMLInputElement {
  const control = document.querySelector<HTMLInputElement>('input[type="datetime-local"]');
  if (!control) throw new Error('expiry control not found');
  return control;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
  });
}

function ManagementBindingsProbe() {
  const query = useManagementBindings('admin', '7');
  return <output data-testid="management-bindings-state">
    {query.isPending ? 'pending' : query.error instanceof Error ? query.error.message : JSON.stringify(query.data)}
  </output>;
}

function ManagedModelMutationProbe() {
  const mutation = useCreateManagedModel('admin');
  return <>
    <button type="button" onClick={() => mutation.mutate({
      provider: 'provider', model: 'charity-model', pricing_mode: 'per_request',
      flatten_tool_calls: false, prices: managedModel.prices, discount: { percent: 100, enabled: false, start_at: null, end_at: null },
    })}>run mutation</button>
    <output data-testid="management-mutation-state">
      {mutation.isPending ? 'pending' : mutation.data ? mutation.data.full_name : mutation.error instanceof Error ? mutation.error.message : 'idle'}
    </output>
  </>;
}

function ManagementCapabilityProbe({ frame }: { frame: 'admin' | 'steward' }) {
  const capability = useManagementCapability(frame);
  return <output data-testid={`${frame}-capability-state`}>
    {capability.authorityReady && capability.data !== true ? 'ready' : 'closed'}
  </output>;
}

function UserSessionProbe() {
  const query = useUserSession();
  return <output data-testid="user-session-state">
    {query.isPending ? 'pending' : query.error ? 'error' : query.data?.user?.username ?? 'anonymous'}
  </output>;
}

function AdminSessionProbe() {
  const query = useAdminSession();
  return <output data-testid="admin-session-state">
    {query.isPending ? 'pending' : query.error ? 'error' : query.data?.admin?.username ?? 'anonymous'}
  </output>;
}

function StationMutationProbe() {
  const mutation = useMutation({
    mutationFn: async () => ({ marker: 'account-a-mutation-result' }),
  });
  return <>
    <button type="button" onClick={() => mutation.mutate()}>run station mutation</button>
    <output data-testid="station-mutation-state">
      {mutation.data?.marker ?? 'idle'}
    </output>
  </>;
}

function ManagementSessionGate({ frame, children }: { frame: 'admin' | 'steward'; children: ReactNode }) {
  const client = useQueryClient();
  const ready = useQuery({
    queryKey: ['management-session-test-seed', frame],
    queryFn: () => true,
    enabled: false,
  });
  useEffect(() => {
    const generation = beginManagementSessionRequest(client, frame);
    const value = frame === 'admin'
      ? { admin: { username: 'fixture-admin' } }
      : { user: { id: '1', username: 'fixture-user', effective_level: 5 } };
    if (!noteManagementSessionSuccess(client, frame, value, generation)) return;
    client.setQueryData(['management-session-test-seed', frame], true);
  }, [client, frame]);
  return ready.data ? <>{children}</> : null;
}

describe('experimental policy and charity controls', () => {
  test('edits the owner-only endpoint key policy and keeps a new secret out of query state', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const marker = 'synthetic-secret-123456';
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/endpoints', body: [endpoint] },
      { method: 'GET', path: '/api/endpoints/1/keys', body: [endpointKey] },
      { method: 'PATCH', path: '/api/endpoints/1/keys/2', body: { ...endpointKey, note: 'updated', force_store_false: true } },
      { method: 'POST', path: '/api/endpoints/1/keys', body: { ...endpointKey, id: 4, force_store_false: true } },
      { method: 'GET', path: '/api/endpoints/1/keys/2/models', body: [] },
    ]);
    const rendered = await renderWithProviders(<EndpointsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Endpoint management' });
    await rendered.user.click(screen.getByRole('button', { name: 'Endpoint keys' }));
    await screen.findByText('sk-a…tail');
    await rendered.user.click(screen.getByRole('button', { name: 'Edit key metadata' }));
    await rendered.user.clear(screen.getByLabelText('Key note'));
    await rendered.user.type(screen.getByLabelText('Key note'), 'updated');
    await rendered.user.click(screen.getByRole('checkbox', { name: 'Require upstream not to store prompts (Experimental)' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Save key metadata' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', '/api/endpoints/1/keys/2')).toEqual({
      note: 'updated', enabled: true, force_store_false: true,
    }));

    await rendered.user.click(screen.getByRole('button', { name: 'Add key' }));
    const secretInput = screen.getByPlaceholderText('Enter the upstream key once');
    await rendered.user.type(secretInput, marker);
    await rendered.user.click(screen.getByRole('checkbox', { name: 'Require upstream not to store prompts (Experimental)' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Save key' }));
    await waitFor(() => expect(lastBody(fetchMock, 'POST', '/api/endpoints/1/keys')).toMatchObject({
      secret: marker, force_store_false: true,
    }));
    expect(screen.queryByDisplayValue(marker)).toBeNull();
    assertNoSensitiveQueryCache(rendered.queryClient, [marker]);
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  test('does not expose the OpenAI-only store policy on an Anthropic key', async () => {
    const anthropicEndpoint = { ...endpoint, id: 4, connector_type: 'anthropic-compatible' };
    const anthropicKey = { ...endpointKey } as Record<string, unknown>;
    delete anthropicKey.force_store_false;
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/endpoints', body: [anthropicEndpoint] },
      { method: 'GET', path: '/api/endpoints/4/keys', body: [{ ...anthropicKey, id: 5 }] },
    ]);
    const rendered = await renderWithProviders(<EndpointsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Endpoint management' });
    await rendered.user.click(screen.getByRole('button', { name: 'Endpoint keys' }));
    await screen.findByText('sk-a…tail');
    await rendered.user.click(screen.getByRole('button', { name: 'Edit key metadata' }));
    expect(screen.queryByRole('checkbox', { name: 'Require upstream not to store prompts (Experimental)' })).toBeNull();
    expect(fetchMock).toHaveBeenCalled();
  });

  test('rolls an optimistic flatten checkbox back after a server conflict', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/models', body: [model] },
      { method: 'PATCH', path: '/api/models/3', status: 409, body: { error: { code: 'conflict', message: 'conflict' } } },
    ]);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    await rendered.user.click(screen.getByRole('button', { name: 'Edit' }));
    const checkbox = screen.getByRole('checkbox', { name: 'Experimental: flatten tool calls' });
    await rendered.user.click(checkbox);
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await expect(screen.findByText('conflict')).resolves.toBeVisible();
    expect(checkbox).not.toBeChecked();
    expect(fetchMock).toHaveBeenCalled();
  });

  test('adopts a committed model policy after a conflict response and refetch', async () => {
    let modelReads = 0;
    const committed = { ...model, flatten_tool_calls: true };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(session);
      if (method === 'GET' && path === '/api/models') {
        modelReads += 1;
        return jsonResponse([modelReads === 1 ? model : committed]);
      }
      if (method === 'PATCH' && path === '/api/models/3') {
        return jsonResponse({ error: { code: 'conflict', message: 'response lost after commit' } }, 409);
      }
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    await rendered.user.click(screen.getByRole('button', { name: 'Edit' }));
    const checkbox = screen.getByRole('checkbox', { name: 'Experimental: flatten tool calls' });
    await rendered.user.click(checkbox);
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await expect(screen.findByText('response lost after commit')).resolves.toBeVisible();
    await waitFor(() => expect(modelReads).toBeGreaterThan(1));
    expect(checkbox).toBeChecked();
  });

  test('refetches the key policy after a network failure and keeps a committed value', async () => {
    let keyReads = 0;
    const committed = { ...endpointKey, force_store_false: true };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(session);
      if (method === 'GET' && path === '/api/endpoints') return jsonResponse([endpoint]);
      if (method === 'GET' && path === '/api/endpoints/1/keys') {
        keyReads += 1;
        return jsonResponse([keyReads === 1 ? endpointKey : committed]);
      }
      if (method === 'PATCH' && path === '/api/endpoints/1/keys/2') throw new TypeError('connection reset');
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<EndpointsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Endpoint management' });
    await rendered.user.click(screen.getByRole('button', { name: 'Endpoint keys' }));
    await screen.findByText('sk-a…tail');
    await rendered.user.click(screen.getByRole('button', { name: 'Edit key metadata' }));
    const checkbox = screen.getByRole('checkbox', { name: 'Require upstream not to store prompts (Experimental)' });
    await rendered.user.click(checkbox);
    await rendered.user.click(screen.getByRole('button', { name: 'Save key metadata' }));
    await expect(screen.findByText(/network request failed/i)).resolves.toBeVisible();
    await waitFor(() => expect(keyReads).toBeGreaterThan(1));
    expect(checkbox).toBeChecked();
  });

  test('refetches the model policy after an invalid response and keeps a committed value', async () => {
    let modelReads = 0;
    const committed = { ...model, flatten_tool_calls: true };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(session);
      if (method === 'GET' && path === '/api/models') {
        modelReads += 1;
        return jsonResponse([modelReads === 1 ? model : committed]);
      }
      if (method === 'PATCH' && path === '/api/models/3') return jsonResponse({ committed: true });
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    await rendered.user.click(screen.getByRole('button', { name: 'Edit' }));
    const checkbox = screen.getByRole('checkbox', { name: 'Experimental: flatten tool calls' });
    await rendered.user.click(checkbox);
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await expect(screen.findByText(/invalid response|invalid model/i)).resolves.toBeVisible();
    await waitFor(() => expect(modelReads).toBeGreaterThan(1));
    expect(checkbox).toBeChecked();
  });

  test('reconciles a lost endpoint-key create response before allowing a retry', async () => {
    let keyReads = 0;
    const createdKey = { ...endpointKey, id: 4, display_head: 'sk-new', display_tail: 'tail2', note: 'created key' };
    const marker = 'synthetic-key-create-secret-123456';
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(session);
      if (method === 'GET' && path === '/api/endpoints') return jsonResponse([endpoint]);
      if (method === 'GET' && path === '/api/endpoints/1/keys') {
        keyReads += 1;
        return jsonResponse(keyReads === 1 ? [endpointKey] : [endpointKey, createdKey]);
      }
      if (method === 'POST' && path === '/api/endpoints/1/keys') throw new TypeError('response lost after key create');
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<EndpointsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Endpoint management' });
    await rendered.user.click(screen.getByRole('button', { name: 'Endpoint keys' }));
    await screen.findByText('sk-a…tail');
    await rendered.user.click(screen.getByRole('button', { name: 'Add key' }));
    await rendered.user.type(screen.getByPlaceholderText('Enter the upstream key once'), marker);
    await rendered.user.click(screen.getByRole('button', { name: 'Save key' }));
    await expect(screen.findByText(/network request failed/i)).resolves.toBeVisible();
    await screen.findByText('sk-new…tail2');
    await waitFor(() => expect(keyReads).toBeGreaterThan(1));
    expect(fetchMock.mock.calls.filter((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      const requestInit = call[1] as RequestInit | undefined;
      return requestURL.pathname === '/api/endpoints/1/keys' && requestInit?.method === 'POST';
    })).toHaveLength(1);
    expect(screen.queryByDisplayValue(marker)).toBeNull();
    assertNoSensitiveQueryCache(rendered.queryClient, [marker]);
  });

  test('reconciles a lost personal-model create response before allowing a retry', async () => {
    let modelReads = 0;
    const createdModel = {
      ...model, id: 4, provider: 'created-provider', model: 'created-model',
      full_name: 'created-provider/created-model',
    };
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && path === '/api/session') return jsonResponse(session);
      if (method === 'GET' && path === '/api/models') {
        modelReads += 1;
        return jsonResponse([modelReads === 1 ? model : model, ...(modelReads > 1 ? [createdModel] : [])]);
      }
      if (method === 'POST' && path === '/api/models') throw new TypeError('response lost after model create');
      throw new Error(`Unexpected fixture request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    await rendered.user.click(screen.getByRole('button', { name: 'Create model' }));
    await rendered.user.type(screen.getByPlaceholderText('example-provider'), 'created-provider');
    await rendered.user.type(screen.getByPlaceholderText('example-model'), 'created-model');
    const createButtons = screen.getAllByRole('button', { name: 'Create model' });
    await rendered.user.click(createButtons[createButtons.length - 1]);
    await expect(screen.findByText(/network request failed/i)).resolves.toBeVisible();
    await screen.findByText('created-provider/created-model');
    await waitFor(() => expect(modelReads).toBeGreaterThan(1));
    expect(fetchMock.mock.calls.filter((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      const requestInit = call[1] as RequestInit | undefined;
      return requestURL.pathname === '/api/models' && requestInit?.method === 'POST';
    })).toHaveLength(1);
  });

  test('surfaces the real Anthropic binding policy conflict instead of treating it as a generic 409', async () => {
    const anthropicEndpoint = { ...endpoint, id: 4, connector_type: 'anthropic-compatible' };
    const anthropicKey = { ...endpointKey } as Record<string, unknown>;
    delete anthropicKey.force_store_false;
    const anthropicModel = { ...model, flatten_tool_calls: true, binding_count: 0 };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/models', body: [anthropicModel] },
      { method: 'GET', path: '/api/models/3/bindings', body: [] },
      { method: 'GET', path: '/api/endpoints', body: [anthropicEndpoint] },
      { method: 'GET', path: '/api/endpoints/4/keys', body: [{ ...anthropicKey, id: 5 }] },
      { method: 'GET', path: '/api/endpoints/4/keys/5/models', body: [{ upstream_model_id: 'claude-3', provider: 'anthropic', fetched_at: 1, status: 'ok' }] },
      {
        method: 'POST', path: '/api/models/3/bindings', status: 409,
        body: { error: { code: 'conflict', message: 'flatten_tool_calls requires OpenAI-compatible bindings' } },
      },
    ]);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    await rendered.user.click(screen.getByRole('button', { name: 'Show bindings' }));
    await rendered.user.click(await screen.findByRole('button', { name: 'Add binding' }));
    await rendered.user.selectOptions(screen.getByLabelText('Endpoint'), '4');
    await rendered.user.selectOptions(await screen.findByLabelText('Endpoint key'), '5');
    await rendered.user.selectOptions(await screen.findByLabelText('Upstream model'), 'claude-3');
    const addButtons = screen.getAllByRole('button', { name: 'Add binding' });
    await rendered.user.click(addButtons[addButtons.length - 1]);
    await waitFor(() => expect(lastBody(fetchMock, 'POST', '/api/models/3/bindings')).toEqual({
      endpoint_key_id: 5, upstream_model_id: 'claude-3',
    }));
    await expect(screen.findByText(/flatten_tool_calls requires OpenAI-compatible bindings/i)).resolves.toBeVisible();
  });

  test('sends logical-model flatten policy and renders its explicit risk', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/models', body: [model] },
      { method: 'GET', path: '/api/models/3/bindings', body: [
        { id: 10, endpoint_key_id: 2, upstream_model_id: 'gpt-a', endpoint_base_url: endpoint.base_url, endpoint_key_display_head: 'sk-a', endpoint_key_display_tail: 'tail', endpoint_key_note: 'key note', endpoint_note: 'primary', ord: 0 },
        { id: 11, endpoint_key_id: 2, upstream_model_id: 'gpt-b', endpoint_base_url: endpoint.base_url, endpoint_key_display_head: 'sk-a', endpoint_key_display_tail: 'tail', endpoint_key_note: 'key note', endpoint_note: 'primary', ord: 1 },
      ] },
      { method: 'PUT', path: '/api/models/3/bindings/order', body: [] },
      { method: 'PATCH', path: '/api/models/3', body: { ...model, flatten_tool_calls: true } },
    ]);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    await rendered.user.click(screen.getByRole('button', { name: 'Show bindings' }));
    const dragHandle = (await screen.findAllByRole('button', { name: 'Drag, or focus and use the arrow keys, to reorder bindings' }))[0];
    await dragHandle.focus();
    await rendered.user.keyboard('{ArrowDown}');
    await waitFor(() => expect(lastBody(fetchMock, 'PUT', '/api/models/3/bindings/order')).toEqual({ order: [11, 10] }));
    await rendered.user.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    await rendered.user.click(screen.getByRole('checkbox', { name: 'Experimental: flatten tool calls' }));
    expect(screen.getByText(/flattening can break normal tool calls/i)).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', '/api/models/3')).toMatchObject({
      provider: 'provider', model: 'model', flatten_tool_calls: true,
    }));
  });

  test('keeps binding deletion disabled when upstream ownership context is unavailable', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/models', body: [model] },
      { method: 'GET', path: '/api/models/3/bindings', body: [{
        id: 10, endpoint_key_id: 2, upstream_model_id: 'gpt-a', endpoint_base_url: endpoint.base_url,
        endpoint_key_display_head: 'sk-a', endpoint_key_display_tail: 'tail', ord: 0,
      }] },
      // The binding's endpoint/key no longer resolves to an enabled owner.
      { method: 'GET', path: '/api/endpoints', body: [] },
    ]);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    await rendered.user.click(screen.getByRole('button', { name: 'Show bindings' }));
    const remove = await screen.findByRole('button', { name: 'Delete binding' });
    await waitFor(() => expect(remove).toBeDisabled());
    expect(screen.getByRole('alert')).toHaveTextContent(/endpoint and key context/i);
    await rendered.user.click(remove);
    expect(fetchMock.mock.calls.some((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      return requestURL.pathname === '/api/models/3/bindings/10' && (call[1] as RequestInit | undefined)?.method === 'DELETE';
    })).toBe(false);
  });

  test('shows a donor charity flatten policy and only the owner donation without a model editor', async () => {
    const ownDonation = {
      id: 9, endpoint_id: 1, endpoint_base_url: endpoint.base_url, status: 'pending', enabled: false,
      description: 'my donation', review_note: '', expires_at: undefined, created_at: 1, updated_at: 2,
      keys: [], reviews: [],
    };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/charity/models', body: { data: [userCharityModel] } },
      { method: 'GET', path: '/api/donations', body: [ownDonation] },
      { method: 'GET', path: '/api/donations/9', body: ownDonation },
      { method: 'GET', path: '/api/endpoints', body: [] },
    ]);
    const rendered = await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    await screen.findByText('provider/charity-model');
    expect(screen.getByText(/Risk: normal tool calls may break/i)).toBeVisible();
    expect(screen.getByText('my donation')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Edit' }));
    expect(screen.queryByRole('checkbox', { name: 'Experimental: flatten tool calls' })).toBeNull();
    expect(fetchMock.mock.calls.some((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      return requestURL.pathname.startsWith('/api/charity/models') && (call[1] as RequestInit | undefined)?.method === 'PATCH';
    })).toBe(false);
    expect(rendered.queryClient.getQueryData(['user', 'charity-models'])).toBeDefined();
  });

  const pendingDonation = {
    id: 9, endpoint_id: 1, endpoint_base_url: endpoint.base_url, status: 'pending', enabled: false,
    description: 'pending donation', review_note: '', expires_at: undefined, created_at: 1, updated_at: 2,
    keys: [], reviews: [],
  };

  test.each([
    { name: 'endpoint query error', endpointResponse: jsonResponse({ error: { code: 'service_unavailable', message: 'endpoint list unavailable' } }, 503), expected: /temporarily unavailable|endpoint list unavailable/i },
    { name: 'endpoint not found', endpointResponse: jsonResponse([]), expected: /donation endpoint could not be verified/i },
  ])('shows a retryable endpoint-context error and disables editing when $name', async ({ endpointResponse, expected }) => {
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
      if (path === '/api/session') return jsonResponse(session);
      if (path === '/api/charity/models') return jsonResponse({ data: [] });
      if (path === '/api/donations') return jsonResponse([pendingDonation]);
      if (path === '/api/donations/9') return jsonResponse(pendingDonation);
      if (path === '/api/endpoints') return endpointResponse;
      throw new Error(`Unexpected fixture request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    const edit = await screen.findByRole('button', { name: 'Edit' });
    const alerts = await screen.findAllByRole('alert');
    expect(alerts.some((alert) => expected.test(alert.textContent ?? ''))).toBe(true);
    expect(edit).toBeDisabled();
    expect(fetchMock.mock.calls.some((call) => new URL(String(call[0]), window.location.origin).pathname === '/api/donations/9')).toBe(false);
  });

  test('shows a retryable donation-detail error and disables editing without using list projection', async () => {
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const path = new URL(input instanceof Request ? input.url : String(input), window.location.origin).pathname;
      if (path === '/api/session') return jsonResponse(session);
      if (path === '/api/charity/models') return jsonResponse({ data: [] });
      if (path === '/api/donations') return jsonResponse([pendingDonation]);
      if (path === '/api/donations/9') return jsonResponse({ error: { code: 'service_unavailable', message: 'detail unavailable' } }, 503);
      if (path === '/api/endpoints') return jsonResponse([endpoint]);
      throw new Error(`Unexpected fixture request: ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    const edit = await screen.findByRole('button', { name: 'Edit' });
    await expect(screen.findByText(/service unavailable|detail unavailable/i)).resolves.toBeVisible();
    expect(edit).toBeDisabled();
  });

  test('uses the existing-endpoint donation wire with physical key ids and explicit limits', async () => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/charity/models', body: { data: [] } },
      { method: 'GET', path: '/api/donations', body: [] },
      { method: 'GET', path: '/api/endpoints', body: [endpoint] },
      { method: 'GET', path: '/api/endpoints/1/keys', body: [endpointKey] },
      { method: 'POST', path: '/api/donations', body: {
        id: 9, endpoint_id: 1, endpoint_base_url: endpoint.base_url, status: 'pending', enabled: false,
        description: 'fixture donation', review_note: '', expires_at: null, created_at: 1, updated_at: 2,
        keys: [], reviews: [],
      } },
    ]);
    const rendered = await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Charity resources & donations' });
    await screen.findByText('https://upstream.test/v1 · primary');
    expect(screen.getByText('Choose an endpoint first to see its enabled keys.')).toBeVisible();
    await rendered.user.selectOptions(screen.getAllByRole('combobox')[0], '1');
    await rendered.user.click(await screen.findByRole('checkbox', { name: /sk-a/ }));
    await rendered.user.clear(screen.getByLabelText('Max concurrency (0 = unlimited)'));
    await rendered.user.type(screen.getByLabelText('Max concurrency (0 = unlimited)'), '7');
    await rendered.user.clear(screen.getByLabelText('Requests per minute (0 = unlimited)'));
    await rendered.user.type(screen.getByLabelText('Requests per minute (0 = unlimited)'), '90');
    await rendered.user.type(screen.getByLabelText('Donation description'), 'fixture donation');
    await rendered.user.click(screen.getByRole('button', { name: 'Submit for review' }));
    await waitFor(() => expect(lastBody(fetchMock, 'POST', '/api/donations')).toEqual({
      description: 'fixture donation',
      existing_endpoint: {
        endpoint_id: 1, key_ids: [2],
        keys: [{ endpoint_key_id: 2, max_concurrency: 7, rpm_limit: 90 }],
      },
    }));
    await rendered.user.clear(screen.getByLabelText('Max concurrency (0 = unlimited)'));
    await rendered.user.clear(screen.getByLabelText('Requests per minute (0 = unlimited)'));
    await rendered.user.type(screen.getByLabelText('Donation description'), 'blank limits');
    await rendered.user.click(screen.getByRole('button', { name: 'Submit for review' }));
    await waitFor(() => expect(lastBody(fetchMock, 'POST', '/api/donations')).toEqual({
      description: 'blank limits',
      existing_endpoint: {
        endpoint_id: 1, key_ids: [2],
        keys: [{ endpoint_key_id: 2, max_concurrency: 0, rpm_limit: 0 }],
      },
    }));
  });

  test('sends pending owner key replacement and never caches a new-endpoint secret', async () => {
    const marker = 'synthetic-donation-secret-123456';
    const pending = {
      id: 9, endpoint_id: 1, endpoint_base_url: endpoint.base_url, status: 'pending', enabled: false,
      description: 'old description', review_note: '', expires_at: 1700000000, created_at: 1, updated_at: 2,
      keys: [], reviews: [],
    };
    const detail = { ...pending, keys: [{
      id: 6, endpoint_key_id: 2, display_head: 'sk-a', display_tail: 'tail', max_concurrency: 2, rpm_limit: 30,
      credits_usage_cap_milli: '0', credits_used_milli: '0', credits_reserved_milli: '0', enabled: true,
      force_store_false: false,
    }] };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/charity/models', body: { data: [] } },
      { method: 'GET', path: '/api/donations', body: [pending] },
      { method: 'GET', path: '/api/donations/9', body: detail },
      { method: 'GET', path: '/api/endpoints', body: [endpoint] },
      { method: 'GET', path: '/api/endpoints/1/keys', body: [endpointKey] },
      { method: 'PATCH', path: '/api/donations/9', body: detail },
      { method: 'POST', path: '/api/donations', body: { id: 10 } },
    ]);
    const rendered = await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Edit' }));
    await rendered.user.clear(await screen.findByLabelText('Max concurrency (0 = unlimited)'));
    await rendered.user.type(screen.getByLabelText('Max concurrency (0 = unlimited)'), '8');
    await rendered.user.clear(screen.getByLabelText('Donation description'));
    await rendered.user.type(screen.getByLabelText('Donation description'), 'new description');
    await rendered.user.clear(expiryControl());
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', '/api/donations/9')).toEqual({
      description: 'new description',
      expires_at: null,
      keys: { key_ids: [2], limits: [{ endpoint_key_id: 2, max_concurrency: 8, rpm_limit: 30 }] },
    }));

    // New-endpoint submission uses the direct fetch path, so the marker never
    // enters the React Query mutation cache.
    await rendered.user.click(await screen.findByRole('button', { name: 'Submit a new endpoint and keys' }));
    await rendered.user.type(screen.getByPlaceholderText('Base URL'), 'https://new-upstream.test/v1');
    await rendered.user.type(screen.getByPlaceholderText('New key (submitted once)'), marker);
    await rendered.user.click(screen.getByRole('checkbox', { name: 'Require upstream not to store prompts (Experimental)' }));
    await rendered.user.type(screen.getByLabelText('Donation description'), 'new endpoint');
    await rendered.user.click(screen.getByRole('button', { name: 'Submit for review' }));
    await waitFor(() => expect(lastBody(fetchMock, 'POST', '/api/donations')).toMatchObject({
      new_endpoint: { connector_type: 'openai-compatible', keys: [{ secret: marker, max_concurrency: 0, rpm_limit: 0, force_store_false: true }] },
    }));
    expect(screen.queryByDisplayValue(marker)).toBeNull();
    assertNoSensitiveQueryCache(rendered.queryClient, [marker]);
  });

  test('edits a pending new-endpoint donation without replacing its not-yet-owned keys', async () => {
    const pendingNewEndpoint = {
      id: 13, endpoint_base_url: 'https://pending-upstream.test/v1', status: 'pending', enabled: false,
      description: 'old nested description', review_note: '', expires_at: 1700000000,
      created_at: 1, updated_at: 2, keys: [], reviews: [],
    };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/charity/models', body: { data: [] } },
      { method: 'GET', path: '/api/donations', body: [pendingNewEndpoint] },
      { method: 'GET', path: '/api/donations/13', body: pendingNewEndpoint },
      { method: 'PATCH', path: '/api/donations/13', body: pendingNewEndpoint },
    ]);
    const rendered = await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Edit' }));
    expect(screen.getByText(/pending new-endpoint donation has no endpoint id/i)).toBeVisible();
    expect(screen.queryByLabelText('Endpoint')).toBeNull();
    await rendered.user.clear(screen.getByLabelText('Donation description'));
    await rendered.user.type(screen.getByLabelText('Donation description'), 'metadata only');
    await rendered.user.clear(expiryControl());
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', '/api/donations/13')).toEqual({
      description: 'metadata only', expires_at: null,
    }));
    expect(lastBody(fetchMock, 'PATCH', '/api/donations/13')).not.toHaveProperty('keys');
  });

  test('fails closed when an endpoint response omits a required boolean', async () => {
    const malformedEndpoint = { ...endpoint, enabled: undefined };
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/charity/models', body: { data: [] } },
      { method: 'GET', path: '/api/donations', body: [] },
      { method: 'GET', path: '/api/endpoints', body: [malformedEndpoint] },
    ]);
    await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    await expect(screen.findByRole('alert')).resolves.toBeVisible();
  });

  test('fails closed when an OpenAI key response omits its required store policy', async () => {
    const malformedKey = { ...endpointKey } as Record<string, unknown>;
    delete malformedKey.force_store_false;
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/endpoints', body: [endpoint] },
      { method: 'GET', path: '/api/endpoints/1/keys', body: [{ ...malformedKey }] },
    ]);
    const rendered = await renderWithProviders(<EndpointsPage />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Endpoint keys' }));
    await expect(screen.findByRole('alert')).resolves.toBeVisible();
  });

  test('fails closed when a binding order exceeds the frozen range', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/models', body: [model] },
      { method: 'GET', path: '/api/models/3/bindings', body: [{
        id: 10, endpoint_key_id: 2, upstream_model_id: 'gpt-a', endpoint_base_url: endpoint.base_url,
        endpoint_key_display_head: 'sk-a', endpoint_key_display_tail: 'tail', ord: 1_000_001,
      }] },
    ]);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    await rendered.user.click(screen.getByRole('button', { name: 'Show bindings' }));
    await expect(screen.findByRole('alert')).resolves.toBeVisible();
  });

  test('hides and omits the OpenAI-only store policy for an Anthropic charity endpoint', async () => {
    const marker = 'synthetic-anthropic-donation-secret-123456';
    const anthropicEndpoint = { ...endpoint, connector_type: 'anthropic-compatible' };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/charity/models', body: { data: [] } },
      { method: 'GET', path: '/api/donations', body: [] },
      { method: 'GET', path: '/api/endpoints', body: [anthropicEndpoint] },
      { method: 'POST', path: '/api/donations', body: { id: 11 } },
    ]);
    const rendered = await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Charity resources & donations' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Submit a new endpoint and keys' }));
    await rendered.user.selectOptions(screen.getByLabelText('Connector'), 'anthropic-compatible');
    expect(screen.queryByRole('checkbox', { name: 'Require upstream not to store prompts (Experimental)' })).toBeNull();
    await rendered.user.type(screen.getByPlaceholderText('Base URL'), 'https://anthropic-upstream.test/v1');
    await rendered.user.type(screen.getByPlaceholderText('New key (submitted once)'), marker);
    await rendered.user.type(screen.getByLabelText('Donation description'), 'anthropic endpoint');
    await rendered.user.click(screen.getByRole('button', { name: 'Submit for review' }));
    await waitFor(() => expect(lastBody(fetchMock, 'POST', '/api/donations')).toEqual({
      description: 'anthropic endpoint',
      new_endpoint: {
        connector_type: 'anthropic-compatible',
        base_url: 'https://anthropic-upstream.test/v1',
        keys: [{ secret: marker, max_concurrency: 0, rpm_limit: 0 }],
      },
    }));
    expect(JSON.stringify(lastBody(fetchMock, 'POST', '/api/donations'))).not.toContain('force_store_false');
    expect(screen.queryByDisplayValue(marker)).toBeNull();
    assertNoSensitiveQueryCache(rendered.queryClient, [marker]);
  });

  test('rejects an invalid pending expiry before sending a PATCH', async () => {
    const pending = {
      id: 12, endpoint_id: 1, endpoint_base_url: endpoint.base_url, status: 'pending', enabled: false,
      description: 'expiry fixture', review_note: '', expires_at: 1700000000, created_at: 1, updated_at: 2,
      keys: [], reviews: [],
    };
    const detail = { ...pending, keys: [{
      id: 8, endpoint_key_id: 2, display_head: 'sk-a', display_tail: 'tail', max_concurrency: 2, rpm_limit: 30,
      credits_usage_cap_milli: '0', credits_used_milli: '0', credits_reserved_milli: '0', enabled: true, force_store_false: false,
    }] };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: session },
      { method: 'GET', path: '/api/charity/models', body: { data: [] } },
      { method: 'GET', path: '/api/donations', body: [pending] },
      { method: 'GET', path: '/api/donations/12', body: detail },
      { method: 'GET', path: '/api/endpoints', body: [endpoint] },
      { method: 'GET', path: '/api/endpoints/1/keys', body: [endpointKey] },
    ]);
    const rendered = await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    await rendered.user.click(await screen.findByRole('button', { name: 'Edit' }));
    const invalidExpiry = expiryControl();
    // datetime-local sanitizes malformed values at the DOM boundary; preserve the
    // invalid event value here so the component's strict parser is exercised.
    Object.defineProperty(invalidExpiry, 'value', { configurable: true, writable: true, value: 'not-a-date' });
    fireEvent.change(invalidExpiry);
    await rendered.user.click(screen.getByRole('button', { name: 'Save' }));
    await expect(screen.findByText('Check the highlighted fields.')).resolves.toBeVisible();
    expect(fetchMock.mock.calls.filter((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      const requestInit = call[1] as RequestInit | undefined;
      return requestURL.pathname === '/api/donations/12' && requestInit?.method === 'PATCH';
    })).toHaveLength(0);
  });

  test('rejects a non-empty invalid reviewer expiry and sends no PATCH', async () => {
    const listDonation = {
      id: 20, user_id: 1, endpoint_base_url: endpoint.base_url, status: 'pending', enabled: false,
      description: 'review expiry fixture', review_note: '', created_at: 1, updated_at: 2,
    };
    const detailDonation = { ...listDonation, expires_at: 1700000000, keys: [], reviews: [] };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/admin/api/donations?page=1&page_size=20&status=pending', body: { data: [listDonation], has_more: false, total: 1 } },
      { method: 'GET', path: '/admin/api/donations/20', body: detailDonation },
      { method: 'GET', path: '/admin/api/charity-models?page=1&page_size=100', body: { data: [], has_more: false, total: 0 } },
      { method: 'GET', path: '/admin/api/site-config', body: { charity_enabled: true, donation_accept_enabled: true, charity_token_reserve_milli: null } },
      { method: 'PATCH', path: '/admin/api/donations/20', body: detailDonation },
    ]);
    const rendered = await renderWithProviders(<ManagementSessionGate frame="admin"><CharityManagement frame="admin" /></ManagementSessionGate>, { station: 'admin', role: 'admin' });
    await screen.findByRole('heading', { name: 'Donation review queue' });
    await screen.findByText('review expiry fixture');
    const expiry = document.querySelector<HTMLInputElement>('input[type="datetime-local"]');
    if (!expiry) throw new Error('review expiry control not found');
    Object.defineProperty(expiry, 'value', { configurable: true, writable: true, value: 'not-a-date' });
    fireEvent.change(expiry);
    await rendered.user.click(screen.getByRole('button', { name: 'Approve donation' }));
    await expect(screen.findByText('Enter a valid expiry date, or leave it blank to clear the expiry.')).resolves.toBeVisible();
    expect(fetchMock.mock.calls.filter((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      const requestInit = call[1] as RequestInit | undefined;
      return requestURL.pathname === '/admin/api/donations/20' && requestInit?.method === 'PATCH';
    })).toHaveLength(0);
    Object.defineProperty(expiry, 'value', { configurable: true, writable: true, value: '' });
    fireEvent.change(expiry);
    await rendered.user.click(screen.getByRole('button', { name: 'Approve donation' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', '/admin/api/donations/20')).toMatchObject({ action: 'approve', expires_at: null }));
  });

  test('keeps management actions disabled on detail failure and enables them after retry', async () => {
    const listDonation = {
      id: 21, user_id: 1, endpoint_base_url: endpoint.base_url, status: 'pending', enabled: false,
      description: 'detail retry fixture', review_note: '', created_at: 1, updated_at: 2,
    };
    const detailDonation = { ...listDonation, expires_at: null, keys: [], reviews: [] };
    let detailReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && requestURL.pathname === '/admin/api/donations') return jsonResponse({ data: [listDonation], has_more: false, total: 1 });
      if (method === 'GET' && requestURL.pathname === '/admin/api/donations/21') {
        detailReads += 1;
        return detailReads === 1
          ? jsonResponse({ error: { code: 'service_unavailable', message: 'detail temporarily unavailable' } }, 503)
          : jsonResponse(detailDonation);
      }
      if (method === 'GET' && requestURL.pathname === '/admin/api/charity-models') return jsonResponse({ data: [], has_more: false, total: 0 });
      if (method === 'GET' && requestURL.pathname === '/admin/api/site-config') return jsonResponse({ charity_enabled: true, donation_accept_enabled: true, charity_token_reserve_milli: null });
      if (method === 'PATCH' && requestURL.pathname === '/admin/api/donations/21') return jsonResponse(detailDonation);
      throw new Error(`Unexpected fixture request: ${method} ${requestURL.pathname}${requestURL.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ManagementSessionGate frame="admin"><CharityManagement frame="admin" /></ManagementSessionGate>, { station: 'admin', role: 'admin' });
    await screen.findByText('detail retry fixture');
    const approve = screen.getByRole('button', { name: 'Approve donation' });
    expect(approve).toBeDisabled();
    await waitFor(() => expect(detailReads).toBeGreaterThan(0));
    await rendered.user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(detailReads).toBe(2));
    await waitFor(() => expect(approve).toBeEnabled());
    await rendered.user.click(approve);
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', '/admin/api/donations/21')).toMatchObject({ action: 'approve' }));
  });

  test.each([
    { frame: 'admin' as const, basePath: '/admin/api', station: 'admin' as const, role: 'admin' as const },
    { frame: 'steward' as const, basePath: '/api/steward', station: 'user' as const, role: 'level5' as const },
  ])('allows the $frame management role to create and update charity flatten policy', async ({ frame, basePath, station, role }) => {
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: `${basePath}/donations?page=1&page_size=20&status=pending`, body: { data: [], has_more: false, total: 0 } },
      { method: 'GET', path: `${basePath}/charity-models?page=1&page_size=100`, body: { data: [managedModel], has_more: false, total: 1 } },
      { method: 'GET', path: `${basePath}/charity-models/7/bindings`, body: { data: [{
        id: 10, charity_model_id: 7, donation_key_id: 6, upstream_model_id: 'gpt-a', ord: 0,
        endpoint_base_url: endpoint.base_url, key_display_head: 'sk-a', key_display_tail: 'tail', donation_key_enabled: true,
      }] } },
      ...(frame === 'admin' ? [{ method: 'GET', path: '/admin/api/site-config', body: { charity_enabled: true, donation_accept_enabled: true, charity_token_reserve_milli: null } }] : []),
      { method: 'PATCH', path: `${basePath}/charity-models/7`, body: { ...managedModel, flatten_tool_calls: true } },
      { method: 'POST', path: `${basePath}/charity-models`, body: { ...managedModel, id: 8, provider: 'new-provider', model: 'new-model', full_name: 'new-provider/new-model', flatten_tool_calls: true } },
    ]);
    const rendered = await renderWithProviders(<ManagementSessionGate frame={frame}><CharityManagement frame={frame} /></ManagementSessionGate>, { station, role });
    await screen.findByRole('heading', { name: 'Donation review queue' });
    await screen.findByText('provider/charity-model');
    await rendered.user.click(screen.getByText('Bindings', { exact: true }));
    await expect(screen.findByText('sk-a…tail')).resolves.toBeVisible();
    await rendered.user.click(screen.getAllByRole('button', { name: 'Edit' })[0]);
    const editForm = rendered.container.querySelector('form.charity-editor');
    if (!editForm) throw new Error('Missing charity model editor');
    await rendered.user.click(within(editForm as HTMLElement).getByRole('checkbox', { name: 'Experimental: flatten tool calls' }));
    await rendered.user.click(within(editForm as HTMLElement).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', `${basePath}/charity-models/7`)).toMatchObject({ flatten_tool_calls: true }));

    await rendered.user.click(screen.getByRole('button', { name: 'Add charity model' }));
    const createForm = rendered.container.querySelector('form.charity-editor');
    if (!createForm) throw new Error('Missing charity model creation form');
    await rendered.user.type(within(createForm as HTMLElement).getByLabelText('Provider'), 'new-provider');
    await rendered.user.type(within(createForm as HTMLElement).getByLabelText('Model'), 'new-model');
    await rendered.user.click(within(createForm as HTMLElement).getByRole('checkbox', { name: 'Experimental: flatten tool calls' }));
    await rendered.user.click(within(createForm as HTMLElement).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(lastBody(fetchMock, 'POST', `${basePath}/charity-models`)).toMatchObject({ flatten_tool_calls: true }));
  });

  test('keeps management controls hidden after a mutation 403 even if the sensitive root is evicted', async () => {
    // A sparse response must not borrow identity fields from the stale cache
    // and accidentally reopen the capability latch.
    const revokedSession = { user: { effective_level: 5 } };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: '/api/steward/donations?page=1&page_size=20&status=pending', body: { data: [], has_more: false, total: 0 } },
      { method: 'GET', path: '/api/steward/charity-models?page=1&page_size=100', body: { data: [managedModel], has_more: false, total: 1 } },
      { method: 'GET', path: '/api/steward/charity-models/7/bindings', body: { data: [] } },
      { method: 'PATCH', path: '/api/steward/charity-models/7', body: { error: { code: 'forbidden', message: 'capability revoked' } }, status: 403 },
      // A stale/insufficient authoritative session must keep the external
      // capability latch closed after the write is rejected.
      { method: 'GET', path: '/api/session', body: revokedSession },
    ]);
    const rendered = await renderWithProviders(<ManagementSessionGate frame="steward"><CharityManagement frame="steward" /></ManagementSessionGate>, { station: 'user', role: 'level5' });
    await screen.findByText('provider/charity-model');
    await rendered.user.click(screen.getByRole('button', { name: 'Edit' }));
    const editForm = rendered.container.querySelector('form.charity-editor');
    if (!editForm) throw new Error('Missing charity model editor');
    await rendered.user.click(within(editForm as HTMLElement).getByRole('checkbox', { name: 'Experimental: flatten tool calls' }));
    await rendered.user.click(within(editForm as HTMLElement).getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(screen.getByText(/capability is no longer active/i)).toBeVisible();
      expect(screen.queryByRole('checkbox', { name: 'Experimental: flatten tool calls' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Add charity model' })).toBeNull();
    });
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);

    // The normal forbidden effect evicts the management root.  The revoke
    // sentinel is deliberately outside that root and must not recreate a
    // writable view while the session refresh is still in flight.
    rendered.queryClient.removeQueries({ queryKey: charityManagementKeys.root('steward') });
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    expect(screen.queryByRole('button', { name: 'Add charity model' })).toBeNull();

    // Cache invalidation must not refetch the local placeholder and reopen
    // controls after an authoritative rejection. A separately successful,
    // identity-checked session refresh is the only recovery path.
    await rendered.queryClient.invalidateQueries({ queryKey: charityManagementKeys.capability('steward') });
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    expect(screen.queryByRole('button', { name: 'Add charity model' })).toBeNull();

    expect(fetchMock.mock.calls.some((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      return requestURL.pathname === '/api/session';
    })).toBe(true);
  });

  test.each([
    { frame: 'admin' as const, basePath: '/admin/api', station: 'admin' as const, role: 'admin' as const },
    { frame: 'steward' as const, basePath: '/api/steward', station: 'user' as const, role: 'level5' as const },
  ])('shows the physical-key store policy read-only for the $frame review role and preserves donation-key ids', async ({ frame, basePath, station, role }) => {
    const listDonation = {
      id: 9, user_id: 1, endpoint_base_url: endpoint.base_url, status: 'pending', enabled: false,
      description: 'review fixture', review_note: '', created_at: 1, updated_at: 2,
    };
    const detailDonation = {
      ...listDonation,
      keys: [{
        id: 6, endpoint_key_id: 2, display_head: 'sk-a', display_tail: 'tail',
        max_concurrency: 2, rpm_limit: 30, credits_usage_cap_milli: '1000',
        credits_used_milli: '10', credits_reserved_milli: '0', enabled: true,
        force_store_false: true,
      }],
      reviews: [],
    };
    const fetchMock = installJsonFetchFixtures([
      { method: 'GET', path: `${basePath}/donations?page=1&page_size=20&status=pending`, body: { data: [listDonation], has_more: false, total: 1 } },
      { method: 'GET', path: `${basePath}/donations/9`, body: detailDonation },
      { method: 'GET', path: `${basePath}/charity-models?page=1&page_size=100`, body: { data: [], has_more: false, total: 0 } },
      ...(frame === 'admin' ? [{ method: 'GET', path: '/admin/api/site-config', body: { charity_enabled: false, donation_accept_enabled: false, charity_token_reserve_milli: null } }] : []),
      { method: 'PATCH', path: `${basePath}/donations/9`, body: detailDonation },
    ]);
    const rendered = await renderWithProviders(<ManagementSessionGate frame={frame}><CharityManagement frame={frame} /></ManagementSessionGate>, { station, role });
    await screen.findByRole('heading', { name: 'Donation review queue' });
    expect(await screen.findByText(/Upstream prompt storage policy \(read-only\): Require upstream not to store prompts \(Experimental\)/)).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Save key limits' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', `${basePath}/donations/9`)).toEqual({
      action: 'update', expires_at: null,
      keys: [{ id: 6, max_concurrency: 2, rpm_limit: 30, credits_usage_cap_milli: '1000', enabled: true }],
    }));
    expect(lastBody(fetchMock, 'PATCH', `${basePath}/donations/9`)).not.toHaveProperty('keys.0.force_store_false');

    const maxConcurrency = screen.getByLabelText('Max concurrency (0 = unlimited)');
    const rpmLimit = screen.getByLabelText('Requests per minute (0 = unlimited)');
    await rendered.user.clear(maxConcurrency);
    await rendered.user.click(screen.getByRole('button', { name: 'Save key limits' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', `${basePath}/donations/9`)).toEqual({
      action: 'update', expires_at: null,
      keys: [{ id: 6, rpm_limit: 30, credits_usage_cap_milli: '1000', enabled: true }],
    }));
    await rendered.user.clear(rpmLimit);
    await rendered.user.type(rpmLimit, '0');
    await rendered.user.click(screen.getByRole('button', { name: 'Save key limits' }));
    await waitFor(() => expect(lastBody(fetchMock, 'PATCH', `${basePath}/donations/9`)).toEqual({
      action: 'update', expires_at: null,
      keys: [{ id: 6, rpm_limit: 0, credits_usage_cap_milli: '1000', enabled: true }],
    }));
    const usageCap = screen.getByLabelText('Usage cap (milli-credits; 0 = unlimited)');
    await rendered.user.clear(usageCap);
    await rendered.user.type(usageCap, '9223372036854775808');
    await rendered.user.click(screen.getByRole('button', { name: 'Save key limits' }));
    await expect(screen.findByText(/Leave a reviewer limit blank to omit it/i)).resolves.toBeVisible();
  });

  test('clears level-5 steward data after a server-forced demotion', async () => {
    const levelFive = { ...session, user: { ...session.user, effective_level: 5 } };
    const demoted = { ...session, user: { ...session.user, effective_level: 4 } };
    let sessionCalls = 0;
    let revoke = false;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (method === 'GET' && requestURL.pathname === '/api/session') {
        const body = sessionCalls++ === 0 ? levelFive : demoted;
        return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/logs') {
        if (!revoke) return new Response(JSON.stringify({ data: [], has_more: false, total: 0 }), { status: 200, headers: { 'content-type': 'application/json' } });
        return new Response(JSON.stringify({ error: { code: 'forbidden', message: 'steward capability revoked' } }), {
          status: 403, headers: { 'content-type': 'application/json' },
        });
      }
      throw new Error(`Unregistered test request: ${method} ${requestURL.pathname}${requestURL.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<StewardPage />, { station: 'user', role: 'level5' });
    await screen.findByText('No request logs');
    rendered.queryClient.setQueryData(charityManagementKeys.models('steward'), [managedModel]);
    revoke = true;
    await rendered.queryClient.invalidateQueries({ queryKey: userKeys.stewardLogsRoot });
    await expect(screen.findByText(/requires the server-resolved level 5 capability/i)).resolves.toBeVisible();
    expect(sessionCalls).toBeGreaterThanOrEqual(2);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.models('steward'))).toBeUndefined();
    expect(rendered.queryClient.getQueryCache().getAll()
      .filter((query) => query.queryKey[1] === 'steward-logs')
      .every((query) => query.state.data === undefined)).toBe(true);
  });

  test('reopens the same steward user after a newer authoritative session success', async () => {
    const levelFive = { ...session, user: { ...session.user, effective_level: 5 } };
    let sessionCalls = 0;
    let logCalls = 0;
    let revoke = false;
    let releaseRefresh!: () => void;
    const refreshGate = new Promise<void>((resolve) => { releaseRefresh = resolve; });
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (method === 'GET' && requestURL.pathname === '/api/session') {
        sessionCalls += 1;
        if (sessionCalls > 1) await refreshGate;
        return jsonResponse(levelFive);
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/logs') {
        logCalls += 1;
        if (revoke && logCalls === 2) {
          return jsonResponse({ error: { code: 'forbidden', message: 'temporary capability rejection' } }, 403);
        }
        return jsonResponse({ data: [], has_more: false, total: 0 });
      }
      throw new Error(`Unregistered test request: ${method} ${requestURL.pathname}${requestURL.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<StewardPage />, { station: 'user', role: 'level5' });
    await screen.findByText('No request logs');
    revoke = true;
    await rendered.queryClient.invalidateQueries({ queryKey: userKeys.stewardLogsRoot });
    await expect(screen.findByText(/requires the server-resolved level 5 capability/i)).resolves.toBeVisible();
    await waitFor(() => expect(sessionCalls).toBeGreaterThanOrEqual(2));
    releaseRefresh();
    await waitFor(() => expect(screen.queryByText(/requires the server-resolved level 5 capability/i)).toBeNull());
    expect(screen.getByText('No request logs')).toBeVisible();
  });

  test('clears charity management data and removes write controls after an L5 downgrade', async () => {
    const levelFive = { ...session, user: { ...session.user, effective_level: 5 } };
    const demoted = { ...session, user: { ...session.user, effective_level: 4 } };
    let sessionCalls = 0;
    let demote = false;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (method === 'GET' && requestURL.pathname === '/api/session') {
        sessionCalls += 1;
        return new Response(JSON.stringify(demote ? demoted : levelFive), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/logs') {
        return new Response(JSON.stringify({ data: [], has_more: false, total: 0 }), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/donations') {
        return new Response(JSON.stringify({ data: [], has_more: false, total: 0 }), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/charity-models') {
        return new Response(JSON.stringify({ data: [managedModel], has_more: false, total: 1 }), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      if (method === 'GET' && requestURL.pathname === '/api/steward/charity-models/7/bindings') {
        return new Response(JSON.stringify({ data: [] }), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      throw new Error(`Unregistered test request: ${method} ${requestURL.pathname}${requestURL.search}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<StewardPage />, { station: 'user', role: 'level5' });
    await screen.findByText('No request logs');
    await rendered.user.click(screen.getByRole('tab', { name: 'Charity management' }));
    await screen.findByText('provider/charity-model');
    expect(rendered.queryClient.getQueryData(charityManagementKeys.models('steward'))).toBeDefined();

    demote = true;
    await rendered.queryClient.invalidateQueries({ queryKey: userKeys.session });
    await screen.findByText('This co-management surface requires the server-resolved level 5 capability.');
    expect(rendered.queryClient.getQueryData(charityManagementKeys.models('steward'))).toBeUndefined();
    expect(screen.queryByRole('checkbox', { name: 'Experimental: flatten tool calls' })).toBeNull();
    expect(fetchMock.mock.calls.some((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      const requestInit = call[1] as RequestInit | undefined;
      return requestInit?.method === 'PATCH' && requestURL.pathname.includes('/charity-models/');
    })).toBe(false);
    expect(sessionCalls).toBeGreaterThanOrEqual(2);
  });

  test('binds recovery to the exact subject and generation, including same-level switches', async () => {
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="steward"><span data-testid="session-gate-ready">ready</span></ManagementSessionGate>,
      { station: 'user', role: 'level5' },
    );
    await screen.findByTestId('session-gate-ready');
    const client = rendered.queryClient;
    const staleGeneration = 1;
    client.setQueryData(charityManagementKeys.capability('steward'), true);
    const nextGeneration = beginManagementSessionRequest(client, 'steward');
    expect(noteManagementSessionSuccess(client, 'steward', session, staleGeneration)).toBe(false);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    const otherUser = { user: { ...session.user, id: '2', username: 'other-user', effective_level: 5 } };
    expect(noteManagementSessionSuccess(client, 'steward', otherUser, nextGeneration)).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);

    const downgradeGeneration = beginManagementSessionRequest(client, 'steward');
    expect(noteManagementSessionSuccess(client, 'steward', {
      user: { ...otherUser.user, effective_level: 4 },
    }, downgradeGeneration)).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    const reauthGeneration = beginManagementSessionRequest(client, 'steward');
    expect(noteManagementSessionSuccess(client, 'steward', otherUser, reauthGeneration)).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);
  });

  test('binds administrator recovery to username rather than an administrator role', async () => {
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin"><span data-testid="session-gate-ready">ready</span></ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByTestId('session-gate-ready');
    const client = rendered.queryClient;
    client.setQueryData(charityManagementKeys.capability('admin'), true);
    const nextGeneration = beginManagementSessionRequest(client, 'admin');
    expect(noteManagementSessionSuccess(client, 'admin', { admin: { username: 'fixture-admin' } }, 1)).toBe(false);
    expect(client.getQueryData(charityManagementKeys.capability('admin'))).toBe(true);
    expect(noteManagementSessionSuccess(client, 'admin', { admin: { username: 'other-admin' } }, nextGeneration)).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('admin'))).toBe(false);
  });

  test('rejects a bindings response with fields outside the exact data envelope', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ data: [], extra: true }));
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin"><ManagementBindingsProbe /></ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await expect(screen.findByText(/invalid charity bindings list data/i)).resolves.toBeVisible();
    expect(rendered.queryClient.getQueryData(charityManagementKeys.bindings('admin', '7'))).toBeUndefined();
  });

  test('drops a management read that returns after the session generation changes', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => { release = resolve; });
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      if (requestURL.pathname === '/admin/api/charity-models/7/bindings') {
        await responseGate;
        return jsonResponse({ data: [] });
      }
      throw new Error(`Unexpected fixture request: ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin"><ManagementBindingsProbe /></ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(0));
    const nextGeneration = beginManagementSessionRequest(rendered.queryClient, 'admin');
    expect(nextGeneration).toBe(2);
    release();
    await expect(screen.findByText(/management session changed/i)).resolves.toBeVisible();
    expect(rendered.queryClient.getQueryState(charityManagementKeys.bindings('admin', '7'))?.data).toBeUndefined();
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('admin'))).not.toBe(true);
  });

  test('does not cache or reconcile a management mutation response from an old subject', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => { release = resolve; });
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      if (requestURL.pathname === '/admin/api/charity-models' && init?.method === 'POST') {
        await responseGate;
        return jsonResponse(managedModel);
      }
      throw new Error(`Unexpected fixture request: ${init?.method ?? 'GET'} ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin"><ManagedModelMutationProbe /></ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByRole('button', { name: 'run mutation' });
    await rendered.user.click(screen.getByRole('button', { name: 'run mutation' }));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBe(1));
    const nextGeneration = beginManagementSessionRequest(rendered.queryClient, 'admin');
    expect(noteManagementSessionSuccess(rendered.queryClient, 'admin', { admin: { username: 'other-admin' } }, nextGeneration)).toBe(true);
    release();
    await expect(screen.findByText(/management session changed/i)).resolves.toBeVisible();
    const mutations = rendered.queryClient.getMutationCache().getAll();
    expect(mutations.at(-1)?.state.data).toBeUndefined();
    expect(fetchMock.mock.calls).toHaveLength(1);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('admin'))).not.toBe(true);
  });

  test('does not revoke a current administrator after an old management read returns 403', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => { release = resolve; });
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      if (requestURL.pathname === '/admin/api/charity-models/7/bindings') {
        await responseGate;
        return jsonResponse({ error: { code: 'forbidden', message: 'old administrator' } }, 403);
      }
      throw new Error(`Unexpected fixture request: ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin"><ManagementBindingsProbe /></ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await waitFor(() => expect(fetchMock.mock.calls.length).toBe(1));
    beginManagementSessionRequest(rendered.queryClient, 'admin');
    release();
    await expect(screen.findByText(/management session changed/i)).resolves.toBeVisible();
    expect(fetchMock.mock.calls).toHaveLength(1);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('admin'))).not.toBe(true);
  });

  test('does not revoke or reconcile a current administrator after an old mutation returns 401', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => { release = resolve; });
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      if (requestURL.pathname === '/admin/api/charity-models' && init?.method === 'POST') {
        await responseGate;
        return jsonResponse({ error: { code: 'unauthorized', message: 'old administrator' } }, 401);
      }
      throw new Error(`Unexpected fixture request: ${init?.method ?? 'GET'} ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="admin"><ManagedModelMutationProbe /></ManagementSessionGate>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByRole('button', { name: 'run mutation' });
    await rendered.user.click(screen.getByRole('button', { name: 'run mutation' }));
    await waitFor(() => expect(fetchMock.mock.calls.length).toBe(1));
    beginManagementSessionRequest(rendered.queryClient, 'admin');
    release();
    await expect(screen.findByText(/management session changed/i)).resolves.toBeVisible();
    expect(fetchMock.mock.calls).toHaveLength(1);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('admin'))).not.toBe(true);
  });

  test('evicts every user-station projection on a same-level account switch', async () => {
    const rendered = await renderWithProviders(<span>session fixture</span>, { station: 'user', role: 'level5' });
    const client = rendered.queryClient;
    const firstGeneration = beginManagementSessionRequest(client, 'steward');
    expect(noteManagementSessionSuccess(client, 'steward', {
      user: { id: '1', username: 'account-a', effective_level: 5 },
    }, firstGeneration)).toBe(true);
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
    expect(noteManagementSessionSuccess(client, 'steward', {
      user: { id: '2', username: 'account-b', effective_level: 5 },
    }, secondGeneration)).toBe(true);
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
      { method: 'GET', path: '/api/config', body: { site_name: 'NonbiriAPI' } },
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
    await waitFor(() => expect(screen.getByTestId('station-mutation-state')).toHaveTextContent('account-a-mutation-result'));
    expect(rendered.queryClient.getMutationCache().getAll().some(
      (mutation) => JSON.stringify(mutation.state.data).includes('account-a-mutation-result'),
    )).toBe(true);

    const generation = beginManagementSessionRequest(rendered.queryClient, 'steward');
    expect(noteManagementSessionSuccess(rendered.queryClient, 'steward', otherSession, generation)).toBe(true);
    rendered.queryClient.setQueryData(userKeys.session, otherSession);

    await screen.findByText('account-b');
    await waitFor(() => expect(screen.getByTestId('station-mutation-state')).toHaveTextContent('idle'));
    expect(rendered.queryClient.getMutationCache().getAll()).toHaveLength(0);
  });

  test('turns an old subject 401 into a session-change result without closing the new account', async () => {
    let release!: () => void;
    const responseGate = new Promise<void>((resolve) => { release = resolve; });
    const rendered = await renderWithProviders(
      <ManagementSessionGate frame="steward"><span data-testid="session-gate-ready">ready</span></ManagementSessionGate>,
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
    expect(noteManagementSessionSuccess(rendered.queryClient, 'steward', otherSession, generation)).toBe(true);
    rendered.queryClient.setQueryData(userKeys.session, otherSession);
    release();

    await expect(oldWrite).rejects.toSatisfy(isStationSessionChanged);
    expect(rendered.queryClient.getQueryData(userKeys.session)).toEqual(otherSession);
    expect(rendered.queryClient.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);
  });

  test('never reveals a caller key returned after the account changes', async () => {
    const marker = 'nbk_account_a_one_time_secret_123456';
    let release!: () => void;
    let regenerateStarted = false;
    const responseGate = new Promise<void>((resolve) => { release = resolve; });
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && requestURL.pathname === '/api/session') return jsonResponse(session);
      if (method === 'GET' && requestURL.pathname === '/api/caller-key') {
        return jsonResponse({ error: { code: 'not_found', message: 'no key' } }, 404);
      }
      if (method === 'POST' && requestURL.pathname === '/api/caller-key/regenerate') {
        regenerateStarted = true;
        await responseGate;
        return jsonResponse({ secret: marker });
      }
      throw new Error(`Unexpected fixture request: ${method} ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<KeysPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Platform caller key' });
    await rendered.user.click(screen.getByRole('button', { name: 'Generate caller key' }));
    const dialog = screen.getByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Generate caller key' }));
    await waitFor(() => expect(regenerateStarted).toBe(true));

    const otherSession = { user: { ...session.user, id: '2', username: 'account-b' } };
    const generation = beginManagementSessionRequest(rendered.queryClient, 'steward');
    expect(noteManagementSessionSuccess(rendered.queryClient, 'steward', otherSession, generation)).toBe(true);
    rendered.queryClient.setQueryData(userKeys.session, otherSession);
    release();

    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Generate caller key' })).toBeEnabled());
    expect(screen.queryByText(marker)).toBeNull();
    assertNoSensitiveQueryCache(rendered.queryClient, [marker]);
  });

  test('does not download an export returned for the previous account', async () => {
    const marker = 'account-a-export-marker-123456';
    const otherSession = { user: { ...session.user, id: '2', username: 'account-b' } };
    let currentSession = session;
    let release!: () => void;
    let exportStarted = false;
    const responseGate = new Promise<void>((resolve) => { release = resolve; });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    document.cookie = 'nb_elevated=test-token-123456; path=/';
    try {
      const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
        const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
        const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
        if (method === 'GET' && requestURL.pathname === '/api/session') return jsonResponse(currentSession);
        if (method === 'GET' && requestURL.pathname === '/api/me') return jsonResponse(currentSession.user);
        if (method === 'GET' && requestURL.pathname === '/api/me/usage') {
          return jsonResponse({
            total_requests: 0,
            total_prompt_tokens: 0,
            total_completion_tokens: 0,
            total_unknown_usage_requests: 0,
          });
        }
        if (method === 'POST' && requestURL.pathname === '/api/account/export') {
          exportStarted = true;
          await responseGate;
          return jsonResponse({ marker });
        }
        throw new Error(`Unexpected fixture request: ${method} ${requestURL.pathname}`);
      });
      vi.stubGlobal('fetch', fetchMock);
      const rendered = await renderWithProviders(<AccountPage />, { station: 'user', role: 'user' });
      await screen.findByRole('heading', { name: 'Account and data' });
      await rendered.user.click(screen.getByRole('button', { name: 'Export account data' }));
      const dialog = screen.getByRole('alertdialog');
      await rendered.user.click(within(dialog).getByRole('button', { name: 'Export' }));
      await waitFor(() => expect(exportStarted).toBe(true));

      const generation = beginManagementSessionRequest(rendered.queryClient, 'steward');
      expect(noteManagementSessionSuccess(rendered.queryClient, 'steward', otherSession, generation)).toBe(true);
      currentSession = otherSession;
      rendered.queryClient.setQueryData(userKeys.session, otherSession);
      release();

      await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Export' })).toBeEnabled());
      expect(clickSpy).not.toHaveBeenCalled();
      assertNoSensitiveQueryCache(rendered.queryClient, [marker]);
    } finally {
      document.cookie = 'nb_elevated=; Max-Age=0; path=/';
      clickSpy.mockRestore();
    }
  });

  test('failed user logout clears the station before showing the server error', async () => {
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      if (requestURL.pathname === '/api/session') return jsonResponse(session);
      if (requestURL.pathname === '/api/config') return jsonResponse({ site_name: 'NonbiriAPI' });
      if (requestURL.pathname === '/api/auth/logout' && init?.method === 'POST') {
        return jsonResponse({ error: { code: 'logout_failed', message: 'logout refused' } }, 401);
      }
      throw new Error(`Unexpected fixture request: ${init?.method ?? 'GET'} ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<UserLayout />, { station: 'user', role: 'user' });
    await screen.findByText('fixture-user');
    rendered.queryClient.setQueryData(userKeys.models, [model]);
    await rendered.user.click(screen.getByRole('button', { name: 'Sign out' }));
    await expect(screen.findByText('logout refused')).resolves.toBeVisible();
    expect(screen.getByRole('link', { name: 'Sign in' })).toBeVisible();
    expect(rendered.queryClient.getQueryData(userKeys.models)).toBeUndefined();
    expect(rendered.queryClient.getQueryData(userKeys.session)).toBeNull();
    expect(fetchMock.mock.calls.filter((call) => {
      const requestURL = new URL(String(call[0]), window.location.origin);
      return requestURL.pathname === '/api/session';
    })).toHaveLength(1);
  });

  test('closes steward management when the current user session fails', async () => {
    const levelFive = { ...session, user: { ...session.user, effective_level: 5 } };
    let healthy = true;
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      if (requestURL.pathname === '/api/session') {
        return healthy
          ? jsonResponse(levelFive)
          : jsonResponse({ error: { code: 'unauthorized', message: 'session expired' } }, 401);
      }
      throw new Error(`Unexpected fixture request: ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <><UserSessionProbe /><ManagementCapabilityProbe frame="steward" /></>,
      { station: 'user', role: 'level5' },
    );
    await screen.findByText('fixture-user');
    await waitFor(() => expect(screen.getByTestId('steward-capability-state')).toHaveTextContent('ready'));
    rendered.queryClient.setQueryData(charityManagementKeys.models('steward'), [managedModel]);
    healthy = false;
    await rendered.queryClient.refetchQueries({ queryKey: userKeys.session, exact: true, type: 'all' });
    await waitFor(() => expect(screen.getByTestId('user-session-state')).toHaveTextContent('error'));
    expect(screen.getByTestId('steward-capability-state')).toHaveTextContent('closed');
    expect(rendered.queryClient.getQueryData(charityManagementKeys.models('steward'))).toBeUndefined();
  });

  test('closes administrator management when the current admin session is invalid', async () => {
    const adminSession = { admin: { username: 'fixture-admin' } };
    let healthy = true;
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      if (requestURL.pathname === '/admin/api/session') {
        return healthy
          ? jsonResponse(adminSession)
          : jsonResponse({ error: { code: 'invalid_response', message: 'session invalid' } }, 200);
      }
      throw new Error(`Unexpected fixture request: ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(
      <><AdminSessionProbe /><ManagementCapabilityProbe frame="admin" /></>,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByText('fixture-admin');
    await waitFor(() => expect(screen.getByTestId('admin-capability-state')).toHaveTextContent('ready'));
    rendered.queryClient.setQueryData(charityManagementKeys.models('admin'), [managedModel]);
    healthy = false;
    await rendered.queryClient.refetchQueries({ queryKey: ['admin', 'session'], exact: true, type: 'all' });
    await waitFor(() => expect(screen.getByTestId('admin-session-state')).toHaveTextContent('error'));
    expect(screen.getByTestId('admin-capability-state')).toHaveTextContent('closed');
    expect(rendered.queryClient.getQueryData(charityManagementKeys.models('admin'))).toBeUndefined();
  });

  test('ignores a stale session failure after a newer administrator identity succeeds', async () => {
    const rendered = await renderWithProviders(<span>session fixture</span>, { station: 'admin', role: 'admin' });
    const client = rendered.queryClient;
    const staleGeneration = beginManagementSessionRequest(client, 'admin');
    const freshGeneration = beginManagementSessionRequest(client, 'admin');
    expect(noteManagementSessionSuccess(client, 'admin', { admin: { username: 'new-admin' } }, freshGeneration)).toBe(true);
    client.setQueryData(charityManagementKeys.capability('admin'), false);
    client.setQueryData(charityManagementKeys.models('admin'), [managedModel]);
    expect(failManagementSessionRequest(client, 'admin', staleGeneration)).toBe(false);
    expect(client.getQueryData(charityManagementKeys.capability('admin'))).toBe(false);
    expect(client.getQueryData(charityManagementKeys.models('admin'))).toEqual([managedModel]);
  });

  test('keeps explicit logout closed until a new authoritative steward session succeeds', async () => {
    const rendered = await renderWithProviders(<span>session fixture</span>, { station: 'user', role: 'level5' });
    const client = rendered.queryClient;
    const initialGeneration = beginManagementSessionRequest(client, 'steward');
    expect(noteManagementSessionSuccess(client, 'steward', {
      user: { ...session.user, effective_level: 5 },
    }, initialGeneration)).toBe(true);
    client.setQueryData(charityManagementKeys.capability('steward'), false);
    client.setQueryData(charityManagementKeys.models('steward'), [managedModel]);
    const staleGeneration = beginManagementSessionRequest(client, 'steward');
    clearManagementSession(client, 'steward');
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    expect(client.getQueryData(charityManagementKeys.models('steward'))).toBeUndefined();
    expect(noteManagementSessionSuccess(client, 'steward', {
      user: { ...session.user, effective_level: 5 },
    }, staleGeneration)).toBe(false);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(true);
    const freshGeneration = beginManagementSessionRequest(client, 'steward');
    expect(noteManagementSessionSuccess(client, 'steward', {
      user: { ...session.user, effective_level: 5 },
    }, freshGeneration)).toBe(true);
    expect(client.getQueryData(charityManagementKeys.capability('steward'))).toBe(false);
  });

  test('treats key-delete 404 reconciliation as deletion and removes dependent projections', async () => {
    let keyReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (method === 'GET' && requestURL.pathname === '/api/session') return jsonResponse(session);
      if (method === 'GET' && requestURL.pathname === '/api/endpoints') return jsonResponse([endpoint]);
      if (method === 'GET' && requestURL.pathname === '/api/endpoints/1/keys') {
        keyReads += 1;
        return keyReads === 1 ? jsonResponse([endpointKey]) : jsonResponse({ error: { code: 'not_found', message: 'deleted' } }, 404);
      }
      if (method === 'GET' && requestURL.pathname === '/api/endpoints/1/keys/2/models') {
        return jsonResponse({ error: { code: 'not_found', message: 'deleted' } }, 404);
      }
      if (method === 'DELETE' && requestURL.pathname === '/api/endpoints/1/keys/2') {
        return jsonResponse({ error: { code: 'not_found', message: 'already deleted' } }, 404);
      }
      throw new Error(`Unexpected fixture request: ${method} ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<EndpointsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Endpoint management' });
    await rendered.user.click(screen.getByRole('button', { name: 'Endpoint keys' }));
    await screen.findByText('sk-a…tail');
    rendered.queryClient.setQueryData([...userKeys.keyModels('1', '2'), 'openai-compatible'], [{ id: 'cached-model' }]);
    rendered.queryClient.setQueryData(userKeys.models, [model]);
    rendered.queryClient.setQueryData(userKeys.bindings('3'), [{ id: 'cached-binding' }]);
    rendered.queryClient.setQueryData(userKeys.donations, [{ id: 'cached-donation' }]);
    await rendered.user.click(screen.getByRole('button', { name: 'Delete key' }));
    const dialog = screen.getByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Delete key' }));
    await waitFor(() => expect(rendered.queryClient.getQueryData(userKeys.keyModels('1', '2'))).toBeUndefined());
    expect(rendered.queryClient.getQueryData(userKeys.endpointKeys('1'))).toBeUndefined();
    expect(rendered.queryClient.getQueryData(userKeys.models)).toBeUndefined();
    expect(rendered.queryClient.getQueryData(userKeys.bindings('3'))).toBeUndefined();
    expect(rendered.queryClient.getQueryData(userKeys.donations)).toBeUndefined();
    expect(screen.queryByText('sk-a…tail')).toBeNull();
  });

  test('treats model-delete 404 as deletion and evicts model dependents', async () => {
    let modelReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
      if (method === 'GET' && requestURL.pathname === '/api/session') return jsonResponse(session);
      if (method === 'GET' && requestURL.pathname === '/api/models') {
        modelReads += 1;
        return modelReads === 1 ? jsonResponse([model]) : jsonResponse({ error: { code: 'not_found', message: 'deleted' } }, 404);
      }
      if (method === 'DELETE' && requestURL.pathname === '/api/models/3') {
        return jsonResponse({ error: { code: 'not_found', message: 'already deleted' } }, 404);
      }
      throw new Error(`Unexpected fixture request: ${method} ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<ModelsPage />, { station: 'user', role: 'user' });
    await screen.findByRole('heading', { name: 'Models and bindings' });
    rendered.queryClient.setQueryData(userKeys.bindings('3'), [{ id: 'cached-binding' }]);
    rendered.queryClient.setQueryData(userKeys.donations, [{ id: 'cached-donation' }]);
    await rendered.user.click(screen.getByRole('button', { name: 'Delete model' }));
    const dialog = screen.getByRole('alertdialog');
    await rendered.user.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(rendered.queryClient.getQueryData(userKeys.models)).toBeUndefined());
    expect(rendered.queryClient.getQueryData(userKeys.bindings('3'))).toBeUndefined();
    expect(rendered.queryClient.getQueryData(userKeys.donations)).toBeUndefined();
  });

  test('treats an owner donation delete 404 as already deleted', async () => {
    const donation = {
      id: 31,
      endpoint_base_url: 'https://pending-upstream.test/v1',
      status: 'pending',
      enabled: false,
      description: 'already removed donation',
      review_note: '',
      expires_at: null,
      created_at: 1,
      updated_at: 2,
      keys: [],
      reviews: [],
    };
    let listReads = 0;
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const requestURL = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
      const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      if (method === 'GET' && requestURL.pathname === '/api/session') return jsonResponse(session);
      if (method === 'GET' && requestURL.pathname === '/api/charity/models') return jsonResponse({ data: [] });
      if (method === 'GET' && requestURL.pathname === '/api/endpoints') return jsonResponse([]);
      if (method === 'GET' && requestURL.pathname === '/api/donations') {
        listReads += 1;
        return jsonResponse(listReads === 1 ? [donation] : []);
      }
      if (method === 'GET' && requestURL.pathname === '/api/donations/31') return jsonResponse(donation);
      if (method === 'DELETE' && requestURL.pathname === '/api/donations/31') {
        return jsonResponse({ error: { code: 'not_found', message: 'already deleted' } }, 404);
      }
      throw new Error(`Unexpected fixture request: ${method} ${requestURL.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<CharityPage />, { station: 'user', role: 'user' });
    await screen.findByText('already removed donation');
    const softDelete = await screen.findByRole('button', { name: 'Soft delete' });
    await waitFor(() => expect(softDelete).toBeEnabled());
    await rendered.user.click(softDelete);
    await rendered.user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Soft delete' }));

    await waitFor(() => expect(screen.queryByText('already removed donation')).toBeNull());
    expect(screen.queryByText('already deleted')).toBeNull();
    expect(rendered.queryClient.getQueryData(userKeys.donation('31'))).toBeUndefined();
    expect(listReads).toBeGreaterThan(1);
  });
});
