import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import user from '../../src/user/i18n/en.json' with { type: 'json' };
import type {
  SmartBackoffFields,
  SmartBackoffProfile,
  SmartBackoffRule,
} from '../../src/shared/operations/smartBackoff';

type Cookie = { Name: string; Value: string };
type Role = 'admin' | 1 | 5 | 6;
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
}
interface Key extends SmartBackoffFields {
  id: string;
  failure_disable_threshold: string;
  streak: { count: string };
}
interface Donation {
  id: string;
  revision: string;
  status: string;
  keys: Key[];
}
const policy = common.common.failurePolicy,
  backoff = common.common.smartBackoff;
const charity = user.user.charity;
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);

async function session(browser: Browser, role: Role, narrow = false) {
  const state = fixture(),
    origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin'
      ? state.admin_cookie
      : state.users.find((entry) => entry.level === role)!.cookie;
  const context = await browser.newContext({
    viewport: narrow ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    reducedMotion: 'reduce',
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
  await context.addInitScript(
    (theme) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', theme);
    },
    role === 6 ? 'dark' : 'light',
  );
  return context;
}
async function read<T>(context: BrowserContext, origin: string, path: string): Promise<T> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json() as Promise<T>;
}
async function write<T>(
  context: BrowserContext,
  origin: string,
  path: string,
  data: unknown,
): Promise<T> {
  const response = await context.request.post(origin + path, {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data,
  });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json() as Promise<T>;
}
async function endpoint(owner: BrowserContext, label: string) {
  const origin = fixture().user_url;
  const baseURL = 'https://' + label + '.example.test/v1';
  const source = await write<{ id: string }>(owner, origin, '/api/endpoints', {
    source: 'custom',
    connector_type: 'openai-compatible',
    base_url: baseURL,
    note: label,
    enabled: true,
  });
  const key = await write<{ id: string }>(owner, origin, '/api/endpoints/' + source.id + '/keys', {
    secret: 'synthetic-' + label,
    note: label,
    enabled: true,
    force_store_false: false,
    ownership_confirmed: true,
  });
  return { keyID: key.id, baseURL, label };
}
async function fill(control: Locator, profile: SmartBackoffProfile, rule: SmartBackoffRule) {
  await control.getByLabel(backoff.profile, { exact: true }).selectOption(profile);
  await control.getByText(backoff.advanced, { exact: true }).click();
  if ((await control.getByLabel('Rule 1 value', { exact: true }).count()) === 0)
    await control.getByRole('button', { name: backoff.addRule, exact: true }).click();
  await control.getByLabel('Rule 1 type', { exact: true }).selectOption(rule.kind);
  await control.getByLabel('Rule 1 value', { exact: true }).fill(rule.value);
  await control.getByLabel('Rule 1 allowance cycle', { exact: true }).selectOption(rule.period);
}
async function save(page: Page, control: Locator, path: string, modelID?: string) {
  const saved = page.waitForResponse(
    (response) =>
      response.request().method() === 'PATCH' && new URL(response.url()).pathname === path,
  );
  await control.getByRole('button', { name: policy.save, exact: true }).click();
  const response = await saved;
  expect(response.ok()).toBe(true);
  expect(new URL(response.url()).searchParams.get('charity_model_id')).toBe(modelID ?? null);
  await expect(control.getByText(policy.done, { exact: true })).toBeVisible();
}

