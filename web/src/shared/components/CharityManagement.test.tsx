vi.mock('@shared/components/AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));
import type { AdminDonation, CharityModel, ManagedDonationKey } from '@shared/operations/charity';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { useAdminSession } from '../../admin/data';
import { useUserSession } from '../../user/data';
import { CharityManagement } from './CharityManagement';
import { CharityRolePolicyForm } from './CharityRolePolicyForm';
import { defaultStewardPermissions } from '@shared/operations/stewardPermissions';

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

const siteTimeContext = { mode: 'site', offset_minutes: 0 };

function numberedPage<T>(rows: readonly T[], url: URL) {
  const pageSize = Number(url.searchParams.get('page_size') ?? '20');
  const requestedPage = Number(url.searchParams.get('page') ?? '1');
  const totalPages = Math.max(1, Math.ceil(rows.length / pageSize));
  const page = Math.min(requestedPage, totalPages);
  return {
    data: rows.slice((page - 1) * pageSize, page * pageSize),
    next_cursor: null,
    pagination: {
      page: String(page),
      page_size: pageSize,
      total_items: String(rows.length),
      total_pages: String(totalPages),
    },
  };
}

function expectNumberedPageQuery(url: URL, filters: Record<string, string> = {}) {
  const page = url.searchParams.get('page');
  const pageSize = url.searchParams.get('page_size');
  expect(page).toMatch(/^[1-9]\d*$/);
  expect(pageSize).toMatch(/^(10|20|50|100)$/);
  const expected = new URLSearchParams(filters);
  if (url.pathname.includes('/donation-sources') || url.pathname.endsWith('/binding-candidates'))
    expected.set('key_state', 'all');
  if (url.pathname.endsWith('/binding-candidates')) expected.set('include_bound', 'true');
  expected.set('page', page!);
  expected.set('page_size', pageSize!);
  expect(url.search).toBe(`?${expected.toString()}`);
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

function SessionBackedManagement({ frame }: { frame: 'admin' | 'steward' }) {
  const admin = useAdminSession(frame === 'admin');
  const user = useUserSession(frame === 'steward');
  const accountId =
    frame === 'admin'
      ? admin.data
        ? `admin:${admin.data.admin.username}`
        : undefined
      : user.data?.user.id;
  return accountId ? (
    <CharityManagement
      frame={frame}
      accountId={accountId}
      trainee={frame === 'steward' && user.data?.user.effective_level === 5}
    />
  ) : null;
}

describe('independent donation and model grants', () => {
  it('opens tagged-source maintenance without granting non-mainstream model access', async () => {
    const policy = { ...defaultStewardPermissions(5), donation_scope: 'enhanced' as const };
    const reads: URL[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'https://example.test');
        reads.push(url);
        if (url.pathname === '/api/steward/donation-sources')
          return jsonResponse(numberedPage([], url));
        throw new Error('Unexpected read: ' + url.pathname);
      }),
    );
    await renderWithProviders(
      <CharityManagement frame="steward" accountId="1" actorLevel={5} permissions={policy} />,
      { station: 'user', role: 'level5', route: '/steward?charity_section=sources' },
    );
    await waitFor(() =>
      expect(reads.some((url) => url.pathname.endsWith('/donation-sources'))).toBe(true),
    );
    expect(reads[0]!.searchParams.get('scope')).toBe('all');
    expect(reads[0]!.searchParams.get('key_state')).toBe('all');
    expect(screen.queryByText(/You do not have this permission yet/)).toBeNull();
  });

  it('keeps donation browsing closed at no scope even when action switches are enabled', async () => {
    const policy = { ...defaultStewardPermissions(6), donation_scope: 'none' as const };
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    await renderWithProviders(
      <CharityManagement frame="steward" accountId="1" actorLevel={5} permissions={policy} />,
      { station: 'user', role: 'level5', route: '/steward?charity_section=donations' },
    );
    expect(screen.getByText(/You do not have this permission yet/)).toBeVisible();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('offers model creation without first requiring model list access', async () => {
    const policy = defaultStewardPermissions(1);
    policy.permissions['charity.create'] = true;
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    await renderWithProviders(
      <CharityManagement frame="steward" accountId="1" actorLevel={5} permissions={policy} />,
      { station: 'user', role: 'level5', route: '/steward?charity_section=models' },
    );
    expect(screen.getByRole('button', { name: 'Create charity model' })).toBeEnabled();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

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
  charity_state: 'available',
  rate_limited_until: null,
  key_limits_revision: '1',
  max_concurrency: 2,
  max_rpm: 30,
  limits: { price: null, calls: null, tokens: null },
  usage: {
    price_used: '0',
    price_inflight: '0',
    calls_used: '0',
    calls_inflight: '0',
    tokens_used: '0',
    tokens_inflight: '0',
  },
  token_reserve: 32,
  authorized_expires_at: null,
  expires_at: null,
  failure_disable_threshold: '10',
  smart_backoff: null,
  streak: { generation: '1', count: '0', failure_disabled: false },
  ended_reason: null,
  safe_note: 'safe note',
  ...overrides,
});

function approvedDonation(key = managedKey()): AdminDonation {
  return {
    id: '1',
    status: 'approved',
    revision: '1',
    handling: {
      state: 'pending',
      revision: '1',
      processed_at: null,
      processed_by_role: null,
      closed_at: null,
      closed_reason: null,
    },
    description: 'Donation description',
    review_result: { decision: 'approve', reason: 'accepted', reviewed_at: 10 },
    keys: [key],
    owner: { user_id: '7', discord_id: null, display_name: 'Donor' },
    reviewer: { user_id: '9', role: 'admin' },
    created_at: 1,
    updated_at: 10,
  };
}

function donationSummary(donation: AdminDonation) {
  const stateCounts = {
    available: '0',
    rate_limited: '0',
    pending: '0',
    disabled: '0',
    suspended: '0',
    exhausted: '0',
    expired: '0',
    ended: '0',
  };
  for (const key of donation.keys)
    stateCounts[key.charity_state] = String(Number(stateCounts[key.charity_state]) + 1);
  const source = donation.keys[0]?.safe_source;
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
    owner: donation.owner,
  };
}

function keySummary(donation: AdminDonation, key: ManagedDonationKey) {
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

function bindingCandidate(
  donation: AdminDonation,
  key: ManagedDonationKey,
  upstreamModelId: string,
) {
  return {
    donation_id: donation.id,
    donation_key_id: key.id,
    upstream_model_id: upstreamModelId,
    source: {
      connector_type: key.safe_source.connector_type,
      canonical_base_url: key.safe_source.base_url,
      display_head: key.display_head,
      display_tail: key.display_tail,
    },
    source_types: ['automatic'],
    current_binding_id: null,
    other_binding_count: '0',
    other_bindings: [],
  };
}

function pendingDonation(): AdminDonation {
  return {
    ...approvedDonation(managedKey({ charity_state: 'pending' })),
    status: 'pending',
    review_result: null,
    reviewer: null,
    updated_at: 1,
  };
}

function installDonationFetch(initial: AdminDonation) {
  let current = initial;
  const keyBodies: Record<string, unknown>[] = [];
  const reviewBodies: Record<string, unknown>[] = [];
  const idempotencyKeys: string[] = [];
  const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(String(input), 'https://example.test');
    const path = url.pathname.replace('/api/steward/', '/admin/api/');
    const method = init?.method ?? 'GET';
    if (method === 'GET' && path === '/api/session') return jsonResponse(stewardSession);
    if (method === 'GET' && path === '/admin/api/session') return jsonResponse(adminSession);
    if (method === 'GET' && path === '/admin/api/time-context')
      return jsonResponse(siteTimeContext);
    if (method === 'GET' && (path === '/admin/api/time-zones' || path === '/api/time-zones'))
      return jsonResponse({
        version: 'go1.26.6-zoneinfo',
        zones: ['America/Indianapolis', 'UTC'],
      });
    if (method === 'GET' && path === '/admin/api/donations') {
      expectNumberedPageQuery(url);
      return jsonResponse(numberedPage([donationSummary(current)], url));
    }
    if (method === 'GET' && path === '/admin/api/donations/1') return jsonResponse(current);
    if (method === 'GET' && path === '/admin/api/donations/1/keys') {
      expectNumberedPageQuery(url);
      return jsonResponse(
        numberedPage(
          current.keys.map((key) => keySummary(current, key)),
          url,
        ),
      );
    }
    if (method === 'PATCH' && path === '/admin/api/donations/1/keys/11') {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      keyBodies.push(body);
      idempotencyKeys.push(new Headers(init?.headers).get('Idempotency-Key') ?? '');
      current = { ...current, revision: String(Number(current.revision) + 1), updated_at: 11 };
      return jsonResponse(current);
    }
    if (method === 'POST' && path === '/admin/api/donations/1/review') {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      reviewBodies.push(body);
      idempotencyKeys.push(new Headers(init?.headers).get('Idempotency-Key') ?? '');
      const approved = body.decision === 'approve';
      current = {
        ...current,
        status: approved ? 'approved' : 'rejected',
        revision: String(Number(current.revision) + 1),
        review_result: {
          decision: approved
            ? 'approve'
            : body.decision === 'force_reject'
              ? 'force_reject'
              : 'reject',
          reason: String(body.reason),
          reviewed_at: 11,
        },
        reviewer: { user_id: '9', role: 'admin' },
        can_force_reject: false,
        keys: current.keys.map((key) => ({
          ...key,
          charity_state: approved ? 'available' : key.charity_state,
          expires_at: approved
            ? ((body.key_settings as { expires_at: number | null }[])[0]?.expires_at ?? null)
            : key.expires_at,
        })),
        updated_at: 11,
      };
      return jsonResponse(current);
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { keyBodies, reviewBodies, idempotencyKeys };
}

const operationKeyPattern = /^[A-Za-z0-9_-]{22,128}$/;

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

const charityModel = (start: number, end: number): CharityModel => ({
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },
  effective_discount: { source: 'fixed', percent: 10, next_change_at: null },
  server_now: 1800000000,

  model_types: ['chat_completions', 'embeddings'],
  route_strategy: 'expiry_weighted',
  affinity_ttl_seconds: 300,
  id: '1',
  provider: 'provider',
  model: 'model',
  full_name: '[公益]provider/model',
  enabled: true,
  allowed_levels: [1, 2, 3, 4, 5],
  public_description: '',
  token_reserve_credits: null,
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: true, percent: 10, start_at: start, end_at: end },
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
});

const siteDateTime = (epoch: number) => `${new Date(epoch * 1_000).toISOString().slice(0, 19)}.000`;

describe('CharityManagement corrective controls', () => {
  it.each(['template undo', 'model reference'] as const)(
    'fills complete prices with %s and saves explicit request reserve clearing',
    async (flow) => {
      const token = {
        recurring_discount: {
          enabled: true,
          time_zone: 'Asia/Tokyo',
          default_percent: 50,
          periods: [{ weekdays: [1], start_time: '22:00', end_time: '02:00', percent: 0 }],
        },
        pricing: {
          mode: 'per_token' as const,
          user_prices: {
            uncached_input: '20',
            cache_write_input: '24',
            cache_read_input: '2',
            output: '60',
          },
          donor_rewards: {
            uncached_input: '10',
            cache_write_input: '12',
            cache_read_input: '1',
            output: '30',
          },
          long_context: {
            input_threshold: '100000',
            user_prices: {
              uncached_input: '40',
              cache_write_input: '48',
              cache_read_input: '4',
              output: '120',
            },
          },
          context_penalty: { input_threshold: '200000', fine: '500' },
        },
        token_reserve_credits: '25.125',
        discount: { enabled: true, percent: 80, start_at: null, end_at: null },
      };
      const request = {
        recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 0, periods: [] },
        pricing: { mode: 'per_request' as const, user_price: '3.5', donor_reward: '1.75' },
        token_reserve_credits: null,
        discount: { enabled: false, percent: 0, start_at: null, end_at: null },
      };
      const templates = [
        {
          ...token,
          id: '1',
          name: 'Token template',
          description: '',
          enabled: true,
          revision: '1',
          created_at: 1,
          updated_at: 1,
        },
        {
          ...request,
          id: '2',
          name: 'Request template',
          description: '',
          enabled: true,
          revision: '1',
          created_at: 1,
          updated_at: 1,
        },
      ];
      let current = {
        ...charityModel(10, 20),
        enabled: false,
        route_strategy: 'ordered' as const,
        token_reserve_credits: '7' as string | null,
      };
      const reference = {
        ...charityModel(10, 20),
        ...token,
        id: '2',
        provider: 'Other',
        model: 'reference',
        full_name: '[公益]Other/reference',
        enabled: true,
      };
      const writes: { path: string; body: Record<string, unknown> }[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async (input, init) => {
          const url = new URL(String(input), 'https://example.test');
          const method = init?.method ?? 'GET';
          if (url.pathname === '/admin/api/session') return jsonResponse(adminSession);
          if (url.pathname.endsWith('/time-context')) return jsonResponse(siteTimeContext);
          if (url.pathname.endsWith('/time-zones'))
            return jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] });
          if (url.pathname === '/admin/api/charity-price-templates')
            return jsonResponse(numberedPage(templates, url));
          if (url.pathname === '/admin/api/charity-models')
            return jsonResponse(numberedPage([current, reference], url));
          if (url.pathname === '/admin/api/charity-models/1') {
            if (method === 'PATCH') {
              const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
              writes.push({ path: url.pathname, body });
              current = { ...current, ...body, revision: String(Number(current.revision) + 1) };
            }
            return jsonResponse(current);
          }
          if (url.pathname.endsWith('/bindings'))
            return jsonResponse({ bindings: [], binding_revision: '0' });
          if (url.pathname.endsWith('/donation-sources'))
            return jsonResponse(numberedPage([], url));
          throw new Error(`Unexpected price workspace request: ${method} ${url.pathname}`);
        }),
      );
      const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
        station: 'admin',
        route: '/charity?charity_section=models&charity_model=1',
      });
      const heading = await screen.findByRole('heading', { name: '[公益]provider/model' });
      const editor = within(heading.closest('.card') as HTMLElement);
      await view.user.click(editor.getByRole('button', { name: 'Choose a template or reference' }));
      await view.user.click(editor.getByText('Price templates', { selector: 'strong' }));
      if (flow === 'template undo') {
        const tokenRow = await editor.findByRole('row', { name: /Token template/ });
        await view.user.click(within(tokenRow).getByRole('button', { name: 'View full prices' }));
        await view.user.click(editor.getByRole('button', { name: 'Fill all prices' }));
        expect(editor.getByRole('combobox', { name: 'Pricing mode' })).toHaveValue('per_token');
        expect(editor.getByLabelText('Output user price')).toHaveValue('60');
        expect(editor.getByLabelText('Output donor reward')).toHaveValue('30');
        expect(editor.getByLabelText('Usage reserve (credits)')).toHaveValue('25.125');
        expect(editor.getByLabelText('Provider')).toHaveValue('provider');
        expect(editor.getByRole('checkbox', { name: 'Enable charity model' })).not.toBeChecked();
        expect(writes).toHaveLength(0);
        await view.user.click(
          editor.getByRole('button', { name: 'Return to current price draft' }),
        );
        expect(
          editor.getByRole('button', { name: 'Choose a template or reference' }),
        ).toHaveFocus();
        expect(editor.getByLabelText('Output user price')).toHaveValue('60');
        await view.user.click(
          editor.getByRole('button', { name: 'Choose a template or reference' }),
        );
        expect(editor.getByRole('heading', { name: /Token template · Revision 1/ })).toBeVisible();
        await view.user.click(editor.getByRole('button', { name: 'Undo the last price fill' }));
        expect(editor.getByRole('combobox', { name: 'Pricing mode' })).toHaveFocus();
        expect(editor.getByLabelText('Request user price')).toHaveValue('1');
        expect(editor.getByLabelText('Usage reserve (credits)')).toHaveValue('7');
        await view.user.click(editor.getByRole('button', { name: 'Fill all prices' }));
      }
      await view.user.click(
        within(await editor.findByRole('row', { name: /Request template/ })).getByRole('button', {
          name: 'View full prices',
        }),
      );
      await view.user.click(editor.getByRole('button', { name: 'Fill all prices' }));
      await view.user.click(editor.getByRole('button', { name: /^Save model / }));
      await waitFor(() => expect(writes).toHaveLength(1));
      expect(writes[0]).toMatchObject({
        path: '/admin/api/charity-models/1',
        body: {
          ...request,
          expected_revision: '1',
        },
      });
      if (flow === 'template undo') return;
      await view.user.click(
        editor.getByText("Reference an existing model's prices", { selector: 'strong' }),
      );
      const referenceRow = await editor.findByRole('row', { name: /\[公益\]Other\/reference/ });
      await view.user.click(within(referenceRow).getByRole('button', { name: 'View full prices' }));
      await view.user.click(editor.getByRole('button', { name: 'Fill these prices' }));
      await view.user.click(editor.getByRole('button', { name: /^Save model / }));
      await waitFor(() => expect(writes).toHaveLength(2));
      expect(writes[1]).toMatchObject({
        path: '/admin/api/charity-models/1',
        body: {
          ...token,
          expected_revision: '2',
        },
      });
    },
  );

  it.each(['admin', 'steward'] as const)(
    'keeps a separate new model draft and defaults balanced routing for %s',
    async (frame) => {
      const writes: Record<string, unknown>[] = [];
      let created: CharityModel | null = null;
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async (input, init) => {
          const url = new URL(String(input), 'https://example.test');
          const path = url.pathname.replace('/api/steward/', '/admin/api/');
          const method = init?.method ?? 'GET';
          if (method === 'GET' && path === '/admin/api/session') return jsonResponse(adminSession);
          if (method === 'GET' && path === '/api/session') return jsonResponse(stewardSession);
          if (method === 'GET' && path.endsWith('/time-context'))
            return jsonResponse(siteTimeContext);
          if (method === 'GET' && path.endsWith('/time-zones'))
            return jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] });
          if (method === 'GET' && /\/(donations|charity-models|donation-sources)$/.test(path))
            return jsonResponse(
              numberedPage(path.endsWith('/charity-models') && created ? [created] : [], url),
            );
          if (method === 'GET' && path === '/admin/api/charity-models/1')
            return jsonResponse(created);
          if (method === 'GET' && path.endsWith('/bindings'))
            return jsonResponse({ bindings: [], binding_revision: '0' });
          if (method === 'POST' && path === '/admin/api/charity-models') {
            const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
            writes.push(body);
            created = {
              ...charityModel(10, 20),
              ...body,
              full_name: '[公益]saved-prefix/saved-model',
            };
            return jsonResponse(created);
          }
          throw new Error(`Unexpected request: ${method} ${path}`);
        }),
      );
      const view = await renderWithProviders(<SessionBackedManagement frame={frame} />, {
        station: frame === 'admin' ? 'admin' : 'user',
        role: frame === 'admin' ? 'admin' : 'user',
      });
      await view.user.click(await screen.findByRole('tab', { name: /Charity models and/ }));
      expect(screen.queryByLabelText('Provider')).not.toBeInTheDocument();
      await view.user.click(screen.getByRole('button', { name: 'Create charity model' }));
      const strategy = screen.getByRole('combobox', { name: /Routing strategy/ });
      expect(strategy).toHaveValue('cache_balanced');
      await view.user.type(screen.getByLabelText('Provider'), 'saved-prefix');
      await view.user.type(screen.getByLabelText('Model'), 'saved-model');
      await view.user.selectOptions(strategy, 'ordered');
      expect(writes).toHaveLength(0);
      expect(screen.getByLabelText('Provider')).toHaveValue('saved-prefix');
      expect(screen.getByLabelText('Model')).toHaveValue('saved-model');
      expect(strategy).toHaveValue('ordered');
      await view.user.selectOptions(strategy, 'cache_balanced');
      await view.user.click(screen.getByRole('button', { name: 'Add charity model' }));
      await waitFor(() => expect(writes).toHaveLength(1));
      expect(writes[0]).toMatchObject({
        provider: 'saved-prefix',
        model: 'saved-model',
        route_strategy: 'cache_balanced',
      });
      await screen.findByRole('heading', { name: '[公益]saved-prefix/saved-model' });
      expect(screen.queryByRole('button', { name: 'Add charity model' })).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Save model #1' })).toBeVisible();
    },
  );

  it.each([
    [false, 'ai-sdk-gateway-v3', 'ai-sdk-gateway-v3'],
    [false, 'ai-sdk-gateway-v4', 'ai-sdk-gateway-v4'],
    [false, 'ai-sdk-gateway-v3', 'ai-sdk-gateway-v4'],
    [true, 'ai-sdk-gateway-v4', 'openai-compatible'],
  ] as const)(
    'offers cache defaults only for an entirely Gateway family (mixed=%s, %s / %s)',
    async (mixed, firstConnector, secondConnector) => {
      const model = { ...charityModel(10, 20), binding_count: '2' };
      const binding = (id: string, connector: string) => ({
        id,
        ord: Number(id) - 1,
        donation_id: id,
        donation_key_id: id,
        upstream_model_id: `model-${id}`,
        source_types: ['automatic'],
        source: {
          connector_type: connector,
          canonical_base_url: 'https://gateway.example/v1',
          display_head: 'head',
          display_tail: 'tail',
        },
      });
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async (input) => {
          const url = new URL(String(input), 'https://example.test');
          if (url.pathname === '/admin/api/session') return jsonResponse(adminSession);
          if (url.pathname === '/admin/api/time-context') return jsonResponse(siteTimeContext);
          if (url.pathname === '/admin/api/time-zones')
            return jsonResponse({ version: 'go1.26.6-zoneinfo', zones: ['UTC'] });
          if (url.pathname === '/admin/api/charity-models')
            return jsonResponse(numberedPage([model], url));
          if (url.pathname === '/admin/api/charity-models/1') return jsonResponse(model);
          if (url.pathname === '/admin/api/charity-models/1/bindings')
            return jsonResponse({
              bindings: [binding('1', firstConnector), binding('2', secondConnector)],
              binding_revision: '0',
            });
          if (url.pathname.endsWith('/request-adaptation'))
            return jsonResponse({
              revision: '1',
              forward_headers: { mode: 'replace', values: [] },
              fixed_headers: { mode: 'replace', values: {} },
              body_defaults: { mode: 'replace', values: {} },
              body_forced: { mode: 'replace', values: {} },
              native_extension_paths: { mode: 'replace', values: [] },
            });
          if (
            url.pathname === '/admin/api/donations' ||
            url.pathname === '/admin/api/donation-sources' ||
            url.pathname.endsWith('/binding-candidates')
          )
            return jsonResponse(numberedPage([], url));
          throw new Error(`Unexpected fixture request: ${url.pathname}`);
        }),
      );
      const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
        station: 'admin',
        role: 'admin',
      });
      await view.user.click(
        await screen.findByRole('tab', { name: 'Charity models and bindings' }),
      );
      await view.user.click(await screen.findByRole('button', { name: 'Manage' }));
      await screen.findByRole('heading', { name: 'Service connection order' });
      expect(
        screen.queryByRole('button', { name: 'Save request adaptation' }),
      ).not.toBeInTheDocument();
      await view.user.click(screen.getByText('Request adaptation', { selector: 'strong' }));
      await screen.findByRole('button', { name: 'Save request adaptation' });
      if (mixed) expect(screen.queryByLabelText('Automatic cache default')).not.toBeInTheDocument();
      else expect(await screen.findByLabelText('Automatic cache default')).toHaveValue('off');
    },
  );
  it('explains unavailable historical key IDs instead of linking all logs', async () => {
    installDonationFetch(approvedDonation(managedKey({ endpoint_key_id: null })));
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });
    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    expect(
      await screen.findByText(
        'The physical key ID is no longer available, so its request logs cannot be linked.',
      ),
    ).toBeVisible();
    expect(
      screen.queryByRole('link', { name: "View this key's request logs" }),
    ).not.toBeInTheDocument();
  });
  it.each(['admin', 'steward'] as const)(
    'links %s donation details to the exact physical key',
    async (frame) => {
      installDonationFetch(approvedDonation());
      const view = await renderWithProviders(<SessionBackedManagement frame={frame} />, {
        station: frame === 'admin' ? 'admin' : 'user',
        role: frame === 'admin' ? 'admin' : 'user',
      });
      await view.user.click(await screen.findByRole('button', { name: 'Review' }));
      const link = await screen.findByRole('link', { name: "View this key's request logs" });
      expect(link).toHaveAttribute(
        'href',
        frame === 'admin' ? '/logs?endpoint_key_id=21' : '/steward?tab=logs&endpoint_key_id=21',
      );
    },
  );
  it('uses the shared charity navigation for a level-5 steward', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      if ((init?.method ?? 'GET') !== 'GET') throw new Error('Unexpected write');
      if (url.pathname === '/api/session')
        return jsonResponse({
          ...stewardSession,
          user: { ...stewardSession.user, effective_level: 5 },
        });
      if (url.pathname === '/api/steward/charity-models')
        return jsonResponse(numberedPage([{ ...charityModel(10, 20), is_mainstream: true }], url));
      if (url.pathname === '/api/steward/donations') return jsonResponse(numberedPage([], url));
      throw new Error(`Unexpected trainee read: ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<SessionBackedManagement frame="steward" />, {
      station: 'user',
      role: 'user',
    });
    await view.user.click(await screen.findByRole('tab', { name: /Charity models and/ }));
    expect(await screen.findByText('[公益]provider/model')).toBeVisible();
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Donation review queue',
      'Charity models and sources',
      'Browse by source',
    ]);
    expect(
      fetchMock.mock.calls.every(([input]) => !String(input).includes('/donation-sources')),
    ).toBe(true);
  });

  it('keeps selections across two donation keys and paginated model candidates and submits the full set', async () => {
    const first = {
      ...approvedDonation(managedKey({ safe_note: 'First shared key\nReviewed limits' })),
      description: 'First donation instructions\nA second line of guidance',
    };
    const second = {
      ...approvedDonation(
        managedKey({ id: '12', endpoint_key_id: '22', safe_note: 'Second shared key' }),
      ),
      id: '2',
      description: 'Second donation instructions',
    };
    const model = charityModel(10, 20);
    const bodies: Record<string, unknown>[] = [];
    let bindingRevision = '0';
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://example.test');
        const method = init?.method ?? 'GET';
        if (method === 'GET' && url.pathname === '/admin/api/session')
          return jsonResponse(adminSession);
        if (method === 'GET' && url.pathname === '/admin/api/time-zones')
          return jsonResponse({
            version: 'go1.26.6-zoneinfo',
            zones: ['America/Indianapolis', 'UTC'],
          });
        if (method === 'GET' && url.pathname === '/admin/api/donations') {
          expectNumberedPageQuery(url);
          return jsonResponse(numberedPage([donationSummary(first), donationSummary(second)], url));
        }
        if (url.pathname === '/admin/api/donations/1') return jsonResponse(first);
        if (url.pathname === '/admin/api/donations/2') return jsonResponse(second);
        if (url.pathname === '/admin/api/charity-models') {
          expectNumberedPageQuery(url);
          return jsonResponse(numberedPage([model], url));
        }
        if (url.pathname === '/admin/api/charity-models/1') return jsonResponse(model);
        if (url.pathname.endsWith('/bindings')) {
          expect(url.search).toBe('');
          return jsonResponse({ bindings: [], binding_revision: bindingRevision });
        }
        if (url.pathname === '/admin/api/donation-sources') {
          expectNumberedPageQuery(url, { scope: 'all' });
          return jsonResponse(
            numberedPage(
              [
                {
                  source_key: `dsg_${'A'.repeat(43)}`,
                  safe_source: first.keys[0].safe_source,
                  donation_count: '2',
                  key_count: '2',
                  usable_key_count: '2',
                  pending_donation_count: '0',
                },
              ],
              url,
            ),
          );
        }
        if (url.pathname === `/admin/api/donation-sources/dsg_${'A'.repeat(43)}/keys`) {
          expectNumberedPageQuery(url, { scope: 'all' });
          return jsonResponse(
            numberedPage(
              [keySummary(first, first.keys[0]), keySummary(second, second.keys[0])],
              url,
            ),
          );
        }
        if (url.pathname.endsWith('/binding-candidates')) {
          const donation = url.searchParams.get('donation_id') === '2' ? second : first;
          const key = donation.keys.find(
            (entry) => entry.id === url.searchParams.get('donation_key_id'),
          );
          expect(key).toBeDefined();
          expectNumberedPageQuery(url, {
            donation_id: donation.id,
            donation_key_id: key!.id,
          });
          const models =
            donation.id === '1'
              ? [
                  bindingCandidate(donation, key!, 'model-one'),
                  ...Array.from({ length: 19 }, (_, index) =>
                    bindingCandidate(donation, key!, `filler-${index + 1}`),
                  ),
                  bindingCandidate(donation, key!, 'model-two'),
                ]
              : [bindingCandidate(donation, key!, 'model-one')];
          return jsonResponse(numberedPage(models, url));
        }
        if (method === 'POST' && url.pathname.endsWith('/bindings/batch-receipt')) {
          bodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
          bindingRevision = '1';
          return jsonResponse({
            model_id: '1',
            binding_revision: bindingRevision,
            added_count: String((bodies.at(-1)!.selections as unknown[]).length),
            binding_count: String((bodies.at(-1)!.selections as unknown[]).length),
          });
        }
        throw new Error(`Unexpected request: ${method} ${url.pathname}`);
      }),
    );
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });
    await view.user.click(await screen.findByRole('tab', { name: 'Charity models and bindings' }));
    await view.user.click(await screen.findByRole('button', { name: 'Manage' }));
    const pickerNode = await waitFor(() => {
      const node = view.container.querySelector('.ops-binding-picker');
      expect(node).not.toBeNull();
      return node as HTMLElement;
    });
    const picker = within(pickerNode);
    await view.user.click(
      await picker.findByRole('button', { name: /https:\/\/example\.test\/v1/ }),
    );
    await view.user.click(await picker.findByRole('button', { name: /First shared key/ }));
    await view.user.click(await picker.findByRole('checkbox', { name: /model-one/ }));
    const candidatePagination = picker.getAllByRole('navigation', { name: 'Pagination' })[0];
    await view.user.click(within(candidatePagination).getByRole('button', { name: 'Next' }));
    await view.user.click(await picker.findByRole('checkbox', { name: /model-two/ }));
    await view.user.click(picker.getByRole('button', { name: /Choose key/ }));
    await view.user.click(await picker.findByRole('button', { name: /Second shared key/ }));
    await view.user.click(await picker.findByRole('checkbox', { name: /model-one/ }));
    expect(picker.getByRole('heading', { name: '3 service connection(s) selected' })).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Add 3 connections' }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({
      expected_binding_revision: '0',
      selections: [
        { donation_key_id: '11', upstream_model_id: 'model-one' },
        { donation_key_id: '11', upstream_model_id: 'model-two' },
        { donation_key_id: '12', upstream_model_id: 'model-one' },
      ],
    });
  });
  it('omits enabled when reset is the only switch-related operator action', async () => {
    const fixture = approvedDonation(
      managedKey({
        charity_state: 'disabled',
        failure_disable_threshold: '10',
        smart_backoff: null,
        streak: { generation: '2', count: '10', failure_disabled: true },
      }),
    );
    const requests = installDonationFetch(fixture);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Donation review' });
    expect(screen.getByLabelText('Charity switch change')).toHaveValue('');
    await view.user.click(screen.getByRole('checkbox', { name: 'Reset failure streak' }));
    await view.user.click(screen.getByRole('button', { name: 'Save key limits' }));

    await waitFor(() => expect(requests.keyBodies).toHaveLength(1));
    expect(requests.keyBodies[0]).toEqual({
      expected_revision: '1',
      price_limit: null,
      input_tokens_limit: null,
      output_tokens_limit: null,
      input_token_reserve: null,
      output_token_reserve: null,
      calls_limit: null,
      tokens_limit: null,
      token_reserve: 32,
      safe_note: 'safe note',
      expires_at: null,
      reset_failure_streak: true,
    });
    expect(requests.keyBodies[0]).not.toHaveProperty('enabled');
    expect(requests.idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
  });

  it('sends enabled after an explicit operator switch choice', async () => {
    const requests = installDonationFetch(approvedDonation());
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Donation review' });
    await view.user.selectOptions(screen.getByLabelText('Charity switch change'), 'false');
    await view.user.click(screen.getByRole('button', { name: 'Save key limits' }));

    await waitFor(() => expect(requests.keyBodies).toHaveLength(1));
    expect(requests.keyBodies[0]).toEqual({
      expected_revision: '1',
      enabled: false,
      price_limit: null,
      input_tokens_limit: null,
      output_tokens_limit: null,
      input_token_reserve: null,
      output_token_reserve: null,
      calls_limit: null,
      tokens_limit: null,
      token_reserve: 32,
      safe_note: 'safe note',
      expires_at: null,
    });
    expect(requests.idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
  });

  it.each(['approved', 'pending'] as const)(
    'confirms force rejection once for a %s parent with approved uses',
    async (status) => {
      const requests = installDonationFetch({
        ...(status === 'pending' ? pendingDonation() : approvedDonation()),
        status,
        first_approval_origin: 'auto',
        can_force_reject: true,
        force_reject_unavailable_reason: null,
      });
      const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
        station: 'admin',
        role: 'admin',
      });
      await view.user.click(await screen.findByRole('button', { name: 'Review' }));
      const action = await screen.findByRole('button', {
        name: 'Force reject donation',
      });
      expect(screen.queryByRole('option', { name: 'Approve donation' })).not.toBeInTheDocument();
      expect(action).toBeDisabled();
      await view.user.type(
        screen.getByLabelText('Reason'),
        'Review this source before future donations',
      );
      await view.user.click(action);
      expect(requests.reviewBodies).toHaveLength(0);
      const dialog = await screen.findByRole('alertdialog');
      expect(within(dialog).getByText(/Dispatched calls settle normally/)).toBeVisible();
      expect(within(dialog).queryByRole('checkbox')).not.toBeInTheDocument();
      await view.user.click(within(dialog).getByRole('button', { name: 'Force reject donation' }));
      await waitFor(() => expect(requests.reviewBodies).toHaveLength(1));
      expect(requests.reviewBodies[0]).toEqual({
        decision: 'force_reject',
        expected_revision: '1',
        reason: 'Review this source before future donations',
      });
      expect(requests.idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
    },
  );

  it('includes the current member review revision only when that member is explicitly enabled', async () => {
    const donation = pendingDonation();
    donation.keys[0].review = { required: true, revision: '7', material_available: true };
    const requests = installDonationFetch(donation);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });
    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Review pending submission' });
    await view.user.type(screen.getByLabelText('Reason'), 'Reviewed the current member');
    await view.user.click(screen.getByRole('button', { name: 'Approve donation' }));
    await waitFor(() => expect(requests.reviewBodies).toHaveLength(1));
    expect(requests.reviewBodies[0].key_settings).toEqual([
      expect.objectContaining({
        donation_key_id: '11',
        enabled: true,
        expected_review_revision: '7',
      }),
    ]);
  });

  it('leaves missing review material disabled and preserves its requirement when approving other settings', async () => {
    const donation = pendingDonation();
    donation.keys[0].review = { required: true, revision: '7', material_available: false };
    const requests = installDonationFetch(donation);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });
    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Review pending submission' });
    await view.user.type(screen.getByLabelText('Reason'), 'Keep the affected member disabled');
    expect(screen.getByRole('button', { name: 'Approve donation' })).toBeDisabled();
    await view.user.click(screen.getByRole('checkbox', { name: 'Enable for charity' }));
    await view.user.click(screen.getByRole('button', { name: 'Approve donation' }));
    await waitFor(() => expect(requests.reviewBodies).toHaveLength(1));
    expect(requests.reviewBodies[0].key_settings).toEqual([
      expect.objectContaining({ enabled: false }),
    ]);
    expect(
      (requests.reviewBodies[0].key_settings as Record<string, unknown>[])[0],
    ).not.toHaveProperty('expected_review_revision');
  });

  it('submits an explicit per-key null expiry for a pending donation', async () => {
    const requests = installDonationFetch(pendingDonation());
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Review pending submission' });
    expect(screen.getByRole('checkbox', { name: 'No expiry' })).toBeChecked();
    expect(screen.getByLabelText('Effective expiry')).toBeDisabled();
    await view.user.type(screen.getByLabelText('Reason'), 'approved without expiry');
    await view.user.click(screen.getByRole('button', { name: 'Approve donation' }));

    await waitFor(() => expect(requests.reviewBodies).toHaveLength(1));
    expect(requests.reviewBodies[0]).toEqual({
      decision: 'approve',
      expected_revision: '1',
      reason: 'approved without expiry',
      key_settings: [
        {
          donation_key_id: '11',
          price_limit: null,
          input_tokens_limit: null,
          output_tokens_limit: null,
          input_token_reserve: null,
          output_token_reserve: null,
          calls_limit: null,
          tokens_limit: null,
          token_reserve: 32,
          enabled: true,
          safe_note: 'safe note',
          expires_at: null,
        },
      ],
    });
    expect(requests.idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
  });

  it('renders site discount seconds and preserves untouched epochs', async () => {
    const start = 1_735_689_845;
    const end = 1_735_693_507;
    let current = charityModel(start, end);
    const patchBodies: Record<string, unknown>[] = [];
    const idempotencyKeys: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      const path = url.pathname;
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path === '/admin/api/session') return jsonResponse(adminSession);
      if (method === 'GET' && path === '/admin/api/time-context')
        return jsonResponse(siteTimeContext);
      if (method === 'GET' && path === '/admin/api/time-zones')
        return jsonResponse({
          version: 'go1.26.6-zoneinfo',
          zones: ['America/Indianapolis', 'UTC'],
        });
      if (method === 'GET' && path === '/admin/api/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([], url));
      }
      if (method === 'GET' && path === '/admin/api/charity-models') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([current], url));
      }
      if (method === 'GET' && path === '/admin/api/charity-models/1') return jsonResponse(current);
      if (method === 'GET' && path === '/admin/api/charity-models/1/bindings') {
        expect(url.search).toBe('');
        return jsonResponse({ bindings: [], binding_revision: '0' });
      }
      if (method === 'GET' && path === '/admin/api/donation-sources') {
        expectNumberedPageQuery(url, { scope: 'all' });
        return jsonResponse(numberedPage([], url));
      }
      if (method === 'GET' && path === '/admin/api/charity-models/1/binding-candidates') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([], url));
      }
      if (method === 'PATCH' && path === '/admin/api/charity-models/1') {
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        patchBodies.push(body);
        idempotencyKeys.push(new Headers(init?.headers).get('Idempotency-Key') ?? '');
        current = {
          ...current,
          revision: '2',
          discount: (body.discount as CharityModel['discount'] | undefined) ?? current.discount,
          updated_at: 2,
        };
        return jsonResponse(current);
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('tab', { name: 'Charity models and bindings' }));
    await view.user.click(await screen.findByRole('button', { name: 'Manage' }));
    const heading = await screen.findByRole('heading', { name: '[公益]provider/model' });
    const card = heading.closest('.card');
    if (!(card instanceof HTMLElement)) throw new Error('Expected model editor card.');
    const editor = within(card);
    const discountPercent = editor.getByLabelText(/Discount percentage/);
    const discountDetails = discountPercent.closest('details');
    fireEvent.change(discountPercent, { target: { value: '101' } });
    expect(discountDetails).toHaveAttribute('open');
    fireEvent.change(discountPercent, { target: { value: '10' } });
    expect(discountDetails).toHaveAttribute('open');
    await waitFor(() =>
      expect(editor.getByLabelText('Start (optional)')).toHaveValue(siteDateTime(start)),
    );
    expect(editor.getByLabelText('End (optional)')).toHaveValue(siteDateTime(end));
    await view.user.selectOptions(
      editor.getByRole('combobox', { name: /Routing strategy/ }),
      'cache_balanced',
    );
    fireEvent.change(editor.getByRole('spinbutton', { name: /Association duration/ }), {
      target: { value: '86400' },
    });

    await view.user.click(
      editor.getByText('Long context and excess-context penalty', { exact: true }),
    );
    await view.user.click(editor.getByRole('switch', { name: 'Enable long-context prices' }));
    fireEvent.change(editor.getByLabelText('Long-context threshold (input tokens)'), {
      target: { value: '100000' },
    });
    fireEvent.change(editor.getByLabelText('Long-context price per request (credits)'), {
      target: { value: '8' },
    });
    await view.user.click(editor.getByRole('switch', { name: 'Enable excess-context penalty' }));
    fireEvent.change(editor.getByLabelText('Penalty threshold (input tokens)'), {
      target: { value: '200000' },
    });
    fireEvent.change(editor.getByLabelText('One-time fine (credits)'), { target: { value: '50' } });
    await view.user.click(editor.getByText('Managed session headers', { exact: true }));
    await view.user.click(editor.getByRole('button', { name: 'Add managed header' }));
    expect(patchBodies).toHaveLength(0);
    fireEvent.change(editor.getByLabelText('Header 1 name'), { target: { value: 'X-Session-ID' } });
    expect(editor.getByLabelText('Idle expiry (seconds)')).toHaveValue(300);
    await view.user.selectOptions(editor.getByLabelText('Random value format'), 'hex_32');

    await view.user.click(editor.getByRole('button', { name: /^Save model / }));

    await waitFor(() => expect(patchBodies).toHaveLength(1));
    expect(patchBodies[0]).toEqual({
      session_headers: [{ name: 'X-Session-ID', idle_seconds: 300, format: 'hex_32' }],
      route_strategy: 'cache_balanced',
      affinity_ttl_seconds: 86400,
      expected_revision: '1',
      pricing: {
        mode: 'per_request',
        user_price: '1',
        donor_reward: '0',
        long_context: { input_threshold: '100000', user_price: '8' },
        context_penalty: { input_threshold: '200000', fine: '50' },
      },
    });
    expect(patchBodies[0]).not.toHaveProperty('pricing_mode');
    expect(patchBodies[0]).not.toHaveProperty('prices');
    expect(current.discount).toEqual({ enabled: true, percent: 10, start_at: start, end_at: end });
    expect(idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
  });

  it.each([
    {
      frame: 'admin' as const,
      locale: 'en' as const,
      station: 'admin' as const,
      testRole: 'admin' as const,
      prefix: '/admin/api',
      sessionPath: '/admin/api/session',
      session: adminSession,
      modelsTab: 'Charity models and bindings',
      reserveLabel: 'Usage reserve (credits)',
      reserveHelp:
        'Used before usage-based requests. Leave blank to use the caller key or site reserve.',
      pricingLabel: 'Pricing mode',
      perRequest: 'Per request',
      perToken: 'Per token',
      validation: /Reserve 0\.001/i,
    },
    {
      frame: 'steward' as const,
      locale: 'zh' as const,
      station: 'user' as const,
      testRole: 'user' as const,
      prefix: '/api/steward',
      sessionPath: '/api/session',
      session: stewardSession,
      modelsTab: '公益模型与来源',
      reserveLabel: '用量预留（积分）',
      reserveHelp: '用于按用量计费请求的预留。留空沿用调用密钥或站点设置。',
      pricingLabel: '计价模式',
      perRequest: '按次',
      perToken: '按 token',
      validation: /预留积分范围：0\.001/i,
    },
  ])(
    'edits the per-token reserve for the $frame station with exact text semantics',
    async (fixture) => {
      let current: CharityModel = {
        ...charityModel(10, 20),
        token_reserve_credits: '1.234',
        pricing: {
          mode: 'per_token',
          user_prices: {
            uncached_input: '0.001',
            cache_write_input: '0.002',
            cache_read_input: '0.003',
            output: '0.004',
          },
          donor_rewards: {
            uncached_input: '0.005',
            cache_write_input: '0.006',
            cache_read_input: '0.007',
            output: '0.008',
          },
        },
      };
      const patchBodies: Record<string, unknown>[] = [];
      const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://example.test');
        const path = url.pathname;
        const method = init?.method ?? 'GET';
        if (method === 'GET' && path === fixture.sessionPath) return jsonResponse(fixture.session);
        if (method === 'GET' && path === `${fixture.prefix}/time-context`)
          return jsonResponse(siteTimeContext);
        if (method === 'GET' && path === `${fixture.prefix}/time-zones`)
          return jsonResponse({
            version: 'go1.26.6-zoneinfo',
            zones: ['America/Indianapolis', 'UTC'],
          });
        if (method === 'GET' && path === `${fixture.prefix}/donations`) {
          expectNumberedPageQuery(url);
          return jsonResponse(numberedPage([], url));
        }
        if (method === 'GET' && path === `${fixture.prefix}/charity-models`) {
          expectNumberedPageQuery(url);
          return jsonResponse(numberedPage([current], url));
        }
        if (method === 'GET' && path === `${fixture.prefix}/charity-models/1`)
          return jsonResponse(current);
        if (method === 'GET' && path === `${fixture.prefix}/charity-models/1/bindings`) {
          expect(url.search).toBe('');
          return jsonResponse({ bindings: [], binding_revision: current.binding_revision });
        }
        if (method === 'GET' && path === `${fixture.prefix}/donation-sources`) {
          expectNumberedPageQuery(url, { scope: 'all' });
          return jsonResponse(numberedPage([], url));
        }
        if (method === 'GET' && path === `${fixture.prefix}/charity-models/1/binding-candidates`) {
          expectNumberedPageQuery(url);
          return jsonResponse(numberedPage([], url));
        }
        if (method === 'PATCH' && path === `${fixture.prefix}/charity-models/1`) {
          const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
          patchBodies.push(body);
          current = {
            ...current,
            revision: String(Number(current.revision) + 1),
            pricing: (body.pricing as CharityModel['pricing'] | undefined) ?? current.pricing,
            token_reserve_credits: Object.hasOwn(body, 'token_reserve_credits')
              ? (body.token_reserve_credits as string | null)
              : current.token_reserve_credits,
            updated_at: current.updated_at + 1,
          };
          return jsonResponse(current);
        }
        throw new Error(`Unexpected request: ${method} ${path}`);
      });
      vi.stubGlobal('fetch', fetchMock);
      const view = await renderWithProviders(<SessionBackedManagement frame={fixture.frame} />, {
        station: fixture.station,
        role: fixture.testRole,
        locale: fixture.locale,
      });

      await view.user.click(await screen.findByRole('tab', { name: fixture.modelsTab }));
      await view.user.click(await screen.findByRole('button', { name: /Manage|管理/ }));
      const heading = await screen.findByRole('heading', { name: '[公益]provider/model' });
      const card = heading.closest('.card');
      if (!(card instanceof HTMLElement)) throw new Error('Expected model editor card.');
      const editor = within(card);
      if (fixture.frame === 'steward') {
        expect(editor.queryByLabelText('关联有效时长（秒）')).not.toBeInTheDocument();
      }
      await view.user.click(
        editor.getByText(fixture.locale === 'zh' ? '额度与预留' : 'Quota and reserve', {
          selector: 'strong',
        }),
      );
      const reserve = editor.getByLabelText(fixture.reserveLabel);
      expect(reserve).toHaveValue('1.234');
      expect(editor.getByText(fixture.reserveHelp)).toBeVisible();

      fireEvent.change(reserve, { target: { value: '0' } });
      expect(editor.getByRole('button', { name: /Save model|保存模型/ })).toBeDisabled();
      await view.user.selectOptions(
        editor.getByRole('combobox', { name: fixture.pricingLabel }),
        'per_request',
      );
      expect(editor.getByLabelText(fixture.reserveLabel)).toHaveValue('0');
      expect(editor.getByRole('button', { name: /Save model|保存模型/ })).toBeDisabled();
      fireEvent.change(reserve, { target: { value: '2.345' } });
      expect(editor.getByRole('button', { name: /Save model|保存模型/ })).toBeEnabled();
      await view.user.click(editor.getByRole('button', { name: /Save model|保存模型/ }));
      await waitFor(() => expect(patchBodies).toHaveLength(1));
      expect(patchBodies[0]).toHaveProperty('token_reserve_credits', '2.345');
      if (fixture.frame === 'steward') {
        expect(patchBodies[0]).not.toHaveProperty('affinity_ttl_seconds');
      }
      expect(current.pricing).toMatchObject({ mode: 'per_request' });

      await view.user.selectOptions(
        editor.getByRole('combobox', { name: fixture.pricingLabel }),
        'per_token',
      );
      const reserveAgain = editor.getByLabelText(fixture.reserveLabel);
      expect(reserveAgain).toHaveValue('2.345');
      fireEvent.change(reserveAgain, { target: { value: '0' } });
      expect(editor.getByRole('button', { name: /Save model|保存模型/ })).toBeDisabled();
      expect(editor.getByText(fixture.validation)).toBeVisible();
      fireEvent.change(reserveAgain, { target: { value: '9000000000000.001' } });
      expect(editor.getByRole('button', { name: /Save model|保存模型/ })).toBeDisabled();
      fireEvent.change(reserveAgain, { target: { value: '0.001' } });
      await view.user.click(editor.getByRole('button', { name: /Save model|保存模型/ }));
      await waitFor(() => expect(patchBodies).toHaveLength(2));
      expect(patchBodies[1]).toHaveProperty('token_reserve_credits', '0.001');

      fireEvent.change(reserveAgain, { target: { value: '' } });
      await view.user.click(editor.getByRole('button', { name: /Save model|保存模型/ }));
      await waitFor(() => expect(patchBodies).toHaveLength(3));
      expect(patchBodies[2]).toHaveProperty('token_reserve_credits', null);
    },
  );

  it('creates a model with the selected levels and canonical public description', async () => {
    const createBodies: Record<string, unknown>[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      const method = init?.method ?? 'GET';
      if (method === 'GET' && url.pathname === '/admin/api/session')
        return jsonResponse(adminSession);
      if (method === 'GET' && url.pathname === '/admin/api/time-context')
        return jsonResponse(siteTimeContext);
      if (method === 'GET' && url.pathname === '/admin/api/time-zones')
        return jsonResponse({
          version: 'go1.26.6-zoneinfo',
          zones: ['America/Indianapolis', 'UTC'],
        });
      if (method === 'GET' && url.pathname === '/admin/api/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([], url));
      }
      if (method === 'GET' && url.pathname === '/admin/api/charity-models') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([], url));
      }
      if (method === 'POST' && url.pathname === '/admin/api/charity-models') {
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        createBodies.push(body);
        return jsonResponse({
          ...charityModel(10, 20),
          allowed_levels: body.allowed_levels,
          public_description: body.public_description,
        });
      }
      throw new Error(`Unexpected request: ${method} ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('tab', { name: 'Charity models and bindings' }));
    await view.user.click(screen.getByRole('button', { name: 'Create charity model' }));
    expect(screen.getByRole('checkbox', { name: 'Chat completions' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Responses' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Embeddings' })).not.toBeChecked();
    await view.user.click(screen.getByRole('checkbox', { name: 'Image generation' }));
    expect(screen.getByRole('checkbox', { name: 'L1' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'L5' })).toBeChecked();
    await view.user.click(screen.getByRole('button', { name: 'Clear all levels' }));
    expect(
      screen.getByText('No ordinary user can call this model (including level 6 users).'),
    ).toBeVisible();
    await view.user.click(screen.getByRole('checkbox', { name: 'L2' }));
    await view.user.click(screen.getByRole('checkbox', { name: 'L5' }));
    await view.user.type(screen.getByLabelText('Provider'), 'provider');
    await view.user.type(screen.getByLabelText('Model'), 'model');
    fireEvent.change(screen.getByLabelText('Public description (plain text, optional)'), {
      target: { value: 'First line\r\n<b>literal</b>\t😀' },
    });

    const price = screen.getByLabelText('Request user price');
    const reward = screen.getByLabelText('Request donor reward');
    fireEvent.change(price, { target: { value: '0.003' } });
    await view.user.click(screen.getByRole('button', { name: 'Fill rewards with half the price' }));
    expect(reward).toHaveValue('0.001');
    expect(createBodies).toHaveLength(0);
    fireEvent.change(price, { target: { value: '' } });
    await view.user.click(screen.getByRole('button', { name: 'Fill rewards with half the price' }));
    expect(price).toHaveFocus();
    expect(screen.getByText('Enter valid user prices first.')).toBeVisible();
    fireEvent.change(price, { target: { value: '1' } });
    await view.user.click(screen.getByRole('button', { name: 'Fill rewards with half the price' }));
    expect(reward).toHaveValue('0.5');

    const pricing = screen.getByRole('combobox', { name: 'Pricing mode' });
    await view.user.selectOptions(pricing, 'per_token');
    const hiddenReserve = screen.getByLabelText('Usage reserve (credits)');
    fireEvent.change(hiddenReserve, { target: { value: '0' } });
    expect(screen.getByRole('button', { name: 'Add charity model' })).toBeDisabled();
    await view.user.selectOptions(pricing, 'per_request');
    expect(screen.getByLabelText('Usage reserve (credits)')).toHaveValue('0');
    expect(screen.getByRole('button', { name: 'Add charity model' })).toBeDisabled();
    fireEvent.change(hiddenReserve, { target: { value: '' } });
    expect(screen.getByRole('button', { name: 'Add charity model' })).toBeEnabled();
    await view.user.click(screen.getByRole('button', { name: 'Add charity model' }));
    await waitFor(() => expect(createBodies).toHaveLength(1));
    expect(createBodies[0]).toMatchObject({
      model_types: ['chat_completions', 'images_generations', 'responses'],
      allowed_levels: [2, 5],
      public_description: 'First line\n<b>literal</b>\t😀',
    });
    expect(createBodies[0]).toHaveProperty('token_reserve_credits', null);
  });

  it('retains a newer draft when a saved model response is slow', async () => {
    let current = charityModel(10, 20);
    const patchResponse = deferred<Response>();
    const patchBodies: Record<string, unknown>[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      const method = init?.method ?? 'GET';
      if (method === 'GET' && url.pathname === '/admin/api/session')
        return jsonResponse(adminSession);
      if (method === 'GET' && url.pathname === '/admin/api/time-context')
        return jsonResponse(siteTimeContext);
      if (method === 'GET' && url.pathname === '/admin/api/time-zones')
        return jsonResponse({
          version: 'go1.26.6-zoneinfo',
          zones: ['America/Indianapolis', 'UTC'],
        });
      if (method === 'GET' && url.pathname === '/admin/api/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([], url));
      }
      if (method === 'GET' && url.pathname === '/admin/api/charity-models') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([current], url));
      }
      if (method === 'GET' && url.pathname === '/admin/api/charity-models/1')
        return jsonResponse(current);
      if (method === 'GET' && url.pathname === '/admin/api/charity-models/1/bindings') {
        expect(url.search).toBe('');
        return jsonResponse({ bindings: [], binding_revision: current.binding_revision });
      }
      if (method === 'GET' && url.pathname === '/admin/api/donation-sources') {
        expectNumberedPageQuery(url, { scope: 'all' });
        return jsonResponse(numberedPage([], url));
      }
      if (method === 'GET' && url.pathname === '/admin/api/charity-models/1/binding-candidates') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([], url));
      }
      if (method === 'PATCH' && url.pathname === '/admin/api/charity-models/1') {
        patchBodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
        if (patchBodies.length > 1) {
          current = {
            ...current,
            revision: '3',
            public_description: String(patchBodies.at(-1)?.public_description),
          };
          return jsonResponse(current);
        }
        return patchResponse.promise;
      }
      throw new Error(`Unexpected request: ${method} ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('tab', { name: 'Charity models and bindings' }));
    await view.user.click(await screen.findByRole('button', { name: 'Manage' }));
    const heading = await screen.findByRole('heading', { name: '[公益]provider/model' });
    const card = heading.closest('.card');
    if (!(card instanceof HTMLElement)) throw new Error('Expected model editor card.');
    const editor = within(card);
    const textarea = editor.getByLabelText('Public description (plain text, optional)');
    await view.user.click(editor.getByRole('radio', { name: 'Always wait for the full reply' }));
    fireEvent.change(textarea, { target: { value: 'submitted' } });
    await view.user.click(editor.getByRole('button', { name: /^Save model / }));
    await waitFor(() => expect(patchBodies).toHaveLength(1));
    expect(patchBodies[0]).toHaveProperty('transport_rule', 'force_non_stream');
    fireEvent.change(textarea, { target: { value: 'new draft' } });

    current = { ...current, revision: '2', public_description: 'submitted', updated_at: 2 };
    patchResponse.resolve(jsonResponse(current));
    await waitFor(() => expect(editor.getByRole('button', { name: /^Save model / })).toBeEnabled());
    expect(textarea).toHaveValue('new draft');
    expect(patchBodies[0]).toMatchObject({
      public_description: 'submitted',
      expected_revision: '1',
    });
    await view.user.click(editor.getByRole('button', { name: /^Save model / }));
    await waitFor(() => expect(patchBodies).toHaveLength(2));
    expect(patchBodies[1]).toMatchObject({
      public_description: 'new draft',
      expected_revision: '2',
    });
  });

  it('retries a lost model save with its original revision and retains subsequent edits', async () => {
    let current = charityModel(10, 20);
    const sent: { body: Record<string, unknown>; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://example.test');
        const method = init?.method ?? 'GET';
        if (method === 'GET' && url.pathname === '/admin/api/session')
          return jsonResponse(adminSession);
        if (method === 'GET' && url.pathname === '/admin/api/time-context')
          return jsonResponse(siteTimeContext);
        if (method === 'GET' && url.pathname === '/admin/api/time-zones')
          return jsonResponse({
            version: 'go1.26.6-zoneinfo',
            zones: ['America/Indianapolis', 'UTC'],
          });
        if (method === 'GET' && url.pathname === '/admin/api/donations') {
          expectNumberedPageQuery(url);
          return jsonResponse(numberedPage([], url));
        }
        if (method === 'GET' && url.pathname === '/admin/api/charity-models') {
          expectNumberedPageQuery(url);
          return jsonResponse(numberedPage([current], url));
        }
        if (method === 'GET' && url.pathname === '/admin/api/charity-models/1')
          return jsonResponse(current);
        if (method === 'GET' && url.pathname.endsWith('/bindings')) {
          expect(url.search).toBe('');
          return jsonResponse({ bindings: [], binding_revision: '0' });
        }
        if (method === 'GET' && url.pathname === '/admin/api/donation-sources') {
          expectNumberedPageQuery(url, { scope: 'all' });
          return jsonResponse(numberedPage([], url));
        }
        if (method === 'GET' && url.pathname.endsWith('/binding-candidates')) {
          expectNumberedPageQuery(url);
          return jsonResponse(numberedPage([], url));
        }
        if (method === 'PATCH' && url.pathname === '/admin/api/charity-models/1') {
          const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
          sent.push({ body, key: new Headers(init?.headers).get('Idempotency-Key') });
          if (sent.length === 1) {
            current = {
              ...current,
              revision: '2',
              public_description: String(body.public_description),
            };
            throw new TypeError('Connection lost after saving');
          }
          if (sent.length === 3)
            current = {
              ...current,
              revision: '3',
              public_description: String(body.public_description),
            };
          return jsonResponse(current);
        }
        throw new Error('Unexpected model editor request');
      }),
    );
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });
    await view.user.click(await screen.findByRole('tab', { name: 'Charity models and bindings' }));
    await view.user.click(await screen.findByRole('button', { name: 'Manage' }));
    const heading = await screen.findByRole('heading', { name: '[公益]provider/model' });
    const card = heading.closest('.card');
    if (!(card instanceof HTMLElement)) throw new Error('Expected model editor card.');
    const editor = within(card);
    const text = editor.getByLabelText('Public description (plain text, optional)');
    fireEvent.change(text, { target: { value: 'first save' } });
    await view.user.click(editor.getByRole('button', { name: /^Save model / }));
    const retry = await editor.findByRole('button', { name: 'Retry the last save' });
    expect(editor.getByText(/This model has changed/)).toBeVisible();
    fireEvent.change(text, { target: { value: 'new draft' } });
    await view.user.click(retry);
    await waitFor(() => expect(editor.getByRole('button', { name: /^Save model / })).toBeEnabled());
    expect(sent).toHaveLength(2);
    expect(sent[1]).toEqual(sent[0]);
    expect(sent[1].body).toMatchObject({
      expected_revision: '1',
      public_description: 'first save',
    });
    expect(text).toHaveValue('new draft');
    await view.user.click(editor.getByRole('button', { name: /^Save model / }));
    await waitFor(() => expect(sent).toHaveLength(3));
    expect(sent[2].body).toMatchObject({ expected_revision: '2', public_description: 'new draft' });
    expect(sent[2].key).not.toBe(sent[0].key);
  });

  it('uses the same level and description editor for steward management', async () => {
    const model = {
      ...charityModel(10, 20),
      allowed_levels: [2, 5],
      public_description: 'Steward copy',
    };
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname === '/api/session') return jsonResponse(stewardSession);
      if (url.pathname === '/api/steward/time-context') return jsonResponse(siteTimeContext);
      if (url.pathname === '/api/time-zones')
        return jsonResponse({
          version: 'go1.26.6-zoneinfo',
          zones: ['America/Indianapolis', 'UTC'],
        });
      if (url.pathname === '/api/steward/donations') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([], url));
      }
      if (url.pathname === '/api/steward/charity-models') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([model], url));
      }
      if (url.pathname === '/api/steward/charity-models/1') return jsonResponse(model);
      if (url.pathname === '/api/steward/charity-models/1/bindings') {
        expect(url.search).toBe('');
        return jsonResponse({ bindings: [], binding_revision: model.binding_revision });
      }
      if (url.pathname === '/api/steward/donation-sources') {
        expectNumberedPageQuery(url, { scope: 'all' });
        return jsonResponse(numberedPage([], url));
      }
      if (url.pathname === '/api/steward/charity-models/1/binding-candidates') {
        expectNumberedPageQuery(url);
        return jsonResponse(numberedPage([], url));
      }
      throw new Error(`Unexpected request: ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<SessionBackedManagement frame="steward" />, {
      station: 'user',
      role: 'user',
    });

    await view.user.click(await screen.findByRole('tab', { name: 'Charity models and sources' }));
    await view.user.click(await screen.findByRole('button', { name: 'Manage' }));
    const heading = await screen.findByRole('heading', { name: '[公益]provider/model' });
    const editorCard = heading.closest('.card');
    if (!(editorCard instanceof HTMLElement))
      throw new Error('Expected steward model editor card.');
    const editor = within(editorCard);
    expect(editor.getByRole('checkbox', { name: 'L1' })).not.toBeChecked();
    expect(editor.getByRole('checkbox', { name: 'L2' })).toBeChecked();
    expect(editor.getByRole('checkbox', { name: 'L5' })).toBeChecked();
    expect(editor.getByLabelText('Public description (plain text, optional)')).toHaveValue(
      'Steward copy',
    );
  });
});

