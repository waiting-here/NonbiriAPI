import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import type { Period, PeriodInput } from '../../src/shared/lakenotes/api';
import type { ProfileView } from '../../src/user/activities/lake-notes/api';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import adminEn from '../../src/admin/i18n/en.json' with { type: 'json' };
import adminZh from '../../src/admin/i18n/zh.json' with { type: 'json' };
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
  base = '/api/limited-activities/lake-notes',
  adminBase = '/admin/api/limited-activities/lake-notes';
const u = en.user.lakeNotes,
  a = adminEn.admin.lakeNotes,
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
async function period(
  ctx: BrowserContext,
  name: string,
  start: number,
  end: number,
): Promise<Period> {
  const existing = await (
    await api(ctx, adminBase + '/periods?page=1&page_size=20', 'GET', undefined, true)
  ).json();
  for (const p of existing.items as Period[])
    if (p.status === 'published' && p.starts_at < end && start < p.ends_at) {
      const { id, revision, ...input } = p;
      expect(
        (
          await api(
            ctx,
            adminBase + '/periods/' + id,
            'PUT',
            { ...input, expected_revision: revision, status: 'cancelled' },
            true,
          )
        ).status(),
      ).toBe(200);
    }
  const directions = ['coins_to_general', 'general_to_coins', 'coins_to_game', 'game_to_coins'];
  const exchanges = Object.fromEntries(
    directions.map((direction) => [
      direction,
      {
        enabled: true,
        source_amount: direction.startsWith('coins') ? '3' : '125',
        target_amount: direction.startsWith('coins') ? '125' : '3',
      },
    ]),
  );
  const response = await api(
    ctx,
    adminBase + '/periods',
    'POST',
    {
      expected_revision: '0',
      name,
      status: 'published',
      starts_at: start,
      ends_at: end,
      entry_fee_milli: '125',
      exchanges,
    } as PeriodInput,
    true,
  );
  expect(response.status()).toBe(200);
  return response.json();
}
async function screenshot(page: Page, name: string) {
  const folder = join(dirname(statePath), 'preview');
  mkdirSync(folder, { recursive: true });
  await page.screenshot({ path: join(folder, name), fullPage: true });
}
async function noOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
    true,
  );
}

