import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { numberedPage } from './numbered-fixtures';
import { USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { expect, test } from './test';
import { createOptions, mockConnectionResources } from './connection-fixtures';
import enCommon from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zhCommon from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

const endpoint = {
  id: '11',
  connection_id: '1',
  native_services: ['chat_completions', 'responses'],
  connector_type: 'openai-compatible',
  base_url: 'https://provider.example.test/v1',
  origin: { kind: 'custom' },
  note: 'Browser service',
  enabled: true,
  revision: '3',
  key_count: '3',
  created_at: 1700000000,
  updated_at: 1700000001,
};
const evidence = {
  state: 'succeeded',
  revision: '1',
  result: 'nonempty',
  safe_class: 'none',
  observed_at: 1700000000,
  count: '2',
};
const key = {
  id: '21',
  credential_id: '31',
  credential_version: '1',
  grant_revision: '1',
  native_services: ['chat_completions', 'responses'],
  discovery_source_id: null,
  endpoint_id: '11',
  display_head: 'head',
  display_tail: 'tail',
  note: 'Primary key',
  enabled: true,
  force_store_false: false,
  max_concurrency: 0,
  max_rpm: 0,
  suspension_state: 'none',
  revision: '4',
  created_at: 1700000000,
  updated_at: 1700000001,
  browse: {
    model_count: '0',
    binding_count: '0',
    available_binding_count: '0',
    donation_eligibility: 'eligible',
    preview: [],
    discovery: evidence,
  },
};
const entries = ['model-a', 'model-b'].map((name, i) => ({
  id: String(41 + i),
  source_type: 'automatic',
  upstream_model_id: name,
  provider: 'demo',
  source_revision: '1',
  pair_revision: '1',
  created_at: 1700000000,
  updated_at: 1700000001,
}));
function model(id: string, name: string, bound = false) {
  return {
    model_types: ['chat_completions', 'responses', 'embeddings'],
    id,
    provider: 'demo',
    model: name,
    full_name: `demo/${name}`,
    route_strategy: 'ordered',
    silent_retry: false,
    transport_rule: 'passthrough',
    cloudflare_proxy_enabled: false,
    cloudflare_proxy_mode: 'direct',
    flatten_tool_calls: false,
    revision: '1',
    binding_revision: bound ? '2' : '1',
    binding_count: bound ? '1' : '0',
    created_at: 1700000000,
    updated_at: 1700000001,
  };
}
const projection = {
  revision: '0',
  forward_headers: { mode: 'replace', values: [] },
  fixed_headers: { mode: 'replace', values: {} },
  body_defaults: { mode: 'replace', values: {} },
  body_forced: { mode: 'replace', values: {} },
  native_extension_paths: { mode: 'replace', values: [] },
};

for (const locale of ['en', 'zh'] as const) {
  test(`service secret markers remain editable without revealing values ${locale}`, async ({
    page,
  }) => {
    await page.addInitScript((language) => localStorage.setItem('nb.lang', language), locale);
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    const protectedProjection = {
      ...projection,
      fixed_headers: {
        mode: 'replace',
        values: { 'X-Token': { has_value: true, mask: '••••', sensitive_paths: [''] } },
      },
      body_forced: {
        mode: 'replace',
        values: { '/vendor': { has_value: true, mask: '••••', sensitive_paths: ['/token'] } },
      },
    };
    const writes: Record<string, unknown>[] = [];
    await page.route(`${USER_ORIGIN}/api/**`, async (route) => {
      const request = route.request(),
        url = new URL(request.url());
      if (url.pathname === '/api/endpoint-create-options')
        return route.fulfill({ json: createOptions });
      if (url.pathname === '/api/endpoints/11') return route.fulfill({ json: endpoint });
      if (url.pathname === '/api/endpoints/11/keys')
        return route.fulfill({ json: numberedPage([], url.searchParams) });
      if (url.pathname === '/api/endpoints/11/request-adaptation') {
        if (request.method() === 'PUT')
          writes.push(request.postDataJSON() as Record<string, unknown>);
        return route.fulfill({ json: protectedProjection });
      }
      return route.fallback();
    });
    const copy = (en: string, zh: string) => (locale === 'en' ? en : zh);
    await page.goto(`${USER_ORIGIN}/endpoints/11`);
    const fold = page
      .locator('summary')
      .filter({ hasText: copy('Request rewriting (advanced)', '请求改写（高级）') });
    await fold.focus();
    await page.keyboard.press('Enter');
    const protection = page.getByRole('combobox', {
      name: copy('Response protection: X-Token', '响应防回显: X-Token'),
      exact: true,
    });
    await expect(protection).toHaveValue('whole');
    const paths = page.getByLabel(copy('Protected string paths', '敏感字符串路径'), {
      exact: true,
    });
    await expect(paths).toHaveValue('/token');
    for (const width of [1440, 390])
      for (const theme of ['light', 'dark']) {
        await page.setViewportSize({ width, height: 900 });
        await page.evaluate((value) => (document.documentElement.dataset.theme = value), theme);
        await protection.scrollIntoViewIfNeeded();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        if (process.env.NONBIRI_VISUAL_DIR)
          await page.screenshot({
            path: join(
              process.env.NONBIRI_VISUAL_DIR,
              `secret-markers-${locale}-${theme}-${width}.png`,
            ),
            fullPage: true,
          });
      }
    await protection.selectOption('off');
    await paths.fill('/credentials/0/token\n/token');
    await page
      .getByRole('button', { name: copy('Save request rewriting', '保存请求改写'), exact: true })
      .click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0].fixed_headers).toEqual({
      mode: 'replace',
      values: { 'X-Token': { action: 'keep', sensitive_paths: [] } },
    });
    expect(writes[0].body_forced).toEqual({
      mode: 'replace',
      values: {
        '/vendor': { action: 'keep', sensitive_paths: ['/credentials/0/token', '/token'] },
      },
    });
  });
}

