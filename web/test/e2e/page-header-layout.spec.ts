import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockJson, mockPublicConfig, mockRoleSession } from './support';
import { numberedResponse } from './numbered-fixtures';

for (const locale of ['zh', 'en'] as const) {
  test(`shared page descriptions use available width in both stations ${locale}`, async ({
    page,
  }) => {
    const guard = collectConsoleViolations(page);
    await page.addInitScript((lang) => {
      localStorage.setItem('nb.lang', lang);
      localStorage.setItem('nb.theme', lang === 'zh' ? 'dark' : 'light');
    }, locale);
    for (const station of ['user', 'admin'] as const) {
      await mockRoleSession(page, station, station);
      await mockPublicConfig(page, station);
    }
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/maintenance',
      body: { enabled: false, revision: '1' },
    });
    for (const path of ['/api/models', '/api/endpoints', '/admin/api/overview/endpoints']) {
      await page.route(`**${path}?**`, (route) =>
        route.fulfill({ json: numberedResponse([], '1', 20) }),
      );
    }
    for (const [origin, path] of [
      [USER_ORIGIN, '/models'],
      [USER_ORIGIN, '/endpoints'],
      [ADMIN_ORIGIN, '/endpoints'],
    ]) {
      await page.setViewportSize({ width: 3766, height: 900 });
      await page.goto(`${origin}${path}`);
      const header = page.locator('.nb-page-header');
      const description = header.locator('.nb-page-header__description');
      await expect(description).toBeVisible();
      const headerBox = (await header.boundingBox())!;
      const descriptionBox = (await description.boundingBox())!;
      expect(descriptionBox.width).toBeGreaterThan(headerBox.width * 0.8);
      const lineHeight = await description.evaluate((node) =>
        parseFloat(getComputedStyle(node).lineHeight),
      );
      expect(descriptionBox.height).toBeLessThan(lineHeight * 1.5);
      if (process.env.NONBIRI_VISUAL_DIR && path === '/models') {
        await page.screenshot({
          path: `${process.env.NONBIRI_VISUAL_DIR}/models-${locale}-3766.png`,
        });
      }
      for (const width of [320, 390, 768, 1440, 1920]) {
        await page.setViewportSize({ width, height: 900 });
        await expect(description).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        const box = (await description.boundingBox())!;
        expect(box.x).toBeGreaterThanOrEqual(0);
        expect(box.x + box.width).toBeLessThanOrEqual(width);
      }
    }
    guard.assertNone();
  });
}
