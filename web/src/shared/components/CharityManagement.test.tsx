import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useAdminSession } from '../../admin/data';
import { useUserSession } from '../../user/data';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import type { AdminDonation, CharityModel, ManagedDonationKey } from '@shared/operations/charity';
import { CharityManagement } from './CharityManagement';

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

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
  return accountId ? <CharityManagement frame={frame} accountId={accountId} /> : null;
}

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
          decision: approved ? 'approve' : 'reject',
          reason: String(body.reason),
          reviewed_at: 11,
        },
        reviewer: { user_id: '9', role: 'admin' },
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
  route_strategy: 'expiry_weighted',
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
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
});

const datePart = (value: number) => String(value).padStart(2, '0');
function localDateTime(epoch: number): string {
  const date = new Date(epoch * 1_000);
  return `${date.getFullYear()}-${datePart(date.getMonth() + 1)}-${datePart(date.getDate())}T${datePart(date.getHours())}:${datePart(date.getMinutes())}:${datePart(date.getSeconds())}`;
}

describe('CharityManagement corrective controls', () => {
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
          expectNumberedPageQuery(url, { scope: 'active' });
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
          expectNumberedPageQuery(url, { scope: 'active' });
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
        if (method === 'POST' && url.pathname.endsWith('/bindings/batch')) {
          bodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
          bindingRevision = '1';
          return jsonResponse({ bindings: [], binding_revision: bindingRevision });
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
    const pickerNode = view.container.querySelector('.ops-binding-picker')! as HTMLElement;
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
    await view.user.click(screen.getByRole('button', { name: 'Add selected connections' }));
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
        streak: { generation: '2', count: '10', failure_disabled: true },
      }),
    );
    const requests = installDonationFetch(fixture);
    const view = await renderWithProviders(<SessionBackedManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Donation #1' });
    expect(screen.getByLabelText('Charity switch change')).toHaveValue('');
    await view.user.click(screen.getByRole('checkbox', { name: 'Reset failure streak' }));
    await view.user.click(screen.getByRole('button', { name: 'Save key limits' }));

    await waitFor(() => expect(requests.keyBodies).toHaveLength(1));
    expect(requests.keyBodies[0]).toEqual({
      expected_revision: '1',
      price_limit: null,
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
    await screen.findByRole('heading', { name: 'Donation #1' });
    await view.user.selectOptions(screen.getByLabelText('Charity switch change'), 'false');
    await view.user.click(screen.getByRole('button', { name: 'Save key limits' }));

    await waitFor(() => expect(requests.keyBodies).toHaveLength(1));
    expect(requests.keyBodies[0]).toEqual({
      expected_revision: '1',
      enabled: false,
      price_limit: null,
      calls_limit: null,
      tokens_limit: null,
      token_reserve: 32,
      safe_note: 'safe note',
      expires_at: null,
    });
    expect(requests.idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
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
    await view.user.click(
      screen.getByRole('checkbox', {
        name: 'I confirm this review result and its per-key consequences.',
      }),
    );
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

  it('renders local discount seconds and preserves untouched epochs', async () => {
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
        expectNumberedPageQuery(url, { scope: 'active' });
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
          discount: body.discount as CharityModel['discount'],
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
    expect(editor.getByLabelText('Start (optional)')).toHaveValue(`${localDateTime(start)}.000`);
    expect(editor.getByLabelText('End (optional)')).toHaveValue(`${localDateTime(end)}.000`);

    await view.user.click(editor.getByRole('button', { name: 'Save model' }));

    await waitFor(() => expect(patchBodies).toHaveLength(1));
    expect(patchBodies[0]).toEqual({
      route_strategy: 'expiry_weighted',
      expected_revision: '1',
      provider: 'provider',
      model: 'model',
      enabled: true,
      allowed_levels: [1, 2, 3, 4, 5],
      public_description: '',
      pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
      discount: { enabled: true, percent: 10, start_at: start, end_at: end },
      flatten_tool_calls: false,
    });
    expect(patchBodies[0]).not.toHaveProperty('pricing_mode');
    expect(patchBodies[0]).not.toHaveProperty('prices');
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
      reserveLabel: 'Call reservation (credits)',
      reserveHelp: 'Leave blank to inherit the global configuration.',
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
      modelsTab: '公益模型与服务连接',
      reserveLabel: '调用前预留积分',
      reserveHelp: '留空继承全局配置。',
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
          expectNumberedPageQuery(url, { scope: 'active' });
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
            pricing: body.pricing as CharityModel['pricing'],
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
      const reserve = editor.getByLabelText(fixture.reserveLabel);
      expect(reserve).toHaveValue('1.234');
      expect(editor.getByText(fixture.reserveHelp)).toBeVisible();

      fireEvent.change(reserve, { target: { value: '0' } });
      expect(editor.getByRole('button', { name: /Save model|保存模型/ })).toBeDisabled();
      await view.user.selectOptions(
        editor.getByRole('combobox', { name: fixture.pricingLabel }),
        'per_request',
      );
      expect(editor.queryByLabelText(fixture.reserveLabel)).not.toBeInTheDocument();
      expect(editor.getByRole('button', { name: /Save model|保存模型/ })).toBeEnabled();
      await view.user.click(editor.getByRole('button', { name: /Save model|保存模型/ }));
      await waitFor(() => expect(patchBodies).toHaveLength(1));
      expect(patchBodies[0]).not.toHaveProperty('token_reserve_credits');
      expect(current.pricing).toMatchObject({ mode: 'per_request' });

      await view.user.selectOptions(
        editor.getByRole('combobox', { name: fixture.pricingLabel }),
        'per_token',
      );
      const reserveAgain = editor.getByLabelText(fixture.reserveLabel);
      expect(reserveAgain).toHaveValue('0');
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
    expect(screen.getByRole('checkbox', { name: 'L1' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'L5' })).toBeChecked();
    await view.user.click(screen.getByRole('button', { name: 'Clear all levels' }));
    expect(
      screen.getByText('No ordinary user can call this model (including level 5 users).'),
    ).toBeVisible();
    await view.user.click(screen.getByRole('checkbox', { name: 'L2' }));
    await view.user.click(screen.getByRole('checkbox', { name: 'L5' }));
    await view.user.type(screen.getByLabelText('Provider'), 'provider');
    await view.user.type(screen.getByLabelText('Model'), 'model');
    fireEvent.change(screen.getByLabelText('Public description (plain text, optional)'), {
      target: { value: 'First line\r\n<b>literal</b>\t😀' },
    });

    const pricing = screen.getByRole('combobox', { name: 'Pricing mode' });
    await view.user.selectOptions(pricing, 'per_token');
    const hiddenReserve = screen.getByLabelText('Call reservation (credits)');
    fireEvent.change(hiddenReserve, { target: { value: '0' } });
    expect(screen.getByRole('button', { name: 'Add charity model' })).toBeDisabled();
    await view.user.selectOptions(pricing, 'per_request');
    expect(screen.queryByLabelText('Call reservation (credits)')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add charity model' })).toBeEnabled();
    await view.user.click(screen.getByRole('button', { name: 'Add charity model' }));
    await waitFor(() => expect(createBodies).toHaveLength(1));
    expect(createBodies[0]).toMatchObject({
      allowed_levels: [2, 5],
      public_description: 'First line\n<b>literal</b>\t😀',
    });
    expect(createBodies[0]).not.toHaveProperty('token_reserve_credits');
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
        expectNumberedPageQuery(url, { scope: 'active' });
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
    fireEvent.change(textarea, { target: { value: 'submitted' } });
    await view.user.click(editor.getByRole('button', { name: 'Save model' }));
    await waitFor(() => expect(patchBodies).toHaveLength(1));
    fireEvent.change(textarea, { target: { value: 'new draft' } });

    current = { ...current, revision: '2', public_description: 'submitted', updated_at: 2 };
    patchResponse.resolve(jsonResponse(current));
    await waitFor(() => expect(editor.getByRole('button', { name: 'Save model' })).toBeEnabled());
    expect(textarea).toHaveValue('new draft');
    expect(patchBodies[0]).toMatchObject({
      public_description: 'submitted',
      expected_revision: '1',
    });
    await view.user.click(editor.getByRole('button', { name: 'Save model' }));
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
          expectNumberedPageQuery(url, { scope: 'active' });
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
    await view.user.click(editor.getByRole('button', { name: 'Save model' }));
    const retry = await editor.findByRole('button', { name: 'Retry the last save' });
    expect(editor.getByText(/This model has changed/)).toBeVisible();
    fireEvent.change(text, { target: { value: 'new draft' } });
    await view.user.click(retry);
    await waitFor(() => expect(editor.getByRole('button', { name: 'Save model' })).toBeEnabled());
    expect(sent).toHaveLength(2);
    expect(sent[1]).toEqual(sent[0]);
    expect(sent[1].body).toMatchObject({
      expected_revision: '1',
      public_description: 'first save',
    });
    expect(text).toHaveValue('new draft');
    await view.user.click(editor.getByRole('button', { name: 'Save model' }));
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
        expectNumberedPageQuery(url, { scope: 'active' });
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

    await view.user.click(
      await screen.findByRole('tab', { name: 'Charity models and service connections' }),
    );
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
