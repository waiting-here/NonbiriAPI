import { numberedResponse } from './numbered-fixtures';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  assertWorkbenchWidth,
  assertResponsiveOperationTables,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';
import { expect, test } from './test';
import {
  createOptions,
  mockConnectionResources,
  mixedMainstreamLayout,
  mainstreamProjection,
  credential,
} from './connection-fixtures';
import commonCopy from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import userCopy from '../../src/user/i18n/en.json' with { type: 'json' };
const connectionCopy = commonCopy.common.connections;

type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type Page = Parameters<typeof collectConsoleViolations>[0];

const EPHEMERAL_MARKER = 'dec020-e2e-ephemeral-marker';
const MAINSTREAM_CHANNEL_ID = `mch_${'C'.repeat(21)}A`;
const REPORT_ID = `rpc_${'R'.repeat(21)}A`;
const REPORT_TARGET_ID = `rpt_${'T'.repeat(21)}A`;

function catalogModel(model: Record<string, unknown>, levelAllowed = true) {
  return {
    ...model,
    public_description: '',
    enabled: true,
    allowed_levels: levelAllowed ? [1, 2, 3, 4, 5] : [1, 2, 3, 4],
    level_allowed: levelAllowed,
    availability: levelAllowed ? 'available' : 'level_denied',
    currently_available: true,
  };
}

function catalogPage(
  models: Record<string, unknown>[],
  donationIntake: 'open' | 'closed',
  serverNow = 1_800_000_000,
) {
  return {
    models: models.map((model) => catalogModel(model)),
    pagination: {
      page: '1',
      page_size: 20,
      total_items: String(models.length),
      total_pages: String(Math.max(1, Math.ceil(models.length / 20))),
    },
    donation_intake: donationIntake,
    server_now: serverNow,
  };
}

async function prepare(
  context: BrowserContext,
  page: Page,
  station: 'admin' | 'user',
  role: 'admin' | 'user' | 'anonymous',
  locale: 'en' | 'zh' = 'en',
  theme: 'light' | 'dark' = 'light',
) {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await page.setViewportSize({ width: 1_280, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(
    ({ selectedLocale, selectedTheme }) => {
      localStorage.setItem('nb.lang', selectedLocale);
      localStorage.setItem('nb.theme', selectedTheme);
    },
    { selectedLocale: locale, selectedTheme: theme },
  );
  await mockPublicConfig(page, station);
  await mockRoleSession(page, station, role);
  if (station === 'user')
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/endpoint-create-options',
      body: createOptions,
    });
  await page.route(/\/donations\/[1-9][0-9]*\/uses\?/, async (route) => {
    expect(route.request().method()).toBe('GET');
    const params = new URL(route.request().url()).searchParams;
    expect(params.get('page_size')).toBe('100');
    await route.fulfill({ json: numberedResponse([], params.get('page') ?? '1', 100) });
  });
  return consoleGuard;
}

async function assertClean(page: Page, consoleGuard: ReturnType<typeof collectConsoleViolations>) {
  await assertResponsiveOperationTables(page);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
}

async function assertPricesFit(page: Page) {
  const table = page.locator('.charity-price-table');
  const layout = await table.evaluate((element) => {
    const wrapper = element.parentElement!;
    const bounds = wrapper.getBoundingClientRect();
    return {
      wrapperOverflow: wrapper.scrollWidth > wrapper.clientWidth + 1,
      tableOverflow: element.scrollWidth > element.clientWidth + 1,
      pricesVisible: [...element.querySelectorAll('.charity-amount')].every((amount) => {
        const rect = amount.getBoundingClientRect();
        return rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1;
      }),
    };
  });
  expect(layout).toEqual({ wrapperOverflow: false, tableOverflow: false, pricesVisible: true });
}

function endpointKey(
  id: string,
  state: 'available' | 'disabled' | 'expired',
  options: {
    endpointKeyID?: string | null;
    endedReason?: 'expired' | null;
    expiresAt?: number | null;
    displayHead?: string;
  } = {},
) {
  const terminal = state === 'expired';
  return {
    id,
    endpoint_key_id: options.endpointKeyID === undefined ? id : options.endpointKeyID,
    display_head: options.displayHead ?? `key-${id}`,
    display_tail: 'tail',
    safe_source: {
      kind: 'custom',
      connector_type: 'openai-compatible',
      base_url: 'https://donor.example.test/v1',
    },
    physical_enabled: true,
    charity_state: state,
    limits: terminal
      ? { price: null, calls: null, tokens: null }
      : { price: null, calls: '20', tokens: '1000' },
    usage: {
      price_used: '0',
      price_inflight: '0',
      calls_used: '0',
      calls_inflight: '0',
      tokens_used: '0',
      tokens_inflight: '0',
    },
    token_reserve: terminal ? 0 : 32,
    expires_at:
      options.expiresAt === undefined
        ? terminal
          ? 1_800_000_000
          : 1_800_003_600
        : options.expiresAt,
    failure_disable_threshold: '10',
    smart_backoff: null,
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason:
      options.endedReason === undefined ? (terminal ? 'expired' : null) : options.endedReason,
  };
}