test('owner creates per-key backoff and edits it through the mobile donation page', async ({
  browser,
}, info) => {
  const state = fixture(),
    owner = await session(browser, 1, true);
  try {
    const source = await endpoint(owner, 'backoff-owner-browser');
    const page = await owner.newPage(),
      errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(state.user_url + '/charity?tab=donate');
    await page.getByRole('button', { name: charity.presentation.openPicker, exact: true }).click();
    const picker = page.locator('.donation-resource-picker');
    const search = picker.getByLabel(charity.resourcePicker.endpointSearch, { exact: true });
    await search.fill(source.label);
    await search.press('Enter');
    await picker
      .locator('.donation-resource-picker__section')
      .first()
      .getByRole('button', { name: new RegExp(source.label) })
      .click();
    await picker
      .locator('.donation-resource-picker__section')
      .nth(1)
      .getByRole('checkbox', { name: new RegExp(source.label) })
      .check();
    await picker.getByLabel(policy.label, { exact: true }).fill('13');
    const creationRule: SmartBackoffRule = { kind: 'code', value: '1310', period: 'month' };
    await fill(picker, 'day_month', creationRule);
    await page
      .getByRole('button', { name: charity.presentation.selectionDone, exact: true })
      .click();
    await page
      .getByLabel(charity.donationDescription, { exact: true })
      .fill('Synthetic smart backoff donation');
    await page
      .getByRole('radiogroup', { name: 'Accept a public Discord thank-you' })
      .getByText('No', { exact: true })
      .click();
    await page.getByLabel(charity.ownershipAuthorization, { exact: true }).check();
    const submitted = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/api/donations',
    );
    await page.getByRole('button', { name: charity.submit, exact: true }).click();
    const response = await submitted;
    expect(response.ok()).toBe(true);
    const created = (await response.json()) as Donation;
    expect(created.keys[0]).toMatchObject({
      failure_disable_threshold: '13',
      smart_backoff_profile: 'day_month',
      smart_backoff_rules: [creationRule],
      smart_backoff: null,
    });
    await page.goto(state.user_url + '/charity/donations/' + created.id);
    const control = page.locator('.failure-policy-control').first();
    await expect(control.getByLabel(backoff.profile, { exact: true })).toHaveValue('day_month');
    await control.getByLabel(policy.label, { exact: true }).fill('17');
    const editedRule: SmartBackoffRule = {
      kind: 'message',
      value: 'Daily allowance exhausted',
      period: 'day',
    };
    await fill(control, 'day', editedRule);
    await save(
      page,
      control,
      '/api/donations/' + created.id + '/keys/' + created.keys[0].id + '/failure-policy',
    );
    const saved = await read<Donation>(owner, state.user_url, '/api/donations/' + created.id);
    expect(saved.keys[0]).toMatchObject({
      failure_disable_threshold: '17',
      smart_backoff_profile: 'day',
      smart_backoff_rules: [editedRule],
      smart_backoff: null,
      streak: { count: '0' },
    });
    await page.reload();
    await expect(control.getByLabel(backoff.profile, { exact: true })).toHaveValue('day');
    await expect(control.getByLabel('Rule 1 value', { exact: true })).toHaveValue(editedRule.value);
    await control.getByRole('button', { name: policy.save, exact: true }).scrollIntoViewIfNeeded();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({ path: info.outputPath('owner-backoff-mobile.png') });
    expect(errors).toEqual([]);
  } finally {
    await owner.close();
  }
});

