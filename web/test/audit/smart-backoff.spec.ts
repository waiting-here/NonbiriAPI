import { protocolServices } from '../service-onboarding';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
  type TestInfo,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import user from '../../src/user/i18n/en.json' with { type: 'json' };
import type { SmartBackoffFields } from '../../src/shared/operations/smartBackoff';
import type { DonationPageSafeSource } from '../../src/shared/operations/donationPages';

type Cookie = { Name: string; Value: string };
type Role = 'admin' | 1 | 5 | 6;
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: {
    automatic_backoff_donation_id: string;
    automatic_backoff_key_ids: string[];
    automatic_backoff_model_id: string;
  };
}
interface Key extends SmartBackoffFields {
  id: string;
  failure_disable_threshold: string;
  streak: { count: string };
  charity_state: string;
  rate_limited_until: number | null;
  safe_source: DonationPageSafeSource;
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
  const key = await write<{
    id: string;
    credential_id: string;
    credential_version: string;
    grant_revision: string;
  }>(owner, origin, '/api/endpoints/' + source.id + '/keys', {
    secret: 'synthetic-' + label,
    note: label,
    enabled: true,
    force_store_false: false,
    ownership_confirmed: true,
  });
  return { keyID: key.id, key, baseURL, label };
}
async function save(page: Page, control: Locator, path: string, modelID?: string) {
  const saved = page.waitForResponse(
    (response) =>
      response.request().method() === 'PATCH' && new URL(response.url()).pathname === path,
  );
  await control.getByRole('button', { name: policy.save, exact: true }).click();
  const response = await saved;
  expect(response.ok()).toBe(true);
  expect(Object.keys(response.request().postDataJSON()).sort()).toEqual([
    'expected_revision',
    'failure_disable_threshold',
  ]);
  expect(new URL(response.url()).searchParams.get('charity_model_id')).toBe(modelID ?? null);
  await expect(control.getByText(policy.done, { exact: true })).toBeVisible();
}

