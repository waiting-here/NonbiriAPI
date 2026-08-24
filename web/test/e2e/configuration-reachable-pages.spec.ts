import { readFileSync } from 'node:fs';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  tabTo,
  useNarrowReducedMotion as configureNarrowReducedMotion,
} from './support';
import { expect, test } from './test';

const backendCatalogCore = JSON.parse(
  readFileSync(new URL('../fixtures/site-config-catalog-core.json', import.meta.url), 'utf8'),
) as { data: Array<Record<string, unknown>> };

const EPHEMERAL_MARKER = 'configuration-browser-ephemeral-marker';

const user = {
  id: '1', username: 'fixture-user', lang: 'en', is_banned: false,
  endpoint_limit: null, effective_endpoint_limit: 10,
  rpm_limit: null, effective_rpm_limit: 60,
  concurrency_limit: null, effective_concurrency_limit: 5,
  credits: '1000', donation_credit: '2000', effective_level: 2,
  created_at: '2026-01-01T00:00:00Z',
};

function catalogEntry(key: string, options: Record<string, unknown> = {}) {
  return {
    key, group: 'fixture', value_type: 'integer',
    title: { zh: `${key} 中文`, en: key === 'site_timezone_offset_minutes'
      ? 'Site timezone offset' : 'Default Anthropic max output tokens' },
    description: { zh: '配置用途', en: 'Configuration purpose' },
    unit: { zh: '分钟', en: 'minutes' },
    nullable: false, null_writable: false,
    raw_default: 0, effective_fallback: 0,
    minimum: -720, maximum: 840, step: 1,
    allowed_values: [],
    zero_semantics: { zh: '零语义', en: 'Zero semantics' },
    null_semantics: { zh: '空语义', en: 'Null semantics' },
    empty_semantics: { zh: '空字符串语义', en: 'Empty semantics' },
    independent_gates: [],
    write_endpoint: `/admin/api/site-config/${key}`,
    ...options,
  };
}

async function prepare(
  context: Parameters<typeof installURLPersistenceObserver>[0],
  page: Parameters<typeof collectConsoleViolations>[0],
  station: 'admin' | 'user',
  role: 'admin' | 'user' | 'level5',
  locale: 'en' | 'zh',
  theme: 'light' | 'dark',
) {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await configureNarrowReducedMotion(page);
  await page.addInitScript(({ language, selectedTheme }) => {
    localStorage.setItem('nb.lang', language);
    localStorage.setItem('nb.theme', selectedTheme);
  }, { language: locale, selectedTheme: theme });
  await mockPublicConfig(page, station);
  await mockRoleSession(page, station, role);
  return consoleGuard;
}

async function assertResponsiveAndClean(
  page: Parameters<typeof collectConsoleViolations>[0],
  consoleGuard: ReturnType<typeof collectConsoleViolations>,
) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
}

test('reachable user home keeps level state but removes the implementation hint', async ({ context, page }) => {
  const guard = await prepare(context, page, 'user', 'user', 'en', 'dark');
  await mockJson(page, { origin: USER_ORIGIN, method: 'GET', path: '/api/me', body: { user } });
  await mockJson(page, { origin: USER_ORIGIN, method: 'GET', path: '/api/me/usage', body: {
    total_requests: 1, total_prompt_tokens: 2, total_completion_tokens: 3,
    total_unknown_usage_requests: 0,
  } });
  await mockJson(page, { origin: USER_ORIGIN, method: 'GET', path: '/api/checkin', body: { enabled: false } });

  await page.goto(`${USER_ORIGIN}/`);
  await expect(page.getByText('Current level')).toBeVisible();
  await expect(page.getByText('Lv1')).toBeVisible();
  await expect(page.getByText(/This page computes nothing/i)).toHaveCount(0);
  const endpointLink = page.getByRole('link', { name: 'Manage endpoints' });
  await tabTo(page, endpointLink);
  await expect(endpointLink).toBeFocused();
  await assertResponsiveAndClean(page, guard);
});

