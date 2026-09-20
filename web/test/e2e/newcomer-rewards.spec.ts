import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { blackjackWire } from '../../src/user/games/blackjack/testFixtures';
import { catalogWire } from '../../src/user/games/likes/testCatalog';

for (const game of ['bidding', 'likes', 'blackjack'] as const) {
  for (const width of [390, 1440]) {
    test(`${game} newcomer rewards at ${width} show remaining tasks and hide completed progress`, async ({ page }) => {
      const errors = collectConsoleViolations(page);
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      await page.setViewportSize({ width, height: 900 });
      const snapshot = gamesSnapshotWire();
      await page.route('**/api/games**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/games') return route.fulfill({ json: snapshot });
        if (path.endsWith('/catalog')) return route.fulfill({ json: catalogWire() });
        if (path.endsWith('/state')) return route.fulfill({ json: game === 'blackjack' ? blackjackWire() : { server_now: 1800000000, current: null, queue: null, latest_result: null } });
        return route.fallback();
      });
      await page.goto(`${USER_ORIGIN}/games/${game}`);
      const card = page.locator('.game-onboarding');
      await expect(card).toBeVisible();
      await expect(card.getByRole('listitem')).toHaveCount(game === 'blackjack' ? 5 : 4);
      const heading = card.getByRole('button');
      const total = { bidding: '10,000', likes: '18,000', blackjack: '15,000' }[game];
      await expect(heading).toContainText(`${total} general credits available`);
      await heading.click();
      await expect(heading).toHaveAttribute('aria-expanded', 'false');
      await expect(card.getByRole('list')).toHaveCount(0);
      await expect(heading).toContainText(total);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await heading.click();
      await card.screenshot({ path: `../tmp/newcomer-${game}-${width}.png` });
      for (const item of snapshot.onboarding[game].items) item.completed = true;
      snapshot.onboarding[game].all_completed = true;
      await page.reload();
      await expect(card).toHaveCount(0);
      await expect(page.locator('.nb-toast--success')).toHaveCount(0);
      errors.assertNone();
    });
  }
}
