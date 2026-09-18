import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { biddingHomeWire, biddingID } from '../../src/user/games/bidding/testFixtures';

const viewports = [
  { name: 'mobile', width: 390, height: 844 },
  { name: 'desktop', width: 1920, height: 1080 },
] as const;

for (const you of [0, 1] as const) {
  for (const viewport of viewports) {
    test(`bidding reward draw flies from both real decks at ${viewport.name}, you=${you}`, async ({
      page,
    }) => {
      const consoleGuard = collectConsoleViolations(page);
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      const live = biddingHomeWire();
      live.current.you = you;
      const queued = {
        ...live,
        current: null,
        queue: {
          id: biddingID.replace('bid_', 'bidq_'),
          revision: '1',
          mode: 'tier1',
          deadline: live.server_now + 120,
          ticket: '5',
          payment: { general: '2', game: '3' },
          rules_version: 1,
          terms_hash: 'a'.repeat(64),
        },
      };
      let stateCalls = 0;
      await page.route('**/api/games**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
        if (path === '/api/games/bidding/state') {
          stateCalls += 1;
          return route.fulfill({ json: stateCalls === 1 ? queued : live });
        }
        return route.fallback();
      });
      await page.setViewportSize(viewport);
      await page.goto(`${USER_ORIGIN}/games/bidding`);

      await expect(page.locator('.bid-presentation--draw .bid-reward')).toHaveCount(2);
      const source = page.locator('.bid-reward-deck--0 > summary');
      const target = page.locator('.bid-presentation .bid-reward--0');
      const source1 = page.locator('.bid-reward-deck--1 > summary');
      const target1 = page.locator('.bid-presentation .bid-reward--1');
      await expect(source).toBeVisible();
      await expect(source1).toBeVisible();
      const geometry = await page.evaluate(() => {
        const read = (selector: string, pauseAt: number | null) => {
          const element = document.querySelector<HTMLElement>(selector);
          if (!element) throw new Error(`missing ${selector}`);
          const animation = element
            .getAnimations()
            .find((candidate) =>
              (candidate as CSSAnimation).animationName?.includes('draw-from-deck'),
            );
          if (pauseAt !== null) {
            if (!animation) throw new Error(`missing draw animation for ${selector}`);
            animation.pause();
            animation.currentTime = pauseAt;
          }
          const rect = element.getBoundingClientRect();
          return {
            center: { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 },
            width: rect.width,
            height: rect.height,
          };
        };
        const source0 = read('.bid-reward-deck--0 > summary', null);
        const source1 = read('.bid-reward-deck--1 > summary', null);
        const start0 = read('.bid-presentation .bid-reward--0', 0);
        const start1 = read('.bid-presentation .bid-reward--1', 0);
        const end0 = read('.bid-presentation .bid-reward--0', 800);
        const end1 = read('.bid-presentation .bid-reward--1', 800);
        return {
          source0,
          source1,
          start0,
          start1,
          end0,
          end1,
          scrollWidth: document.documentElement.scrollWidth,
          viewport: window.innerWidth,
          keys: [...document.querySelectorAll<HTMLElement>('.bid-presentation .bid-reward')].map(
            (element) => element.dataset.rewardKey,
          ),
        };
      });
      expect(geometry.keys).toHaveLength(2);
      expect(new Set(geometry.keys).size).toBe(2);
      expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.viewport);
      for (const side of [0, 1] as const) {
        const source = side === 0 ? geometry.source0 : geometry.source1;
        const start = side === 0 ? geometry.start0 : geometry.start1;
        const end = side === 0 ? geometry.end0 : geometry.end1;
        expect(start.center.x).toBeCloseTo(source.center.x, 0);
        expect(start.center.y).toBeCloseTo(source.center.y, 0);
        expect(end.center.x).not.toBeCloseTo(source.center.x, 0);
        expect(end.center.y).not.toBeCloseTo(source.center.y, 0);
        expect(start.width / end.width).toBeGreaterThan(0.4);
      }

      await page.waitForTimeout(900);
      await expect(target).toBeVisible();
      await expect(target1).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      consoleGuard.assertNone();
    });
  }
}