test('reachable user charity shows the neutral upstream warning without the status-guide card', async ({ context, page }) => {
  const guard = await prepare(context, page, 'user', 'user', 'zh', 'light');
  await mockJson(page, { origin: USER_ORIGIN, method: 'GET', path: '/api/charity/models', body: [] });
  await mockJson(page, { origin: USER_ORIGIN, method: 'GET', path: '/api/donations', body: [] });
  await mockJson(page, { origin: USER_ORIGIN, method: 'GET', path: '/api/endpoints', body: [] });

  await page.goto(`${USER_ORIGIN}/charity`);
  await expect(page.getByRole('note')).toContainText('第三方上游隐私提示');
  await expect(page.getByRole('note')).toContainText('第三方上游及其账户日志可能看到完整请求内容');
  await expect(page.getByText('调用状态说明')).toHaveCount(0);
  await expect(page.getByText('暂无可用公益模型')).toBeVisible();
  await assertResponsiveAndClean(page, guard);
});

test('reachable admin settings consumes the bilingual catalog and rejects a 345-minute offset locally', async ({ context, page }) => {
  const guard = await prepare(context, page, 'admin', 'admin', 'en', 'dark');
  const anthropic = catalogEntry('anthropic_default_max_tokens', {
    value_type: 'optional_integer', nullable: true, null_writable: true,
    raw_default: null, effective_fallback: 65_536, minimum: 1,
    maximum: 2_147_483_647, step: 1, unit: { zh: 'Token', en: 'tokens' },
    zero_semantics: null,
  });
  const milli = catalogEntry('credits_cap_milli', {
    value_type: 'amount', title: { zh: '签到积分门槛', en: 'Check-in credit threshold' },
    unit: { zh: '毫积分', en: 'milli-credits' }, raw_default: '0', effective_fallback: '0',
    minimum: '0', maximum: '9223372036854775807', step: '1',
  });
  const seconds = catalogEntry('rpm_ban_duration_seconds', {
    title: { zh: 'RPM 封禁时长', en: 'RPM auto-ban duration' },
    unit: { zh: '秒', en: 'seconds' }, raw_default: 3_661, effective_fallback: 3_661,
    minimum: 1, maximum: 86_400, step: 1,
  });
  const timezone = catalogEntry('site_timezone_offset_minutes', {
    value_type: 'optional_integer', nullable: true, raw_default: null,
    effective_fallback: null, step: 30,
  });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: '/admin/api/site-config', body: {
    anthropic_default_max_tokens: null, credits_cap_milli: '9223372036854775807',
    rpm_ban_duration_seconds: 3_661, site_name: 'Fixture Site', site_timezone_offset_minutes: null,
  } });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: '/admin/api/site-config/catalog', body: {
    data: [anthropic, milli, seconds, backendCatalogCore.data[0], timezone],
  } });

  await page.goto(`${ADMIN_ORIGIN}/settings`);
  await expect(page.getByLabel('Default Anthropic max output tokens')).toBeVisible();
  const timezoneInput = page.getByLabel('Site timezone offset');
  await timezoneInput.fill('345');
  await page.locator('form').filter({ has: timezoneInput }).getByRole('button', { name: 'Save value' }).click();
  await expect(page.locator('form').filter({ has: timezoneInput }).getByRole('alert')).toBeVisible();
  await expect(page.getByText(/Hard range: -720 … 840 · step 30/)).toBeVisible();
  await expect(page.getByText('Human-readable duration: 1h 1m 1s')).toBeVisible();
  await expect(page.getByText(/Exact milli-credits: 9223372036854775807/)).toContainText(
    'Display credits: 9223372036854775.807',
  );
  await page.setViewportSize({ width: 780, height: 844 });
  await page.evaluate(() => { document.documentElement.style.zoom = '200%'; });
  await expect(page.getByLabel('Site name')).toBeVisible();
  await assertResponsiveAndClean(page, guard);
});

