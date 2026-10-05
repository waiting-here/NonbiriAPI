import { mkdirSync, readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { dirname, resolve } from 'node:path';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';

import en from '../../src/user/i18n/en.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import adminZh from '../../src/admin/i18n/zh.json' with { type: 'json' };

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
function evidencePath(name: string) {
  const directory = resolve(dirname(process.env.NONBIRI_AUDIT_BROWSER_STATE!), 'form-gaps');
  mkdirSync(directory, { recursive: true });
  return resolve(directory, name);
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the optional management fixture.',
);

async function session(
  browser: Browser,
  role: 'admin' | 1 | 5 | 6,
  narrow = false,
  language = 'en',
) {
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
  await context.addInitScript((language) => {
    localStorage.setItem('nb.lang', language);
    localStorage.setItem('nb.theme', 'light');
  }, language);
  return context;
}
async function read(context: BrowserContext, origin: string, path: string) {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok()).toBe(true);
  return response.json();
}

async function write(context: BrowserContext, origin: string, path: string, data: unknown) {
  const response = await context.request.post(origin + path, {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data,
  });
  expect(response.ok(), 'Synthetic setup ' + path + ': ' + response.status()).toBe(true);
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
      const card = panel.locator('.economy-donation-summary').filter({ has: detail });
      await card
        .getByRole('button', { name: copy.presentation.donationActions, exact: true })
        .click();
      await card.getByRole('menuitem', { name: copy.ownerPages.keys, exact: true }).click();
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
    const dialog = page.locator('.nb-expandable-panel:not([hidden])');
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

test('administrator saves, edits and deletes Gateway capabilities in narrow English', async ({
  browser,
}, info) => {
  const state = fixture(),
    context = await session(browser, 'admin', true);
  const copy = commonEn.gatewayCapabilities,
    path = '/admin/api/gateway-model-capabilities';
  const model = 'anthropic/browser-form-check',
    baseURL = 'https://gateway.example.test/ai';
  try {
    const page = await context.newPage();
    await page.goto(state.admin_url + '/settings?group=gateway');
    const panel = page
      .locator('section.card')
      .filter({ has: page.getByRole('heading', { name: copy.title, exact: true }) });
    await panel.getByRole('button', { name: copy.add, exact: true }).click();
    const form = panel.locator('form');
    await form.getByLabel(copy.baseUrl, { exact: true }).fill(baseURL + '/');
    await form.getByLabel(copy.model, { exact: true }).fill(model);
    await form.getByLabel(copy.adapter, { exact: true }).selectOption('anthropic_always_adaptive');
    await form.getByRole('checkbox', { name: copy.effort.high, exact: true }).check();
    await form.getByLabel(copy.output, { exact: true }).fill('128000');
    await form.locator('summary').filter({ hasText: copy.advanced }).click();
    await form.getByLabel(copy.cache, { exact: true }).selectOption('anthropic');
    const creating = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' && new URL(response.url()).pathname === path,
    );
    await form.getByRole('button', { name: commonEn.common.save, exact: true }).click();
    const created = await creating;
    expect(created.ok()).toBe(true);
    const row = await created.json();
    await expect(
      form.getByText(commonEn.common.operation.confirmed, { exact: true }),
    ).toBeVisible();
    expect((await read(context, state.admin_url, path)).data).toContainEqual(
      expect.objectContaining({
        id: row.id,
        base_url: baseURL,
        model,
        max_output_tokens: 128000,
        cache: 'anthropic',
      }),
    );
    await form.getByLabel(copy.output, { exact: true }).fill('127999');
    await expect(form.getByText(commonEn.common.operation.confirmed, { exact: true })).toHaveCount(
      0,
    );
    const updating = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname === path + '/' + row.id,
    );
    await form.getByRole('button', { name: commonEn.common.save, exact: true }).click();
    expect((await updating).ok()).toBe(true);
    await expect(
      form.getByText(commonEn.common.operation.confirmed, { exact: true }),
    ).toBeVisible();
    expect((await read(context, state.admin_url, path)).data).toContainEqual(
      expect.objectContaining({ id: row.id, max_output_tokens: 127999 }),
    );
    const screenshot = evidencePath('gateway-en-390.png');
    await panel.screenshot({ path: screenshot });
    await info.attach('Gateway English narrow', { path: screenshot, contentType: 'image/png' });
    await page.reload();
    await panel
      .getByRole('button', { name: copy.editTarget.replace('{{model}}', model), exact: true })
      .click();
    await expect(form.getByLabel(copy.output, { exact: true })).toHaveValue('127999');
    const remove = panel.getByRole('button', {
      name: copy.deleteTarget.replace('{{model}}', model),
      exact: true,
    });
    await remove.click();
    let dialog = page.getByRole('alertdialog');
    await expect(dialog).toHaveCount(1);
    await expect(dialog).toContainText(model);
    await expect(dialog).toContainText(baseURL);
    await dialog.getByRole('button', { name: commonEn.common.cancel, exact: true }).click();
    expect((await read(context, state.admin_url, path)).data).toContainEqual(
      expect.objectContaining({ id: row.id }),
    );
    await remove.click();
    dialog = page.getByRole('alertdialog');
    const deleting = page.waitForResponse(
      (response) =>
        response.request().method() === 'DELETE' &&
        new URL(response.url()).pathname === path + '/' + row.id,
    );
    await dialog.getByRole('button', { name: commonEn.common.remove, exact: true }).click();
    expect((await deleting).ok()).toBe(true);
    await expect(dialog).toHaveCount(0);
    expect((await read(context, state.admin_url, path)).data).not.toContainEqual(
      expect.objectContaining({ id: row.id }),
    );
  } finally {
    await context.close();
  }
});