test('real entry, all original menus, exact exchanges and cast recovery', async ({ browser }) => {
  const admin = await context(browser, true),
    ctx = await context(browser),
    page = await ctx.newPage(),
    now = Math.floor(Date.now() / 1000);
  const configResponse = await api(admin, adminBase, 'GET', undefined, true),
    config = await configResponse.json();
  expect(configResponse.status()).toBe(200);
  expect(config.status).toBe('unconfigured');
  expect(config.visible).toBe(false);
  await page.goto(fixture().user_url + '/activities/lake-notes');
  await expect(page.getByText(u.closed, { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: u.start, exact: true })).toBeDisabled();
  const first = await period(admin, 'Morning lake', now - 10, now + 600);
  const opened = await api(
    admin,
    adminBase,
    'PUT',
    {
      expected_revision: config.revision,
      visible: true,
      paused: false,
      starts_at: now - 10,
      ends_at: now + 1200,
      module_config: {},
    },
    true,
  );
  expect(opened.status()).toBe(200);
  await page.reload();
  await expect(page.getByRole('button', { name: u.enter, exact: true })).toBeEnabled();
  const before = await profile(ctx);
  await page.getByRole('button', { name: u.enter, exact: true }).click();
  await expect(page.getByText(u.paid, { exact: true })).toBeVisible();
  const paid = await profile(ctx);
  expect(BigInt(before.wallet.general_milli) - BigInt(paid.wallet.general_milli)).toBe(125n);
  await page.reload();
  expect((await profile(ctx)).wallet).toEqual(paid.wallet);
  for (const menu of [
    'locations',
    'skills',
    'shop',
    'basket',
    'catalogTitle',
    'contracts',
  ] as const) {
    const label = u[menu];
    await page.getByRole('button', { name: label, exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await noOverflow(page);
    if (menu === 'shop')
      expect(await dialog.locator('.shop-item').count()).toBeGreaterThanOrEqual(14);
    if (menu === 'catalogTitle') expect(await dialog.locator('.catalog-entry').count()).toBe(18);
    await page.keyboard.press('Escape');
    await expect(dialog).not.toBeVisible();
    await expect(page.getByRole('button', { name: label, exact: true })).toBeFocused();
  }
  await page.getByLabel(u.direction, { exact: true }).selectOption('general_to_coins');
  await page.getByLabel(u.batches, { exact: true }).fill('2');
  await page.getByRole('button', { name: u.quote, exact: true }).click();
  await expect(page.locator('.lake-quote')).toContainText('0.25');
  await expect(page.locator('.lake-quote')).toContainText('6');
  let lost = true;
  const keys: string[] = [];
  await page.route('**' + base + '/exchange', async (route) => {
    keys.push(route.request().headers()['idempotency-key']);
    if (lost) {
      lost = false;
      await route.fetch();
      await route.abort('failed');
    } else await route.continue();
  });
  await page.getByRole('button', { name: u.exchangeConfirm, exact: true }).click();
  await expect(page.getByText(u.exchangeUnknown, { exact: true })).toBeVisible();
  await page.getByRole('button', { name: u.retry, exact: true }).click();
  await expect(page.getByText(u.exchangeSaved, { exact: true })).toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
  const exchanged = await profile(ctx);
  expect(exchanged.profile.coins).toBe(String(BigInt(paid.profile.coins) + 6n));
  expect(BigInt(paid.wallet.general_milli) - BigInt(exchanged.wallet.general_milli)).toBe(250n);
  await page.getByRole('button', { name: u.start, exact: true }).click();
  await expect(page.getByRole('button', { name: u.pause, exact: true })).toBeVisible();
  await expect
    .poll(async () => Number((await profile(ctx)).cast?.ack_tick ?? 0), { timeout: 8000 })
    .toBeGreaterThanOrEqual(120);
  await page.getByRole('button', { name: u.pause, exact: true }).click();
  await expect(page.getByRole('button', { name: u.resume, exact: true })).toBeVisible();
  const paused = await profile(ctx),
    cast = paused.cast!;
  expect(cast.paused).toBe(true);
  await control(ctx, 'restart');
  await page.reload();
  await expect(page.getByRole('button', { name: u.resume, exact: true })).toBeVisible();
  expect((await profile(ctx)).cast?.state.plan).toEqual(cast.state.plan);
  await page.getByRole('button', { name: u.resume, exact: true }).click();
  await expect
    .poll(async () => BigInt((await profile(ctx)).cast?.generation ?? '0'))
    .toBeGreaterThan(BigInt(cast.generation));
  const other = await context(browser),
    otherPage = await other.newPage();
  await otherPage.goto(fixture().user_url + '/activities/lake-notes');
  await otherPage.getByRole('button', { name: u.takeOver, exact: true }).click();
  await expect(page.getByText(u.controllerLost, { exact: true })).toBeVisible();
  await otherPage.getByRole('button', { name: u.pause, exact: true }).click();
  const closedConfig = await (await api(admin, adminBase, 'GET', undefined, true)).json();
  expect(
    (
      await api(
        admin,
        adminBase,
        'PUT',
        {
          expected_revision: closedConfig.revision,
          visible: true,
          paused: true,
          starts_at: now - 10,
          ends_at: now + 1200,
          module_config: {},
        },
        true,
      )
    ).status(),
  ).toBe(200);
  await page.reload();
  await expect(page.getByText(u.closed, { exact: true })).toBeVisible();
  expect((await profile(ctx)).entitlement?.period_id).toBe(first.id);
  await screenshot(page, 'user-closed-en-light-wide.png');
  await noOverflow(page);
  await page.goto(fixture().user_url + '/credits');
  await expect(page.getByText(u.ledgerEntry, { exact: true })).toBeVisible();
  await expect(page.getByText(u.ledgerExchange, { exact: true })).toBeVisible();
  await other.close();
  await ctx.close();
  await admin.close();
});

test('administrator draft defaults, lost-response retry and bilingual narrow layout', async ({
  browser,
}) => {
  const ctx = await context(browser, true),
    page = await ctx.newPage(),
    now = Math.floor(Date.now() / 1000);
  const site = await (await api(ctx, '/admin/api/site-config', 'GET', undefined, true)).json();
  expect(
    (
      await api(
        ctx,
        '/admin/api/site-config',
        'PATCH',
        { expected_revision: site.revision, values: { site_timezone_offset_minutes: 0 } },
        true,
      )
    ).status(),
  ).toBe(200);
  await page.goto(fixture().admin_url + '/limited-activities/lake-notes');
  await page.getByRole('button', { name: a.newPeriod, exact: true }).click();
  const editor = page.locator('.lake-period-editor');
  expect(await editor.getByRole('checkbox', { name: a.enabled, exact: true }).count()).toBe(4);
  for (const toggle of await editor.getByRole('checkbox', { name: a.enabled, exact: true }).all())
    await expect(toggle).not.toBeChecked();
  await editor.getByLabel(a.name, { exact: true }).fill('Later lake');
  await editor
    .getByLabel(a.start, { exact: true })
    .fill(new Date((now + 1200) * 1000).toISOString().slice(0, 19));
  await editor
    .getByLabel(a.end, { exact: true })
    .fill(new Date((now + 1800) * 1000).toISOString().slice(0, 19));
  let lost = true;
  const keys: string[] = [];
  await page.route('**' + adminBase + '/periods', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue();
      return;
    }
    keys.push(route.request().headers()['idempotency-key']);
    if (lost) {
      lost = false;
      await route.fetch();
      await route.abort('failed');
    } else await route.continue();
  });
  await editor.getByRole('button', { name: a.saveDraft, exact: true }).click();
  await expect(page.getByText(a.unknown, { exact: true })).toBeVisible();
  await expect(editor.getByLabel(a.name, { exact: true })).toBeDisabled();
  await expect(editor.getByRole('button', { name: a.cancelEdit, exact: true })).toBeDisabled();
  await editor.getByRole('button', { name: a.retry, exact: true }).click();
  await expect(page.getByText(a.saved, { exact: true }).first()).toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
  const stored = await (
    await api(ctx, adminBase + '/periods?page=1&page_size=20', 'GET', undefined, true)
  ).json();
  const draft = (stored.items as Period[]).find((p) => p.name === 'Later lake')!;
  expect(draft.status).toBe('draft');
  expect(draft.entry_fee_milli).toBe(null);
  expect(Object.values(draft.exchanges).every((setting) => !setting.enabled)).toBe(true);
  await ctx.close();
  for (const [language, theme, width] of [
    ['en', 'light', 1280],
    ['zh', 'dark', 390],
  ] as const) {
    const viewing = await context(browser, true, 0, language, width),
      view = await viewing.newPage(),
      copy = language === 'zh' ? adminZh.admin.lakeNotes : a;
    await view.goto(fixture().admin_url + '/limited-activities/lake-notes');
    await view.getByRole('button', { name: copy.edit, exact: true }).first().click();
    await noOverflow(view);
    await screenshot(view, `admin-${language}-${theme}-${width}.png`);
    await viewing.close();
  }
});

