import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
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
import adminEn from '../../src/admin/i18n/en.json' with { type: 'json' };
import userEn from '../../src/user/i18n/en.json' with { type: 'json' };
import bindingCopy from '../../src/user/features/core/models/binding-copy/en.json' with { type: 'json' };
import type { NativeService } from '../../src/shared/nativeServices';
import type { DonationUse } from '../../src/shared/operations/donationUses';

const c = common.common.connections;
const native = common.common.modelTypes;
const channels = adminEn.admin.mainstreamChannels;
const services = userEn.user.services;
type Cookie = { Name: string; Value: string };
interface Fixture {
  user_url: string;
  admin_url: string;
  admin_cookie: Cookie;
  return_alert_id: string;
  users: { id: string; level: number; cookie: Cookie }[];
  management: { endpoint_id: string };
}
type Identity = { id: string; revision: string };
type Credential = Identity & { credential_version: string; connection_id: string | null };
type Entry = Identity & { base_url: string; note: string; native_services: NativeService[] };
type Key = Identity & {
  credential_id: string;
  credential_version: string;
  grant_revision: string;
  native_services: NativeService[];
  discovery_source_id: string | null;
};
type Source = Identity & {
  discovery_source_revision: string;
  credential_version: string;
  state: string;
  models_url: string;
};
type DonationKey = {
  id: string;
  can_manage?: boolean;
  endpoint_key_id?: string | null;
  charity_state: string;
  ended_reason: string | null;
  limits: unknown;
  usage: unknown;
  token_reserve: number;
};
type Donation = Identity & { status: string; keys: DonationKey[] };
type Target = {
  id: string;
  endpoint_key_id?: string;
  donation_key_id?: string;
  upstream_model_id: string;
  native_services: NativeService[];
};
type Bindings = { binding_revision: string; bindings: { id: string; targets: Target[] }[] };
type Numbered<T> = { data: T[] };
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);
async function session(browser: Browser, role: 'owner' | 'admin' | 5 | 6) {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin'
      ? state.admin_cookie
      : role === 'owner'
        ? state.users[0]!.cookie
        : state.users.find((row) => row.level === role)!.cookie;
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
    ...(data === undefined ? {} : { data }),
  });
  expect(response.status(), method + ' ' + path).toBe(status);
  return status === 204 ? (undefined as T) : ((await response.json()) as T);
}
const userAPI = <T>(
  context: BrowserContext,
  path: string,
  method = 'GET',
  data?: unknown,
  status?: number,
) => api<T>(context, fixture().user_url, path, method, data, status);
const adminAPI = <T>(
  context: BrowserContext,
  path: string,
  method = 'GET',
  data?: unknown,
  status?: number,
) => api<T>(context, fixture().admin_url, path, method, data, status);
async function protocolServices(owner: BrowserContext) {
  const options = await userAPI<{
    native_service_options: { connector_type: string; native_services: NativeService[] }[];
  }>(owner, '/api/endpoint-create-options');
  return options.native_service_options.find((row) => row.connector_type === 'openai-compatible')!
    .native_services;
}
async function qualifyForSteward(
  admin: BrowserContext,
  donation: Donation,
  member: string,
  name: string,
) {
  const path = `/admin/api/donations/${donation.id}/keys/${member}/models`;
  const catalog = await adminAPI<{ manual_catalog_revision: string }>(
    admin,
    path + '?page=1&page_size=20',
  );
  await adminAPI(
    admin,
    path + '/manual',
    'POST',
    {
      expected_manual_catalog_revision: catalog.manual_catalog_revision,
      entries: ['synthetic-scope-model'],
    },
    200,
  );
  const model = await adminAPI<Identity>(admin, '/admin/api/charity-models', 'POST', {
    provider: 'synthetic',
    model: 'scope-' + name,
    enabled: true,
    is_mainstream: true,
    flatten_tool_calls: false,
    model_types: ['chat_completions'],
    pricing: { mode: 'per_request', user_price: '0', donor_reward: '0' },
    discount: { enabled: false, percent: 100, start_at: null, end_at: null },
  });
  const bindingPath = `/admin/api/charity-models/${model.id}/bindings`;
  const before = await adminAPI<Bindings>(admin, bindingPath);
  await adminAPI(admin, bindingPath + '/batch-receipt', 'POST', {
    expected_binding_revision: before.binding_revision,
    selections: [
      {
        targets: [
          {
            donation_key_id: member,
            upstream_model_id: 'synthetic-scope-model',
            native_services: ['chat_completions'],
          },
        ],
      },
    ],
  });
}
function responseFor(page: Page, method: string, path: string) {
  return page.waitForResponse(
    (response) =>
      response.request().method() === method && new URL(response.url()).pathname === path,
  );
}
async function openFold(scope: Page | Locator, name: string) {
  const page = 'goto' in scope ? scope : scope.page();
  const fold = scope
    .locator('details')
    .filter({ has: page.getByText(name, { exact: true }) })
    .last();
  if ((await fold.getAttribute('open')) === null) await fold.locator(':scope > summary').click();
  return fold;
}
async function screenshot(
  page: Page,
  info: TestInfo,
  name: string,
  width: number,
  theme: 'light' | 'dark',
  focus: Locator,
  summary: unknown,
) {
  await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
  await page.evaluate((value) => {
    document.documentElement.dataset.theme = value;
  }, theme);
  await focus.scrollIntoViewIfNeeded();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
    .toBe(true);
  const directory = resolve(
    process.env.NONBIRI_VISUAL_DIR ?? 'test-results/connection-experience-evidence',
  );
  mkdirSync(directory, { recursive: true });
  const stem = name + '-' + width + '-' + theme;
  await page.screenshot({ path: resolve(directory, stem + '.png'), fullPage: false });
  writeFileSync(resolve(directory, stem + '.json'), JSON.stringify(summary, null, 2) + '\n');
  await info.attach(stem, { path: resolve(directory, stem + '.png'), contentType: 'image/png' });
}
async function noSecrets(page: Page, secrets: string[]) {
  const snapshot = await page.evaluate(() =>
    JSON.stringify({
      url: location.href,
      dom: document.documentElement.outerHTML,
      inputs: [...document.querySelectorAll('input')].map((input) => input.value),
      local: Object.entries(localStorage),
      session: Object.entries(sessionStorage),
    }),
  );
  for (const secret of secrets) expect(snapshot).not.toContain(secret);
}
async function baseURL(context: BrowserContext) {
  return (await userAPI<Entry>(context, '/api/endpoints/' + fixture().management.endpoint_id))
    .base_url;
}
async function connection(context: BrowserContext, name: string) {
  return userAPI<Identity>(context, '/api/connections', 'POST', { name });
}

test('connection search exposes entry context and separate System One setup', async ({
  browser,
}, info) => {
  const owner = await session(browser, 'owner');
  try {
    const marker = 'search-' + randomUUID();
    const name = 'Recognizable provider ' + randomUUID();
    const group = await connection(owner, name);
    const origin = await baseURL(owner);
    const primaryURL = origin + '/long-path-' + 'catalog-'.repeat(18);
    const primaryNote = 'Primary endpoint ' + 'readable provider note '.repeat(12);
    for (const [base, note] of [
      [primaryURL, primaryNote],
      [origin + '/' + marker, 'Secondary ' + marker],
    ]) {
      await userAPI<Entry>(owner, '/api/endpoints', 'POST', {
        connection_id: group.id,
        source: 'custom',
        connector_type: 'openai-compatible',
        base_url: base,
        note,
        enabled: true,
        native_services: ['chat_completions', 'responses'],
      });
    }
    const page = await owner.newPage();
    await page.goto(fixture().user_url + '/endpoints');
    await page.getByPlaceholder(c.connectionSearch).fill(marker);
    const searchResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === 'GET' &&
        url.pathname === '/api/connections' &&
        url.searchParams.get('q') === marker
      );
    });
    await page.getByRole('button', { name: c.search, exact: true }).click();
    const result = await searchResponse;
    expect(result.status()).toBe(200);
    await result.finished();
    const table = page.getByRole('table', { name: c.yourConnections, exact: true });
    await expect(table.getByRole('row')).toHaveCount(2);
    const row = table.getByRole('row').filter({ hasText: name });
    await expect(row.getByText(primaryURL, { exact: true })).toBeVisible();
    await expect(row.getByText(primaryNote, { exact: true })).toHaveAttribute('title', primaryNote);
    for (const width of [1440, 390]) {
      for (const theme of ['light', 'dark'] as const) {
        await screenshot(page, info, 'connection-identification', width, theme, row, {
          entry_count: 2,
          matched_secondary_entry: true,
        });
      }
    }
    await row.getByRole('link', { name, exact: true }).click();
    await expect(page.getByRole('heading', { name, level: 1, exact: true })).toBeVisible();
    await page.getByRole('button', { name: c.editConnection, exact: true }).click();
    await expect(page.getByRole('textbox', { name: c.name, exact: true })).toHaveValue(name);
    await page
      .getByRole('region', { name: c.editConnection, exact: true })
      .getByRole('button', { name: c.close, exact: true })
      .click();
    await page.getByRole('button', { name: c.addEntry, exact: true }).click();
    const form = page.getByRole('region', { name: c.addEntry, exact: true });
    await openFold(form, services.advancedSetup);
    await form
      .getByRole('combobox', { name: c.protocol, exact: true })
      .selectOption('typesafe-systemone');
    await expect(form.getByRole('checkbox', { name: native.systemone, exact: true })).toHaveCount(
      0,
    );
    await expect(form.getByRole('checkbox', { name: native.decisions, exact: true })).toHaveCount(
      0,
    );
    await screenshot(page, info, 'system-one-entry', 390, 'dark', form, {
      protocol: 'typesafe-systemone',
    });
    await form.getByRole('button', { name: c.close, exact: true }).first().click();
    await page.getByRole('link', { name: c.backToConnections, exact: true }).click();
    await expect(page.getByPlaceholder(c.connectionSearch)).toHaveValue(marker);
    await expect(page.getByRole('link', { name, exact: true })).toBeVisible();
  } finally {
    await owner.close();
  }
});