it('shows a directly rejected donation as never approved', async () => {
  installDonationFetch({
    ...approvedDonation(),
    status: 'rejected',
    first_approval_origin: 'none',
    can_force_reject: false,
    review_result: { decision: 'reject', reason: 'Unsupported source', reviewed_at: 10 },
  });
  const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
    station: 'admin',
    role: 'admin',
  });
  await view.user.click(await screen.findByRole('button', { name: 'Review' }));
  expect(await screen.findByText('Never approved')).toBeVisible();
  expect(screen.queryByText('First approval source is unknown')).not.toBeInTheDocument();
});

describe('trainee model capabilities', () => {
  it.each(['images_generations', 'embeddings'] as const)(
    'shows non-chat capabilities without editable chat policies for %s',
    async (types) => {
      const model: CharityModel = { ...charityModel(10, 20), model_types: [types] };
      await renderWithProviders(<CharityRolePolicyForm model={model} refresh={vi.fn()} />, {
        station: 'user',
        role: 'level5',
      });
      expect(screen.getByRole('heading', { name: model.full_name })).toBeVisible();
      expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /^Save model / })).not.toBeInTheDocument();
    },
  );
});

describe('CharityManagement candidate key shortcuts', () => {
  it.each(['admin', 'level6', 'level5'] as const)(
    'opens and returns from only the selected key using existing %s permissions',
    async (access) => {
      const frame = access === 'admin' ? 'admin' : 'steward';
      const prefix = frame === 'admin' ? '/admin/api' : '/api/steward';
      const donation = approvedDonation(managedKey({ safe_note: 'Shortcut key' }));
      const model = { ...charityModel(10, 20), is_mainstream: true };
      const requests: string[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async (input, init) => {
          const url = new URL(String(input), 'https://example.test');
          requests.push(url.pathname + url.search);
          if ((init?.method ?? 'GET') !== 'GET') throw new Error('Unexpected write');
          if (url.pathname === '/admin/api/session') return jsonResponse(adminSession);
          if (url.pathname === '/api/session')
            return jsonResponse({
              ...stewardSession,
              user: { ...stewardSession.user, effective_level: access === 'level5' ? 5 : 6 },
            });
          if (url.pathname.endsWith('/time-context')) return jsonResponse(siteTimeContext);
          if (url.pathname.endsWith('/time-zones'))
            return jsonResponse({
              version: 'go1.26.6-zoneinfo',
              zones: ['UTC', 'America/Indianapolis'],
            });
          if (url.pathname === prefix + '/charity-models')
            return jsonResponse(numberedPage([model], url));
          if (url.pathname === prefix + '/charity-models/1') return jsonResponse(model);
          if (url.pathname.endsWith('/bindings'))
            return jsonResponse({ bindings: [], binding_revision: '0' });
          if (url.pathname === prefix + '/donations')
            return jsonResponse(numberedPage([donationSummary(donation)], url));
          if (url.pathname === prefix + '/donations/1') {
            expect(frame).toBe('admin');
            return jsonResponse(donation);
          }
          if (url.pathname === prefix + '/donations/1/keys/11') {
            expect(frame).toBe('steward');
            expect(url.searchParams.get('charity_model_id')).toBe('1');
            return jsonResponse(keySummary(donation, donation.keys[0]));
          }
          if (url.pathname === prefix + '/donation-sources')
            return jsonResponse(
              numberedPage(
                [
                  {
                    source_key: 'dsg_' + 'A'.repeat(43),
                    safe_source: donation.keys[0].safe_source,
                    donation_count: '1',
                    key_count: '1',
                    usable_key_count: '1',
                    pending_donation_count: '0',
                  },
                ],
                url,
              ),
            );
          if (url.pathname.endsWith('/keys'))
            return jsonResponse(numberedPage([keySummary(donation, donation.keys[0])], url));
          if (url.pathname.endsWith('/binding-candidates'))
            return jsonResponse(
              numberedPage([bindingCandidate(donation, donation.keys[0], 'shortcut-model')], url),
            );
          throw new Error('Unexpected request: ' + url.pathname);
        }),
      );
      const view = await renderWithProviders(<SessionBackedManagement frame={frame} />, {
        station: frame === 'admin' ? 'admin' : 'user',
        role: access,
      });
      await view.user.click(await screen.findByRole('tab', { name: /Charity models and/ }));
      await view.user.click(await screen.findByRole('button', { name: 'Manage' }));
      const pickerNode = await waitFor(() => {
        const node = view.container.querySelector('.ops-binding-picker');
        expect(node).not.toBeNull();
        return node as HTMLElement;
      });
      const picker = within(pickerNode);
      await view.user.click(
        await picker.findByRole('button', { name: /https:\/\/example.test\/v1/ }),
      );
      await view.user.click(await picker.findByRole('button', { name: /Shortcut key/ }));
      await view.user.click(await picker.findByRole('checkbox', { name: /shortcut-model/ }));
      await view.user.click(picker.getByRole('button', { name: 'Manage key' }));
      await screen.findByRole('heading', { name: 'Manage key #11' });
      const save = await screen.findByRole('button', { name: 'Save key limits' });
      expect(save).toBeEnabled();
      expect(screen.getByRole('button', { name: 'Add 1 connections' })).toBeDisabled();
      await view.user.click(screen.getByRole('button', { name: 'Return to model list' }));
      await waitFor(() =>
        expect(picker.getByRole('checkbox', { name: /shortcut-model/ })).toBeEnabled(),
      );
      expect(picker.getByRole('checkbox', { name: /shortcut-model/ })).toBeChecked();
      expect(screen.getByRole('button', { name: 'Add 1 connections' })).toBeEnabled();
      if (frame === 'steward') {
        expect(requests).toContain(prefix + '/donations/1/keys/11?charity_model_id=1');
        expect(requests).not.toContain(prefix + '/donations/1');
      }
    },
  );
});