test('real administrator period editing and natural units', async ({ browser }) => {
  const ctx = await context(browser, true),
    page = await ctx.newPage();
  const site = await (await api(ctx, '/admin/api/site-config', 'GET', undefined, true)).json();
  expect(
    (
      await api(
        ctx,
        '/admin/api/site-config',
        'PATCH',
        { expected_revision: site.revision, values: { site_timezone_offset_minutes: 0 } },
        true,
      )
    ).status(),
  ).toBe(200);
  const now = Math.floor(Date.now() / 1000);
  await period(ctx, 'Editor lake', now - 10, now + 600);
  await page.goto(fixture().admin_url + '/limited-activities/lake-notes');
  await expect(page.getByRole('heading', { name: a.title, exact: true })).toBeVisible();
  await page
    .locator('.lake-period-list article')
    .filter({ has: page.getByRole('heading', { name: 'Editor lake', exact: true }) })
    .getByRole('button', { name: a.edit, exact: true })
    .click();
  await page.getByLabel(a.fee, { exact: true }).fill('0.375');
  await page.getByLabel(a.name, { exact: true }).fill('Evening lake');
  const saving = page.waitForResponse(
    (response) =>
      response.request().method() === 'PUT' &&
      new URL(response.url()).pathname.startsWith(adminBase + '/periods/'),
  );
  await page.getByRole('button', { name: a.publish, exact: true }).click();
  const saveResponse = await saving;
  expect(saveResponse.status(), await saveResponse.text()).toBe(200);
  await expect(page.getByText(a.saved, { exact: true }).first()).toBeVisible();
  expect(
    (
      await (
        await api(ctx, adminBase + '/periods?page=1&page_size=20', 'GET', undefined, true)
      ).json()
    ).items.find((value: Period) => value.name === 'Evening lake')?.entry_fee_milli,
  ).toBe('375');
  await page.getByLabel(a.fee, { exact: true }).fill('0.5');
  await page.getByRole('button', { name: a.publish, exact: true }).click();
  await expect
    .poll(
      async () =>
        (
          await (
            await api(ctx, adminBase + '/periods?page=1&page_size=20', 'GET', undefined, true)
          ).json()
        ).items.find((value: Period) => value.name === 'Evening lake')?.entry_fee_milli,
    )
    .toBe('500');
  await screenshot(page, 'admin-en-light-wide.png');
  await noOverflow(page);
  await ctx.close();
});

