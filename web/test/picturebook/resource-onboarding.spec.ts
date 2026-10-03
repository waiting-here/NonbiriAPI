import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };

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
const copy = (locale: Locale) => (locale === 'zh' ? zh : en).user.quickstart;
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
async function open(page: Page, locale: Locale = 'en') {
  await page.goto(fixture().user_url + '/endpoints?quickstart=1');
  await expect(
    page.getByRole('heading', { name: servicesCopy(locale).quickTitle, exact: true }),
  ).toBeVisible();
}
async function newService(page: Page, name: string, locale: Locale = 'en') {
  const text = copy(locale);
  await page.getByRole('radio', { name: servicesCopy(locale).otherService, exact: true }).check();
  await page
    .getByRole('radiogroup', { name: text.connector, exact: true })
    .getByRole('radio', { name: 'OpenAI-compatible', exact: true })
    .check();
  await page.getByLabel(text.address, { exact: true }).fill(fixture().upstream_url + '/v1');
  await page.getByLabel(text.serviceNote, { exact: true }).fill(name);
  await page.getByRole('button', { name: text.createService, exact: true }).click();
  await expect(page.getByLabel(text.secret, { exact: true })).toBeVisible();
}
async function newKey(page: Page, locale: Locale = 'en') {
  const text = copy(locale);
  await page.getByLabel(text.secret, { exact: true }).fill(fixture().private_markers[0]);
  await page.getByLabel(text.ownership, { exact: true }).check();
  await page.getByRole('button', { name: text.addKey, exact: true }).click();
  await expect(page.getByLabel(servicesCopy(locale).prefix, { exact: true })).toBeVisible();
}
async function manual(page: Page, upstream: string, prefix: string, locale: Locale = 'en') {
  const text = copy(locale);
  const fold = page.locator('.quickstart details').filter({
    has: page.getByRole('textbox', { name: text.upstream, exact: true, includeHidden: true }),
  });
  if ((await fold.getAttribute('open')) === null) await fold.locator(':scope > summary').click();
  const form = fold.locator('form');
  await form.getByLabel(text.upstream, { exact: true }).fill(upstream);
  await form.getByRole('button', { name: text.manual, exact: true }).click();
  await expect(page.getByLabel(upstream, { exact: true })).toBeVisible();
  await page.getByLabel(servicesCopy(locale).prefix, { exact: true }).fill(prefix);
  await page.getByLabel(upstream, { exact: true }).check();
  return page.locator('.quickstart-model').filter({ hasText: upstream });
}
function connect(page: Page, locale: Locale = 'en', count = 1) {
  return page.getByRole('button', {
    name: servicesCopy(locale).addModels.replace('{{count}}', String(count)),
    exact: true,
  });
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
    base_url: fixture().upstream_url + '/v1',
    note: name,
    enabled: true,
  });
  expect(response.status()).toBe(201);
  const endpoint = (await response.json()) as { id: string };
  const keyResponse = await api(context, '/api/endpoints/' + endpoint.id + '/keys', 'POST', {
    secret: fixture().private_markers[0],
    ownership_confirmed: true,
    enabled: true,
    note: name + '-key',
    force_store_false: false,
  });
  expect(keyResponse.status()).toBe(201);
  return { endpoint, key: (await keyResponse.json()) as { id: string } };
}
async function existing(page: Page, name: string, locale: Locale = 'en') {
  const text = copy(locale);
  const source = page.getByRole('radio', {
    name: servicesCopy(locale).existingService,
    exact: true,
  });
  await source.locator('..').click();
  await expect(source).toBeChecked();
  await page.getByLabel(text.serviceSearch, { exact: true }).fill(name);
  await expect(page.locator('.quickstart-choice-list li')).toHaveCount(1);
  await page.getByRole('button', { name: text.useService, exact: true }).click();
  await page.getByRole('button', { name: text.existingKey, exact: true }).click();
  await page.getByLabel(text.keySearch, { exact: true }).fill(name + '-key');
  await page.locator('.quickstart-choice-list button').click();
  await expect(page.getByLabel(servicesCopy(locale).prefix, { exact: true })).toBeVisible();
  await page
    .locator('.quickstart details')
    .filter({
      has: page.getByRole('button', { name: text.check, exact: true, includeHidden: true }),
    })
    .locator(':scope > summary')
    .click();
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
    await page.locator('.quickstart-models input[type=checkbox]').first().check();
    await page.getByLabel(servicesCopy('en').prefix, { exact: true }).fill(prefix);
    await connect(page).focus();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('status').filter({ hasText: copy('en').finished })).toBeVisible();
    const saved = await models(user, prefix);
    expect(saved).toHaveLength(1);
    expect(saved[0].binding_count).toBe('1');
    expect(
      await page.evaluate(() =>
        JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
      ),
    ).not.toContain(fixture().private_markers[0]);
    await expect(page.locator('.quickstart-models')).toHaveCount(0);
    await expect(
      page.getByRole('button', { name: '← ' + servicesCopy('en').back, exact: true }),
    ).toBeVisible();
    await screenshot(page, 'resources-connected-en-wide-light.png');
    await page.getByRole('button', { name: servicesCopy('en').done, exact: true }).click();
    await expect(page).toHaveURL(/\/endpoints$/);
    await page
      .getByRole('link')
      .filter({ has: page.getByText(prefix, { exact: true }) })
      .click();
    await expect(
      page
        .getByRole('table', { name: en.user.core['endpoints.key'], exact: true })
        .locator('tbody > tr'),
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
    await page.getByRole('button', { name: copy('zh').check, exact: true }).click();
    await expect(page.getByText(copy('zh').empty, { exact: true })).toBeVisible();
    await control(user, 'failed');
    await page.getByRole('button', { name: copy('zh').check, exact: true }).click();
    await expect(page.getByText(copy('zh').checkFailed, { exact: true })).toBeVisible();
    await manual(page, 'manual-chat-' + unique(), prefix, 'zh');
    await expect(
      page.getByText(copy('zh').selected.replace('{{count}}', '1'), { exact: true }),
    ).toBeVisible();
    await connect(page, 'zh').click();
    await expect(page.getByRole('status').filter({ hasText: copy('zh').finished })).toBeVisible();
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
    await expect(row.getByText(copy('en').sameName, { exact: true })).toBeVisible();
    expect(await models(user, prefix)).toHaveLength(1);
    await row.getByLabel(copy('en').append, { exact: true }).check();
    await connect(page).click();
    await expect(page.getByRole('status').filter({ hasText: copy('en').finished })).toBeVisible();
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
    await renamed.getByLabel(copy('en').callName, { exact: true }).fill('chat');
    await connect(page).click();
    await renamed.getByLabel(copy('en').rename, { exact: true }).check();
    await renamed.getByLabel(copy('en').callName, { exact: true }).fill('other-chat');
    await connect(page).click();
    await expect(page.getByRole('status').filter({ hasText: copy('en').finished })).toBeVisible();
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
          path === '/api/endpoints' ||
          /^\/api\/endpoints\/\d+\/keys$/.test(path) ||
          path.endsWith('/models/refresh') ||
          path.endsWith('/models/manual') ||
          path === '/api/models' ||
          path.endsWith('/bindings/batch')
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
    await expect(page.locator('.quickstart-models .quickstart-model').first()).toBeVisible();
    const upstream = 'recovered-chat-' + unique();
    await manual(page, upstream, prefix);
    await connect(page).click();
    await expect(page.getByText(copy('en').modelSaved, { exact: true })).toBeVisible();
    await connect(page).click();
    await expect(page.getByRole('status').filter({ hasText: copy('en').finished })).toBeVisible();
    expect(await models(user, prefix)).toHaveLength(1);
    expect([...counts.values()]).toEqual([1, 1, 1, 1, 1, 1]);
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
    await page.route('**/bindings/batch', async (route) => {
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
      page.getByRole('button', { name: copy('en').checkResult, exact: true }),
    ).toBeVisible();
    const partial = await models(user, prefix);
    expect(partial).toHaveLength(2);
    expect(partial.map((model) => model.binding_count).sort()).toEqual(['0', '1']);
    await page.getByRole('button', { name: copy('en').checkResult, exact: true }).click();
    await expect(page.getByRole('status').filter({ hasText: copy('en').finished })).toBeVisible();
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
    await page.getByRole('radio', { name: servicesCopy('en').otherService, exact: true }).check();
    await page
      .getByRole('radiogroup', { name: copy('en').connector, exact: true })
      .getByRole('radio', { name: 'OpenAI-compatible', exact: true })
      .check();
    await page.getByLabel(copy('en').address, { exact: true }).fill(fixture().upstream_url + '/v1');
    await page.getByLabel(copy('en').serviceNote, { exact: true }).fill(name);
    await page.getByRole('button', { name: copy('en').createService, exact: true }).click();
    await saved;
    await page.getByRole('button', { name: '← ' + servicesCopy('en').back, exact: true }).click();
    release();
    await expect(page.getByText(copy('en').stopped, { exact: true })).toBeVisible();
    await expect(page.getByLabel(copy('en').secret, { exact: true })).toHaveCount(0);
    const endpoints = await api(user, '/api/endpoints?page=1&page_size=100&q=' + name);
    expect((await endpoints.json()).data).toHaveLength(1);
    await page.getByRole('button', { name: copy('en').continue, exact: true }).click();
    await expect(page.getByLabel(copy('en').secret, { exact: true })).toBeVisible();
    await expect(page.getByLabel(copy('en').secret, { exact: true })).toHaveValue('');
    await screenshot(page, 'resources-stop-continue-en.png');
  } finally {
    await user.close();
  }
});