function userDonation(
  id: string,
  keys: unknown[],
  options: {
    status?: 'approved' | 'expired';
    reviewAt?: number;
    createdAt?: number;
    updatedAt?: number;
  } = {},
) {
  const status = options.status ?? 'approved';
  const createdAt = options.createdAt ?? 1_800_000_000;
  const updatedAt = options.updatedAt ?? createdAt + 10;
  return {
    id,
    status,
    revision: '1',
    description: `Fixture donation ${id}`,
    review_result: {
      decision: 'approve',
      reason: 'Fixture authority review',
      reviewed_at: options.reviewAt ?? createdAt + 1,
    },
    keys,
    created_at: createdAt,
    updated_at: updatedAt,
  };
}

function ownerDonationPageItem(donation: ReturnType<typeof userDonation>): Record<string, unknown> {
  const keys = donation.keys as Array<Record<string, unknown>>;
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
  const sources: Record<string, unknown>[] = [];
  for (const key of keys) {
    const state = key.charity_state;
    if (typeof state === 'string' && Object.hasOwn(stateCounts, state)) {
      stateCounts[state as keyof typeof stateCounts] = String(
        Number(stateCounts[state as keyof typeof stateCounts]) + 1,
      );
    }
    const source = key.safe_source;
    if (
      source !== null &&
      typeof source === 'object' &&
      !Array.isArray(source) &&
      !sources.some((entry) => JSON.stringify(entry) === JSON.stringify(source))
    ) {
      sources.push(source as Record<string, unknown>);
    }
  }
  return {
    id: donation.id,
    status: donation.status,
    revision: donation.revision,
    description: donation.description,
    review_result: donation.review_result,
    created_at: donation.created_at,
    updated_at: donation.updated_at,
    key_count: String(keys.length),
    state_counts: stateCounts,
    source_count: String(sources.length),
    sources,
  };
}

function endpointSummary(id: string, keyCount: string) {
  return {
    id,
    connector_type: 'openai-compatible',
    base_url: 'https://donor.example.test/v1',
    origin: { kind: 'custom' },
    note: 'Fixture endpoint',
    enabled: true,
    revision: '1',
    key_count: keyCount,
    created_at: 1_800_000_000,
    updated_at: 1_800_000_001,
  };
}

function endpointKeySummary(id: string, endpointID: string, displayHead = `key-${id}`) {
  return {
    id,
    endpoint_id: endpointID,
    credential_id: id,
    credential_version: '1',
    grant_revision: '1',
    native_services: ['chat_completions', 'responses'],
    discovery_source_id: null,
    display_head: displayHead,
    display_tail: 'tail',
    note: '',
    enabled: true,
    force_store_false: false,
    suspension_state: 'none',
    revision: '1',
    created_at: 1_800_000_000,
    updated_at: 1_800_000_001,
  };
}

