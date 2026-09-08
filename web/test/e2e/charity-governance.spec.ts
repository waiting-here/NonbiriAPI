import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  assertResponsiveOperationTables,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { numberedResponse } from './numbered-fixtures';

const NOW = 1_800_000_000;
const EVIDENCE_DIR = process.env.NONBIRI_VISUAL_DIR
  ? resolve(process.env.NONBIRI_VISUAL_DIR)
  : null;
const ADMIN_MARKER = 'governance-admin-ephemeral-7c4a1f9e';
const CATALOG_MARKER = 'governance-catalog-ephemeral-2d8b6f4a';
const STEWARD_MARKER = 'governance-steward-ephemeral-5e9c3b7a';
const REQUEST_ID = `req_${'A'.repeat(21)}Q`;
const DISCORD_ID = '1'.repeat(18);
const CALLER_NICKNAME = 'Ada Example';
const DONATION_STATES = [
  'available',
  'pending',
  'disabled',
  'suspended',
  'exhausted',
  'expired',
  'ended',
] as const;

type JSONRecord = Record<string, unknown>;
type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type RouteLike = {
  fulfill(options: {
    status: number;
    headers: Record<string, string>;
    body: string;
  }): Promise<void>;
};

async function saveScreenshot(page: Page, name: string, selector?: string): Promise<void> {
  if (!EVIDENCE_DIR) return;
  await mkdir(EVIDENCE_DIR, { recursive: true });
  const path = resolve(EVIDENCE_DIR, `${name}.png`);
  if (selector) {
    await page
      .locator(selector)
      .first()
      .evaluate((element) => element.scrollIntoView({ block: 'center' }));
    await page.locator(selector).first().screenshot({ path });
  } else {
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({ path, fullPage: true });
  }
}

async function fulfillJSON(route: RouteLike, value: unknown, status = 200): Promise<void> {
  await route.fulfill({
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: JSON.stringify(value),
  });
}

async function prepareStation(
  context: BrowserContext,
  page: Page,
  station: 'admin' | 'user',
  role: 'admin' | 'user' | 'level5',
  locale: 'en' | 'zh',
  theme: 'light' | 'dark',
  width: number,
  forbiddenToken: string,
) {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [forbiddenToken]);
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(
    ({ locale: initialLocale, theme: initialTheme }) => {
      localStorage.setItem('nb.lang', initialLocale);
      localStorage.setItem('nb.theme', initialTheme);
    },
    { locale, theme },
  );
  await mockPublicConfig(page, station);
  await mockRoleSession(page, station, role);
  return { consoleGuard, forbiddenToken, locale, theme };
}

async function assertPagePresentation(
  page: Page,
  setup: Awaited<ReturnType<typeof prepareStation>>,
): Promise<void> {
  await expect(page.locator('html')).toHaveAttribute(
    'lang',
    setup.locale === 'zh' ? 'zh-CN' : 'en',
  );
  await expect(page.locator('html')).toHaveAttribute('data-theme', setup.theme);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await assertNoSensitiveBrowserPersistence(page, [setup.forbiddenToken]);
  setup.consoleGuard.assertNone();
}

function managedKey(id: string, state: 'pending' | 'available' = 'pending'): JSONRecord {
  return {
    id,
    binding_count: '0',
    idle: true,
    endpoint_key_id: '21',
    display_head: 'sk-live',
    display_tail: 'fixture',
    safe_source: {
      kind: 'custom',
      connector_type: 'openai-compatible',
      base_url: 'https://donor.example.test/v1',
    },
    physical_enabled: true,
    charity_state: state,
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
    authorized_expires_at: null,
    expires_at: null,
    streak: { generation: '1', count: '0', failure_disabled: false },
    ended_reason: null,
    safe_note: 'Synthetic reviewer note',
    max_concurrency: 4,
    max_rpm: 60,
  };
}

function managedKeyPageItem(
  id: string,
  donation: JSONRecord,
  state: 'pending' | 'available' = 'pending',
): JSONRecord {
  return {
    ...managedKey(id, state),
    donation_id: donation.id,
    key_id: id,
    donation_revision: donation.revision,
    rule_count: '0',
    rules: [],
    handling: donation.handling,
  };
}

