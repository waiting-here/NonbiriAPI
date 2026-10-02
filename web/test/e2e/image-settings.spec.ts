import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { taskFixture } from '../../src/shared/picturebook/fixtures';
import type {
  ImageModel,
  ImageTask,
  ParameterRule,
  SubmitInput,
} from '../../src/shared/picturebook/publicTypes';
import type { SizeCapability } from '../../src/shared/picturebook/capabilities';
import automaticModel from '../../src/admin/features/picturebook/fixtures/automatic-model.json' with { type: 'json' };

const prefix = '/admin/api/limited-activities/picture-book';
test.afterEach(async ({ context }, info) => {
  if (info.status === info.expectedStatus) return;
  const evidence = process.env.NONBIRI_PICTUREBOOK_EVIDENCE_DIR ?? '../tmp';
  await mkdir(evidence, { recursive: true });
  for (const [index, page] of context.pages().entries()) {
    const name =
      'failed-settings-' + info.title.match(/\d+px/)?.[0] + '-' + Date.now() + '-' + index;
    await page.screenshot({ path: resolve(evidence, name + '.png'), fullPage: true });
    await writeFile(resolve(evidence, name + '.txt'), await page.locator('body').innerText());
  }
});
const publicPrefix = '/api/limited-activities/picture-book';
const ids = [
  'imdl_AAAAAAAAAAAAAAAAAAAAAA',
  'imdl_BBBBBBBBBBBBBBBBBBBBBQ',
  'imdl_CCCCCCCCCCCCCCCCCCCCCg',
  'imdl_DDDDDDDDDDDDDDDDDDDDDw',
  'imdl_EEEEEEEEEEEEEEEEEEEEEA',
];
interface CatalogModel extends ImageModel {
  upstream_model_id: string;
  metadata: Record<string, unknown>;
  configured: boolean;
  enabled: boolean;
  parameter_capabilities: {
    key: string;
    source: string;
    support: string;
    overridden: boolean;
    conflict: boolean;
  }[];
  mapping: { model_pointer: string; parameters: Record<string, string>; constants: never[] };
  capability_revision: string;
  capability_readiness: string;
  capability_issues: { model_id: string; field_path: string; code: string; safe_message: string }[];
  catalog_type: string;
  missing: boolean;
}

function discoveredModel(index: number, size: SizeCapability): CatalogModel {
  const first = size.combinations?.[0];
  const rules: ParameterRule[] = [
    {
      key: 'prompt',
      supported: true,
      required: true,
      type: 'string',
      length_unit: 'utf16_units',
      min_length: 1,
      max_length: 128,
    },
    {
      key: 'negative_prompt',
      supported: true,
      required: false,
      type: 'string',
      length_unit: 'unicode_scalars',
      max_length: 128,
    },
    {
      key: 'n',
      supported: true,
      required: false,
      type: 'integer',
      minimum: 1,
      maximum: 4,
      default: 1,
    },
    {
      key: 'seed',
      supported: true,
      required: false,
      type: 'integer',
      minimum: 0,
      maximum: 2147483647,
    },
    {
      key: 'steps',
      supported: true,
      required: false,
      type: 'integer',
      minimum: 1,
      maximum: 8,
      default: 4,
    },
    {
      key: 'guidance',
      supported: true,
      required: false,
      type: 'number',
      enum: [1, 1.5, 2],
      default: 1.5,
    },
    {
      key: 'quality',
      supported: true,
      required: false,
      type: 'string',
      length_unit: 'utf8_bytes',
      enum: ['low', 'medium', 'high'],
      default: 'low',
    },
  ];
  if (size.mode === 'width_height' || first?.width)
    rules.push({
      key: 'size',
      supported: true,
      required: true,
      type: 'string',
      length_unit: 'utf8_bytes',
      default: first?.width ? `${first.width}x${first.height}` : '256x256',
    });
  if (first?.ratio)
    rules.push({
      key: 'aspect_ratio',
      supported: true,
      required: true,
      type: 'string',
      length_unit: 'utf8_bytes',
      default: first.ratio,
    });
  if (first?.resolution)
    rules.push({
      key: 'resolution',
      supported: true,
      required: true,
      type: 'string',
      length_unit: 'utf8_bytes',
      default: first.resolution,
    });
  return {
    id: ids[index],
    display_name: 'Synthetic studio ' + (index + 1),
    description: 'Synthetic image model',
    upstream_model_id: 'synthetic-image-' + (index + 1),
    metadata: {},
    configured: false,
    revision: '0',
    enabled: false,
    price: { paper: '0', brush: '0' },
    pricing_revision: '0',
    size_capability: size,
    parameters: index === 0 ? (automaticModel.parameters as ParameterRule[]) : rules,
    combinations: [],
    parameter_capabilities: (index === 0 ? automaticModel.parameters : rules).map((rule) => ({
      key: rule.key,
      source: 'discovered',
      support: 'supported',
      overridden: false,
      conflict: false,
    })),
    mapping: { model_pointer: '', parameters: {}, constants: [] },
    capability_revision: '0',
    capability_readiness: 'ready',
    capability_issues: [],
    catalog_type: 'image',
    missing: false,
  };
}