test('administrator creates and releases a legal hold with one confirmation in wide Chinese', async ({
  browser,
}, info) => {
  const state = fixture(),
    context = await session(browser, 'admin', false, 'zh');
  const copy = adminZh.admin.legalHolds,
    target = state.management.pending_donation_id;
  const basis = 'Synthetic retention basis',
    reason = 'Synthetic retention completed';
  // The fixed disposable fixture account from auditConfig().
  const password = 'correct horse battery staple';
  try {
    const donation = await read(context, state.admin_url, '/admin/api/donations/' + target);
    expect(donation.id).toBe(target);
    const page = await context.newPage();
    await page.goto(state.admin_url + '/settings?group=legal-hold');
    await expect(page.getByRole('heading', { name: copy.title, exact: true })).toBeVisible();
    const list = page
      .locator('section.card')
      .filter({ has: page.getByRole('heading', { name: copy.title, exact: true }) });
    await list.getByLabel(copy.filters.state).selectOption('active');
    await list.getByLabel(copy.filters.objectKind).selectOption('donation');
    await expect(page).toHaveURL(/hold_state=active/);
    const create = page
      .locator('section.card')
      .filter({ has: page.getByRole('heading', { name: copy.create.title, exact: true }) });
    await create.getByLabel(copy.create.objectKind).selectOption('donation');
    await create.getByLabel(copy.create.objectReference, { exact: true }).fill(target);
    await create.getByLabel(copy.create.days, { exact: true }).fill('2');
    await create.getByLabel(copy.create.basis).fill(basis);
    await create.getByLabel(copy.create.password, { exact: true }).fill('synthetic-wrong-password');
    await create.getByRole('button', { name: copy.actions.create, exact: true }).click();
    let dialog = page.getByRole('alertdialog');
    await expect(dialog).toHaveCount(1);
    const failedElevation = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/admin/api/auth/elevate',
    );
    await dialog.getByRole('button', { name: copy.confirm.createAction, exact: true }).click();
    expect((await failedElevation).ok()).toBe(false);
    await expect(dialog).toHaveCount(0);
    await expect(create.getByLabel(copy.create.objectReference, { exact: true })).toHaveValue(
      target,
    );
    await expect(create.getByLabel(copy.create.basis)).toHaveValue(basis);
    await create.getByLabel(copy.create.password, { exact: true }).fill(password);
    await create.getByRole('button', { name: copy.actions.create, exact: true }).click();
    dialog = page.getByRole('alertdialog');
    await expect(dialog).toHaveCount(1);
    await expect(dialog).toContainText(target);
    await expect(dialog).toContainText(basis);
    const screenshot = evidencePath('legal-hold-zh-1280.png');
    await page.screenshot({ path: screenshot });
    await info.attach('Legal hold Chinese wide', { path: screenshot, contentType: 'image/png' });
    const elevating = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/admin/api/auth/elevate',
    );
    const creating = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/admin/api/legal-holds',
    );
    await dialog.getByRole('button', { name: copy.confirm.createAction, exact: true }).click();
    const elevated = await elevating;
    expect(elevated.ok()).toBe(true);
    const created = await creating;
    expect(created.ok()).toBe(true);
    const hold = await created.json();
    await expect(dialog).toHaveCount(0);
    expect(await read(context, state.admin_url, '/admin/api/legal-holds/' + hold.id)).toMatchObject(
      { object_kind: 'donation', object_ref: target, basis, state: 'active' },
    );
    const detail = page
      .locator('section.card')
      .filter({ has: page.getByRole('heading', { name: copy.detail.title, exact: true }) });
    await expect(detail).toContainText(basis);
    await detail.getByLabel(copy.release.reason, { exact: true }).fill(reason);
    await detail.getByLabel(copy.release.password, { exact: true }).fill(password);
    await detail.getByRole('button', { name: copy.actions.release, exact: true }).click();
    dialog = page.getByRole('alertdialog');
    await expect(dialog).toHaveCount(1);
    await expect(dialog).toContainText(target);
    await expect(dialog).toContainText(reason);
    const releasing = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/admin/api/legal-holds/' + hold.id + '/release',
    );
    await dialog.getByRole('button', { name: copy.confirm.releaseAction, exact: true }).click();
    expect((await releasing).ok()).toBe(true);
    await expect(dialog).toHaveCount(0);
    await list.getByLabel(copy.filters.state).selectOption('released');
    await expect(page).toHaveURL(/hold_state=released/);
    const table = page
      .locator('section.card')
      .filter({ has: page.getByRole('heading', { name: copy.title, exact: true }) })
      .locator('table');
    await expect(table.getByRole('row').filter({ hasText: target })).toContainText(
      copy.state.released,
    );
    expect(await read(context, state.admin_url, '/admin/api/legal-holds/' + hold.id)).toMatchObject(
      { object_ref: target, basis, state: 'released', end_reason: reason },
    );
  } finally {
    await context.close();
  }
});

