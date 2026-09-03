import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  useNarrowReducedMotion as configureNarrowReducedMotion,
} from './support';
import { expect, test } from './test';

type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type Page = Parameters<typeof collectConsoleViolations>[0];
type Locale = 'en' | 'zh';
type Theme = 'light' | 'dark';
type Viewport = 'narrow' | 'desktop';

const EPHEMERAL_MARKER = 'operations-pages-ephemeral-marker';
const REPORT_SECRET = 'sk-report-browser-only-7c4a1f9e2d';
const REPORT_ACCEPTED_MESSAGE =
  'If matching credentials exist, temporary protection will be applied and an administrator will review the report.';
const ANNOUNCEMENT_ID = `ann_${'A'.repeat(22)}`;
const ANNOUNCEMENT_EPOCH = `b1e_${'A'.repeat(22)}`;
const REPORT_ID = `rpc_${'A'.repeat(22)}`;

interface Scenario {
  station: 'user' | 'admin';
  role: 'anonymous' | 'user' | 'admin';
  locale: Locale;
  theme: Theme;
  viewport: Viewport;
  forbiddenTokens?: readonly string[];
}

async function prepare(context: BrowserContext, page: Page, scenario: Scenario) {
  const forbiddenTokens = scenario.forbiddenTokens ?? [EPHEMERAL_MARKER];
  const consoleGuard = scenario.role === 'anonymous' ? null : collectConsoleViolations(page);
  await installURLPersistenceObserver(context, forbiddenTokens);
  if (scenario.viewport === 'narrow') {
    await configureNarrowReducedMotion(page);
  } else {
    await page.setViewportSize({ width: 1_280, height: 900 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
  }
  await page.addInitScript(
    ({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    },
    { locale: scenario.locale, theme: scenario.theme },
  );
  await mockPublicConfig(page, scenario.station);
  await mockRoleSession(page, scenario.station, scenario.role);
  return { consoleGuard, forbiddenTokens };
}

async function assertRouteClean(
  page: Page,
  setup: Awaited<ReturnType<typeof prepare>>,
  scenario: Scenario,
) {
  await expect(page.locator('html')).toHaveAttribute(
    'lang',
    scenario.locale === 'zh' ? 'zh-CN' : 'en',
  );
  await expect(page.locator('html')).toHaveAttribute('data-theme', scenario.theme);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await assertNoSensitiveBrowserPersistence(page, setup.forbiddenTokens);
  expect(
    setup.consoleGuard,
    'a console guard must cover the interactive route state',
  ).not.toBeNull();
  setup.consoleGuard?.assertNone();
}

async function probeQueryAndMutationCaches(page: Page, token: string) {
  return page.evaluate((forbiddenToken) => {
    interface FiberNode {
      child?: FiberNode | null;
      sibling?: FiberNode | null;
      memoizedProps?: unknown;
    }
    interface CacheEntry {
      queryKey?: unknown;
      mutationId?: unknown;
      options?: { mutationKey?: unknown };
      state?: { data?: unknown; error?: unknown; variables?: unknown };
    }
    interface CacheCollection {
      getAll(): CacheEntry[];
    }
    interface QueryClientCandidate {
      getQueryCache(): CacheCollection;
      getMutationCache(): CacheCollection;
    }

    const root = document.getElementById('root');
    const rootKey = root
      ? Object.keys(root).find((key) => key.startsWith('__reactContainer$'))
      : undefined;
    const rootFiber =
      root && rootKey ? (root as unknown as Record<string, unknown>)[rootKey] : undefined;
    const stack: unknown[] = rootFiber ? [rootFiber] : [];
    const seen = new Set<unknown>();

    while (stack.length > 0) {
      const current = stack.pop();
      if (!current || typeof current !== 'object' || seen.has(current)) continue;
      seen.add(current);
      const fiber = current as FiberNode;
      const props = fiber.memoizedProps;
      if (props && typeof props === 'object') {
        const values = props as Record<string, unknown>;
        for (const candidate of [values.client, values.value]) {
          if (!candidate || typeof candidate !== 'object') continue;
          const queryClient = candidate as Partial<QueryClientCandidate>;
          if (
            typeof queryClient.getQueryCache !== 'function' ||
            typeof queryClient.getMutationCache !== 'function'
          )
            continue;
          const queries = queryClient.getQueryCache
            .call(candidate)
            .getAll()
            .map((entry) => ({
              queryKey: entry.queryKey,
              data: entry.state?.data,
              error:
                entry.state?.error instanceof Error
                  ? { name: entry.state.error.name, message: entry.state.error.message }
                  : entry.state?.error,
            }));
          const mutations = queryClient.getMutationCache
            .call(candidate)
            .getAll()
            .map((entry) => ({
              mutationId: entry.mutationId,
              mutationKey: entry.options?.mutationKey,
              data: entry.state?.data,
              error:
                entry.state?.error instanceof Error
                  ? { name: entry.state.error.name, message: entry.state.error.message }
                  : entry.state?.error,
              variables: entry.state?.variables,
            }));
          const serialized = JSON.stringify({ queries, mutations }, (_key, value) =>
            typeof value === 'bigint' ? value.toString() : value,
          );
          return { found: true, containsToken: serialized.includes(forbiddenToken) };
        }
      }
      if (fiber.sibling) stack.push(fiber.sibling);
      if (fiber.child) stack.push(fiber.child);
    }
    return { found: false, containsToken: false };
  }, token);
}

function userAnnouncement(locale: 'en' | 'zh', options: { detail: boolean; fallback?: boolean }) {
  const fallback = options.fallback === true;
  const effectiveLanguage = fallback ? 'en' : locale;
  const title = fallback
    ? 'Fallback authority notice'
    : locale === 'zh'
      ? '中文权威公告'
      : 'English authority notice';
  const common = {
    epoch: ANNOUNCEMENT_EPOCH,
    id: ANNOUNCEMENT_ID,
    revision: '7',
    severity: 'important',
    pinned: true,
    dismissible: true,
    published_at: 1_800_000_000,
    expires_at: null,
    effective_language: effectiveLanguage,
    fallback_from: fallback ? 'zh' : null,
    title,
  };
  return options.detail
    ? {
        ...common,
        rendered_body: fallback
          ? '<h2>Fallback content</h2><p><strong>Safe English authority body.</strong></p>'
          : locale === 'zh'
            ? '<h2>安全正文</h2><p><strong>仅渲染允许的标签。</strong></p>'
            : '<h2>Safe content</h2><p><strong>Only allowed tags render.</strong> <a href="https://docs.example.test/notice" rel="noopener noreferrer">Documentation</a></p>',
      }
    : {
        ...common,
        excerpt: fallback
          ? 'English fallback excerpt.'
          : locale === 'zh'
            ? '中文摘要。'
            : 'English excerpt.',
      };
}

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
    targets: '0',
    distinct_owners: '0',
    processed: '0',
    deleted: '0',
    released: '0',
  },
  retry: null,
  created_at: 1_800_000_000,
  terminal_at: null,
};

const adminAnnouncement = {
  id: ANNOUNCEMENT_ID,
  state: 'published',
  revision: '9',
  draft: {
    zh: { title: '管理员中文草稿', body: '管理员中文正文' },
    en: { title: 'Administrator authority draft', body: 'Administrator authority body' },
  },
  published: {
    revision: '8',
    published_at: 1_800_000_000,
    zh: { title: '已发布中文标题', rendered_body: '<p><strong>已发布安全正文。</strong></p>' },
    en: {
      title: 'Published English title',
      rendered_body: '<p><strong>Published safe body.</strong></p>',
    },
  },
  severity: 'warning',
  pinned: false,
  dismissible: true,
  expires_at: null,
  withdrawn_at: null,
  created_at: 1_799_000_000,
  updated_at: 1_800_000_100,
};

test('anonymous credential report keeps its secret request-only and accepts the equalized receipt', async ({
  context,
  page,
}) => {
  const scenario: Scenario = {
    station: 'user',
    role: 'anonymous',
    locale: 'en',
    theme: 'light',
    viewport: 'narrow',
    forbiddenTokens: [EPHEMERAL_MARKER, REPORT_SECRET],
  };
  const setup = await prepare(context, page, scenario);
  const observation = {
    count: 0,
    exactBody: false,
    noStore: false,
    idempotencyKey: false,
    secretInRequestURL: false,
  };
  page.on('request', (request) => {
    if (request.url().includes(REPORT_SECRET)) observation.secretInRequestURL = true;
  });
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (
      url.origin !== USER_ORIGIN ||
      url.pathname !== '/api/reports/credential-theft' ||
      url.search !== '' ||
      request.method() !== 'POST'
    ) {
      await route.fallback();
      return;
    }
    observation.count += 1;
    const body = request.postDataJSON() as Record<string, unknown>;
    observation.exactBody =
      JSON.stringify(Object.keys(body).sort()) ===
        JSON.stringify(['base_url', 'connector_type', 'note', 'secret']) &&
      body.connector_type === 'openai-compatible' &&
      body.base_url === 'https://reported.example.test/v1' &&
      body.secret === REPORT_SECRET &&
      body.note === 'Synthetic reporter context without credentials.';
    const headers = request.headers();
    observation.noStore =
      headers['cache-control'] === 'no-store' && headers['content-type'] === 'application/json';
    observation.idempotencyKey = /^[A-Za-z0-9_-]{22}$/.test(headers['idempotency-key'] ?? '');
    await route.fulfill({
      status: 202,
      headers: {
        'content-type': 'application/json',
        'cache-control': 'no-store',
        'x-nonbiri-report-accepted': '1',
      },
      body: JSON.stringify({ accepted: true, message: REPORT_ACCEPTED_MESSAGE }),
    });
  });

  const anonymousSession = page.waitForResponse(
    (response) => response.url() === `${USER_ORIGIN}/api/session` && response.status() === 401,
  );
  await page.goto(`${USER_ORIGIN}/report`);
  await anonymousSession;
  await page.waitForTimeout(50);
  setup.consoleGuard = collectConsoleViolations(page);
  await page.getByLabel('Service address (URL)').fill('https://reported.example.test/v1');
  await page.getByLabel('Suspected leaked key or credential').fill(REPORT_SECRET);
  await page
    .getByLabel('Additional note (optional)')
    .fill('Synthetic reporter context without credentials.');
  await page.getByRole('button', { name: 'Submit report' }).click();

  await expect(page.getByRole('heading', { name: 'Report accepted' })).toBeVisible();
  await expect(page.locator('body')).not.toContainText(REPORT_SECRET);
  expect(observation).toEqual({
    count: 1,
    exactBody: true,
    noStore: true,
    idempotencyKey: true,
    secretInRequestURL: false,
  });
  const cacheProbe = await probeQueryAndMutationCaches(page, REPORT_SECRET);
  expect(cacheProbe.found, 'the mounted TanStack Query client must be observable').toBe(true);
  expect(cacheProbe.containsToken, 'the report secret reached a query or mutation cache').toBe(
    false,
  );
  await assertRouteClean(page, setup, scenario);
});