async function boundDonation(owner: BrowserContext, admin: BrowserContext) {
  const state = fixture(),
    source = await endpoint(owner, 'backoff-scoped-browser');
  const donation = await write<Donation>(owner, state.user_url, '/api/donations', {
    description: 'Synthetic bound backoff source',
    ownership_authorized: true,
    discord_public_thanks: false,
    keys: [{ endpoint_key_id: source.keyID, expires_at: null }],
  });
  const current = await read<Donation>(
    admin,
    state.admin_url,
    '/admin/api/donations/' + donation.id,
  );
  const keyID = current.keys[0].id;
  if (current.status === 'pending')
    await write(admin, state.admin_url, '/admin/api/donations/' + current.id + '/review', {
      decision: 'approve',
      expected_revision: current.revision,
      reason: 'Synthetic backoff setup',
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
  const candidatesPath = '/admin/api/donations/' + current.id + '/keys/' + keyID + '/models';
  const catalog = await read<{ manual_catalog_revision: string }>(
    admin,
    state.admin_url,
    candidatesPath,
  );
  await write(admin, state.admin_url, candidatesPath + '/manual', {
    expected_manual_catalog_revision: catalog.manual_catalog_revision,
    entries: ['browser/smart-backoff'],
  });
  const models: string[] = [];
  for (const model of ['smart-backoff-bound', 'smart-backoff-unbound']) {
    const added = await write<{ id: string }>(admin, state.admin_url, '/admin/api/charity-models', {
      provider: 'browser',
      model,
      enabled: true,
      is_mainstream: true,
      transport_rule: 'passthrough',
      flatten_tool_calls: false,
      pricing: { mode: 'per_request', user_price: '0', donor_reward: '0' },
      discount: { enabled: false, percent: 100, start_at: null, end_at: null },
    });
    models.push(added.id);
  }
  const path = '/admin/api/charity-models/' + models[0] + '/bindings';
  const bindings = await read<{ binding_revision: string }>(admin, state.admin_url, path);
  await write(admin, state.admin_url, path + '/batch', {
    expected_binding_revision: bindings.binding_revision,
    selections: [{ donation_key_id: keyID, upstream_model_id: 'browser/smart-backoff' }],
  });
  return {
    donationID: current.id,
    keyID,
    modelID: models[0],
    unboundModelID: models[1],
    baseURL: source.baseURL,
  };
}

async function openManagement(
  page: Page,
  role: 'admin' | 5 | 6,
  data: Awaited<ReturnType<typeof boundDonation>>,
) {
  const state = fixture();
  if (role === 5) {
    await page.goto(
      state.user_url + '/steward?tab=charity&charity_section=models&charity_model=' + data.modelID,
    );
    await page
      .locator('.charity-source-browser__source-button')
      .filter({ hasText: data.baseURL })
      .click();
    await page
      .locator('.charity-source-browser')
      .getByRole('button', { name: 'Manage key #' + data.keyID, exact: true })
      .click();
  } else
    await page.goto(
      (role === 'admin'
        ? state.admin_url + '/charity?donation_id='
        : state.user_url + '/steward?tab=charity&donation_id=') + data.donationID,
    );
  await page.getByText('Failure strategy', { exact: true }).click();
  return page.locator('.failure-policy-control').first();
}

test('administrator and collaborators persist backoff while level five stays within its bound model', async ({
  browser,
}, info) => {
  const state = fixture(),
    owner = await session(browser, 1),
    admin = await session(browser, 'admin');
  try {
    const data = await boundDonation(owner, admin);
    const initial = await read<Donation>(
      admin,
      state.admin_url,
      '/admin/api/donations/' + data.donationID,
    );
    expect(initial.keys[0]).toMatchObject({
      smart_backoff_profile: 'none',
      smart_backoff_rules: [],
      smart_backoff: null,
    });
    for (const entry of [
      {
        role: 'admin',
        profile: 'five_hour_week',
        threshold: '11',
        rule: { kind: 'code', value: '1308', period: 'five_hour' },
      },
      {
        role: 6,
        profile: 'five_hour_week_month',
        threshold: '12',
        rule: { kind: 'code', value: '1310', period: 'month' },
      },
      {
        role: 5,
        profile: 'day',
        threshold: '13',
        rule: { kind: 'message', value: 'Daily allowance exhausted', period: 'day' },
      },
    ] as const) {
      const context =
        entry.role === 'admin' ? admin : await session(browser, entry.role, entry.role === 6);
      try {
        const page = await context.newPage(),
          errors: string[] = [];
        page.on('pageerror', (error) => errors.push(error.message));
        const control = await openManagement(page, entry.role, data);
        await control.getByLabel(policy.label, { exact: true }).fill(entry.threshold);
        await fill(control, entry.profile, entry.rule);
        const path =
          (entry.role === 'admin' ? '/admin/api' : '/api/steward') +
          '/donations/' +
          data.donationID +
          '/keys/' +
          data.keyID +
          '/failure-policy';
        await save(page, control, path, entry.role === 5 ? data.modelID : undefined);
        const saved = await read<Donation>(
          admin,
          state.admin_url,
          '/admin/api/donations/' + data.donationID,
        );
        expect(saved.keys[0]).toMatchObject({
          failure_disable_threshold: entry.threshold,
          smart_backoff_profile: entry.profile,
          smart_backoff_rules: [entry.rule],
          smart_backoff: null,
          streak: { count: '0' },
        });
        if (entry.role === 5) {
          const keyPath = '/api/steward/donations/' + data.donationID + '/keys/' + data.keyID;
          expect(
            await read<Key>(context, state.user_url, keyPath + '?charity_model_id=' + data.modelID),
          ).toMatchObject({
            smart_backoff_profile: entry.profile,
            smart_backoff_rules: [entry.rule],
          });
          for (const suffix of ['', '?charity_model_id=' + data.unboundModelID]) {
            const rejected = await context.request.patch(
              state.user_url + keyPath + '/failure-policy' + suffix,
              {
                headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
                data: {
                  expected_revision: saved.revision,
                  failure_disable_threshold: '99',
                  smart_backoff_profile: 'day_month',
                  smart_backoff_rules: [],
                },
              },
            );
            expect(rejected.status()).toBe(404);
          }
          const unchanged = await read<Donation>(
            admin,
            state.admin_url,
            '/admin/api/donations/' + data.donationID,
          );
          expect(unchanged).toEqual(saved);
        }
        const reloaded = await openManagement(page, entry.role, data);
        await expect(reloaded.getByLabel(backoff.profile, { exact: true })).toHaveValue(
          entry.profile,
        );
        await expect(reloaded.getByLabel('Rule 1 value', { exact: true })).toHaveValue(
          entry.rule.value,
        );
        await reloaded
          .getByRole('button', { name: policy.save, exact: true })
          .scrollIntoViewIfNeeded();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        await page.screenshot({ path: info.outputPath('backoff-' + entry.role + '.png') });
        expect(errors).toEqual([]);
        await page.close();
      } finally {
        if (entry.role !== 'admin') await context.close();
      }
    }
  } finally {
    await owner.close();
    await admin.close();
  }
});
