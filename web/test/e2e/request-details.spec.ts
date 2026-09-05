import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';

const fixtures = JSON.parse(
  readFileSync(resolve(process.cwd(), '../internal/logapi/testdata/role_dtos.golden'), 'utf8'),
) as Record<
  string,
  {
    request: { id: string } & Record<string, unknown>;
    attempts: { data: Record<string, unknown>[]; next_cursor: null };
  }
>;

for (const station of ['user', 'admin'] as const) {
  test(`${station} request details wrap long fields and keep attempts vertical on desktop and mobile`, async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'user');
    await mockPublicConfig(page, station);
    const detail = structuredClone(fixtures[station === 'admin' ? 'admin' : 'user_self']);
    detail.attempts.data[0].endpoint_base_url = `https://example.test/${'very-long-route-'.repeat(20)}/v1`;
    detail.attempts.data[1].diag = 'A safe diagnostic '.repeat(90);
    const prefix = station === 'admin' ? '/admin/api/logs' : '/api/logs';
    let detailReads = 0;
    await page.route(`**${prefix}**`, async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === prefix) {
        await route.fulfill({ json: { data: [detail.request], next_cursor: null } });
        return;
      }
      if (path === `${prefix}/${detail.request.id}`) {
        detailReads++;
        await route.fulfill({ json: detail });
        return;
      }
      await route.fallback();
    });
    const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    await page.setViewportSize({ width: 1600, height: 1000 });
    await page.goto(
      `${origin}/logs${station === 'user' ? `?request_id=${detail.request.id}` : ''}`,
    );
    if (station === 'admin') await page.locator('.table-wrap tbody button').first().click();
    const drawer = page.getByRole('dialog');
    await expect(drawer).toBeVisible();
    await expect(drawer.locator('.log-attempt')).toHaveCount(2);
    await expect(drawer.locator('table')).toHaveCount(0);
    await expect(drawer.locator('.log-attempt-notice')).toBeVisible();
    expect(detailReads).toBeGreaterThan(0);
    await expect
      .poll(() => drawer.evaluate((node) => node.scrollWidth <= node.clientWidth + 1))
      .toBe(true);
    await page.screenshot({ path: `../tmp/${station}-request-detail-desktop.png` });
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 844 });
      await page.evaluate(() => {
        document.documentElement.style.scrollbarGutter = 'stable';
      });
      await expect
        .poll(() => drawer.evaluate((node) => node.getBoundingClientRect().left))
        .toBeGreaterThanOrEqual(0);
      await expect
        .poll(() =>
          drawer.evaluate((node) =>
            [node, ...node.querySelectorAll<HTMLElement>('*')]
              .filter(
                (element) =>
                  element.clientWidth > 0 &&
                  element.scrollWidth > element.clientWidth + 1 &&
                  !element.classList.contains('visually-hidden'),
              )
              .map((element) => ({
                element: `${element.tagName}.${element.className}`,
                width: element.clientWidth,
                content: element.scrollWidth,
              })),
          ),
        )
        .toEqual([]);
      await expect.poll(() => page.evaluate(() => document.body.style.overflow)).toBe('hidden');
      await drawer.evaluate((node) => {
        node.scrollTop = node.scrollHeight;
      });
      await expect(drawer.getByRole('button', { name: 'Close', exact: true })).toBeInViewport();
      await page.screenshot({ path: `../tmp/${station}-request-detail-mobile-${width}.png` });
    }
    await page.keyboard.press('Escape');
    await expect(drawer).toHaveCount(0);
    await expect.poll(() => page.evaluate(() => document.body.style.overflow)).not.toBe('hidden');
    if (station === 'user') expect(new URL(page.url()).searchParams.has('request_id')).toBe(false);
    errors.assertNone();
  });
}

test('an expired or foreign request link stays unavailable in the current account', async ({
  page,
}) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const id = fixtures.user_self.request.id;
  await page.route('**/api/logs**', (route) =>
    new URL(route.request().url()).pathname.endsWith(id)
      ? route.fulfill({ status: 404, json: { error: { code: 'not_found', message: 'not found' } } })
      : route.fulfill({ json: { data: [], next_cursor: null } }),
  );
  await page.goto(`${USER_ORIGIN}/logs?request_id=${id}`);
  await expect(page.getByRole('dialog')).toContainText(/no longer available|unavailable|expired/i);
  await expect(page.locator('.log-attempt')).toHaveCount(0);
});