test('user announcements render English safe content on the real list and detail routes', async ({
  context,
  page,
}) => {
  const scenario: Scenario = {
    station: 'user',
    role: 'user',
    locale: 'en',
    theme: 'dark',
    viewport: 'desktop',
  };
  const setup = await prepare(context, page, scenario);
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/announcements?limit=20',
    body: { data: [userAnnouncement('en', { detail: false })], next_cursor: null },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: `/api/announcements/${ANNOUNCEMENT_ID}`,
    body: userAnnouncement('en', { detail: true }),
  });

  await page.goto(`${USER_ORIGIN}/announcements`);
  await expect(page.getByText('English authority notice')).toBeVisible();
  await page.locator(`a[href="/announcements/${ANNOUNCEMENT_ID}"]`).click();
  await expect(page).toHaveURL(`${USER_ORIGIN}/announcements/${ANNOUNCEMENT_ID}`);
  await expect(page.getByRole('heading', { name: 'Safe content' })).toBeVisible();
  const documentation = page.getByRole('link', { name: 'Documentation' });
  await expect(documentation).toHaveAttribute('href', 'https://docs.example.test/notice');
  await expect(documentation).toHaveAttribute('rel', 'noopener noreferrer');
  await expect(page.locator('.ops-announcement-body script')).toHaveCount(0);
  await assertRouteClean(page, setup, scenario);
});