test('administrator mainstream channel CRUD keeps channel details and archive consequences visible', async ({
  context,
  page,
}) => {
  const guard = await prepare(context, page, 'admin', 'admin', 'en', 'dark');
  await page.route(`${ADMIN_ORIGIN}/admin/api/endpoint-create-options`, (route) =>
    route.fulfill({ json: createOptions }),
  );
  let channel: Record<string, unknown> | null = null;
  const requests: Array<{ method: string; body: Record<string, unknown> | null }> = [];

  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== ADMIN_ORIGIN || !url.pathname.startsWith('/admin/api/mainstream-channels')) {
      await route.fallback();
      return;
    }
    const body = request.postDataJSON() as Record<string, unknown> | null;
    requests.push({ method: request.method(), body });
    if (request.method() === 'GET' && url.pathname === '/admin/api/mainstream-channels') {
      const state = url.searchParams.get('state');
      const include =
        channel !== null &&
        (state === 'all' ||
          state === channel.state ||
          (state === 'active' && channel.state === 'active'));
      await route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
        body: JSON.stringify(
          numberedResponse(
            include ? [channel] : [],
            url.searchParams.get('page') ?? '1',
            Number(url.searchParams.get('page_size') ?? '20'),
          ),
        ),
      });
      return;
    }
    if (
      request.method() === 'GET' &&
      url.pathname === `/admin/api/mainstream-channels/${MAINSTREAM_CHANNEL_ID}`
    ) {
      if (!channel) {
        await route.fulfill({
          status: 404,
          body: JSON.stringify({ error: { code: 'not_found' } }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
        body: JSON.stringify(channel),
      });
      return;
    }
    if (request.method() === 'POST' && url.pathname === '/admin/api/mainstream-channels') {
      const input = body ?? {};
      if (mixedMainstreamLayout(input))
        return route.fulfill({
          status: 400,
          json: { error: { code: 'invalid_request', message: 'Use one channel layout.' } },
        });
      channel = {
        id: MAINSTREAM_CHANNEL_ID,
        name: String(input.name ?? ''),
        category: input.category,
        ...mainstreamProjection(input),
        enabled: input.enabled,
        entries: ((input.entries ?? []) as Record<string, unknown>[]).map((entry, index) => ({
          ...entry,
          id: String(index + 1),
        })),
        discovery_source: input.discovery_source,
        model_mappings: input.model_mappings,
        state: 'active',
        revision: '1',
        created_at: 1_800_000_000,
        updated_at: 1_800_000_000,
        retired_at: null,
      };
      await route.fulfill({
        status: 201,
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
        body: JSON.stringify(channel),
      });
      return;
    }
    if (
      request.method() === 'PATCH' &&
      url.pathname === `/admin/api/mainstream-channels/${MAINSTREAM_CHANNEL_ID}`
    ) {
      if (!channel) {
        await route.fulfill({
          status: 404,
          body: JSON.stringify({ error: { code: 'not_found' } }),
        });
        return;
      }
      if (mixedMainstreamLayout(body ?? {}))
        return route.fulfill({
          status: 400,
          json: { error: { code: 'invalid_request', message: 'Use one channel layout.' } },
        });
      channel = {
        ...channel,
        ...body,
        ...mainstreamProjection(body ?? {}),
        revision: (BigInt(String(channel.revision)) + 1n).toString(),
        updated_at: 1_800_000_010,
      };
      delete channel.expected_revision;
      await route.fulfill({
        status: 200,
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
        body: JSON.stringify(channel),
      });
      return;
    }
    if (
      request.method() === 'DELETE' &&
      url.pathname === `/admin/api/mainstream-channels/${MAINSTREAM_CHANNEL_ID}`
    ) {
      if (channel) {
        channel = {
          ...channel,
          enabled: false,
          state: 'retired',
          revision: '4',
          retired_at: 1_800_000_020,
          updated_at: 1_800_000_020,
        };
      }
      await route.fulfill({ status: 204, headers: { 'cache-control': 'no-store' } });
      return;
    }
    await route.fallback();
  });

  await page.goto(`${ADMIN_ORIGIN}/mainstream-channels`);
  await expect(page.getByRole('heading', { name: 'Mainstream channels', level: 1 })).toBeVisible();
  await page.getByRole('button', { name: /New channel/ }).click();
  await page.getByLabel('Channel name').fill('Fixture channel');
  await page.getByRole('combobox', { name: 'Category', exact: true }).selectOption('api_platform');
  await page.getByRole('button', { name: connectionCopy.addEntry, exact: true }).click();
  await page
    .getByRole('combobox', { name: connectionCopy.protocol, exact: true })
    .selectOption('anthropic-compatible');
  await page
    .getByLabel(connectionCopy.entryURL, { exact: true })
    .fill('https://channel.example.test/v1');
  await page.getByRole('button', { name: 'Create channel' }).click();

  await expect(page.getByRole('heading', { name: 'Channel details' })).toBeVisible();
  expect(requests.find((request) => request.method === 'POST')?.body).toEqual({
    name: 'Fixture channel',
    category: 'api_platform',
    enabled: true,
    entries: [
      {
        name: '',
        connector_type: 'anthropic-compatible',
        base_url: 'https://channel.example.test/v1',
        native_services: ['anthropic_messages'],
        rerank_format: '',
        enabled: true,
      },
    ],
    discovery_source: null,
    model_mappings: [],
  });
  const editName = page.getByLabel('Channel name');
  await editName.fill('Renamed channel');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByText('Renamed channel')).toBeVisible();
  expect(requests.find((request) => request.method === 'PATCH')?.body).toMatchObject({
    expected_revision: '1',
    name: 'Renamed channel',
  });

  await page
    .getByLabel(connectionCopy.entryURL, { exact: true })
    .fill('https://updated-channel.example.test/v1');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect.poll(() => requests.filter((row) => row.method === 'PATCH').length).toBe(2);
  const layoutPatch = requests.filter((row) => row.method === 'PATCH').at(-1)?.body;
  expect(layoutPatch).toMatchObject({
    expected_revision: '2',
    entries: [
      expect.objectContaining({ id: '1', base_url: 'https://updated-channel.example.test/v1' }),
    ],
  });
  expect(layoutPatch).not.toHaveProperty('connector_type');
  expect(layoutPatch).not.toHaveProperty('base_url');

  await page.getByRole('button', { name: 'Archive channel' }).click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Archive channel' }).click();
  await expect(
    page.getByText('Archived channels cannot be edited or re-enabled.', { exact: false }),
  ).toBeVisible();
  expect(requests.find((request) => request.method === 'DELETE')?.body).toEqual({
    expected_revision: '3',
    confirmation: 'retire',
  });
  await assertClean(page, guard);
});

