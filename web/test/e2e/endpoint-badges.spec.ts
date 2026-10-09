import { numberedPage } from './numbered-fixtures';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

for (const width of [1440, 390]) {
  for (const theme of ['light', 'dark'] as const) {
    test(`current mainstream endpoint badges at ${width}px ${theme}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((value) => {
        localStorage.setItem('nb.lang', 'zh');
        localStorage.setItem('nb.theme', value);
      }, theme);
      await mockPublicConfig(page, 'user');
      await mockRoleSession(page, 'user', 'user');
      let current = true;
      await page.route(`${USER_ORIGIN}/api/endpoints?**`, (route) =>
        route.fulfill({
          json: numberedPage(
            ['subscription', 'api_platform'].map((category, index) => ({
              id: String(index + 1),
              connector_type: 'openai-compatible',
              base_url: `https://service-${index + 1}.example/v1`,
              origin: { kind: 'custom' },
              note: index ? '常用 API 服务' : '常用订阅服务',
              enabled: true,
              revision: '1',
              key_count: '1',
              created_at: 1_700_000_000,
              updated_at: 1_700_000_001,
              browse: {
                model_count: '3',
                available_key_count: '1',
                state: 'available',
                mainstream_categories: current ? [category] : [],
              },
            })),
            new URL(route.request().url()).searchParams,
          ),
        }),
      );
      await page.goto(`${USER_ORIGIN}/endpoints`);
      await expect(page.getByText('主流订阅', { exact: true })).toBeVisible();
      await expect(page.getByText('主流 API 平台', { exact: true })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: `../tmp/endpoint-badges-${width}-${theme}.png`,
        fullPage: true,
        animations: 'disabled',
      });
      current = false;
      await page.reload();
      await expect(page.getByText('常用订阅服务', { exact: true })).toBeVisible();
      await expect(page.getByText('主流订阅', { exact: true })).toHaveCount(0);
      await expect(page.getByText('主流 API 平台', { exact: true })).toHaveCount(0);
      guard.assertNone();
    });
  }
}