test('owner submits with automatic recovery and edits its threshold through the mobile donation page', async ({
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
    const services = await protocolServices(page, 'openai-compatible');
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
    await picker.getByText(user.user.workflows.donationOptions, { exact: true }).click();
    await picker.getByLabel(policy.label, { exact: true }).fill('13');
    await expect(
      picker.locator('..').getByText(backoff.automaticHelp, { exact: true }),
    ).toBeVisible();
    await expect(picker.locator('.smart-backoff-input')).toHaveCount(0);
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
    expect(response.request().postDataJSON()).toEqual({
      description: 'Synthetic smart backoff donation',
      ownership_authorized: true,
      discord_public_thanks: false,
      keys: [
        {
          endpoint_key_id: source.keyID,
          credential_id: source.key.credential_id,
          credential_version: source.key.credential_version,
          grant_revision: source.key.grant_revision,
          native_services: services,
          expires_at: null,
          failure_disable_threshold: '13',
        },
      ],
    });
    const created = (await response.json()) as Donation;
    expect(created.keys[0]).toMatchObject({
      failure_disable_threshold: '13',
      smart_backoff: null,
    });
    await page.goto(state.user_url + '/charity/donations/' + created.id);
    const control = page.locator('.failure-policy-control').first();
    await expect(control.getByLabel(policy.label, { exact: true })).toHaveValue('13');
    await expect(control.getByRole('combobox')).toHaveCount(0);
    await control.getByLabel(policy.label, { exact: true }).fill('17');
    await save(
      page,
      control,
      '/api/donations/' + created.id + '/keys/' + created.keys[0].id + '/failure-policy',
    );
    const saved = await read<Donation>(owner, state.user_url, '/api/donations/' + created.id);
    expect(saved.keys[0]).toMatchObject({
      failure_disable_threshold: '17',
      smart_backoff: null,
      streak: { count: '0' },
    });
    await page.reload();
    await expect(control.getByLabel(policy.label, { exact: true })).toHaveValue('17');
    await expect(control.getByRole('combobox')).toHaveCount(0);
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
  for (const [index, model] of ['smart-backoff-bound', 'smart-backoff-private'].entries()) {
    const added = await write<{ id: string }>(admin, state.admin_url, '/admin/api/charity-models', {
      provider: 'browser',
      model,
      enabled: true,
      is_mainstream: index === 0,
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
    nonMainstreamModelID: models[1],
    safeSource: current.keys[0].safe_source,
  };
}

async function openManagement(
  page: Page,
  role: 'admin' | 5 | 6,
  data: Awaited<ReturnType<typeof boundDonation>>,
  allKeys = false,
) {
  const state = fixture();
  if (role === 5) {
    await page.goto(
      state.user_url +
        '/steward?tab=charity&charity_section=models&charity_model=' +
        data.modelID +
        (allKeys ? '&source_key_state=all' : ''),
    );
    const sourceName =
      data.safeSource.kind === 'mainstream'
        ? common.common.operations.charity.sourceBrowser.mainstreamSource.replace(
            '{{name}}',
            data.safeSource.name,
          )
        : common.common.operations.charity.sourceBrowser.customSource.replace(
            '{{baseUrl}}',
            data.safeSource.base_url,
          );
    await page
      .locator('.charity-source-browser__source-button')
      .filter({ has: page.getByText(sourceName, { exact: true }) })
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

test('administrator and collaborators edit thresholds while level five stays within qualified sources', async ({
  browser,
}, info) => {
  const state = fixture(),
    owner = await session(browser, 1),
    admin = await session(browser, 'admin');
  try {
    const data = await boundDonation(owner, admin);
    const outside = await endpoint(owner, 'backoff-unqualified-browser');
    const unqualified = await write<Donation>(owner, state.user_url, '/api/donations', {
      description: 'Synthetic unqualified backoff source',
      ownership_authorized: true,
      discord_public_thanks: false,
      keys: [{ endpoint_key_id: outside.keyID, expires_at: null }],
    });
    const unqualifiedPath =
      '/api/steward/donations/' + unqualified.id + '/keys/' + unqualified.keys[0].id;
    const outsideBefore = await read<Donation>(
      admin,
      state.admin_url,
      '/admin/api/donations/' + unqualified.id,
    );
    const initial = await read<Donation>(
      admin,
      state.admin_url,
      '/admin/api/donations/' + data.donationID,
    );
    expect(initial.keys[0]).toMatchObject({
      smart_backoff: null,
    });
    for (const entry of [
      { role: 'admin', threshold: '11' },
      { role: 6, threshold: '12' },
      { role: 5, threshold: '13' },
    ] as const) {
      const context =
        entry.role === 'admin' ? admin : await session(browser, entry.role, entry.role === 6);
      try {
        const page = await context.newPage(),
          errors: string[] = [];
        page.on('pageerror', (error) => errors.push(error.message));
        const control = await openManagement(page, entry.role, data);
        await control.getByLabel(policy.label, { exact: true }).fill(entry.threshold);
        await expect(control.getByRole('combobox')).toHaveCount(0);
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
          smart_backoff: null,
          streak: { count: '0' },
        });
        if (entry.role === 5) {
          const keyPath = '/api/steward/donations/' + data.donationID + '/keys/' + data.keyID;
          expect(
            await read<Key>(context, state.user_url, keyPath + '?charity_model_id=' + data.modelID),
          ).toMatchObject({ smart_backoff: null });
          expect(await read<Key>(context, state.user_url, keyPath)).toMatchObject({
            failure_disable_threshold: entry.threshold,
          });
          for (const [target, revision] of [
            [keyPath + '?charity_model_id=' + data.nonMainstreamModelID, saved.revision],
            [unqualifiedPath, unqualified.revision],
          ]) {
            const targetURL = new URL(target, state.user_url);
            targetURL.pathname += '/failure-policy';
            const rejected = await context.request.patch(targetURL.href, {
              headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
              data: {
                expected_revision: revision,
                failure_disable_threshold: '99',
              },
            });
            expect(rejected.status()).toBe(404);
          }
          const unchanged = await read<Donation>(
            admin,
            state.admin_url,
            '/admin/api/donations/' + data.donationID,
          );
          expect(unchanged).toEqual(saved);
          expect(
            await read<Donation>(admin, state.admin_url, '/admin/api/donations/' + unqualified.id),
          ).toEqual(outsideBefore);
        }
        const reloaded = await openManagement(page, entry.role, data);
        await expect(reloaded.getByLabel(policy.label, { exact: true })).toHaveValue(
          entry.threshold,
        );
        await expect(reloaded.getByRole('combobox')).toHaveCount(0);
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

async function statusVisuals(page: Page, info: TestInfo, role: Role, index: number) {
  for (const theme of ['light', 'dark'])
    for (const width of [1440, 390]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      const status = page.getByRole('region', { name: backoff.statusTitle }).nth(index);
      await status.scrollIntoViewIfNeeded();
      await expect(status).toBeInViewport();
      const geometry = await status.evaluate((element) => ({
        width: element.clientWidth,
        scroll: element.scrollWidth,
      }));
      expect(geometry.scroll).toBeLessThanOrEqual(geometry.width);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      const name = `automatic-recovery-${role}-${index}-${theme}-${width}.png`;
      await page.screenshot({
        path: process.env.NONBIRI_VISUAL_DIR
          ? resolve(process.env.NONBIRI_VISUAL_DIR, name)
          : info.outputPath(name),
        animations: 'disabled',
      });
    }
}

test('existing recovery states and independent upstream waits remain visible across key permissions', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management;
  const admin = await session(browser, 'admin');
  try {
    const donation = await read<Donation>(
      admin,
      state.admin_url,
      '/admin/api/donations/' + data.automatic_backoff_donation_id,
    );
    const [waiting, header, ready] = data.automatic_backoff_key_ids.map((id) =>
      donation.keys.find((key) => key.id === id)!,
    );
    expect(waiting.smart_backoff).toMatchObject({
      state: 'waiting',
      reason: 'day_quota',
      source: 'custom_rule',
    });
    expect(waiting.rate_limited_until).not.toBeNull();
    expect(waiting.rate_limited_until).toBeLessThan(waiting.smart_backoff!.retry_at);
    expect(header.charity_state).toBe('disabled');
    expect(header.smart_backoff).toBeNull();
    expect(header.rate_limited_until).toBeGreaterThan(Date.now() / 1000);
    expect(ready.smart_backoff).toMatchObject({
      state: 'ready',
      reason: 'week_quota',
      source: 'builtin',
    });
    for (const role of [1, 'admin', 6, 5] as const) {
      const context = role === 'admin' ? admin : await session(browser, role, role === 6);
      try {
        const page = await context.newPage();
        if (role === 5) {
          for (const key of [waiting, header, ready]) {
            const scoped = await read<Key>(
              context,
              state.user_url,
              `/api/steward/donations/${donation.id}/keys/${key.id}?charity_model_id=${data.automatic_backoff_model_id}`,
            );
            expect(scoped.smart_backoff).toEqual(key.smart_backoff);
            expect(scoped.rate_limited_until).toBe(key.rate_limited_until);
            await openManagement(
              page,
              role,
              {
                donationID: donation.id,
                keyID: key.id,
                modelID: data.automatic_backoff_model_id,
                nonMainstreamModelID: '0',
                safeSource: key.safe_source,
              },
              true,
            );
            const status = page
              .locator('.donation-key-editor')
              .getByRole('region', { name: backoff.statusTitle });
            await expect(status).toBeVisible();
            if (key.smart_backoff)
              await expect(status).toContainText(backoff.states[key.smart_backoff.state]);
            if (key.rate_limited_until !== null)
              await expect(status).toContainText(backoff.headerTitle);
          }
        } else {
          await page.goto(
            role === 1
              ? `${state.user_url}/charity/donations/${donation.id}`
              : role === 'admin'
                ? `${state.admin_url}/charity?donation_id=${donation.id}`
                : `${state.user_url}/steward?tab=charity&donation_id=${donation.id}`,
          );
          const statuses = page.getByRole('region', { name: backoff.statusTitle });
          await expect(statuses).toHaveCount(3);
          await expect(statuses.nth(0)).toContainText(backoff.states.waiting);
          await expect(statuses.nth(0)).toContainText(backoff.sources.custom_rule);
          await expect(statuses.nth(0)).toContainText(backoff.independentHelp);
          expect(
            await statuses
              .nth(0)
              .locator('time')
              .evaluateAll((nodes) => nodes.map((node) => node.getAttribute('datetime'))),
          ).toEqual([
            new Date(waiting.rate_limited_until! * 1000).toISOString(),
            new Date(waiting.smart_backoff!.retry_at * 1000).toISOString(),
          ]);
          await expect(statuses.nth(1)).toContainText(backoff.headerTitle);
          await expect(
            statuses.nth(1).getByText(backoff.automaticTitle, { exact: true }),
          ).toHaveCount(0);
          await expect(statuses.nth(2)).toContainText(backoff.states.ready);
          await expect(statuses.nth(2)).toContainText(backoff.sources.builtin);
          if (role === 1 || role === 'admin') {
            await statusVisuals(page, info, role, 0);
            await statusVisuals(page, info, role, 1);
          }
        }
        await page.close();
      } finally {
        if (role !== 'admin') await context.close();
      }
    }
    const unchanged = await read<Donation>(
      admin,
      state.admin_url,
      '/admin/api/donations/' + donation.id,
    );
    expect(unchanged).toEqual(donation);
  } finally {
    await admin.close();
  }
});