test('user endpoint source wizard submits an immutable mainstream channel selection', async ({
  context,
  page,
}) => {
  const guard = await prepare(context, page, 'user', 'user');
  const endpointID = '21';
  const endpoint = {
    id: endpointID,
    connector_type: 'openai-compatible',
    base_url: 'https://channel.example.test/v1',
    origin: { kind: 'mainstream', channel_id: MAINSTREAM_CHANNEL_ID, name: 'Hosted channel' },
    note: 'Selected channel endpoint',
    enabled: true,
    revision: '1',
    key_count: '0',
    connection_id: '1',
    native_services: ['chat_completions', 'responses', 'decisions', 'embeddings'],
    created_at: 1_800_000_000,
    updated_at: 1_800_000_000,
  };
  let postBody: Record<string, unknown> | null = null;
  let grantBody: Record<string, unknown> | null = null;
  await mockConnectionResources(page, () => (postBody ? [endpoint] : []));
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoints?page=1&page_size=20',
    body: numberedResponse([], '1', 20),
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoint-create-options',
    body: {
      base_connector_types: ['anthropic-compatible', 'openai-compatible'],
      native_service_options: createOptions.native_service_options,
      mainstream_channels: [
        {
          id: MAINSTREAM_CHANNEL_ID,
          name: 'Hosted channel',
          revision: '2',
          entries: [
            {
              id: '101',
              name: 'Hosted entry',
              connector_type: 'openai-compatible',
              base_url: 'https://channel.example.test/v1',
              native_services: ['chat_completions', 'responses'],
              rerank_format: '',
              enabled: true,
            },
          ],
          discovery_source: null,
          model_mappings: [],
          connector_type: 'openai-compatible',
          base_url: 'https://channel.example.test/v1',
        },
      ],
    },
  });
  await page.route(`${USER_ORIGIN}/api/endpoints`, async (route) => {
    const request = route.request();
    if (request.method() !== 'POST') {
      await route.fallback();
      return;
    }
    postBody = request.postDataJSON() as Record<string, unknown>;
    await route.fulfill({
      status: 201,
      headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
      body: JSON.stringify(endpoint),
    });
  });
  await page.route(`${USER_ORIGIN}/api/endpoints/${endpointID}/keys`, async (route) => {
    expect(route.request().method()).toBe('POST');
    grantBody = route.request().postDataJSON() as Record<string, unknown>;
    await route.fulfill({
      status: 201,
      json: {
        ...endpointKeySummary('41', endpointID),
        credential_id: credential.id,
        credential_version: credential.credential_version,
        native_services: endpoint.native_services,
        max_concurrency: 0,
        max_rpm: 0,
      },
    });
  });

  await page.goto(`${USER_ORIGIN}/endpoints?connection_id=1`);
  await page.getByRole('button', { name: connectionCopy.addEntry, exact: true }).first().click();
  const wizard = page
    .locator('.nb-expandable-panel')
    .filter({ has: page.getByRole('heading', { name: connectionCopy.addEntry, exact: true }) });
  const mainstream = wizard.getByRole('radio', { name: connectionCopy.mainstream, exact: true });
  await mainstream.locator('..').click();
  await expect(mainstream).toBeChecked();
  await wizard
    .getByRole('combobox', { name: connectionCopy.mainstream, exact: true })
    .selectOption(MAINSTREAM_CHANNEL_ID);
  await wizard.locator('.nb-fold > summary').filter({ hasText: connectionCopy.entries }).click();
  await expect(wizard.getByRole('checkbox', { name: 'Hosted entry', exact: true })).toBeChecked();
  await expect(
    wizard.getByRole('textbox', { name: connectionCopy.entryURL, exact: true }),
  ).toHaveCount(0);
  await wizard
    .getByRole('row')
    .filter({ hasText: credential.note })
    .getByRole('button', { name: connectionCopy.select, exact: true })
    .click();
  await wizard.getByRole('checkbox', { name: connectionCopy.ownership, exact: true }).check();
  await wizard
    .getByRole('button', { name: userCopy.user.workflows.continueService, exact: true })
    .click();
  await expect(
    wizard.getByText(userCopy.user.workflows.serviceReady, { exact: true }),
  ).toBeVisible();
  expect(postBody).toEqual({
    source: 'mainstream',
    channel_id: MAINSTREAM_CHANNEL_ID,
    note: 'Hosted entry',
    enabled: true,
    connection_id: '1',
    channel_entry_id: '101',
    channel_revision: '2',
    native_services: ['chat_completions', 'responses', 'decisions', 'embeddings'],
    rerank_format: '',
  });
  expect(grantBody).toEqual({
    credential_id: credential.id,
    native_services: ['chat_completions', 'responses', 'decisions', 'embeddings'],
    discovery_source_id: null,
    note: '',
    enabled: true,
    ownership_confirmed: true,
    force_store_false: false,
    max_concurrency: 0,
    max_rpm: 0,
  });
  await assertClean(page, guard);
});

