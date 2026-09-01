import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CharityManagement } from '@shared/components/CharityManagement';
import type {
  AdminDonation,
  CharityModel,
  ManagedDonationKey,
  StewardDonation,
} from '@shared/operations/charity';
import { renderWithProviders } from '../../../../../test/unit/support';

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function errorResponse(status: number, code: string): Response {
  return jsonResponse({ error: { code, message: `request failed: ${code}` } }, status);
}

const managedKey = (overrides: Partial<ManagedDonationKey> = {}): ManagedDonationKey => ({
  id: '11',
  endpoint_key_id: '21',
  display_head: 'head',
  display_tail: 'tail',
  safe_source: { base_url: 'https://example.test/v1', connector_type: 'openai' },
  physical_enabled: true,
  charity_state: 'pending',
  limits: { price: '0', calls: '0', tokens: '0' },
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
  safe_note: '',
  ...overrides,
});

function pendingAdminDonation(expiresAt: number | null = null): AdminDonation {
  return {
    id: '1',
    status: 'pending',
    revision: '7',
    description: 'Generation 2 donor submission',
    review_result: null,
    expires_at: expiresAt,
    keys: [managedKey()],
    owner: { user_id: '7', discord_id: 'discord-7', display_name: 'Admin-visible donor' },
    reviewer: null,
    created_at: 1_735_689_600,
    updated_at: 1_735_689_600,
  };
}

function stewardDonation(): StewardDonation {
  return {
    id: '2',
    status: 'pending',
    revision: '3',
    description: 'My donation',
    review_result: null,
    expires_at: null,
    keys: [managedKey({ id: '12', endpoint_key_id: '22' })],
    owner: { user_id: '8', display_name: 'Current steward' },
    reviewer: null,
    created_at: 1_735_689_600,
    updated_at: 1_735_689_600,
  };
}

const datePart = (value: number) => String(value).padStart(2, '0');
function localDateTime(epoch: number): string {
  const date = new Date(epoch * 1_000);
  return `${date.getFullYear()}-${datePart(date.getMonth() + 1)}-${datePart(date.getDate())}T${datePart(date.getHours())}:${datePart(date.getMinutes())}:${datePart(date.getSeconds())}`;
}

function approve(current: AdminDonation, body: Record<string, unknown>): AdminDonation {
  return {
    ...current,
    status: 'approved',
    revision: String(Number(current.revision) + 1),
    review_result: {
      decision: 'approve',
      reason: String(body.reason),
      reviewed_at: 1_735_689_700,
    },
    expires_at: body.expires_at as number | null,
    keys: current.keys.map((key) => ({ ...key, charity_state: 'available' })),
    reviewer: { user_id: '9', role: 'admin' },
    updated_at: 1_735_689_700,
  };
}

const operationKeyPattern = /^[A-Za-z0-9_-]{22,128}$/;

