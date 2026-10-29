import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };
import {
  fillService,
  readService,
  serviceCopy,
  servicePanel,
  submitService,
} from '../service-onboarding';

interface FixtureState {
  user_url: string;
  upstream_url: string;
  control_url: string;
  control_token: string;
  users: { id: string; cookie: { Name: string; Value: string } }[];
  private_markers: string[];
}
type Locale = 'en' | 'zh';
const statePath = process.env.NONBIRI_IMAGE_BROWSER_STATE!;
const fixture = () => JSON.parse(readFileSync(statePath, 'utf8')) as FixtureState;
const servicesCopy = (locale: Locale) => (locale === 'zh' ? zh : en).user.services;
const unique = () => randomBytes(5).toString('hex');
async function context(browser: Browser, locale: Locale = 'en', narrow = false) {
  const state = fixture();
  const result = await browser.newContext({
    viewport: { width: narrow ? 390 : 1280, height: 900 },
  });
  await result.addCookies([
    {
      name: state.users[0].cookie.Name,
      value: state.users[0].cookie.Value,
      domain: new URL(state.user_url).hostname,
      path: '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await result.addInitScript((locale) => {
    if (!['http:', 'https:'].includes(location.protocol)) return;
    localStorage.setItem('nb.lang', locale);
    localStorage.setItem('nb.theme', locale === 'zh' ? 'dark' : 'light');
  }, locale);
  return result;
}
async function api(context: BrowserContext, path: string, method = 'GET', data?: unknown) {
  return context.request.fetch(fixture().user_url + path, {
    method,
    data,
    headers: {
      Origin: fixture().user_url,
      'Idempotency-Key': randomBytes(16).toString('base64url'),
    },
  });
}
async function control(context: BrowserContext, mode: 'success' | 'empty' | 'failed') {
  const state = fixture();
  const response = await context.request.post(state.control_url + '/discovery', {
    headers: { Authorization: 'Bearer ' + state.control_token },
    data: { mode },
  });
  expect(response.status()).toBe(200);
}
const connectionCopy = (locale: Locale) =>
  (locale === 'zh' ? commonZh : commonEn).common.connections;
const sharedCopy = (locale: Locale) => (locale === 'zh' ? commonZh : commonEn).common;
const resources = new WeakMap<
  Page,
  {
    name: string;
    endpointId: string;
    connectionId?: string;
    keyId?: string;
    sourceId?: string;
    keyLabel?: string;
  }
>();
async function open(page: Page, locale: Locale = 'en') {
  await page.goto(fixture().user_url + '/endpoints?quickstart=1');
  await expect(
    page.getByRole('heading', { name: serviceCopy(locale).addService, exact: true }),
  ).toBeVisible();
}
async function newService(page: Page, name: string, recoverLost = false) {
  const panel = servicePanel(page);
  await fillService(panel, {
    name,
    url: fixture().upstream_url + '/v1',
    secret: fixture().private_markers[0],
  });
  await submitService(panel, 'en', recoverLost);
  const { endpoint, key } = await readService(page, name);
  expect(key.discovery_source_id).toBeTruthy();
  resources.set(page, {
    name,
    endpointId: endpoint.id,
    connectionId: endpoint.connection_id,
    keyId: key.id,
    sourceId: key.discovery_source_id!,
    keyLabel: key.note || key.display_head + '…' + key.display_tail,
  });
  await expect(
    panel.locator('summary').filter({ hasText: servicesCopy('en').manualModels }),
  ).toBeVisible();
  if (recoverLost) {
    await expect(
      panel
        .locator('.service-key-models')
        .getByRole('button', { name: servicesCopy('en').fetchModels, exact: true }),
    ).toBeEnabled();
  }
  await expect
    .poll(async () => {
      const response = await api(
        page.context(),
        '/api/discovery-sources/' + key.discovery_source_id,
      );
      expect(response.status()).toBe(200);
      return (await response.json()).state;
    })
    .toBe('succeeded');
}
async function associateSource(page: Page) {
  const meta = resources.get(page)!;
  const keys = await api(
    page.context(),
    '/api/endpoints/' + meta.endpointId + '/keys?page=1&page_size=100',
  );
  const savedKey = (await keys.json()).data.find((row: { id: string }) => row.id === meta.keyId);
  const sourceResponse = await api(page.context(), '/api/discovery-sources/' + meta.sourceId);
  expect(sourceResponse.status()).toBe(200);
  const savedSource = await sourceResponse.json();
  expect(savedSource.credential_id).toBe(savedKey.credential_id);
  expect(savedSource.credential_version).toBe(savedKey.credential_version);
  expect(savedKey.discovery_source_id).toBe(meta.sourceId);
  const path = '/api/endpoints/' + meta.endpointId + '/keys/' + meta.keyId;
  const waitForWrite = () =>
    page.waitForResponse(
      (response) =>
        response.request().method() === 'PATCH' && new URL(response.url()).pathname === path,
    );
  const detached = await api(page.context(), path, 'PATCH', {
    expected_revision: savedKey.revision,
    discovery_source_id: null,
  });
  expect(detached.status()).toBe(200);
  const detachedKey = await detached.json();
  expect(detachedKey.discovery_source_id).toBeNull();
  await page.goto(
    fixture().user_url +
      '/endpoints?connection_id=' +
      meta.connectionId +
      '&endpoint_id=' +
      meta.endpointId +
      '&key_id=' +
      meta.keyId,
  );
  await expect(
    page.getByRole('button', { name: servicesCopy('en').fetchModels, exact: true }),
  ).toBeDisabled();
  const associating = waitForWrite();
  const associationResult = await page.evaluate(
    async ({ path, source, revision }) => {
      const response = await fetch(path, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
        body: JSON.stringify({ discovery_source_id: source, expected_revision: revision }),
      });
      return { status: response.status, body: await response.json() };
    },
    { path, source: meta.sourceId, revision: detachedKey.revision },
  );
  const associated = await associating;
  expect(associated.status()).toBe(200);
  expect(associationResult.status).toBe(200);
  expect(associated.request().postDataJSON()).toEqual({
    discovery_source_id: meta.sourceId,
    expected_revision: detachedKey.revision,
  });
  expect(associationResult.body).toMatchObject({
    credential_id: savedKey.credential_id,
    credential_version: savedKey.credential_version,
    discovery_source_id: meta.sourceId,
  });
  await page.reload();
  await expect(
    page.getByRole('button', { name: servicesCopy('en').fetchModels, exact: true }),
  ).toBeEnabled();
  await page.getByRole('button', { name: servicesCopy('en').fetchModels, exact: true }).click();
}
async function manual(page: Page, upstream: string, prefix: string, locale: Locale = 'en') {
  const c = connectionCopy(locale),
    meta = resources.get(page)!;
  const core = (locale === 'zh' ? zh : en).user.core;
  const fold = page.locator('details').filter({
    has: page.locator(':scope > summary').filter({ hasText: servicesCopy(locale).manualModels }),
  });
  if ((await fold.getAttribute('open')) === null) await fold.locator(':scope > summary').click();
  const modelInput = fold.getByLabel(core['endpoints.manualModel'], { exact: true });
  await modelInput.fill(upstream);
  await fold.getByRole('button', { name: core['endpoints.manualAdd'], exact: true }).click();
  await expect
    .poll(async () => {
      const check = fold.getByRole('button', {
        name: sharedCopy(locale).outcome.recheck,
        exact: true,
      });
      if ((await check.isVisible()) && (await check.isEnabled())) await check.click();
      return modelInput.inputValue();
    })
    .toBe('');
  await page
    .locator('.service-key-models')
    .getByRole('tab', { name: servicesCopy(locale).manualModels, exact: true })
    .click();
  await page
    .locator('.service-key-models')
    .getByRole('checkbox', { name: upstream, exact: true })
    .check();
  const row = page.locator('details').filter({
    has: page
      .locator(':scope > summary strong')
      .filter({ hasText: new RegExp('^' + upstream + '$') }),
  });
  await row.getByRole('checkbox', { name: meta.name + ' · ' + upstream, exact: true }).check();
  await row
    .getByRole('group', { name: sharedCopy(locale).nativeServices.title, exact: true })
    .getByRole('checkbox', { name: sharedCopy(locale).modelTypes.chat_completions, exact: true })
    .check();
  await row
    .getByRole('group', { name: sharedCopy(locale).nativeServices.title, exact: true })
    .getByRole('checkbox', { name: sharedCopy(locale).modelTypes.responses, exact: true })
    .check();
  await row.getByLabel(c.provider, { exact: true }).fill(prefix);
  await row
    .getByRole('group', { name: sharedCopy(locale).modelTypes.title, exact: true })
    .getByRole('checkbox', { name: sharedCopy(locale).modelTypes.chat_completions, exact: true })
    .check();
  return row;
}
function connect(page: Page, locale: Locale = 'en', count = 1) {
  const c = connectionCopy(locale);
  return page
    .getByRole('button', {
      name: c.saveSelectedModels.replace('{{count}}', String(count)),
      exact: true,
    })
    .or(
      page.getByRole('button', {
        name: c.retryUnfinishedModels.replace('{{count}}', String(count)),
        exact: true,
      }),
    );
}
async function models(context: BrowserContext, prefix: string) {
  const response = await api(
    context,
    '/api/models?page=1&page_size=100&provider=' + encodeURIComponent(prefix),
  );
  expect(response.status()).toBe(200);
  return (await response.json()).data as {
    id: string;
    full_name: string;
    route_strategy: string;
    silent_retry: boolean;
    flatten_tool_calls: boolean;
    binding_count: string;
  }[];
}
async function screenshot(page: Page, name: string) {
  const directory =
    process.env.NONBIRI_IMAGE_BROWSER_PREVIEW || join(dirname(statePath), 'preview');
  mkdirSync(directory, { recursive: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
    true,
  );
  await page.screenshot({ path: join(directory, name), fullPage: true });
}
async function service(context: BrowserContext, name: string) {
  const response = await api(context, '/api/endpoints', 'POST', {
    source: 'custom',
    connector_type: 'openai-compatible',
    native_services: ['chat_completions', 'responses'],
    base_url: fixture().upstream_url + '/v1',
    note: name,
    enabled: true,
  });
  expect(response.status()).toBe(201);
  const endpoint = (await response.json()) as { id: string };
  const keyResponse = await api(context, '/api/endpoints/' + endpoint.id + '/keys', 'POST', {
    secret: fixture().private_markers[0],
    native_services: ['chat_completions', 'responses'],
    ownership_confirmed: true,
    enabled: true,
    note: name + '-key',
    force_store_false: false,
  });
  expect(keyResponse.status()).toBe(201);
  return { endpoint, key: (await keyResponse.json()) as { id: string } };
}
async function existing(page: Page, name: string, locale: Locale = 'en') {
  const endpoints = await api(
    page.context(),
    '/api/endpoints?page=1&page_size=100&q=' + encodeURIComponent(name),
  );
  const endpoint = (await endpoints.json()).data.find((row: { note: string }) => row.note === name);
  expect(endpoint).toBeTruthy();
  const keys = await api(
    page.context(),
    '/api/endpoints/' + endpoint.id + '/keys?page=1&page_size=100',
  );
  const key = (await keys.json()).data.find((row: { note: string }) => row.note === name + '-key');
  expect(key.discovery_source_id).toBeTruthy();
  resources.set(page, {
    name,
    endpointId: endpoint.id,
    connectionId: endpoint.connection_id,
    keyId: key.id,
    sourceId: key.discovery_source_id,
    keyLabel: key.note || key.display_head + '…' + key.display_tail,
  });
  await page.goto(
    fixture().user_url +
      '/endpoints?connection_id=' +
      endpoint.connection_id +
      '&endpoint_id=' +
      endpoint.id +
      '&key_id=' +
      key.id,
  );
  await expect(
    page.getByRole('button', { name: servicesCopy(locale).fetchModels, exact: true }),
  ).toBeVisible();
}

test.describe.configure({ mode: 'serial' });
test('optional setup connects selected models in one action and shows real relationships and structured debug messages', async ({
  browser,
}) => {
  const user = await context(browser),
    prefix = 'personal-' + unique();
  try {
    await control(user, 'success');
    const page = await user.newPage();
    await open(page);
    await newService(page, prefix);
    await associateSource(page);
    await manual(page, 'selected-chat-' + unique(), prefix);
    await expect(connect(page)).toBeEnabled();
    await connect(page).focus();
    await expect(connect(page)).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByText(connectionCopy('en').mappingSaved, { exact: true })).toBeVisible();
    const saved = await models(user, prefix);
    expect(saved).toHaveLength(1);
    expect(saved[0].binding_count).toBe('1');
    expect(
      await page.evaluate(() =>
        JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
      ),
    ).not.toContain(fixture().private_markers[0]);
    await screenshot(page, 'resources-connected-en-wide-light.png');
    await page
      .getByRole('link', { name: connectionCopy('en').backToConnections, exact: true })
      .click();
    await expect(page).toHaveURL(/\/endpoints$/);
    await page
      .getByRole('table', { name: connectionCopy('en').yourConnections, exact: true })
      .getByRole('link', { name: prefix, exact: true })
      .click();
    await expect(page).toHaveURL(
      (url) =>
        url.pathname === '/endpoints' &&
        url.searchParams.get('connection_id') === resources.get(page)!.connectionId &&
        url.searchParams.get('connection_view') === 'entries',
    );
    await expect(
      page
        .getByRole('table', { name: en.user.core['endpoints.key'], exact: true })
        .locator('tbody > tr.core-key-card'),
    ).toHaveCount(1);
    await screenshot(page, 'resources-relationship-en-wide.png');

    const caller = await api(user, '/api/caller-key');
    expect(caller.status()).toBe(200);
    const generation = caller.headers()['x-nonbiri-callerkey-generation'];
    const regenerated = await api(user, '/api/caller-key/regenerate', 'POST', {
      expected_generation: generation,
    });
    expect(regenerated.status()).toBe(200);
    const callerSecret = (await regenerated.json()).secret as string;
    await page.goto(fixture().user_url + '/debug');
    await page.getByRole('button', { name: 'Start Debug', exact: true }).click();
    await expect(
      page.locator('.ops-debug-status').getByText(en.user.debug.state.mode.dry, { exact: true }),
    ).toBeVisible();
    const long = 'Long message line\n'.repeat(130),
      raw = '<img src=x onerror=alert(1)>';
    const intercepted = await user.request.post(fixture().user_url + '/v1/chat/completions', {
      headers: { Authorization: 'Bearer ' + callerSecret },
      data: {
        model: saved[0].full_name,
        messages: [
          { role: 'user', content: raw, name: 'field-preserved' },
          { role: 'assistant', content: long, custom_field: { preserved: true } },
        ],
      },
    });
    expect(intercepted.status()).toBe(422);
    await page.locator('.ops-debug-trace > details > summary').click();
    await expect(
      page.getByRole('heading', { name: 'Message 1 · user', exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole('heading', { name: 'Message 2 · assistant', exact: true }),
    ).toBeVisible();
    await expect(page.locator('.nb-messages').getByText(raw, { exact: true })).toBeVisible();
    await expect(
      page.locator('.nb-messages').getByText('custom_field', { exact: true }),
    ).toBeVisible();
    await expect(page.locator('.nb-messages details')).toHaveCount(1);
    await expect(page.locator('.nb-messages details')).not.toHaveAttribute('open');
    await page.locator('.nb-messages details summary').click();
    await expect(page.locator('.nb-messages details pre')).toBeVisible();
    await screenshot(page, 'resources-debug-messages-en.png');
  } finally {
    await user.close();
  }
});

test('existing resources support empty and failed discovery plus manual setup in Chinese on a narrow dark screen', async ({
  browser,
}) => {
  const user = await context(browser, 'zh', true),
    name = 'existing-' + unique(),
    prefix = 'manual-' + unique();
  try {
    await service(user, name);
    const page = await user.newPage();
    await open(page, 'zh');
    await existing(page, name, 'zh');
    await control(user, 'empty');
    const sourceId = resources.get(page)!.sourceId!;
    const accepted = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname ===
          '/api/endpoints/' +
            resources.get(page)!.endpointId +
            '/keys/' +
            resources.get(page)!.keyId +
            '/models/refresh',
    );
    await page.getByRole('button', { name: servicesCopy('zh').fetchModels, exact: true }).click();
    const refreshResponse = await accepted;
    expect(refreshResponse.status()).toBe(202);
    const refresh = await refreshResponse.json();
    expect(refresh.operation_id).toBeTruthy();
    expect(refresh.evidence).toMatchObject({ state: 'checking' });
    await expect
      .poll(async () => {
        const response = await api(user, '/api/discovery-sources/' + sourceId);
        expect(response.status()).toBe(200);
        return (await response.json()).state;
      })
      .toBe('succeeded');
    await expect(
      page.getByText(zh.user.core['endpoints.discoveryEmpty'], { exact: true }).first(),
    ).toBeVisible();
    await expect(page.getByText(servicesCopy('zh').pulledListEmpty, { exact: true })).toBeVisible();
    await control(user, 'failed');
    await page.getByRole('button', { name: servicesCopy('zh').fetchModels, exact: true }).click();
    await expect(
      page.getByRole('alert').filter({
        hasText: zh.user.core['endpoints.discoveryReasonUnavailable'],
      }),
    ).toBeVisible();
    await manual(page, 'manual-chat-' + unique(), prefix, 'zh');
    await expect(connect(page, 'zh')).toBeVisible();
    await connect(page, 'zh').click();
    await expect(page.getByText(connectionCopy('zh').mappingSaved, { exact: true })).toBeVisible();
    expect((await models(user, prefix))[0].binding_count).toBe('1');
    await screenshot(page, 'resources-manual-zh-narrow-dark.png');
    await page.getByRole('link', { name: servicesCopy('zh').apiKeyLink, exact: true }).click();
    await page.getByRole('link', { name: zh.user.core['keys.charityModels'], exact: true }).click();
    await expect(page).toHaveURL(/\/charity$/);
  } finally {
    await control(user, 'success');
    await user.close();
  }
});

test('same call names require only an actual collision choice and retain existing routing settings', async ({
  browser,
}) => {
  const user = await context(browser),
    name = 'collision-' + unique(),
    prefix = 'same-' + unique();
  try {
    await service(user, name);
    const response = await api(user, '/api/models', 'POST', {
      provider: prefix,
      model: 'chat',
      route_strategy: 'random',
      silent_retry: false,
      transport_rule: 'passthrough',
      flatten_tool_calls: true,
    });
    expect(response.status()).toBe(201);
    const previous = (await response.json()) as { id: string };
    const page = await user.newPage();
    await open(page);
    await existing(page, name);
    const row = await manual(page, 'chat', prefix);
    await connect(page).click();
    await expect(
      row.getByRole('button', { name: connectionCopy('en').existingModel, exact: true }),
    ).toBeVisible();
    expect(await models(user, prefix)).toHaveLength(1);
    await row
      .getByRole('button', { name: connectionCopy('en').existingModel, exact: true })
      .click();
    await row.getByRole('searchbox').fill(prefix + '/chat');
    await row.getByRole('button', { name: connectionCopy('en').search, exact: true }).click();
    await row
      .getByRole('table', { name: connectionCopy('en').existingModel, exact: true })
      .getByRole('row')
      .filter({ has: page.getByText(prefix + '/chat', { exact: true }) })
      .getByRole('button', { name: connectionCopy('en').select, exact: true })
      .click();
    await connect(page).click();
    await expect(page.getByText(connectionCopy('en').mappingSaved, { exact: true })).toBeVisible();
    const saved = (await models(user, prefix))[0];
    expect(saved).toMatchObject({
      id: previous.id,
      route_strategy: 'random',
      silent_retry: false,
      transport_rule: 'passthrough',
      flatten_tool_calls: true,
      binding_count: '1',
    });
    await open(page);
    await existing(page, name);
    const renamed = await manual(page, 'another-upstream', prefix);
    await renamed.getByLabel(connectionCopy('en').model, { exact: true }).fill('chat');
    await connect(page).click();
    await renamed.getByLabel(connectionCopy('en').model, { exact: true }).fill('other-chat');
    await connect(page).click();
    await expect(page.getByText(connectionCopy('en').mappingSaved, { exact: true })).toBeVisible();
    expect(await models(user, prefix)).toHaveLength(2);
    await screenshot(page, 'resources-collision-rename-en.png');
  } finally {
    await user.close();
  }
});

test('lost responses for every creation stage reconcile committed real resources without duplicates', async ({
  browser,
}) => {
  const user = await context(browser),
    prefix = 'recover-' + unique();
  try {
    const page = await user.newPage(),
      counts = new Map<string, number>();
    await page.route('**/api/**', async (route) => {
      const request = route.request(),
        path = new URL(request.url()).pathname;
      if (
        request.method() !== 'POST' ||
        !(
          path === '/api/connections' ||
          /^\/api\/connections\/\d+\/(credentials|discovery-sources)$/.test(path) ||
          /^\/api\/discovery-sources\/\d+\/refresh$/.test(path) ||
          path === '/api/endpoints' ||
          /^\/api\/endpoints\/\d+\/keys$/.test(path) ||
          path.endsWith('/models/refresh') ||
          path.endsWith('/models/manual') ||
          path === '/api/models' ||
          path.endsWith('/bindings/batch-receipt')
        )
      ) {
        await route.continue();
        return;
      }
      counts.set(path, (counts.get(path) ?? 0) + 1);
      const response = await route.fetch();
      expect(response.status()).toBeLessThan(300);
      await route.abort('failed');
    });
    await open(page);
    await newService(page, prefix, true);
    await expect(
      page.getByRole('button', { name: servicesCopy('en').fetchModels, exact: true }),
    ).toBeVisible();
    const upstream = 'recovered-chat-' + unique();
    await manual(page, upstream, prefix);
    for (let attempt = 0; attempt < 4; attempt++) {
      if (await page.getByText(connectionCopy('en').mappingSaved, { exact: true }).count()) break;
      await connect(page).click();
      await expect
        .poll(
          async () =>
            (await page
              .getByText(connectionCopy('en').mappingSaved, { exact: true })
              .isVisible()) || (await connect(page).isEnabled()),
        )
        .toBe(true);
    }
    await expect(page.getByText(connectionCopy('en').mappingSaved, { exact: true })).toBeVisible();
    const savedModels = await models(user, prefix);
    expect(savedModels).toHaveLength(1);
    const meta = resources.get(page)!;
    expect([...counts.keys()].sort()).toEqual(
      [
        '/api/connections',
        '/api/connections/' + meta.connectionId + '/credentials',
        '/api/endpoints',
        '/api/endpoints/' + meta.endpointId + '/keys',
        '/api/endpoints/' + meta.endpointId + '/keys/' + meta.keyId + '/models/refresh',
        '/api/endpoints/' + meta.endpointId + '/keys/' + meta.keyId + '/models/manual',
        '/api/models',
        '/api/models/' + savedModels[0].id + '/bindings/batch-receipt',
      ].sort(),
    );
    expect([...counts.values()].every((count) => count === 1)).toBe(true);
    await screenshot(page, 'resources-recovered-en.png');
  } finally {
    await user.close();
  }
});

test('partial completion keeps finished models and retries only the unfinished connection', async ({
  browser,
}) => {
  const user = await context(browser),
    name = 'partial-' + unique(),
    prefix = 'partial-' + unique();
  try {
    await service(user, name);
    const page = await user.newPage();
    await open(page);
    await existing(page, name);
    await manual(page, 'first-chat', prefix);
    await manual(page, 'second-chat', prefix);
    const keys: string[] = [];
    await page.route('**/bindings/batch-receipt', async (route) => {
      keys.push(route.request().headers()['idempotency-key']);
      if (keys.length === 2)
        await route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: JSON.stringify({
            error: { code: 'internal', message: 'The connection could not be saved.' },
          }),
        });
      else await route.continue();
    });
    await connect(page, 'en', 2).click();
    await expect(
      page.getByRole('button', { name: connectionCopy('en').resumeUnrecorded, exact: true }),
    ).toBeVisible();
    const partial = await models(user, prefix);
    expect(partial).toHaveLength(2);
    expect(partial.map((model) => model.binding_count).sort()).toEqual(['0', '1']);
    await page
      .getByRole('button', { name: connectionCopy('en').resumeUnrecorded, exact: true })
      .click();
    await connect(page, 'en', 1).click();
    await expect(page.getByText(connectionCopy('en').mappingSaved, { exact: true })).toHaveCount(2);
    expect(keys).toHaveLength(3);
    expect(keys[2]).toBe(keys[1]);
    expect((await models(user, prefix)).map((model) => model.binding_count)).toEqual(['1', '1']);
    await screenshot(page, 'resources-partial-continued-en.png');
  } finally {
    await user.close();
  }
});

