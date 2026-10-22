import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { isDeepStrictEqual } from 'node:util';
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
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };
import user from '../../src/user/i18n/en.json' with { type: 'json' };
import admin from '../../src/admin/i18n/en.json' with { type: 'json' };
import matching from '../../src/user/features/core/connections/matching-copy/en.json' with { type: 'json' };
import type { Endpoint, EndpointCreateOptions } from '../../src/user/features/core/types';
import type { AdminMainstreamChannel } from '../../src/admin/features/operations/channels';

type Cookie = { Name: string; Value: string };
interface Fixture {
  user_url: string;
  admin_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: { channel_id: string };
}
interface Configuration {
  revision: string;
  fixed_headers: { values: Record<string, { value?: unknown }> };
  body_defaults: { values: Record<string, { value?: unknown }> };
  body_forced: { values: Record<string, { value?: unknown }> };
}
type Edit = { action: string; value?: unknown; sensitive_paths?: string[] };
type AdaptationWrite = Record<
  'fixed_headers' | 'body_defaults' | 'body_forced',
  { values: Record<string, Edit> }
>;

const c = common.common.connections;
const native = common.common.modelTypes;
const editor = common.requestAdaptation.editor;
const service = common.requestAdaptation.service;
const channels = admin.admin.mainstreamChannels;

test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the management fixture.');

function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}

async function session(browser: Browser, role: 'owner' | 'admin' | 5, language = 'en') {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  // The second ordinary user is separate from existing management journeys.
  const identity = role === 'owner' ? state.users[1] : state.users.find((u) => u.level === role);
  const cookie = role === 'admin' ? state.admin_cookie : identity!.cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
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
  await context.addInitScript((lang) => {
    localStorage.setItem('nb.lang', lang);
    localStorage.setItem('nb.theme', 'light');
  }, language);
  return context;
}

async function api<T>(
  context: BrowserContext,
  origin: string,
  path: string,
  method = 'GET',
  data?: unknown,
  status = method === 'POST' ? 201 : 200,
): Promise<T> {
  const response = await context.request.fetch(origin + path, {
    method,
    headers: { Origin: origin, ...(method === 'GET' ? {} : { 'Idempotency-Key': randomUUID() }) },
    data,
  });
  expect(response.status(), method + ' ' + path).toBe(status);
  return response.json() as Promise<T>;
}

function responseFor(page: Page, method: string, path: string) {
  return page.waitForResponse(
    (response) =>
      response.request().method() === method && new URL(response.url()).pathname === path,
  );
}

async function openAdaptation(page: Page) {
  const fold = page
    .locator('details')
    .filter({ has: page.getByText(user.user.services.adaptation, { exact: true }) });
  if ((await fold.getAttribute('open')) === null) await fold.locator('summary').click();
  await expect(fold.getByRole('button', { name: service.save, exact: true })).toBeEnabled();
  return fold;
}

async function capture(page: Page, info: TestInfo, name: string, focus: Locator) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await focus.scrollIntoViewIfNeeded();
      await expect
        .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
        .toBe(true);
      const file = name + '-' + theme + '-' + width + '.png';
      const path = process.env.NONBIRI_VISUAL_DIR
        ? resolve(process.env.NONBIRI_VISUAL_DIR, file)
        : resolve(dirname(process.env.NONBIRI_AUDIT_BROWSER_STATE!), 'closeout-controls', file);
      mkdirSync(dirname(path), { recursive: true });
      await page.screenshot({ path });
      await info.attach(file, { path, contentType: 'image/png' });
    }
  }
}

function expectPrivateEqual(actual: unknown, expected: unknown) {
  expect(isDeepStrictEqual(actual, expected)).toBe(true);
}

function expectNoMarkers(value: unknown, markers: string[]) {
  expect(markers.some((marker) => JSON.stringify(value).includes(marker))).toBe(false);
}

