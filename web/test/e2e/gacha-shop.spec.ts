import { resolve } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gachaSnapshot } from '../fixtures/gacha';
import type { Action } from '../../src/user/activities/gacha/types';

for (const theme of ['light', 'dark'] as const)
  for (const width of [1440, 390])
    test(`daily ticket bundles ${theme} ${width}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript((theme) => {
        localStorage.setItem('nb.lang', 'zh');
        localStorage.setItem('nb.theme', theme);
        localStorage.setItem(
          'nb.gacha.preferences',
          JSON.stringify({ music: false, sound: false, reduced: true }),
        );
      }, theme);
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      const state = gachaSnapshot();
      state.credits = { general: '5000', game: '15000' };
      await page.route('**/api/events', (route) =>
        route.fulfill({
          contentType: 'text/event-stream',
          body: `: connected

`,
        }),
      );
      await page.route('**/api/gacha/state', (route) => route.fulfill({ json: state }));
      await page.route('**/api/gacha/entry', (route) =>
        route.fulfill({ json: { enabled: true, entered: true, pools: [] } }),
      );
      await page.route('**/api/appearance', (route) =>
        route.fulfill({
          json: {
            selected: 'default',
            visible: ['default'],
            unlocked: ['default'],
            updated_at: null,
            moment: 'cover:permanent',
            moments: ['cover:permanent'],
          },
        }),
      );
      const purchases: string[] = [];
      await page.route('**/api/gacha/actions', (route) => {
        const input = route.request().postDataJSON() as Action;
        if (input.action === 'redeem') {
          expect(input.product).toBe(purchases.length ? 'bulk-limited' : 'bulk-permanent');
          expect(input.revision).toBe(1);
          purchases.push(input.product);
          state.redeemed.push(input.product);
          state.tickets[input.product === 'bulk-permanent' ? 'permanent' : 'limited'] += 10;
          state.credits =
            purchases.length === 1
              ? { general: '5000', game: '5000' }
              : { general: '0', game: '0' };
        } else expect(input.action).toBe('enter');
        return route.fulfill({ json: { snapshot: state } });
      });
      await page.goto(USER_ORIGIN + '/activities/gacha');
      await page.getByRole('tab', { name: '神秘商店', exact: true }).click();
      const shop = page.getByRole('region', { name: '量大管饱' });
      await expect(
        shop.getByRole('heading', { name: '10张常驻抽奖券', exact: true }),
      ).toBeVisible();
      await expect(
        shop.getByRole('heading', { name: '10张限定抽奖券', exact: true }),
      ).toBeVisible();
      await expect(shop.getByText('10,000 悠哉积分', { exact: true })).toHaveCount(2);
      await expect(shop.getByText('今日剩余：1/1', { exact: true })).toHaveCount(2);
      await shop.scrollIntoViewIfNeeded();
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `gacha-shop-${theme}-${width}.png`),
        });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await shop.getByRole('button', { name: '购买', exact: true }).first().click();
      await expect(shop.getByRole('button', { name: '今日已购买', exact: true })).toHaveCount(1);
      await expect(page.getByText('购买成功，10张抽奖券已到账', { exact: true })).toBeVisible();
      await shop.getByRole('button', { name: '购买', exact: true }).click();
      await expect(shop.getByRole('button', { name: '今日已购买', exact: true })).toHaveCount(2);
      await expect(shop.getByText('今日剩余：0/1', { exact: true })).toHaveCount(2);
      expect(purchases).toEqual(['bulk-permanent', 'bulk-limited']);
      guard.assertNone();
    });