describe('Generation 2 charity management policy', () => {
  it('preserves the authoritative pending expiry and reconciles an unknown result without replaying', async () => {
    const expiresAt = 1_735_776_005;
    let current = pendingAdminDonation(expiresAt);
    const reviewRequests: RequestInit[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path.startsWith('/admin/api/donations?')) {
        return jsonResponse({ data: [current], next_cursor: null });
      }
      if (method === 'GET' && path === '/admin/api/donations/1') return jsonResponse(current);
      if (method === 'POST' && path === '/admin/api/donations/1/review') {
        reviewRequests.push(init ?? {});
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        current = approve(current, body);
        return errorResponse(500, 'internal');
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<CharityManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    expect(await screen.findByLabelText('Whole-donation expiry')).toHaveValue(
      `${localDateTime(expiresAt)}.000`,
    );
    await view.user.type(screen.getByLabelText('Reason'), 'approved with retained expiry');
    await view.user.click(
      screen.getByRole('checkbox', {
        name: 'I confirm this review result and its whole-donation consequences.',
      }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Approve donation' }));

    await waitFor(() => expect(screen.getAllByText('approved').length).toBeGreaterThan(0));
    expect(reviewRequests).toHaveLength(1);
    const body = JSON.parse(String(reviewRequests[0].body)) as Record<string, unknown>;
    expect(body.expires_at).toBe(expiresAt);
    expect(new Headers(reviewRequests[0].headers).get('Idempotency-Key')).toMatch(
      operationKeyPattern,
    );
  });

  it('blocks non-canonical or out-of-range review values and submits exact maxima', async () => {
    let current = pendingAdminDonation();
    const reviewRequests: RequestInit[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path.startsWith('/admin/api/donations?')) {
        return jsonResponse({ data: [current], next_cursor: null });
      }
      if (method === 'GET' && path === '/admin/api/donations/1') return jsonResponse(current);
      if (method === 'POST' && path === '/admin/api/donations/1/review') {
        reviewRequests.push(init ?? {});
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        current = approve(current, body);
        return jsonResponse(current);
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<CharityManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Review pending submission' });
    fireEvent.change(screen.getByLabelText('Price limit'), { target: { value: '0.0000' } });
    fireEvent.change(screen.getByLabelText('Call limit'), { target: { value: '01' } });
    fireEvent.change(screen.getByLabelText('Token reserve'), {
      target: { value: '2147483648' },
    });
    fireEvent.change(screen.getByLabelText('Reviewer-safe note'), {
      target: { value: '🫶'.repeat(257) },
    });
    await view.user.type(screen.getByLabelText('Reason'), 'exact boundary review');
    await view.user.click(
      screen.getByRole('checkbox', {
        name: 'I confirm this review result and its whole-donation consequences.',
      }),
    );
    expect(screen.getByRole('button', { name: 'Approve donation' })).toBeDisabled();
    expect(reviewRequests).toHaveLength(0);

    fireEvent.change(screen.getByLabelText('Price limit'), {
      target: { value: '9000000000000' },
    });
    fireEvent.change(screen.getByLabelText('Call limit'), {
      target: { value: '9000000000000000' },
    });
    fireEvent.change(screen.getByLabelText('Token limit'), {
      target: { value: '9000000000000000' },
    });
    fireEvent.change(screen.getByLabelText('Token reserve'), {
      target: { value: '2147483647' },
    });
    fireEvent.change(screen.getByLabelText('Reviewer-safe note'), {
      target: { value: '🫶'.repeat(256) },
    });
    const approveButton = screen.getByRole('button', { name: 'Approve donation' });
    expect(approveButton).toBeEnabled();
    await view.user.click(approveButton);

    await waitFor(() => expect(reviewRequests).toHaveLength(1));
    const body = JSON.parse(String(reviewRequests[0].body)) as {
      expected_revision: string;
      key_settings: Record<string, unknown>[];
    };
    expect(body.expected_revision).toBe('7');
    expect(body.key_settings).toEqual([
      {
        donation_key_id: '11',
        price_limit: '9000000000000',
        calls_limit: '9000000000000000',
        tokens_limit: '9000000000000000',
        token_reserve: 2_147_483_647,
        enabled: true,
        safe_note: '🫶'.repeat(256),
      },
    ]);
    expect(new Headers(reviewRequests[0].headers).get('Idempotency-Key')).toMatch(
      operationKeyPattern,
    );
  });

  it('requires canonical model prices before creating a Generation 2 model', async () => {
    let models: CharityModel[] = [];
    const createRequests: RequestInit[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path.startsWith('/admin/api/donations?')) {
        return jsonResponse({ data: [], next_cursor: null });
      }
      if (method === 'GET' && path.startsWith('/admin/api/charity-models?')) {
        return jsonResponse({ data: models, next_cursor: null });
      }
      if (method === 'POST' && path === '/admin/api/charity-models') {
        createRequests.push(init ?? {});
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        const model: CharityModel = {
          id: '1',
          provider: String(body.provider),
          model: String(body.model),
          full_name: `[公益]${String(body.provider)}/${String(body.model)}`,
          enabled: true,
          pricing: body.pricing as CharityModel['pricing'],
          discount: body.discount as CharityModel['discount'],
          flatten_tool_calls: false,
          revision: '1',
          binding_revision: '0',
          binding_count: '0',
          rolling_success: { sample_count: '0', success_count: '0', percent: null },
          created_at: 1,
          updated_at: 1,
        };
        models = [model];
        return jsonResponse(model, 201);
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<CharityManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(screen.getByRole('tab', { name: 'Models and bindings' }));
    await view.user.type(screen.getByLabelText('Provider'), 'provider');
    await view.user.type(screen.getByLabelText('Model'), 'model');
    fireEvent.change(screen.getByLabelText('User price'), { target: { value: '1.0000' } });
    expect(screen.getByRole('button', { name: 'Create model' })).toBeDisabled();
    fireEvent.change(screen.getByLabelText('User price'), { target: { value: '1.001' } });
    const create = screen.getByRole('button', { name: 'Create model' });
    expect(create).toBeEnabled();
    await view.user.click(create);

    await waitFor(() => expect(createRequests).toHaveLength(1));
    const body = JSON.parse(String(createRequests[0].body)) as Record<string, unknown>;
    expect(body).toMatchObject({
      pricing: { mode: 'per_request', user_price: '1.001', donor_reward: '0' },
    });
    expect(new Headers(createRequests[0].headers).get('Idempotency-Key')).toMatch(
      operationKeyPattern,
    );
  });

  it('fails closed when a steward response contains administrator-only identity fields', async () => {
    const invalid = stewardDonation() as unknown as Record<string, unknown>;
    invalid.owner = { user_id: '8', display_name: 'Current steward', discord_id: 'forbidden' };
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      expect(String(input)).toMatch(/^\/api\/steward\/donations\?/);
      return jsonResponse({ data: [invalid], next_cursor: null });
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithProviders(<CharityManagement frame="steward" />, {
      station: 'user',
      role: 'user',
    });

    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid/i);
    expect(screen.queryByText('forbidden')).not.toBeInTheDocument();
    expect(screen.queryByText('No donations')).not.toBeInTheDocument();
  });

  it('clears the rendered steward projection after a detail read loses authority', async () => {
    const item = stewardDonation();
    const onCapabilityLoss = vi.fn();
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path.startsWith('/api/steward/donations?')) {
        return jsonResponse({ data: [item], next_cursor: null });
      }
      if (method === 'GET' && path === '/api/steward/donations/2') {
        return errorResponse(403, 'forbidden');
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(
      <CharityManagement frame="steward" onCapabilityLoss={onCapabilityLoss} />,
      { station: 'user', role: 'user' },
    );

    expect(await screen.findByText('My donation')).toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Review' }));
    await waitFor(() => expect(onCapabilityLoss).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByText('My donation')).not.toBeInTheDocument());
    expect(screen.getByRole('alert')).toHaveTextContent(/access.*no longer/i);
  });
});