async function noPersistentSecrets(page: Page, markers: string[]) {
  const result = await page.evaluate(async (values) => {
    const stored = JSON.stringify({
      local: Object.entries(localStorage),
      session: Object.entries(sessionStorage),
      url: location.href,
      history: history.state,
    });
    return {
      leaked: values.some((value) => stored.includes(value)),
      databases: (await indexedDB.databases()).length,
      caches: (await caches.keys()).length,
      serviceWorkers: (await navigator.serviceWorker.getRegistrations()).length,
    };
  }, markers);
  expect(result).toEqual({ leaked: false, databases: 0, caches: 0, serviceWorkers: 0 });
  // Authenticated harness sessions legitimately retain their own HttpOnly cookie.
  expectNoMarkers(await page.context().cookies(), markers);
}

async function baseURL(context: BrowserContext) {
  const state = fixture();
  const options = await api<EndpointCreateOptions>(
    context,
    state.user_url,
    '/api/endpoint-create-options',
  );
  const seed = options.mainstream_channels.find(
    (channel) => channel.id === state.management.channel_id,
  )!;
  return seed.entries![0].base_url;
}

async function selectService(scope: Locator, name: 'chat_completions' | 'responses') {
  const group = scope.getByRole('group', { name: common.common.nativeServices.title, exact: true });
  for (const box of await group.getByRole('checkbox').all()) await box.uncheck();
  await group.getByRole('checkbox', { name: native[name], exact: true }).check();
}