async function entry(
  context: BrowserContext,
  group: Identity,
  credential: Credential,
  base: string,
  name: string,
  services: NativeService[],
  defaultSource = false,
) {
  const endpoint = await userAPI<Entry>(context, '/api/endpoints', 'POST', {
    connection_id: group.id,
    source: 'custom',
    connector_type: 'openai-compatible',
    base_url: base,
    note: name,
    enabled: true,
    native_services: services,
    ...(services.includes('rerank') ? { rerank_format: 'generic' } : {}),
  });
  const key = await userAPI<Key>(context, '/api/endpoints/' + endpoint.id + '/keys', 'POST', {
    credential_id: credential.id,
    note: name,
    enabled: true,
    ownership_confirmed: true,
    force_store_false: false,
    native_services: services,
    ...(defaultSource ? {} : { discovery_source_id: null }),
  });
  return { endpoint, key };
}
async function credential(context: BrowserContext, group: Identity, name: string, secret: string) {
  const value = await userAPI<Credential>(
    context,
    '/api/connections/' + group.id + '/credentials',
    'POST',
    { note: name, secret, ownership_confirmed: true },
  );
  expect(JSON.stringify(value)).not.toContain(secret);
  return value;
}
async function donate(
  context: BrowserContext,
  key: Key,
  services: NativeService[],
  description: string,
  source?: Source,
) {
  return userAPI<Donation>(context, '/api/donations', 'POST', {
    description,
    ownership_authorized: true,
    discord_public_thanks: false,
    keys: [
      {
        endpoint_key_id: key.id,
        credential_id: key.credential_id,
        credential_version: key.credential_version,
        grant_revision: key.grant_revision,
        native_services: services,
        expires_at: null,
        ...(source
          ? {
              discovery_source_id: source.id,
              discovery_source_revision: source.discovery_source_revision,
            }
          : {}),
      },
    ],
  });
}
async function uses(context: BrowserContext, id: string) {
  return (
    await userAPI<Numbered<DonationUse>>(
      context,
      '/api/donations/' + id + '/uses?page=1&page_size=20',
    )
  ).data;
}
async function endDonation(context: BrowserContext, id: string): Promise<Donation> {
  const live = await userAPI<Donation>(context, '/api/donations/' + id);
  if (live.status === 'pending' || live.status === 'approved')
    return userAPI<Donation>(
      context,
      '/api/donations/' + id + (live.status === 'pending' ? '/withdraw' : '/terminate'),
      'POST',
      {
        expected_revision: live.revision,
        ...(live.status === 'approved' ? { confirmation: 'End synthetic donation' } : {}),
      },
      200,
    );
  expect(['rejected', 'deleted', 'expired']).toContain(live.status);
  return live;
}
async function approve(context: BrowserContext, owner: BrowserContext, donation: Donation) {
  const rows = await uses(owner, donation.id);
  const current = await adminAPI<Donation>(context, '/admin/api/donations/' + donation.id);
  const result = await adminAPI<Donation>(
    context,
    '/admin/api/donations/' + donation.id + '/pending-review',
    'POST',
    {
      expected_revision: current.revision,
      decision: 'approve',
      reason: 'Synthetic initial approval',
      uses: rows
        .filter((row) => row.pending)
        .map((row) => ({
          use_id: row.id,
          expected_revision: row.revision,
          expected_review_revision: row.pending!.expected_review_revision,
        })),
    },
    200,
  );
  expect(result.status).toBe('approved');
}

for (const configuredCatalog of [false, true]) {
  test(`donation catalog ${configuredCatalog ? 'configured' : 'standard'} works before and after approval without sharing consent`, async ({
    browser,
  }, info) => {
    const owner = await session(browser, 'owner');
    const admin = await session(browser, 'admin');
    const manager = configuredCatalog ? admin : await session(browser, 6);
    const tag = 'Catalog access ' + randomUUID().slice(0, 8);
    let donationId: string | undefined;
    try {
      const group = await connection(owner, tag);
      const saved = await credential(owner, group, tag, 'synthetic-image-credential');
      const value = await entry(
        owner,
        group,
        saved,
        await baseURL(owner),
        tag,
        ['chat_completions'],
        configuredCatalog,
      );
      expect(Boolean(value.key.discovery_source_id)).toBe(configuredCatalog);
      const donation = await donate(owner, value.key, ['chat_completions'], tag);
      donationId = donation.id;
      const page = await manager.newPage();
      const origin = configuredCatalog ? fixture().admin_url : fixture().user_url;
      const path = configuredCatalog
        ? '/charity?charity_section=donations&donation_id='
        : '/steward?tab=charity&charity_section=donations&donation_id=';
      const prefix = configuredCatalog ? '/admin/api' : '/api/steward';
      for (const status of ['pending', 'approved']) {
        if (status === 'approved') await approve(admin, owner, donation);
        await page.goto(origin + path + donation.id);
        const use = page.locator('.donation-discovery').filter({
          has: page.getByRole('button', {
            name: common.common.donationDiscovery.one,
            exact: true,
          }),
        });
        const fetchButton = use.getByRole('button', {
          name: common.common.donationDiscovery.one,
          exact: true,
        });
        await expect(fetchButton).toBeEnabled();
        const result = responseFor(
          page,
          'POST',
          prefix +
            '/donations/' +
            donation.id +
            '/keys/' +
            donation.keys[0]!.id +
            '/models/refresh',
        );
        await fetchButton.click();
        expect((await result).status()).toBe(202);
        await expect(use).toContainText('Fetching complete');
        if (status === 'pending')
          for (const width of [1440, 390])
            for (const theme of ['light', 'dark'] as const) {
              await screenshot(
                page,
                info,
                'donation-catalog-' + (configuredCatalog ? 'configured' : 'standard'),
                width,
                theme,
                use,
                { status, configuredCatalog },
              );
            }
      }
    } finally {
      try {
        if (donationId) await endDonation(owner, donationId);
      } finally {
        await Promise.all([...new Set([owner, admin, manager])].map((context) => context.close()));
      }
    }
  });
}

test('service keys reject cross-service reuse while shared versions preserve charity approval', async ({
  browser,
}, info) => {
  const owner = await session(browser, 'owner');
  const admin = await session(browser, 'admin');
  const tag = 'Shared ' + randomUUID().slice(0, 8);
  const secrets = [
    'synthetic-shared-' + randomUUID(),
    'synthetic-rotation-' + randomUUID(),
    'synthetic-isolated-' + randomUUID(),
  ];
  try {
    const base = await baseURL(owner);
    const first = await connection(owner, tag + ' original');
    const second = await connection(owner, tag + ' retained');
    const saved = await credential(owner, first, tag, secrets[0]!);
    const a = await entry(owner, first, saved, base, tag + ' A', ['chat_completions']);
    const b = await entry(owner, first, saved, base, tag + ' B', ['chat_completions']);
    const isolated = await credential(owner, second, tag + ' isolated key', secrets[2]!);
    const other = await entry(owner, second, isolated, base, tag + ' isolated endpoint', [
      'chat_completions',
    ]);
    await userAPI(
      owner,
      '/api/endpoints/' + other.endpoint.id + '/keys',
      'POST',
      {
        credential_id: saved.id,
        note: 'Cross-service reference',
        enabled: true,
        ownership_confirmed: true,
        force_store_false: false,
        native_services: ['chat_completions'],
      },
      404,
    );
    const page = await owner.newPage();
    await page.goto(fixture().user_url + '/endpoints?connection_id=' + second.id);
    await page.getByRole('button', { name: c.addEntry, exact: true }).click();
    const newEndpoint = page.getByRole('region', { name: c.addEntry, exact: true });
    const availableKeys = newEndpoint.getByRole('table', { name: c.chooseCredential, exact: true });
    await expect(availableKeys.getByText(tag + ' isolated key', { exact: true })).toBeVisible();
    await expect(availableKeys.getByText(tag, { exact: true })).toHaveCount(0);
    await newEndpoint.getByRole('button', { name: c.close, exact: true }).click();
    const source = await userAPI<Source>(
      owner,
      '/api/connections/' + first.id + '/discovery-sources',
      'POST',
      {
        name: tag + ' separate directory',
        base_url: base + '/unavailable',
        list_format: 'openai',
        credential_id: saved.id,
        enabled: true,
      },
    );
    a.key = await userAPI<Key>(
      owner,
      '/api/endpoints/' + a.endpoint.id + '/keys/' + a.key.id,
      'PATCH',
      {
        expected_revision: a.key.revision,
        discovery_source_id: source.id,
      },
    );
    await userAPI(
      owner,
      '/api/endpoints/' + b.endpoint.id + '/keys/' + b.key.id,
      'PATCH',
      {
        expected_revision: b.key.revision,
        discovery_source_id: source.id,
      },
      409,
    );
    await userAPI(
      owner,
      '/api/endpoints/' + other.endpoint.id + '/keys/' + other.key.id,
      'PATCH',
      {
        expected_revision: other.key.revision,
        discovery_source_id: source.id,
      },
      404,
    );
    const donation = await donate(owner, a.key, ['chat_completions'], tag);
    await approve(admin, owner, donation);
    const groupURL = '/endpoints?connection_id=' + first.id;
    await page.goto(
      fixture().user_url + groupURL + '&endpoint_id=' + a.endpoint.id + '&key_id=' + a.key.id,
    );
    const refreshed = responseFor(
      page,
      'POST',
      '/api/endpoints/' + a.endpoint.id + '/keys/' + a.key.id + '/models/refresh',
    );
    await page.getByRole('button', { name: services.fetchModels, exact: true }).click();
    expect((await refreshed).status()).toBe(202);
    await expect
      .poll(async () => (await userAPI<Source>(owner, '/api/discovery-sources/' + source.id)).state)
      .toBe('failed');
    await page.reload();
    await expect(page.getByText(services.retainedCatalog, { exact: true })).toBeVisible();
    const serviceKeys = await openFold(page, services.manageServiceKeys);
    await serviceKeys
      .getByRole('row')
      .filter({ hasText: tag })
      .getByRole('button', { name: c.manage, exact: true })
      .click();
    await page.getByRole('button', { name: c.rotate, exact: true }).click();
    const impact = await userAPI<{ summary: unknown }>(
      owner,
      '/api/credentials/' + saved.id + '/impact?kind=personal&page=1&page_size=20',
    );
    expect(impact.summary).toEqual({
      endpoint_keys: '2',
      donation_keys: '1',
      discovery_sources: '1',
    });
    await page.getByLabel(c.newSecret, { exact: true }).fill(secrets[1]!);
    await page.getByLabel(c.ownership, { exact: true }).check();
    const rotated = responseFor(page, 'POST', '/api/credentials/' + saved.id + '/versions');
    await page
      .locator('form')
      .filter({ has: page.getByLabel(c.newSecret, { exact: true }) })
      .getByRole('button', { name: c.rotate, exact: true })
      .click();
    expect((await rotated).status()).toBe(201);
    await expect
      .poll(
        async () =>
          (
            await userAPI<Numbered<Key>>(
              owner,
              '/api/endpoints/' + b.endpoint.id + '/keys?page=1&page_size=20',
            )
          ).data.find((row) => row.id === b.key.id)!.credential_version,
      )
      .toBe('2');
    expect(
      (await userAPI<Credential>(owner, '/api/credentials/' + isolated.id)).credential_version,
    ).toBe('1');
    expect(
      (
        await userAPI<Numbered<Key>>(
          owner,
          '/api/endpoints/' + other.endpoint.id + '/keys?page=1&page_size=20',
        )
      ).data[0]!.credential_version,
    ).toBe('1');
    const versions = await uses(owner, donation.id);
    expect(versions[0]!.approved?.credential_version).toBe('1');
    expect(versions[0]!.pending?.credential_version).toBe('2');
    expect(
      (await userAPI<Source>(owner, '/api/discovery-sources/' + source.id)).credential_version,
    ).toBe('2');
    expect(
      (
        await userAPI<{ summary: unknown }>(
          owner,
          '/api/credentials/' + saved.id + '/impact?kind=personal&page=1&page_size=20',
        )
      ).summary,
    ).toEqual(impact.summary);
    await openFold(serviceKeys, c.versionHistory);
    const oldRow = serviceKeys.getByRole('row').filter({
      has: page.getByText(c.versionNumber.replace('{{version}}', '1'), { exact: true }),
    });
    await oldRow.getByRole('button', { name: c.disable, exact: true }).click();
    const disabled = responseFor(page, 'PATCH', '/api/credentials/' + saved.id + '/versions/1');
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: c.disable, exact: true })
      .click();
    expect((await disabled).ok()).toBe(true);
    await expect
      .poll(
        async () =>
          (
            await userAPI<Numbered<{ credential_version: string; enabled: boolean }>>(
              owner,
              '/api/credentials/' + saved.id + '/versions?page=1&page_size=20',
            )
          ).data.find((row) => row.credential_version === '1')!.enabled,
      )
      .toBe(false);
    expect((await uses(owner, donation.id))[0]!.approved?.credential_version).toBe('1');
    const enabled = responseFor(page, 'PATCH', '/api/credentials/' + saved.id + '/versions/1');
    await oldRow.getByRole('button', { name: c.enable, exact: true }).click();
    expect((await enabled).ok()).toBe(true);
    await expect(oldRow.getByRole('button', { name: c.disable, exact: true })).toBeEnabled();
    await noSecrets(page, secrets);
    await screenshot(page, info, 'shared-versions', 1440, 'light', oldRow, {
      versions: await userAPI(
        owner,
        '/api/credentials/' + saved.id + '/versions?page=1&page_size=20',
      ),
      uses: await uses(owner, donation.id),
      impact: impact.summary,
    });
    await page.getByRole('button', { name: c.backToCredentials, exact: true }).click();
    await expect(page.getByPlaceholder(c.credentialSearch)).toBeVisible();
    await page.getByRole('button', { name: c.editConnection, exact: true }).click();
    await page.getByRole('button', { name: c.deleteConnection, exact: true }).click();
    const deleted = responseFor(page, 'DELETE', '/api/connections/' + first.id);
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: c.delete, exact: true })
      .click();
    expect((await deleted).status()).toBe(204);
    expect(
      (await owner.request.get(fixture().user_url + '/api/endpoints/' + a.endpoint.id)).status(),
    ).toBe(404);
    const retained = await userAPI<Credential>(owner, '/api/credentials/' + isolated.id);
    expect(retained.connection_id).toBe(second.id);
    expect(retained.credential_version).toBe('1');
    const membership = await userAPI<Numbered<Credential>>(
      owner,
      '/api/connections/' + second.id + '/credentials?page=1&page_size=20',
    );
    expect(membership.data.map((row) => row.id)).toContain(isolated.id);
    expect(membership.data.map((row) => row.id)).not.toContain(saved.id);
    const preservedKey = (
      await userAPI<Numbered<Key>>(
        owner,
        '/api/endpoints/' + other.endpoint.id + '/keys?page=1&page_size=20',
      )
    ).data[0]!;
    expect(preservedKey.credential_id).toBe(isolated.id);
    expect(preservedKey.credential_version).toBe('1');
    await page.goto(
      fixture().user_url + '/endpoints?connection_id=' + second.id + '&connection_view=credentials',
    );
    await page
      .getByRole('row')
      .filter({ hasText: tag + ' isolated key' })
      .getByRole('button', { name: c.manage, exact: true })
      .click();
    await expect(
      page.getByRole('heading', { name: tag + ' isolated key', exact: true }),
    ).toBeVisible();
    await page.getByRole('button', { name: c.backToCredentials, exact: true }).click();
    await page.getByRole('link', { name: c.backToConnections, exact: true }).click();
    await expect(page.getByPlaceholder(c.connectionSearch)).toBeVisible();
    await noSecrets(page, secrets);
  } finally {
    await owner.close();
    await admin.close();
  }
});