function donationHandling(state: 'pending' | 'processed', revision: string): JSONRecord {
  return {
    state,
    revision,
    processed_at: state === 'processed' ? NOW + 120 : null,
    processed_by_role: state === 'processed' ? 'admin' : null,
    closed_at: null,
    closed_reason: null,
  };
}

function adminDonation(): JSONRecord {
  return {
    id: '7',
    status: 'pending',
    revision: '7',
    handling: donationHandling('pending', '7'),
    description: 'Synthetic pending donation',
    review_result: null,
    keys: [managedKey('11')],
    owner: { user_id: '42', discord_id: null, display_name: 'Synthetic donor' },
    reviewer: null,
    created_at: NOW,
    updated_at: NOW,
  };
}

function stewardDonation(): JSONRecord {
  return {
    id: '8',
    status: 'approved',
    revision: '3',
    handling: donationHandling('pending', '4'),
    description: 'Another donor shared resource',
    review_result: { decision: 'approve', reason: 'Synthetic approval', reviewed_at: NOW },
    keys: [managedKey('12', 'available')],
    owner: null,
    reviewer: { user_id: null, role: 'admin' },
    created_at: NOW - 60,
    updated_at: NOW,
  };
}

function donationPageItem(donation: JSONRecord, role: 'admin' | 'steward'): JSONRecord {
  const keys = Array.isArray(donation.keys)
    ? donation.keys.filter(
        (key): key is JSONRecord => key !== null && typeof key === 'object' && !Array.isArray(key),
      )
    : [];
  const stateCounts = Object.fromEntries(
    DONATION_STATES.map((state) => [state, '0']),
  ) as JSONRecord;
  const sources: JSONRecord[] = [];
  for (const key of keys) {
    if (typeof key.charity_state === 'string' && Object.hasOwn(stateCounts, key.charity_state)) {
      stateCounts[key.charity_state] = String(Number(stateCounts[key.charity_state]) + 1);
    }
    if (
      key.safe_source !== null &&
      typeof key.safe_source === 'object' &&
      !Array.isArray(key.safe_source) &&
      !sources.some((source) => JSON.stringify(source) === JSON.stringify(key.safe_source))
    ) {
      sources.push(key.safe_source as JSONRecord);
    }
  }
  const owner = donation.owner;
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
    handling: donation.handling,
    reviewer: donation.reviewer ?? null,
    owner:
      owner === null || typeof owner !== 'object' || Array.isArray(owner)
        ? null
        : role === 'admin'
          ? owner
          : {
              user_id: (owner as JSONRecord).user_id,
              display_name: (owner as JSONRecord).display_name,
            },
  };
}

function capabilityModel(model: string): JSONRecord {
  return {
    id: '1',
    provider: 'provider',
    model,
    full_name: `[公益]provider/${model}`,
    pricing: {
      mode: 'per_request',
      user_price_milli: '3000',
      discounted_user_price_milli: '2400',
      user_prices_milli: null,
      discounted_user_prices_milli: null,
    },
    discount: { enabled: true, percent: 80, start_at: NOW - 60, end_at: NOW + 3_600 },
  };
}

function catalogModel(id: string, model: string, overrides: JSONRecord = {}): JSONRecord {
  return {
    ...capabilityModel(model),
    id,
    public_description: `Public description for ${model}`,
    enabled: true,
    allowed_levels: [1, 3, 5],
    level_allowed: true,
    currently_available: true,
    availability: 'available',
    ...overrides,
  };
}

function catalogPage(
  models: JSONRecord[],
  page: number,
  pageSize: number,
  totalItems: number,
): JSONRecord {
  return {
    models,
    pagination: {
      page: String(page),
      page_size: pageSize,
      total_items: String(totalItems),
      total_pages: String(Math.max(1, Math.ceil(totalItems / pageSize))),
    },
    donation_intake: 'open',
    server_now: NOW,
  };
}