test('level 6 can enable maintenance but only the administrator can restore service', async ({
  browser,
}) => {
  const state = fixture(),
    steward = await session(browser, 6, true),
    admin = await session(browser, 'admin');
  const copy = commonEn.common.operations.maintenance;
  try {
    const page = await steward.newPage();
    await page.goto(state.user_url + '/steward?tab=maintenance');
    await page.getByLabel(copy.reasonLabel).fill('Synthetic emergency pause');
    await page.getByRole('button', { name: copy.enableAction, exact: true }).click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toHaveCount(1);
    const enabling = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/api/steward/maintenance/enable',
    );
    await dialog.getByRole('button', { name: copy.enableAction, exact: true }).click();
    expect((await enabling).ok()).toBe(true);
    await expect(dialog).toHaveCount(0);
    await expect(page.getByText(copy.stewardCannotDisable, { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: copy.disableAction, exact: true })).toHaveCount(
      0,
    );
    const enabled = await read(admin, state.admin_url, '/admin/api/maintenance');
    expect(enabled.enabled).toBe(true);
    const forbidden = await steward.request.post(
      state.user_url + '/api/steward/maintenance/disable',
      {
        headers: { Origin: state.user_url, 'Idempotency-Key': 'browser-maintenance-deny-0001' },
        data: { expected_revision: enabled.revision, reason: 'Unpermitted synthetic restore' },
      },
    );
    expect(forbidden.status()).toBe(404);
    expect(await read(admin, state.admin_url, '/admin/api/maintenance')).toEqual(enabled);
    const adminPage = await admin.newPage();
    await adminPage.goto(state.admin_url + '/settings?group=maintenance');
    const panel = adminPage
      .locator('section.card')
      .filter({ has: adminPage.getByRole('heading', { name: copy.adminTitle, exact: true }) });
    await panel.getByLabel(copy.reasonLabel).fill('Synthetic service restored');
    await panel.getByRole('button', { name: copy.disableAction, exact: true }).click();
    const restoring = adminPage.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/admin/api/maintenance/disable',
    );
    await adminPage
      .getByRole('alertdialog')
      .getByRole('button', { name: copy.disableAction, exact: true })
      .click();
    expect((await restoring).ok()).toBe(true);
    await expect(adminPage.getByRole('alertdialog')).toHaveCount(0);
    expect((await read(admin, state.admin_url, '/admin/api/maintenance')).enabled).toBe(false);
    await page.reload();
    await expect(page.getByRole('button', { name: copy.enableAction, exact: true })).toBeVisible();
  } finally {
    await steward.close();
    await admin.close();
  }
});

