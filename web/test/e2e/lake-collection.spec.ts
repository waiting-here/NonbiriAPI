import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer } from 'vite';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import type { ProfileView } from '../../src/user/activities/lake-notes/api';

test.use({ locale: 'zh-CN' });
let rules: typeof import('../../src/user/activities/lake-notes/rules');
test.beforeAll(async () => {
  const server = await createServer({
    configFile: false,
    optimizeDeps: { noDiscovery: true, include: [] },
    server: { middlewareMode: true, watch: null },
  });
  try {
    rules = (await server.ssrLoadModule(
      '/src/user/activities/lake-notes/rules/index.ts',
    )) as typeof rules;
  } finally {
    await server.close();
  }
});

for (const width of [1440, 390])
  for (const theme of ['light', 'dark']) {
    test(`lake collection manual reward at ${width}px in ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.addInitScript((value) => localStorage.setItem('nb.theme', value), theme);
      const errors = collectConsoleViolations(page);
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      const profile = rules.initialProfile();
      for (const fish of rules.catalog.FISH_TYPES.filter((fish) => fish.location === 'lake'))
        profile.records[fish.kind] = {
          caught: '1',
          maxLength: fish.length[0],
          bestQuality: 0,
          perfectCount: '0',
        };
      const view: ProfileView = {
        readonly: false,
        revision: '1',
        rules_id: rules.RULES_ID,
        profile,
        cast: null,
        wallet: { general_milli: '0', game_milli: '0' },
        settings: {
          revision: '1',
          enabled: true,
          exchanges: {
            coins_to_general: { enabled: false, source_amount: '', target_amount: '' },
            general_to_coins: { enabled: false, source_amount: '', target_amount: '' },
            coins_to_game: { enabled: false, source_amount: '', target_amount: '' },
            game_to_coins: { enabled: false, source_amount: '', target_amount: '' },
          },
        },
      };
      let claims = 0;
      await page.route('**/api/games/lake-notes/**', async (route) => {
        if (route.request().url().endsWith('/profile')) await route.fulfill({ json: view });
        else if (route.request().url().endsWith('/collection-claims')) {
          expect(route.request().postDataJSON()).toEqual({ location: 'lake' });
          claims++;
          view.profile.collectionClaims = { lake: 1800000000 };
          view.revision = '2';
          await route.fulfill({ json: view });
        } else await route.fallback();
      });
      await page.goto(USER_ORIGIN + '/games/lake-notes');
      await page.locator('.lake-original #catalogButton').click();
      const reward = page.locator('.catalog-reward');
      await expect(reward).toContainText('50 捐赠回馈');
      await expect(reward).toContainText('100,000 游戏悠哉积分');
      await expect(reward.getByRole('button', { name: '领取奖励' })).toBeEnabled();
      expect(claims).toBe(0);
      const folder = process.env.LAKE_COLLECTION_SCREENSHOTS || 'test-results/lake-collection';
      await mkdir(folder, { recursive: true });
      await reward.scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: join(folder, `ready-${theme}-${width}.png`),
        animations: 'disabled',
      });
      await reward.getByRole('button', { name: '领取奖励' }).click();
      await expect(reward.getByRole('button', { name: '已领取' })).toBeDisabled();
      expect(claims).toBe(1);
      await page.screenshot({
        path: join(folder, `claimed-${theme}-${width}.png`),
        animations: 'disabled',
      });
      errors.assertNone();
    });
  }
