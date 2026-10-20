import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { numberedPage } from './numbered-fixtures';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

const cases = [
  { station: 'admin', width: 1440, theme: 'light' },
  { station: 'admin', width: 1440, theme: 'dark' },
  { station: 'admin', width: 390, theme: 'light' },
  { station: 'admin', width: 390, theme: 'dark' },
  { station: 'user', width: 390, theme: 'dark' },
] as const;

for (const scenario of cases) {
  test(`new charity model workspace retains inputs on ${scenario.station} at ${scenario.width}px ${scenario.theme}`, async ({
    page,
  }) => {
    const consoleGuard = collectConsoleViolations(page);
    await page.setViewportSize({ width: scenario.width, height: 900 });
    await page.addInitScript((theme) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', theme);
    }, scenario.theme);
    await mockPublicConfig(page, scenario.station);
    await mockRoleSession(
      page,
      scenario.station,
      scenario.station === 'admin' ? 'admin' : 'level6',
    );
    const origin = scenario.station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const root = scenario.station === 'admin' ? '/admin/api' : '/api/steward';
    let submissions = 0;
    await page.route(origin + root + '/**', async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      if (request.method() === 'POST' && url.pathname === root + '/charity-models') {
        submissions++;
        await route.fulfill({ status: 500, body: '{}' });
        return;
      }
      if (
        request.method() === 'GET' &&
        ['/donations', '/charity-models', '/donation-sources', '/charity-price-templates'].some(
          (suffix) => url.pathname === root + suffix,
        )
      ) {
        await route.fulfill({ json: numberedPage([], url.searchParams) });
        return;
      }
      await route.fallback();
    });
    await page.goto(origin + (scenario.station === 'admin' ? '/charity' : '/steward?tab=charity'));
    await page.getByRole('tab', { name: /Charity models and/ }).click();
    await expect(page.getByLabel('Provider', { exact: true })).toHaveCount(0);
    await page.getByRole('button', { name: 'Create charity model', exact: true }).click();
    await expect(
      page.getByRole('heading', { name: 'Create charity model', exact: true }),
    ).toBeVisible();
    const provider = page.getByLabel('Provider', { exact: true });
    const model = page.getByLabel('Model', { exact: true });
    const strategy = page.getByRole('combobox', { name: /Routing strategy/ });
    await expect(strategy).toHaveValue('cache_balanced');
    await provider.fill('draft-prefix');
    await model.fill('draft-model');
    await strategy.selectOption('ordered');
    const reference = page.getByRole('button', { name: 'Choose a template or reference' });
    await reference.focus();
    await reference.press('Enter');
    await expect(page.getByRole('heading', { name: 'Price reference', exact: true })).toBeVisible();
    expect(submissions).toBe(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    const evidence = process.env.NONBIRI_VISUAL_DIR;
    const filename = `${scenario.station}-new-charity-model-${scenario.width}-${scenario.theme}`;
    if (evidence) {
      await mkdir(evidence, { recursive: true });
      await page.screenshot({ path: resolve(evidence, filename + '-reference.png') });
    }
    const returnToDraft = page.getByRole('button', { name: 'Return to current price draft' });
    await returnToDraft.focus();
    await returnToDraft.press('Enter');
    await expect(page.getByRole('heading', { name: 'Price reference', exact: true })).toBeHidden();
    await expect(provider).toHaveValue('draft-prefix');
    await expect(model).toHaveValue('draft-model');
    await expect(strategy).toHaveValue('ordered');
    await expect(page.locator('html')).toHaveAttribute('data-theme', scenario.theme);
    expect(submissions).toBe(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    if (evidence) {
      await page.screenshot({
        path: resolve(evidence, filename + '-draft.png'),
        fullPage: true,
      });
    }
    await page.getByRole('button', { name: 'Return to list', exact: true }).click();
    const leave = page.getByRole('alertdialog', { name: 'Leave this page?' });
    await expect(leave).toBeVisible();
    await leave.getByRole('button', { name: 'Stay here' }).click();
    await expect(provider).toHaveValue('draft-prefix');
    await page.getByRole('button', { name: 'Return to list', exact: true }).click();
    await leave.getByRole('button', { name: 'Leave page' }).click();
    await expect(provider).toHaveCount(0);
    consoleGuard.assertNone();
  });
}
