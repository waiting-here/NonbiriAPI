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
        shop.getByRole('heading', { name: '常驻抽奖券 ×10', exact: true }),
      ).toBeVisible();
      await expect(
        shop.getByRole('heading', { name: '限定抽奖券 ×10', exact: true }),
      ).toBeVisible();
      await expect(shop.getByText('需要：10,000 悠哉积分', { exact: true })).toHaveCount(2);
      await expect(shop.getByText('今日剩余：1/1 件', { exact: true })).toHaveCount(2);
      await page.getByRole('tab', { name: '卡牌收藏', exact: true }).click();
      await expect(page.getByRole('heading', { name: '常驻 SSR', exact: true })).toBeVisible();
      await expect(page.getByText('兑换需要：3 SSR 碎片', { exact: true })).toHaveCount(5);
      await page.getByRole('tab', { name: '抽奖券', exact: true }).click();
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
      await expect(shop.getByText('今日剩余：0/1 件', { exact: true })).toHaveCount(2);
      expect(purchases).toEqual(['bulk-permanent', 'bulk-limited']);
      guard.assertNone();
    });

for (const theme of ['light', 'dark'] as const)
  for (const width of [1440, 390])
    test(`shard offers with artwork background ${theme} ${width}`, async ({ page }) => {
      const english = theme === 'light';
      const t = (zh: string, en: string) => (english ? en : zh);
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript(
        ({ theme, english }) => {
          localStorage.setItem('nb.lang', english ? 'en' : 'zh');
          localStorage.setItem('nb.theme', theme);
          localStorage.setItem(
            'nb.gacha.preferences',
            JSON.stringify({ music: false, sound: false, reduced: true }),
          );
        },
        { theme, english },
      );
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      const state = gachaSnapshot();
      state.fragments = { N: 60, R: 20, SR: 10, SSR: 2 };
      state.month_used.limited = state.shop.limited_cap;
      await page.route('**/api/events', (route) =>
        route.fulfill({
          contentType: 'text/event-stream',
          body: ': connected\n\n',
        }),
      );
      await page.route('**/api/gacha/state', (route) => route.fulfill({ json: state }));
      await page.route('**/api/gacha/entry', (route) =>
        route.fulfill({
          json: { enabled: true, entered: true, pools: [] },
        }),
      );
      await page.route('**/api/appearance', (route) =>
        route.fulfill({
          json: {
            selected: 'heart-moment',
            visible: ['default', 'heart-moment'],
            unlocked: ['default', 'heart-moment'],
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
          expect(input.revision).toBe(1);
          purchases.push(input.product);
          if (input.product === 'ticket-n-unlimited') {
            state.fragments.N -= 40;
            state.tickets.permanent++;
          } else if (input.product === 'ticket-n') {
            state.fragments.N -= 20;
            state.tickets.permanent++;
            state.month_used.permanent++;
          } else {
            expect(input.product).toBe('ticket-ssr-unlimited');
            state.fragments.SSR -= 2;
            state.tickets.limited += 2;
          }
        } else expect(input.action).toBe('enter');
        return route.fulfill({ json: { snapshot: state } });
      });
      await page.goto(USER_ORIGIN + '/activities/gacha');
      await page.getByRole('tab', { name: t('神秘商店', 'Mystery shop'), exact: true }).click();
      const n = page.getByRole('region', {
        name: t('N 碎片兑换', 'N shard exchange'),
        exact: true,
      });
      const ssr = page.getByRole('region', {
        name: t('SSR 碎片兑换', 'SSR shard exchange'),
        exact: true,
      });
      await expect(n.getByTitle(t('20 N 碎片', '20 N shards'))).toBeVisible();
      await expect(n.getByTitle(t('40 N 碎片', '40 N shards'))).toBeVisible();
      await expect(
        n.getByText(english ? /The monthly offer still has/ : /月限量还可兑换/),
      ).toBeVisible();
      await expect(
        ssr.getByRole('button', { name: t('本月额度不足', 'Monthly limit reached'), exact: true }),
      ).toBeDisabled();
      await expect(
        ssr.getByRole('button', { name: t('不限量兑换', 'Exchange unlimited'), exact: true }),
      ).toBeEnabled();
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `gacha-shop-shards-${theme}-${width}.png`),
          fullPage: true,
        });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );

      await n
        .getByRole('button', { name: t('不限量兑换', 'Exchange unlimited'), exact: true })
        .click();
      await expect(
        n.getByText(t('本月剩余：20/20 张', 'Remaining this month: 20/20 tickets'), {
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        n.getByRole('button', { name: t('碎片不足', 'Need more shards'), exact: true }),
      ).toBeDisabled();
      await n
        .getByRole('button', { name: t('月限量兑换', 'Exchange monthly'), exact: true })
        .click();
      await expect(
        n.getByText(t('本月剩余：19/20 张', 'Remaining this month: 19/20 tickets'), {
          exact: true,
        }),
      ).toBeVisible();
      await ssr
        .getByRole('button', { name: t('不限量兑换', 'Exchange unlimited'), exact: true })
        .click();
      await expect(
        ssr.getByRole('button', { name: t('碎片不足', 'Need more shards'), exact: true }),
      ).toBeDisabled();
      expect(
        await page
          .locator('.gacha-ticket-option button, .gacha-ticket-option__price')
          .evaluateAll((elements) =>
            elements.every((element) => element.scrollWidth <= element.clientWidth),
          ),
      ).toBe(true);
      expect(purchases).toEqual(['ticket-n-unlimited', 'ticket-n', 'ticket-ssr-unlimited']);
      expect(state.tickets).toEqual({ permanent: 12, limited: 2 });
      expect(state.month_used).toEqual({ permanent: 1, limited: 10 });
      guard.assertNone();
    });