describe('level-5 pending review queue', () => {
  it.each(['approve', 'reject'] as const)(
    'completes %s and refreshes only the pending queue',
    async (decision) => {
      let current: AdminDonation = {
        ...pendingDonation(),
        owner: null,
        reviewer: null,
        can_force_reject: false,
      };
      let completed = false;
      let completedReads = 0;
      let submitted: Record<string, unknown> | undefined;
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async (input, init) => {
          const url = new URL(String(input), 'https://example.test');
          if (url.pathname.endsWith('/time-context')) return jsonResponse(siteTimeContext);
          if (url.pathname === '/api/steward/donations') {
            return jsonResponse(numberedPage(completed ? [] : [donationSummary(current)], url));
          }
          if (url.pathname === '/api/steward/donations/1' && (init?.method ?? 'GET') === 'GET') {
            if (completed) completedReads++;
            return jsonResponse(current);
          }
          if (url.pathname === '/api/steward/donations/1/review') {
            submitted = JSON.parse(String(init?.body));
            completed = true;
            current = {
              ...current,
              status: decision === 'approve' ? 'approved' : 'rejected',
              revision: '2',
              review_result: { decision, reason: 'Reviewed pending source', reviewed_at: 2 },
            };
            return jsonResponse(current);
          }
          throw new Error(`Unexpected pending review request: ${url.pathname}`);
        }),
      );
      const view = await renderWithProviders(
        <CharityManagement frame="steward" accountId="1" trainee />,
        {
          station: 'user',
          route: '/steward?charity_section=donations',
          role: 'level5',
        },
      );
      view.queryClient.setQueryData(['user', 'session'], {
        ...stewardSession,
        user: { ...stewardSession.user, effective_level: 5 },
      });
      await view.user.click(await screen.findByRole('button', { name: 'Review' }));
      await screen.findByRole('heading', { name: 'Review pending submission' });
      expect(
        screen.queryByRole('button', { name: 'Force reject donation' }),
      ).not.toBeInTheDocument();
      if (decision === 'reject')
        await view.user.selectOptions(screen.getByLabelText('Decision'), 'reject');
      await view.user.type(screen.getByLabelText('Reason'), 'Reviewed pending source');
      await view.user.click(
        screen.getByRole('button', {
          name: decision === 'approve' ? 'Approve donation' : 'Reject donation',
        }),
      );
      await screen.findByText(/Review saved:/);
      await screen.findByRole('heading', { name: 'No donations' });
      expect(submitted).toMatchObject({
        decision,
        expected_revision: '1',
        reason: 'Reviewed pending source',
      });
      expect(completedReads).toBe(0);
    },
  );
});

