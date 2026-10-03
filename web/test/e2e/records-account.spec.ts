import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import {
  collectConsoleViolations,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';

const states = [
  { locale: 'en', theme: 'light', width: 1440 },
  { locale: 'zh', theme: 'dark', width: 390 },
  { locale: 'en', theme: 'dark', width: 768 },
  { locale: 'zh', theme: 'light', width: 768 },
] as const;

for (const state of states)
  test(`account and legal navigation at ${state.locale} ${state.theme} ${state.width}`, async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/me',
      body: userSession('user'),
    });
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    }, state);
    await page.setViewportSize({ width: state.width, height: 900 });
    await page.goto(`${USER_ORIGIN}/account`);
    await expect(page.getByRole('radiogroup').first()).toBeVisible();
    await expect(
      page.getByRole('heading', {
        name: state.locale === 'zh' ? '偏好' : 'Preferences',
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.locator('.account-activity')).toBeVisible();
    const positions = await page
      .locator('.account-workspace > *')
      .evaluateAll((nodes) => nodes.map((node) => node.className));
    expect(positions.indexOf('core-card account-activity')).toBeGreaterThan(
      positions.indexOf('core-card core-account-preferences'),
    );
    expect(positions.at(-1)).toContain('account-delete-panel');
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
      .toBe(true);
    if (state.width === 390)
      expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeLessThan(2000);
    if (process.env.NONBIRI_SCREENSHOT_DIR)
      await page.screenshot({
        path: `${process.env.NONBIRI_SCREENSHOT_DIR}/account-${state.locale}-${state.theme}-${state.width}.png`,
        fullPage: true,
      });

    await page.goto(`${USER_ORIGIN}/privacy`);
    const headings = page.locator('.legal-layout__body h2, .legal-layout__body h3');
    await expect(headings.first()).toHaveAttribute('id', 'privacy-section-1');
    const nav =
      state.width >= 1024
        ? page.locator('.legal-toc--desktop')
        : page.locator('.legal-toc--mobile');
    if (state.width < 1024) await nav.locator('summary').click();
    expect(await nav.locator('a').count()).toBe(await headings.count());
    const link = nav.locator('a').nth(3);
    const href = await link.getAttribute('href');
    await link.click();
    await expect.poll(() => new URL(page.url()).hash).toBe(href);
    await expect
      .poll(() => page.locator(href!).evaluate((node) => node.getBoundingClientRect().top))
      .toBeLessThan(180);
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
      .toBe(true);
    expect(await page.locator('.legal-heading-anchor').count()).toBe(await headings.count());
    if (process.env.NONBIRI_SCREENSHOT_DIR)
      await page.screenshot({
        path: `${process.env.NONBIRI_SCREENSHOT_DIR}/privacy-${state.locale}-${state.theme}-${state.width}.png`,
        fullPage: false,
      });
    await page.goto(`${USER_ORIGIN}/terms`);
    await expect(page.locator('.legal-layout__body h2').first()).toHaveAttribute(
      'id',
      'terms-section-1',
    );
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
      .toBe(true);
    errors.assertNone();
  });

test('account language selection waits for confirmation and rolls back after failure', async ({
  page,
}) => {
  const unexpectedConsole: string[] = [];
  let expectedNetworkErrors = 0;
  page.on('console', (message) => {
    if (message.type() !== 'error' && message.type() !== 'warning') return;
    if (
      message.text().startsWith('Failed to load resource:') &&
      message.text().includes('status of 503') &&
      message.location().url.endsWith('/api/me')
    )
      expectedNetworkErrors++;
    else unexpectedConsole.push(message.type());
  });
  page.on('pageerror', () => unexpectedConsole.push('pageerror'));
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  let envelope = userSession('user');
  const patches: { body: unknown; key: string | undefined }[] = [];
  await page.route(`${USER_ORIGIN}/api/me`, async (route) => {
    if (route.request().method() === 'PATCH') {
      patches.push({
        body: route.request().postDataJSON(),
        key: route.request().headers()['idempotency-key'],
      });
      if (patches.length === 1)
        envelope = {
          ...envelope,
          user: { ...envelope.user, lang: 'zh', updated_at: envelope.user.updated_at + 1 },
        };
      else {
        await route.fulfill({
          status: 503,
          json: { error: { code: 'maintenance', message: 'Temporarily unavailable.' } },
        });
        return;
      }
    }
    await route.fulfill({ json: envelope });
  });
  await page.goto(`${USER_ORIGIN}/account`);
  await page.locator('.account-preference-language').getByText('中文', { exact: true }).click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await expect(page.getByText('已保存。', { exact: true })).toBeVisible();
  await page.locator('.account-preference-language').getByText('English', { exact: true }).click();
  await expect(page.getByRole('radio', { name: '中文', exact: true })).toBeChecked();
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  expect(patches.map((patch) => patch.body)).toEqual([{ lang: 'zh' }, { lang: 'en' }]);
  expect(patches.every((patch) => Boolean(patch.key))).toBe(true);
  expect(patches[0].key).not.toBe(patches[1].key);
  expect(unexpectedConsole).toEqual([]);
  expect(expectedNetworkErrors).toBe(1);
});

test('issue rows expose real facts and unresolved count through record route links', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  await page.route('**/api/issues**', (route) =>
    route.fulfill({
      json: {
        data: [
          {
            id: `iss_${'A'.repeat(22)}`,
            state: 'current',
            source: 'model_discovery',
            resource_kind: 'endpoint_key',
            summary_code: 'discovery_failed',
            safe_detail: 'This service key is unavailable.',
            deep_link: null,
            first_seen_at: 1800000000,
            last_seen_at: 1800000001,
            count: '1',
            closed_at: null,
          },
        ],
        next_cursor: null,
        projection_incomplete: false,
        pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
      },
    }),
  );
  await page.setViewportSize({ width: 390, height: 900 });
  await page.goto(`${USER_ORIGIN}/issues`);
  await expect(page.locator('.records-issue')).toHaveCount(1);
  await expect(page.locator('.records-tabs a[aria-current="page"] .nb-badge')).toHaveText('1');
  await expect(page.getByText('Service key', { exact: true })).toBeVisible();
  const hrefs = await page
    .locator('.records-tabs a')
    .evaluateAll((links) => links.map((link) => link.getAttribute('href')));
  expect(hrefs).toEqual(['/logs', '/credits', '/issues', '/debug']);
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    .toBe(true);
  errors.assertNone();
});
