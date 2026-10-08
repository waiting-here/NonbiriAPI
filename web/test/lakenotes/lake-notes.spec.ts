import { mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { test, expect, type BrowserContext, type Page } from '@playwright/test';
import { directions } from '../../src/shared/lakenotes/api';
import type { ProfileView } from '../../src/user/activities/lake-notes/api';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };
import { api, context, control, fixture, statePath } from './real-fixture';

const base = '/api/games/lake-notes',
  adminBase = '/admin/api/games/config';
const u = en.user.lakeNotes,
  c = common.common.lakeNotes;
async function profile(ctx: BrowserContext): Promise<ProfileView> {
  const response = await api(ctx, base + '/profile');
  expect(response.status()).toBe(200);
  return response.json();
}
async function configure(ctx: BrowserContext, enabled = true) {
  const config = await (await api(ctx, adminBase, 'GET', undefined, true)).json();
  const result = await api(
    ctx,
    adminBase,
    'PATCH',
    {
      expected_revision: config.revision,
      master_enabled: true,
      lakenotes: {
        enabled,
        exchanges: Object.fromEntries(
          directions.map((d) => [
            d,
            {
              enabled: true,
              source_amount: d.startsWith('coins') ? '3' : '125',
              target_amount: d.startsWith('coins') ? '125' : '3',
            },
          ]),
        ),
      },
    },
    true,
  );
  expect(result.status(), await result.text()).toBe(200);
}
async function noOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
    true,
  );
}
async function screenshot(page: Page, name: string) {
  const folder = join(dirname(statePath), 'preview');
  mkdirSync(folder, { recursive: true });
  await page.screenshot({ path: join(folder, name), fullPage: true });
}
test('free permanent play, menus, four exchanges and restart recovery', async ({ browser }) => {
  const admin = await context(browser, true),
    ctx = await context(browser),
    page = await ctx.newPage();
  await page.goto(fixture().user_url + '/games/lake-notes');
  await expect(page.locator('.lake-original #startButton')).toBeDisabled();
  const before = await profile(ctx);
  await configure(admin);
  await page.reload();
  expect((await profile(ctx)).wallet).toEqual(before.wallet);
  await expect(page.locator('.lake-original #scenery')).toHaveCSS(
    'background-image',
    /url\(.*assets\/lake-notes\/scene-lake/,
  );
  for (const menu of ['location', 'skill', 'shop', 'basket', 'catalog', 'contracts']) {
    await page.locator('.lake-original #' + menu + 'Button').click();
    const dialog = page.locator('.lake-original #' + menu + 'Modal');
    await expect(dialog).toBeVisible();
    if (menu === 'shop') expect(await dialog.locator('.rod-shop-art').count()).toBe(6);
    if (menu === 'catalog')
      expect(await dialog.locator('.catalog-entry').count()).toBeGreaterThanOrEqual(18);
    await noOverflow(page);
    await dialog.locator('#' + menu + 'Close').click();
    await expect(dialog).toBeHidden();
  }
  for (const d of ['general_to_coins', 'coins_to_game', 'game_to_coins', 'coins_to_general']) {
    await page.getByLabel(u.direction, { exact: true }).selectOption(d);
    await page.getByRole('button', { name: u.quote, exact: true }).click();
    await expect(page.locator('.lake-quote')).toContainText('0.125');
    await page.getByRole('button', { name: u.exchangeConfirm, exact: true }).click();
    await expect(page.getByText(u.exchangeSaved, { exact: true })).toBeVisible();
  }
  expect((await profile(ctx)).wallet).toEqual(before.wallet);
  await page.locator('.lake-original #startButton').click();
  await expect
    .poll(async () => (await profile(ctx)).cast?.ack_tick ?? 0, { timeout: 8000 })
    .toBeGreaterThanOrEqual(120);
  const paused = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname.endsWith('/pause') && response.request().method() === 'POST',
  );
  await page.locator('.lake-back').click();
  expect((await paused).status()).toBe(200);
  const saved = (await profile(ctx)).cast!;
  // Keep the shell mounted beyond its session freshness window. Returning by
  // in-app navigation must survive the resulting same-account session refresh.
  await page.waitForTimeout(15_100);
  await control(ctx, 'restart');
  const sessionRefresh = page.waitForResponse(
    (response) => new URL(response.url()).pathname === '/api/session' && response.status() === 200,
  );
  await page.locator('a[href="/games/lake-notes"]').click();
  await sessionRefresh;
  await expect(page.locator('.lake-original #overlayButton')).toBeVisible();
  expect((await profile(ctx)).cast?.state.plan).toEqual(saved.state.plan);
  const resumed = page.waitForResponse((response) =>
    new URL(response.url()).pathname.endsWith('/resume'),
  );
  await page.locator('.lake-original #overlayButton').click();
  const resumedResponse = await resumed;
  expect(resumedResponse.status(), await resumedResponse.text()).toBe(200);
  await expect
    .poll(async () => BigInt((await profile(ctx)).cast?.generation ?? '0'))
    .toBeGreaterThan(BigInt(saved.generation));
  await expect
    .poll(async () => (await profile(ctx)).cast?.ack_tick ?? 0, { timeout: 8000 })
    .toBeGreaterThan(saved.ack_tick);
  await expect(page.getByRole('alert')).toHaveCount(0);
  await configure(admin, false);
  await page.reload();
  await expect(page.getByText(u.closed, { exact: true })).toBeVisible();
  expect((await profile(ctx)).cast?.paused).toBe(true);
  await ctx.close();
  await admin.close();
});
test('unknown exchange reuses its receipt after the game closes', async ({ browser }) => {
  const admin = await context(browser, true),
    ctx = await context(browser, false, 2),
    page = await ctx.newPage();
  await configure(admin);
  await page.goto(fixture().user_url + '/games/lake-notes');
  const before = await profile(ctx);
  const keys: string[] = [];
  await page.route('**' + base + '/exchange', async (route) => {
    keys.push(route.request().headers()['idempotency-key']);
    const response = await route.fetch();
    if (keys.length === 1) await route.abort('connectionreset');
    else await route.fulfill({ response });
  });
  await page.getByLabel(u.direction, { exact: true }).selectOption('general_to_coins');
  await page.getByRole('button', { name: u.quote, exact: true }).click();
  await expect(page.locator('.lake-quote')).toBeVisible();
  await page.getByRole('button', { name: u.exchangeConfirm, exact: true }).click();
  await expect(page.getByText(u.exchangeUnknown, { exact: true })).toBeVisible();
  await configure(admin, false);
  await page.getByRole('button', { name: u.retry, exact: true }).click();
  await expect(page.getByText(u.exchangeSaved, { exact: true })).toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
  const after = await profile(ctx);
  expect(BigInt(after.profile.coins) - BigInt(before.profile.coins)).toBe(3n);
  expect(BigInt(before.wallet.general_milli) - BigInt(after.wallet.general_milli)).toBe(125n);
  await ctx.close();
  await admin.close();
});
test('game layout, menus and cover fit both themes and small screens', async ({ browser }) => {
  const admin = await context(browser, true);
  await configure(admin);
  for (const [language, theme, width, height] of [
    ['en', 'light', 1440, 900],
    ['zh', 'dark', 1280, 720],
    ['en', 'dark', 390, 844],
    ['zh', 'light', 720, 420],
  ] as const) {
    const ctx = await context(browser, false, 1, language, width),
      page = await ctx.newPage();
    await page.setViewportSize({ width, height });
    await page.addInitScript((theme) => localStorage.setItem('nb.theme', theme), theme);
    await page.goto(fixture().user_url + '/games/lake-notes');
    await expect(page.locator('.lake-original #scenery')).toBeVisible();
    await noOverflow(page);
    const menus = page.locator('.lake-original .dock');
    const clipped = await menus.evaluate((el) => {
      const bounds = el.getBoundingClientRect();
      return Array.from(el.querySelectorAll('button'))
        .filter((button) => {
          const b = button.getBoundingClientRect();
          return (
            b.left < bounds.left - 1 ||
            b.right > bounds.right + 1 ||
            button.scrollWidth > button.clientWidth + 1
          );
        })
        .map((button) => ({
          text: button.textContent,
          width: button.clientWidth,
          contentWidth: button.scrollWidth,
        }));
    });
    expect(clipped).toEqual([]);
    await page.locator('.lake-original #catalogButton').click();
    const dialog = page.locator('.lake-original #catalogModal');
    await expect(dialog).toBeVisible();
    await noOverflow(page);
    await dialog.locator('#catalogClose').click();
    await expect(dialog).toBeHidden();
    if ([1440, 390].includes(width)) await screenshot(page, 'lake-' + theme + '-' + width + '.png');
    await page.goto(fixture().user_url + '/games');
    const card = page.locator('a[href="/games/lake-notes"]');
    await expect(card).toBeVisible();
    await noOverflow(page);
    const shared = language === 'zh' ? commonZh.common.lakeNotes : c;
    await page.route('**/assets/lake-notes/cover.png', (route) => route.abort('failed'));
    await page.reload();
    await card.scrollIntoViewIfNeeded();
    await expect(page.getByRole('img', { name: shared.coverAlt, exact: true })).toBeVisible();
    await expect(page.locator('.lake-cover-fallback')).toHaveText(shared.title);
    await ctx.close();
  }
  await admin.close();
});
