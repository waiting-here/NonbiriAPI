import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { expect, test } from './test';

type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type Page = Parameters<typeof collectConsoleViolations>[0];

const EPHEMERAL_MARKER = 'dec020-e2e-ephemeral-marker';
const MAINSTREAM_CHANNEL_ID = `mch_${'C'.repeat(21)}A`;
const REPORT_ID = `rpc_${'R'.repeat(21)}A`;
const REPORT_TARGET_ID = `rpt_${'T'.repeat(21)}A`;

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
  return consoleGuard;
}

async function assertClean(page: Page, consoleGuard: ReturnType<typeof collectConsoleViolations>) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
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

test('administrator mainstream channel CRUD keeps the channel authority and retirement state visible', async ({
  context,
  page,
}) => {
  const guard = await prepare(context, page, 'admin', 'admin', 'en', 'dark');
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
        body: JSON.stringify({ data: include ? [channel] : [], next_cursor: null }),
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
      channel = {
        id: MAINSTREAM_CHANNEL_ID,
        name: String(input.name ?? ''),
        category: input.category,
        connector_type: input.connector_type,
        base_url: input.base_url,
        enabled: input.enabled,
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
      channel = {
        ...channel,
        ...body,
        revision: '2',
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
          revision: '3',
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
  await expect(page.getByRole('heading', { name: 'Mainstream channel authority' })).toBeVisible();
  await page.getByLabel('Channel name').fill('Fixture channel');
  await page.getByLabel('Category').selectOption('api_platform');
  await page.getByLabel('Connector').selectOption('anthropic-compatible');
  await page.getByLabel('Canonical base URL').fill('https://channel.example.test/v1');
  await page.getByRole('button', { name: 'Create channel' }).click();

  await expect(page.getByRole('heading', { name: 'Channel authority' })).toBeVisible();
  expect(requests.find((request) => request.method === 'POST')?.body).toEqual({
    name: 'Fixture channel',
    category: 'api_platform',
    connector_type: 'anthropic-compatible',
    base_url: 'https://channel.example.test/v1',
    enabled: true,
  });
  const editName = page.getByLabel('Channel name').nth(1);
  await editName.fill('Renamed channel');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByText('Renamed channel')).toBeVisible();
  expect(requests.find((request) => request.method === 'PATCH')?.body).toMatchObject({
    expected_revision: '1',
    name: 'Renamed channel',
  });

  await page.getByRole('button', { name: 'Retire channel' }).click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Retire channel' }).click();
  await expect(page.getByText('Retired channels are immutable')).toBeVisible();
  expect(requests.find((request) => request.method === 'DELETE')?.body).toEqual({
    expected_revision: '2',
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
    created_at: 1_800_000_000,
    updated_at: 1_800_000_000,
  };
  let postBody: Record<string, unknown> | null = null;
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoints?limit=50',
    body: { data: [], next_cursor: null },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoint-create-options',
    body: {
      base_connector_types: ['openai-compatible', 'anthropic-compatible'],
      mainstream_channels: [
        {
          id: MAINSTREAM_CHANNEL_ID,
          name: 'Hosted channel',
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

  await page.goto(`${USER_ORIGIN}/endpoints`);
  await page.getByRole('button', { name: 'Create endpoint' }).first().click();
  const wizard = page.locator('section.core-wizard');
  await expect(wizard.getByRole('heading', { name: 'Create endpoint and key' })).toBeVisible();
  await expect(wizard.getByRole('button', { name: 'Mainstream channel' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await wizard.getByRole('button', { name: 'Next' }).click();
  await expect(wizard.getByLabel('Service address')).toHaveValue(
    'https://channel.example.test/v1',
  );
  await expect(wizard.getByLabel('Service address')).toHaveAttribute('readonly');
  await wizard.getByLabel('Note').fill('Selected channel endpoint');
  await wizard.getByRole('button', { name: 'Create endpoint' }).click();
  await expect(page.getByLabel('Service key')).toBeVisible();
  expect(postBody).toEqual({
    source: 'mainstream',
    channel_id: MAINSTREAM_CHANNEL_ID,
    note: 'Selected channel endpoint',
    enabled: true,
  });
  await assertClean(page, guard);
});

test('user charity overview loads all cursor pages, filters each key state, and submits per-key expiry', async ({
  context,
  page,
}) => {
  const guard = await prepare(context, page, 'user', 'user', 'en', 'dark');
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
        { id: '7', provider: 'provider', model: 'charity', full_name: '[公益]provider/charity' },
      ],
      donation_intake: 'open',
    },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/donations?limit=100',
    body: { data: [donationOne], next_cursor: 'donation-next' },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/donations?limit=100&cursor=donation-next',
    body: { data: [donationTwo], next_cursor: null },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoints?limit=100',
    body: { data: [endpointSummary('20', '4')], next_cursor: null },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoints/20/keys?limit=100',
    body: {
      data: [
        endpointKeySummary('11', '20'),
        endpointKeySummary('12', '20'),
        endpointKeySummary('13', '20'),
        endpointKeySummary('14', '20', 'key-free'),
      ],
      next_cursor: null,
    },
  });
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
  await expect(page.getByRole('heading', { name: 'My donated keys' })).toBeVisible();
  await expect(page.getByText('Showing 3 of 3 keys')).toBeVisible();
  const filter = page.getByLabel('Key status');
  await filter.selectOption('available');
  await expect(page.getByText('Showing 1 of 3 keys')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'key-available…tail' })).toBeVisible();
  await filter.selectOption('blocked');
  await expect(page.getByText('Showing 1 of 3 keys')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'key-blocked…tail' })).toBeVisible();
  await filter.selectOption('ended');
  await expect(page.getByText('Showing 1 of 3 keys')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'key-ended…tail' })).toBeVisible();
  await filter.selectOption('all');

  const composer = page.locator('.economy-donation-composer');
  await expect(composer.getByRole('heading', { name: 'Submit a charity donation' })).toBeVisible();
  await composer.getByLabel(/key-free…tail/).check();
  await composer.getByLabel('Expiry for key-free…tail').fill('2027-01-15T08:00');
  await composer
    .getByRole('checkbox', {
      name: 'I own every selected resource or have authorization to contribute its capacity.',
    })
    .check();
  await composer
    .getByRole('textbox', { name: 'Donation description' })
    .fill('Per-key expiry fixture');
  await composer.getByRole('button', { name: 'Submit for review' }).click();
  await expect(page.getByText('Donation submitted for review.')).toBeVisible();
  expect(donationPostBody).toMatchObject({
    description: 'Per-key expiry fixture',
    ownership_authorized: true,
    keys: [{ endpoint_key_id: '14', expires_at: 1_800_000_000 }],
  });
  await assertClean(page, guard);
});

