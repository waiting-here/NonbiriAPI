import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession, collectConsoleViolations } from './support';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { blackjackWire } from '../../src/user/games/blackjack/testFixtures';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';

for (const theme of ['light', 'dark'] as const) {
  for (const width of [320, 390, 1440]) {
    test(`Blackjack ${theme} ${width} keeps cards readable and actions below the hand`, async ({
      page,
    }) => {
      const errors = collectConsoleViolations(page);
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
      await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
      await page.route('**/api/games**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        await route.fulfill({
          json: path === '/api/games' ? gamesSnapshotWire() : blackjackWire(),
        });
      });
      await page.goto(`${USER_ORIGIN}/games/blackjack`);
      await expect(page.locator('.bj-mine')).toBeVisible();
      await expect(page.locator('.bj-seat')).toHaveCount(8);
      const card = page.locator('.bj-mine .bj-card').first();
      await expect(card).toHaveCSS('background-color', 'rgb(252, 248, 239)');
      await expect(card).toHaveCSS('color', 'rgb(40, 57, 47)');
      const hand = await page.locator('.bj-mine').boundingBox();
      const controls = await page.locator('.bj-controls').boundingBox();
      expect(hand).not.toBeNull();
      expect(controls).not.toBeNull();
      expect(controls!.y).toBeGreaterThanOrEqual(hand!.y + hand!.height);
      await expect(page.getByRole('button', { name: 'Hit', exact: true })).toBeEnabled();
      await page.getByRole('button', { name: 'Rules', exact: true }).focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('dialog')).toBeVisible();
      await page.keyboard.press('Escape');
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      errors.assertNone();
    });
  }
}

for (const users of [101, 10017]) {
  test(`Dashboard reads numbered endpoint totals with ${users} shared users`, async ({ page }) => {
    await mockRoleSession(page, 'admin', 'admin');
    await mockPublicConfig(page, 'admin');
    await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    const requests: URL[] = [];
    await page.route('**/admin/api/overview/endpoints*', async (route) => {
      const url = new URL(route.request().url());
      requests.push(url);
      if (!url.searchParams.has('page')) {
        await route.fulfill({
          status: 413,
          json: { error: { code: 'payload_too_large', message: 'payload is too large' } },
        });
        return;
      }
      await route.fulfill({
        json: {
          data: Array.from({ length: 20 }, (_, index) => ({
            base_url: `https://endpoint-${index}.example/v1`,
            user_count: String(users),
            endpoint_count: String(users),
            key_count: '0',
            users: Array.from({ length: 3 }, (_, user) => ({
              user_id: String(user + 1),
              endpoint_count: '1',
              key_count: '0',
              enabled_count: '1',
            })),
          })),
          next_cursor: null,
          pagination: { page: '1', page_size: 20, total_items: '37', total_pages: '2' },
        },
      });
    });
    await page.goto(ADMIN_ORIGIN);
    await expect(
      page.getByText('There are 37 endpoint groups. Open the endpoints page for details.'),
    ).toBeVisible();
    expect(requests.length).toBeGreaterThan(0);
    expect(
      requests.every(
        (url) =>
          url.searchParams.get('page') === '1' &&
          url.searchParams.get('page_size') === '20' &&
          !url.searchParams.has('limit'),
      ),
    ).toBe(true);
    await expect(page.getByText(/payload is too large/)).toHaveCount(0);
  });
}