test.describe('donation expiry in UTC', () => {
  test.use({ timezoneId: 'UTC' });
  test('user charity overview reads numbered state summaries and key pages, and submits per-key expiry', async ({
    context,
    page,
  }) => {
    const guard = await prepare(context, page, 'user', 'user', 'en', 'dark');
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: `/api/time/resolve?${new URLSearchParams({ local: '2027-01-15T08:00:00', time_zone: 'UTC' })}`,
      body: {
        instant: 1_800_000_000,
        local: '2027-01-15T08:00:00',
        time_zone: 'UTC',
        offset_seconds: 0,
        adjustment: 'none',
      },
    });
    const available = endpointKey('11', 'available', { displayHead: 'key-available' });
    const blocked = endpointKey('12', 'disabled', { displayHead: 'key-blocked' });
    const ended = endpointKey('13', 'expired', {
      endpointKeyID: null,
      endedReason: 'expired',
      displayHead: 'key-ended',
    });
    const donationOne = userDonation('9', [available, blocked]);
    const donationTwo = userDonation('10', [ended], {
      status: 'expired',
      createdAt: 1_800_000_020,
      updatedAt: 1_800_000_030,
      reviewAt: 1_800_000_021,
    });
    const createdDonation = userDonation('11', [
      endpointKey('14', 'available', { displayHead: 'key-free' }),
    ]);
    let donationPostBody: Record<string, unknown> | null = null;
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/charity/models',
      body: {
        state: 'available',
        models: [
          {
            recurring_discount: {
              enabled: false,
              time_zone: 'UTC',
              default_percent: 100,
              periods: [],
            },
            effective_discount: { source: 'fixed', percent: 80, next_change_at: null },

            model_types: ['chat_completions', 'embeddings'],
            id: '7',
            provider: 'provider',
            model: 'charity',
            full_name: '[公益]provider/charity',
            pricing: {
              current_user_price_milli: '2400',
              current_user_prices_milli: null,

              mode: 'per_request',
              user_price_milli: '3000',
              discounted_user_price_milli: '2400',
              user_prices_milli: null,
              discounted_user_prices_milli: null,
            },
            discount: {
              enabled: true,
              percent: 80,
              start_at: 1_799_999_000,
              end_at: 1_800_003_600,
            },
          },
        ],
        donation_intake: 'open',
        server_now: 1_800_000_000,
      },
    });
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/charity/models?view=catalog&page=1&page_size=20&allowed_for_me=true&currently_available=true',
      body: catalogPage(
        [
          {
            recurring_discount: {
              enabled: false,
              time_zone: 'UTC',
              default_percent: 100,
              periods: [],
            },
            effective_discount: { source: 'fixed', percent: 80, next_change_at: null },

            model_types: ['chat_completions', 'embeddings'],
            id: '7',
            provider: 'provider',
            model: 'charity',
            full_name: '[公益]provider/charity',
            pricing: {
              current_user_price_milli: '2400',
              current_user_prices_milli: null,

              mode: 'per_request',
              user_price_milli: '3000',
              discounted_user_price_milli: '2400',
              user_prices_milli: null,
              discounted_user_prices_milli: null,
            },
            discount: {
              enabled: true,
              percent: 80,
              start_at: 1_799_999_000,
              end_at: 1_800_003_600,
            },
          },
        ],
        'open',
      ),
    });
    await page.route(`${USER_ORIGIN}/api/donations?*`, async (route) => {
      const url = new URL(route.request().url());
      const donations = [donationOne, donationTwo];
      const status = url.searchParams.get('status');
      const view = url.searchParams.get('view');
      return route.fulfill({
        json: numberedResponse(
          donations
            .filter((item) =>
              view === 'history' ? item.status === 'expired' : item.status !== 'expired',
            )
            .filter((item) => !status || item.status === status)
            .map(ownerDonationPageItem),
          '1',
          20,
        ),
      });
    });
    for (const donation of [donationOne, donationTwo]) {
      await mockJson(page, {
        origin: USER_ORIGIN,
        method: 'GET',
        path: `/api/donations/${donation.id}/keys?view=${donation.status === 'expired' ? 'ended' : 'current'}&page=1&page_size=20`,
        body: numberedResponse(
          donation.keys.map((value) => {
            const key = value as Record<string, unknown>;
            return {
              ...key,
              donation_id: donation.id,
              key_id: key.id,
              donation_revision: donation.revision,
              rule_count: '0',
              rules: [],
            };
          }),
          '1',
          20,
        ),
      });
    }
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/endpoints?page=1&page_size=20',
      body: numberedResponse([endpointSummary('20', '4')], '1', 20),
    });
    await page.route(`${USER_ORIGIN}/api/endpoints/20/keys?*`, async (route) =>
      route.fulfill({
        json: numberedResponse(
          [
            ...['11', '12', '13', '14'].map((id) => ({
              ...endpointKeySummary(id, '20', id === '14' ? 'key-free' : `key-${id}`),
              browse: {
                donation_eligibility:
                  id === '14' && !donationPostBody ? 'eligible' : 'already_donated',
                model_count: '0',
                binding_count: '0',
                available_binding_count: '0',
                preview: [],
                discovery: {
                  state: 'unknown',
                  revision: '1',
                  result: null,
                  safe_class: 'none',
                  observed_at: null,
                  count: null,
                },
              },
            })),
          ],
          '1',
          20,
        ),
      }),
    );
    await page.route(`${USER_ORIGIN}/api/donations`, async (route) => {
      const request = route.request();
      if (request.method() !== 'POST') {
        await route.fallback();
        return;
      }
      donationPostBody = request.postDataJSON() as Record<string, unknown>;
      await route.fulfill({
        status: 201,
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
        body: JSON.stringify(createdDonation),
      });
    });

    await page.goto(`${USER_ORIGIN}/charity`);
    await page.getByRole('button', { name: '[公益]provider/charity', exact: true }).click();
    const priceTable = page.getByRole('table', { name: 'Charity model prices' });
    await expect(priceTable).toBeVisible();
    await expect(priceTable.getByLabel('Original price: 3')).toBeVisible();
    await expect(priceTable.getByLabel('Current price: 2.4')).toBeVisible();
    await expect(
      page.locator('.charity-model-detail .charity-discount').getByText('20% off', { exact: true }),
    ).toBeVisible();
    await expect(priceTable.getByText('Donor reward')).toHaveCount(0);
    for (const width of [320, 390, 1440, 1935]) {
      await page.setViewportSize({ width, height: 1000 });
      await assertWorkbenchWidth(page, '.economy-page');
      await assertPricesFit(page);
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole('tab', { name: 'My donations', exact: true }).click();
    const donations = page.locator('.economy-donation-list');
    await expect(donations.locator('.economy-donation-summary')).toHaveCount(1);
    const first = donations
      .locator('.economy-donation-summary')
      .filter({ has: page.getByRole('heading', { name: 'Donation #9', exact: true }) });
    await expect(first.getByText('2 keys · 1 available', { exact: true })).toBeVisible();
    await first.locator('.nb-fold > summary').filter({ hasText: 'Sources and key status' }).click();
    await expect(first.locator('.economy-status-stack')).toContainText('Available · 1');
    await expect(first.locator('.economy-status-stack')).toContainText(
      'Charity services paused or not enabled · 1',
    );
    await first.getByRole('button', { name: 'Donation actions', exact: true }).click();
    await page.getByRole('menuitem', { name: 'View donated keys', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'key-available…tail' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'key-blocked…tail' })).toBeVisible();
    await page.getByRole('tab', { name: 'History', exact: true }).click();
    const filter = page.getByRole('combobox', { name: 'Review status', exact: true });
    await filter.selectOption('expired');
    await expect(donations.locator('.economy-donation-summary')).toHaveCount(1);
    await expect(donations.getByText('1 keys · 0 available', { exact: true })).toBeVisible();
    await donations
      .locator('.nb-fold > summary')
      .filter({ hasText: 'Sources and key status' })
      .click();
    await expect(donations.locator('.economy-status-stack')).toContainText('Expired · 1');
    await donations.getByRole('button', { name: 'Donation actions', exact: true }).click();
    await page.getByRole('menuitem', { name: 'View donated keys', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'key-ended…tail' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'key-available…tail' })).toHaveCount(0);
    await page.getByRole('tab', { name: 'Current donations', exact: true }).click();
    await expect(donations.locator('.economy-donation-summary')).toHaveCount(1);

    await page.getByRole('tab', { name: 'Donate my keys', exact: true }).click();
    const composer = page.locator('.economy-donation-composer');
    await expect(
      composer.getByRole('heading', { name: 'Submit a charity donation' }),
    ).toBeVisible();
    await composer.getByRole('button', { name: 'Choose keys…', exact: true }).click();
    const picker = page.getByRole('region', { name: 'Choose keys to donate', exact: true });
    const resources = picker.locator('.donation-resource-picker');
    await resources.getByRole('button', { name: /^Fixture endpoint / }).click();
    const keyChoices = resources.locator('.donation-resource-picker__section').nth(1);
    await expect(keyChoices.getByRole('checkbox', { name: /key-11…tail/ })).toBeDisabled();
    await keyChoices.getByRole('checkbox', { name: /key-free…tail/ }).check();
    await expect(
      picker.getByText(commonCopy.common.connections.shareExactVersion.replace('{{version}}', '1')),
    ).toBeVisible();
    await picker
      .locator('.nb-fold > summary')
      .filter({ hasText: userCopy.user.workflows.donationOptions })
      .click();
    await picker.getByLabel('Expiry for key-free…tail').fill('2027-01-15T08:00');
    await picker.getByRole('button', { name: 'Done', exact: true }).click();
    await composer
      .getByRole('checkbox', {
        name: 'I own every selected resource or have authorization to contribute its capacity.',
      })
      .check();
    await composer
      .getByRole('textbox', { name: 'Donation description' })
      .fill('Per-key expiry fixture');
    const publicThanks = composer
      .getByRole('radiogroup', { name: 'Accept a public Discord thank-you' })
      .getByRole('radio', { name: 'No', exact: true });
    await publicThanks.focus();
    await publicThanks.press('Space');
    await expect(publicThanks).toBeChecked();
    await composer.getByRole('button', { name: 'Submit for review' }).click();
    await expect(page.getByText('Donation submitted for review.')).toBeVisible();
    expect(donationPostBody).toMatchObject({
      description: 'Per-key expiry fixture',
      ownership_authorized: true,
      discord_public_thanks: false,
      keys: [
        {
          endpoint_key_id: '14',
          native_services: ['chat_completions', 'responses', 'decisions', 'embeddings'],
          credential_id: '14',
          credential_version: '1',
          grant_revision: '1',
          expires_at: 1_800_000_000,
        },
      ],
    });
    await assertClean(page, guard);
  });
});