const catalog = () => [
  discoveredModel(0, {
    mode: 'resolution_ratio_grid',
    combinations: [
      { ratio: '1:1', resolution: 'compact', width: 512, height: 512, tier: 'compact' },
      { ratio: '16:9', resolution: 'compact', width: 768, height: 432, tier: 'compact' },
    ],
  }),
  discoveredModel(1, {
    mode: 'ratio_resolution',
    combinations: [{ ratio: '1:1' }, { ratio: '16:9' }],
  }),
  discoveredModel(2, {
    mode: 'ratio_size_map',
    combinations: [
      { ratio: '1:1', width: 256, height: 256 },
      { ratio: '16:9', width: 512, height: 288 },
    ],
  }),
  discoveredModel(3, {
    mode: 'width_height',
    width: { minimum: 256, maximum: 1024, step: 256 },
    height: { minimum: 256, maximum: 1024, step: 256 },
  }),
];
const activity = {
  key: 'picture-book',
  name: 'Picture book',
  cover_key: 'picture-book',
  visible: false,
  starts_at: null,
  ends_at: null,
  paused: false,
  revision: '1',
  status: 'unconfigured',
  module_config: {
    paper_price: '1',
    brush_price: '1',
    brush_cap: '100',
    brush_exchanged: '0',
    brush_remaining: '100',
  },
};
const adapter = {
  discovery: {
    method: 'GET',
    path: 'models?type=image',
    items_pointer: '/data',
    id_pointer: '/id',
  },
  submit: {
    method: 'POST',
    path: 'images/generations',
    mapping: { model_pointer: '/model', parameters: { prompt: '/prompt', n: '/n' }, constants: [] },
  },
  response: {
    images_pointer: '/images',
    base64_pointer: '',
    working_states: [],
    success_states: [],
    failure_states: [],
  },
};

