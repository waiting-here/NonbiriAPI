import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
  useNarrowReducedMotion as configureNarrowReducedMotion,
} from './support';
import { numberedResponse } from './numbered-fixtures';

const EVIDENCE_DIR = process.env.NONBIRI_VISUAL_DIR
  ? resolve(process.env.NONBIRI_VISUAL_DIR)
  : null;
const MODEL_ID = '7';
const PAGE_SIZE = 20;
const MODEL_NAME = '[公益]provider/charity-model';
const EXPECTED_CONFLICT_BROWSER_ERROR =
  'Failed to load resource: the server responded with a status of 409 (Conflict)';

type JSONRecord = Record<string, unknown>;
type Pricing =
  | { mode: 'per_request'; user_price: string; donor_reward: string }
  | {
      mode: 'per_token';
      user_prices: {
        uncached_input: string;
        cache_write_input: string;
        cache_read_input: string;
        output: string;
      };
      donor_rewards: {
        uncached_input: string;
        cache_write_input: string;
        cache_read_input: string;
        output: string;
      };
    };

interface Scenario {
  name: string;
  station: 'admin' | 'user';
  role: 'admin' | 'level6';
  frame: 'admin' | 'steward';
  origin: string;
  root: string;
  locale: 'en' | 'zh';
  theme: 'light' | 'dark';
  pagePath: string;
  modelsTab: string;
  pricingLabel: string;
  perRequest: string;
  perToken: string;
  reserveLabel: string;
  reserveHelp: string;
  marker: string;
}

interface FixtureState {
  model: JSONRecord;
  patchBodies: JSONRecord[];
  conflictNext: boolean;
}

const scenarios: readonly Scenario[] = [
  {
    name: 'admin-en-light',
    station: 'admin',
    role: 'admin',
    frame: 'admin',
    origin: ADMIN_ORIGIN,
    root: '/admin/api',
    locale: 'en',
    theme: 'light',
    pagePath: '/charity?charity_section=models',
    modelsTab: 'Charity models and bindings',
    pricingLabel: 'Pricing mode',
    perRequest: 'Per request',
    perToken: 'Per token',
    reserveLabel: 'Call reservation (credits)',
    reserveHelp: 'Leave blank to inherit the global configuration.',
    marker: 'model-reserve-admin-ephemeral-8f4c',
  },
  {
    name: 'steward-zh-dark',
    station: 'user',
    role: 'level6',
    frame: 'steward',
    origin: USER_ORIGIN,
    root: '/api/steward',
    locale: 'zh',
    theme: 'dark',
    pagePath: '/steward?tab=charity&charity_section=models',
    modelsTab: '公益模型与服务连接',
    pricingLabel: '计价模式',
    perRequest: '按次',
    perToken: '按 token',
    reserveLabel: '调用前预留积分',
    reserveHelp: '留空继承全局配置。',
    marker: 'model-reserve-steward-ephemeral-2c7d',
  },
];

function tokenPricing(): Pricing {
  return {
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
  };
}

function requestPricing(): Pricing {
  return { mode: 'per_request', user_price: '0', donor_reward: '0' };
}

function initialModel(): JSONRecord {
  return {
    route_strategy: 'expiry_weighted',
    id: MODEL_ID,
    provider: 'provider',
    model: 'charity-model',
    full_name: MODEL_NAME,
    enabled: true,
    allowed_levels: [1, 2, 3, 4, 5],
    public_description: '',
    token_reserve_credits: '1.234',
    pricing: tokenPricing(),
    discount: { enabled: false, percent: 0, start_at: null, end_at: null },
    transport_rule: 'passthrough',
    flatten_tool_calls: false,
    revision: '1',
    binding_revision: '0',
    binding_count: '0',
    rolling_success: { sample_count: '0', success_count: '0', percent: null },
    created_at: 1,
    updated_at: 2,
  };
}

async function fulfillJSON(
  route: Parameters<NonNullable<Parameters<Page['route']>[1]>>[0],
  value: unknown,
  status = 200,
): Promise<void> {
  await route.fulfill({
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: JSON.stringify(value),
  });
}

function assertNumberedPage(url: URL): void {
  expect(url.searchParams.get('page')).toBe('1');
  expect(url.searchParams.get('page_size')).toBe(String(PAGE_SIZE));
}