it('keeps level-5 access when a pending target was concurrently reviewed', async () => {
  const capabilityLost = vi.fn();
  let listReads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname === '/api/steward/donations') {
        listReads++;
        return jsonResponse(numberedPage([], url));
      }
      if (url.pathname === '/api/steward/donations/1')
        return new Response(
          JSON.stringify({ error: { code: 'forbidden', message: 'No longer pending' } }),
          { status: 403, headers: { 'content-type': 'application/json' } },
        );
      throw new Error(`Unexpected pending review request: ${url.pathname}`);
    }),
  );
  const view = await renderWithProviders(
    <CharityManagement frame="steward" accountId="1" trainee onCapabilityLoss={capabilityLost} />,
    { station: 'user', route: '/steward?charity_section=donations&donation_id=1' },
  );
  await screen.findByText('This action is not allowed for the current account.');
  expect(listReads).toBeGreaterThanOrEqual(1);
  expect(capabilityLost).not.toHaveBeenCalled();
  await view.user.click(screen.getByRole('button', { name: 'Return to list' }));
  await screen.findByRole('heading', { name: 'No donations' });
});

it.each(['approve', 'reject'] as const)(
  'keeps the confirmed use %s receipt when the last pending detail loses access and the queue refresh fails',
  async (decision) => {
    const current: AdminDonation = {
      ...pendingDonation(),
      owner: null,
      reviewer: null,
      can_force_reject: false,
      use_summary: { total: '1', pending: '1', approved: '0' },
    };
    const pending = {
      credential_id: '8',
      credential_version: '1',
      grant_revision: '1',
      status: 'pending',
      review_note: '',
      reviewed_at: null,
      expected_review_revision: null,
      discovery_source_id: null,
      discovery_source_revision: null,
      display_head: 'head',
      display_tail: 'tail',
      review: { required: false, revision: null, material_available: false },
      discovery: null,
    };
    const use = {
      id: '6',
      donation_id: '1',
      donation_key_id: '11',
      endpoint_key_id: null,
      native_service: 'chat_completions',
      enabled: true,
      discovery_enabled: false,
      revision: '9',
      approved: null,
      pending,
      latest: pending,
      health: null,
      capabilities: {
        can_review: true,
        can_manage: false,
        can_reset_failures: false,
        can_discover: false,
        can_bind: false,
        unavailable_reason: 'not_approved',
      },
    };
    const capabilityLost = vi.fn();
    let completed = false;
    let failQueue = true;
    let writes = 0;
    let finishWrite!: () => void;
    const writeGate = new Promise<void>((resolve) => {
      finishWrite = resolve;
    });
    const error = (status: number, code: string) =>
      new Response(
        JSON.stringify({
          error: {
            code,
            message: code === 'forbidden' ? 'No longer pending' : 'Refresh temporarily unavailable',
          },
        }),
        { status, headers: { 'content-type': 'application/json' } },
      );
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://example.test');
        if (url.pathname.endsWith('/time-context')) return jsonResponse(siteTimeContext);
        if (url.pathname === '/api/steward/donations') {
          return completed && failQueue
            ? error(503, 'unavailable')
            : jsonResponse(numberedPage(completed ? [] : [donationSummary(current)], url));
        }
        if (url.pathname === '/api/steward/donations/1')
          return completed ? error(403, 'forbidden') : jsonResponse(current);
        if (url.pathname === '/api/steward/donations/1/uses')
          return completed ? error(403, 'forbidden') : jsonResponse(numberedPage([use], url));
        if (url.pathname === '/api/steward/donations/1/pending-review') {
          expect(init?.method).toBe('POST');
          writes++;
          expect(JSON.parse(String(init?.body))).toEqual({
            expected_revision: current.revision,
            decision,
            reason: '',
            uses: [
              {
                use_id: '6',
                expected_revision: '9',
                expected_review_revision: null,
              },
            ],
          });
          expect(new Headers(init?.headers).get('Idempotency-Key')).toBeTruthy();
          await writeGate;
          completed = true;
          return jsonResponse({
            ...current,
            revision: '2',
            status: decision === 'approve' ? 'approved' : 'rejected',
            review_result: { decision, reason: '', reviewed_at: 1_800_000_000 },
            use_summary: { total: '1', pending: '0', approved: decision === 'approve' ? '1' : '0' },
          });
        }
        throw new Error('Unexpected request: ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(
      <CharityManagement frame="steward" accountId="1" trainee onCapabilityLoss={capabilityLost} />,
      {
        station: 'user',
        route: '/steward?charity_section=donations&donation_id=1',
        role: 'level5',
      },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      ...stewardSession,
      user: { ...stewardSession.user, effective_level: 5 },
    });
    await screen.findByRole('button', { name: 'Approve pending content' });
    await view.user.click(
      screen.getByRole('button', {
        name: decision === 'approve' ? 'Approve pending content' : 'Return pending content',
      }),
    );
    await waitFor(() => expect(writes).toBe(1));
    expect(screen.getByRole('button', { name: 'Return to list' })).toBeDisabled();
    finishWrite();
    await screen.findByText('Review saved.');
    await screen.findByText('Refresh temporarily unavailable');
    expect(screen.queryByRole('heading', { name: 'Donation uses' })).not.toBeInTheDocument();
    failQueue = false;
    await view.user.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByText(
      'This donation has already been reviewed. Return to the pending queue.',
    );
    expect(screen.getByText('Review saved.')).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Return to list' }));
    await screen.findByRole('heading', { name: 'No donations' });
    expect(screen.getByText('Review saved.')).toBeVisible();
    expect(writes).toBe(1);
    expect(capabilityLost).not.toHaveBeenCalled();
  },
);
