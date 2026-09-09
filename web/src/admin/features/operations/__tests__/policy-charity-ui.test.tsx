import { fireEvent, screen, waitFor } from '@testing-library/react';
import { useAdminSession } from '../../../data';
import { useUserSession } from '../../../../user/data';
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

const adminSession = { admin: { username: 'fixture-admin' } };

const stewardSession = {
  user: {
    id: '1',
    username: 'fixture-steward',
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
    effective_level: 5,
    level_display_name: 'Lv5',
    game_profile_public: false,
    created_at: 1,
    updated_at: 1,
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

const managedKey = (overrides: Partial<ManagedDonationKey> = {}): ManagedDonationKey => ({
  id: '11',
  binding_count: '0',
  idle: true,
  endpoint_key_id: '21',
  display_head: 'head',
  display_tail: 'tail',
  safe_source: {
    kind: 'custom',
    base_url: 'https://example.test/v1',
    connector_type: 'openai-compatible',
  },
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
  authorized_expires_at: null,
  expires_at: null,
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
    handling: {
      state: 'pending',
      revision: '1',
      processed_at: null,
      processed_by_role: null,
      closed_at: null,
      closed_reason: null,
    },
    description: 'Generation 2 donor submission',
    review_result: null,
    keys: [managedKey({ authorized_expires_at: expiresAt, expires_at: expiresAt })],
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
    handling: {
      state: 'pending',
      revision: '1',
      processed_at: null,
      processed_by_role: null,
      closed_at: null,
      closed_reason: null,
    },
    description: 'My donation',
    review_result: null,
    keys: [managedKey({ id: '12', endpoint_key_id: '22' })],
    owner: { user_id: '8', discord_id: 'steward-discord-8', display_name: 'Current steward' },
    reviewer: null,
    created_at: 1_735_689_600,
    updated_at: 1_735_689_600,
  };
}

function managementNumberedPage<T>(data: T[]) {
  return {
    data,
    next_cursor: null,
    pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
  };
}

function expectNumberedPageQuery(url: URL) {
  expect(url.searchParams.get('page')).toBe('1');
  expect(url.searchParams.get('page_size')).toBe('20');
}

function donationPageSummary(donation: AdminDonation | StewardDonation) {
  const stateCounts: Record<string, string> = {
    available: '0',
    pending: '0',
    disabled: '0',
    suspended: '0',
    exhausted: '0',
    expired: '0',
    ended: '0',
  };
  for (const key of donation.keys) {
    stateCounts[key.charity_state] = String(Number(stateCounts[key.charity_state]) + 1);
  }
  const source = donation.keys[0]?.safe_source;
  const owner = donation.owner;
  return {
    id: donation.id,
    status: donation.status,
    revision: donation.revision,
    handling: donation.handling,
    description: donation.description,
    review_result: donation.review_result,
    created_at: donation.created_at,
    updated_at: donation.updated_at,
    key_count: String(donation.keys.length),
    state_counts: stateCounts,
    source_count: source ? '1' : '0',
    sources: source ? [source] : [],
    reviewer: donation.reviewer,
    owner: owner === null ? null : owner,
  };
}

function keyPageSummary(donation: AdminDonation | StewardDonation, key: ManagedDonationKey) {
  return {
    ...key,
    donation_id: donation.id,
    key_id: key.id,
    donation_revision: donation.revision,
    rule_count: '0',
    rules: [],
    handling: donation.handling,
    max_concurrency: key.max_concurrency ?? null,
    max_rpm: key.max_rpm ?? null,
  };
}

function SessionBackedManagement({
  frame,
  onCapabilityLoss,
}: {
  frame: 'admin' | 'steward';
  onCapabilityLoss?: () => void;
}) {
  const admin = useAdminSession(frame === 'admin');
  const user = useUserSession(frame === 'steward');
  const accountId =
    frame === 'admin'
      ? admin.data
        ? `admin:${admin.data.admin.username}`
        : undefined
      : user.data?.user.id;
  return accountId ? (
    <CharityManagement frame={frame} accountId={accountId} onCapabilityLoss={onCapabilityLoss} />
  ) : null;
}

const datePart = (value: number) => String(value).padStart(2, '0');
function localDateTime(epoch: number): string {
  const date = new Date(epoch * 1_000);
  return `${date.getFullYear()}-${datePart(date.getMonth() + 1)}-${datePart(date.getDate())}T${datePart(date.getHours())}:${datePart(date.getMinutes())}:${datePart(date.getSeconds())}`;
}

function approve(current: AdminDonation, body: Record<string, unknown>): AdminDonation {
  const settings = body.key_settings as { donation_key_id: string; expires_at: number | null }[];
  return {
    ...current,
    status: 'approved',
    revision: String(Number(current.revision) + 1),
    review_result: {
      decision: 'approve',
      reason: String(body.reason),
      reviewed_at: 1_735_689_700,
    },
    keys: current.keys.map((key) => ({
      ...key,
      charity_state: 'available',
      expires_at:
        settings.find((setting) => setting.donation_key_id === key.id)?.expires_at ?? null,
    })),
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
      const url = new URL(String(input), 'https://example.test');
      const path = url.pathname;
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path === '/admin/api/session') return jsonResponse(adminSession);
      if (method === 'GET' && path === '/admin/api/time-zones')
        return jsonResponse({
          version: 'go1.26.6-zoneinfo',
          zones: ['America/Indianapolis', 'UTC'],
        });
      if (method === 'GET' && path === '/admin/api/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(managementNumberedPage([donationPageSummary(current)]));
      }
      if (method === 'GET' && path === '/admin/api/donations/1') return jsonResponse(current);
      if (method === 'GET' && path === '/admin/api/donations/1/keys') {
        expectNumberedPageQuery(url);
        return jsonResponse(
          managementNumberedPage(current.keys.map((key) => keyPageSummary(current, key))),
        );
      }
      if (method === 'POST' && path === '/admin/api/donations/1/review') {
        reviewRequests.push(init ?? {});
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        current = approve(current, body);
        return errorResponse(500, 'internal');
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    expect(await screen.findByLabelText('Effective expiry')).toHaveValue(
      `${localDateTime(expiresAt)}.000`,
    );
    await view.user.type(screen.getByLabelText('Reason'), 'approved with retained expiry');
    await view.user.click(
      screen.getByRole('checkbox', {
        name: 'I confirm this review result and its per-key consequences.',
      }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Approve donation' }));

    await waitFor(() => expect(screen.getAllByText('Approved').length).toBeGreaterThan(0));
    expect(reviewRequests).toHaveLength(1);
    const body = JSON.parse(String(reviewRequests[0].body)) as Record<string, unknown>;
    expect(body).not.toHaveProperty('expires_at');
    expect((body.key_settings as { expires_at: number | null }[])[0]?.expires_at).toBe(expiresAt);
    expect(new Headers(reviewRequests[0].headers).get('Idempotency-Key')).toMatch(
      operationKeyPattern,
    );
  });

  it('blocks non-canonical or out-of-range review values and submits exact maxima', async () => {
    let current = pendingAdminDonation();
    const reviewRequests: RequestInit[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      const path = url.pathname;
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path === '/admin/api/session') return jsonResponse(adminSession);
      if (method === 'GET' && path === '/admin/api/time-zones')
        return jsonResponse({
          version: 'go1.26.6-zoneinfo',
          zones: ['America/Indianapolis', 'UTC'],
        });
      if (method === 'GET' && path === '/admin/api/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(managementNumberedPage([donationPageSummary(current)]));
      }
      if (method === 'GET' && path === '/admin/api/donations/1') return jsonResponse(current);
      if (method === 'GET' && path === '/admin/api/donations/1/keys') {
        expectNumberedPageQuery(url);
        return jsonResponse(
          managementNumberedPage(current.keys.map((key) => keyPageSummary(current, key))),
        );
      }
      if (method === 'POST' && path === '/admin/api/donations/1/review') {
        reviewRequests.push(init ?? {});
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        current = approve(current, body);
        return jsonResponse(current);
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Review pending submission' });
    fireEvent.change(screen.getByLabelText('Price limit (credits)'), {
      target: { value: '0.0000' },
    });
    fireEvent.change(screen.getByLabelText('Call limit'), { target: { value: '01' } });
    fireEvent.change(screen.getByLabelText('Token reserve'), {
      target: { value: '2147483648' },
    });
    fireEvent.change(screen.getByLabelText('Review note'), {
      target: { value: '🫶'.repeat(257) },
    });
    await view.user.type(screen.getByLabelText('Reason'), 'exact boundary review');
    await view.user.click(
      screen.getByRole('checkbox', {
        name: 'I confirm this review result and its per-key consequences.',
      }),
    );
    expect(screen.getByRole('button', { name: 'Approve donation' })).toBeDisabled();
    expect(reviewRequests).toHaveLength(0);

    fireEvent.change(screen.getByLabelText('Price limit (credits)'), {
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
    fireEvent.change(screen.getByLabelText('Review note'), {
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
        expires_at: null,
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
      const url = new URL(String(input), 'https://example.test');
      const path = url.pathname;
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path === '/admin/api/session') return jsonResponse(adminSession);
      if (method === 'GET' && path === '/admin/api/time-zones')
        return jsonResponse({
          version: 'go1.26.6-zoneinfo',
          zones: ['America/Indianapolis', 'UTC'],
        });
      if (method === 'GET' && path === '/admin/api/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(managementNumberedPage([]));
      }
      if (method === 'GET' && path === '/admin/api/charity-models') {
        expectNumberedPageQuery(url);
        return jsonResponse(managementNumberedPage(models));
      }
      if (method === 'POST' && path === '/admin/api/charity-models') {
        createRequests.push(init ?? {});
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        const model: CharityModel = {
          route_strategy: body.route_strategy as CharityModel['route_strategy'],
          id: '1',
          provider: String(body.provider),
          model: String(body.model),
          full_name: `[公益]${String(body.provider)}/${String(body.model)}`,
          enabled: true,
          allowed_levels: body.allowed_levels as number[],
          public_description: String(body.public_description),
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
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('tab', { name: 'Charity models and bindings' }));
    await view.user.type(screen.getByLabelText('Provider'), 'provider');
    await view.user.type(screen.getByLabelText('Model'), 'model');
    fireEvent.change(screen.getByLabelText('Request user price'), {
      target: { value: '1.0000' },
    });
    expect(screen.getByRole('button', { name: 'Add charity model' })).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Request user price'), {
      target: { value: '1.001' },
    });
    const create = screen.getByRole('button', { name: 'Add charity model' });
    expect(create).toBeEnabled();
    await view.user.click(create);

    await waitFor(() => expect(createRequests).toHaveLength(1));
    const body = JSON.parse(String(createRequests[0].body)) as Record<string, unknown>;
    expect(body).toMatchObject({
      allowed_levels: [1, 2, 3, 4, 5],
      public_description: '',
      pricing: { mode: 'per_request', user_price: '1.001', donor_reward: '0' },
    });
    expect(new Headers(createRequests[0].headers).get('Idempotency-Key')).toMatch(
      operationKeyPattern,
    );
  });

  it('fails closed when a steward response contains an unknown owner field', async () => {
    const donation = stewardDonation();
    const invalid = {
      ...donationPageSummary(donation),
      owner: {
        user_id: '8',
        display_name: 'Current steward',
        discord_id: 'steward-discord-8',
        email: 'forbidden',
      },
    };
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      const method = init?.method ?? 'GET';
      if (method === 'GET' && url.pathname === '/api/session') return jsonResponse(stewardSession);
      if (method === 'GET' && url.pathname === '/api/steward/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(managementNumberedPage([invalid]));
      }
      throw new Error(`Unexpected request: ${method} ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithProviders(<SessionBackedManagement frame="steward" />, {
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
      const url = new URL(String(input), 'https://example.test');
      const path = url.pathname;
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path === '/api/session') return jsonResponse(stewardSession);
      if (method === 'GET' && path === '/api/time-zones')
        return jsonResponse({
          version: 'go1.26.6-zoneinfo',
          zones: ['America/Indianapolis', 'UTC'],
        });
      if (method === 'GET' && path === '/api/steward/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(managementNumberedPage([donationPageSummary(item)]));
      }
      if (method === 'GET' && path === '/api/steward/donations/2') {
        return errorResponse(403, 'forbidden');
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(
      <SessionBackedManagement frame="steward" onCapabilityLoss={onCapabilityLoss} />,
      { station: 'user', role: 'user' },
    );

    expect(await screen.findByText('My donation')).toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Review' }));
    await waitFor(() => expect(onCapabilityLoss).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByText('My donation')).not.toBeInTheDocument());
    expect(screen.getByRole('alert')).toHaveTextContent(/access.*no longer/i);
  });
});