test('personal rewriting reads ordinary values and preserves protected keep and replace edits', async ({
  browser,
}, info) => {
  const state = fixture();
  const owner = await session(browser, 'owner');
  try {
    const connection = await api<{ id: string }>(
      owner,
      state.user_url,
      '/api/connections',
      'POST',
      {
        name: 'Readable rewriting ' + randomUUID().slice(0, 8),
      },
    );
    const endpoint = await api<Endpoint>(owner, state.user_url, '/api/endpoints', 'POST', {
      connection_id: connection.id,
      source: 'custom',
      connector_type: 'openai-compatible',
      base_url: await baseURL(owner),
      note: 'Readable rewriting',
      enabled: true,
      native_services: ['chat_completions'],
    });
    const path = '/api/endpoints/' + endpoint.id + '/request-adaptation';
    const header = 'synthetic-header-' + randomUUID();
    const token = 'synthetic-token-' + randomUUID();
    const replacement = 'synthetic-replacement-' + randomUUID();
    const markers = [header, token, replacement];
    const page = await owner.newPage();
    await page.goto(state.user_url + '/endpoints/' + endpoint.id);
    let fold = await openAdaptation(page);
    const headers = () => fold.getByRole('group', { name: service.fixedHeaders, exact: true });
    const defaults = () => fold.getByRole('group', { name: service.bodyDefaults, exact: true });
    const forced = () => fold.getByRole('group', { name: service.bodyForced, exact: true });
    const save = async () => {
      const written = responseFor(page, 'PUT', path);
      const refreshed = responseFor(page, 'GET', path);
      await fold.getByRole('button', { name: service.save, exact: true }).click();
      const response = await written;
      expect(response.status()).toBe(200);
      expectNoMarkers(await response.json(), markers);
      const read = await refreshed;
      expect(read.status()).toBe(200);
      await expect(fold.getByRole('button', { name: service.save, exact: true })).toBeEnabled();
      return {
        input: response.request().postDataJSON() as AdaptationWrite,
        view: (await read.json()) as Configuration,
      };
    };
    for (const [group, name, value] of [
      [headers(), 'X-Token', header],
      [defaults(), '/temperature', '0.25'],
      [forced(), '/vendor', JSON.stringify({ token, mode: 'high' })],
    ] as const) {
      await group.getByRole('button', { name: editor.add, exact: true }).click();
      await group.getByLabel(editor.name, { exact: true }).fill(name);
      await group.getByLabel(editor.value, { exact: true }).fill(value);
    }
    const first = await save();
    expect(first.view.fixed_headers.values['X-Token'].value === header).toBe(true);
    expect(first.view.body_defaults.values['/temperature'].value).toBe(0.25);
    expectPrivateEqual(first.view.body_forced.values['/vendor'].value, { token, mode: 'high' });
    const refresh = responseFor(page, 'GET', path);
    await fold.getByRole('button', { name: editor.refresh, exact: true }).click();
    expect((await refresh).status()).toBe(200);
    await page.reload();
    fold = await openAdaptation(page);
    await expect
      .poll(
        async () =>
          (await headers().getByLabel(editor.value, { exact: true }).inputValue()) === header,
      )
      .toBe(true);
    await expect(defaults().getByLabel(editor.value, { exact: true })).toHaveValue('0.25');
    await expect
      .poll(async () =>
        (await forced().getByLabel(editor.value, { exact: true }).inputValue()).includes(token),
      )
      .toBe(true);
    await noPersistentSecrets(page, markers);

    await headers()
      .getByLabel(editor.protection + ': X-Token', { exact: true })
      .selectOption('whole');
    await forced()
      .getByLabel(editor.protection + ': /vendor', { exact: true })
      .selectOption('leaves');
    await forced().getByLabel(editor.protectedPaths, { exact: true }).fill('/token');
    const protectedSave = await save();
    expectPrivateEqual(protectedSave.input.fixed_headers.values['X-Token'], {
      action: 'keep',
      sensitive_paths: [''],
    });
    expectPrivateEqual(protectedSave.input.body_forced.values['/vendor'], {
      action: 'keep',
      sensitive_paths: ['/token'],
    });
    expect(Object.hasOwn(protectedSave.view.fixed_headers.values['X-Token'], 'value')).toBe(false);
    expect(Object.hasOwn(protectedSave.view.body_forced.values['/vendor'], 'value')).toBe(false);
    expect(protectedSave.view.body_defaults.values['/temperature'].value).toBe(0.25);
    expectNoMarkers(protectedSave.view, markers);
    await page.reload();
    fold = await openAdaptation(page);
    await expect(headers().getByText(editor.masked, { exact: true })).toBeVisible();
    await expect(forced().getByText(editor.masked, { exact: true })).toBeVisible();
    await expect(headers().getByLabel(editor.value, { exact: true })).toHaveCount(0);
    await expect(forced().getByLabel(editor.value, { exact: true })).toHaveCount(0);
    expectNoMarkers(await page.locator('body').innerText(), markers);
    await capture(page, info, 'protected-rewriting', headers());
    await headers().getByLabel(editor.action, { exact: true }).selectOption('replace');
    await headers().getByLabel(editor.value, { exact: true }).fill(replacement);
    const changed = await save();
    expect(changed.input.fixed_headers.values['X-Token'].action).toBe('replace');
    expect(changed.input.fixed_headers.values['X-Token'].value === replacement).toBe(true);
    expectPrivateEqual(changed.input.body_forced.values['/vendor'], { action: 'keep' });
    expectNoMarkers(changed.view, markers);

    // Disabling protection with keep proves the hidden saved values were retained.
    await headers()
      .getByLabel(editor.protection + ': X-Token', { exact: true })
      .selectOption('off');
    await forced()
      .getByLabel(editor.protection + ': /vendor', { exact: true })
      .selectOption('off');
    const revealed = await save();
    expectPrivateEqual(revealed.input.fixed_headers.values['X-Token'], {
      action: 'keep',
      sensitive_paths: [],
    });
    expect(revealed.view.fixed_headers.values['X-Token'].value === replacement).toBe(true);
    expectPrivateEqual(revealed.view.body_forced.values['/vendor'].value, { token, mode: 'high' });
    expect(revealed.view.body_defaults.values['/temperature'].value).toBe(0.25);
    await noPersistentSecrets(page, markers);
  } finally {
    await owner.close();
  }
});