test('all four real exchanges, bilingual themes, narrow menus and cover fallback', async ({
  browser,
}) => {
  const admin = await context(browser, true),
    now = Math.floor(Date.now() / 1000),
    p = await period(admin, 'Presentation lake', now - 10, now + 600);
  const config = await (await api(admin, adminBase, 'GET', undefined, true)).json();
  expect(
    (
      await api(
        admin,
        adminBase,
        'PUT',
        {
          expected_revision: config.revision,
          visible: true,
          paused: false,
          starts_at: now - 10,
          ends_at: now + 700,
          module_config: {},
        },
        true,
      )
    ).status(),
  ).toBe(200);
  const ctx = await context(browser, false, 1),
    page = await ctx.newPage();
  await page.goto(fixture().user_url + '/activities/lake-notes');
  await page.getByRole('button', { name: u.enter, exact: true }).click();
  await expect(page.getByText(u.paid, { exact: true })).toBeVisible();
  const initial = await profile(ctx);
  for (const direction of [
    'general_to_coins',
    'coins_to_game',
    'game_to_coins',
    'coins_to_general',
  ]) {
    await page.getByLabel(u.direction, { exact: true }).selectOption(direction);
    await page.getByLabel(u.batches, { exact: true }).fill('1');
    await page.getByRole('button', { name: u.quote, exact: true }).click();
    await expect(page.locator('.lake-quote')).toContainText('0.125');
    await expect(page.locator('.lake-quote')).toContainText('3');
    const response = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === base + '/exchange' &&
        response.request().method() === 'POST',
    );
    await page.getByRole('button', { name: u.exchangeConfirm, exact: true }).click();
    expect((await response).status()).toBe(200);
    await expect(page.getByText(u.exchangeSaved, { exact: true })).toBeVisible();
  }
  expect((await profile(ctx)).wallet).toEqual(initial.wallet);
  expect((await profile(ctx)).profile.coins).toBe(initial.profile.coins);
  await page.getByRole('button', { name: u.start, exact: true }).click();
  await expect(page.getByRole('button', { name: u.pause, exact: true })).toBeVisible();
  const track = page.getByRole('button', { name: u.track, exact: true });
  await track.focus();
  await page.keyboard.down('Space');
  await page.keyboard.up('Space');
  const hold = page.getByRole('button', { name: u.hold, exact: true });
  await hold.dispatchEvent('pointerdown', { pointerId: 1 });
  await hold.dispatchEvent('pointercancel', { pointerId: 1 });
  await page.evaluate(() => window.dispatchEvent(new Event('blur')));
  await expect.poll(async () => (await profile(ctx)).cast?.paused).toBe(true);
  await ctx.close();
  for (const [language, theme, width] of [
    ['en', 'light', 1280],
    ['zh', 'dark', 390],
    ['zh', 'light', 1280],
    ['en', 'dark', 390],
  ] as const) {
    const viewing = await context(browser, false, 1, language, width);
    await viewing.addInitScript((theme) => {
      if (location.protocol === 'http:' || location.protocol === 'https:')
        localStorage.setItem('nb.theme', theme);
    }, theme);
    const view = await viewing.newPage(),
      copy = language === 'zh' ? zh.user.lakeNotes : u,
      shared = language === 'zh' ? commonZh.common.lakeNotes : c;
    await view.goto(fixture().user_url + '/activities/lake-notes');
    await expect(
      view.getByRole('heading', { name: shared.title, exact: true }).first(),
    ).toBeVisible();
    await noOverflow(view);
    await screenshot(view, `user-${language}-${theme}-${width}.png`);
    await view.getByRole('button', { name: copy.shop, exact: true }).click();
    const dialog = view.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByText(copy.baits, { exact: true }).scrollIntoViewIfNeeded();
    await screenshot(view, `shop-${language}-${theme}-${width}.png`);
    await noOverflow(view);
    await view.keyboard.press('Escape');
    await view.getByRole('button', { name: copy.catalogTitle, exact: true }).click();
    expect(await view.getByRole('dialog').locator('.catalog-entry').count()).toBe(18);
    const tabs = view.getByRole('dialog').locator('.catalog-tabs button');
    for (let i = 0; i < 3; i++) {
      await tabs.nth(i).click();
      expect(await view.getByRole('dialog').locator('.catalog-entry').count()).toBe(18);
    }
    await view.keyboard.press('Escape');
    await view.goto(fixture().user_url + '/activities');
    const cover = view.locator('.lake-cover');
    await cover.scrollIntoViewIfNeeded();
    await expect(cover).toBeVisible();
    expect(await cover.getAttribute('loading')).toBe('lazy');
    expect(await cover.evaluate((img) => (img as HTMLImageElement).naturalWidth)).toBeGreaterThan(
      0,
    );
    expect(await cover.getAttribute('alt')).toBe(shared.coverAlt);
    const box = await cover.boundingBox();
    expect(box!.width / box!.height).toBeCloseTo(16 / 9, 1);
    await view.route('**/assets/lake-notes/cover.png', (route) => route.abort('failed'));
    await view.reload();
    await expect(view.getByRole('img', { name: shared.coverAlt, exact: true })).toBeVisible();
    await expect(view.locator('.lake-cover-fallback')).toHaveText(shared.title);
    await noOverflow(view);
    await screenshot(view, `directory-fallback-${language}-${theme}-${width}.png`);
    await viewing.close();
  }
  expect(p.status).toBe('published');
  await admin.close();
});