test('user announcement detail explains the Chinese-to-English authority fallback', async ({
  context,
  page,
}) => {
  const scenario: Scenario = {
    station: 'user',
    role: 'user',
    locale: 'zh',
    theme: 'light',
    viewport: 'narrow',
  };
  const setup = await prepare(context, page, scenario);
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: `/api/announcements/${ANNOUNCEMENT_ID}`,
    body: userAnnouncement('zh', { detail: true, fallback: true }),
  });

  await page.goto(`${USER_ORIGIN}/announcements/${ANNOUNCEMENT_ID}`);
  await expect(page.locator('.inline-notice')).toContainText('英文');
  await expect(page.getByRole('heading', { name: 'Fallback content' })).toBeVisible();
  await expect(page.locator('.ops-announcement-body script')).toHaveCount(0);
  await assertRouteClean(page, setup, scenario);
});

test('administrator activities route reads the frozen singleton and empty pool pages', async ({
  context,
  page,
}) => {
  const scenario: Scenario = {
    station: 'admin',
    role: 'admin',
    locale: 'zh',
    theme: 'dark',
    viewport: 'narrow',
  };
  const setup = await prepare(context, page, scenario);
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/activities/config',
    body: {
      revision: '5',
      master_enabled: true,
      welfare: { enabled: true, threshold: '12345678901234567890', cap: '22345678901234567890' },
      thursday: { enabled: true },
    },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/activities/thursday',
    body: { period: null },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/pools?limit=50',
    body: { data: [], next_cursor: null },
  });

  await page.goto(`${ADMIN_ORIGIN}/activities`);
  await expect(page.locator('input[value="12345678901234567890"]')).toBeVisible();
  await expect(page.locator('.empty-state')).toHaveCount(2);
  await assertRouteClean(page, setup, scenario);
});