test('additional services stay pending with their old budget and redacted steward management', async ({
  browser,
}, info) => {
  const owner = await session(browser, 'owner');
  const admin = await session(browser, 'admin');
  const trainee = await session(browser, 5);
  const tag = 'Uses ' + randomUUID().slice(0, 8);
  try {
    const group = await connection(owner, tag);
    const saved = await credential(owner, group, tag, 'synthetic-uses-' + randomUUID());
    const value = await entry(
      owner,
      group,
      saved,
      await baseURL(owner),
      tag,
      ['chat_completions', 'responses', 'embeddings'],
      true,
    );
    const donation = await donate(owner, value.key, ['chat_completions'], tag);
    const manager = await admin.newPage();
    await manager.goto(
      fixture().admin_url + '/charity?charity_section=donations&donation_id=' + donation.id,
    );
    expect(
      (await adminAPI<Donation>(admin, '/admin/api/donations/' + donation.id)).keys[0]!.can_manage,
    ).toBe(false);
    await expect(
      manager.getByRole('button', { name: c.modelsAndBindings, exact: true }),
    ).toHaveCount(0);
    await approve(admin, owner, donation);
    await qualifyForSteward(admin, donation, donation.keys[0]!.id, randomUUID().slice(0, 8));
    let current = await adminAPI<Donation>(admin, '/admin/api/donations/' + donation.id);
    await adminAPI(
      admin,
      '/admin/api/donations/' + donation.id + '/keys/' + current.keys[0]!.id,
      'PATCH',
      { expected_revision: current.revision, enabled: false, calls_limit: '7' },
    );
    current = await adminAPI<Donation>(admin, '/admin/api/donations/' + donation.id);
    const member = current.keys[0]!;
    const budget = (row: DonationKey) => ({
      id: row.id,
      state: row.charity_state,
      limits: row.limits,
      usage: row.usage,
      reserve: row.token_reserve,
    });
    const before = budget(member);
    expect(value.key.discovery_source_id).not.toBeNull();
    const sharedSource = await userAPI<Source>(
      owner,
      '/api/discovery-sources/' + value.key.discovery_source_id,
    );
    const page = await owner.newPage();
    await page.goto(fixture().user_url + '/charity/donations/' + donation.id);
    const chat = page.locator('.economy-donation-key').first();
    const paused = responseFor(
      page,
      'PATCH',
      '/api/donations/' + donation.id + '/uses/' + (await uses(owner, donation.id))[0]!.id,
    );
    await chat.getByRole('button', { name: c.pauseSharedKey, exact: true }).click();
    expect((await paused).ok()).toBe(true);
    await page.reload();
    const addition = await openFold(page, c.addServices);
    await expect(addition.getByRole('checkbox')).toHaveCount(0);
    const form = addition;
    const submitted = responseFor(page, 'POST', '/api/donations/' + donation.id + '/uses');
    await form.getByRole('button', { name: c.submitServices, exact: true }).click();
    const response = await submitted;
    expect(response.status()).toBe(201);
    const input = response.request().postDataJSON() as {
      items: { native_service: string; expected_revision: null }[];
      discovery_source_id: string;
      discovery_source_revision: string;
      credential_version: string;
    };
    const fullServices = await protocolServices(owner);
    expect(input.items.map((row) => row.native_service)).toEqual(fullServices);
    expect(
      input.items.find((row) => row.native_service === 'chat_completions')!.expected_revision,
    ).not.toBeNull();
    expect(
      input.items
        .filter((row) => row.native_service !== 'chat_completions')
        .every((row) => row.expected_revision === null),
    ).toBe(true);
    expect(input.discovery_source_id).toBeNull();
    expect(input.discovery_source_revision).toBeNull();
    const addedUses = await uses(owner, donation.id);
    for (const use of addedUses.filter((row) => row.native_service !== 'chat_completions')) {
      expect(use.pending?.discovery_source_id).toBe(sharedSource.id);
      expect(use.pending?.discovery_source_revision).toBe(sharedSource.discovery_source_revision);
    }
    expect(input.credential_version).toBe('1');
    expect(
      budget((await adminAPI<Donation>(admin, '/admin/api/donations/' + donation.id)).keys[0]!),
    ).toEqual(before);
    expect(
      (await uses(owner, donation.id)).find((row) => row.native_service === 'chat_completions')!
        .enabled,
    ).toBe(false);
    const reviewer = await trainee.newPage();
    await reviewer.goto(
      fixture().user_url +
        '/steward?tab=charity&charity_section=donations&donation_id=' +
        donation.id,
    );
    await expect(
      reviewer.getByRole('tab', { name: userEn.user.steward.donationsTitle, exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    const projected = await userAPI<Numbered<DonationUse>>(
      trainee,
      '/api/steward/donations/' + donation.id + '/uses?page=1&page_size=20',
    );
    expect(projected.data).toHaveLength(fullServices.length);
    for (const row of projected.data) {
      expect(row.endpoint_key_id).toBeNull();
      expect(row.capabilities.can_review).toBe(true);
      expect(row.capabilities.can_manage).toBe(true);
    }
    await expect(reviewer.getByRole('button', { name: c.pauseSharedKey, exact: true })).toHaveCount(
      0,
    );
    await expect(
      reviewer.getByRole('button', { name: c.modelsAndBindings, exact: true }),
    ).toBeEnabled();
    await openFold(reviewer, c.donationMaterials);
    await reviewer.getByLabel(c.reviewReason, { exact: true }).fill('Synthetic pending approval');
    const accepted = responseFor(
      reviewer,
      'POST',
      '/api/steward/donations/' + donation.id + '/pending-review',
    );
    await reviewer.getByRole('button', { name: c.approveAllUses, exact: true }).click();
    const acceptedResponse = await accepted;
    expect(acceptedResponse.status()).toBe(200);
    expect(acceptedResponse.request().postDataJSON()).toMatchObject({
      decision: 'approve',
      reason: 'Synthetic pending approval',
      uses: projected.data
        .filter((row) => row.pending)
        .map((row) => ({
          use_id: row.id,
          expected_revision: row.revision,
          expected_review_revision: row.pending!.expected_review_revision,
        })),
    });
    const currentCredential = await userAPI<Credential>(owner, '/api/credentials/' + saved.id);
    await userAPI(owner, '/api/credentials/' + saved.id + '/versions', 'POST', {
      expected_revision: currentCredential.revision,
      secret: 'synthetic-replacement-' + randomUUID(),
      ownership_confirmed: true,
    });
    const replacements = (await uses(owner, donation.id)).filter((row) => row.pending);
    expect(replacements).toHaveLength(fullServices.length);
    await reviewer.reload();
    await openFold(reviewer, c.donationMaterials);
    await reviewer
      .getByLabel(c.reviewReason, { exact: true })
      .fill('Synthetic pending replacement return');
    await screenshot(
      reviewer,
      info,
      'use-only-review',
      390,
      'dark',
      reviewer.getByLabel(c.reviewReason, { exact: true }),
      {
        projection: projected.data.map((row) => ({
          id: row.id,
          endpoint_key_id: row.endpoint_key_id,
          capabilities: row.capabilities,
        })),
      },
    );
    const rejected = responseFor(
      reviewer,
      'POST',
      '/api/steward/donations/' + donation.id + '/pending-review',
    );
    await reviewer.getByRole('button', { name: c.rejectAllUses, exact: true }).click();
    const rejectedResponse = await rejected;
    expect(rejectedResponse.status()).toBe(200);
    expect(rejectedResponse.request().postDataJSON()).toMatchObject({
      decision: 'reject',
      reason: 'Synthetic pending replacement return',
      uses: replacements.map((row) => ({
        use_id: row.id,
        expected_revision: row.revision,
        expected_review_revision: row.pending!.expected_review_revision,
      })),
    });
    const decided = await uses(owner, donation.id);
    expect(decided.find((row) => row.native_service === 'embeddings')!.approved?.status).toBe(
      'approved',
    );
    expect(decided.find((row) => row.native_service === 'responses')!.latest?.status).toBe(
      'rejected',
    );
    expect(decided.every((row) => row.approved?.credential_version === '1' && !row.pending)).toBe(
      true,
    );
    expect(
      decided.find((row) => row.native_service === 'chat_completions')!.approved
        ?.credential_version,
    ).toBe('1');
    expect(
      budget((await adminAPI<Donation>(admin, '/admin/api/donations/' + donation.id)).keys[0]!),
    ).toEqual(before);
    await manager.goto(
      fixture().admin_url + '/charity?charity_section=donations&donation_id=' + donation.id,
    );
    await expect(
      manager.getByRole('button', { name: c.modelsAndBindings, exact: true }),
    ).toBeEnabled();
    await manager.getByRole('button', { name: c.modelsAndBindings, exact: true }).click();
    expect(member.endpoint_key_id).toBe(value.key.id);
    await expect(manager).toHaveURL(
      (url) =>
        url.pathname === '/charity' &&
        url.searchParams.get('donation_id') === donation.id &&
        url.searchParams.get('binding_key') === member.id,
    );
    await manager
      .getByRole('button', { name: common.common.workflow.returnDonation, exact: true })
      .click();
    await expect(
      manager.getByRole('button', { name: c.modelsAndBindings, exact: true }),
    ).toBeVisible();
    const live = await userAPI<Donation>(owner, '/api/donations/' + donation.id);
    expect(live.status).toBe('approved');
    const ended = await userAPI<
      Donation & { use_summary: { total: string; pending: string; approved: string } }
    >(
      owner,
      '/api/donations/' + donation.id + '/terminate',
      'POST',
      { expected_revision: live.revision, confirmation: 'End synthetic donation' },
      200,
    );
    expect(ended.id).toBe(donation.id);
    expect(ended.status).toBe('deleted');
    expect(ended.use_summary).toEqual({
      total: String(fullServices.length),
      pending: '0',
      approved: '0',
      rejected: String(fullServices.length),
      withdrawn: '0',
      review_status: 'rejected',
    });
    expect(ended.keys).toHaveLength(1);
    const endedMember = ended.keys.find((row) => row.id === member.id)!;
    expect(endedMember.ended_reason).toBe('terminated');
    expect(budget(endedMember)).toEqual({ ...before, state: 'ended' });
    await manager.reload();
    await expect(
      manager.getByRole('button', { name: c.modelsAndBindings, exact: true }),
    ).toHaveCount(0);
    const closed = await adminAPI<Donation>(admin, '/admin/api/donations/' + donation.id);
    expect(closed.keys).toHaveLength(1);
    expect(closed.keys[0]!.id).toBe(member.id);
    expect(closed.keys[0]!.can_manage).toBe(false);
    const terminal = await uses(owner, donation.id);
    expect(terminal).toHaveLength(fullServices.length);
    expect(terminal.map((row) => row.id).sort()).toEqual(decided.map((row) => row.id).sort());
    for (const row of terminal) {
      expect(row.donation_key_id).toBe(member.id);
      expect(row.native_service).toBe(
        decided.find((previous) => previous.id === row.id)!.native_service,
      );
      expect(row.approved).toBeNull();
      expect(row.pending).toBeNull();
      expect(row.enabled).toBe(false);
      expect(row.discovery_enabled).toBe(false);
      expect(row.capabilities).toEqual({
        can_review: false,
        can_manage: false,
        can_reset_failures: false,
        can_discover: false,
        can_bind: false,
        unavailable_reason: 'ended',
      });
    }
    const priorDecision = decided.find((row) => row.native_service === 'responses')!.latest!;
    expect(terminal.find((row) => row.native_service === 'responses')!.latest).toMatchObject({
      status: 'rejected',
      review_note: priorDecision.review_note,
      reviewed_at: priorDecision.reviewed_at,
      credential_id: priorDecision.credential_id,
      credential_version: priorDecision.credential_version,
      grant_revision: priorDecision.grant_revision,
      discovery_source_id: priorDecision.discovery_source_id,
      discovery_source_revision: priorDecision.discovery_source_revision,
    });
    await page.reload();
    await expect(page.locator('.economy-donation-card')).toBeVisible();
    await expect(page.getByText(c.addServices, { exact: true })).toHaveCount(0);
  } finally {
    await owner.close();
    await admin.close();
    await trainee.close();
  }
});

const targetRows = (editor: Locator) =>
  editor
    .locator('fieldset')
    .filter({ has: editor.page().getByRole('button', { name: c.removeTarget, exact: true }) });
async function saveTargets(page: Page, editor: Locator, path: string, status = 200) {
  const saveLabel = path.startsWith('/api/models/') ? bindingCopy.saveCombination : c.saveTargets;
  const response = responseFor(page, 'PUT', path);
  await editor.getByRole('button', { name: saveLabel, exact: true }).click();
  const saved = await response;
  expect(saved.status()).toBe(status);
  if (status === 200) {
    const receipt = (await saved.json()) as {
      binding_id: string;
      binding_revision: string;
      targets: { id: string; revision: string }[];
    };
    expect(receipt.binding_id).toBe(path.split('/').at(-2));
    expect(receipt.binding_revision).toMatch(/^[1-9][0-9]*$/);
    expect(receipt.targets.length).toBeGreaterThan(0);
    await expect(editor.getByRole('button', { name: saveLabel, exact: true })).toBeEnabled();
  }
}
async function openTargetEditor(page: Page, title: string, charity: boolean) {
  if (!charity) return openFold(await openFold(page, bindingCopy.advanced), title);
  await page
    .getByRole('row')
    .filter({ has: page.getByRole('button', { name: c.editBindingSources, exact: true }) })
    .first()
    .getByRole('button', { name: c.editBindingSources, exact: true })
    .click();
  return page.getByRole('region', { name: c.editBindingSources, exact: true });
}
async function addGroup(
  page: Page,
  name: string,
  charity: boolean,
  path: string,
  source?: { endpoint: Entry; key: Key },
) {
  let editor: Locator;
  if (charity) {
    const mode = page.getByRole('combobox', { name: c.addBindingMode, exact: true });
    await mode.selectOption('group');
    editor = mode
      .locator('xpath=ancestor::section[1]')
      .locator(':scope > div')
      .filter({ has: page.getByRole('table', { name: c.availableTargets, exact: true }) });
  } else
    editor = await openFold(await openFold(page, bindingCopy.advanced), bindingCopy.addCombination);
  if (charity) {
    await editor.getByLabel(c.modelSearch, { exact: true }).fill(name);
    const row = editor.getByRole('row').filter({ hasText: name });
    await row.getByRole('button', { name: c.addTarget, exact: true }).click();
  } else {
    expect(source).toBeDefined();
    const candidates = await openFold(editor, bindingCopy.addSources);
    const picker = candidates.locator('.personal-binding-picker');
    await expect(
      picker.getByRole('combobox', { name: bindingCopy.filterService, exact: true }),
    ).toBeVisible();
    if (
      !(await picker
        .getByRole('searchbox', { name: bindingCopy.endpoints, exact: true })
        .isVisible())
    )
      await picker.getByRole('button', { name: bindingCopy.changeEndpoint, exact: true }).click();
    await picker
      .getByRole('searchbox', { name: bindingCopy.endpoints, exact: true })
      .fill(source!.endpoint.note);
    await picker
      .locator('.personal-binding-picker__browse')
      .getByRole('button')
      .filter({ hasText: source!.endpoint.note })
      .click();
    await picker.getByRole('searchbox', { name: bindingCopy.searchModels, exact: true }).fill(name);
    await picker
      .locator('.personal-binding-picker__model')
      .filter({ has: page.getByText(name, { exact: true }) })
      .getByRole('checkbox')
      .check();
  }
  const selected = targetRows(editor);
  await expect(selected).toHaveCount(1);
  const chat = selected.getByLabel(native.chat_completions, { exact: true });
  await expect(chat).toBeChecked();
  const response = responseFor(page, 'POST', path + '/batch-receipt');
  await editor
    .getByRole('button', {
      name: charity ? c.addTargetGroup : bindingCopy.addCombination,
      exact: true,
    })
    .click();
  const saved = await response;
  expect(saved.status()).toBe(201);
  expect(saved.request().postDataJSON().selections[0].targets[0].native_services).toEqual([
    'chat_completions',
  ]);
  await expect(targetRows(editor)).toHaveCount(0);
}

async function exerciseTargets(
  page: Page,
  context: BrowserContext,
  origin: string,
  path: string,
  destination: string,
  names: string[],
  charity: boolean,
  info: TestInfo,
  personalSources?: { endpoint: Entry; key: Key }[],
) {
  await page.goto(origin + destination);
  const title = charity ? c.editBindingSources : bindingCopy.combination + ' 1';
  let editor = await openTargetEditor(page, title, charity);
  await expect(targetRows(editor)).toHaveCount(2);
  const before = await api<Bindings>(context, origin, path);
  const binding = before.bindings[0]!;
  const targetPath = path + '/' + binding.id + '/targets';
  await targetRows(editor).first().getByRole('button', { name: c.moveDown, exact: true }).click();
  await saveTargets(page, editor, targetPath);
  await expect
    .poll(async () =>
      (await api<Bindings>(context, origin, path)).bindings[0]!.targets.map(
        (row) => row.upstream_model_id,
      ),
    )
    .toEqual([names[1], names[0]]);
  await targetRows(editor).first().getByRole('button', { name: c.moveDown, exact: true }).click();
  await saveTargets(page, editor, targetPath);
  await expect
    .poll(async () =>
      (await api<Bindings>(context, origin, path)).bindings[0]!.targets.map(
        (row) => row.upstream_model_id,
      ),
    )
    .toEqual([names[0], names[1]]);
  const current = await api<Bindings>(context, origin, path);
  const external = current.bindings[0]!.targets.slice()
    .reverse()
    .map((row) => ({
      [charity ? 'donation_key_id' : 'endpoint_key_id']: charity
        ? row.donation_key_id
        : row.endpoint_key_id,
      upstream_model_id: row.upstream_model_id,
      native_services: row.native_services,
    }));
  await api(context, origin, targetPath, 'PUT', {
    expected_binding_revision: current.binding_revision,
    targets: external,
  });
  await targetRows(editor)
    .first()
    .getByRole('button', { name: c.removeTarget, exact: true })
    .click();
  await saveTargets(page, editor, targetPath, 409);
  await expect(editor.getByText(c.conflictDraft, { exact: true })).toBeVisible();
  await expect(targetRows(editor)).toHaveCount(1);
  await expect(targetRows(editor).first()).toContainText(names[1]!);
  const persisted = await api<Bindings>(context, origin, path);
  expect(persisted.bindings[0]!.targets).toHaveLength(2);
  const draft = targetRows(editor).first();
  if (!charity)
    await screenshot(
      page,
      info,
      'target-conflict-draft',
      1440,
      'dark',
      editor.getByRole('button', { name: c.useCurrentRevision, exact: true }),
      {
        binding_revision: persisted.binding_revision,
        persisted: persisted.bindings[0]!.targets.map((row) => row.upstream_model_id),
        draft: [names[1]],
      },
    );
  await editor.getByRole('button', { name: c.useCurrentRevision, exact: true }).click();
  await expect(draft).toContainText(names[1]!);
  await saveTargets(page, editor, targetPath);
  await expect
    .poll(async () =>
      (await api<Bindings>(context, origin, path)).bindings[0]!.targets.map(
        (row) => row.upstream_model_id,
      ),
    )
    .toEqual([names[1]]);
  if (charity) await editor.getByRole('button', { name: c.returnToBindings, exact: true }).click();
  await addGroup(page, names[2]!, charity, path, personalSources?.[0]);
  await addGroup(page, names[3]!, charity, path, personalSources?.[1]);
  await expect
    .poll(async () => (await api<Bindings>(context, origin, path)).bindings.length)
    .toBe(3);
  await page.reload();
  editor = await openTargetEditor(page, title, charity);
  await expect(targetRows(editor)).toHaveCount(1);
  await expect(targetRows(editor).first()).toContainText(names[1]!);
  if (charity) await editor.getByRole('button', { name: c.returnToBindings, exact: true }).click();
  else await editor.locator(':scope > summary').click();
  editor = await openTargetEditor(page, title, charity);
  await expect(targetRows(editor).first()).toContainText(names[1]!);
  const final = await api<Bindings>(context, origin, path);
  expect(
    final.bindings.map((row) => row.targets.map((target) => target.upstream_model_id)),
  ).toEqual([[names[1]], [names[2]], [names[3]]]);
}

test('self and charity target groups save repeatedly and preserve drafts across a real conflict', async ({
  browser,
}, info) => {
  const owner = await session(browser, 'owner');
  const admin = await session(browser, 'admin');
  const tag = randomUUID().slice(0, 8);
  const names = ['a-', 'b-', 'c-', 'd-'].map((prefix) => prefix + tag);
  try {
    const base = await baseURL(owner);
    const group = await connection(owner, 'Targets ' + tag);
    const saved = await credential(
      owner,
      group,
      'Targets ' + tag,
      'synthetic-targets-' + randomUUID(),
    );
    const a = await entry(owner, group, saved, base, 'Targets A ' + tag, ['chat_completions']);
    const secondCredential = await credential(
      owner,
      group,
      'Targets B ' + tag,
      'synthetic-targets-b-' + randomUUID(),
    );
    const b = await entry(owner, group, secondCredential, base, 'Targets B ' + tag, [
      'chat_completions',
    ]);
    for (const [value, models] of [
      [a, [names[0], names[2]]],
      [b, [names[1], names[3]]],
    ] as const) {
      await userAPI(
        owner,
        '/api/endpoints/' + value.endpoint.id + '/keys/' + value.key.id + '/models/manual',
        'POST',
        {
          entries: models.map((upstream_model_id) => ({
            upstream_model_id,
            provider: 'synthetic',
          })),
        },
      );
    }
    const model = await userAPI<Identity>(owner, '/api/models', 'POST', {
      provider: 'synthetic',
      model: 'targets-' + tag,
      model_types: ['chat_completions'],
      route_strategy: 'ordered',
      silent_retry: false,
      flatten_tool_calls: false,
    });
    const selfPath = '/api/models/' + model.id + '/bindings';
    const initial = await userAPI<Bindings>(owner, selfPath);
    await userAPI(owner, selfPath + '/batch-receipt', 'POST', {
      expected_binding_revision: initial.binding_revision,
      selections: [
        {
          targets: [
            {
              endpoint_key_id: a.key.id,
              upstream_model_id: names[0],
              native_services: ['chat_completions'],
            },
            {
              endpoint_key_id: b.key.id,
              upstream_model_id: names[1],
              native_services: ['chat_completions'],
            },
          ],
        },
      ],
    });
    await exerciseTargets(
      await owner.newPage(),
      owner,
      fixture().user_url,
      selfPath,
      '/models?model_id=' + model.id,
      names,
      false,
      info,
      [a, b],
    );
    const donation = await userAPI<Donation>(owner, '/api/donations', 'POST', {
      description: 'Targets ' + tag,
      ownership_authorized: true,
      discord_public_thanks: false,
      keys: [a.key, b.key].map((key) => ({
        endpoint_key_id: key.id,
        credential_id: key.credential_id,
        credential_version: key.credential_version,
        grant_revision: key.grant_revision,
        native_services: ['chat_completions'],
        expires_at: null,
      })),
    });
    await approve(admin, owner, donation);
    const members = (await userAPI<Donation>(owner, '/api/donations/' + donation.id)).keys;
    expect(members.map((row) => row.endpoint_key_id)).toEqual([a.key.id, b.key.id]);
    for (const [member, models] of [
      [members[0]!, [names[0], names[2]]],
      [members[1]!, [names[1], names[3]]],
    ] as const) {
      const manualPath = '/admin/api/donations/' + donation.id + '/keys/' + member.id + '/models';
      const catalog = await adminAPI<{ manual_catalog_revision: string }>(
        admin,
        manualPath + '?page=1&page_size=20',
      );
      await adminAPI(
        admin,
        manualPath + '/manual',
        'POST',
        { expected_manual_catalog_revision: catalog.manual_catalog_revision, entries: models },
        200,
      );
    }
    const charity = await adminAPI<Identity>(admin, '/admin/api/charity-models', 'POST', {
      provider: 'synthetic',
      model: 'targets-' + tag,
      enabled: true,
      is_mainstream: false,
      flatten_tool_calls: false,
      model_types: ['chat_completions'],
      pricing: { mode: 'per_request', user_price: '0', donor_reward: '0' },
      discount: { enabled: false, percent: 100, start_at: null, end_at: null },
    });
    const charityPath = '/admin/api/charity-models/' + charity.id + '/bindings';
    const charityInitial = await adminAPI<Bindings>(admin, charityPath);
    await adminAPI(admin, charityPath + '/batch-receipt', 'POST', {
      expected_binding_revision: charityInitial.binding_revision,
      selections: [
        {
          targets: [
            {
              donation_key_id: members[0]!.id,
              upstream_model_id: names[0],
              native_services: ['chat_completions'],
            },
            {
              donation_key_id: members[1]!.id,
              upstream_model_id: names[1],
              native_services: ['chat_completions'],
            },
          ],
        },
      ],
    });
    await exerciseTargets(
      await admin.newPage(),
      admin,
      fixture().admin_url,
      charityPath,
      '/charity?charity_section=models&charity_model=' + charity.id,
      names,
      true,
      info,
    );
    const ended = await endDonation(owner, donation.id);
    expect(ended.status).toBe('deleted');
  } finally {
    await owner.close();
    await admin.close();
  }
});

test('connection lists return to the requested page and mainstream mappings use saved entry IDs', async ({
  browser,
}, info) => {
  const owner = await session(browser, 'owner');
  const admin = await session(browser, 'admin');
  const tag = 'Navigation ' + randomUUID().slice(0, 8);
  try {
    for (let index = 0; index < 11; index++)
      await connection(owner, tag + ' ' + String(index).padStart(2, '0'));
    const page = await owner.newPage();
    await page.goto(fixture().user_url + '/endpoints');
    const search = page.getByPlaceholder(c.connectionSearch);
    const table = page.getByRole('table', { name: c.yourConnections, exact: true });
    const pagination = page.getByRole('navigation', {
      name: common.common.pagination,
      exact: true,
    });
    const expectList = async (number: string, size: string, rows: number) => {
      await expect(page).toHaveURL(
        (url) =>
          url.pathname === '/endpoints' &&
          !url.searchParams.has('connection_id') &&
          url.searchParams.get('connections_q') === tag &&
          (url.searchParams.get('connections_page') ?? '1') === number &&
          url.searchParams.get('connections_page_size') === size,
      );
      await expect(search).toHaveValue(tag);
      await expect(table.getByRole('row')).toHaveCount(rows);
      const sizeControl = page.getByLabel(common.common.pageControls.size, { exact: true });
      await expect(sizeControl).toHaveValue(size);
      await expect(sizeControl).toBeEnabled();
      if (size === '10') {
        const current = pagination.getByRole('button', { name: number, exact: true });
        await expect(current).toHaveAttribute('aria-current', 'page');
        await expect(current).toBeEnabled();
      }
    };
    await search.fill(tag);
    const filteredResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === 'GET' &&
        url.pathname === '/api/connections' &&
        url.searchParams.get('q') === tag
      );
    });
    await page.getByRole('button', { name: c.search, exact: true }).click();
    expect((await filteredResponse).status()).toBe(200);
    await expect(page).toHaveURL((url) => url.searchParams.get('connections_q') === tag);
    await expect(table.getByRole('row')).toHaveCount(12);
    await page.getByLabel(common.common.pageControls.size, { exact: true }).selectOption('10');
    await expectList('1', '10', 11);
    await pagination.getByRole('button', { name: common.common.next, exact: true }).click();
    await expectList('2', '10', 2);
    await pagination.getByRole('button', { name: common.common.previous, exact: true }).click();
    await expectList('1', '10', 11);
    await pagination.getByRole('button', { name: '2', exact: true }).click();
    await expectList('2', '10', 2);
    const selected = table.getByRole('link').filter({ hasText: tag }).first();
    const selectedName = await selected.innerText();
    const selectedHref = (await selected.getAttribute('href'))!;
    const selectedURL = new URL(selectedHref, fixture().user_url);
    const selectedID = selectedURL.searchParams.get('connection_id')!;
    expect(selectedID).toMatch(/^[1-9][0-9]*$/);
    expect(selectedURL.searchParams.get('connections_q')).toBe(tag);
    expect(selectedURL.searchParams.get('connections_page')).toBe('2');
    const detailResponse = responseFor(page, 'GET', '/api/connections/' + selectedID);
    const exactLink = table.getByRole('link', { name: selectedName, exact: true });
    await expect(exactLink).toHaveAttribute('href', selectedHref);
    await exactLink.click();
    const read = await detailResponse;
    expect(read.status()).toBe(200);
    const detail = (await read.json()) as Identity & { name: string };
    expect(detail.id).toBe(selectedID);
    expect(detail.name).toBe(selectedName);
    const expectDetail = async () => {
      await expect(page).toHaveURL(selectedURL.href);
      await expect(page.getByRole('heading', { name: detail.name, exact: true })).toBeVisible();
    };
    await expectDetail();
    await page.reload();
    await expectDetail();
    await page.getByRole('link', { name: c.backToConnections, exact: true }).click();
    await expectList('2', '10', 2);
    await page.goBack();
    await expectDetail();
    await page.goForward();
    await expectList('2', '10', 2);
    await page.reload();
    await expectList('2', '10', 2);
    await page.getByLabel(common.common.pageControls.size, { exact: true }).selectOption('20');
    await expectList('1', '20', 12);
    await search.fill(tag + ' no match');
    await page.getByRole('button', { name: c.search, exact: true }).click();
    await expect(page).toHaveURL(
      (url) => url.searchParams.get('connections_q') === tag + ' no match',
    );
    await expect(page.getByText(c.noMatches, { exact: true })).toBeVisible();
    await search.fill(tag);
    await page.getByRole('button', { name: c.search, exact: true }).click();
    await expectList('1', '20', 12);
    const missing = responseFor(page, 'GET', '/api/connections/9223372036854775807');
    await page.goto(
      fixture().user_url +
        '/endpoints?connection_id=9223372036854775807&connections_q=' +
        encodeURIComponent(tag) +
        '&connections_page_size=20',
    );
    expect((await missing).status()).toBe(404);
    await expect(page.locator('.core-state--error[role="alert"]')).toBeVisible();
    await page.getByRole('link', { name: c.backToConnections, exact: true }).click();
    await expectList('1', '20', 12);
    const manager = await admin.newPage();
    const name = 'Multi entry ' + randomUUID().slice(0, 8);
    const base = await baseURL(owner);
    await manager.goto(fixture().admin_url + '/mainstream-channels');
    await manager.getByRole('button', { name: channels.actions.new, exact: true }).click();
    let form = manager
      .locator('form')
      .filter({ has: manager.getByLabel(channels.form.name, { exact: true }) });
    await form.getByLabel(channels.form.name, { exact: true }).fill(name);
    await form.getByRole('button', { name: c.addEntry, exact: true }).click();
    await form.getByRole('button', { name: c.addEntry, exact: true }).click();
    for (const index of [0, 1]) {
      const group = form.getByRole('group', {
        name: c.presetEntry.replace('{{number}}', String(index + 1)),
        exact: true,
      });
      await group.getByLabel(c.name, { exact: true }).fill(name + ' ' + String(index + 1));
      const protocol = group.getByLabel(c.protocol, { exact: true });
      await protocol.selectOption({ label: channels.connector.openaiCompatible });
      await expect(protocol.locator('option:checked')).toHaveText(
        channels.connector.openaiCompatible,
      );
      await group.getByLabel(c.entryURL, { exact: true }).fill(base);
      await expect(
        group.getByRole('group', { name: common.common.nativeServices.title, exact: true }),
      ).toHaveCount(0);
    }
    const mappings = await openFold(form, c.modelMappings);
    await expect(mappings.getByRole('button', { name: c.addMapping, exact: true })).toBeDisabled();
    await expect(mappings.getByText(c.saveEntriesBeforeMappings, { exact: true })).toBeVisible();
    const suggestion = await openFold(form, c.discoverySuggestion);
    await suggestion.getByLabel(c.addDiscoverySuggestion, { exact: true }).check();
    await suggestion.getByLabel(c.discoveryBaseURL, { exact: true }).fill(base);
    await suggestion.getByLabel(c.listFormat, { exact: true }).selectOption('openai');
    const created = responseFor(manager, 'POST', '/admin/api/mainstream-channels');
    await form.getByRole('button', { name: channels.actions.create, exact: true }).click();
    const createdResponse = await created;
    expect(createdResponse.status()).toBe(201);
    const payload = createdResponse.request().postDataJSON() as Record<string, unknown>;
    expect(payload).not.toHaveProperty('connector_type');
    expect(payload).not.toHaveProperty('base_url');
    expect(payload.entries).toHaveLength(2);
    expect(
      (payload.entries as { native_services: NativeService[] }[]).map((row) => row.native_services),
    ).toEqual([await protocolServices(owner), await protocolServices(owner)]);
    expect(payload.model_mappings).toEqual([]);
    const channel = (await createdResponse.json()) as Identity & {
      entries: { id: string; name: string; native_services: NativeService[] }[];
    };
    expect(channel.entries.every((row) => /^[1-9][0-9]*$/.test(row.id))).toBe(true);
    form = manager
      .locator('form')
      .filter({ has: manager.getByLabel(channels.form.name, { exact: true }) });
    await expect(
      form.getByRole('button', { name: channels.actions.save, exact: true }),
    ).toBeVisible();
    const mappingEditor = await openFold(form, c.modelMappings);
    await mappingEditor.getByRole('button', { name: c.addMapping, exact: true }).click();
    await mappingEditor
      .getByLabel(c.catalogModel, { exact: true })
      .fill('synthetic-' + randomUUID().slice(0, 8));
    await mappingEditor.getByLabel(channel.entries[0]!.name, { exact: true }).check();
    await mappingEditor.getByLabel(c.upstreamModel, { exact: true }).first().fill('mapped-chat');
    const firstSelection = mappingEditor
      .getByRole('group', { name: common.common.nativeServices.title, exact: true })
      .first();
    for (const option of await firstSelection.getByRole('checkbox').all()) await option.uncheck();
    await firstSelection.getByLabel(native.chat_completions, { exact: true }).check();
    await mappingEditor.getByLabel(channel.entries[1]!.name, { exact: true }).check();
    await mappingEditor
      .getByLabel(c.upstreamModel, { exact: true })
      .nth(1)
      .fill('mapped-responses');
    const secondSelection = mappingEditor
      .getByRole('group', { name: common.common.nativeServices.title, exact: true })
      .nth(1);
    for (const option of await secondSelection.getByRole('checkbox').all()) await option.uncheck();
    await secondSelection.getByLabel(native.responses, { exact: true }).check();
    const edited = responseFor(manager, 'PATCH', '/admin/api/mainstream-channels/' + channel.id);
    await form.getByRole('button', { name: channels.actions.save, exact: true }).click();
    const savedResponse = await edited;
    expect(savedResponse.status()).toBe(200);
    const submitted = savedResponse.request().postDataJSON() as {
      expected_revision: string;
      entries: { id: string }[];
      model_mappings: { entries: { entry_id: string; native_services: NativeService[] }[] }[];
    };
    expect(submitted).not.toHaveProperty('connector_type');
    expect(submitted).not.toHaveProperty('base_url');
    expect(submitted.expected_revision).toBe(channel.revision);
    expect(submitted.model_mappings[0]!.entries.map((row) => row.entry_id)).toEqual(
      channel.entries.map((row) => row.id),
    );
    expect(submitted.model_mappings[0]!.entries.map((row) => row.native_services)).toEqual([
      ['chat_completions'],
      ['responses'],
    ]);
    const saved = await adminAPI<{
      entries: { id: string }[];
      discovery_source: { base_url: string; list_format: string };
      model_mappings: unknown[];
    }>(admin, '/admin/api/mainstream-channels/' + channel.id);
    expect(saved.entries.map((row) => row.id)).toEqual(channel.entries.map((row) => row.id));
    expect(saved.discovery_source).toEqual({ base_url: base, list_format: 'openai' });
    expect(saved.model_mappings).toEqual(submitted.model_mappings);
    await manager.reload();
    await expect(manager.getByRole('table', { name: channels.title, exact: true })).toBeVisible();
    const pageSize = manager.getByLabel(common.common.pageControls.size, { exact: true });
    if (await pageSize.count()) await pageSize.selectOption('100');
    await manager
      .getByRole('row')
      .filter({ hasText: name })
      .getByRole('button', { name: channels.actions.view, exact: true })
      .click();
    form = manager
      .locator('form')
      .filter({ has: manager.getByLabel(channels.form.name, { exact: true }) });
    await expect(form.getByLabel(channels.form.name, { exact: true })).toHaveValue(name);
    const reread = await openFold(form, c.modelMappings);
    const savedMapping = reread.getByRole('group', {
      name: c.modelMapping.replace('{{number}}', '1'),
      exact: true,
    });
    const mappedEntry = (name: string) =>
      savedMapping
        .locator(':scope > div.nb-stack')
        .filter({ has: manager.getByLabel(name, { exact: true }) });
    const savedChat = mappedEntry(channel.entries[0]!.name);
    const savedResponses = mappedEntry(channel.entries[1]!.name);
    await expect(savedChat.getByLabel(c.upstreamModel, { exact: true })).toHaveValue('mapped-chat');
    await expect(savedChat.getByLabel(native.chat_completions, { exact: true })).toBeChecked();
    await expect(savedChat.getByLabel(native.responses, { exact: true })).not.toBeChecked();
    await expect(savedResponses.getByLabel(c.upstreamModel, { exact: true })).toHaveValue(
      'mapped-responses',
    );
    await expect(
      savedResponses.getByLabel(native.chat_completions, { exact: true }),
    ).not.toBeChecked();
    await expect(savedResponses.getByLabel(native.responses, { exact: true })).toBeChecked();
    await screenshot(
      manager,
      info,
      'mainstream-mapped-entries',
      390,
      'light',
      reread.getByLabel(c.catalogModel, { exact: true }),
      {
        channel_id: channel.id,
        entries: channel.entries,
        model_mappings: saved.model_mappings,
        discovery_source: saved.discovery_source,
      },
    );
  } finally {
    await owner.close();
    await admin.close();
  }
});

