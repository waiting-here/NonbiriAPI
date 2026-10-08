import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { expect, test, type Page } from './test';

const secret = `nbk_${'Q'.repeat(43)}`;
const keyMetadata = {
  display: 'nbk_QQQQ…QQQQ',
  created_at: 1700000000,
  updated_at: 1700000001,
  generation: '1',
};
const model = {
  model_types: ['chat_completions', 'embeddings'],
  id: '7',
  provider: 'my',
  model: 'model',
  full_name: 'my/model',
  route_strategy: 'ordered',
  silent_retry: false,
  transport_rule: 'passthrough',
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '1',
  binding_count: '1',
  created_at: 1700000000,
  updated_at: 1700000001,
};
const charityModel = {
  model_types: ['chat_completions', 'embeddings'],
  id: '1',
  provider: 'provider',
  model: 'model',
  full_name: '[公益]provider/model',
  pricing: {
    mode: 'per_request',
    user_price_milli: '1000',
    discounted_user_price_milli: '1000',
    user_prices_milli: null,
    discounted_user_prices_milli: null,
  },
  discount: { enabled: false, percent: 100, start_at: 0, end_at: 0 },
  public_description: 'A donated model',
  enabled: true,
  allowed_levels: [1],
  level_allowed: true,
  currently_available: false,
  availability: 'no_usable_key',
};
const pagination = (total: number) => ({
  page: '1',
  page_size: 10,
  total_items: String(total),
  total_pages: '1',
});
type State = {
  active?: boolean;
  key: boolean;
  models: boolean;
  charity: boolean;
  posts: number;
  reads: string[];
};
async function mockHome(page: Page, state: State, locale: 'en' | 'zh') {
  const announcement = {
    epoch: `b1e_${'N'.repeat(21)}A`,
    id: `ann_${'N'.repeat(21)}A`,
    revision: '1',
    severity: 'info',
    pinned: false,
    dismissible: false,
    published_at: 1800000000,
    expires_at: null,
    effective_language: locale,
    fallback_from: null,
    title: locale === 'zh' ? '欢迎使用本站' : 'Welcome to the site',
    excerpt:
      locale === 'zh'
        ? '创建密钥后，可在客户端调用模型。'
        : 'Create a key to call models in your client.',
  };
  await page.route(`${USER_ORIGIN}/api/**`, async (route) => {
    const url = new URL(route.request().url());
    state.reads.push(url.pathname + url.search);
    if (url.pathname === '/api/caller-key')
      return route.fulfill({
        json: state.key ? keyMetadata : null,
        headers: { 'X-Nonbiri-CallerKey-Generation': state.key ? '1' : '0' },
      });
    if (url.pathname === '/api/caller-key/regenerate') {
      state.posts++;
      expect(route.request().postDataJSON()).toEqual({ expected_generation: '0' });
      expect(route.request().headers()['idempotency-key']).toBeUndefined();
      state.key = true;
      return route.fulfill({ json: { secret, metadata: keyMetadata } });
    }
    if (url.pathname === '/api/models') {
      expect(url.searchParams.get('page')).toBe('1');
      expect(url.searchParams.get('page_size')).toBe('10');
      return route.fulfill({
        json: {
          data: state.models ? [model] : [],
          next_cursor: null,
          pagination: pagination(Number(state.models)),
        },
      });
    }
    if (url.pathname === '/api/charity/models') {
      expect(url.searchParams.get('view')).toBe('catalog');
      expect(url.searchParams.get('page_size')).toBe('10');
      expect(url.searchParams.get('allowed_for_me')).toBe('true');
      return route.fulfill({
        json: {
          models: state.charity ? [charityModel] : [],
          pagination: pagination(Number(state.charity)),
          donation_intake: 'open',
          server_now: 1800000000,
        },
      });
    }
    if (url.pathname === '/api/checkin' || url.pathname === '/api/checkin/game')
      return route.fulfill({
        json: state.active
          ? {
              enabled: true,
              mutually_exclusive: true,
              blocked_by_other_checkin: false,
              asset_type: url.pathname.endsWith('/game') ? 'game' : 'general',
              checked_in_today: false,
              balance: '0',
              award_min: '1',
              award_max: '2',
              balance_cap: '0',
            }
          : { enabled: false },
      });
    if (url.pathname === '/api/home/game-summary')
      return route.fulfill({ json: { continue: [], pending_results: [] } });
    if (url.pathname === '/api/announcements')
      return route.fulfill({
        json: { data: state.active ? [announcement] : [], next_cursor: null },
      });
    if (url.pathname === `/api/announcements/${announcement.id}`) {
      const { excerpt, ...detail } = announcement;
      return route.fulfill({ json: { ...detail, rendered_body: `<p>${excerpt}</p>` } });
    }
    return route.fallback();
  });
}
async function capture(page: Page, name: string, locale: string, metrics: unknown[]) {
  const directory = process.env.NONBIRI_VISUAL_DIR;
  if (!directory) return;
  await mkdir(directory, { recursive: true });
  for (const width of [1440, 768, 390])
    for (const theme of ['light', 'dark']) {
      await page.setViewportSize({
        width,
        height: width === 768 ? 1024 : width === 390 ? 844 : 900,
      });
      await page.evaluate((selected) => {
        document.documentElement.dataset.theme = selected;
        document.documentElement.style.colorScheme = selected;
      }, theme);
      await page.waitForTimeout(250);
      const measure = await page.evaluate(() => ({
        width: innerWidth,
        scrollWidth: document.documentElement.scrollWidth,
        height: document.documentElement.scrollHeight,
      }));
      expect(measure.scrollWidth).toBeLessThanOrEqual(width);
      metrics.push({ name, locale, theme, ...measure });
      await page.screenshot({
        path: resolve(directory, `${name}-${locale}-${theme}-${width}.png`),
        fullPage: true,
      });
    }
}
for (const locale of ['en', 'zh'] as const) {
  test(`home checklist and client instructions follow real state ${locale}`, async ({
    context,
    page,
  }) => {
    const guard = collectConsoleViolations(page);
    await installURLPersistenceObserver(context, [secret]);
    await page.addInitScript((language) => {
      localStorage.setItem('nb.lang', language);
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: async (value: string) => {
            if ((window as unknown as { failCopy: boolean }).failCopy)
              throw new Error('Clipboard unavailable');
            (window as unknown as { copied: string }).copied = value;
          },
        },
      });
      document.execCommand = () => false;
    }, locale);
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    const state: State = { key: false, models: false, charity: false, posts: 0, reads: [] };
    await mockHome(page, state, locale);
    const metrics: unknown[] = [];
    const progress = (count: number) =>
      locale === 'zh' ? `已完成 ${count} / 3` : `${count} of 3 complete`;
    const started = locale === 'zh' ? '开始使用' : 'Get started';
    const completed = locale === 'zh' ? '已完成设置' : 'Setup complete';
    await page.goto(USER_ORIGIN);
    await expect(page.getByRole('heading', { name: started })).toBeVisible();
    await expect(page.getByText(progress(0), { exact: true })).toBeVisible();
    await expect
      .poll(() => state.reads.filter((path) => path.startsWith('/api/models?')).length)
      .toBeGreaterThan(0);
    expect(
      state.reads
        .filter((path) => path.startsWith('/api/models?'))
        .every((path) => path.includes('page=1') && path.includes('page_size=10')),
    ).toBe(true);
    await capture(page, 'home-new', locale, metrics);
    await page.evaluate(() => {
      (window as unknown as { failCopy: boolean }).failCopy = true;
    });
    await page
      .getByRole('button', { name: locale === 'zh' ? '复制API 地址' : 'Copy API address' })
      .click();
    await expect(
      page.getByRole('status').filter({ hasText: locale === 'zh' ? '复制失败' : 'Copy failed' }),
    ).toBeVisible();
    expect(await page.evaluate(() => localStorage.getItem('nb.onboarding.client'))).toBeNull();

    state.active = true;
    await page.reload();
    await expect(
      page.getByRole('heading', { name: locale === 'zh' ? '欢迎使用本站' : 'Welcome to the site' }),
    ).toBeVisible();
    await capture(page, 'home-active', locale, metrics);

    state.key = true;
    await page.reload();
    await expect(page.getByText(progress(1), { exact: true })).toBeVisible();
    await capture(page, 'home-key-no-model', locale, metrics);
    await page
      .getByRole('link', { name: locale === 'zh' ? '查看填写方法' : 'See client instructions' })
      .click();
    await expect(page).toHaveURL(`${USER_ORIGIN}/keys#client`);
    await expect(page.locator('#client')).toContainText(`${USER_ORIGIN}/v1`);
    expect(await page.evaluate(() => localStorage.getItem('nb.onboarding.client'))).toBe('1');
    await page
      .getByRole('button', { name: locale === 'zh' ? '复制API 地址' : 'Copy API address' })
      .click();
    expect(await page.evaluate(() => (window as unknown as { copied: string }).copied)).toBe(
      `${USER_ORIGIN}/v1`,
    );
    await capture(page, 'api-existing', locale, metrics);
    await page
      .getByText(locale === 'zh' ? '命令行测试' : 'Command-line test', { exact: true })
      .click();
    await expect(page.locator('.nb-fold[open]')).toContainText('$NONBIRI_KEY');
    await expect(page.locator('.nb-fold[open]')).toContainText(
      `${USER_ORIGIN}/v1/chat/completions`,
    );
    await expect(page.locator('.nb-fold[open]')).not.toContainText(secret);
    await capture(page, 'api-curl', locale, metrics);
    await page
      .getByText(
        locale === 'zh'
          ? '用脚本批量导入密钥和模型（进阶）'
          : 'Import keys and models with scripts (advanced)',
        { exact: true },
      )
      .click();
    await expect(page.locator('.personal-automation-guide details')).toHaveAttribute('open', '');
    await capture(page, 'api-automation', locale, metrics);

    state.models = true;
    await page.goto(USER_ORIGIN);
    await expect(page.getByRole('heading', { name: completed })).toBeVisible();
    await expect(page.locator('.home-checklist')).toHaveCount(0);
    await capture(page, 'home-complete', locale, metrics);
    state.models = false;
    state.charity = true;
    await page.reload();
    await expect(page.getByRole('heading', { name: completed })).toBeVisible();
    await page
      .getByRole('button', { name: locale === 'zh' ? '隐藏' : 'Hide', exact: true })
      .click();
    await expect(page.locator('.home-start')).toHaveCount(0);
    expect(await page.evaluate(() => localStorage.getItem('nb.onboarding.hidden'))).toBe('1');
    await page.reload();
    await expect(
      page.getByRole('heading', { name: locale === 'zh' ? '我的钱包' : 'My wallet' }),
    ).toBeVisible();
    await expect(page.locator('.home-start')).toHaveCount(0);
    await capture(page, 'home-hidden', locale, metrics);

    state.key = false;
    await page.goto(`${USER_ORIGIN}/keys`);
    await expect(
      page.getByRole('button', { name: locale === 'zh' ? '创建密钥' : 'Create key', exact: true }),
    ).toBeEnabled();
    await capture(page, 'api-no-key', locale, metrics);
    await page
      .getByRole('button', { name: locale === 'zh' ? '创建密钥' : 'Create key', exact: true })
      .click();
    await expect(page.locator('.core-secret-value')).toHaveText(secret);
    expect(state.posts).toBe(1);
    await capture(page, 'api-one-time', locale, metrics);
    await assertNoSensitiveBrowserPersistence(page, [secret]);
    await page
      .getByRole('button', { name: locale === 'zh' ? '我已保存' : 'I have saved it', exact: true })
      .click();
    await expect(page.locator('.core-secret-value')).toHaveCount(0);
    await page.reload();
    await expect(page.locator('.core-secret-value')).toHaveCount(0);
    await assertNoSensitiveBrowserPersistence(page, [secret]);
    if (process.env.NONBIRI_VISUAL_DIR)
      await writeFile(
        resolve(process.env.NONBIRI_VISUAL_DIR, `metrics-${locale}.json`),
        JSON.stringify(metrics, null, 2),
      );
    guard.assertNone();
  });
}
for (const locale of ['en', 'zh'] as const) {
  test(`logged-out home stays public ${locale}`, async ({ page }) => {
    const violations: string[] = [];
    page.on('console', (message) => {
      if (
        (message.type() === 'error' || message.type() === 'warning') &&
        message.text() !==
          'Failed to load resource: the server responded with a status of 401 (Unauthorized)'
      )
        violations.push(message.type());
    });
    page.on('pageerror', () => violations.push('pageerror'));
    const guard = { assertNone: () => expect(violations).toEqual([]) };
    await page.addInitScript((language) => localStorage.setItem('nb.lang', language), locale);
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'anonymous');
    const privateReads: string[] = [];
    page.on('request', (request) => {
      const path = new URL(request.url()).pathname;
      if (/^\/api\/(me|caller-key|models|charity|checkin|home|announcements)/.test(path))
        privateReads.push(path);
    });
    await page.goto(USER_ORIGIN);
    await expect(page.getByRole('heading', { name: 'NonbiriAPI', exact: true })).toBeVisible();
    await expect(
      page.getByRole('link', {
        name: locale === 'zh' ? '使用 Discord 登录' : 'Sign in with Discord',
      }),
    ).toBeVisible();
    await expect(page.locator('.home-intro-points > div')).toHaveCount(3);
    const metrics: unknown[] = [];
    await capture(page, 'home-logged-out', locale, metrics);
    expect(privateReads).toEqual([]);
    if (process.env.NONBIRI_VISUAL_DIR)
      await writeFile(
        resolve(process.env.NONBIRI_VISUAL_DIR, `metrics-anonymous-${locale}.json`),
        JSON.stringify(metrics, null, 2),
      );
    guard.assertNone();
  });
}