test('admin pending badge opens the shared queue and processing survives refresh', async ({
  context,
  page,
}) => {
  const setup = await prepareStation(
    context,
    page,
    'admin',
    'admin',
    'en',
    'light',
    1_280,
    ADMIN_MARKER,
  );
  let current = adminDonation();
  let processed = false;
  const listReads: string[] = [];
  const detailReads: string[] = [];
  const donationKeyReads: string[] = [];
  const processRequests: Array<{ body: JSONRecord; idempotencyKey: string }> = [];
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== ADMIN_ORIGIN || url.pathname !== '/admin/api/donations') {
      await route.fallback();
      return;
    }
    if (request.method() === 'GET') {
      listReads.push(url.search);
      const filteredOut = url.searchParams.get('handling') === 'pending' && processed;
      await fulfillJSON(
        route,
        numberedResponse(
          filteredOut ? [] : [donationPageItem(current, 'admin')],
          url.searchParams.get('page') ?? '1',
          Number(url.searchParams.get('page_size') ?? '20'),
        ),
      );
      return;
    }
    await route.fallback();
  });
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== ADMIN_ORIGIN || !url.pathname.startsWith('/admin/api/donations/')) {
      await route.fallback();
      return;
    }
    if (request.method() === 'GET' && url.pathname === '/admin/api/donations/badge') {
      await fulfillJSON(route, { pending_count: processed ? '0' : '1', server_now: NOW });
      return;
    }
    if (request.method() === 'GET' && url.pathname === '/admin/api/donations/7') {
      detailReads.push(url.search);
      await fulfillJSON(route, current);
      return;
    }
    if (request.method() === 'GET' && url.pathname === '/admin/api/donations/7/keys') {
      if (url.searchParams.get('page') !== '1' || url.searchParams.get('page_size') !== '20') {
        throw new Error(`Unexpected donation-key page request: ${request.method()} ${url.href}`);
      }
      donationKeyReads.push(url.search);
      await fulfillJSON(
        route,
        numberedResponse(
          [managedKeyPageItem('11', current)],
          url.searchParams.get('page') ?? '1',
          Number(url.searchParams.get('page_size') ?? '20'),
        ),
      );
      return;
    }
    if (
      request.method() === 'POST' &&
      url.pathname === '/admin/api/donations/7/handling/processed'
    ) {
      const rawBody = request.postData() ?? '{}';
      processRequests.push({
        body: JSON.parse(rawBody) as JSONRecord,
        idempotencyKey: request.headers()['idempotency-key'] ?? '',
      });
      current = {
        ...current,
        handling: donationHandling('processed', '8'),
      };
      processed = true;
      await fulfillJSON(route, { donation_id: '7', handling: current.handling });
      return;
    }
    await route.fallback();
  });

  await page.goto(`${ADMIN_ORIGIN}/charity`);
  const badge = page.getByRole('link', {
    name: '1 donations awaiting shared follow-up',
    exact: true,
  });
  await expect(badge).toBeVisible();
  await badge.click();
  await expect(page).toHaveURL(`${ADMIN_ORIGIN}/charity?handling=pending`);
  await expect.poll(() => listReads.length).toBeGreaterThanOrEqual(2);
  await expect(
    page.locator('.status-badge').filter({ hasText: 'Pending follow-up' }).first(),
  ).toBeVisible();
  await expect(page.getByText('Synthetic pending donation', { exact: true })).toBeVisible();
  expect(listReads).toContain('?page=1&page_size=20');
  expect(listReads).toContain('?handling=pending&page=1&page_size=20');
  await saveScreenshot(page, 'admin-pending-queue-1280-light-en');

  await page.getByRole('button', { name: 'Review', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Donation #7', exact: true })).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Key 11 · sk-live…fixture', exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Mark as processed', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Mark as processed', exact: true }).click();
  await expect.poll(() => processRequests.length).toBe(1);
  await expect.poll(() => listReads.length).toBeGreaterThanOrEqual(3);
  await expect.poll(() => detailReads.length).toBeGreaterThanOrEqual(2);
  await expect.poll(() => donationKeyReads.length).toBeGreaterThanOrEqual(2);
  expect(processRequests[0]).toEqual({
    body: { expected_handling_revision: '7' },
    idempotencyKey: expect.stringMatching(/^[A-Za-z0-9_-]{22,128}$/),
  });
  await expect(
    page.locator('.status-badge').filter({ hasText: 'Processed' }).first(),
  ).toBeVisible();
  await expect(page.getByText(/Processed by an administrator/)).toBeVisible();
  await expect(
    page.getByRole('link', {
      name: '0 donations awaiting shared follow-up',
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Review', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Mark as processed', exact: true })).toHaveCount(0);
  expect(current.revision).toBe('7');
  expect(current.updated_at).toBe(NOW);

  await page.setViewportSize({ width: 320, height: 900 });
  await assertResponsiveOperationTables(page);
  expect(
    await page
      .locator('.ops-subcard h4')
      .evaluateAll((headings) =>
        headings.every((heading) => heading.scrollWidth <= heading.clientWidth + 1),
      ),
  ).toBe(true);
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await saveScreenshot(page, 'admin-processed-320-light-en');
  await page.reload();
  await expect(page).toHaveURL(`${ADMIN_ORIGIN}/charity?handling=pending&donation_id=7`);
  await expect(page.getByRole('combobox', { name: 'Follow-up status', exact: true })).toHaveValue(
    'pending',
  );
  await expect(page.getByRole('heading', { name: 'Donation #7', exact: true })).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Key 11 · sk-live…fixture', exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/Processed by an administrator/)).toBeVisible();
  await expect(page.getByRole('button', { name: 'Review', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Return to list', exact: true }).click();
  await expect(page).toHaveURL(`${ADMIN_ORIGIN}/charity?handling=pending`);
  await expect(page.getByRole('combobox', { name: 'Follow-up status', exact: true })).toHaveValue(
    'pending',
  );
  await expect(page.getByText('No donations', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Donation #7', exact: true })).toHaveCount(0);
  await page.getByRole('combobox', { name: 'Follow-up status', exact: true }).selectOption('');
  await expect(page.getByText('Synthetic pending donation', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Review', exact: true }).click();
  await expect(page.getByText(/Processed by an administrator/)).toBeVisible();
  expect(processRequests).toHaveLength(1);
  expect(donationKeyReads).toContain('?page=1&page_size=20');
  await assertPagePresentation(page, setup);
});

test('user catalog searches, filters levels, paginates, and expands plain descriptions', async ({
  context,
  page,
}) => {
  const setup = await prepareStation(
    context,
    page,
    'user',
    'user',
    'zh',
    'dark',
    320,
    CATALOG_MARKER,
  );
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/donations?page=1&page_size=20',
    body: numberedResponse([], '1', 20),
  });
  const firstModels = Array.from({ length: 20 }, (_, index) =>
    catalogModel(
      String(index + 1),
      index === 0 ? 'plain' : `model-${index + 1}`,
      index === 0 ? { public_description: '<b>plain</b>\nsecond line' } : {},
    ),
  );
  const deniedModel = catalogModel('22', 'denied', {
    public_description: 'Needle public description',
    allowed_levels: [2, 4],
    level_allowed: false,
    availability: 'level_denied',
  });
  const catalogRequests: string[] = [];
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== USER_ORIGIN || url.pathname !== '/api/charity/models') {
      await route.fallback();
      return;
    }
    if (request.method() !== 'GET') {
      await route.fallback();
      return;
    }
    if (url.searchParams.get('view') !== 'catalog') {
      await fulfillJSON(route, {
        state: 'available',
        donation_intake: 'open',
        server_now: NOW,
        models: [capabilityModel('plain')],
      });
      return;
    }
    catalogRequests.push(`${url.pathname}${url.search}`);
    const pageNumber = url.searchParams.get('page') ?? '';
    const pageSize = Number(url.searchParams.get('page_size') ?? '0');
    const query = url.searchParams.get('q') ?? '';
    const access = url.searchParams.get('allowed_for_me') ?? '';
    if (query === 'needle' && access === 'false') {
      await fulfillJSON(route, catalogPage([deniedModel], 1, pageSize, 1));
      return;
    }
    if (query === 'needle') {
      await fulfillJSON(
        route,
        catalogPage(
          [catalogModel('21', 'needle', { public_description: 'Needle description' })],
          1,
          pageSize,
          1,
        ),
      );
      return;
    }
    if (pageNumber === '2') {
      await fulfillJSON(route, catalogPage([catalogModel('21', 'page-two')], 2, pageSize, 21));
      return;
    }
    await fulfillJSON(route, catalogPage(firstModels, 1, pageSize, 21));
  });

  await page.goto(`${USER_ORIGIN}/charity`);
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  const firstCard = page.locator('.economy-catalog-item').first();
  await expect(firstCard).toBeVisible();
  await expect(firstCard).toContainText('<b>plain</b>');
  expect(await firstCard.locator('b').count()).toBe(0);
  await expect(firstCard.getByText('L1, L3, L5', { exact: true })).toBeVisible();
  await expect(firstCard.getByText('本等级允许', { exact: true })).toBeVisible();
  await expect(firstCard.getByText('当前可用', { exact: true })).toBeVisible();
  const priceTable = firstCard.getByRole('table', { name: '公益模型价格', exact: true });
  await expect(priceTable).toBeVisible();
  await expect(priceTable.locator('[aria-label="原价: 3"]')).toBeVisible();
  await expect(priceTable.locator('[aria-label="优惠价: 2.4"]')).toBeVisible();
  const toggle = firstCard.locator('.economy-catalog-item__description-toggle');
  await expect(toggle).toHaveAccessibleName('展开完整说明');
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(toggle).toHaveAccessibleName('收起说明');
  await expect(firstCard.getByRole('button', { name: '复制模型名称', exact: true })).toBeVisible();
  await saveScreenshot(page, 'catalog-expanded-320-dark-zh', '.economy-catalog-item');

  await page.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(page.getByText('[公益]provider/page-two', { exact: true })).toBeVisible();
  const search = page.getByRole('searchbox');
  const requestCountBeforeTyping = catalogRequests.length;
  await search.fill('needle');
  expect(catalogRequests).toHaveLength(requestCountBeforeTyping);
  await search.press('Enter');
  await expect(page.getByText('[公益]provider/needle', { exact: true })).toBeVisible();
  expect(catalogRequests).toContain(
    '/api/charity/models?view=catalog&page=2&page_size=20&allowed_for_me=true&currently_available=true',
  );
  expect(catalogRequests).toContain(
    '/api/charity/models?view=catalog&page=1&page_size=20&q=needle&allowed_for_me=true&currently_available=true',
  );

  await page.getByRole('combobox', { name: '本人访问权限', exact: true }).selectOption('false');
  await expect(page.getByText('[公益]provider/denied', { exact: true })).toBeVisible();
  await expect(page.locator('.economy-catalog-item').first().locator('dd').nth(1)).toHaveText(
    '本等级不允许',
  );
  await expect(page.getByText('本人等级不允许', { exact: true })).toBeVisible();
  expect(catalogRequests).toContain(
    '/api/charity/models?view=catalog&page=1&page_size=20&q=needle&allowed_for_me=false&currently_available=true',
  );

  await page.getByRole('combobox', { name: '每页条数', exact: true }).selectOption('50');
  await expect(page.getByRole('combobox', { name: '每页条数', exact: true })).toHaveValue('50');
  await expect.poll(() => catalogRequests.length).toBeGreaterThanOrEqual(4);
  expect(catalogRequests).toContain(
    '/api/charity/models?view=catalog&page=1&page_size=50&q=needle&allowed_for_me=false&currently_available=true',
  );
  await page.setViewportSize({ width: 1_280, height: 900 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await saveScreenshot(page, 'catalog-filtered-1280-dark-zh');
  await assertPagePresentation(page, setup);
});

test('level-five stewardship hides another donor and shows caller identity safely', async ({
  context,
  page,
}) => {
  const setup = await prepareStation(
    context,
    page,
    'user',
    'level5',
    'en',
    'dark',
    320,
    STEWARD_MARKER,
  );
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: USER_ORIGIN });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/steward/donations/badge',
    body: { pending_count: '1', server_now: NOW },
  });

  const currentDonation = stewardDonation();
  const donationListReads: string[] = [];
  const donationDetailReads: string[] = [];
  const donationKeyReads: string[] = [];
  const logListReads: string[] = [];
  const logDetailReads: string[] = [];
  const usage = {
    uncached_input_tokens: '0',
    cache_write_input_tokens: '0',
    cache_read_input_tokens: '0',
    output_tokens: '1',
    total_tokens: '1',
    usage_unknown: false,
    charge: '0',
  };
  const logRow: JSONRecord = {
    id: REQUEST_ID,
    route_kind: 'charity_chat_completions',
    caller_result_class: 'success',
    caller_status: 200,
    caller_error_code: null,
    started_at: NOW,
    completed_at: NOW + 1,
    usage,
    caller_identity: { discord_nickname: CALLER_NICKNAME, discord_id: DISCORD_ID },
    attempt_count: '1',
  };

  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== USER_ORIGIN) {
      await route.fallback();
      return;
    }
    if (request.method() === 'GET' && url.pathname === '/api/steward/donations') {
      donationListReads.push(url.search);
      await fulfillJSON(
        route,
        numberedResponse(
          [donationPageItem(currentDonation, 'steward')],
          url.searchParams.get('page') ?? '1',
          Number(url.searchParams.get('page_size') ?? '20'),
        ),
      );
      return;
    }
    if (request.method() === 'GET' && url.pathname === '/api/steward/donations/8') {
      donationDetailReads.push(url.search);
      await fulfillJSON(route, currentDonation);
      return;
    }
    if (request.method() === 'GET' && url.pathname === '/api/steward/donations/8/keys') {
      if (url.searchParams.get('page') !== '1' || url.searchParams.get('page_size') !== '20') {
        throw new Error(`Unexpected donation-key page request: ${request.method()} ${url.href}`);
      }
      donationKeyReads.push(url.search);
      await fulfillJSON(
        route,
        numberedResponse(
          [managedKeyPageItem('12', currentDonation, 'available')],
          url.searchParams.get('page') ?? '1',
          Number(url.searchParams.get('page_size') ?? '20'),
        ),
      );
      return;
    }
    if (request.method() === 'GET' && url.pathname === '/api/steward/logs') {
      logListReads.push(url.search);
      await fulfillJSON(
        route,
        numberedResponse(
          [logRow],
          url.searchParams.get('page') ?? '1',
          Number(url.searchParams.get('page_size') ?? '20'),
        ),
      );
      return;
    }
    if (request.method() === 'GET' && url.pathname === `/api/steward/logs/${REQUEST_ID}`) {
      logDetailReads.push(url.search);
      await fulfillJSON(route, {
        request: logRow,
        attempts: { data: [], next_cursor: null },
        attempt_pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
      });
      return;
    }
    await route.fallback();
  });

  await page.goto(`${USER_ORIGIN}/steward?tab=charity&handling=pending`);
  await expect(page.getByRole('tab', { name: 'Charity management', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(page.getByText('Donor details are hidden', { exact: true }).first()).toBeVisible();
  expect(donationListReads).toContain('?handling=pending&page=1&page_size=20');
  await page.getByRole('button', { name: 'Review', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Donation #8', exact: true })).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Key 12 · sk-live…fixture', exact: true }),
  ).toBeVisible();
  await expect(page.getByText('Donor details are hidden', { exact: true })).toHaveCount(2);
  expect(donationDetailReads).toContain('');
  expect(donationKeyReads).toContain('?page=1&page_size=20');
  await saveScreenshot(page, 'steward-other-donor-320-dark-en');

  await page.getByRole('tab', { name: 'Request logs', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Request logs', exact: true })).toBeVisible();
  await expect(page.getByText(CALLER_NICKNAME, { exact: true })).toBeVisible();
  await expect(page.getByText(DISCORD_ID, { exact: true })).toBeVisible();
  expect(logListReads).toContain('?page=1&page_size=20');
  const copyButton = page.getByRole('button', { name: 'Copy Discord ID', exact: true }).first();
  await copyButton.click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(DISCORD_ID);
  await page.getByRole('button', { name: 'Details', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(CALLER_NICKNAME, { exact: true })).toBeVisible();
  await expect(dialog.getByText(DISCORD_ID, { exact: true })).toBeVisible();
  expect(logDetailReads).toContain('?attempt_page=1&attempt_page_size=20');
  await saveScreenshot(page, 'steward-caller-detail-320-dark-en');
  await page.getByRole('button', { name: 'Close', exact: true }).click();
  await assertResponsiveOperationTables(page);
  await page.setViewportSize({ width: 1_280, height: 900 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await saveScreenshot(page, 'steward-caller-1280-dark-en');
  await assertPagePresentation(page, setup);
});