for (const width of [1440, 390]) {
  test(
    'simple image service setup, authoritative refresh and all size modes at ' + width + 'px',
    async ({ page, context }) => {
      const errors = collectConsoleViolations(page);
      const evidence = process.env.NONBIRI_PICTUREBOOK_EVIDENCE_DIR ?? '../tmp';
      await mkdir(evidence, { recursive: true });
      await page.setViewportSize({ width, height: 1000 });
      await mockRoleSession(page, 'admin', 'admin');
      await mockPublicConfig(page, 'admin');
      await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
      let configured = false;
      let upstreamRevision = 1;
      let refreshes = 0;
      let modelRows: CatalogModel[] = [];
      const calls: { path: string; method: string; body: unknown }[] = [];
      const saves: {
        id: string;
        body: {
          expected_revision: string;
          enabled: boolean;
          price: ImageModel['price'];
          pricing: ImageModel['pricing'];
        };
      }[] = [];
      await page.route('**/admin/api/limited-activities/**', async (route) => {
        const request = route.request(),
          path = new URL(request.url()).pathname;
        calls.push({
          path,
          method: request.method(),
          body: request.postData() ? request.postDataJSON() : null,
        });
        if (path === prefix + '/upstream') {
          if (request.method() === 'PUT') {
            const body = request.postDataJSON();
            expect(Object.keys(body).sort()).toEqual(['base_url', 'expected_revision', 'secret']);
            expect(body.expected_revision).toBe(String(upstreamRevision));
            configured = true;
            upstreamRevision++;
            return route.fulfill({ json: { revision: String(upstreamRevision) } });
          }
          return route.fulfill({
            json: {
              revision: String(upstreamRevision),
              configured,
              base_url: configured ? 'https://images.example.invalid/v1' : '',
              secret_set: configured,
              rpm: configured ? 100 : null,
              concurrency: configured ? 2 : null,
              per_user_limit: 1,
              global_limit: 100,
              queue_timeout_seconds: 1800,
              execution_timeout_seconds: 1800,
              memory_budget_mib: 512,
              image_origins: [],
              adapter: configured ? adapter : null,
              control: null,
            },
          });
        }
        if (path === prefix + '/upstream/controls')
          return route.fulfill({ json: { data: [], next_cursor: null } });
        if (path === prefix + '/models/refresh') {
          refreshes++;
          if (!modelRows.length) modelRows = catalog();
          else
            modelRows.forEach((model) => {
              if (model.configured) {
                model.revision = String(Number(model.revision) + 1);
                model.capability_revision = model.revision;
                model.pricing_revision = model.revision;
              }
            });
          upstreamRevision++;
          return route.fulfill({
            json: {
              operation: {
                id: refreshes === 1 ? 'op_AAAAAAAAAAAAAAAAAAAAAA' : 'op_BBBBBBBBBBBBBBBBBBBBBQ',
                state: 'succeeded',
                created_at: 1700000000,
                completed_at: 1700000001,
                model_count: 4,
                error_code: null,
              },
            },
          });
        }
        if (path === prefix + '/models')
          return route.fulfill({
            json: {
              data: modelRows,
              total: modelRows.length,
              revision: '0',
              page: 1,
              page_size: 20,
            },
          });
        const model = modelRows.find((item) => path === prefix + '/models/' + item.id);
        if (model) {
          if (request.method() === 'PUT') {
            const body = request.postDataJSON();
            expect(Object.keys(body).sort()).toEqual([
              'enabled',
              'expected_revision',
              'price',
              'pricing',
            ]);
            expect(body.expected_revision).toBe(model.revision);
            saves.push({ id: model.id, body });
            model.price = body.price;
            model.pricing = body.pricing;
            model.enabled = body.enabled;
            model.configured = true;
            model.revision = String(Number(model.revision) + 1);
            model.capability_revision = model.revision;
            model.pricing_revision = model.revision;
            upstreamRevision++;
            return route.fulfill({
              json: {
                id: model.id,
                revision: model.revision,
                capability_revision: model.revision,
                pricing_revision: model.revision,
              },
            });
          }
          return route.fulfill({ json: model });
        }
        return route.fulfill({ json: activity });
      });
      await page.goto(ADMIN_ORIGIN + '/limited-activities');
      await expect(page.getByLabel(/JSON|pointer|RPM|Concurrent generation/)).toHaveCount(0);
      await page.getByLabel('Service base URL').fill('https://images.example.invalid/v1');
      await page.getByLabel('Activity key').fill('synthetic-image-key');
      await page.getByRole('button', { name: 'Save service settings' }).click();
      await expect(page.getByLabel('Activity key')).toHaveCount(0);
      await page.getByRole('button', { name: 'Refresh model catalog', exact: true }).click();
      await expect(page.getByRole('option', { name: /synthetic-image-1/ })).toBeAttached();
      const editor = page
        .getByRole('heading', { name: 'Model availability and pricing' })
        .locator('..');
      for (let index = 0; index < modelRows.length; index++) {
        await page.getByLabel('Choose a model to configure').selectOption(ids[index]);
        await editor.getByLabel('Sketch paper per image').fill('2');
        await editor.getByLabel('Brushes per image').fill('1');
        await editor.getByLabel('Make this model available').check();
        if (index === 0) {
          await editor.getByText('Optional size prices', { exact: true }).click();
          await editor.getByRole('button', { name: 'Add tier price' }).click();
          await editor.getByLabel('Tier sketch paper 1').fill('3');
          await editor.getByRole('button', { name: 'Add size price' }).click();
          await editor.getByLabel('Price width 1').fill('768');
          await editor.getByLabel('Price height 1').fill('432');
          await editor.getByLabel('Size sketch paper 1').fill('7');
          await editor.getByLabel('Size brushes 1').fill('0');
          await expect
            .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
            .toBe(true);
          await page.screenshot({ path: resolve(evidence, 'admin-prices-' + width + '.png') });
        }
        const readback = page.waitForResponse(
          (response) =>
            response.url().endsWith('/models/' + ids[index]) &&
            response.request().method() === 'GET',
        );
        await editor.getByRole('button', { name: 'Save model settings' }).click();
        await readback;
        await expect(editor.getByLabel('Sketch paper per image')).toBeEnabled();
      }
      await page.reload();
      await expect(page.getByRole('option', { name: /synthetic-image-1/ })).toBeAttached();
      await page.getByLabel('Choose a model to configure').selectOption(ids[0]);
      const initialCatalogIDs = modelRows.map((model) => model.id);
      const refreshed = page.waitForResponse(
        (response) =>
          response.url().endsWith('/models/' + ids[0]) && response.request().method() === 'GET',
      );
      await page.getByRole('button', { name: 'Refresh model catalog', exact: true }).click();
      await refreshed;
      await expect(editor.getByLabel('Sketch paper per image')).toBeEnabled();
      await expect(editor.getByLabel('Make this model available')).toBeChecked();
      await expect(editor.getByLabel('Tier sketch paper 1')).toHaveValue('3');
      await expect(editor.getByLabel('Size sketch paper 1')).toHaveValue('7');
      await editor.getByLabel('Sketch paper per image').fill('5');
      await editor.getByRole('button', { name: 'Save model settings' }).click();
      await expect.poll(() => saves.length).toBe(5);
      expect(saves[4].body.expected_revision).toBe('2');
      expect(modelRows.map((model) => model.id)).toEqual(initialCatalogIDs);
      await expect(page.getByRole('button', { name: 'Save service settings' })).toBeEnabled();
      await page.getByRole('button', { name: 'Save service settings' }).click();
      await expect
        .poll(
          () =>
            calls.filter((call) => call.path.endsWith('/upstream') && call.method === 'PUT').length,
        )
        .toBe(2);
      expect(calls.some((call) => /capability-profile|capabilities/.test(call.path))).toBe(false);
      expect(calls.filter((call) => /\/quote|\/tasks|\/models\/check/.test(call.path))).toEqual([]);
      await expect(page.getByLabel(/JSON|pointer/)).toHaveCount(0);
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({
        path: resolve(evidence, 'admin-settings-' + width + '.png'),
        fullPage: true,
      });
      errors.assertNone();

      const user = await context.newPage();
      const userErrors = collectConsoleViolations(user);
      await user.setViewportSize({ width, height: 1000 });
      await mockRoleSession(user, 'user', 'user');
      await mockPublicConfig(user, 'user');
      await user.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
      const submissions: SubmitInput[] = [];
      const tasks = new Map<string, ImageTask>();
      const taskReads = new Map<string, number>();
      const png = Buffer.from(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aSyAAAAAASUVORK5CYII=',
        'base64',
      );
      const publicModels = () =>
        modelRows
          .filter((model) => model.enabled)
          .map((model) => ({
            id: model.id,
            display_name: model.display_name,
            description: model.description,
            revision: model.revision,
            pricing_revision: model.pricing_revision,
            price: model.price,
            pricing: model.pricing,
            parameters: model.parameters,
            combinations: model.combinations,
            size_capability: model.size_capability,
          }));
      await user.route('**/api/limited-activities/picture-book**', async (route) => {
        const request = route.request(),
          path = new URL(request.url()).pathname;
        if (path === publicPrefix)
          return route.fulfill({ json: { ...activity, visible: true, status: 'open' } });
        if (path.endsWith('/wallet'))
          return route.fulfill({
            json: { general: '100', sketch_paper: '100', sketch_brush: '100' },
          });
        if (path.endsWith('/models'))
          return route.fulfill({ json: { data: publicModels(), next_cursor: null } });
        if (path.endsWith('/queue'))
          return route.fulfill({
            json: { queued: 0, running: 0, own: [], dispatch_paused: false },
          });
        if (path.endsWith('/quote') || (path.endsWith('/tasks') && request.method() === 'POST')) {
          const input: SubmitInput = request.postDataJSON();
          const model = modelRows.find((item) => item.id === input.model_id)!;
          expect(input.expected_model_revision).toBe(model.revision);
          expect(input.expected_pricing_revision).toBe(model.pricing_revision);
          const grid = input.model_id === ids[0];
          const unit = grid ? { paper: '7', brush: '0' } : { paper: '2', brush: '1' };
          const total = {
            paper: String(Number(unit.paper) * (input.n ?? 1)),
            brush: String(Number(unit.brush) * (input.n ?? 1)),
          };
          if (path.endsWith('/quote'))
            return route.fulfill({
              json: {
                model_revision: model.revision,
                pricing_revision: model.pricing_revision,
                effective_selection: { values: {}, selection: {} },
                unit,
                total,
                basis: grid ? 'size' : 'default',
                price_key: grid ? '768x432' : '',
              },
            });
          submissions.push(input);
          const id = model.id.replace('imdl_', 'img_');
          const task = taskFixture({
            id,
            model_id: model.id,
            n: input.n ?? 1,
            charge: total,
            status: grid ? 'queued' : 'succeeded',
            billing_state: grid ? 'reserved' : 'charged',
            queue_position: grid ? 1 : null,
            actual_images: grid ? 0 : 1,
            completed_at: grid ? null : 1700000010,
            result_available: !grid,
            result_expires_at: grid ? null : Math.floor(Date.now() / 1000) + 600,
            images: grid ? [] : [{ index: 0, mime: 'image/png', bytes: png.length }],
          });
          tasks.set(id, task);
          return route.fulfill({ json: { task } });
        }
        if (path.endsWith('/tasks'))
          return route.fulfill({ json: { data: [], next_cursor: null } });
        if (path.includes('/images/'))
          return route.fulfill({
            body: png,
            contentType: 'image/png',
            headers: { 'Content-Length': String(png.length) },
          });
        const id = path.split('/').at(-1)!;
        const task = tasks.get(id);
        if (task) {
          const reads = (taskReads.get(id) ?? 0) + 1;
          taskReads.set(id, reads);
          if (id === ids[0].replace('imdl_', 'img_')) {
            task.status = reads === 1 ? 'running' : 'succeeded';
            task.dispatched_at = 1700000001;
            task.queue_position = null;
            if (task.status === 'succeeded') {
              task.billing_state = 'charged';
              task.actual_images = 1;
              task.completed_at = 1700000010;
              task.result_available = true;
              task.result_expires_at = Math.floor(Date.now() / 1000) + 600;
              task.images = [{ index: 0, mime: 'image/png', bytes: png.length }];
            }
          }
          return route.fulfill({ json: task });
        }
        return route.fulfill({
          status: 400,
          json: { code: 'invalid_request', message: 'Unexpected synthetic image request.' },
        });
      });
      await user.goto(USER_ORIGIN + '/activities/picture-book');
      await expect(user.getByLabel('Resolution')).toHaveValue('compact');
      await expect(user.getByLabel('Resolution').locator('option')).toHaveCount(1);
      await user.getByLabel('Aspect ratio').selectOption('16:9');
      await user.getByLabel(/^Prompt/).fill('🐟'.repeat(65));
      await expect(user.getByLabel(/^Prompt/)).toHaveAttribute('maxlength', '128');
      await expect(user.getByLabel(/^Prompt/)).toHaveValue('🐟'.repeat(64));
      await user.getByLabel(/^Prompt/).fill('');
      await user.getByRole('button', { name: 'Reserve currency and join queue' }).click();
      expect(
        await user
          .getByLabel(/^Prompt/)
          .evaluate((input: HTMLTextAreaElement) => input.validity.valueMissing),
      ).toBe(true);
      expect(submissions).toHaveLength(0);
      await user.getByLabel(/^Prompt/).fill('🐟 Synthetic picture');
      await user.getByLabel('Image count').fill('2');
      await user.getByText('Advanced parameters', { exact: true }).click();
      await expect(user.getByLabel('Seed')).toHaveValue('');
      await expect(user.getByLabel('Steps')).toHaveValue('4');
      await expect(user.getByLabel('Guidance')).toHaveValue('1.5');
      await expect(user.getByLabel('Quality')).toHaveValue('low');
      await user.getByLabel('Guidance').selectOption('2');
      await user.getByLabel('Quality').selectOption('high');
      await expect(user.getByText('14 paper + 0 brushes')).toBeVisible();
      await user.evaluate(() => window.scrollTo(0, 0));
      await user.screenshot({
        path: resolve(evidence, 'user-selection-' + width + '.png'),
        fullPage: true,
      });
      await user.getByRole('button', { name: 'Reserve currency and join queue' }).click();
      await expect(user.getByText('Generating', { exact: true })).toBeVisible();
      await expect(user.getByText('Generated', { exact: true })).toBeVisible({ timeout: 10000 });
      expect(submissions[0]).toMatchObject({
        n: 2,
        aspect_ratio: '16:9',
        resolution: 'compact',
        size: '768x432',
        guidance: 2,
        quality: 'high',
        expected_model_revision: '3',
      });
      expect(submissions[0]).not.toHaveProperty('seed');
      await expect(user.getByRole('link', { name: 'Download original' })).toBeVisible();
      const download = user.waitForEvent('download');
      await user.getByRole('link', { name: 'Download original' }).click();
      expect(await (await download).failure()).toBeNull();
      for (const index of [1, 2, 3]) {
        await user.getByLabel('Image model').selectOption(ids[index]);
        await user.getByLabel(/^Prompt/).fill('Synthetic size selection');
        if (index === 3) {
          await user.getByLabel('Width').fill('512');
          await user.getByLabel('Height').fill('768');
        } else await user.getByLabel('Aspect ratio').selectOption('16:9');
        await expect(user.getByLabel('Resolution')).toHaveCount(0);
        await user.getByRole('button', { name: 'Reserve currency and join queue' }).click();
        await expect.poll(() => submissions.length).toBe(index + 1);
        expect(submissions[index]).not.toHaveProperty('resolution');
        expect(submissions[index]).not.toHaveProperty('seed');
        expect(submissions[index]).toMatchObject({ n: 1, steps: 4, guidance: 1.5, quality: 'low' });
        if (index === 1) expect(submissions[index]).not.toHaveProperty('size');
        if (index === 2) expect(submissions[index].size).toBe('512x288');
        if (index === 3) expect(submissions[index].size).toBe('512x768');
      }
      await expect
        .poll(() => user.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
        .toBe(true);
      await user.evaluate(() => window.scrollTo(0, 0));
      await user.screenshot({
        path: resolve(evidence, 'user-result-' + width + '.png'),
        fullPage: true,
      });
      userErrors.assertNone();
      await user.close();
    },
  );
}
