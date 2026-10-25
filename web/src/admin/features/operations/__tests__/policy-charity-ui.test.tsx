vi.mock('@shared/components/AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));
import { CharityManagement } from '@shared/components/CharityManagement';
import { charityKeys } from '@shared/operations/charity';
import type { DonationUse } from '@shared/operations/donationUses';
import type {
  AdminDonation,
  CharityModel,
  ManagedDonationKey,
  StewardDonation,
} from '@shared/operations/charity';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { useUserSession } from '../../../../user/data';
import { useAdminSession } from '../../../data';

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
    global_rpm_limit: null,
    effective_global_rpm_limit: '60',
    light_rpm_limit: null,
    effective_light_rpm_limit: '30',
    light_concurrency_limit: null,
    effective_light_concurrency_limit: '15',
    balance: '0',
    game_balance: '0',
    donation_credit: '0',
    effective_level: 6,
    level_display_name: 'Lv6',
    game_profile_public: false,
    charity_profile_public: false,
    automatic_restrictions: [],
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
  rate_limited_until: null,
  key_limits_revision: '1',
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
  failure_disable_threshold: '10',
  smart_backoff: null,
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
    use_summary: { total: '2', pending: '2', approved: '0', review_status: 'pending' },
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
    rate_limited: '0',
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
    ...(donation.use_summary ? { use_summary: donation.use_summary } : {}),
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

const siteOffsetMinutes = 330;

function approve(current: AdminDonation, body: Record<string, unknown>): AdminDonation {
  return {
    ...current,
    status: 'approved',
    use_summary: { total: '2', pending: '0', approved: '2', review_status: 'approved' },
    revision: String(Number(current.revision) + 1),
    review_result: {
      decision: 'approve',
      reason: String(body.reason),
      reviewed_at: 1_735_689_700,
    },
    keys: current.keys.map((key) => ({
      ...key,
      charity_state: 'available',
      rate_limited_until: null,
    })),
    reviewer: { user_id: '9', role: 'admin' },
    updated_at: 1_735_689_700,
  };
}

function donationUsesPage(donation: AdminDonation | StewardDonation, url: URL) {
  const pending = donation.use_summary?.pending !== '0';
  const version = {
    credential_id: '8',
    credential_version: '2',
    grant_revision: '1',
    status: pending ? ('pending' as const) : ('approved' as const),
    review_note: '',
    reviewed_at: pending ? null : 1_735_689_700,
    expected_review_revision: 'review-11',
    discovery_source_id: null,
    discovery_source_revision: null,
    display_head: 'head',
    display_tail: 'tail',
    review: { required: true, revision: 'review-11', material_available: true },
    discovery: null,
  };
  const rows: DonationUse[] =
    donation.use_summary?.total === '0'
      ? []
      : (['chat_completions', 'responses'] as const).map((native_service, index) => ({
          id: String(31 + index),
          donation_id: donation.id,
          donation_key_id: donation.keys[0].id,
          endpoint_key_id: donation.keys[0].endpoint_key_id,
          native_service,
          enabled: true,
          discovery_enabled: false,
          revision: '1',
          pending: pending ? version : null,
          approved: pending ? null : version,
          latest: version,
          health: null,
          capabilities: {
            can_review: pending,
            can_manage: true,
            can_reset_failures: true,
            can_discover: false,
            can_bind: true,
            unavailable_reason: null,
          },
        }));
  const data = url.searchParams.get('state') === 'pending' && !pending ? [] : rows;
  return {
    data,
    pagination: {
      page: '1',
      page_size: Number(url.searchParams.get('page_size')),
      total_items: String(data.length),
      total_pages: '1',
    },
  };
}

const operationKeyPattern = /^[A-Za-z0-9_-]{22,128}$/;

describe('Generation 2 charity management policy', () => {
  it('preserves the authoritative pending expiry and recovers an unknown result with the same atomic request', async () => {
    const expiresAt = 1_735_776_005;
    let current = pendingAdminDonation(expiresAt);
    current.keys[0] = {
      ...current.keys[0],
      limits: { price: '12.345', calls: '50', tokens: '900' },
      token_reserve: 64,
    };
    const originalKey = structuredClone(current.keys[0]);
    const reviewRequests: RequestInit[] = [];
    let committedReviews = 0;
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      const path = url.pathname;
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path === '/admin/api/session') return jsonResponse(adminSession);
      if (method === 'GET' && path === '/admin/api/time-context')
        return jsonResponse({ mode: 'site', offset_minutes: siteOffsetMinutes });
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
      if (method === 'GET' && path === '/admin/api/donations/1/uses') {
        expect(url.searchParams.get('page')).toBe('1');
        return jsonResponse(donationUsesPage(current, url));
      }
      if (method === 'GET' && path === '/admin/api/donations/1/keys') {
        expectNumberedPageQuery(url);
        return jsonResponse(
          managementNumberedPage(current.keys.map((key) => keyPageSummary(current, key))),
        );
      }
      if (method === 'POST' && path === '/admin/api/donations/1/pending-review') {
        reviewRequests.push(init ?? {});
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        if (reviewRequests.length === 1) {
          current = approve(current, body);
          committedReviews++;
          return errorResponse(500, 'internal');
        }
        expect(init?.body).toBe(reviewRequests[0].body);
        expect(new Headers(init?.headers).get('Idempotency-Key')).toBe(
          new Headers(reviewRequests[0].headers).get('Idempotency-Key'),
        );
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
    await view.user.type(
      await screen.findByLabelText('Review reason'),
      'approved with retained expiry',
    );
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));

    await waitFor(() => expect(screen.getAllByText('All approved').length).toBeGreaterThan(0));
    expect(reviewRequests).toHaveLength(2);
    expect(committedReviews).toBe(1);
    expect(JSON.parse(String(reviewRequests[0].body))).toEqual({
      expected_revision: '7',
      decision: 'approve',
      reason: 'approved with retained expiry',
      uses: [
        { use_id: '31', expected_revision: '1', expected_review_revision: 'review-11' },
        { use_id: '32', expected_revision: '1', expected_review_revision: 'review-11' },
      ],
    });
    expect(current.keys[0]).toEqual({ ...originalKey, charity_state: 'available' });
    expect(new Headers(reviewRequests[0].headers).get('Idempotency-Key')).toMatch(
      operationKeyPattern,
    );
  });

  it('preserves existing exact quota maxima and expiry without review overrides', async () => {
    let current = pendingAdminDonation(1_735_776_005);
    current.keys[0] = {
      ...current.keys[0],
      limits: { price: '9000000000000', calls: '9000000000000000', tokens: '9000000000000000' },
      token_reserve: 2_147_483_647,
      safe_note: '🫶'.repeat(256),
    };
    const originalKey = structuredClone(current.keys[0]);
    const reviewRequests: RequestInit[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      const path = url.pathname;
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path === '/admin/api/session') return jsonResponse(adminSession);
      if (method === 'GET' && path === '/admin/api/time-context')
        return jsonResponse({ mode: 'site', offset_minutes: siteOffsetMinutes });
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
      if (method === 'GET' && path === '/admin/api/donations/1/uses') {
        expect(url.searchParams.get('page')).toBe('1');
        return jsonResponse(donationUsesPage(current, url));
      }
      if (method === 'GET' && path === '/admin/api/donations/1/keys') {
        expectNumberedPageQuery(url);
        return jsonResponse(
          managementNumberedPage(current.keys.map((key) => keyPageSummary(current, key))),
        );
      }
      if (method === 'POST' && path === '/admin/api/donations/1/pending-review') {
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
    await view.user.type(
      await screen.findByLabelText('Review reason'),
      'retain exact authorized limits',
    );
    for (const label of [
      'Price limit (credits)',
      'Call limit',
      'Token limit',
      'Token reserve',
      'Effective expiry',
    ]) {
      expect(screen.queryByLabelText(label)).not.toBeInTheDocument();
    }
    expect(
      screen.queryByRole('checkbox', {
        name: 'I confirm this review result and its per-key consequences.',
      }),
    ).not.toBeInTheDocument();
    expect(reviewRequests).toHaveLength(0);
    await view.user.click(screen.getByRole('button', { name: 'Approve pending content' }));

    await waitFor(() => expect(reviewRequests).toHaveLength(1));
    expect(JSON.parse(String(reviewRequests[0].body))).toEqual({
      expected_revision: '7',
      decision: 'approve',
      reason: 'retain exact authorized limits',
      uses: [
        { use_id: '31', expected_revision: '1', expected_review_revision: 'review-11' },
        { use_id: '32', expected_revision: '1', expected_review_revision: 'review-11' },
      ],
    });
    expect(current.keys[0]).toEqual({ ...originalKey, charity_state: 'available' });
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
      if (method === 'GET' && path === '/admin/api/time-context')
        return jsonResponse({ mode: 'site', offset_minutes: siteOffsetMinutes });
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
          recurring_discount: {
            enabled: false,
            time_zone: 'UTC',
            default_percent: 100,
            periods: [],
          },
          effective_discount: { source: 'none', percent: 100, next_change_at: null },
          server_now: 1800000000,

          model_types: ['chat_completions', 'embeddings'],
          route_strategy: body.route_strategy as CharityModel['route_strategy'],
          id: '1',
          provider: String(body.provider),
          model: String(body.model),
          full_name: `[公益]${String(body.provider)}/${String(body.model)}`,
          enabled: true,
          allowed_levels: body.allowed_levels as number[],
          is_mainstream: body.is_mainstream as boolean,
          excluded_request_fields: body.excluded_request_fields as string[],
          public_description: String(body.public_description),
          token_reserve_credits:
            typeof body.token_reserve_credits === 'string' ? body.token_reserve_credits : null,
          pricing: body.pricing as CharityModel['pricing'],
          discount: body.discount as CharityModel['discount'],
          transport_rule: 'passthrough',
          cloudflare_proxy_enabled: false,
          cloudflare_proxy_mode: 'direct',
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
    await view.user.click(screen.getByRole('button', { name: 'Create charity model' }));
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
      allowed_levels: [1, 2, 3, 4, 5, 6],
      is_mainstream: false,
      excluded_request_fields: [],
      public_description: '',
      pricing: { mode: 'per_request', user_price: '1.001', donor_reward: '0' },
    });
    expect(new Headers(createRequests[0].headers).get('Idempotency-Key')).toMatch(
      operationKeyPattern,
    );
  });

  it('renders the allowed steward owner projection without extra personal fields', async () => {
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

    expect(await screen.findByText('Current steward')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByText('forbidden')).not.toBeInTheDocument();
    expect(screen.queryByText('No donations')).not.toBeInTheDocument();
  });

  it.each([401, 403])(
    'clears the lost detail projection after HTTP %s while retaining only authorized views',
    async (status) => {
      const item = stewardDonation();
      const detailItem = {
        ...item,
        description: 'Detail-only sensitive projection',
        use_summary: { total: '0', pending: '0', approved: '0' },
      };
      let detailAllowed = true;
      let detailReads = 0;
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
          detailReads++;
          return detailAllowed
            ? jsonResponse(detailItem)
            : errorResponse(status, status === 401 ? 'unauthorized' : 'forbidden');
        }
        if (method === 'GET' && path === '/api/steward/donations/2/keys') {
          expectNumberedPageQuery(url);
          return jsonResponse(
            managementNumberedPage(item.keys.map((key) => keyPageSummary(item, key))),
          );
        }
        if (method === 'GET' && path === '/api/steward/donations/2/uses')
          return jsonResponse(donationUsesPage(detailItem, url));
        throw new Error('Unexpected request: ' + method + ' ' + path);
      });
      vi.stubGlobal('fetch', fetchMock);
      const view = await renderWithProviders(
        <SessionBackedManagement frame="steward" onCapabilityLoss={onCapabilityLoss} />,
        { station: 'user', role: 'user' },
      );

      expect(await screen.findByRole('cell', { name: /^My donation/ })).toBeInTheDocument();
      await view.user.click(screen.getByRole('button', { name: 'Review' }));
      expect(await screen.findByText('Detail-only sensitive projection')).toBeVisible();
      detailAllowed = false;
      await view.queryClient.invalidateQueries({ queryKey: charityKeys.donation('steward', '2') });
      await waitFor(() => expect(detailReads).toBe(2));
      await waitFor(() =>
        expect(screen.queryByText('Detail-only sensitive projection')).not.toBeInTheDocument(),
      );
      if (status === 401) {
        await waitFor(() => expect(onCapabilityLoss).toHaveBeenCalledTimes(1));
        expect(screen.queryByRole('cell', { name: /^My donation/ })).not.toBeInTheDocument();
        expect(
          screen.queryByRole('checkbox', { name: 'My donation', hidden: true }),
        ).not.toBeInTheDocument();
        expect(screen.getByRole('alert')).toHaveTextContent(/access.*no longer/i);
      } else {
        expect(onCapabilityLoss).not.toHaveBeenCalled();
        expect(screen.getByRole('cell', { name: /^My donation/ })).toBeInTheDocument();
        expect(screen.getByRole('alert')).toHaveTextContent(/forbidden|permission|access/i);
      }
    },
  );
});
