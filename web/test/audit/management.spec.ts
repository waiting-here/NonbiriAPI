import { readFileSync } from 'node:fs';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';

import en from '../../src/user/i18n/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: {
    old_user_id: string;
    history_record_id: string;
    history_discord_id: string;
    request_ids: string[];
    charity_model_id: string;
    automatic_donation_id: string;
    pending_donation_id: string;
    denials: Record<'client_rules' | 'charity' | 'manual', { cookie: Cookie }>;
  };
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the optional management fixture.',
);

async function session(browser: Browser, role: 'admin' | 1 | 5 | 6, narrow = false) {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((user) => user.level === role)!.cookie;
  const context = await browser.newContext({
    viewport: narrow ? { width: 390, height: 844 } : { width: 1280, height: 900 },
  });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: role === 'admin' ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return context;
}
async function read(context: BrowserContext, origin: string, path: string) {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok()).toBe(true);
  return response.json();
}

test('owner reads donation keys and details from current API responses', async ({ browser }) => {
  const state = fixture(),
    context = await session(browser, 1);
  try {
    const page = await context.newPage();
    const copy = en.user.charity;
    await page.goto(state.user_url + '/charity?tab=donations');
    const panel = page.locator('.economy-owner-pages');
    const donations = await read(context, state.user_url, '/api/donations?page=1&page_size=20');
    expect(donations.data).toHaveLength(3);
    for (const { id } of donations.data as { id: string }[]) {
      const detail = page.locator('a[href="/charity/donations/' + id + '"]');
      const card = panel.locator('.economy-donation-card').filter({ has: detail });
      await card.getByRole('button', { name: copy.ownerPages.keys, exact: true }).click();
      const keys = panel.getByRole('region', { name: copy.ownerPages.keys, exact: true });
      const response = await read(context, state.user_url, '/api/donations/' + id);
      const key = response.keys[0];
      expect(key.review).toHaveProperty('required');
      const masked = key.display_head + '…' + key.display_tail;
      await expect(keys.getByText(masked, { exact: true })).toBeVisible();
      await card.getByRole('link', { name: copy.openDonationDetail, exact: true }).click();
      await expect(page.getByText(masked, { exact: true })).toBeVisible();
      await page.getByRole('link', { name: copy.backToDonations, exact: true }).click();
    }
  } finally {
    await context.close();
  }
});

test('administrator follows retained request identity through deletion history and a Discord IP window', async ({
  browser,
}, info) => {
  const state = fixture(),
    context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
    await page.goto(state.admin_url + '/logs?request_id=' + state.management.request_ids[0]);
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Original account: ' + state.management.old_user_id);
    await expect(dialog).toContainText('Original account deleted');
    const history = dialog.getByRole('link', { name: 'View deletion history' });
    await expect(history).toHaveAttribute(
      'href',
      '/users?deleted=' + state.management.history_record_id,
    );
    await history.click();
    await expect(page.getByRole('heading', { name: 'Deleted account', exact: true })).toBeVisible();
    await expect(page.locator('main')).toContainText(state.management.history_discord_id);
    await page.goto(state.admin_url + '/abuse-audit?audit_tab=user_ips');
    await page.getByRole('button', { name: 'Start new scan', exact: true }).click();
    await expect(
      page.getByRole('heading', { name: 'Discord ID: ' + state.management.history_discord_id }),
    ).toBeVisible();
    await expect(
      page.getByRole('link', { name: 'View original requests in this window' }).first(),
    ).toHaveAttribute('href', /user_id=/);
    await page.screenshot({
      path: info.outputPath('historical-window-desktop.png'),
      fullPage: true,
    });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({
      path: info.outputPath('historical-window-narrow.png'),
      fullPage: true,
    });
  } finally {
    await context.close();
  }
});