for (const locale of ['en', 'zh'] as const) {
  test(`charity token prices remain fully readable on narrow screens (${locale})`, async ({
    context,
    page,
  }) => {
    const guard = await prepare(context, page, 'user', 'user', locale, 'dark');
    const session = userSession('user');
    session.user.lang = locale;
    for (const path of ['/api/session', '/api/me']) {
      await mockJson(page, { origin: USER_ORIGIN, method: 'GET', path, body: session });
    }
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/charity/models',
      body: {
        state: 'available',
        models: [
          {
            recurring_discount: {
              enabled: false,
              time_zone: 'UTC',
              default_percent: 100,
              periods: [],
            },
            effective_discount: { source: 'fixed', percent: 80, next_change_at: null },

            model_types: ['chat_completions', 'embeddings'],
            id: '7',
            provider: 'provider',
            model: 'charity',
            full_name: '[公益]provider/charity',
            pricing: {
              current_user_price_milli: null,
              current_user_prices_milli: {
                uncached_input: '8000000',
                cache_write_input: '8000000',
                cache_read_input: '1280000',
                output: '24000000',
              },

              mode: 'per_token',
              user_price_milli: null,
              discounted_user_price_milli: null,
              user_prices_milli: {
                uncached_input: '10000000',
                cache_write_input: '10000000',
                cache_read_input: '1600000',
                output: '30000000',
              },
              discounted_user_prices_milli: {
                uncached_input: '8000000',
                cache_write_input: '8000000',
                cache_read_input: '1280000',
                output: '24000000',
              },
            },
            discount: {
              enabled: true,
              percent: 80,
              start_at: 1_799_999_000,
              end_at: 1_800_003_600,
            },
          },
        ],
        donation_intake: 'closed',
        server_now: 1_800_000_000,
      },
    });
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/charity/models?view=catalog&page=1&page_size=20&allowed_for_me=true&currently_available=true',
      body: catalogPage(
        [
          {
            recurring_discount: {
              enabled: false,
              time_zone: 'UTC',
              default_percent: 100,
              periods: [],
            },
            effective_discount: { source: 'fixed', percent: 80, next_change_at: null },

            model_types: ['chat_completions', 'embeddings'],
            id: '7',
            provider: 'provider',
            model: 'charity',
            full_name: '[公益]provider/charity',
            pricing: {
              current_user_price_milli: null,
              current_user_prices_milli: {
                uncached_input: '8000000',
                cache_write_input: '8000000',
                cache_read_input: '1280000',
                output: '24000000',
              },

              mode: 'per_token',
              user_price_milli: null,
              discounted_user_price_milli: null,
              user_prices_milli: {
                uncached_input: '10000000',
                cache_write_input: '10000000',
                cache_read_input: '1600000',
                output: '30000000',
              },
              discounted_user_prices_milli: {
                uncached_input: '8000000',
                cache_write_input: '8000000',
                cache_read_input: '1280000',
                output: '24000000',
              },
            },
            discount: {
              enabled: true,
              percent: 80,
              start_at: 1_799_999_000,
              end_at: 1_800_003_600,
            },
          },
        ],
        'closed',
      ),
    });
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/donations?view=current&page=1&page_size=20',
      body: numberedResponse([], '1', 20),
    });
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/endpoints?page=1&page_size=20',
      body: numberedResponse([], '1', 20),
    });
    await page.goto(`${USER_ORIGIN}/charity`);
    await page.getByRole('button', { name: '[公益]provider/charity', exact: true }).click();
    await expect(
      page.getByRole('table', { name: locale === 'zh' ? '公益模型价格' : 'Charity model prices' }),
    ).toBeVisible();
    await expect(page.locator('.charity-price-table tbody tr')).toHaveCount(4);
    await expect(page.locator('.charity-price-table .charity-amount')).toHaveCount(8);
    for (const width of [320, 390, 768, 1935]) {
      await page.setViewportSize({ width, height: 900 });
      await assertPricesFit(page);
    }
    await assertClean(page, guard);
  });
}

