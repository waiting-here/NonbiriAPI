import {
  fillService,
  protocolServices,
  readService,
  servicePanel,
  submitService,
} from '../service-onboarding';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Page,
  type TestInfo,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: { charity_model_id: string };
}
type Model = {
  id: string;
  revision: string;
  cloudflare_proxy_enabled: boolean;
  cloudflare_proxy_mode: 'worker' | 'legacy' | 'direct';
  session_headers: { name: string; idle_seconds: number; format: string }[];
};
const copy = common.common.cloudflareProxy;
const headerCopy = common.common.operations.charity.sessionHeaders;
const managedHeaders = [
  { name: 'x-browser-id', idle_seconds: 0, format: 'opencode_part' },
  { name: 'x-browser-session', idle_seconds: 300, format: 'opencode_session' },
];
type Adaptation = {
  forward_headers: { mode: string; values: string[] };
};
async function managedHeaderFields(page: Page) {
  const summary = page
    .locator('summary')
    .filter({ has: page.getByText(headerCopy.title, { exact: true }) });
  const fold = summary.locator('..');
  if ((await fold.getAttribute('open')) === null) await summary.click();
  return fold;
}
async function configureManagedHeaders(page: Page) {
  const fields = await managedHeaderFields(page);
  for (const [index, row] of [managedHeaders[1], managedHeaders[0]].entries()) {
    await fields.getByRole('button', { name: headerCopy.add, exact: true }).click();
    await fields
      .getByLabel(headerCopy.name.replace('{{n}}', String(index + 1)), { exact: true })
      .fill(row.name.toUpperCase());
    await fields
      .getByLabel(headerCopy.idle, { exact: true })
      .nth(index)
      .fill(row.idle_seconds === 0 ? '0' : '');
    await fields.getByLabel(headerCopy.format, { exact: true }).nth(index).selectOption(row.format);
  }
}
async function expectManagedHeaders(page: Page) {
  const fields = await managedHeaderFields(page);
  for (const [index, row] of managedHeaders.entries()) {
    await expect(
      fields.getByLabel(headerCopy.name.replace('{{n}}', String(index + 1)), { exact: true }),
    ).toHaveValue(row.name);
    await expect(fields.getByLabel(headerCopy.idle, { exact: true }).nth(index)).toHaveValue(
      String(row.idle_seconds),
    );
    await expect(fields.getByLabel(headerCopy.format, { exact: true }).nth(index)).toHaveValue(
      row.format,
    );
  }
}
async function saveSeparateAdaptation(
  page: Page,
  context: BrowserContext,
  origin: string,
  modelPath: string,
) {
  const before = await read(context, origin, modelPath);
  const summary = page
    .locator('summary')
    .filter({ has: page.getByText(common.requestAdaptation.model.title, { exact: true }) });
  await summary.click();
  const fold = summary.locator('..');
  const forwarded = fold.getByRole('group', {
    name: common.requestAdaptation.model.forwardHeaders,
    exact: true,
  });
  await expect(forwarded).toBeVisible();
  const mode = forwarded.getByRole('combobox', { name: 'Configuration source', exact: true });
  if (await mode.count()) await mode.selectOption('replace');
  await forwarded.getByRole('textbox').fill('x-browser-forward');
  const path = modelPath + '/request-adaptation';
  const saved = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === path && response.request().method() === 'PUT',
  );
  await fold
    .getByRole('button', { name: common.requestAdaptation.model.save, exact: true })
    .click();
  const response = await saved;
  expect(response.ok()).toBe(true);
  expect(response.request().postDataJSON()).not.toHaveProperty('session_headers');
  expect((await read<Adaptation>(context, origin, path)).forward_headers.values).toEqual([
    'x-browser-forward',
  ]);
  expect((await read(context, origin, modelPath)).session_headers).toEqual(before.session_headers);
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);
async function session(browser: Browser, role: 'admin' | 1 | 5 | 6) {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((item) => item.level === role)!.cookie;
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
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return { context, origin, prefix: role === 'admin' ? '/admin/api' : '/api/steward' };
}
function managementPath(role: 'admin' | 5 | 6, selection: Record<string, string>) {
  const query = new URLSearchParams({
    ...(role === 'admin' ? {} : { tab: 'charity' }),
    charity_section: 'models',
    ...selection,
  });
  return (role === 'admin' ? '/charity' : '/steward') + '?' + query;
}
async function read<T = Model>(context: BrowserContext, origin: string, path: string): Promise<T> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json();
}
async function patch(
  context: BrowserContext,
  origin: string,
  path: string,
  model: Model,
  enabled: boolean,
  changed: Record<string, string> = {},
) {
  const response = await context.request.patch(origin + path, {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data: { expected_revision: model.revision, cloudflare_proxy_enabled: enabled, ...changed },
  });
  expect(response.ok(), 'Synthetic model update: ' + response.status()).toBe(true);
  return response.json() as Promise<Model>;
}
async function visuals(page: Page, info: TestInfo, name: string) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.getByRole('switch', { name: copy.label, exact: true }).scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: process.env.NONBIRI_VISUAL_DIR
          ? resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${theme}-${width}.png`)
          : info.outputPath(`${name}-${theme}-${width}.png`),
        animations: 'disabled',
      });
    }
  }
  await page.setViewportSize({ width: 1440, height: 900 });
}

test('personal models default on and retain a changed setting after a real revision conflict', async ({
  browser,
}, info) => {
  const { context, origin } = await session(browser, 1);
  try {
    const page = await context.newPage();
    await page.goto(origin + '/models');
    await page
      .locator('[data-component="page-header"]')
      .getByRole('button', { name: 'Create model', exact: true })
      .click();
    const toggle = page.getByRole('switch', { name: copy.label, exact: true });
    await expect(toggle).toBeChecked();
    await expect(toggle).toHaveAccessibleDescription(copy.new);
    await visuals(page, info, 'personal-cloudflare-new');
    const name = 'proxy-' + randomUUID().slice(0, 8);
    const editor = page.locator('form.model-editor');
    await editor.getByRole('textbox', { name: 'Prefix', exact: true }).fill('browser');
    await editor.getByRole('textbox', { name: 'Model name', exact: true }).fill(name);
    await configureManagedHeaders(page);
    await toggle.uncheck();
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Edit model', exact: true })).toBeVisible();
    const id = new URL(page.url()).searchParams.get('model_id');
    expect(id).toMatch(/^[1-9]\d*$/);
    const path = '/api/models/' + id;
    let current = await read(context, origin, path);
    expect(current.cloudflare_proxy_enabled).toBe(false);
    expect(current.cloudflare_proxy_mode).toBe('worker');
    expect(current.session_headers).toEqual(managedHeaders);
    await saveSeparateAdaptation(page, context, origin, path);
    await page.getByRole('button', { name: 'Edit model', exact: true }).click();
    await expect(toggle).not.toBeChecked();
    await expectManagedHeaders(page);
    await toggle.check();
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(toggle).toHaveCount(0);
    current = await read(context, origin, path);
    expect(current.cloudflare_proxy_enabled).toBe(true);
    await page.getByRole('button', { name: 'Edit model', exact: true }).click();
    await toggle.uncheck();
    await patch(context, origin, path, current, true, { model: name + '-changed' });
    const conflict = page.waitForResponse(
      (response) =>
        response.url().endsWith(path) &&
        response.request().method() === 'PATCH' &&
        response.status() === 409,
    );
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await conflict;
    await expect(toggle).not.toBeChecked();
    await visuals(page, info, 'personal-cloudflare-conflict');
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await page.getByRole('button', { name: 'Edit model', exact: true }).click();
    await expect(toggle).toBeChecked();
    await toggle.uncheck();
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(toggle).toHaveCount(0);
    expect((await read(context, origin, path)).cloudflare_proxy_enabled).toBe(false);
    await page.reload();
    await expect(page.getByText(copy.off, { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Edit model', exact: true }).click();
    await expectManagedHeaders(page);
    expect((await read(context, origin, path)).session_headers).toEqual(managedHeaders);
    expect(
      (await read<Adaptation>(context, origin, path + '/request-adaptation')).forward_headers
        .values,
    ).toEqual(['x-browser-forward']);
  } finally {
    await context.close();
  }
});

for (const role of ['admin', 6] as const) {
  test(`${role} creates with forwarding off, saves it, and reloads a conflicting draft`, async ({
    browser,
  }, info) => {
    const { context, origin, prefix } = await session(browser, role);
    try {
      const page = await context.newPage();
      await page.goto(origin + managementPath(role, { model_workspace: 'new' }));
      const toggle = page.getByRole('switch', { name: copy.label, exact: true });
      await expect(toggle).not.toBeChecked();
      await visuals(page, info, `charity-${role}-cloudflare-new`);
      await page.getByRole('textbox', { name: 'Provider', exact: true }).fill('browser');
      await page
        .getByRole('textbox', { name: 'Model', exact: true })
        .fill('proxy-' + randomUUID().slice(0, 8));
      await configureManagedHeaders(page);
      await toggle.check();
      await page.getByRole('button', { name: 'Add charity model', exact: true }).click();
      await expect(page).toHaveURL(/charity_model=[1-9]\d*/);
      const id = new URL(page.url()).searchParams.get('charity_model');
      const path = prefix + '/charity-models/' + id;
      let current = await read(context, origin, path);
      expect(current.cloudflare_proxy_enabled).toBe(true);
      expect(current.cloudflare_proxy_mode).toBe('worker');
      expect(current.session_headers).toEqual(managedHeaders);
      await expectManagedHeaders(page);
      if (role === 'admin') await saveSeparateAdaptation(page, context, origin, path);
      await expect(toggle).toBeChecked();
      await toggle.uncheck();
      const save = page.getByRole('button', {
        name: common.common.workflow.saveTarget.replace('{{id}}', id!),
        exact: true,
      });
      await save.click();
      await expect(save).toBeEnabled();
      await expect
        .poll(async () => (await read(context, origin, path)).cloudflare_proxy_enabled)
        .toBe(false);
      await page.reload();
      await expect(toggle).not.toBeChecked();
      current = await read(context, origin, path);
      await toggle.check();
      await patch(context, origin, path, current, false, {
        public_description: 'Changed in another session',
      });
      const conflict = page.waitForResponse(
        (response) =>
          response.url().endsWith(path) &&
          response.request().method() === 'PATCH' &&
          response.status() === 409,
      );
      await save.click();
      await conflict;
      await expect(toggle).toBeChecked();
      await expect(
        page.getByText(common.common.operations.charity.modelChanged, { exact: true }),
      ).toBeVisible();
      await visuals(page, info, `charity-${role}-cloudflare-conflict`);
      await page
        .getByRole('button', { name: common.common.operations.charity.reloadModel, exact: true })
        .click();
      await expect(toggle).not.toBeChecked();
      await toggle.check();
      await save.click();
      await expect
        .poll(async () => (await read(context, origin, path)).cloudflare_proxy_enabled)
        .toBe(true);
      await page.reload();
      await expect(toggle).toBeChecked();
      await expectManagedHeaders(page);
      expect((await read(context, origin, path)).session_headers).toEqual(managedHeaders);
      if (role === 'admin')
        expect(
          (await read<Adaptation>(context, origin, path + '/request-adaptation')).forward_headers
            .values,
        ).toEqual(['x-browser-forward']);
    } finally {
      await context.close();
    }
  });
}

test('level 5 reads the saved setting and cannot change the global model policy', async ({
  browser,
}) => {
  const { context, origin, prefix } = await session(browser, 5);
  try {
    const id = fixture().management.charity_model_id;
    const path = prefix + '/charity-models/' + id;
    const current = await read(context, origin, path);
    const page = await context.newPage();
    await page.goto(origin + managementPath(5, { charity_model: id }));
    await expect(page.getByText(copy.label, { exact: true })).toBeVisible();
    const forwarding = page.getByRole('switch', { name: copy.label, exact: true });
    await expect(forwarding).toBeChecked({ checked: current.cloudflare_proxy_enabled });
    await forwarding.setChecked(!current.cloudflare_proxy_enabled);
    const submitted = page.waitForResponse(
      (response) =>
        response.request().method() === 'PATCH' && new URL(response.url()).pathname === path,
    );
    await page
      .getByRole('button', {
        name: common.common.workflow.saveTarget.replace('{{id}}', id),
        exact: true,
      })
      .click();
    const denied = await submitted;
    expect(denied.status()).toBe(403);
    expect((await denied.json()).error.code).toBe('steward_permission_denied');
    await expect(
      page.getByText(common.management.permissions.denied, { exact: true }),
    ).toBeVisible();
    await expect(forwarding).toBeChecked({ checked: !current.cloudflare_proxy_enabled });
    expect((await read(context, origin, path)).cloudflare_proxy_enabled).toBe(
      current.cloudflare_proxy_enabled,
    );
  } finally {
    await context.close();
  }
});

// Control-plane coverage uses the isolated backend and never sends inference traffic.
test('shared connection preserves explicit OpenAI, Anthropic, and Gateway v4 grants', async ({
  browser,
}, info) => {
  test.setTimeout(120000);
  const { context, origin } = await session(browser, 1);
  const c = common.common.connections;
  const native = common.common.nativeServices;
  const name = 'connection-' + randomUUID().slice(0, 8);
  const entries: { id: string; keyId: string; services: string[] }[] = [];
  const create = async (path: string, data: unknown) => {
    const response = await context.request.post(origin + path, {
      headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
      data,
    });
    expect(response.ok(), path + ': ' + response.status()).toBe(true);
    return response.json();
  };
  try {
    const page = await context.newPage();
    let connectionId = '',
      sharedCredential = '',
      sharedVersion = '',
      credentialLabel = '';
    const cases = [
      {
        type: 'openai-compatible',
        label: 'OpenAI-compatible',
        url: 'https://api.example.com/v1',
        services: ['chat_completions', 'responses'],
        labels: [common.common.modelTypes.chat_completions, common.common.modelTypes.responses],
      },
      {
        type: 'anthropic-compatible',
        label: 'Anthropic-compatible',
        url: 'https://api.example.com/anthropic',
        services: ['anthropic_messages'],
        labels: [native.anthropic_messages],
      },
      {
        type: 'ai-sdk-gateway-v4',
        label: common.common.gatewayV4Name,
        url: 'https://api.example.com/gateway',
        services: ['gateway_text', 'images_generations', 'embeddings', 'rerank'],
        labels: [
          native.gateway_text,
          common.common.modelTypes.images_generations,
          common.common.modelTypes.embeddings,
          common.common.modelTypes.rerank,
        ],
      },
    ];
    for (const [index, item] of cases.entries()) {
      if (index) {
        await page.goto(origin + '/endpoints?connection_id=' + connectionId);
        await page.getByRole('button', { name: c.addEntry, exact: true }).first().click();
      } else await page.goto(origin + '/endpoints?quickstart=1');
      const panel = servicePanel(page, 'en', index > 0);
      const entryName = name + '-' + index;
      await fillService(panel, {
        name: entryName,
        url: item.url,
        protocol: item.type,
        discovery: false,
        ...(index === 0 ? { secret: 'synthetic-browser-shared-credential' } : {}),
      });
      await submitService(panel);
      const { endpoint, key } = await readService(page, entryName);
      expect(endpoint.configuration_type).toBe(item.type);
      expect(endpoint.base_url).toBe(item.url);
      const configured = await protocolServices(page, item.type);
      expect(endpoint.native_services.slice().sort()).toEqual(configured.slice().sort());
      expect(endpoint.native_services).toEqual(expect.arrayContaining(item.services));
      expect(key.native_services.slice().sort()).toEqual(configured.slice().sort());
      if (index === 0) {
        connectionId = endpoint.connection_id;
        sharedCredential = key.credential_id;
        sharedVersion = key.credential_version;
        credentialLabel = key.display_head + '••••' + key.display_tail;
      }
      expect(endpoint.connection_id).toBe(connectionId);
      expect(key.credential_id).toBe(sharedCredential);
      expect(key.credential_version).toBe(sharedVersion);
      expect(key.discovery_source_id).toBeNull();
      expect(await panel.getByLabel(c.secret, { exact: true }).count()).toBe(0);
      entries.push({ id: endpoint.id, keyId: key.id, services: item.services });
    }
    const connection = { id: connectionId };
    await page.goto(origin + '/endpoints?connection_id=' + connectionId);
    await page.getByRole('tab', { name: c.discoverySources, exact: true }).click();
    await page.getByRole('button', { name: c.addSource, exact: true }).click();
    const sourcePanel = page
      .locator('.nb-expandable-panel')
      .filter({ has: page.getByRole('heading', { name: c.addSource, exact: true }) });
    await sourcePanel
      .getByLabel(c.name + ' ' + c.optional, { exact: true })
      .fill(name + ' catalog');
    await sourcePanel
      .getByLabel(c.discoveryBaseURL, { exact: true })
      .fill('https://api.example.com/v1');
    await sourcePanel
      .getByRole('combobox', { name: c.listFormat, exact: true })
      .selectOption('openai');
    await sourcePanel
      .getByRole('table', { name: c.chooseCredential, exact: true })
      .getByRole('row')
      .filter({ hasText: credentialLabel })
      .getByRole('button', { name: c.select, exact: true })
      .click();
    const sourceSaved = page.waitForResponse(
      (response) =>
        response.url().endsWith('/api/connections/' + connection.id + '/discovery-sources') &&
        response.request().method() === 'POST',
    );
    await sourcePanel.getByRole('button', { name: c.saveSource, exact: true }).click();
    const source = (await (await sourceSaved).json()) as {
      id: string;
      models_url: string;
      credential_id: string;
      credential_version: string;
      discovery_source_revision: string;
    };
    await expect(page.getByText(source.models_url, { exact: true })).toBeVisible();
    expect(source.models_url).toBe('https://api.example.com/v1/models');
    const refs = [] as { key: Record<string, unknown>; upstream: string }[];
    for (const [index, entry] of entries.entries()) {
      const keyPage = await read<{ data: Record<string, unknown>[] }>(
        context,
        origin,
        '/api/endpoints/' + entry.id + '/keys?page=1&page_size=20',
      );
      const key = keyPage.data.find((row) => row.id === entry.keyId)!;
      expect(key.credential_id).toBe(source.credential_id);
      expect(key.credential_version).toBe(source.credential_version);
      const associated = await context.request.patch(
        origin + '/api/endpoints/' + entry.id + '/keys/' + entry.keyId,
        {
          headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
          data: { expected_revision: key.revision, discovery_source_id: source.id },
        },
      );
      expect(associated.ok()).toBe(true);
      const upstream = 'exact-' + index;
      await create('/api/endpoints/' + entry.id + '/keys/' + entry.keyId + '/models/manual', {
        entries: [{ upstream_model_id: upstream, provider: 'browser' }],
      });
      refs.push({ key: await associated.json(), upstream });
    }
    const logical = (await create('/api/models', {
      provider: 'browser',
      model: name,
      model_types: ['chat_completions', 'responses'],
    })) as { id: string };
    await page.goto(origin + '/models?model_id=' + logical.id);
    const groupSummary = page
      .locator('summary')
      .filter({ has: page.getByText(c.addTargetGroup, { exact: true }) });
    await groupSummary.click();
    const group = groupSummary.locator('..');
    await group
      .locator('summary')
      .filter({ has: page.getByText(c.addTarget, { exact: true }) })
      .click();
    for (const [index] of entries.entries()) {
      const candidates = group.getByRole('table', { name: c.availableTargets, exact: true });
      await candidates
        .getByRole('row')
        .filter({ hasText: refs[index]!.upstream })
        .getByRole('button', { name: c.addTarget, exact: true })
        .click();
      const target = group.getByRole('group', {
        name: index + 1 + '. ' + name + '-' + index,
        exact: true,
      });
      const labels =
        index === 0
          ? [common.common.modelTypes.chat_completions, common.common.modelTypes.responses]
          : index === 1
            ? [native.anthropic_messages]
            : [native.gateway_text];
      for (const checkbox of await target.getByRole('checkbox').all()) await checkbox.uncheck();
      for (const label of labels)
        await target.getByRole('checkbox', { name: label, exact: true }).check();
    }
    const bindingSaved = page.waitForResponse(
      (response) =>
        response.url().endsWith('/api/models/' + logical.id + '/bindings/batch-receipt') &&
        response.request().method() === 'POST',
    );
    await group.getByRole('button', { name: c.addTargetGroup, exact: true }).click();
    expect((await bindingSaved).ok()).toBe(true);
    const bindings = await read<{
      bindings: {
        targets: {
          endpoint_key_id: string;
          upstream_model_id: string;
          native_services: string[];
        }[];
      }[];
    }>(context, origin, '/api/models/' + logical.id + '/bindings');
    expect(bindings.bindings).toHaveLength(1);
    expect(bindings.bindings[0]!.targets).toHaveLength(3);
    expect(bindings.bindings[0]!.targets.map((target) => target.upstream_model_id)).toEqual(
      refs.map((ref) => ref.upstream),
    );
    expect(
      bindings.bindings[0]!.targets.map((target) => target.native_services.slice().sort()),
    ).toEqual([['chat_completions', 'responses'], ['anthropic_messages'], ['gateway_text']]);
    await page.reload();
    await expect(page.getByText(c.addTargetGroup, { exact: true }).first()).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: process.env.NONBIRI_VISUAL_DIR
        ? resolve(process.env.NONBIRI_VISUAL_DIR, 'shared-native-targets.png')
        : info.outputPath('shared-native-targets.png'),
      animations: 'disabled',
    });
  } finally {
    await context.close();
  }
});