test('administrator report inbox opens an authoritative empty case detail', async ({
  context,
  page,
}) => {
  const scenario: Scenario = {
    station: 'admin',
    role: 'admin',
    locale: 'en',
    theme: 'light',
    viewport: 'desktop',
  };
  const setup = await prepare(context, page, scenario);
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/reports/badge',
    body: {
      total: '1',
      by_status: { pending_indexing: '0', pending_review: '1', approved_processing: '0' },
    },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/reports?limit=50',
    body: { data: [reportSummary], next_cursor: null },
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
    body: { data: [], next_cursor: null },
  });

  await page.goto(`${ADMIN_ORIGIN}/reports`);
  await expect(page.getByText('https://reported.example.test/v1')).toBeVisible();
  await page.locator(`a[href="/reports/${REPORT_ID}"]`).click();
  await expect(page).toHaveURL(`${ADMIN_ORIGIN}/reports/${REPORT_ID}`);
  await expect(page.getByText('https://reported.example.test/v1')).toBeVisible();
  await expect(page.locator('.empty-state')).toHaveCount(2);
  await assertRouteClean(page, setup, scenario);
});

test('administrator announcement list opens the strict published authority detail', async ({
  context,
  page,
}) => {
  const scenario: Scenario = {
    station: 'admin',
    role: 'admin',
    locale: 'zh',
    theme: 'dark',
    viewport: 'desktop',
  };
  const setup = await prepare(context, page, scenario);
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/announcements?limit=50',
    body: { data: [adminAnnouncement], next_cursor: null },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: `/admin/api/announcements/${ANNOUNCEMENT_ID}`,
    body: adminAnnouncement,
  });

  await page.goto(`${ADMIN_ORIGIN}/announcements`);
  const row = page.locator('tr').filter({ hasText: 'Administrator authority draft' });
  await expect(row).toBeVisible();
  await row.locator('button').click();
  await expect(page).toHaveURL(`${ADMIN_ORIGIN}/announcements/${ANNOUNCEMENT_ID}`);
  await expect(page.locator('input[value="Administrator authority draft"]')).toBeVisible();
  await expect(page.getByText('Published safe body.')).toBeVisible();
  await expect(page.locator('.ops-announcement-body script')).toHaveCount(0);
  await assertRouteClean(page, setup, scenario);
});
