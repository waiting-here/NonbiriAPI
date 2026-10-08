import { mkdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import type { Binding, BindingCandidate, Model } from '../../src/user/features/core/types';
import { numberedPage } from './numbered-fixtures';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test, type Page } from './test';

const cases = [
  { width: 1440, locale: 'en', theme: 'light', create: true },
  { width: 768, locale: 'zh', theme: 'dark', create: false },
  { width: 1440, locale: 'zh', theme: 'light', create: false },
  { width: 1440, locale: 'zh', theme: 'dark', create: false },
  { width: 390, locale: 'zh', theme: 'dark', create: false },
  { width: 390, locale: 'zh', theme: 'light', create: false },
] as const;
const candidate = (index: number): BindingCandidate => ({
  endpoint_key_id: index === 10 ? '112' : '111',
  endpoint_base_url: `https://service-${index === 10 ? 12 : 11}.example.test/v1`,
  connector_type: 'openai-compatible',
  endpoint_note: index === 10 ? 'Service B' : 'Service A',
  endpoint_key_display_head: 'sk-test',
  endpoint_key_display_tail: 'tail',
  endpoint_key_note: 'Primary key',
  upstream_model_id: `gpt-source-${index + 1}`,
  source_types: [index === 10 ? 'manual' : 'automatic'],
});
const original: Model = {
  model_types: ['chat_completions', 'embeddings'],
  id: '101',
  provider: 'team',
  model: 'assistant',
  full_name: 'team/assistant',
  route_strategy: 'ordered',
  transport_rule: 'passthrough',
  silent_retry: false,
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '1',
  binding_count: '25',
  role_policy: { default_action: 'native', rules: {} },
  created_at: 1700000000,
  updated_at: 1700000001,
};
async function picture(page: Page, name: string, fullPage = true) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (!process.env.NONBIRI_VISUAL_DIR) return;
  const dir = resolve(process.env.NONBIRI_VISUAL_DIR);
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: resolve(dir, `${name}.png`), fullPage });
}
async function fixture(page: Page, empty: boolean) {
  let model = { ...original, binding_count: empty ? '0' : '25' };
  let models: Model[] = empty
    ? []
    : [
        model,
        {
          ...original,
          id: '102',
          provider: 'work',
          full_name: 'work/assistant',
          binding_count: '0',
        },
        {
          ...original,
          id: '103',
          provider: 'long-prefix',
          model: 'long-model-name-for-responsiveness',
          full_name: 'long-prefix/long-model-name-for-responsiveness',
          binding_count: '1',
          route_strategy: 'random',
        },
      ];
  let bindings: Binding[] = empty
    ? []
    : Array.from({ length: 25 }, (_, i) => ({
        ...(({ source_types, ...fields }) => {
          void source_types;
          return fields;
        })(candidate(i)),
        id: String(201 + i),
        ord: i,
      }));
  const candidates = Array.from({ length: 11 }, (_, i) => candidate(i));
  const creates: Record<string, unknown>[] = [],
    patches: Record<string, unknown>[] = [];
  const orders: Record<string, unknown>[] = [],
    manual: Record<string, unknown>[] = [];
  const adds: Record<string, unknown>[] = [],
    reads: URL[] = [];
  const endpoints = ['11', '12'].map((id) => ({
    id,
    connector_type: 'openai-compatible',
    base_url: `https://service-${id}.example.test/v1`,
    origin: { kind: 'custom' },
    note: id === '11' ? 'Service A' : 'Service B',
    enabled: true,
    revision: '1',
    key_count: '1',
    created_at: 1700000000,
    updated_at: 1700000001,
  }));
  await page.route(USER_ORIGIN + '/api/**', async (route) => {
    const req = route.request(),
      url = new URL(req.url()),
      path = url.pathname;
    const input = () => req.postDataJSON() as Record<string, unknown>;
    const identity = () => expect(req.headers()['idempotency-key']).toBeTruthy();
    let body: unknown;
    if (path === '/api/caller-key') {
      await route.fulfill({ json: null, headers: { 'X-Nonbiri-CallerKey-Generation': '0' } });
      return;
    }
    if (path === '/api/models' && req.method() === 'POST') {
      identity();
      creates.push(input());
      model = { ...model, ...input(), full_name: 'team/assistant' };
      models = [model];
      body = model;
    } else if (path === '/api/models') body = numberedPage(models, url.searchParams);
    else if (path === '/api/models/101') {
      if (req.method() === 'PATCH') {
        identity();
        patches.push(input());
        const fields = { ...input() };
        delete fields.expected_revision;
        model = { ...model, ...fields, revision: String(Number(model.revision) + 1) };
        model.full_name = model.provider + '/' + model.model;
        models = models.map((row) => (row.id === model.id ? model : row));
      }
      body = model;
    } else if (path.endsWith('/bindings/order')) {
      identity();
      orders.push(input());
      const ids = input().order as string[];
      bindings = ids.map((id, ord) => ({ ...bindings.find((row) => row.id === id)!, ord }));
      body = { bindings, binding_revision: '2' };
    } else if (path.endsWith('/bindings/batch')) {
      identity();
      adds.push(input());
      body = { bindings, binding_revision: '2' };
    } else if (path.endsWith('/bindings'))
      body = { bindings, binding_revision: orders.length || adds.length ? '2' : '1' };
    else if (path.endsWith('/binding-candidates')) {
      reads.push(url);
      expect(url.searchParams.has('endpoint_key_id')).toBe(false);
      expect(url.searchParams.has('source_type')).toBe(false);
      let rows = candidates.filter((row) =>
        row.upstream_model_id.includes(url.searchParams.get('q') ?? ''),
      );
      if (url.searchParams.get('endpoint_id'))
        rows = rows.filter(
          (row) =>
            row.endpoint_note ===
            (url.searchParams.get('endpoint_id') === '11' ? 'Service A' : 'Service B'),
        );
      body = numberedPage(rows, url.searchParams);
    } else if (path === '/api/endpoints') body = numberedPage(endpoints, url.searchParams);
    else if (path.endsWith('/keys'))
      body = numberedPage(
        [
          {
            id: '111',
            endpoint_id: '11',
            display_head: 'sk-test',
            display_tail: 'tail',
            note: 'Primary key',
            enabled: true,
            force_store_false: false,
            max_concurrency: 0,
            max_rpm: 0,
            suspension_state: 'none',
            revision: '1',
            created_at: 1700000000,
            updated_at: 1700000001,
          },
        ],
        url.searchParams,
      );
    else if (path.endsWith('/models/manual')) {
      identity();
      manual.push(input());
      body = {
        entries: [
          {
            id: '301',
            source_type: 'manual',
            upstream_model_id: 'typed-model',
            provider: '',
            source_revision: '1',
            pair_revision: '1',
            created_at: 1700000000,
            updated_at: 1700000001,
          },
        ],
      };
    } else {
      await route.fallback();
      return;
    }
    await route.fulfill({ status: req.method() === 'POST' ? 201 : 200, json: body });
  });
  return { creates, patches, orders, manual, adds, reads };
}
for (const scenario of cases) {
  test(`model layout and source actions ${scenario.width} ${scenario.locale} ${scenario.theme}`, async ({
    page,
  }) => {
    const copy = JSON.parse(
      await readFile(resolve(process.cwd(), `src/user/i18n/${scenario.locale}.json`), 'utf8'),
    ).user;
    const core = (key: string) => copy.core[key] as string;
    const text = copy.models;
    const guard = collectConsoleViolations(page);
    await page.setViewportSize({ width: scenario.width, height: 900 });
    await page.addInitScript(({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    }, scenario);
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'level6');
    const state = await fixture(page, scenario.create);
    await page.goto(USER_ORIGIN + '/models');
    if (scenario.create) {
      await expect(page.getByText(text.emptyBody)).toBeVisible();
      await expect(page.getByRole('link', { name: text.addService })).toHaveAttribute(
        'href',
        '/endpoints?quickstart=1',
      );
      await picture(page, '1440-en-light-empty');
      await page
        .getByRole('button', { name: core('models.create'), exact: true })
        .first()
        .click();
      await page
        .locator('.model-editor')
        .getByRole('textbox', { name: core('models.provider'), exact: true })
        .fill('team');
      await page
        .getByRole('textbox', { name: core('models.model'), exact: true })
        .fill('assistant');
      await page
        .locator('.model-editor')
        .getByRole('button', { name: core('common.save'), exact: true })
        .click();
      await expect(page.getByTestId('model-source-search')).toBeFocused();
      expect(state.creates[0]).toMatchObject({
        model_types: ['chat_completions'],
        route_strategy: 'ordered',
        silent_retry: false,
        flatten_tool_calls: false,
        transport_rule: 'passthrough',
        role_policy: { default_action: 'native', rules: {} },
      });
      const results = page.locator('.model-source-results');
      await expect(results.locator('.model-source-result')).toHaveCount(10);
      await results.getByRole('button', { name: /Next|下一页/ }).click();
      await expect(results.getByText('gpt-source-11', { exact: true })).toBeVisible();
      await results.locator('.model-source-result').click();
      await page.locator('.model-service-filter summary').click();
      await page.getByRole('combobox', { name: text.service, exact: true }).selectOption('11');
      await expect(results.locator('.model-source-result')).toHaveCount(10);
      await expect.poll(() => state.reads.at(-1)?.searchParams.get('page')).toBe('1');
      await page.getByRole('button', { name: text.manual, exact: true }).click();
      await page.getByRole('combobox', { name: text.key, exact: true }).selectOption('111');
      await page.getByRole('textbox', { name: text.modelName, exact: true }).fill('typed-model');
      await page.getByRole('button', { name: core('endpoints.manualAdd'), exact: true }).click();
      await expect(page.getByText(text.manualSaved, { exact: true })).toBeVisible();
      expect(state.manual).toEqual([
        { entries: [{ upstream_model_id: 'typed-model', provider: '' }] },
      ]);
      expect(state.adds).toHaveLength(0);
      await page
        .getByRole('button', {
          name: core('models.addSelected').replace('{{count}}', '2'),
          exact: true,
        })
        .click();
      await expect.poll(() => state.adds.length).toBe(1);
      expect(state.adds[0]).toMatchObject({
        expected_binding_revision: '1',
        selections: [
          { endpoint_key_id: '112', upstream_model_id: 'gpt-source-11' },
          { endpoint_key_id: '111', upstream_model_id: 'typed-model' },
        ],
      });
      await picture(page, '1440-en-light-sources');
    } else {
      await expect(page.locator('.nb-table tbody tr')).toHaveCount(3);
      await picture(page, `${scenario.width}-zh-${scenario.theme}-list`);
      if (scenario.width === 768)
        await page.locator('.nb-table tbody tr').first().locator('[data-cell=meta]').click();
      else {
        const edit = page
          .locator('.nb-table tbody tr')
          .first()
          .getByRole('button', { name: core('models.editModel'), exact: true });
        await edit.focus();
        await page.keyboard.press('Enter');
      }
      const rows = page.locator('.core-binding-list .core-binding-row');
      await expect(rows).toHaveCount(20);
      await rows.first().getByRole('button').click();
      await page.getByRole('menuitem', { name: core('models.moveDown'), exact: true }).click();
      await expect(rows.first()).toContainText('gpt-source-2');
      await page.getByRole('button', { name: core('models.saveOrder'), exact: true }).click();
      await expect.poll(() => state.orders.length).toBe(1);
      expect(state.orders[0]).toEqual({
        expected_binding_revision: '1',
        order: ['202', '201', ...Array.from({ length: 23 }, (_, i) => String(203 + i))],
      });
      await picture(page, `${scenario.width}-zh-${scenario.theme}-sources`);
    }
    await page.getByRole('button', { name: core('models.editModel'), exact: true }).click();
    await page.locator('summary').filter({ hasText: text.advanced }).click();
    await page
      .locator('.model-editor')
      .getByRole('textbox', { name: core('models.provider'), exact: true })
      .fill('changed');
    await page
      .getByRole('radio', { name: core('models.random'), exact: true })
      .locator('..')
      .click();
    await page.getByRole('switch', { name: core('models.silentRetry'), exact: true }).click();
    await page.getByRole('switch', { name: core('models.flattenTools'), exact: true }).click();
    await page
      .getByRole('radio', {
        name: scenario.locale === 'en' ? 'Always fetch as a stream' : '总是用流式取回',
        exact: true,
      })
      .locator('..')
      .click();
    await picture(page, `${scenario.width}-${scenario.locale}-${scenario.theme}-advanced`);
    await page.getByRole('button', { name: core('common.save'), exact: true }).click();
    await expect.poll(() => state.patches.length).toBe(1);
    expect(state.patches[0]).toMatchObject({
      expected_revision: '1',
      provider: 'changed',
      route_strategy: 'random',
      silent_retry: true,
      flatten_tool_calls: true,
      transport_rule: 'force_stream',
    });
    await page.getByRole('button', { name: core('models.editModel'), exact: true }).click();
    const editor = page.locator('.model-editor');
    const types = editor.getByRole('group', { name: /Model types|模型类型/ });
    const chat = types.getByRole('checkbox', { name: /Chat completions|聊天补全/ });
    const embeddings = types.getByRole('checkbox', { name: /Embeddings|向量化/ });
    const images = types.getByRole('checkbox', { name: /Image generation|图像生成/ });
    await expect(chat).toBeChecked();
    await expect(embeddings).toBeChecked({ checked: !scenario.create });
    await images.check();
    await editor.getByRole('button', { name: core('common.cancel'), exact: true }).click();
    await page.getByRole('button', { name: core('models.editModel'), exact: true }).click();
    await expect(images).not.toBeChecked();
    await chat.uncheck();
    await embeddings.uncheck();
    await images.check();
    const advanced = editor
      .locator('.nb-fold')
      .filter({ has: page.locator('summary', { hasText: text.advanced }) });
    if (!(await advanced.evaluate((element) => (element as HTMLDetailsElement).open))) {
      await advanced.locator('summary').click();
    }
    await expect(
      editor.getByRole('switch', { name: core('models.silentRetry'), exact: true }),
    ).toBeVisible();
    await expect(
      editor.getByRole('switch', { name: core('models.flattenTools'), exact: true }),
    ).toHaveCount(0);
    await page
      .getByRole('radio', { name: core('models.cacheBalanced'), exact: true })
      .locator('..')
      .click();
    await types.scrollIntoViewIfNeeded();
    await picture(
      page,
      String(scenario.width) + '-' + scenario.locale + '-' + scenario.theme + '-image-types',
      false,
    );
    await editor.getByRole('button', { name: core('common.save'), exact: true }).click();
    await expect.poll(() => state.patches.length).toBe(2);
    expect(state.patches[1]).toMatchObject({
      model_types: ['images_generations'],
      route_strategy: 'cache_balanced',
    });
    await page.getByRole('button', { name: core('models.editModel'), exact: true }).click();
    await expect(images).toBeChecked();
    await expect(chat).not.toBeChecked();
    await expect(embeddings).not.toBeChecked();
    await expect(
      page.getByRole('radio', { name: core('models.cacheBalanced'), exact: true }),
    ).toBeChecked();
    guard.assertNone();
  });
}