async function installManagementRoutes(page: Page, scenario: Scenario, state: FixtureState) {
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== scenario.origin) {
      await route.fallback();
      return;
    }
    const path = url.pathname;
    if (request.method() === 'GET' && path === `${scenario.root}/charity-models`) {
      assertNumberedPage(url);
      await fulfillJSON(route, numberedResponse([state.model], '1', PAGE_SIZE));
      return;
    }
    if (request.method() === 'GET' && path === `${scenario.root}/charity-models/${MODEL_ID}`) {
      expect(url.search).toBe('');
      await fulfillJSON(route, state.model);
      return;
    }
    if (
      request.method() === 'GET' &&
      path === `${scenario.root}/charity-models/${MODEL_ID}/bindings`
    ) {
      expect(url.search).toBe('');
      await fulfillJSON(route, { bindings: [], binding_revision: '0' });
      return;
    }
    if (request.method() === 'GET' && path === `${scenario.root}/donation-sources`) {
      expect(url.searchParams.get('scope')).toBe('active');
      assertNumberedPage(url);
      await fulfillJSON(route, numberedResponse([], '1', PAGE_SIZE));
      return;
    }
    if (
      request.method() === 'GET' &&
      path === `${scenario.root}/charity-models/${MODEL_ID}/binding-candidates`
    ) {
      assertNumberedPage(url);
      await fulfillJSON(route, numberedResponse([], '1', PAGE_SIZE));
      return;
    }
    if (request.method() === 'PATCH' && path === `${scenario.root}/charity-models/${MODEL_ID}`) {
      const body = request.postDataJSON() as JSONRecord;
      state.patchBodies.push(body);
      expect(body.expected_revision).toBe(state.model.revision);
      if (state.conflictNext) {
        state.conflictNext = false;
        state.model = {
          ...state.model,
          revision: String(Number(state.model.revision) + 1),
          token_reserve_credits: '9.876',
          updated_at: Number(state.model.updated_at) + 1,
        };
        await fulfillJSON(
          route,
          { error: { code: 'conflict', message: 'Synthetic concurrent model update.' } },
          409,
        );
        return;
      }
      const pricing = body.pricing;
      expect(pricing).toBeTruthy();
      state.model = {
        ...state.model,
        pricing,
        ...(Object.hasOwn(body, 'transport_rule') ? { transport_rule: body.transport_rule } : {}),
        ...(Object.hasOwn(body, 'token_reserve_credits')
          ? { token_reserve_credits: body.token_reserve_credits }
          : {}),
        revision: String(Number(state.model.revision) + 1),
        updated_at: Number(state.model.updated_at) + 1,
      };
      await fulfillJSON(route, state.model);
      return;
    }
    await route.fallback();
  });
}