test('bulk use review covers hidden pages and updates rejected and approved summaries', async ({
  browser,
}, info) => {
  const owner = await session(browser, 'owner');
  const admin = await session(browser, 'admin');
  const createdDonationIDs: string[] = [];
  const tag = 'Bulk uses ' + randomUUID().slice(0, 8);
  const services: NativeService[] = [
    'chat_completions',
    'responses',
    'embeddings',
    'images_generations',
    'audio_speech',
    'audio_transcriptions',
    'rerank',
    'moderations',
  ];
  try {
    const group = await connection(owner, tag);
    const base = await baseURL(owner);
    const keys: Key[] = [];
    for (let index = 0; index < 3; index++) {
      const saved = await credential(owner, group, tag + index, 'synthetic-bulk-' + randomUUID());
      keys.push((await entry(owner, group, saved, base, tag + index, services)).key);
    }
    const donation = await userAPI<Donation>(owner, '/api/donations', 'POST', {
      description: tag,
      ownership_authorized: true,
      discord_public_thanks: false,
      keys: keys.map((key) => ({
        endpoint_key_id: key.id,
        credential_id: key.credential_id,
        credential_version: key.credential_version,
        grant_revision: key.grant_revision,
        native_services: services,
        expires_at: null,
      })),
    });
    createdDonationIDs.push(donation.id);
    const page = await admin.newPage();
    await page.goto(
      fixture().admin_url +
        '/charity?charity_section=donations&donation_id=' +
        donation.id +
        '&uses_page_size=10',
    );
    await page.getByLabel(c.reviewReason, { exact: true }).fill('Synthetic complete rejection');
    for (const [width, theme] of [
      [1440, 'light'],
      [1440, 'dark'],
      [390, 'light'],
      [390, 'dark'],
    ] as const) {
      await screenshot(
        page,
        info,
        'bulk-use-review',
        width,
        theme,
        page.getByLabel(c.reviewReason, { exact: true }),
        {
          pending: 24,
          visiblePageSize: 10,
          scope: 'entire donation',
        },
      );
    }
    const reviewed = responseFor(
      page,
      'POST',
      '/admin/api/donations/' + donation.id + '/pending-review',
    );
    await page.getByRole('button', { name: c.rejectAllUses, exact: true }).click();
    await expect(page.getByRole('alertdialog')).toHaveCount(0);
    const receipt = await reviewed;
    expect(receipt.status()).toBe(200);
    expect((receipt.request().postDataJSON() as { uses: unknown[] }).uses).toHaveLength(24);
    await expect(page.getByText(c.reviewSaved, { exact: true }).first()).toBeVisible();
    const rejected = await adminAPI<
      Numbered<{ id: string; use_summary: { review_status: string } }>
    >(admin, '/admin/api/donations?page=1&page_size=100&review_status=rejected');
    expect(rejected.data.find((row) => row.id === donation.id)?.use_summary.review_status).toBe(
      'rejected',
    );
    const ownerPage = await owner.newPage();
    await ownerPage.goto(fixture().user_url + '/charity/donations/' + donation.id);
    await expect(
      ownerPage.locator('.status-badge').filter({ hasText: c.donationStates.rejected }).first(),
    ).toBeVisible();

    const saved = await credential(
      owner,
      group,
      tag + ' approve',
      'synthetic-approve-' + randomUUID(),
    );
    const value = await entry(owner, group, saved, base, tag + ' approve', services.slice(0, 2));
    const approved = await donate(owner, value.key, services.slice(0, 2), tag + ' approve');
    createdDonationIDs.push(approved.id);
    await page.goto(
      fixture().admin_url + '/charity?charity_section=donations&donation_id=' + approved.id,
    );
    await page.getByRole('button', { name: c.approveAllUses, exact: true }).first().click();
    await expect(page.getByRole('alertdialog')).toHaveCount(0);
    await expect(page.getByText(c.reviewSaved, { exact: true }).first()).toBeVisible();
    const rows = await uses(owner, approved.id);
    expect(rows).toHaveLength(2);
    expect(rows.every((row) => row.approved?.status === 'approved' && !row.pending)).toBe(true);
    await page
      .getByLabel(common.common.operations.charity.reason, { exact: true })
      .fill('Synthetic review of the approved services');
    await page
      .getByRole('button', { name: common.common.donationReview.forceTitle, exact: true })
      .click();
    const rejection = page.getByRole('alertdialog', {
      name: common.common.donationReview.forceTitle,
      exact: true,
    });
    const rejectedResponse = responseFor(
      page,
      'POST',
      '/admin/api/donations/' + approved.id + '/review',
    );
    await rejection
      .getByRole('button', { name: common.common.donationReview.forceTitle, exact: true })
      .click();
    const forcedRejection = await rejectedResponse;
    expect(forcedRejection.status()).toBe(200);
    expect(forcedRejection.request().postDataJSON().decision).toBe('force_reject');
    expect(forcedRejection.request().postDataJSON()).not.toHaveProperty('key_settings');
    expect((await uses(owner, approved.id)).every((row) => !row.approved && !row.pending)).toBe(
      true,
    );
    createdDonationIDs.splice(createdDonationIDs.indexOf(approved.id), 1);
  } finally {
    try {
      for (const id of createdDonationIDs) {
        await endDonation(owner, id);
      }
    } finally {
      await Promise.all([owner.close(), admin.close()]);
    }
  }
});

