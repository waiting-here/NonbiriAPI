import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import {
  collectConsoleViolations,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';

test('user management separates identifiers and copies Discord IDs exactly at desktop and mobile sizes', async ({
  page,
  context,
}) => {
  const errors = collectConsoleViolations(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/maintenance',
    body: { enabled: false, revision: '1' },
  });
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: ADMIN_ORIGIN });
  const user = {
    ...Object.fromEntries(
      Object.entries(userSession('user').user).filter(
        ([key]) => !['avatar', 'effective_level', 'level_display_name'].includes(key),
      ),
    ),
    id: '7',
    discord_id: '1234567890123456789',
    is_admin: false,
    banned_reason: '',
    level: { manual: null, automatic: 1, effective: 1, display_name: 'Lv1' },
    revision: '1',
  };
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/users?limit=50',
    body: { data: [user], next_cursor: null },
  });
  await page.setViewportSize({ width: 1935, height: 1000 });
  await page.goto(`${ADMIN_ORIGIN}/users`);
  const table = page.locator('.ops-users-table');
  await expect(table.getByRole('columnheader', { name: 'User ID', exact: true })).toBeVisible();
  const cells = table.locator('tbody tr td');
  await expect(cells.nth(0)).toHaveText('7');
  await expect(cells.nth(1)).toHaveText('fixture-user');
  await page.getByRole('button', { name: 'Copy Discord ID', exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(user.discord_id);
  for (const width of [320, 390, 1440, 1935]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
      .toBe(true);
    await expect(table.getByText(user.discord_id, { exact: true })).toBeVisible();
    const box = await table.getByText(user.discord_id, { exact: true }).boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
  }
  errors.assertNone();
});