test('reachable admin settings preserves CRLF legal text through untouched and edited browser saves', async ({ context, page }) => {
  const guard = await prepare(context, page, 'admin', 'admin', 'en', 'light');
  const key = 'legal_terms_override_en';
  const original = 'alpha\r\nbeta\r\n';
  let state = original;
  const patches: string[] = [];
  const legal = catalogEntry(key, {
    value_type: 'multiline_text', title: { zh: '服务条款覆盖（英文）', en: 'Terms override (English)' },
    unit: { zh: '无', en: 'none' }, raw_default: '', effective_fallback: '',
    minimum: 0, maximum: 65_536, step: null,
  });
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== ADMIN_ORIGIN) return route.fallback();
    if (request.method() === 'GET' && url.pathname === '/admin/api/site-config') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ [key]: state }) });
    }
    if (request.method() === 'GET' && url.pathname === '/admin/api/site-config/catalog') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: [legal] }) });
    }
    if (request.method() === 'PATCH' && url.pathname === `/admin/api/site-config/${key}`) {
      const value = (request.postDataJSON() as { value: string }).value;
      patches.push(value);
      state = value;
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ key, value }) });
    }
    return route.fallback();
  });

  await page.goto(`${ADMIN_ORIGIN}/settings`);
  const textarea = page.getByLabel('Terms override (English)');
  const save = page.locator('form').filter({ has: textarea }).getByRole('button', { name: 'Save value' });
  await save.click();
  await expect.poll(() => patches.length).toBe(1);
  expect(patches[0]).toBe(original);
  await expect(page.getByRole('status')).toContainText('Configuration updated');
  await textarea.fill('alpha\nbeta\n!');
  await save.click();
  await expect.poll(() => patches.length).toBe(2);
  await expect(page.getByRole('status')).toContainText('Configuration updated');
  expect(patches[1]).toBe('alpha\r\nbeta\r\n!');
  expect(/(^|[^\r])\n/.test(patches[1] ?? '')).toBe(false);
  await textarea.fill(original.replaceAll('\r\n', '\n'));
  await save.click();
  await expect.poll(() => patches.length).toBe(3);
  await expect(page.getByRole('status')).toContainText('Configuration updated');
  expect(state).toBe(original);
  await assertResponsiveAndClean(page, guard);
});

test('reachable admin settings exposes bounded loading and error states', async ({ context, page }) => {
  const guard = await prepare(context, page, 'admin', 'admin', 'en', 'dark');
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: '/admin/api/site-config', body: {} });
  await page.route(`${ADMIN_ORIGIN}/admin/api/site-config/catalog`, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 150));
    await route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({ data: 'malformed-catalog' }),
    });
  });
  await page.goto(`${ADMIN_ORIGIN}/settings`);
  await expect(page.getByRole('status')).toContainText('Loading…');
  await expect(page.getByRole('alert')).toContainText('The service returned an invalid response.');
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();
  await assertResponsiveAndClean(page, guard);
});

test('reachable admin charity opens the corrected pending review query without invalid_request', async ({ context, page }) => {
  const guard = await prepare(context, page, 'admin', 'admin', 'zh', 'light');
  await mockJson(page, {
    origin: ADMIN_ORIGIN, method: 'GET',
    path: '/admin/api/donations?page=1&page_size=20&status=pending',
    body: { data: [], has_more: false, total: 0 },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN, method: 'GET',
    path: '/admin/api/charity-models?page=1&page_size=100', body: { data: [], has_more: false },
  });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: '/admin/api/site-config', body: {
    charity_enabled: false, donation_accept_enabled: false, charity_token_reserve_milli: null,
  } });

  await page.goto(`${ADMIN_ORIGIN}/charity`);
  await expect(page.getByRole('heading', { name: '公益与捐赠管理' })).toBeVisible();
  await expect(page.getByText(/invalid_request/i)).toHaveCount(0);
  await assertResponsiveAndClean(page, guard);
});

test('reachable level-5 steward page keeps its bounded log projection usable', async ({ context, page }) => {
  const guard = await prepare(context, page, 'user', 'level5', 'en', 'dark');
  await mockJson(page, {
    origin: USER_ORIGIN, method: 'GET', path: '/api/steward/logs?page=1&page_size=20',
    body: { data: [], has_more: false },
  });
  await mockJson(page, {
    origin: USER_ORIGIN, method: 'GET',
    path: '/api/steward/donations?page=1&page_size=20&status=pending',
    body: { data: [], has_more: false, total: 0 },
  });
  await mockJson(page, {
    origin: USER_ORIGIN, method: 'GET', path: '/api/steward/charity-models?page=1&page_size=100',
    body: { data: [], has_more: false },
  });

  await page.goto(`${USER_ORIGIN}/steward`);
  await expect(page.getByRole('tab', { name: 'Request logs' })).toBeVisible();
  await expect(page.getByText('No request logs')).toBeVisible();
  await page.getByRole('tab', { name: 'Charity management' }).click();
  await expect(page.getByRole('heading', { name: 'Donation review queue' })).toBeVisible();
  await expect(page.getByText('No donations')).toBeVisible();
  await expect(page.getByText(/invalid_request/i)).toHaveCount(0);
  await assertResponsiveAndClean(page, guard);
});