test('returned request alerts show source details and unresolved review state', async ({
  browser,
}, info) => {
  const admin = await session(browser, 'admin');
  try {
    const page = await admin.newPage();
    const state = fixture();
    await page.goto(state.admin_url + '/alerts?resolved=false&alert_id=' + state.return_alert_id);
    await expect(
      page.getByText(adminEn.admin.alerts.kindDescription.returnDetected).first(),
    ).toBeVisible();
    await expect(
      page.getByText(adminEn.admin.alerts.returnDetectedFacts.source_request_id, { exact: true }),
    ).toBeVisible();
    const detail = await adminAPI<{ alert: { subtype: string; resolved: boolean } }>(
      admin,
      '/admin/api/alerts/' + state.return_alert_id,
    );
    expect(detail.alert.subtype).toBe('return_detected');
    expect(detail.alert.resolved).toBe(false);
    for (const [width, theme] of [
      [1440, 'light'],
      [1440, 'dark'],
      [390, 'light'],
      [390, 'dark'],
    ] as const) {
      await screenshot(
        page,
        info,
        'returned-request-alert',
        width,
        theme,
        page.getByText(adminEn.admin.alerts.returnDetectedFacts.source_request_id, { exact: true }),
        { subtype: detail.alert.subtype, resolved: false },
      );
    }
  } finally {
    await admin.close();
  }
});