test('Chinese management confirmation revokes only the selected site key and respects steward levels', async ({
  browser,
}, info) => {
  const state = fixture();
  const target = state.users[1].id;
  const owner = await session(browser, 'owner');
  const manager = await session(browser, 'admin', 'zh');
  const steward = await session(browser, 5);
  const userPath = '/admin/api/users/' + target;
  const keyPath = userPath + '/caller-key';
  const copy = commonZh.management.users;
  let restoreLevel: number | null | undefined;
  const setLevel = async (level: number | null) => {
    const current = await api<{ revision: string }>(manager, state.admin_url, userPath);
    await api(manager, state.admin_url, userPath, 'PATCH', {
      mode: 'profile',
      expected_revision: current.revision,
      level,
    });
  };
  try {
    const original = await api<{ level: { manual: number | null } }>(
      manager,
      state.admin_url,
      userPath,
    );
    const currentKey = await owner.request.get(state.user_url + '/api/caller-key');
    expect(currentKey.status()).toBe(200);
    const generation = currentKey.headers()['x-nonbiri-callerkey-generation'];
    expect(generation).toMatch(/^\d+$/);
    const issued = await api<{ secret: string; metadata: { generation: string } }>(
      owner,
      state.user_url,
      '/api/caller-key/regenerate',
      'POST',
      { expected_generation: generation },
      200,
    );
    const call = () =>
      owner.request.get(state.user_url + '/v1/models', {
        headers: { Authorization: 'Bearer ' + issued.secret },
      });
    expect((await call()).status()).toBe(200);

    restoreLevel = original.level.manual;
    await setLevel(5);
    const trainee = await steward.newPage();
    for (const id of [target, state.users.find((entry) => entry.level === 6)!.id]) {
      const metadataRequests: string[] = [];
      const observe = (request: import('@playwright/test').Request) => {
        if (new URL(request.url()).pathname === '/api/steward/users/' + id + '/caller-key')
          metadataRequests.push(request.method());
      };
      trainee.on('request', observe);
      await trainee.goto(state.user_url + '/steward?tab=users&user=' + id);
      await expect(
        trainee.getByRole('tab', { name: common.management.users.tabOverview, exact: true }),
      ).toBeVisible();
      await expect(
        trainee.getByRole('heading', { name: common.management.users.callerKeyTitle, exact: true }),
      ).toHaveCount(0);
      await expect(
        trainee.getByRole('button', { name: common.management.users.callerKeyRevoke, exact: true }),
      ).toHaveCount(0);
      expect(metadataRequests).toEqual([]);
      trainee.off('request', observe);
    }
    await setLevel(restoreLevel);
    restoreLevel = undefined;
    await trainee.goto(state.user_url + '/steward?tab=users&user=' + target);
    await expect(
      trainee.getByRole('button', { name: common.management.users.callerKeyRevoke, exact: true }),
    ).toBeEnabled();

    const page = await manager.newPage();
    const metadataRead = responseFor(page, 'GET', keyPath);
    let revocations = 0;
    page.on('request', (request) => {
      if (request.method() === 'POST' && new URL(request.url()).pathname === keyPath + '/revoke')
        revocations++;
    });
    await page.goto(state.admin_url + '/users?user=' + target);
    const metadataResponse = await metadataRead;
    expect(metadataResponse.status()).toBe(200);
    const metadata = (await metadataResponse.json()) as { generation: string; active: boolean };
    expect(Object.keys(metadata).sort()).toEqual([
      'active',
      'created_at',
      'generation',
      'updated_at',
      'user_id',
    ]);
    expect(metadata.active).toBe(true);
    expect(metadata.generation).toBe(issued.metadata.generation);
    expectNoMarkers(metadata, [issued.secret]);
    await expect(page.getByText(copy.callerKeyActive, { exact: true })).toBeVisible();
    await page.getByRole('button', { name: copy.callerKeyRevoke, exact: true }).click();
    const dialog = page.getByRole('alertdialog', { name: copy.callerKeyConfirmTitle, exact: true });
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: commonZh.common.cancel, exact: true }).click();
    await expect(dialog).toBeHidden();
    expect((await api<{ active: boolean }>(manager, state.admin_url, keyPath)).active).toBe(true);
    expect(revocations).toBe(0);
    expect((await call()).status()).toBe(200);
    await page.getByRole('button', { name: copy.callerKeyRevoke, exact: true }).click();
    await capture(page, info, 'caller-key-confirm-zh', dialog);
    const revoked = responseFor(page, 'POST', keyPath + '/revoke');
    await dialog.getByRole('button', { name: copy.callerKeyRevoke, exact: true }).click();
    const response = await revoked;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON()).toEqual({
      expected_generation: issued.metadata.generation,
    });
    expectPrivateEqual(await response.json(), {
      generation: String(BigInt(issued.metadata.generation) + 1n),
      revoked: true,
    });
    await expect(page.getByText(copy.callerKeyRevoked, { exact: true })).toBeVisible();
    await expect(page.getByText(copy.callerKeyEmpty, { exact: true })).toBeVisible();
    expect(revocations).toBe(1);
    expect((await call()).status()).toBe(401);
    // The same authenticated context still reads the account's own key metadata.
    expect(await api(owner, state.user_url, '/api/caller-key')).toBeNull();
    expectNoMarkers(await page.locator('body').innerText(), [issued.secret]);
    await noPersistentSecrets(page, [issued.secret]);
  } finally {
    try {
      if (restoreLevel !== undefined) await setLevel(restoreLevel);
    } finally {
      await owner.close();
      await steward.close();
      await manager.close();
    }
  }
});