test('stopping an in-flight step keeps a committed resource for explicit continuation and ignores its late reply', async ({
  browser,
}) => {
  const user = await context(browser),
    name = 'stop-' + unique();
  try {
    const page = await user.newPage();
    let release!: () => void, committed!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const saved = new Promise<void>((resolve) => {
      committed = resolve;
    });
    await page.route('**/api/endpoints', async (route) => {
      if (route.request().method() !== 'POST') {
        await route.continue();
        return;
      }
      const response = await route.fetch();
      expect(response.status()).toBe(201);
      committed();
      await gate;
      await route.fulfill({ response }).catch(() => undefined);
    });
    await open(page);
    const c = connectionCopy('en');
    const panel = servicePanel(page);
    await fillService(panel, {
      name,
      url: fixture().upstream_url + '/v1',
      secret: fixture().private_markers[0],
    });
    await panel.getByRole('button', { name: servicesCopy('en').addAndFetch, exact: true }).click();
    await saved;
    await panel.getByRole('button', { name: c.close, exact: true }).click();
    release();
    await expect(page.getByLabel(c.secret, { exact: true })).toHaveCount(0);
    const endpoints = await api(user, '/api/endpoints?page=1&page_size=100&q=' + name);
    expect((await endpoints.json()).data).toHaveLength(1);
    const savedEndpoint = (await endpoints.json()).data[0] as { id: string };
    await page.goto(fixture().user_url + '/endpoints/' + savedEndpoint.id);
    await page
      .getByRole('button', { name: en.user.core['endpoints.addKey'], exact: false })
      .first()
      .click();
    await page.getByRole('radio', { name: c.newCredential, exact: true }).locator('..').click();
    await expect(page.getByRole('radio', { name: c.newCredential, exact: true })).toBeChecked();
    await expect(page.getByLabel(c.secret, { exact: true })).toBeVisible();
    await expect(page.getByLabel(c.secret, { exact: true })).toHaveValue('');
    await screenshot(page, 'resources-stop-continue-en.png');
  } finally {
    await user.close();
  }
});