test('administrator saves automatic Gateway cache defaults and level 6 reads the effective binding', async ({
  browser,
}) => {
  const state = fixture(),
    admin = await session(browser, 'admin'),
    owner = await session(browser, 1),
    steward = await session(browser, 6, true);
  const baseURL = 'https://cache.example.test/ai',
    upstream = 'anthropic/browser-cache';
  try {
    const endpoint = await write(owner, state.user_url, '/api/endpoints', {
      source: 'custom',
      connector_type: 'ai-sdk-gateway-v3',
      base_url: baseURL,
      note: 'Synthetic Gateway',
      enabled: true,
    });
    const key = await write(owner, state.user_url, '/api/endpoints/' + endpoint.id + '/keys', {
      secret: 'synthetic-gateway-cache-key',
      note: 'Synthetic cache credential',
      enabled: true,
      force_store_false: false,
      ownership_confirmed: true,
    });
    const donation = await write(owner, state.user_url, '/api/donations', {
      description: 'Synthetic Gateway cache source',
      discord_public_thanks: false,
      ownership_authorized: true,
      keys: [{ endpoint_key_id: key.id, expires_at: null }],
    });
    const donated = await read(admin, state.admin_url, '/admin/api/donations/' + donation.id);
    const keyID = donated.keys[0].id;
    if (donated.status === 'pending')
      await write(admin, state.admin_url, '/admin/api/donations/' + donation.id + '/review', {
        decision: 'approve',
        expected_revision: donated.revision,
        reason: 'Synthetic Gateway cache setup',
        key_settings: [
          {
            donation_key_id: keyID,
            enabled: true,
            price_limit: null,
            calls_limit: null,
            tokens_limit: null,
            token_reserve: 0,
            safe_note: '',
            expires_at: null,
          },
        ],
      });
    const candidatesPath = '/admin/api/donations/' + donation.id + '/keys/' + keyID + '/models';
    const candidates = await read(admin, state.admin_url, candidatesPath);
    await write(admin, state.admin_url, candidatesPath + '/manual', {
      expected_manual_catalog_revision: candidates.manual_catalog_revision,
      entries: [upstream],
    });
    const model = await write(admin, state.admin_url, '/admin/api/charity-models', {
      provider: 'browser',
      model: 'cache',
      enabled: true,
      is_mainstream: false,
      transport_rule: 'passthrough',
      flatten_tool_calls: false,
      pricing: { mode: 'per_request', user_price: '0', donor_reward: '0' },
      discount: { enabled: false, percent: 100, start_at: null, end_at: null },
    });
    const modelPath = '/admin/api/charity-models/' + model.id;
    const bindings = await read(admin, state.admin_url, modelPath + '/bindings');
    const added = await write(admin, state.admin_url, modelPath + '/bindings/batch', {
      expected_binding_revision: bindings.binding_revision,
      selections: [{ donation_key_id: keyID, upstream_model_id: upstream }],
    });
    const binding = added.bindings[0];
    const policy = {
      adapter: 'anthropic_always_adaptive',
      efforts: ['high'],
      max_output_tokens: 128000,
      storage: 'reject',
      cache: 'anthropic',
    };
    await write(admin, state.admin_url, '/admin/api/gateway-model-capabilities', {
      expected_revision: '0',
      entry: { base_url: baseURL, model: upstream, ...policy },
    });
    expect(
      (await read(admin, state.admin_url, modelPath + '/bindings')).bindings[0]
        .gateway_capabilities,
    ).toEqual(policy);
    const page = await admin.newPage();
    await page.goto(state.admin_url + '/charity?charity_section=models&charity_model=' + model.id);
    const row = page.getByRole('row').filter({ hasText: upstream });
    await expect(row).toContainText(
      commonEn.gatewayCapabilities.adapters.anthropic_always_adaptive,
    );
    await expect(row).toContainText(commonEn.gatewayCapabilities.cacheOptions.anthropic);
    await row.getByRole('button', { name: 'Request headers and body', exact: true }).click();
    await page.getByText('Request adaptation', { exact: true }).last().click();
    const editor = page.locator('section.core-card').last();
    const defaults = editor.getByRole('group', {
      name: 'Body defaults (only when absent)',
      exact: true,
    });
    await defaults.getByLabel('Configuration source').selectOption('replace');
    await defaults.getByLabel(commonEn.requestAdaptation.cache.label).selectOption('5m');
    const adaptationPath = modelPath + '/bindings/' + binding.id + '/request-adaptation';
    const saving = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname === adaptationPath,
    );
    await editor.getByRole('button', { name: 'Save request adaptation', exact: true }).click();
    const saved = await saving;
    expect(saved.ok()).toBe(true);
    expect(saved.request().postDataJSON().body_defaults.values['/cache_control']).toEqual({
      action: 'replace',
      value: { type: 'ephemeral', ttl: '5m' },
    });
    await expect(editor.getByText('Request adaptation saved.', { exact: true })).toBeVisible();
    expect(await read(admin, state.admin_url, adaptationPath)).toMatchObject({
      body_defaults: { mode: 'replace', values: { '/cache_control': { has_value: true } } },
      effective: {
        body_defaults: { source: 'binding', values: { '/cache_control': { has_value: true } } },
      },
    });

    const stewardPage = await steward.newPage();
    await stewardPage.goto(
      state.user_url + '/steward?tab=charity&charity_section=models&charity_model=' + model.id,
    );
    const stewardRow = stewardPage.getByRole('row').filter({ hasText: upstream });
    await expect(stewardRow).toContainText(
      commonEn.gatewayCapabilities.adapters.anthropic_always_adaptive,
    );
    await expect(stewardRow).toContainText(commonEn.gatewayCapabilities.cacheOptions.anthropic);
    await stewardRow.getByRole('button', { name: 'Request headers and body', exact: true }).click();
    await stewardPage.getByText('Request adaptation', { exact: true }).last().click();
    const readOnly = stewardPage.locator('section.core-card').last();
    await expect(
      readOnly.getByText('This setting is read-only for your role.', { exact: true }),
    ).toBeVisible();
    await expect(readOnly.getByLabel(commonEn.requestAdaptation.cache.label)).toBeDisabled();
    await expect(
      readOnly.getByRole('button', { name: 'Save request adaptation', exact: true }),
    ).toHaveCount(0);
    expect(
      await read(
        steward,
        state.user_url,
        '/api/steward/charity-models/' +
          model.id +
          '/bindings/' +
          binding.id +
          '/request-adaptation',
      ),
    ).toMatchObject({
      effective: {
        body_defaults: { source: 'binding', values: { '/cache_control': { has_value: true } } },
      },
    });
    await defaults.getByLabel(commonEn.requestAdaptation.cache.label).selectOption('off');
    await expect(editor.getByText('Request adaptation saved.', { exact: true })).toHaveCount(0);
    const clearing = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname === adaptationPath,
    );
    await editor.getByRole('button', { name: 'Save request adaptation', exact: true }).click();
    expect((await clearing).ok()).toBe(true);
    const cleared = await read(admin, state.admin_url, adaptationPath);
    expect(cleared.body_defaults.values).toEqual({});
    expect(cleared.effective.body_defaults.values).toEqual({});
  } finally {
    await admin.close();
    await owner.close();
    await steward.close();
  }
});