test('level 5 saves only message-role fields for its mainstream model and cannot open history APIs', async ({
  browser,
}, info) => {
  const state = fixture(),
    context = await session(browser, 5, true);
  try {
    const page = await context.newPage();
    await page.goto(
      state.user_url + '/steward?tab=charity&charity_model=' + state.management.charity_model_id,
    );
    const fallback = page.getByLabel('Default action for unlisted roles');
    await page.locator('details').filter({ has: fallback }).locator('summary').click();
    await expect(fallback).toBeVisible();
    await expect(fallback).toHaveValue('native');
    await page.getByRole('button', { name: 'Add a role rule', exact: true }).click();
    await expect(page.getByLabel('Role name', { exact: true })).toBeFocused();
    await page.keyboard.type('developer');
    await expect(page.getByLabel('Role name', { exact: true })).toBeVisible();
    await page.getByRole('combobox', { name: 'Handling action', exact: true }).selectOption('user');
    await fallback.selectOption('reject');
    const saved = page.waitForResponse(
      (response) =>
        response.request().method() === 'PATCH' &&
        new URL(response.url()).pathname.endsWith(
          '/charity-models/' + state.management.charity_model_id,
        ),
    );
    await page.getByRole('button', { name: 'Save model', exact: true }).click();
    const result = await saved;
    expect(result.ok()).toBe(true);
    const intent = result.request().postDataJSON();
    expect(Object.keys(intent).sort()).toEqual(['expected_revision', 'role_policy']);
    expect(intent.role_policy).toEqual({ default_action: 'reject', rules: { developer: 'user' } });
    await expect(page.getByText('Message role settings saved.', { exact: true })).toBeVisible();
    await fallback.selectOption('native');
    await expect(page.getByText('Message role settings saved.', { exact: true })).toHaveCount(0);
    await page.screenshot({ path: info.outputPath('steward-role-narrow.png'), fullPage: true });
    const model = await read(
      context,
      state.user_url,
      '/api/steward/charity-models/' + state.management.charity_model_id,
    );
    expect(model.role_policy ?? model.data?.role_policy).toEqual(intent.role_policy);
    const forbidden = await context.request.get(state.user_url + '/api/steward/logs');
    expect(forbidden.status()).toBe(403);
    await expect(page.getByRole('tab', { name: 'Logs', exact: true })).toHaveCount(0);
  } finally {
    await context.close();
  }
});

test('level 6 forcibly rejects an automatic donation through one consequence confirmation', async ({
  browser,
}) => {
  const state = fixture(),
    context = await session(browser, 6);
  try {
    const page = await context.newPage();
    await page.goto(
      state.user_url + '/steward?tab=charity&donation_id=' + state.management.automatic_donation_id,
    );
    const action = page.getByRole('button', {
      name: 'Reject an automatically approved donation',
      exact: true,
    });
    await expect(action).toBeDisabled();
    await page.getByLabel('Reason', { exact: true }).fill('Synthetic review finding');
    await action.click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toContainText('New community calls will stop');
    await expect(dialog).toContainText('Synthetic review finding');
    const response = page.waitForResponse(
      (item) =>
        item.request().method() === 'POST' &&
        new URL(item.url()).pathname.endsWith(
          '/donations/' + state.management.automatic_donation_id + '/review',
        ),
    );
    await dialog
      .getByRole('button', { name: 'Reject an automatically approved donation', exact: true })
      .click();
    const completed = await response;
    expect(completed.ok()).toBe(true);
    expect(completed.request().postDataJSON().decision).toBe('force_reject');
    expect(completed.request().postDataJSON()).not.toHaveProperty('key_settings');
    await expect(dialog).toHaveCount(0);
  } finally {
    await context.close();
  }
});

test('verified denial grants show current-language automatic labels and preserve manual notes', async ({
  browser,
}) => {
  const state = fixture();
  for (const kind of ['client_rules', 'charity', 'manual'] as const) {
    const context = await browser.newContext();
    try {
      const cookie = state.management.denials[kind].cookie;
      await context.addCookies([
        {
          name: cookie.Name,
          value: cookie.Value,
          domain: new URL(state.user_url).hostname,
          path: '/api/auth',
          httpOnly: true,
          sameSite: 'Lax',
        },
      ]);
      await context.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
      const page = await context.newPage();
      await page.goto(state.user_url + '/access-denied#restrictions=untrusted');
      await expect(
        page.getByRole('heading', { name: 'Current access restriction reasons' }),
      ).toBeVisible();
      await expect(page).toHaveURL(state.user_url + '/access-denied');
      if (kind === 'client_rules')
        await expect(
          page.getByText('The following client usage rules were matched:', { exact: true }),
        ).toBeVisible();
      const facts = await read(context, state.user_url, '/api/auth/access-denied-reasons');
      for (const item of facts.items)
        if (item.automatic_reason?.manual_text)
          await expect(page.locator('main')).toContainText(item.automatic_reason.manual_text);
      if (kind === 'manual')
        for (const item of facts.items)
          await expect(page.locator('main')).toContainText(item.reason);
      await page.getByRole('button', { name: /中文|Chinese/ }).click();
      if (kind === 'client_rules')
        await expect(page.getByText('匹配了以下客户端使用规则：', { exact: true })).toBeVisible();
    } finally {
      await context.close();
    }
  }
});
