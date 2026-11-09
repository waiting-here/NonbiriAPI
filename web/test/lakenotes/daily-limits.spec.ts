import { mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { test, expect, type BrowserContext } from '@playwright/test';
import { directions } from '../../src/shared/lakenotes/api';
import userEn from '../../src/user/i18n/en.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import { api, context, fixture, statePath } from './real-fixture';

const base = '/api/games/lake-notes';
const u = userEn.user.lakeNotes;
const c = commonEn.common.lakeNotes;
async function cap(ctx: BrowserContext, limit: string) {
  const config = await (await api(ctx, '/admin/api/games/config', 'GET', undefined, true)).json();
  const response = await api(
    ctx,
    '/admin/api/games/config',
    'PATCH',
    {
      expected_revision: config.revision,
      master_enabled: true,
      lakenotes: {
        enabled: true,
        exchanges: Object.fromEntries(
          directions.map((direction) => [
            direction,
            {
              enabled: true,
              source_amount: direction.startsWith('coins') ? '3' : '125',
              target_amount: direction.startsWith('coins') ? '125' : '3',
              daily_source_limit: limit,
            },
          ]),
        ),
      },
    },
    true,
  );
  expect(response.status(), await response.text()).toBe(200);
}

test('daily exchange spending, configuration and remaining allowance in both screen sizes and themes', async ({
  browser,
}) => {
  const admin = await context(browser, true);
  const user = await context(browser);
  const initial = await (
    await api(admin, '/admin/api/games/config', 'GET', undefined, true)
  ).json();
  try {
    await cap(admin, '1000000');
    const beforeResponse = await api(user, base + '/exchange/quote', 'POST', {
      direction: 'general_to_coins',
      quantity: '1',
    });
    expect(beforeResponse.status(), await beforeResponse.text()).toBe(200);
    const before = await beforeResponse.json();
    const exchange = await api(user, base + '/exchange', 'POST', {
      direction: 'general_to_coins',
      quantity: '1',
      expected_settings_revision: before.settings_revision,
      expected_profile_revision: before.profile_revision,
    });
    expect(exchange.status(), await exchange.text()).toBe(200);
    const result = await exchange.json();
    expect(
      BigInt(before.daily_source_remaining) - BigInt(result.receipt.daily_source_remaining),
    ).toBe(125n);
    const exchangePage = await user.newPage();
    await exchangePage.goto(fixture().user_url + '/games/lake-notes');
    await exchangePage.getByLabel(u.direction, { exact: true }).selectOption('general_to_coins');
    await exchangePage.getByRole('button', { name: u.quote, exact: true }).click();
    await exchangePage.getByRole('button', { name: u.exchangeConfirm, exact: true }).click();
    await expect(exchangePage.getByText(u.exchangeSaved, { exact: true })).toBeVisible();
    await expect(exchangePage.locator('.lake-daily-allowance')).toContainText(
      'Available to spend today:',
    );
    await expect(
      exchangePage.getByRole('button', { name: u.exchangeConfirm, exact: true }),
    ).toBeDisabled();
    await exchangePage.close();
    const folder = join(dirname(statePath), 'preview');
    mkdirSync(folder, { recursive: true });
    for (const width of [1440, 390]) {
      for (const theme of ['light', 'dark']) {
        const adminContext = await context(browser, true, 0, 'en', width);
        await adminContext.addInitScript((theme) => localStorage.setItem('nb.theme', theme), theme);
        const management = await adminContext.newPage();
        await management.goto(fixture().admin_url + '/games');
        await management
          .getByRole('button', { name: 'Lake Notes Game settings', exact: true })
          .click();
        const fields = management.locator('.lake-period-form');
        await expect(fields.getByPlaceholder('Unlimited')).toHaveCount(4);
        await fields.scrollIntoViewIfNeeded();
        expect(
          await management.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
        ).toBe(true);
        await fields.screenshot({
          path: join(folder, `lake-daily-admin-${width}-${theme}.png`),
          animations: 'disabled',
        });
        await management.screenshot({
          path: join(folder, `lake-daily-admin-${width}-${theme}-viewport.png`),
          animations: 'disabled',
        });
        const save = management.getByRole('button', { name: 'Save all changes', exact: true });
        await save.scrollIntoViewIfNeeded();
        await expect(save).toBeVisible();
        await management.screenshot({
          path: join(folder, `lake-daily-admin-${width}-${theme}-footer.png`),
          animations: 'disabled',
        });
        await adminContext.close();
        const userContext = await context(browser, false, 0, 'en', width);
        await userContext.addInitScript((theme) => localStorage.setItem('nb.theme', theme), theme);
        const page = await userContext.newPage();
        await page.goto(fixture().user_url + '/games/lake-notes');
        await page.getByLabel(u.direction, { exact: true }).selectOption('general_to_coins');
        await page.getByRole('button', { name: u.quote, exact: true }).click();
        await expect(page.locator('.lake-daily-allowance')).toContainText(
          'Available to spend today:',
        );
        await expect(page.locator('.lake-daily-allowance')).toContainText(c.general);
        await expect(
          page.getByRole('button', { name: u.exchangeConfirm, exact: true }),
        ).toBeEnabled();
        await page.locator('.lake-daily-allowance').scrollIntoViewIfNeeded();
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
        ).toBe(true);
        await page.screenshot({
          path: join(folder, `lake-daily-user-${width}-${theme}.png`),
          animations: 'disabled',
        });
        await userContext.close();
      }
    }
    const management = await admin.newPage();
    await management.goto(fixture().admin_url + '/games');
    await management.getByRole('button', { name: 'Lake Notes Game settings', exact: true }).click();
    const section = management.locator('.lake-exchange-setting').filter({
      has: management.getByRole('heading', { name: c.general_to_coins, exact: true }),
    });
    await section.getByLabel(/Daily spending limit/).fill('0');
    const saved = management.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/admin/api/games/config' &&
        response.request().method() === 'PATCH',
    );
    await management.getByRole('button', { name: 'Save all changes', exact: true }).click();
    expect((await saved).status()).toBe(200);
    await expect(management.locator('.lake-period-form')).toHaveCount(0);
    await management.getByRole('button', { name: 'Lake Notes Game settings', exact: true }).click();
    await expect(section.getByLabel(/Daily spending limit/)).toHaveValue('0');
    const page = await user.newPage();
    await page.goto(fixture().user_url + '/games/lake-notes');
    await page.getByLabel(u.direction, { exact: true }).selectOption('general_to_coins');
    const batches = page.getByLabel(u.batches, { exact: true });
    await batches.fill('2');
    await page.getByRole('button', { name: u.quote, exact: true }).click();
    await expect(page.locator('.lake-daily-allowance')).toContainText(u.dailyLimitReached);
    await expect(page.getByRole('button', { name: u.exchangeConfirm, exact: true })).toBeDisabled();
    await expect(batches).toHaveValue('2');
    await section.getByLabel(/Daily spending limit/).fill('');
    const reset = management.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/admin/api/games/config' &&
        response.request().method() === 'PATCH',
    );
    await management.getByRole('button', { name: 'Save all changes', exact: true }).click();
    expect((await reset).status()).toBe(200);
    const unlimited = await (
      await api(user, base + '/exchange/quote', 'POST', {
        direction: 'general_to_coins',
        quantity: '1',
      })
    ).json();
    expect(unlimited.daily_source_remaining).toBeNull();
  } finally {
    const current = await (
      await api(admin, '/admin/api/games/config', 'GET', undefined, true)
    ).json();
    const restored = await api(
      admin,
      '/admin/api/games/config',
      'PATCH',
      {
        expected_revision: current.revision,
        master_enabled: initial.master_enabled,
        lakenotes: initial.lakenotes,
      },
      true,
    );
    const status = restored.status(),
      message = await restored.text();
    await Promise.all([user.close(), admin.close()]);
    expect(status, message).toBe(200);
  }
});