test('mainstream authorization isolates endpoint catalogs and lets users decline the suggestion', async ({
  browser,
}, info) => {
  const owner = await session(browser, 'owner');
  const admin = await session(browser, 'admin');
  const tag = 'Catalog default ' + randomUUID().slice(0, 8);
  let group: Identity | undefined;
  let channel: (Identity & { entries: { id: string; name: string }[] }) | undefined;
  try {
    group = await connection(owner, tag);
    const saved = await credential(owner, group, tag, 'synthetic-catalog-' + randomUUID());
    const base = await baseURL(owner);
    channel = await adminAPI<Identity & { entries: { id: string; name: string }[] }>(
      admin,
      '/admin/api/mainstream-channels',
      'POST',
      {
        name: tag,
        category: 'api_platform',
        enabled: true,
        entries: [1, 2, 3].map((index) => ({
          name: tag + ' ' + index,
          connector_type: 'openai-compatible',
          base_url: base,
          native_services: ['chat_completions', 'responses'],
          rerank_format: '',
          enabled: true,
        })),
        discovery_source: { base_url: base, list_format: 'openai' },
        model_mappings: [],
      },
    );
    const page = await owner.newPage();
    await page.goto(fixture().user_url + '/endpoints?connection_id=' + group.id);
    await page.getByRole('button', { name: c.addEntry, exact: true }).click();
    const form = page.getByRole('region', { name: c.addEntry, exact: true });
    await openFold(form, services.advancedSetup);
    const mainstreamMode = form.getByRole('radio', { name: c.mainstream, exact: true });
    await form.locator('.nb-seg').getByText(c.mainstream, { exact: true }).click();
    await expect(mainstreamMode).toBeChecked();
    await form.getByRole('combobox', { name: c.mainstream, exact: true }).selectOption(channel.id);
    const entryChoices = await openFold(form, c.entries);
    for (const [index, item] of channel.entries.entries()) {
      const selectedEntry = entryChoices.getByRole('checkbox', { name: item.name, exact: true });
      await expect(selectedEntry).toBeChecked();
      const option = selectedEntry.locator(
        'xpath=ancestor::div[contains(@class, "nb-stack--sm")][1]',
      );
      await expect(option.getByLabel(native.chat_completions, { exact: true })).toHaveCount(0);
      await expect(option.getByLabel(native.responses, { exact: true })).toHaveCount(0);
      const defaultCatalog = option.getByLabel(c.useSuggestedCatalog, { exact: true });
      await expect(defaultCatalog).toBeChecked();
      if (index === 2) await defaultCatalog.uncheck();
      if (index === 0) {
        for (const width of [1440, 390])
          for (const theme of ['light', 'dark'] as const)
            await screenshot(
              page,
              info,
              'suggested-catalog-authorization',
              width,
              theme,
              defaultCatalog,
              { credential_version: saved.credential_version, default_selected: true },
            );
      }
    }
    await expect(
      form.getByRole('radio', { name: c.existingCredential, exact: true }),
    ).toBeChecked();
    await form
      .getByRole('table', { name: c.chooseCredential, exact: true })
      .getByRole('row')
      .filter({ hasText: tag })
      .getByRole('button', { name: c.select, exact: true })
      .click();
    const grantResponses: { path: string; status: number }[] = [];
    page.on('response', (response) => {
      const path = new URL(response.url()).pathname;
      if (response.request().method() === 'POST' && /^\/api\/endpoints\/[^/]+\/keys$/.test(path))
        grantResponses.push({ path, status: response.status() });
    });
    await form
      .getByRole('button', { name: userEn.user.workflows.continueService, exact: true })
      .click();
    await expect(form.getByText(userEn.user.workflows.serviceReady, { exact: true })).toBeVisible();
    await expect(form.locator('.service-key-models')).toHaveCount(3);
    await expect(
      form.getByRole('link', { name: userEn.user.workflows.donateService, exact: true }),
    ).toBeVisible();
    const entries = (
      await userAPI<Numbered<Entry & { note: string }>>(
        owner,
        '/api/connections/' + group.id + '/entries?page=1&page_size=20',
      )
    ).data;
    expect(entries).toHaveLength(3);
    expect(grantResponses.map((response) => response.status)).toEqual([201, 201, 201]);
    expect(grantResponses.map((response) => response.path).sort()).toEqual(
      entries.map((entry) => '/api/endpoints/' + entry.id + '/keys').sort(),
    );
    const granted: Key[] = [];
    for (const [index, item] of channel.entries.entries()) {
      const target = entries.find((row) => row.note === item.name)!;
      const keys = await userAPI<Numbered<Key>>(
        owner,
        '/api/endpoints/' + target.id + '/keys?page=1&page_size=20',
      );
      expect(keys.data).toHaveLength(1);
      const grant = keys.data[0]!;
      expect(grant.credential_id).toBe(saved.id);
      expect(grant.credential_version).toBe(saved.credential_version);
      expect(grant.native_services).toEqual(await protocolServices(owner));
      if (index < 2) expect(grant.discovery_source_id).not.toBeNull();
      else expect(grant.discovery_source_id).toBeNull();
      granted.push(grant);
    }
    expect(granted[1]!.discovery_source_id).not.toBe(granted[0]!.discovery_source_id);
    const sources = (
      await userAPI<
        Numbered<Source & { credential_id: string; base_url: string; list_format: string }>
      >(owner, '/api/connections/' + group.id + '/discovery-sources?page=1&page_size=20')
    ).data;
    expect(sources).toHaveLength(2);
    for (const key of granted.slice(0, 2))
      expect(sources.find((source) => source.id === key.discovery_source_id)).toMatchObject({
        credential_id: saved.id,
        credential_version: saved.credential_version,
        base_url: base,
        list_format: 'openai',
      });

    const draft = await owner.newPage();
    const first = entries.find((row) => row.note === channel!.entries[0]!.name)!;
    await draft.goto(
      fixture().user_url +
        '/charity?tab=donate&endpoint_id=' +
        first.id +
        '&endpoint_q=' +
        encodeURIComponent(first.note),
    );
    await draft
      .getByRole('button', { name: userEn.user.charity.presentation.openPicker, exact: true })
      .click();
    const picker = draft.locator('.donation-resource-picker');
    await picker.locator('.donation-resource-picker__key-list').getByRole('checkbox').check();
    const selected = picker.getByRole('region', {
      name: userEn.user.charity.resourcePicker.selectedTitle,
      exact: true,
    });
    await expect(selected.getByLabel(native.chat_completions, { exact: true })).toHaveCount(0);
    await expect(selected.getByLabel(native.responses, { exact: true })).toHaveCount(0);
    for (const width of [1440, 390])
      for (const theme of ['light', 'dark'] as const)
        await screenshot(draft, info, 'suggested-catalog-donation-draft', width, theme, selected, {
          services: granted[0]!.native_services,
          discovery_source_id: granted[0]!.discovery_source_id,
          submitted: false,
        });
    await draft.route('**/api/appearance', (route) =>
      route.fulfill({
        json: {
          selected: 'celestial',
          moment: 'cover:h',
          moments: ['cover:h'],
          unlocked: ['default', 'celestial'],
          visible: ['default', 'celestial'],
          updated_at: 1800000000,
        },
      }),
    );
    await draft.reload();
    await expect(draft.locator('html')).toHaveAttribute('data-appearance', 'celestial');
    await draft
      .getByRole('button', { name: userEn.user.charity.presentation.openPicker, exact: true })
      .click();
    await picker.locator('.donation-resource-picker__key-list').getByRole('checkbox').check();
    for (const width of [1440, 390])
      await screenshot(
        draft,
        info,
        'suggested-catalog-donation-appearance',
        width,
        'dark',
        selected,
        {
          appearance: 'celestial',
          submitted: false,
        },
      );
  } finally {
    try {
      if (group) {
        const current = await userAPI<Identity>(owner, '/api/connections/' + group.id);
        await userAPI(
          owner,
          '/api/connections/' + group.id,
          'DELETE',
          { expected_revision: current.revision },
          204,
        );
      }
      if (channel) {
        const current = await adminAPI<Identity>(
          admin,
          '/admin/api/mainstream-channels/' + channel.id,
        );
        await adminAPI(
          admin,
          '/admin/api/mainstream-channels/' + channel.id,
          'DELETE',
          { expected_revision: current.revision, confirmation: 'retire' },
          204,
        );
      }
    } finally {
      await owner.close();
      await admin.close();
    }
  }
});

