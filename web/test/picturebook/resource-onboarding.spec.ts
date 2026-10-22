import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

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
  { name: string; endpointId: string; connectionId?: string; keyId?: string; sourceId?: string }
>();
async function open(page: Page, locale: Locale = 'en') {
  await page.goto(fixture().user_url + '/endpoints?quickstart=1');
  await expect(
    page.getByRole('heading', { name: connectionCopy(locale).addConnection, exact: true }),
  ).toBeVisible();
}
async function newService(page: Page, name: string, locale: Locale = 'en') {
  const c = connectionCopy(locale);
  const panel = page
    .locator('.nb-expandable-panel')
    .filter({ has: page.getByRole('heading', { name: c.addConnection, exact: true }) });
  await panel.getByLabel(c.name, { exact: true }).fill(name);
  await panel.getByRole('button', { name: c.create, exact: true }).click();
  await page.getByRole('button', { name: c.addEntry, exact: true }).first().click();
  const entry = page
    .locator('.nb-expandable-panel')
    .filter({ has: page.getByRole('heading', { name: c.addEntry, exact: true }) });
  await entry
    .getByRole('combobox', { name: c.protocol, exact: true })
    .selectOption({ label: (locale === 'zh' ? zh : en).user.core['connector.openai'] });
  await entry.getByLabel(c.entryURL, { exact: true }).fill(fixture().upstream_url + '/v1');
  await entry
    .getByRole('checkbox', { name: sharedCopy(locale).modelTypes.responses, exact: true })
    .check();
  await entry.getByLabel(c.note + ' ' + c.optional, { exact: true }).fill(name);
  await entry.getByRole('button', { name: c.saveEntries, exact: true }).click();
  // Lost-response scenarios recover through the normal operation receipt.
  await expect(entry.getByText(c.entrySaved)).toBeVisible();
  const response = await api(
    page.context(),
    '/api/endpoints?page=1&page_size=100&q=' + encodeURIComponent(name),
  );
  const rows = (await response.json()).data as { id: string; note: string }[];
  expect(rows.filter((row) => row.note === name)).toHaveLength(1);
  resources.set(page, { name, endpointId: rows.find((row) => row.note === name)!.id });
}
async function newKey(page: Page, locale: Locale = 'en') {
  const c = connectionCopy(locale),
    meta = resources.get(page)!;
  const entry = page
    .locator('.nb-expandable-panel')
    .filter({ has: page.getByRole('heading', { name: c.addEntry, exact: true }) });
  await entry.getByRole('radio', { name: c.newCredential, exact: true }).locator('..').click();
  await expect(entry.getByRole('radio', { name: c.newCredential, exact: true })).toBeChecked();
  await entry
    .getByLabel(c.credentialName + ' ' + c.optional, { exact: true })
    .fill(meta.name + ' credential');
  await entry.getByLabel(c.secret, { exact: true }).fill(fixture().private_markers[0]);
  await entry.getByRole('checkbox', { name: c.ownership, exact: true }).check();
  await entry.getByRole('button', { name: c.saveCredential, exact: true }).click();
  const authorize = entry.getByRole('button', { name: c.authorizeEntry, exact: true });
  await expect(authorize).toBeVisible();
  const grant = entry
    .locator('form')
    .filter({ has: page.getByRole('button', { name: c.authorizeEntry, exact: true }) });
  await grant.getByLabel(c.note + ' ' + c.optional, { exact: true }).fill(meta.name + '-key');
  await authorize.click();
  await expect(entry.getByText(c.grantSaved, { exact: true })).toBeVisible();
  const keys = await api(
    page.context(),
    '/api/endpoints/' + meta.endpointId + '/keys?page=1&page_size=100',
  );
  const savedKey = (await keys.json()).data.find(
    (row: { note: string }) => row.note === meta.name + '-key',
  ) as { id: string; credential_id: string; credential_version: string; revision: string };
  meta.keyId = savedKey.id;
  await entry.getByRole('button', { name: c.close, exact: true }).first().click();
  await page.getByRole('tab', { name: c.discoverySources, exact: true }).click();
  await page.getByRole('button', { name: c.addSource, exact: true }).click();
  const source = page
    .locator('.nb-expandable-panel')
    .filter({ has: page.getByRole('heading', { name: c.addSource, exact: true }) });
  await source.getByLabel(c.name + ' ' + c.optional, { exact: true }).fill(meta.name + ' catalog');
  await source.getByLabel(c.discoveryBaseURL, { exact: true }).fill(fixture().upstream_url + '/v1');
  await source
    .getByRole('table', { name: c.chooseCredential, exact: true })
    .getByRole('button', { name: c.select, exact: true })
    .click();
  await source.getByRole('button', { name: c.saveSource, exact: true }).click();
  await expect(source).toHaveCount(0);
  const connectionId = new URL(page.url()).searchParams.get('connection_id');
  expect(connectionId).toBeTruthy();
  meta.connectionId = connectionId!;
  const sources = await api(
    page.context(),
    '/api/connections/' + connectionId + '/discovery-sources?page=1&page_size=100',
  );
  const savedSource = (await sources.json()).data.find(
    (row: { name: string }) => row.name === meta.name + ' catalog',
  ) as { id: string; credential_id: string; credential_version: string };
  meta.sourceId = savedSource.id;
  expect(savedSource.credential_id).toBe(savedKey.credential_id);
  expect(savedSource.credential_version).toBe(savedKey.credential_version);
  await page.goto(fixture().user_url + '/endpoints?discovery_source_id=' + meta.sourceId);
  const associations = page
    .locator('details')
    .filter({ has: page.locator(':scope > summary').filter({ hasText: c.associateEntries }) });
  await associations.locator(':scope > summary').click();
  await associations
    .getByRole('table', { name: c.chooseEntry, exact: true })
    .getByRole('row')
    .filter({ hasText: meta.name })
    .getByRole('button', { name: c.select, exact: true })
    .click();
  const keyRow = associations
    .getByRole('table', { name: meta.name, exact: true })
    .getByRole('row')
    .filter({ hasText: meta.name + '-key' });
  const associate = keyRow.getByRole('button', { name: c.useSource, exact: true });
  await expect(associate).toBeEnabled();
  const associationSaved = page.waitForResponse(
    (response) =>
      response.request().method() === 'PATCH' &&
      new URL(response.url()).pathname ===
        '/api/endpoints/' + meta.endpointId + '/keys/' + meta.keyId,
  );
  await associate.click();
  const associationResponse = await associationSaved;
  expect(associationResponse.status()).toBe(200);
  expect(associationResponse.request().postDataJSON()).toEqual({
    discovery_source_id: meta.sourceId,
    expected_revision: savedKey.revision,
  });
  await expect(keyRow.getByRole('button', { name: c.detachSource, exact: true })).toBeVisible();
  const linkedKeys = await api(
    page.context(),
    '/api/endpoints/' + meta.endpointId + '/keys?page=1&page_size=100',
  );
  expect(
    (await linkedKeys.json()).data.find((row: { id: string }) => row.id === meta.keyId),
  ).toMatchObject({
    credential_id: savedKey.credential_id,
    credential_version: savedKey.credential_version,
    discovery_source_id: meta.sourceId,
  });
  await page.getByRole('button', { name: c.refreshSource, exact: true }).click();
}
async function manual(page: Page, upstream: string, prefix: string, locale: Locale = 'en') {
  const c = connectionCopy(locale),
    meta = resources.get(page)!;
  const fold = page
    .locator('details')
    .filter({ has: page.locator(':scope > summary').filter({ hasText: c.manualModel }) });
  if ((await fold.getAttribute('open')) === null) await fold.locator(':scope > summary').click();
  await fold.getByLabel(c.upstreamModel, { exact: true }).fill(upstream);
  await fold.getByRole('button', { name: c.configureModel, exact: true }).click();
  const row = page.locator('details').filter({
    has: page
      .locator(':scope > summary strong')
      .filter({ hasText: new RegExp('^' + upstream + '$') }),
  });
  if (
    !(await row.getByRole('checkbox', { name: meta.name + ' · ' + upstream, exact: true }).count())
  ) {
    const choose = page
      .locator('details')
      .filter({ has: page.locator(':scope > summary').filter({ hasText: c.chooseEntry }) })
      .first();
    if ((await choose.getAttribute('open')) === null)
      await choose.locator(':scope > summary').click();
    await choose
      .getByRole('table', { name: c.chooseEntry, exact: true })
      .getByRole('row')
      .filter({ hasText: meta.name })
      .getByRole('button', { name: c.select, exact: true })
      .click();
    await choose
      .getByRole('table', { name: c.chooseCredential, exact: true })
      .getByRole('row')
      .filter({ hasText: meta.name + '-key' })
      .getByRole('button', { name: c.select, exact: true })
      .click();
  }
  await row.getByRole('checkbox', { name: meta.name + ' · ' + upstream, exact: true }).check();
  await row
    .getByRole('checkbox', { name: sharedCopy(locale).modelTypes.chat_completions, exact: true })
    .check();
  await row
    .getByRole('checkbox', { name: sharedCopy(locale).modelTypes.responses, exact: true })
    .check();
  await row.getByLabel(c.provider, { exact: true }).fill(prefix);
  await row
    .getByRole('checkbox', { name: sharedCopy(locale).modelTypes.chat_responses, exact: true })
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
    keyId: key.id,
    sourceId: key.discovery_source_id,
  });
  await page.goto(fixture().user_url + '/endpoints?discovery_source_id=' + key.discovery_source_id);
  await expect(
    page.getByRole('button', { name: connectionCopy(locale).refreshSource, exact: true }),
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
    await newKey(page);
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
      .getByRole('button', { name: connectionCopy('en').backToSources, exact: true })
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
    await page.getByRole('link', { name: connectionCopy('en').manageEntry, exact: true }).click();
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
        new URL(response.url()).pathname === '/api/discovery-sources/' + sourceId + '/refresh',
    );
    await page
      .getByRole('button', { name: connectionCopy('zh').refreshSource, exact: true })
      .click();
    const refreshResponse = await accepted;
    expect(refreshResponse.status()).toBe(202);
    const refresh = await refreshResponse.json();
    expect(refresh.operation_id).toBeTruthy();
    expect(refresh.discovery_source).toMatchObject({ id: sourceId, state: 'checking' });
    await expect
      .poll(async () => {
        const response = await api(user, '/api/discovery-sources/' + sourceId);
        expect(response.status()).toBe(200);
        return (await response.json()).state;
      })
      .toBe('succeeded');
    await expect(
      page.getByText(connectionCopy('zh').sourceStates.succeeded, { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(connectionCopy('zh').emptyTitle, { exact: true }).first(),
    ).toBeVisible();
    await control(user, 'failed');
    await page
      .getByRole('button', { name: connectionCopy('zh').refreshSource, exact: true })
      .click();
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
    await newService(page, prefix);
    await newKey(page);
    await expect(
      page.getByRole('button', { name: connectionCopy('en').refreshSource, exact: true }),
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
    expect(await models(user, prefix)).toHaveLength(1);
    expect(counts.size).toBe(9);
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
    const group = page
      .locator('.nb-expandable-panel')
      .filter({ has: page.getByRole('heading', { name: c.addConnection, exact: true }) });
    await group.getByLabel(c.name, { exact: true }).fill(name);
    await group.getByRole('button', { name: c.create, exact: true }).click();
    await page.getByRole('button', { name: c.addEntry, exact: true }).first().click();
    const entry = page
      .locator('.nb-expandable-panel')
      .filter({ has: page.getByRole('heading', { name: c.addEntry, exact: true }) });
    await entry
      .getByRole('combobox', { name: c.protocol, exact: true })
      .selectOption({ label: en.user.core['connector.openai'] });
    await entry.getByLabel(c.entryURL, { exact: true }).fill(fixture().upstream_url + '/v1');
    await entry.getByLabel(c.note + ' ' + c.optional, { exact: true }).fill(name);
    await entry.getByRole('button', { name: c.saveEntries, exact: true }).click();
    await saved;
    await entry.getByRole('button', { name: c.close, exact: true }).first().click();
    release();
    await expect(page.getByLabel(c.secret, { exact: true })).toHaveCount(0);
    const endpoints = await api(user, '/api/endpoints?page=1&page_size=100&q=' + name);
    expect((await endpoints.json()).data).toHaveLength(1);
    await page.getByRole('link', { name: c.manageEntry, exact: true }).click();
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