test('unknown exchange survives a closed period refresh and resumes in the next period', async ({
  browser,
}) => {
  const admin = await context(browser, true),
    ctx = await context(browser, false, 2),
    page = await ctx.newPage(),
    now = Math.floor(Date.now() / 1000),
    first = await period(admin, 'Closing lake', now - 10, now + 600);
  const config = await (await api(admin, adminBase, 'GET', undefined, true)).json();
  expect(
    (
      await api(
        admin,
        adminBase,
        'PUT',
        {
          expected_revision: config.revision,
          visible: true,
          paused: false,
          starts_at: now - 10,
          ends_at: now + 900,
          module_config: {},
        },
        true,
      )
    ).status(),
  ).toBe(200);
  await page.goto(fixture().user_url + '/activities/lake-notes');
  await page.getByRole('button', { name: u.enter, exact: true }).click();
  await expect(page.getByText(u.paid, { exact: true })).toBeVisible();
  const entered = await profile(ctx);
  expect(
    (
      await api(ctx, base + '/casts', 'POST', { expected_profile_revision: entered.revision })
    ).status(),
  ).toBe(200);
  await page.reload();
  await expect(page.getByRole('button', { name: u.checkSaved, exact: true })).toBeVisible();
  await page.getByLabel(u.direction, { exact: true }).selectOption('general_to_coins');
  await page.getByRole('button', { name: u.quote, exact: true }).click();
  await expect(page.locator('.lake-quote')).toBeVisible();
  let lost = true;
  const keys: string[] = [];
  await page.route('**' + base + '/exchange', async (route) => {
    keys.push(route.request().headers()['idempotency-key']);
    if (lost) {
      lost = false;
      await route.fetch();
      await route.abort('failed');
    } else await route.continue();
  });
  await page.getByRole('button', { name: u.exchangeConfirm, exact: true }).click();
  await expect(page.getByText(u.exchangeUnknown, { exact: true })).toBeVisible();
  const { id, revision, ...input } = first;
  expect(
    (
      await api(
        admin,
        adminBase + '/periods/' + id,
        'PUT',
        { ...input, expected_revision: revision, status: 'cancelled' },
        true,
      )
    ).status(),
  ).toBe(200);
  await page.getByRole('button', { name: u.checkSaved, exact: true }).click();
  await expect(page.getByText(u.closed, { exact: true })).toBeVisible();
  await expect(page.getByText(u.exchangeUnknown, { exact: true })).toBeVisible();
  await page.getByRole('button', { name: u.retry, exact: true }).click();
  await expect(page.getByText(u.exchangeSaved, { exact: true })).toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
  const closed = await profile(ctx);
  expect(closed.profile.coins).toBe('3');
  expect(closed.cast?.paused).toBe(true);
  const next = await period(admin, 'Next lake', now - 10, now + 600);
  await page.reload();
  await page.getByRole('button', { name: u.enter, exact: true }).click();
  await expect(page.getByText(u.paid, { exact: true })).toBeVisible();
  const paid = await profile(ctx);
  expect(paid.entitlement?.period_id).toBe(next.id);
  expect(BigInt(closed.wallet.general_milli) - BigInt(paid.wallet.general_milli)).toBe(125n);
  expect(paid.cast?.state.plan).toEqual(closed.cast?.state.plan);
  await page.getByRole('button', { name: u.resume, exact: true }).click();
  await expect
    .poll(async () => BigInt((await profile(ctx)).cast?.generation ?? '0'))
    .toBeGreaterThan(BigInt(closed.cast!.generation));
  await page.getByRole('button', { name: u.pause, exact: true }).click();
  await ctx.close();
  await admin.close();
});