async function prepare(
  context: Parameters<typeof installURLPersistenceObserver>[0],
  page: Page,
  scenario: Scenario,
) {
  const consoleGuard = collectConsoleViolations(page);
  const postConflictViolations: string[] = [];
  page.on('console', (message) => {
    if (
      (message.type() === 'error' || message.type() === 'warning') &&
      message.text() !== EXPECTED_CONFLICT_BROWSER_ERROR
    ) {
      postConflictViolations.push(`${message.type()}: ${message.text()}`);
    }
  });
  page.on('pageerror', (error) => postConflictViolations.push(`pageerror: ${error.message}`));
  await installURLPersistenceObserver(context, [scenario.marker]);
  await configureNarrowReducedMotion(page);
  await page.addInitScript(
    ({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    },
    { locale: scenario.locale, theme: scenario.theme },
  );
  await mockPublicConfig(page, scenario.station);
  await mockRoleSession(page, scenario.station, scenario.role);
  return {
    beforeConflict() {
      consoleGuard.assertNone();
      postConflictViolations.length = 0;
    },
    afterConflict() {
      expect(postConflictViolations).toEqual([]);
    },
  };
}

async function saveScreenshot(page: Page, name: string): Promise<void> {
  if (!EVIDENCE_DIR) return;
  await mkdir(EVIDENCE_DIR, { recursive: true });
  await page.screenshot({ path: resolve(EVIDENCE_DIR, `${name}.png`), fullPage: true });
}

async function assertPresentation(
  page: Page,
  scenario: Scenario,
  consoleGuard: Awaited<ReturnType<typeof prepare>>,
): Promise<void> {
  await expect(page.locator('html')).toHaveAttribute(
    'lang',
    scenario.locale === 'zh' ? 'zh-CN' : 'en',
  );
  await expect(page.locator('html')).toHaveAttribute('data-theme', scenario.theme);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await assertNoSensitiveBrowserPersistence(page, [scenario.marker]);
  consoleGuard.afterConflict();
}

async function exerciseScenario(
  page: Page,
  context: Parameters<typeof installURLPersistenceObserver>[0],
  scenario: Scenario,
): Promise<void> {
  const state: FixtureState = { model: initialModel(), patchBodies: [], conflictNext: false };
  const consoleGuard = await prepare(context, page, scenario);
  await installManagementRoutes(page, scenario, state);
  await page.goto(scenario.origin + scenario.pagePath);
  await expect(page.getByRole('tab', { name: scenario.modelsTab })).toBeVisible();
  const row = page.locator('.ops-table tbody tr').filter({ hasText: MODEL_NAME });
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: /Manage|管理/ }).click();
  const editor = page.locator('.card').filter({
    has: page.getByRole('heading', { name: MODEL_NAME }),
  });
  await expect(editor).toBeVisible();
  const pricing = editor.getByRole('combobox', { name: scenario.pricingLabel });
  const save = editor.getByRole('button', { name: /Save model|保存模型/ });
  let reserve = editor.getByRole('textbox', { name: scenario.reserveLabel });
  await expect(pricing.locator('option[value="per_request"]')).toHaveText(scenario.perRequest);
  await expect(pricing.locator('option[value="per_token"]')).toHaveText(scenario.perToken);
  await expect(pricing).toHaveValue('per_token');
  await expect(reserve).toHaveValue('1.234');
  await expect(editor.getByText(scenario.reserveHelp)).toBeVisible();
  await saveScreenshot(page, `${scenario.name}-initial`);

  await reserve.fill('1.111');
  const saved = page.waitForResponse(
    (response) =>
      response.url() === `${scenario.origin}${scenario.root}/charity-models/${MODEL_ID}` &&
      response.request().method() === 'PATCH',
  );
  await save.click();
  expect((await saved).status()).toBe(200);
  await expect.poll(() => state.patchBodies).toHaveLength(1);
  expect(state.patchBodies[0]).toMatchObject({
    expected_revision: '1',
    token_reserve_credits: '1.111',
    pricing: { mode: 'per_token' },
  });
  expect(state.model.token_reserve_credits).toBe('1.111');

  await page.reload();
  await expect(page.getByRole('tab', { name: scenario.modelsTab })).toBeVisible();
  reserve = editor.getByRole('textbox', { name: scenario.reserveLabel });
  await expect(reserve).toHaveValue('1.111');

  await reserve.fill('1.222');
  await editor.getByRole('button', { name: /Discard edits|放弃编辑/ }).click();
  reserve = editor.getByRole('textbox', { name: scenario.reserveLabel });
  await expect(reserve).toHaveValue('1.111');

  await reserve.fill('0');
  await expect(save).toBeDisabled();
  await pricing.selectOption('per_request');
  await expect(editor.getByRole('textbox', { name: scenario.reserveLabel })).toHaveCount(0);
  await expect(save).toBeEnabled();
  const modeSaved = page.waitForResponse(
    (response) =>
      response.url() === `${scenario.origin}${scenario.root}/charity-models/${MODEL_ID}` &&
      response.request().method() === 'PATCH',
  );
  await save.click();
  expect((await modeSaved).status()).toBe(200);
  await expect.poll(() => state.patchBodies).toHaveLength(2);
  expect(state.patchBodies[1]).not.toHaveProperty('token_reserve_credits');
  expect(state.patchBodies[1]).toMatchObject({ pricing: { mode: 'per_request' } });
  expect(state.model.pricing).toMatchObject(requestPricing());
  expect(state.model.token_reserve_credits).toBe('1.111');

  consoleGuard.beforeConflict();
  await pricing.selectOption('per_token');
  reserve = editor.getByRole('textbox', { name: scenario.reserveLabel });
  await expect(reserve).toHaveValue('0');
  await expect(save).toBeDisabled();

  await reserve.fill('1.222');
  state.conflictNext = true;
  const conflict = page.waitForResponse(
    (response) =>
      response.url() === `${scenario.origin}${scenario.root}/charity-models/${MODEL_ID}` &&
      response.request().method() === 'PATCH',
  );
  await save.click();
  expect((await conflict).status()).toBe(409);
  await expect.poll(() => state.patchBodies).toHaveLength(3);
  await expect(editor.getByRole('alert')).toContainText('Synthetic concurrent model update.');
  await expect(reserve).toHaveValue('1.222');

  await page.reload();
  await expect(page.getByRole('tab', { name: scenario.modelsTab })).toBeVisible();
  const reloadedEditor = page.locator('.card').filter({
    has: page.getByRole('heading', { name: MODEL_NAME }),
  });
  await expect(reloadedEditor).toBeVisible();
  const reloadedPricing = reloadedEditor.getByRole('combobox', { name: scenario.pricingLabel });
  await expect(reloadedPricing).toHaveValue('per_request');
  await expect(reloadedEditor.getByRole('textbox', { name: scenario.reserveLabel })).toHaveCount(0);
  expect(state.model.token_reserve_credits).toBe('9.876');

  await reloadedPricing.selectOption('per_token');
  const reloadedReserve = reloadedEditor.getByRole('textbox', { name: scenario.reserveLabel });
  await expect(reloadedReserve).toHaveValue('9.876');
  const clear = reloadedEditor.getByRole('button', { name: /Save model|保存模型/ });
  await reloadedReserve.fill('');
  const cleared = page.waitForResponse(
    (response) =>
      response.url() === `${scenario.origin}${scenario.root}/charity-models/${MODEL_ID}` &&
      response.request().method() === 'PATCH',
  );
  await clear.click();
  expect((await cleared).status()).toBe(200);
  await expect.poll(() => state.patchBodies).toHaveLength(4);
  expect(state.patchBodies[3]).toHaveProperty('token_reserve_credits', null);
  expect(state.patchBodies[3]).toMatchObject({ pricing: { mode: 'per_token' } });
  expect(state.model.token_reserve_credits).toBe(null);
  await expect(reloadedReserve).toHaveValue('');
  await page.reload();
  await expect(page.getByRole('tab', { name: scenario.modelsTab })).toBeVisible();
  const clearedEditor = page.locator('.card').filter({
    has: page.getByRole('heading', { name: MODEL_NAME }),
  });
  await expect(clearedEditor).toBeVisible();
  await expect(clearedEditor.getByRole('combobox', { name: scenario.pricingLabel })).toHaveValue(
    'per_token',
  );
  await expect(clearedEditor.getByRole('textbox', { name: scenario.reserveLabel })).toHaveValue('');
  await saveScreenshot(page, `${scenario.name}-cleared`);
  await assertPresentation(page, scenario, consoleGuard);
}