test('user charity overview fails closed on an invalid numbered page and privacy states export and lineage retention', async ({
  context,
  page,
}) => {
  const guard = await prepare(context, page, 'user', 'user');
  const firstDonation = userDonation('9', [endpointKey('11', 'available')]);
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/charity/models',
    body: {
      state: 'no_models',
      models: [],
      donation_intake: 'closed',
      server_now: 1_800_000_000,
    },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/charity/models?view=catalog&page=1&page_size=20&allowed_for_me=true&currently_available=true',
    body: catalogPage([], 'closed'),
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/donations?view=current&page=1&page_size=20',
    body: {
      ...numberedResponse([ownerDonationPageItem(firstDonation)], '1', 20),
      next_cursor: 'unexpected-cursor',
    },
  });
  await page.goto(`${USER_ORIGIN}/charity`);
  await page.getByRole('tab', { name: 'My donations', exact: true }).click();
  await expect(
    page.getByText('The service returned an invalid response.', { exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();
  await page.goto(`${USER_ORIGIN}/privacy`);
  await expect(page.getByRole('heading', { name: 'Privacy policy' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Retention and deletion' })).toBeVisible();
  await expect(page.locator('body')).toContainText(
    'It excludes full keys, encrypted key content, sign-in tokens, raw diagnostics, source IP and client clues',
  );
  await expect(page.locator('body')).toContainText(
    'A new account using the same Discord identity cannot read or export old logs.',
  );
  await expect(page.locator('body')).toContainText(
    'Request logs and necessary sources remain until 30 days after completion or an applicable hold, including after self, administrator or inactivity deletion.',
  );
  await expect(page.locator('body')).toContainText('clears private account projections');
  await expect(page.locator('body')).toContainText('up to 90 days');
  await assertClean(page, guard);
});

test('administrator charity provenance grouping and report lineage expose safe donation facts with paging', async ({
  context,
  page,
}) => {
  const guard = await prepare(context, page, 'admin', 'admin', 'en');
  const source = {
    kind: 'mainstream',
    connector_type: 'openai-compatible',
    base_url: 'https://channel.example.test/v1',
    channel_id: MAINSTREAM_CHANNEL_ID,
    name: 'Hosted channel',
    channel_revision: '3',
    category: 'subscription',
  };
  const safeSource = {
    kind: 'mainstream',
    connector_type: source.connector_type,
    base_url: source.base_url,
    channel_id: source.channel_id,
    name: source.name,
  };
  const managedKey = {
    id: '31',
    binding_count: '0',
    idle: true,
    endpoint_key_id: '41',
    display_head: 'safe-head',
    display_tail: 'safe-tail',
    safe_source: safeSource,
    physical_enabled: true,
    charity_state: 'available',
    limits: { price: null, calls: '20', tokens: '1000' },
    usage: {
      price_used: '0',
      price_inflight: '0',
      calls_used: '0',
      calls_inflight: '0',
      tokens_used: '0',
      tokens_inflight: '0',
    },
    token_reserve: 32,
    authorized_expires_at: 1_800_003_600,
    expires_at: 1_800_003_600,
    failure_disable_threshold: '10',
    smart_backoff: null,
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    safe_note: 'Safe administrative note',
  };
  const managedDonation = {
    id: '41',
    status: 'approved',
    revision: '2',
    handling: {
      state: 'pending',
      revision: '1',
      processed_at: null,
      processed_by_role: null,
      closed_at: null,
      closed_reason: null,
    },
    description: 'Administrative fixture donation',
    review_result: { decision: 'approve', reason: 'Reviewed', reviewed_at: 1_800_000_001 },
    keys: [managedKey],
    owner: { user_id: '1', discord_id: '123456789', display_name: 'Fixture donor' },
    reviewer: { user_id: '2', role: 'admin' },
    created_at: 1_800_000_000,
    updated_at: 1_800_000_010,
  };
  const reportSummary = {
    id: REPORT_ID,
    status: 'pending_review',
    progress_state: 'complete',
    connector_type: 'openai-compatible',
    canonical_base_url: 'https://reported.example.test/v1',
    material_version: '3',
    target_version: '4',
    deadline: 1_800_086_400,
    counts: {
      materials: '0',
      targets: '1',
      distinct_owners: '1',
      processed: '0',
      deleted: '0',
      released: '0',
    },
    retry: null,
    created_at: 1_800_000_000,
    terminal_at: null,
  };
  const reportTarget = {
    id: REPORT_TARGET_ID,
    target_seq: '1',
    state: 'protected',
    endpoint_key_id: '41',
    key_ref: 'K'.repeat(43),
    owner: { user_id: '1', discord_id: '123456789', display_name: 'Fixture donor' },
    endpoint: {
      connector_type: 'openai-compatible',
      canonical_base_url: 'https://reported.example.test/v1',
      display_head: 'safe-head',
      display_tail: 'safe-tail',
    },
    discovered_version: '4',
    decided_version: null,
    donation_match_count: '2',
    created_at: 1_800_000_000,
    updated_at: 1_800_000_001,
  };
  const lineageFirst = {
    donation_id: '41',
    donation_key_id: '31',
    donation_status: 'approved',
    key_state: 'available',
    expires_at: 1_800_003_600,
    ended_reason: null,
    ended_at: null,
  };
  const lineageSecond = {
    donation_id: '42',
    donation_key_id: '32',
    donation_status: 'deleted',
    key_state: 'ended',
    expires_at: 1_800_003_000,
    ended_reason: 'account_deleted',
    ended_at: 1_800_000_100,
  };
  const sourceKey = `dsg_${'A'.repeat(43)}`;
  const sourceKeys = {
    source_key: sourceKey,
    safe_source: safeSource,
    donation_count: '1',
    key_count: '1',
    usable_key_count: '0',
    pending_donation_count: '1',
  };
  const managedKeyPage = {
    max_concurrency: 0,
    max_rpm: 0,
    ...managedKey,
    donation_id: '41',
    key_id: '31',
    donation_revision: '2',
    rule_count: '0',
    rules: [],
    handling: managedDonation.handling,
  };
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/donations?page=1&page_size=20',
    body: numberedResponse([], '1', 20),
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/donation-sources?scope=all&key_state=all&page=1&page_size=20',
    body: numberedResponse([sourceKeys], '1', 20),
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/donation-sources/${sourceKey}/keys?scope=all&key_state=all&page=1&page_size=20`,
    body: numberedResponse([managedKeyPage], '1', 20),
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/reports/${REPORT_ID}?materials_page=1&materials_page_size=20`,
    body: {
      ...reportSummary,
      materials: { data: [], next_cursor: null },
      materials_pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
      decision: null,
    },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/reports/${REPORT_ID}/targets?page=1&page_size=20`,
    body: numberedResponse([reportTarget], '1', 20),
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/reports/${REPORT_ID}/targets/${REPORT_TARGET_ID}/donations?page=1&page_size=20`,
    body: numberedResponse(
      [
        lineageFirst,
        ...Array.from({ length: 19 }, (_, index) => ({
          ...lineageFirst,
          donation_id: String(100 + index),
          donation_key_id: String(200 + index),
        })),
      ],
      '1',
      20,
      21,
    ),
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/reports/${REPORT_ID}/targets/${REPORT_TARGET_ID}/donations?page=2&page_size=20`,
    body: numberedResponse([lineageSecond], '2', 20, 21),
  });

  await page.goto(`${ADMIN_ORIGIN}/charity`);
  await page.getByRole('tab', { name: 'Browse by source' }).click();
  await page.getByRole('button', { name: /Hosted channel/ }).click();
  const sourceRow = page.locator('.charity-source-browser__source');
  await expect(sourceRow).toContainText('Hosted channel');
  await expect(sourceRow).toContainText('https://channel.example.test/v1');
  await expect(sourceRow).toContainText('openai-compatible');
  const keyRow = page.locator('.charity-source-browser__key');
  await expect(keyRow.getByRole('heading', { name: 'safe-head…safe-tail' })).toBeVisible();
  await expect(keyRow.getByText('Safe administrative note', { exact: true })).toHaveCount(0);
  await page.goto(`${ADMIN_ORIGIN}/reports/${REPORT_ID}`);
  const targetRow = page.getByRole('row').filter({ hasText: 'safe-head…safe-tail' });
  await expect(targetRow).toContainText('2');
  await expect(page.getByRole('button', { name: 'Open lineage' })).toBeVisible();
  await page.getByRole('button', { name: 'Open lineage' }).click();
  const lineageCard = page.locator('section.card').filter({
    has: page.getByRole('heading', { name: 'Donation lineage' }),
  });
  await expect(lineageCard.getByText('41', { exact: true })).toBeVisible();
  await lineageCard.getByRole('button', { name: 'Next' }).click();
  await expect(lineageCard.getByText('42', { exact: true })).toBeVisible();
  await assertClean(page, guard);
});