test('user charity overview fails closed on a cursor page and privacy states export and lineage retention', async ({
  context,
  page,
}) => {
  const guard = await prepare(context, page, 'user', 'user');
  const firstDonation = userDonation('9', [endpointKey('11', 'available')]);
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/charity/models',
    body: { state: 'no_models', models: [], donation_intake: 'closed' },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/donations?limit=100',
    body: { data: [firstDonation], next_cursor: 'missing-page' },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/donations?limit=100&cursor=missing-page',
    body: { data: [], next_cursor: 99 },
  });
  await page.goto(`${USER_ORIGIN}/charity`);
  await expect(
    page.getByRole('heading', { name: 'The donated-key overview is incomplete' }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();
  await page.goto(`${USER_ORIGIN}/privacy`);
  await expect(page.getByRole('heading', { name: 'Privacy policy' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Retention and deletion' })).toBeVisible();
  await expect(page.locator('body')).toContainText('Export version 4');
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
  const managedKey = {
    id: '31',
    endpoint_key_id: '41',
    display_head: 'safe-head',
    display_tail: 'safe-tail',
    safe_source: source,
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
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    safe_note: 'Safe administrative note',
  };
  const managedDonation = {
    id: '41',
    status: 'approved',
    revision: '2',
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
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/donations?limit=50',
    body: { data: [managedDonation], next_cursor: null },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/reports/${REPORT_ID}?materials_limit=50`,
    body: { ...reportSummary, materials: { data: [], next_cursor: null }, decision: null },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/reports/${REPORT_ID}/targets?limit=50`,
    body: { data: [reportTarget], next_cursor: null },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/reports/${REPORT_ID}/targets/${REPORT_TARGET_ID}/donations?limit=50`,
    body: { data: [lineageFirst], next_cursor: 'lineage-next' },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/reports/${REPORT_ID}/targets/${REPORT_TARGET_ID}/donations?cursor=lineage-next&limit=50`,
    body: { data: [lineageSecond], next_cursor: null },
  });

  await page.goto(`${ADMIN_ORIGIN}/charity`);
  await expect(page.getByText('【Mainstream subscription】Hosted channel')).toBeVisible();
  await expect(page.getByText('safe-head…safe-tail')).toBeVisible();
  await expect(page.getByText('Safe administrative note')).toHaveCount(0);
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
