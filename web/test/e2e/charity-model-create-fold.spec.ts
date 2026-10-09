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
  test(`new charity model disclosure retains inputs on ${scenario.station} at ${scenario.width}px ${scenario.theme}`, async ({
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
        ['/donations', '/charity-models', '/donation-sources'].some(
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
    const summary = page.locator('.charity-model-editor > .nb-fold > summary');
    const disclosure = page.locator('.charity-model-editor > .nb-fold');
    const provider = page.getByLabel('Provider', { exact: true });
    const model = page.getByLabel('Model', { exact: true });
    const strategy = page.getByRole('combobox', { name: /Routing strategy/ });
    await expect(disclosure).toHaveAttribute('open');
    await expect(strategy).toHaveValue('cache_balanced');
    await provider.fill('draft-prefix');
    await model.fill('draft-model');
    await strategy.selectOption('ordered');
    await summary.click();
    await expect(disclosure).not.toHaveAttribute('open');
    await expect(provider).toBeHidden();
    expect(submissions).toBe(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    const evidence = process.env.NONBIRI_VISUAL_DIR;
    const filename = `${scenario.station}-new-charity-model-${scenario.width}-${scenario.theme}`;
    if (evidence) {
      await mkdir(evidence, { recursive: true });
      await page.screenshot({ path: resolve(evidence, filename + '-collapsed.png') });
    }
    await summary.focus();
    await summary.press('Enter');
    await expect(disclosure).toHaveAttribute('open');
    await expect(provider).toHaveValue('draft-prefix');
    await expect(model).toHaveValue('draft-model');
    await expect(strategy).toHaveValue('ordered');
    await summary.press('Space');
    await expect(disclosure).not.toHaveAttribute('open');
    await summary.press('Enter');
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
        path: resolve(evidence, filename + '-expanded.png'),
        fullPage: true,
      });
    }
    consoleGuard.assertNone();
  });
}
