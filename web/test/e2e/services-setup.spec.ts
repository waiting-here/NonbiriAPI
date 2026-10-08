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

const endpoint = {
  id: '11',
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
    model_types: ['chat_completions', 'embeddings'],
    id,
    provider: 'demo',
    model: name,
    full_name: `demo/${name}`,
    route_strategy: 'ordered',
    silent_retry: false,
    transport_rule: 'passthrough',
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
        message.text() !==
          'Failed to load resource: the server responded with a status of 400 (Bad Request)'
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
      if (path === '/api/endpoint-create-options')
        return fulfill({
          base_connector_types: ['openai-compatible', 'anthropic-compatible'],
          mainstream_channels: [],
        });
      if (path === '/api/endpoints')
        return request.method() === 'POST'
          ? fulfill(endpoint, 201)
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
        if (!failedModel && input.model === 'model-b') {
          failedModel = true;
          return fulfill(
            { error: { code: 'invalid_request', message: 'Synthetic model failure' } },
            400,
          );
        }
        return fulfill(model('52', String(input.model)), 201);
      }
      if (path === '/api/models')
        return fulfill(
          numberedPage(
            url.searchParams.get('q') === 'demo/model-a' ? [model('51', 'model-a')] : [],
            url.searchParams,
          ),
        );
      if (/^\/api\/models\/\d+\/bindings(?:\/batch)?$/.test(path)) {
        const id = path.split('/')[3];
        if (input) bound.add(id);
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
          if (name === 'detail-3' && width === 390) expect(size.height).toBeLessThan(1800);
        }
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.evaluate(() => (document.documentElement.dataset.theme = 'light'));
      await writeFile(join(out, `metrics-${locale}.json`), JSON.stringify(metrics, null, 2));
    }
    await page.goto(`${USER_ORIGIN}/endpoints`);
    await expect(page.getByText('Browser service', { exact: true })).toBeVisible();
    await shot('list');
    await page.locator('.nb-filter summary').click();
    await shot('filter');
    empty = true;
    await page.reload();
    await expect(
      page.getByText(copy('No services added yet', '还没有添加服务'), { exact: true }),
    ).toBeVisible();
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
    await expect(page.locator('.services-list')).toHaveCount(0);
    await expect(page.locator('.nb-filter')).toHaveCount(0);
    await shot('step-1');
    await page
      .getByRole('button', {
        name: copy('Need advanced options? Set up manually', '需要设置请求头等高级选项？手动设置'),
        exact: true,
      })
      .click();
    await shot('manual-wizard');
    await page
      .locator('.nb-expandable-panel:not([hidden])')
      .getByRole('button', { name: copy('Close', '关闭'), exact: true })
      .click();
    await page.getByLabel(copy('Service URL', '服务地址'), { exact: true }).fill(endpoint.base_url);
    await page.getByRole('button', { name: copy('Next', '下一步'), exact: true }).click();
    await expect(page.getByLabel(copy('Key', '密钥'), { exact: true })).toBeVisible();
    await shot('step-2');
    await page.getByLabel(copy('Key', '密钥'), { exact: true }).fill('synthetic-setup-secret');
    await page
      .getByLabel(
        copy(
          'This is my key, or I have permission to use it',
          '这是我自己的密钥，或我已获得使用授权',
        ),
      )
      .check();
    failedRead = true;
    await page
      .getByRole('button', { name: copy('Save and load models', '保存并读取模型'), exact: true })
      .click();
    await expect(
      page.getByText(
        copy(
          'The model check did not complete. Try again or add a model name supplied by your provider.',
          '未能读取可用模型。可以重试，或按服务商提供的名称手动添加。',
        ),
        { exact: true },
      ),
    ).toBeVisible();
    await shot('read-failed');
    await page
      .getByRole('button', { name: copy('Retry loading models', '重试读取'), exact: true })
      .click();
    await expect(page.getByLabel('model-a', { exact: true })).toBeVisible();
    expect(discoveryRefreshes).toBe(2);
    await page.getByLabel(copy('Model name prefix', '模型名前缀'), { exact: true }).fill('demo');
    await page.getByLabel('model-a', { exact: true }).check();
    await page.getByLabel('model-b', { exact: true }).check();
    await shot('step-3');
    await page
      .getByRole('button', { name: copy('Add 2 models', '添加 2 个模型'), exact: true })
      .click();
    await expect(
      page.getByRole('radio', {
        name: copy('Add this source and keep existing settings', '追加此来源，保留原设置'),
        exact: true,
      }),
    ).toBeVisible();
    await shot('name-conflict');
    await page
      .getByRole('radio', {
        name: copy('Add this source and keep existing settings', '追加此来源，保留原设置'),
        exact: true,
      })
      .check();
    await page
      .getByRole('button', { name: copy('Add 2 models', '添加 2 个模型'), exact: true })
      .click();
    await expect(
      page.getByRole('button', {
        name: copy('Retry 1 unfinished models', '重试未完成的 1 个'),
        exact: true,
      }),
    ).toBeVisible();
    await shot('partial');
    await page
      .getByRole('button', {
        name: copy('Retry 1 unfinished models', '重试未完成的 1 个'),
        exact: true,
      })
      .click();
    await expect(
      page.getByRole('button', { name: copy('Done', '完成'), exact: true }),
    ).toBeVisible();
    await shot('complete');
    await page
      .getByRole('button', { name: copy('Add another key', '继续添加密钥'), exact: true })
      .click();
    await expect(page.getByLabel(copy('Key', '密钥'), { exact: true })).toHaveValue('');
    await expect(
      page.getByLabel(
        copy(
          'This is my key, or I have permission to use it',
          '这是我自己的密钥，或我已获得使用授权',
        ),
      ),
    ).toBeChecked();
    const keyWrite = writes.find((w) => w.path === '/api/endpoints/11/keys');
    expect(keyWrite?.body.ownership_confirmed).toBe(true);
    expect(keyWrite?.identity).toBeTruthy();
    expect(writes.some((w) => w.path.endsWith('/models/refresh'))).toBe(true);
    await assertNoSensitiveBrowserPersistence(page, ['synthetic-setup-secret']);
    expect(errors).toEqual([]);
  });
}