test('matching presets remain suggestions until an entry is explicitly selected', async ({
  browser,
}, info) => {
  const state = fixture();
  const owner = await session(browser, 'owner');
  const manager = await session(browser, 'admin');
  try {
    const suffix = randomUUID().slice(0, 8);
    const address = (await baseURL(owner)).replace(/\/$/, '') + '/matching-' + suffix;
    const presets: AdminMainstreamChannel[] = [];
    const adminPage = await manager.newPage();
    for (const [index, kind] of (['chat_completions', 'responses'] as const).entries()) {
      await adminPage.goto(state.admin_url + '/mainstream-channels');
      await adminPage.getByRole('button', { name: channels.actions.new, exact: true }).click();
      const form = adminPage.locator('form').filter({
        has: adminPage.getByLabel(channels.form.name, { exact: true }),
      });
      await form
        .getByLabel(channels.form.name, { exact: true })
        .fill('Matching preset ' + suffix + ' ' + index);
      await form.getByRole('button', { name: c.addEntry, exact: true }).click();
      const group = form.getByRole('group', {
        name: c.presetEntry.replace('{{number}}', '1'),
        exact: true,
      });
      await group.getByLabel(c.name, { exact: true }).fill('Matching entry ' + index);
      await group
        .getByLabel(c.protocol, { exact: true })
        .selectOption({ label: channels.connector.openaiCompatible });
      await group.getByLabel(c.entryURL, { exact: true }).fill(address);
      await selectService(group, kind);
      const saved = responseFor(adminPage, 'POST', '/admin/api/mainstream-channels');
      await form.getByRole('button', { name: channels.actions.create, exact: true }).click();
      const response = await saved;
      expect(response.status()).toBe(201);
      presets.push((await response.json()) as AdminMainstreamChannel);
    }
    const connection = await api<{ id: string }>(
      owner,
      state.user_url,
      '/api/connections',
      'POST',
      {
        name: 'Preset choices ' + suffix,
      },
    );
    const donations = () =>
      api<{ data: { id: string; status: string }[] }>(
        owner,
        state.user_url,
        '/api/donations?view=current&page=1&page_size=20',
      );
    const beforeDonations = await donations();
    const page = await owner.newPage();
    const writes: string[] = [];
    page.on('request', (request) => {
      if (!['GET', 'HEAD', 'OPTIONS'].includes(request.method()))
        writes.push(request.method() + ' ' + new URL(request.url()).pathname);
    });
    const url =
      state.user_url + '/endpoints?connection_id=' + connection.id + '&connection_view=entries';
    const beginCustom = async (kind: 'chat_completions' | 'responses') => {
      await page.goto(url);
      await page.getByRole('button', { name: c.addEntry, exact: true }).first().click();
      const panel = page.locator('.nb-expandable-panel:not([hidden])');
      await panel
        .getByLabel(c.protocol, { exact: true })
        .selectOption({ label: user.user.core['connector.openai'] });
      await panel.getByLabel(c.entryURL, { exact: true }).fill(address);
      await selectService(panel, kind);
      const suggestions = panel.getByRole('region', { name: matching.title, exact: true });
      await expect(
        suggestions.getByRole('button', { name: matching.usePreset, exact: false }),
      ).toHaveCount(2);
      for (const preset of presets) {
        const entry = preset.entries![0];
        const detail = suggestions.locator('details').filter({
          has: page.getByText(preset.name + ' · ' + entry.name, { exact: true }),
        });
        await expect(detail.getByText(address, { exact: true })).toBeVisible();
        await expect(detail).toContainText(channels.connector.openaiCompatible);
        await expect(detail).toContainText(
          native[entry.native_services[0] as 'chat_completions' | 'responses'],
        );
      }
      await expect(panel.getByRole('radio', { name: c.custom, exact: true })).toBeChecked();
      return { panel, suggestions };
    };
    const first = await beginCustom('chat_completions');
    expect(writes).toEqual([]);
    await capture(page, info, 'matching-preset-choices', first.suggestions);
    const customSaved = responseFor(page, 'POST', '/api/endpoints');
    await first.panel.getByRole('button', { name: c.saveEntries, exact: true }).click();
    const customResponse = await customSaved;
    expect(customResponse.status()).toBe(201);
    expect(customResponse.request().postDataJSON()).toMatchObject({
      source: 'custom',
      base_url: address,
      connection_id: connection.id,
      native_services: ['chat_completions'],
    });
    const custom = (await customResponse.json()) as Endpoint;
    expect(custom.origin).toEqual({ kind: 'custom' });

    const second = await beginCustom('responses');
    const chosen = presets[1];
    const chosenEntry = chosen.entries![0];
    await second.suggestions
      .getByRole('button', {
        name: matching.usePreset + ': ' + chosen.name + ' · ' + chosenEntry.name,
        exact: true,
      })
      .click();
    await expect(
      second.panel.getByRole('radio', { name: c.mainstream, exact: true }),
    ).toBeChecked();
    await expect(
      second.panel.getByRole('combobox', { name: c.mainstream, exact: true }),
    ).toHaveValue(chosen.id);
    await expect(
      second.panel.getByRole('checkbox', { name: chosenEntry.name, exact: true }),
    ).toBeChecked();
    await expect(
      second.panel.getByRole('checkbox', { name: native.responses, exact: true }),
    ).toBeChecked();
    const selectedSaved = responseFor(page, 'POST', '/api/endpoints');
    await second.panel.getByRole('button', { name: c.saveEntries, exact: true }).click();
    const selectedResponse = await selectedSaved;
    expect(selectedResponse.status()).toBe(201);
    expect(selectedResponse.request().postDataJSON()).toMatchObject({
      source: 'mainstream',
      channel_id: chosen.id,
      channel_entry_id: chosenEntry.id,
      channel_revision: chosen.revision,
      connection_id: connection.id,
      native_services: ['responses'],
    });
    const selected = (await selectedResponse.json()) as Endpoint;
    const entries = await api<{ data: Endpoint[] }>(
      owner,
      state.user_url,
      '/api/connections/' + connection.id + '/entries?page=1&page_size=20',
    );
    expect(entries.data.map((entry) => entry.id).sort()).toEqual([custom.id, selected.id].sort());
    expect(entries.data.find((entry) => entry.id === custom.id)?.origin).toEqual({
      kind: 'custom',
    });
    expect(entries.data.find((entry) => entry.id === selected.id)).toMatchObject({
      origin: { kind: 'mainstream', channel_id: chosen.id, name: chosen.name },
      native_services: ['responses'],
      base_url: address,
      configuration_type: 'openai-compatible',
    });
    for (const entry of entries.data) {
      const keys = await api<{ data: unknown[] }>(
        owner,
        state.user_url,
        '/api/endpoints/' + entry.id + '/keys',
      );
      expect(keys.data).toEqual([]);
    }
    expect(await donations()).toEqual(beforeDonations);
    expect(writes).toEqual(['POST /api/endpoints', 'POST /api/endpoints']);
  } finally {
    await owner.close();
    await manager.close();
  }
});