test('a key without a catalog can recover, show a readable failure and load models', async ({
  browser,
}, info) => {
  const owner = await session(browser, 'owner');
  const tag = 'Catalog recovery ' + randomUUID().slice(0, 8);
  let group: Identity | undefined;
  try {
    group = await connection(owner, tag);
    const saved = await credential(owner, group, tag, 'synthetic-image-credential');
    const base = await baseURL(owner);
    const value = await entry(owner, group, saved, base, tag, ['chat_completions']);
    const page = await owner.newPage();
    const keyPath = '/api/endpoints/' + value.endpoint.id + '/keys/' + value.key.id;
    const refreshPath = keyPath + '/models/refresh';
    const core = userEn.user.core;
    await page.goto(fixture().user_url + '/endpoints/' + value.endpoint.id);
    await expect(
      page.getByRole('button', { name: core['endpoints.refreshDiscovery'], exact: true }),
    ).toHaveCount(0);
    await page.getByRole('button', { name: userEn.user.services.viewModels, exact: true }).click();
    await expect(
      page.getByRole('button', { name: services.fetchModels, exact: true }),
    ).toBeDisabled();
    await openFold(page, services.manualModels);
    await page
      .getByLabel(core['endpoints.manualModel'], { exact: true })
      .fill('vendor/manual-before-discovery');
    const manualCreated = responseFor(page, 'POST', keyPath + '/models/manual');
    await page.getByRole('button', { name: core['endpoints.manualAdd'], exact: true }).click();
    expect((await manualCreated).status()).toBe(201);
    await expect(page.getByText('vendor/manual-before-discovery', { exact: true })).toBeVisible();
    await page
      .getByRole('region', { name: userEn.user.services.viewModels + ' · ' + tag, exact: true })
      .getByRole('button', { name: core['common.close'], exact: true })
      .click();
    await page
      .getByRole('button', { name: userEn.user.services.chooseCatalog, exact: true })
      .click();
    const grant = await openFold(page, services.modelListSettings);
    await expect(grant.getByLabel(c.discoveryBaseURL, { exact: true })).toHaveValue(base);
    await expect(grant.getByRole('table', { name: c.chooseCredential, exact: true })).toHaveCount(
      0,
    );
    const createdSource = responseFor(
      page,
      'POST',
      '/api/connections/' + group.id + '/discovery-sources',
    );
    const patched = responseFor(page, 'PATCH', keyPath);
    await grant.getByRole('button', { name: services.saveListSettings, exact: true }).click();
    const sourceResponse = await createdSource;
    expect(sourceResponse.status()).toBe(201);
    const source = (await sourceResponse.json()) as Source;
    const patchResponse = await patched;
    expect(patchResponse.status()).toBe(200);
    expect(((await patchResponse.json()) as Key).discovery_source_id).toBe(source.id);
    await page
      .getByRole('region', { name: userEn.user.services.editKey, exact: true })
      .getByRole('button', { name: core['common.close'], exact: true })
      .click();
    await page.getByRole('button', { name: services.viewModels, exact: true }).click();

    const failure = 'The selected model catalog changed. Choose the current catalog and try again.';
    await page.route('**' + refreshPath, (route) =>
      route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'invalid_request', message: failure } }),
      }),
    );
    await page
      .getByRole('button', { name: core['endpoints.refreshDiscovery'], exact: true })
      .click();
    const feedback = page.locator('.core-key-feedback');
    await expect(feedback).toContainText(failure);
    await expect(feedback.locator('td')).toHaveAttribute('colspan', '6');
    for (const width of [1440, 390])
      for (const theme of ['light', 'dark'] as const)
        await screenshot(page, info, 'key-operation-error', width, theme, feedback, {
          error: 'invalid_request',
          catalog_linked: true,
        });
    await page.unroute('**' + refreshPath);
    const refreshed = responseFor(page, 'POST', refreshPath);
    await page
      .getByRole('button', { name: core['endpoints.refreshDiscovery'], exact: true })
      .click();
    expect((await refreshed).status()).toBe(202);
    await expect
      .poll(
        async () =>
          (await userAPI<{ evidence: { state: string } }>(owner, keyPath + '/models')).evidence
            .state,
      )
      .toBe('succeeded');
    const catalog = await userAPI<{
      automatic_entries: { upstream_model_id: string }[];
      manual_entries: { upstream_model_id: string }[];
    }>(owner, keyPath + '/models');
    expect(catalog.automatic_entries.length).toBeGreaterThan(0);
    expect(catalog.manual_entries.map((row) => row.upstream_model_id)).toContain(
      'vendor/manual-before-discovery',
    );
    await expect(
      page.getByText(catalog.automatic_entries[0]!.upstream_model_id, { exact: true }),
    ).toBeVisible();
    await openFold(page, services.manualModels);
    await page.getByLabel(core['endpoints.manualModel'], { exact: true }).fill('vendor/recovered');
    await page.getByRole('button', { name: core['endpoints.manualAdd'], exact: true }).click();
    await expect(page.getByText('vendor/recovered', { exact: true })).toBeVisible();
  } finally {
    try {
      if (group) {
        const current = await userAPI<Identity>(owner, '/api/connections/' + group.id);
        await userAPI(
          owner,
          '/api/connections/' + group.id,
          'DELETE',
          { expected_revision: current.revision },
          204,
        );
      }
    } finally {
      await owner.close();
    }
  }
});