for (const locale of ['en', 'zh'] as const) {
  test(`service setup keeps drafts, ownership and partial results ${locale}`, async ({
    page,
    context,
  }) => {
    test.setTimeout(180000);
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    page.on('console', (message) => {
      if (
        (message.type() === 'error' || message.type() === 'warning') &&
        ![
          'Failed to load resource: the server responded with a status of 400 (Bad Request)',
          'Failed to load resource: the server responded with a status of 409 (Conflict)',
        ].includes(message.text())
      )
        errors.push(message.text());
    });
    await installURLPersistenceObserver(context, ['synthetic-setup-secret']);
    await page.addInitScript((lang) => {
      localStorage.setItem('nb.lang', lang);
      localStorage.setItem('nb.theme', 'light');
    }, locale);
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    let count = 3,
      empty = false,
      failedRead = false,
      failedModel = false;
    let discoveryRefreshes = 0;
    const bound = new Set<string>();
    const c = (locale === 'zh' ? zhCommon : enCommon).common.connections;
    const chatLabel = (locale === 'zh' ? zhCommon : enCommon).common.modelTypes.chat_responses;
    const nativeResponse = (locale === 'zh' ? zhCommon : enCommon).common.modelTypes.responses;
    const source = () => ({
      id: '51',
      connection_id: '1',
      name: 'Shared catalog',
      base_url: endpoint.base_url,
      models_url: endpoint.base_url + '/models',
      list_format: 'openai',
      credential_id: '31',
      credential_version: '1',
      revision: '1',
      discovery_source_revision: '1',
      enabled: true,
      state: failedRead ? 'failed' : 'succeeded',
      checked_at: 1700000000,
      safe_class: failedRead ? 'transport' : 'none',
      safe_diag: '',
      created_at: 1700000000,
      updated_at: 1700000001,
    });
    await mockConnectionResources(
      page,
      () => [endpoint],
      () =>
        empty
          ? []
          : [
              {
                id: '1',
                name: 'Browser connection',
                note: '',
                revision: '1',
                entry_count: '1',
                credential_count: '1',
                created_at: 1700000000,
                updated_at: 1700000001,
              },
            ],
    );

    const writes: { path: string; body: Record<string, unknown>; identity: string | undefined }[] =
      [];
    await page.route(`${USER_ORIGIN}/api/**`, async (route) => {
      const request = route.request(),
        url = new URL(request.url()),
        path = url.pathname;
      const input =
        request.method() === 'POST'
          ? (request.postDataJSON() as Record<string, unknown> | null)
          : null;
      const fulfill = (body: unknown, status = 200) => route.fulfill({ status, json: body });
      if (request.method() === 'POST')
        writes.push({ path, body: input ?? {}, identity: request.headers()['idempotency-key'] });
      if (path === '/api/endpoint-create-options') return fulfill(createOptions);
      if (path === '/api/connections/1/discovery-sources' && input) return fulfill(source(), 201);
      if (path === '/api/discovery-sources/51') return fulfill(source());
      if (path === '/api/discovery-sources/51/refresh') {
        discoveryRefreshes++;
        if (discoveryRefreshes === 2) failedRead = false;
        return fulfill(
          {
            operation_id: 'op_000000000000000000000A',
            discovery_source: { ...source(), state: 'checking' },
          },
          202,
        );
      }
      if (path === '/api/discovery-sources/51/models')
        return fulfill(
          numberedPage(
            failedRead
              ? []
              : entries.map((row) => ({
                  id: row.id,
                  upstream_model_id: row.upstream_model_id,
                  provider: row.provider,
                  native_services: [],
                  service_evidence: 'unknown',
                  needs_service_selection: true,
                })),
            url.searchParams,
          ),
        );
      if (/\/keys\/\d+\/models\/manual$/.test(path))
        return fulfill(
          {
            entries: [
              {
                ...entries[0],
                id: '91',
                source_type: 'manual',
                upstream_model_id: (input?.entries as { upstream_model_id: string }[])[0]
                  ?.upstream_model_id,
              },
            ],
          },
          201,
        );
      if (path === '/api/endpoints')
        return request.method() === 'POST'
          ? fulfill({ ...endpoint, ...input }, 201)
          : fulfill(
              numberedPage(
                empty
                  ? []
                  : [endpoint, { ...endpoint, id: '12', note: 'Backup service', enabled: false }],
                url.searchParams,
              ),
            );
      if (path === '/api/endpoints/11') return fulfill({ ...endpoint, key_count: String(count) });
      if (path === '/api/endpoints/11/request-adaptation') return fulfill(projection);
      if (path === '/api/endpoints/11/keys')
        return request.method() === 'POST'
          ? fulfill(key, 201)
          : fulfill(
              numberedPage(
                Array.from({ length: count }, (_, i) => ({
                  ...key,
                  id: String(21 + i),
                  note: i ? `Backup ${i}` : key.note,
                })),
                url.searchParams,
              ),
            );
      if (/\/keys\/\d+\/models\/refresh$/.test(path)) {
        discoveryRefreshes++;
        if (discoveryRefreshes === 2) failedRead = false;
        return fulfill(
          {
            operation_id: 'op_000000000000000000000A',
            evidence: { ...evidence, state: 'checking', result: null, count: null },
          },
          202,
        );
      }
      if (/\/keys\/\d+\/models$/.test(path)) {
        const current = failedRead
          ? { ...evidence, state: 'failed', result: null, count: null, safe_class: 'transport' }
          : evidence;
        const catalog = {
          evidence: current,
          automatic_entries:
            failedRead || url.searchParams.get('source') === 'manual' ? [] : entries,
          manual_entries: [],
          next_cursor: null,
        };
        return fulfill(
          url.searchParams.has('limit')
            ? catalog
            : {
                ...catalog,
                pagination: {
                  page: '1',
                  page_size: 20,
                  total_items: catalog.automatic_entries.length.toString(),
                  total_pages: '1',
                },
              },
        );
      }
      if (path === '/api/models' && input) {
        if (input.model === 'model-a')
          return fulfill(
            { error: { code: 'conflict', message: 'Calling name already exists' } },
            409,
          );
        if (!failedModel && input.model === 'model-b') {
          failedModel = true;
          return fulfill(
            { error: { code: 'invalid_request', message: 'Synthetic model failure' } },
            400,
          );
        }
        return fulfill(
          { ...model('52', String(input.model)), model_types: input.model_types },
          201,
        );
      }
      if (path === '/api/models')
        return fulfill(
          numberedPage(
            url.searchParams.get('q') === 'demo/model-a' ? [model('51', 'model-a')] : [],
            url.searchParams,
          ),
        );
      if (/^\/api\/models\/\d+\/bindings(?:\/batch(?:-receipt)?)?$/.test(path)) {
        const id = path.split('/')[3];
        if (input) bound.add(id);
        if (input && path.endsWith('/batch-receipt'))
          return fulfill(
            { model_id: id, binding_revision: '2', added_count: '1', binding_count: '1' },
            201,
          );
        return fulfill(
          {
            binding_revision: bound.has(id) ? '2' : '1',
            bindings: bound.has(id)
              ? [
                  {
                    id: `${id}1`,
                    endpoint_key_id: '21',
                    endpoint_base_url: endpoint.base_url,
                    connector_type: endpoint.connector_type,
                    endpoint_note: endpoint.note,
                    endpoint_key_display_head: key.display_head,
                    endpoint_key_display_tail: key.display_tail,
                    endpoint_key_note: key.note,
                    upstream_model_id: id === '51' ? 'model-a' : 'model-b',
                    ord: 0,
                  },
                ]
              : [],
          },
          input ? 201 : 200,
        );
      }
      if (/^\/api\/models\/\d+$/.test(path)) {
        const id = path.split('/').pop()!;
        return fulfill(model(id, id === '51' ? 'model-a' : 'model-b', bound.has(id)));
      }
      return route.fallback();
    });
    const copy = (en: string, zh: string) => (locale === 'en' ? en : zh);
    const metrics: Record<string, unknown>[] = [];
    async function shot(name: string) {
      const out = process.env.NONBIRI_VISUAL_DIR;
      if (!out) return;
      await mkdir(out, { recursive: true });
      for (const width of [1440, 768, 390])
        for (const theme of ['light', 'dark']) {
          await page.setViewportSize({ width, height: 900 });
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
            document.documentElement.style.colorScheme = value;
          }, theme);
          await page.waitForTimeout(250);
          await page.screenshot({
            path: join(out, `${name}-${locale}-${theme}-${width}.png`),
            fullPage: true,
          });
          const size = await page.evaluate(() => ({
            width: innerWidth,
            scrollWidth: document.documentElement.scrollWidth,
            height: document.documentElement.scrollHeight,
            sections: [
              ...document.querySelectorAll(
                '.services-detail > *, .services-key-table tr.core-key-card > td',
              ),
            ].map((e) => ({
              class: e.className,
              tag: e.tagName,
              height: e.getBoundingClientRect().height,
              padding: getComputedStyle(e).padding,
              gap: getComputedStyle(e).gap,
            })),
            overflowing: [...document.querySelectorAll('body *')]
              .filter((e) => {
                const r = e.getBoundingClientRect();
                return r.width && r.right > innerWidth + 0.5;
              })
              .map((e) => ({
                tag: e.tagName,
                class: e.className,
                right: e.getBoundingClientRect().right,
                width: e.getBoundingClientRect().width,
              }))
              .slice(0, 8),
          }));
          metrics.push({ name, locale, theme, ...size });
          await writeFile(join(out, `metrics-${locale}.json`), JSON.stringify(metrics, null, 2));
          expect(size.scrollWidth, `${name} ${locale} ${width} overflow`).toBeLessThanOrEqual(
            width,
          );
          // The entry-services fold adds a section above the three compact key rows.
          if (name === 'detail-3' && width === 390) expect(size.height).toBeLessThan(1900);
        }
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.evaluate(() => (document.documentElement.dataset.theme = 'light'));
      await writeFile(join(out, `metrics-${locale}.json`), JSON.stringify(metrics, null, 2));
    }
    await page.goto(`${USER_ORIGIN}/endpoints`);
    await expect(page.getByText('Browser connection', { exact: true })).toBeVisible();
    await shot('list');
    await page.getByRole('searchbox').fill('Browser');
    await page.getByRole('button', { name: c.search, exact: true }).click();
    await shot('filter');
    empty = true;
    await page.reload();
    await expect(page.getByText(c.emptyTitle, { exact: true })).toBeVisible();
    await shot('empty');
    empty = false;
    for (const n of [1, 3, 12]) {
      count = n;
      await page.goto(`${USER_ORIGIN}/endpoints/11`);
      await expect(page.getByText('Primary key', { exact: true })).toBeVisible();
      await shot(`detail-${n}`);
    }
    await page
      .getByRole('button', { name: copy('View models', '查看模型'), exact: true })
      .first()
      .click();
    await expect(page.locator('.nb-expandable-panel:not([hidden])')).toBeVisible();
    await shot('models-panel');
    const manual = page
      .locator('.nb-expandable-panel:not([hidden])')
      .getByLabel(copy('Provider model name', '服务商的模型名'), { exact: true });
    await manual.fill('draft-model');
    await page
      .locator('.nb-expandable-panel:not([hidden])')
      .getByRole('button', { name: copy('Close', '关闭'), exact: true })
      .click();
    await page
      .getByRole('button', { name: copy('View models', '查看模型'), exact: true })
      .first()
      .click();
    await expect(manual).toHaveValue('draft-model');
    await page
      .locator('.nb-expandable-panel:not([hidden])')
      .getByRole('button', { name: copy('Close', '关闭'), exact: true })
      .click();
    await page
      .locator('summary')
      .filter({ hasText: copy('Request rewriting (advanced)', '请求改写（高级）') })
      .click();
    await shot('adaptation');
    await page.goto(`${USER_ORIGIN}/endpoints?quickstart=1`);
    const connectionPanel = page
      .locator('.nb-expandable-panel')
      .filter({ has: page.getByRole('heading', { name: c.addConnection, exact: true }) });
    await connectionPanel.getByLabel(c.name, { exact: true }).fill('Browser connection');
    await shot('step-1');
    await connectionPanel.getByRole('button', { name: c.create, exact: true }).click();
    await page.getByRole('button', { name: c.addEntry, exact: true }).first().click();
    const entryPanel = page
      .locator('.nb-expandable-panel')
      .filter({ has: page.getByRole('heading', { name: c.addEntry, exact: true }) });
    await entryPanel
      .getByRole('combobox', { name: c.protocol, exact: true })
      .selectOption({ label: locale === 'zh' ? 'OpenAI 兼容' : 'OpenAI-compatible' });
    await entryPanel.getByLabel(c.entryURL, { exact: true }).fill(endpoint.base_url);
    await entryPanel.getByRole('button', { name: c.saveEntries, exact: true }).click();
    await shot('manual-wizard');
    await entryPanel
      .getByRole('radio', { name: c.newCredential, exact: true })
      .locator('..')
      .click();
    await expect(
      entryPanel.getByRole('radio', { name: c.newCredential, exact: true }),
    ).toBeChecked();
    const secret = entryPanel.getByLabel(c.secret, { exact: true });
    await secret.fill('synthetic-setup-secret');
    await entryPanel.getByRole('checkbox', { name: c.ownership, exact: true }).check();
    await entryPanel.getByRole('button', { name: c.saveCredential, exact: true }).click();
    await expect(secret).toHaveCount(0);
    await entryPanel.getByRole('button', { name: c.authorizeEntry, exact: true }).click();
    await expect(entryPanel.getByText(c.grantSaved, { exact: true })).toBeVisible();
    await shot('step-2');
    await entryPanel.getByRole('button', { name: c.close, exact: true }).first().click();
    await page.getByRole('tab', { name: c.discoverySources, exact: true }).click();
    await page.getByRole('button', { name: c.addSource, exact: true }).click();
    const sourcePanel = page
      .locator('.nb-expandable-panel')
      .filter({ has: page.getByRole('heading', { name: c.addSource, exact: true }) });
    await sourcePanel.getByLabel(c.name + ' ' + c.optional, { exact: true }).fill('Shared catalog');
    await sourcePanel.getByLabel(c.discoveryBaseURL, { exact: true }).fill(endpoint.base_url);
    await sourcePanel
      .getByRole('table', { name: c.chooseCredential, exact: true })
      .getByRole('button', { name: c.select, exact: true })
      .click();
    failedRead = true;
    await sourcePanel.getByRole('button', { name: c.saveSource, exact: true }).click();
    await page.goto(`${USER_ORIGIN}/endpoints?discovery_source_id=51`);
    await page.getByRole('button', { name: c.refreshSource, exact: true }).click();
    await expect(page.locator('.nb-note--bad')).toBeVisible();
    await shot('read-failed');
    await page.getByRole('button', { name: c.refreshSource, exact: true }).click();
    await expect(page.locator('.nb-note--bad')).toHaveCount(0);
    expect(discoveryRefreshes).toBe(2);
    for (const name of ['model-a', 'model-b'])
      await page
        .getByRole('table', { name: c.catalog, exact: true })
        .getByRole('row')
        .filter({ hasText: name })
        .getByRole('button', { name: c.configureModel, exact: true })
        .click();
    const choose = page
      .locator('details')
      .filter({ has: page.locator(':scope > summary').filter({ hasText: c.chooseEntry }) })
      .first();
    await choose.locator(':scope > summary').click();
    await choose
      .getByRole('table', { name: c.chooseEntry, exact: true })
      .getByRole('row')
      .filter({ hasText: 'Browser service' })
      .getByRole('button', { name: c.select, exact: true })
      .click();
    await choose
      .getByRole('table', { name: c.chooseCredential, exact: true })
      .getByRole('row')
      .filter({ hasText: 'Primary key' })
      .getByRole('button', { name: c.select, exact: true })
      .click();
    for (const name of ['model-a', 'model-b']) {
      const row = page.locator('details').filter({
        has: page
          .locator(':scope > summary strong')
          .filter({ hasText: new RegExp('^' + name + '$') }),
      });
      await row.getByRole('checkbox', { name: new RegExp(name) }).check();
      await row.getByRole('checkbox', { name: nativeResponse, exact: true }).check();
      await row.getByRole('checkbox', { name: chatLabel, exact: true }).check();
    }
    await shot('step-3');
    await page
      .getByRole('button', { name: c.saveSelectedModels.replace('{{count}}', '2'), exact: true })
      .click();
    const rowA = page
      .locator('details')
      .filter({ has: page.locator(':scope > summary strong').filter({ hasText: /^model-a$/ }) });
    await rowA.getByRole('button', { name: c.existingModel, exact: true }).click();
    const existing = rowA.getByRole('table', { name: c.existingModel, exact: true });
    await rowA.getByRole('searchbox').fill('demo/model-a');
    await rowA.getByRole('button', { name: c.search, exact: true }).click();
    await existing.getByRole('button', { name: c.select, exact: true }).click();
    await shot('name-conflict');
    await page
      .getByRole('button', { name: c.retryUnfinishedModels.replace('{{count}}', '2'), exact: true })
      .click();
    await expect(
      page.getByRole('button', {
        name: c.retryUnfinishedModels.replace('{{count}}', '1'),
        exact: true,
      }),
    ).toBeEnabled();
    await shot('partial');
    await page
      .getByRole('button', { name: c.retryUnfinishedModels.replace('{{count}}', '1'), exact: true })
      .click();
    await expect(page.getByText(c.mappingSaved, { exact: true })).toHaveCount(2);
    await shot('complete');
    expect(writes.filter((write) => write.path === '/api/endpoints')).toHaveLength(1);
    expect(writes.filter((write) => write.path === '/api/connections/1/credentials')).toHaveLength(
      1,
    );
    expect(writes.filter((write) => write.path === '/api/endpoints/11/keys')).toHaveLength(1);
    expect(
      writes.filter((write) => write.path === '/api/models/51/bindings/batch-receipt'),
    ).toHaveLength(1);
    expect(
      writes.find((write) => write.path === '/api/connections/1/credentials')?.body
        .ownership_confirmed,
    ).toBe(true);
    expect(writes.every((write) => Boolean(write.identity))).toBe(true);
    await assertNoSensitiveBrowserPersistence(page, ['synthetic-setup-secret']);
    expect(errors).toEqual([]);
  });
}
