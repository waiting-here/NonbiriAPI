import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { directions } from '../../src/shared/lakenotes/api';
import type { ProfileView } from '../../src/user/activities/lake-notes/api';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

interface Fixture {
  user_url: string;
  admin_url: string;
  control_url: string;
  control_token: string;
  users: { id: string; level: number; cookie: { Name: string; Value: string } }[];
  admin_cookie: { Name: string; Value: string };
}
const statePath = process.env.NONBIRI_IMAGE_BROWSER_STATE!,
  base = '/api/games/lake-notes',
  adminBase = '/admin/api/games/config';
const u = en.user.lakeNotes,
  c = common.common.lakeNotes;
const fixture = (): Fixture => JSON.parse(readFileSync(statePath, 'utf8')) as Fixture;
async function context(browser: Browser, admin = false, index = 0, language = 'en', width = 1280) {
  const f = fixture(),
    origin = admin ? f.admin_url : f.user_url,
    cookie = admin ? f.admin_cookie : f.users[index].cookie;
  const ctx = await browser.newContext({ viewport: { width, height: 900 } });
  await ctx.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: admin ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await ctx.addInitScript(
    ({ language }) => {
      if (location.protocol !== 'http:' && location.protocol !== 'https:') return;
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', language === 'zh' ? 'dark' : 'light');
    },
    { language },
  );
  return ctx;
}
async function api(
  ctx: BrowserContext,
  path: string,
  method = 'GET',
  data?: unknown,
  admin = false,
  key = randomBytes(16).toString('base64url'),
) {
  const origin = admin ? fixture().admin_url : fixture().user_url;
  return ctx.request.fetch(origin + path, {
    method,
    data,
    headers: { Origin: origin, 'Idempotency-Key': key },
  });
}
async function control(ctx: BrowserContext, action: string, data = {}) {
  const f = fixture(),
    response = await ctx.request.post(f.control_url + '/' + action, {
      data,
      headers: { Authorization: 'Bearer ' + f.control_token },
    });
  expect(response.status()).toBe(200);
}
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
  await expect(page.getByRole('button', { name: u.start, exact: true })).toBeDisabled();
  const before = await profile(ctx);
  await configure(admin);
  await page.reload();
  expect((await profile(ctx)).wallet).toEqual(before.wallet);
  for (const menu of [
    'locations',
    'skills',
    'shop',
    'basket',
    'catalogTitle',
    'contracts',
  ] as const) {
    await page.getByRole('button', { name: u[menu], exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    if (menu === 'shop') expect(await dialog.locator('.lake-rod-art').count()).toBe(6);
    if (menu === 'catalogTitle')
      expect(await dialog.locator('.catalog-entry').count()).toBeGreaterThanOrEqual(18);
    await noOverflow(page);
    await page.keyboard.press('Escape');
  }
  for (const d of ['general_to_coins', 'coins_to_game', 'game_to_coins', 'coins_to_general']) {
    await page.getByLabel(u.direction, { exact: true }).selectOption(d);
    await page.getByRole('button', { name: u.quote, exact: true }).click();
    await expect(page.locator('.lake-quote')).toContainText('0.125');
    await page.getByRole('button', { name: u.exchangeConfirm, exact: true }).click();
    await expect(page.getByText(u.exchangeSaved, { exact: true })).toBeVisible();
  }
  expect((await profile(ctx)).wallet).toEqual(before.wallet);
  await page.getByRole('button', { name: u.start, exact: true }).click();
  await expect
    .poll(async () => (await profile(ctx)).cast?.ack_tick ?? 0, { timeout: 8000 })
    .toBeGreaterThanOrEqual(120);
  await page.getByRole('button', { name: u.pause, exact: true }).click();
  await expect(page.getByRole('button', { name: u.resume, exact: true })).toBeVisible();
  const saved = (await profile(ctx)).cast!;
  await control(ctx, 'restart');
  await page.reload();
  expect((await profile(ctx)).cast?.state.plan).toEqual(saved.state.plan);
  await page.getByRole('button', { name: u.resume, exact: true }).click();
  await expect
    .poll(async () => BigInt((await profile(ctx)).cast?.generation ?? '0'))
    .toBeGreaterThan(BigInt(saved.generation));
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
    ['zh', 'light', 1024, 768],
    ['en', 'dark', 390, 844],
    ['zh', 'light', 360, 740],
  ] as const) {
    const ctx = await context(browser, false, 1, language, width),
      page = await ctx.newPage();
    await page.setViewportSize({ width, height });
    await page.addInitScript((theme) => localStorage.setItem('nb.theme', theme), theme);
    await page.goto(fixture().user_url + '/games/lake-notes');
    const copy = language === 'zh' ? zh.user.lakeNotes : u;
    await expect(page.locator('.lake-game .scenery')).toBeVisible();
    await noOverflow(page);
    const menus = page.locator('.lake-dashboard .menu-actions');
    const clipped = await menus.evaluate((el) => {
      const bounds = el.getBoundingClientRect();
      return Array.from(el.querySelectorAll('button')).some((button) => {
        const b = button.getBoundingClientRect();
        return (
          b.left < bounds.left - 1 ||
          b.right > bounds.right + 1 ||
          button.scrollWidth > button.clientWidth + 1
        );
      });
    });
    expect(clipped).toBe(false);
    const before = await page.locator('.game-grid').boundingBox();
    // Exercise the production result CSS independently from randomly catching a fish.
    await page.locator('.scenery').evaluate((el) => {
      const r = document.createElement('section');
      r.className = 'lake-result';
      r.innerHTML =
        '<h2>Catch saved</h2><img src="/assets/lake-notes/fish-lake_carp.webp" alt=""><p>Carp · 40 cm · 10 XP</p><p>Treasure reward</p>';
      el.append(r);
    });
    const after = await page.locator('.game-grid').boundingBox(),
      result = await page.locator('.lake-result').boundingBox();
    expect(after?.height).toBe(before?.height);
    expect(result!.y + result!.height).toBeLessThanOrEqual(after!.y + after!.height);
    if (width >= 1280) expect(after!.y + after!.height).toBeLessThanOrEqual(height);
    await screenshot(page, 'lake-' + theme + '-' + width + '.png');
    await page.getByRole('button', { name: copy.catalogTitle, exact: true }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await noOverflow(page);
    await page.keyboard.press('Escape');
    await page.goto(fixture().user_url + '/games');
    const card = page.locator('a[href="/games/lake-notes"]');
    await expect(card).toBeVisible();
    await noOverflow(page);
    const shared = language === 'zh' ? commonZh.common.lakeNotes : c;
    await page.route('**/assets/lake-notes/cover.png', (route) => route.abort('failed'));
    await page.reload();
    await expect(page.getByRole('img', { name: shared.coverAlt, exact: true })).toBeVisible();
    await expect(page.locator('.lake-cover-fallback')).toHaveText(shared.title);
    await ctx.close();
  }
  await admin.close();
});