test('saved Gateway capabilities keep target actions inside the narrow card', async ({
  browser,
}, info) => {
  const state = fixture(),
    context = await session(browser, 'admin', true);
  const copy = commonEn.gatewayCapabilities,
    model = 'anthropic/browser-form-check';
  try {
    await write(context, state.admin_url, '/admin/api/gateway-model-capabilities', {
      expected_revision: '0',
      entry: {
        base_url: 'https://gateway.example.test/ai',
        model,
        adapter: 'anthropic_always_adaptive',
        efforts: ['high'],
        max_output_tokens: 127999,
        storage: 'reject',
        cache: 'anthropic',
      },
    });
    const page = await context.newPage();
    await page.goto(state.admin_url + '/settings?group=gateway');
    const panel = page
      .locator('section.card')
      .filter({ has: page.getByRole('heading', { name: copy.title, exact: true }) });
    const remove = panel.getByRole('button', {
      name: copy.deleteTarget.replace('{{model}}', model),
      exact: true,
    });
    await remove.scrollIntoViewIfNeeded();
    const card = await panel.boundingBox();
    expect(card).not.toBeNull();
    for (const action of [
      remove,
      panel.getByRole('button', { name: copy.editTarget.replace('{{model}}', model), exact: true }),
    ]) {
      const box = await action.boundingBox();
      expect(box).not.toBeNull();
      expect(box!.x).toBeGreaterThanOrEqual(card!.x);
      expect(box!.x + box!.width).toBeLessThanOrEqual(card!.x + card!.width);
    }
    const screenshot = evidencePath('gateway-en-390-layout.png');
    await panel.screenshot({ path: screenshot });
    await info.attach('Gateway English narrow header', {
      path: screenshot,
      contentType: 'image/png',
    });
  } finally {
    await context.close();
  }
});