for (const scenario of scenarios) {
  test(`${scenario.name} preserves exact model token reserve controls at 390px`, async ({
    context,
    page,
  }) => {
    await exerciseScenario(page, context, scenario);
  });

  test(`${scenario.name} saves a transport rule once and exports the applied charity log filter`, async ({
    context,
    page,
  }) => {
    const state: FixtureState = { model: initialModel(), patchBodies: [], conflictNext: false };
    const consoleGuard = await prepare(context, page, scenario);
    if (scenario.frame === 'admin') await page.setViewportSize({ width: 1440, height: 900 });
    await installManagementRoutes(page, scenario, state);
    const logReads: URL[] = [];
    const exportReads: URL[] = [];
    await page.route('**/*', async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      if (request.method() !== 'GET' || url.origin !== scenario.origin) {
        await route.fallback();
        return;
      }
      if (url.pathname === `${scenario.root}/logs`) {
        assertNumberedPage(url);
        logReads.push(url);
        await fulfillJSON(route, numberedResponse([], '1', PAGE_SIZE));
        return;
      }
      const format = ['csv', 'json'].find(
        (format) => url.pathname === `${scenario.root}/logs/export.${format}`,
      );
      if (format) {
        exportReads.push(url);
        await route.fulfill({
          status: 200,
          headers: {
            'content-type': format === 'csv' ? 'text/csv' : 'application/json',
            'content-disposition': `attachment; filename="logs.${format}"`,
            'cache-control': 'no-store',
          },
          body: format === 'csv' ? 'request_id,charity_model\n' : '[]',
        });
        return;
      }
      await route.fallback();
    });

    const chinese = scenario.locale === 'zh';
    const rule = chinese ? 'force_stream' : 'force_non_stream';
    const ruleLabel = chinese ? '传输规则' : 'Transport rule';
    const options = chinese
      ? ['透传（默认）', '假流式（强制非流）', '假非流（强制流式）']
      : [
          'Pass through (default)',
          'Simulated streaming (force non-stream)',
          'Buffered non-streaming (force stream)',
        ];
    await page.goto(scenario.origin + scenario.pagePath);
    const row = page.locator('.ops-table tbody tr').filter({ hasText: MODEL_NAME });
    await row.getByRole('button', { name: /Manage|管理/ }).click();
    const editor = page.locator('.card').filter({
      has: page.getByRole('heading', { name: MODEL_NAME }),
    });
    const transport = editor.getByRole('combobox', { name: ruleLabel });
    await expect(transport).toHaveValue('passthrough');
    await expect(transport.locator('option')).toHaveText(options);
    await transport.selectOption(rule);
    await expect(transport).toHaveAccessibleDescription(
      chinese
        ? '收齐上游流后，向非流式调用方返回 JSON；非流请求仍受代理等待时限。'
        : 'Streaming callers receive keepalives while waiting, then the complete upstream result.',
    );
    const saved = page.waitForResponse(
      (response) =>
        response.url() === `${scenario.origin}${scenario.root}/charity-models/${MODEL_ID}` &&
        response.request().method() === 'PATCH',
    );
    await editor.getByRole('button', { name: /Save model|保存模型/ }).click();
    expect((await saved).status()).toBe(200);
    await expect.poll(() => state.patchBodies).toHaveLength(1);
    expect(state.patchBodies[0]).toMatchObject({ expected_revision: '1', transport_rule: rule });
    await page.reload();
    await expect(transport).toHaveValue(rule);
    expect(state.patchBodies).toHaveLength(1);
    await saveScreenshot(page, `${scenario.name}-transport-saved`);
    await assertPresentation(page, scenario, consoleGuard);

    await page.goto(scenario.origin + (scenario.frame === 'admin' ? '/logs' : '/steward?tab=logs'));
    const filters = page.getByTestId('log-filters');
    const charity = filters.getByRole('textbox', {
      name: chinese ? '调用的公益模型' : 'Called charity model',
    });
    await expect(charity).toHaveAttribute(
      'placeholder',
      chinese
        ? '按名称片段搜索；英文大小写无关'
        : 'Search by part of the name; English letter case ignored',
    );
    await expect.poll(() => logReads.length).toBeGreaterThan(0);
    const readsBeforeTyping = logReads.length;
    const nameFragment = 'MiXeD%_公益';
    await charity.fill(nameFragment);
    expect(new URL(page.url()).searchParams.has('charity_model')).toBe(false);
    expect(logReads).toHaveLength(readsBeforeTyping);
    const filtered = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        url.origin === scenario.origin &&
        url.pathname === `${scenario.root}/logs` &&
        url.searchParams.get('charity_model') === nameFragment
      );
    });
    await filters.getByRole('button', { name: chinese ? '应用筛选' : 'Apply filter' }).click();
    expect((await filtered).status()).toBe(200);
    await expect
      .poll(() => new URL(page.url()).searchParams.get('charity_model'))
      .toBe(nameFragment);
    expect(new URL(page.url()).searchParams.get('page')).toBe('1');

    for (const format of ['csv', 'json'] as const) {
      const link = page.getByRole('link', {
        name: `${chinese ? '导出' : 'Export'} ${format.toUpperCase()}`,
        exact: true,
      });
      const href = await link.getAttribute('href');
      expect(href).not.toBeNull();
      const exportURL = new URL(href!, scenario.origin);
      expect(exportURL.pathname).toBe(`${scenario.root}/logs/export.${format}`);
      expect(exportURL.searchParams.get('charity_model')).toBe(nameFragment);
      expect(exportURL.searchParams.has('page')).toBe(false);
      const downloaded = page.waitForEvent('download');
      await link.click();
      expect(await (await downloaded).failure()).toBeNull();
    }
    expect(exportReads.map((url) => url.searchParams.get('charity_model'))).toEqual([
      nameFragment,
      nameFragment,
    ]);
    await page.reload();
    await expect(charity).toHaveValue(nameFragment);
    expect(logReads.at(-1)?.searchParams.get('charity_model')).toBe(nameFragment);
    await saveScreenshot(page, `${scenario.name}-charity-log-filter`);
    await assertPresentation(page, scenario, consoleGuard);
  });
}