test('model imports reuse manual support after reopening and resume after a model conflict', async ({
  browser,
}) => {
  const owner = await session(browser, 'owner');
  const tag = 'Import reuse ' + randomUUID().slice(0, 8);
  const provider = 'import-reuse-' + randomUUID().slice(0, 8);
  const models: string[] = [];
  let group: Identity | undefined;
  try {
    group = await connection(owner, tag);
    const saved = await credential(owner, group, tag, 'synthetic-import-' + randomUUID());
    const base = await baseURL(owner);
    const target = await entry(owner, group, saved, base, tag, ['chat_completions']);
    const catalogPath =
      '/api/endpoints/' + target.endpoint.id + '/keys/' + target.key.id + '/models';
    const upstream = 'existing-' + randomUUID().slice(0, 8);
    const original = await userAPI<{
      entries: { id: string; provider: string; source_revision: string; pair_revision: string }[];
    }>(owner, catalogPath + '/manual', 'POST', {
      entries: [{ upstream_model_id: upstream, provider: 'Original provider' }],
    });
    const page = await owner.newPage();
    const manualWrites: string[] = [];
    page.on('request', (request) => {
      if (
        request.method() === 'POST' &&
        new URL(request.url()).pathname === catalogPath + '/manual'
      )
        manualWrites.push(request.postData() ?? '');
    });
    const destination =
      fixture().user_url +
      '/endpoints?connection_id=' +
      group.id +
      '&endpoint_id=' +
      target.endpoint.id +
      '&key_id=' +
      target.key.id;
    const prepare = async (upstreamName: string, modelName: string) => {
      await page.goto(destination);
      const pairModels = page.locator('.service-key-models');
      await pairModels.getByRole('tab', { name: services.manualModels, exact: true }).click();
      await pairModels.getByRole('checkbox', { name: upstream, exact: true }).check();
      const row = await openFold(page.locator('.connection-model-batch'), upstream);
      await row.getByRole('checkbox', { name: tag + ' · ' + upstream, exact: true }).check();
      if (upstreamName !== upstream)
        await row.getByLabel(c.upstreamModel + ' · ' + tag, { exact: true }).fill(upstreamName);
      await row
        .getByRole('group', { name: common.common.nativeServices.title, exact: true })
        .getByRole('checkbox', { name: native.chat_completions, exact: true })
        .check();
      await row.getByLabel(c.provider, { exact: true }).fill(provider);
      await row.getByLabel(c.model, { exact: true }).fill(modelName);
      await row
        .getByRole('group', { name: common.common.modelTypes.title, exact: true })
        .getByRole('checkbox', { name: native.chat_completions, exact: true })
        .check();
      return row;
    };
    const save = async (row: Locator, upstreamName: string) => {
      const written = responseFor(page, 'POST', '/api/models');
      await row.getByRole('button', { name: c.saveMapping, exact: true }).click();
      const response = await written;
      expect(response.status()).toBe(201);
      const model = (await response.json()) as Identity;
      models.push(model.id);
      await expect(row.getByText(c.mappingSaved, { exact: true })).toBeVisible();
      const bindings = await userAPI<Bindings>(owner, '/api/models/' + model.id + '/bindings');
      expect(bindings.bindings).toHaveLength(1);
      expect(bindings.bindings[0]!.targets).toMatchObject([
        {
          endpoint_key_id: target.key.id,
          upstream_model_id: upstreamName,
          native_services: ['chat_completions'],
        },
      ]);
    };
    const firstName = 'first-' + randomUUID().slice(0, 8);
    await save(await prepare(upstream, firstName), upstream);
    await save(await prepare(upstream, 'reopened-' + randomUUID().slice(0, 8)), upstream);
    expect(manualWrites).toHaveLength(0);
    const reused = await userAPI<{ manual_entries: unknown[] }>(
      owner,
      catalogPath + '?page=1&page_size=100&source=manual',
    );
    expect(reused.manual_entries).toEqual(original.entries);

    const fresh = 'fresh-' + randomUUID().slice(0, 8);
    const row = await prepare(fresh, firstName);
    const failed = responseFor(page, 'POST', '/api/models');
    await row.getByRole('button', { name: c.saveMapping, exact: true }).click();
    expect((await failed).status()).toBe(409);
    await expect(row.getByText(c.mappingSaved, { exact: true })).toHaveCount(0);
    expect(manualWrites).toHaveLength(1);
    await expect(row.getByLabel(c.model, { exact: true })).toBeEnabled();
    await row.getByLabel(c.model, { exact: true }).fill('recovered-' + randomUUID().slice(0, 8));
    await save(row, fresh);
    expect(manualWrites).toHaveLength(1);
    const final = await userAPI<{ manual_entries: { upstream_model_id: string }[] }>(
      owner,
      catalogPath + '?page=1&page_size=100&source=manual',
    );
    expect(final.manual_entries.map((item) => item.upstream_model_id).sort()).toEqual(
      [upstream, fresh].sort(),
    );
  } finally {
    try {
      try {
        for (const id of models) {
          const current = await userAPI<Identity>(owner, '/api/models/' + id);
          await userAPI(
            owner,
            '/api/models/' + id,
            'DELETE',
            { expected_revision: current.revision },
            204,
          );
        }
      } finally {
        if (group) {
          const current = await userAPI<Identity>(owner, '/api/connections/' + group.id);
          await userAPI(
            owner,
            '/api/connections/' + group.id,
            'DELETE',
            { expected_revision: current.revision },
            204,
          );
        }
      }
    } finally {
      await owner.close();
    }
  }
});
